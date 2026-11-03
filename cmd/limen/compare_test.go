// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/pkg/errwriter"

	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store/file"
)

// publishRecords is the synthetic history of spec 011's replay comparison.
var publishRecords = filepath.Join("..", "..", "engine", "testdata", "compare", "publish.ndjson")

// The comparison of the synthetic history, end to end (spec 011,
// criterion 21): learned/2 withholds one allow and gives three, each on an
// action that considered approvals cover.
func TestCompareFile(t *testing.T) {
	code, out, errOut := runCLI(t, "", env(nil), "compare", "file", publishRecords)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"Limen compared learned/1 and learned/2 on 39 actions of 3 persons. 39 of the actions have an answer.",
		"The verdicts that each model would suggest:",
		"allow -> ask (short)",
		"Allows of learned/1 that learned/2 does not give: 1",
		"Allows of learned/2 that learned/1 does not give: 3",
		"Verdicts of learned/2 that permit more than the verdicts of learned/1: 3",
		"Of these, verdicts that the cover rule does not hold: 0",
		"The Brier score of each model, from 39 answers",
		"example/considered  ses-0/10  publish: update  ask (split) 0.821 -> allow 0.904  covered",
		"For each person:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the comparison lacks %q:\n%s", want, out)
		}
	}
	code, out, errOut = runCLI(t, "", env(nil), "compare", "file", "--json", publishRecords)
	var got struct {
		Comparison engine.Comparison `json:"comparison"`
	}
	if code != exitOK || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("JSON: exit %d: %s\n%s", code, errOut, out)
	}
	if o := got.Comparison.Overall; o.Records != 39 || o.Looser != 3 || o.Outside != 0 || o.Withheld != 1 || len(got.Comparison.Persons) != 3 {
		t.Errorf("JSON comparison %+v", o)
	}
}

// A history of Topos compares the same from its logs and from the ledger a
// kept replay wrote, which keeps no input.
func TestCompareToposAndLedger(t *testing.T) {
	data := history(t, 40)
	code, fromLogs, errOut := runCLI(t, "", env(nil), "compare", "topos", "--data", data, "--json")
	if code != exitOK {
		t.Fatalf("topos: exit %d: %s", code, errOut)
	}
	ledger := t.TempDir()
	if code, out, errOut := runCLI(t, "", env(nil), "replay", "topos", "--data", data, "--store", ledger, "--keep"); code != exitOK {
		t.Fatalf("keep: exit %d:\n%s%s", code, out, errOut)
	}
	code, fromLedger, errOut := runCLI(t, "", env(nil), "compare", "ledger", "--store", ledger, "--json")
	if code != exitOK {
		t.Fatalf("ledger: exit %d: %s", code, errOut)
	}
	var a, b struct {
		Comparison engine.Comparison `json:"comparison"`
	}
	if err := json.Unmarshal([]byte(fromLogs), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fromLedger), &b); err != nil {
		t.Fatal(err)
	}
	ao, bo := a.Comparison.Overall, b.Comparison.Overall
	if ao.Records != 80 || bo.Records != ao.Records || bo.Answered != ao.Answered || bo.Looser != ao.Looser ||
		*bo.BrierBase != *ao.BrierBase || *bo.BrierNext != *ao.BrierNext || len(b.Comparison.Changed) != len(a.Comparison.Changed) {
		t.Errorf("the logs and the ledger compare otherwise:\n%+v\n%+v", ao, bo)
	}
	org, subject := b.Comparison.Persons[0].Org, b.Comparison.Persons[0].Subject
	code, out, errOut := runCLI(t, "", env(nil), "compare", "ledger", "--store", ledger, "--org", org, "--subject", subject)
	if code != exitOK || !strings.Contains(out, "on 80 actions of 1 person.") {
		t.Errorf("one person of the ledger: exit %d: %s\n%s", code, errOut, out)
	}
	code, out, _ = runCLI(t, "", env(nil), "compare", "ledger", "--store", ledger, "--org", "nobody")
	if code != exitOK || !strings.Contains(out, "There is nothing to compare yet.") {
		t.Errorf("an organization with no case: exit %d:\n%s", code, out)
	}
	// limend holds the lock of its ledger: the comparison asks for a copy.
	held, err := file.Open(ledger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if code, _, errOut := runCLI(t, "", env(nil), "compare", "ledger", "--store", ledger); code != exitFail || !strings.Contains(errOut, "locked") {
		t.Errorf("a locked ledger: exit %d: %s", code, errOut)
	}
}

