// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"latere.ai/x/pkg/errwriter"
	"latere.ai/x/topos/session"

	"latere.ai/x/limen"
	"latere.ai/x/limen/importer/topos"
	"latere.ai/x/limen/importer/topos/topostest"
)

// env is a getenv over a map, so no test reads the process's environment.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func runCLI(t *testing.T, stdin string, getenv func(string) string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut, getenv)
	return code, out.String(), errOut.String()
}

// history writes a Topos data directory in which one person, in confirm
// mode, approved every test run and denied every recursive delete.
func history(t *testing.T, n int) string {
	t.Helper()
	data := t.TempDir()
	var sessions []topostest.Session
	for s := range 4 {
		sess := topostest.Session{Initiator: "https://issuer.example|usr_42", AgentID: "agt_1"}
		for i := range n / 4 {
			risk := &session.Risk{Score: 0.5, Source: "rules/1"}
			sess.Calls = append(sess.Calls,
				topostest.Call{ToolUseID: fmt.Sprintf("t%d-%d", s, i), Name: "bash", Input: `{"command":"go test ./..."}`, Verdict: "ask", Mode: "confirm", Risk: risk, Answer: "allow"},
				topostest.Call{ToolUseID: fmt.Sprintf("d%d-%d", s, i), Name: "bash", Input: `{"command":"rm -rf build"}`, Verdict: "ask", Mode: "confirm", Risk: risk, Answer: "deny"},
			)
		}
		sessions = append(sessions, sess)
	}
	sessions[0].Calls = append(sessions[0].Calls, topostest.Call{ToolUseID: "old", Name: "bash"})
	topostest.Write(t, data, sessions...)
	return data
}

// The end to end path: a real Topos data directory, replayed, reported.
func TestReplayTopos(t *testing.T) {
	data := history(t, 80)
	code, out, errOut := runCLI(t, "", env(nil), "replay", "topos", "--data", data)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"Limen replayed 160 actions. You answered 160 of them.",
		"Limen skipped 1 action for these reasons:",
		"the log comes from a Topos version that did not record verdicts",
		"The verdicts that Limen would suggest:",
		"allow", "ask", "block",
		"Limen would allow, and you denied:",
		"The false allow rate of Limen:",
		"With Limen:",
		"In this history: 100.0% of the actions have your answer",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Limen has only ") {
		t.Errorf("warned about too few answers with 160:\n%s", out)
	}
}

func TestReplayToposJSON(t *testing.T) {
	data := history(t, 40)
	code, out, errOut := runCLI(t, "", env(map[string]string{"TOPOS_DATA_DIR": data}), "replay", "topos", "--json", "--org", "acme")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		Report  limen.ReplayReport `json:"report"`
		Skipped map[string]int     `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got.Report.Records != 80 || got.Report.Answered != 80 || got.Skipped[topos.ReasonNoVerdict] != 1 {
		t.Errorf("report %+v, skipped %v", got.Report, got.Skipped)
	}
	if got.Report.Would[limen.Allow] == 0 || got.Report.Would[limen.Block] == 0 {
		t.Errorf("Limen learned nothing from 80 answers: %+v", got.Report.Would)
	}
}

func TestFewAnswersWarns(t *testing.T) {
	data := t.TempDir()
	topostest.Write(t, data, topostest.Session{Initiator: "u", Calls: []topostest.Call{
		{ToolUseID: "a", Name: "bash", Verdict: "ask", Mode: "confirm", Answer: "allow"},
		{ToolUseID: "b", Name: "read", Verdict: "allow", Mode: "confirm"},
	}})
	code, out, _ := runCLI(t, "", env(nil), "replay", "topos", "--data", data)
	if code != exitOK || !strings.Contains(out, "Limen has only 1 answer from you.") || !strings.Contains(out, "Limen cannot measure it") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestEmptyStore(t *testing.T) {
	data := t.TempDir()
	if err := os.Mkdir(filepath.Join(data, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "", env(nil), "replay", "topos", "--data", data)
	if code != exitOK || !strings.Contains(out, "nothing to report") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestReplayFile(t *testing.T) {
	yes, no := true, false
	var lines []string
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range 40 {
		a := limen.Action{Org: "o", Subject: "s", ActionID: fmt.Sprintf("a%d", i), Kind: "tool", Name: "bash", Effect: limen.EffectWrite,
			Input: json.RawMessage(`{"command":"go test ./..."}`)}
		approve := &yes
		if i%5 == 0 {
			approve = &no
		}
		b, err := json.Marshal(limen.ReplayRecord{At: at.Add(time.Duration(i) * time.Minute), Action: a, Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1", Approve: approve})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b), "")
	}
	path := filepath.Join(t.TempDir(), "records.ndjson")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "", env(nil), "replay", "file", path)
	if code != exitOK || !strings.Contains(out, "Limen replayed 40 actions. You answered 40 of them.") {
		t.Errorf("exit %d: %s\n%s", code, errOut, out)
	}
	code, out, errOut = runCLI(t, strings.Join(lines, "\n"), env(nil), "replay", "file", "--json", "-")
	if code != exitOK || !strings.Contains(out, `"records": 40`) {
		t.Errorf("stdin: exit %d: %s\n%s", code, errOut, out)
	}
}

func TestReplayFileErrors(t *testing.T) {
	code, _, errOut := runCLI(t, "", env(nil), "replay", "file", filepath.Join(t.TempDir(), "missing.ndjson"))
	if code != exitFail || !strings.Contains(errOut, "cannot open") {
		t.Errorf("missing file: exit %d: %s", code, errOut)
	}
	code, _, errOut = runCLI(t, "{not json\n", env(nil), "replay", "file", "-")
	if code != exitFail || !strings.Contains(errOut, "line 1 is not a record") {
		t.Errorf("bad line: exit %d: %s", code, errOut)
	}
	code, _, errOut = runCLI(t, `{"at":"2026-09-01T00:00:00Z","action":{},"verdict":"ask","review_probability":1,"source":"x"}`+"\n", env(nil), "replay", "file", "-")
	if code != exitFail || !strings.Contains(errOut, "A replayed record has a missing field") || !strings.Contains(errOut, "records[0].action.org") {
		t.Errorf("invalid record: exit %d: %s", code, errOut)
	}
	long := `{"at":"` + strings.Repeat("x", 17*1024*1024) + `"}`
	code, _, errOut = runCLI(t, long, env(nil), "replay", "file", "-")
	if code != exitFail || errOut == "" {
		t.Errorf("an oversized line: exit %d: %s", code, errOut)
	}
}

func TestUsage(t *testing.T) {
	data := history(t, 4)
	cases := []struct {
		args []string
		code int
		want string
	}{
		{nil, exitUsage, "Usage:"},
		{[]string{"nope"}, exitUsage, `unknown command "nope"`},
		{[]string{"replay"}, exitUsage, "give the source to replay"},
		{[]string{"replay", "slack"}, exitUsage, `"slack" is not a known source`},
		{[]string{"replay", "topos", "--data", data, "extra"}, exitUsage, `unexpected argument "extra"`},
		{[]string{"replay", "topos", "--bogus"}, exitUsage, "flag provided but not defined"},
		{[]string{"replay", "topos", "--data", data, "--keep"}, exitUsage, "--keep needs --store"},
		{[]string{"replay", "topos", "--data", filepath.Join(t.TempDir(), "none")}, exitFail, "there are no Topos sessions"},
		{[]string{"replay", "file"}, exitUsage, "give one file of records"},
		{[]string{"replay", "file", "--bogus"}, exitUsage, "flag provided but not defined"},
		{[]string{"replay", "topos"}, exitUsage, "there is no Topos data directory"},
	}
	for _, c := range cases {
		code, out, errOut := runCLI(t, "", env(nil), c.args...)
		if code != c.code || !strings.Contains(out+errOut, c.want) {
			t.Errorf("%v: exit %d, want %d with %q:\n%s%s", c.args, code, c.code, c.want, out, errOut)
		}
	}
	for _, help := range [][]string{{"help"}, {"-h"}, {"replay", "topos", "-h"}, {"replay", "file", "-h"}} {
		if code, _, _ := runCLI(t, "", env(nil), help...); code != exitOK {
			t.Errorf("%v: exit %d", help, code)
		}
	}
	if code, out, _ := runCLI(t, "", env(nil), "version"); code != exitOK || !strings.HasPrefix(out, "limen ") {
		t.Errorf("version: exit %d: %q", code, out)
	}
}

func TestToposDataDir(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"TOPOS_DATA_DIR": "/d", "XDG_STATE_HOME": "/x", "HOME": "/h"}, "/d"},
		{map[string]string{"XDG_STATE_HOME": "/x", "HOME": "/h"}, filepath.Join("/x", "topos")},
		{map[string]string{"HOME": "/h"}, filepath.Join("/h", ".local", "state", "topos")},
		{map[string]string{"USERPROFILE": "/w"}, filepath.Join("/w", ".local", "state", "topos")},
		{nil, ""},
	}
	for _, c := range cases {
		if got := toposDataDir(env(c.env)); got != c.want {
			t.Errorf("%v: %q, want %q", c.env, got, c.want)
		}
	}
}

func TestReportPieces(t *testing.T) {
	if share(1, 0) != "0.0%" || pct(0.123) != "12.3%" || total(map[string]int{"a": 2, "b": 3}) != 5 || plural(2, "a", "as") != "as" {
		t.Error("formatting helpers")
	}
	var buf bytes.Buffer
	v := 0.02
	writeReport(errwriter.New(&buf), limen.ReplayReport{Records: 10, Answered: 40, Would: map[limen.Verdict]int{limen.Allow: 10},
		FalseAllow: limen.Estimate{Value: &v, Covered: 0.5, Reviews: 9}, Kept: true}, nil)
	out := buf.String()
	for _, want := range []string{"2.0% of its allows (95% interval: 0.0% to 100.0%)", "50.0% of its allows", "Limen added this history to its ledger"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	buf.Reset()
	printError(errwriter.New(&buf), &source{}, fmt.Errorf("plain failure"))
	if !strings.Contains(buf.String(), "plain failure") {
		t.Errorf("plain error: %q", buf.String())
	}
}

// failing is a writer whose every write fails, as a closed pipe does.
type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }

// Output that cannot be written fails the command.
func TestClosedStdout(t *testing.T) {
	if code := run([]string{"version"}, strings.NewReader(""), failing{}, io.Discard, env(nil)); code != exitFail {
		t.Errorf("a failed write reported exit %d", code)
	}
	data := history(t, 4)
	if code := run([]string{"replay", "topos", "--data", data, "--json"}, strings.NewReader(""), failing{}, io.Discard, env(nil)); code != exitFail {
		t.Errorf("a failed JSON write reported exit %d", code)
	}
}

// A kept replay lands in the ledger on disk: replaying the same history into
// it again finds every action already decided.
func TestKeepIntoAStore(t *testing.T) {
	data := history(t, 4)
	ledger := t.TempDir()
	if code, out, errOut := runCLI(t, "", env(nil), "replay", "topos", "--data", data, "--store", ledger, "--keep"); code != exitOK {
		t.Fatalf("keep: exit %d:\n%s%s", code, out, errOut)
	}
	code, out, errOut := runCLI(t, "", env(nil), "replay", "topos", "--data", data, "--store", ledger, "--keep")
	if code != exitFail || !strings.Contains(out+errOut, "already has a decision") {
		t.Errorf("a second kept replay: exit %d:\n%s%s", code, out, errOut)
	}
}