// A replay can run learned/2, and refuses a model it does not know.
func TestReplayModel(t *testing.T) {
	code, out, errOut := runCLI(t, "", env(nil), "replay", "file", "--model", "learned/2", "--json", publishRecords)
	if code != exitOK || !strings.Contains(out, `"records": 39`) {
		t.Errorf("learned/2: exit %d: %s\n%s", code, errOut, out)
	}
	code, _, errOut = runCLI(t, "", env(nil), "replay", "file", "--model", "learned/7", publishRecords)
	if code != exitUsage || !strings.Contains(errOut, "--model is not correct") {
		t.Errorf("an unknown model: exit %d: %s", code, errOut)
	}
}

func TestCompareUsage(t *testing.T) {
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"compare"}, exitUsage, "give the source to compare"},
		{[]string{"compare", "slack"}, exitUsage, `"slack" is not a known source`},
		{[]string{"compare", "ledger"}, exitUsage, "give the ledger directory with --store"},
		{[]string{"compare", "ledger", "--store", t.TempDir(), "extra"}, exitUsage, `unexpected argument "extra"`},
		{[]string{"compare", "ledger", "--bogus"}, exitUsage, "flag provided but not defined"},
		{[]string{"compare", "file"}, exitUsage, "limen compare file: give one file of records"},
		{[]string{"compare", "file", filepath.Join(t.TempDir(), "missing.ndjson")}, exitFail, "cannot open"},
		{[]string{"compare", "topos", "--data", filepath.Join(t.TempDir(), "none")}, exitFail, "limen compare: there are no Topos sessions"},
		{[]string{"compare", "file", "--store", filepath.Join(t.TempDir(), "x", "\x00"), publishRecords}, exitFail, "limen compare:"},
	}
	for _, c := range cases {
		code, out, errOut := runCLI(t, "", env(nil), c.args...)
		if code != c.code || !strings.Contains(out+errOut, c.want) {
			t.Errorf("%v: exit %d, want %d with %q:\n%s%s", c.args, code, c.code, c.want, out, errOut)
		}
	}
	for _, help := range [][]string{{"compare", "ledger", "-h"}, {"compare", "file", "-h"}} {
		if code, _, _ := runCLI(t, "", env(nil), help...); code != exitOK {
			t.Errorf("%v: exit %d", help, code)
		}
	}
	code, out, _ := runCLI(t, "", env(nil), "compare", "file", "-")
	if code != exitOK || !strings.Contains(out, "There is nothing to compare yet.") {
		t.Errorf("no records: exit %d:\n%s", code, out)
	}
	code, _, errOut := runCLI(t, "{not json\n", env(nil), "compare", "file", "-")
	if code != exitFail || !strings.Contains(errOut, "limen compare: line 1 is not a record") {
		t.Errorf("a bad line: exit %d: %s", code, errOut)
	}
	if code := run([]string{"compare", "file", "--json", publishRecords}, strings.NewReader(""), failing{}, io.Discard, env(nil)); code != exitFail {
		t.Errorf("a failed JSON write reported exit %d", code)
	}
}

// The parts of a comparison that a history of records does not show: the
// verdicts a ledger recorded, a cover that differs, skipped actions, and a
// person with no answer.
func TestComparisonPieces(t *testing.T) {
	brier := 0.1
	c := engine.Comparison{Base: engine.Learned1, Next: engine.Learned2,
		Overall: engine.Tally{Records: 3, Answered: 1, Base: map[string]int{"block": 1, "allow": 1, "ask (few)": 1}, Next: map[string]int{"ask (short)": 2, "allow": 1},
			Changes: map[string]int{"block -> ask (short)": 1}, Looser: 1, Outside: 1, CoverDiffers: 1, BrierBase: &brier, BrierNext: &brier,
			Recorded: map[string]int{"allow": 2}, Agree: 1},
		Persons: []engine.Tally{{Org: "o", Subject: "s", Records: 3}},
		Changed: []engine.Change{{Org: "o", Subject: "s", ActionID: "a", Label: "bash: rm -r", Looser: true, Outside: true,
			Base: engine.Suggestion{Kind: "block"}, Next: engine.Suggestion{Kind: "ask (short)"}}}}
	var buf bytes.Buffer
	writeComparison(errwriter.New(&buf), c, map[string]int{"old": 2})
	out := buf.String()
	for _, want := range []string{
		"Limen skipped 2 actions.",
		"Of these, verdicts that the cover rule does not hold: 1",
		"Actions whose cover is different between the two models: 1.",
		"learned/1 suggests the verdict that the ledger recorded for 1 of 2 predictions.",
		"block 0.000 -> ask (short) 0.000  not held by the cover rule",
		"o/s     3        0        0                    0                    0            0         -                -",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if k := kinds(map[string]int{"block": 1, "ask (b)": 1}, map[string]int{"allow": 1, "ask (a)": 1, "ask (b)": 2}); strings.Join(k, ",") != "allow,ask (a),ask (b),block" {
		t.Errorf("kinds %v", k)
	}
}
