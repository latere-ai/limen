// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"latere.ai/x/topos/session"

	"latere.ai/x/limen"
	"latere.ai/x/limen/importer/topos"
	"latere.ai/x/limen/importer/topos/topostest"
)

func collect(t *testing.T, dataDir string, o topos.Options) ([]limen.ReplayRecord, error) {
	t.Helper()
	var out []limen.ReplayRecord
	for r, err := range topos.Records(dataDir, o) {
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

func byID(rs []limen.ReplayRecord) map[string]limen.ReplayRecord {
	out := map[string]limen.ReplayRecord{}
	for _, r := range rs {
		_, id, _ := cut(r.Action.ActionID)
		out[id] = r
	}
	return out
}

func cut(s string) (string, string, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[:i], s[i+1:], true
		}
	}
	return "", s, false
}

func TestRecords(t *testing.T) {
	data := t.TempDir()
	risk := &session.Risk{Score: 0.5, Source: "rules/1", Features: []string{"bash"}}
	ids := topostest.Write(t, data,
		topostest.Session{Initiator: "https://issuer.example|usr_42", AgentID: "agt_1", Calls: []topostest.Call{
			{ToolUseID: "ask-allow", Name: "bash", Input: `{"command":"go test ./..."}`, Verdict: "ask", Mode: "confirm", Risk: risk, Answer: "allow"},
			{ToolUseID: "ask-deny", Name: "bash", Input: `{"command":"rm -rf build"}`, Verdict: "ask", Mode: "confirm", Risk: risk, Answer: "deny"},
			{ToolUseID: "allow", Name: "read", Input: `{"path":"a.go"}`, Verdict: "allow", Mode: "confirm"},
			{ToolUseID: "block", Name: "web_fetch", Verdict: "block", Mode: "plan"},
			{ToolUseID: "flag", Name: "edit", Verdict: "flag", Mode: "progressive", Answer: "allow"},
			{ToolUseID: "remember", Name: "bash", Input: `{"command":"make"}`, Verdict: "ask", Mode: "confirm", Answer: "allow", Remember: "bash:make*"},
			{ToolUseID: "mcp", Name: "mcp__github__merge_pull_request", Verdict: "ask", Mode: "confirm"},
		}},
		topostest.Session{Initiator: "https://issuer.example|usr_7", Machine: session.MachineCella, Org: "acme", Calls: []topostest.Call{
			{ToolUseID: "c1", Name: "write", Verdict: "allow", Mode: "confirm"},
		}},
	)
	rs, err := collect(t, data, topos.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 8 {
		t.Fatalf("%d records, want 8", len(rs))
	}
	got := byID(rs)
	yes, no := true, false
	cases := []struct {
		id      string
		verdict limen.Verdict
		prob    float64
		source  string
		approve *bool
		effect  limen.Effect
	}{
		{"ask-allow", limen.Ask, 1, "topos/confirm", &yes, limen.EffectWrite},
		{"ask-deny", limen.Ask, 1, "topos/confirm", &no, limen.EffectWrite},
		{"allow", limen.Allow, 0, "topos/confirm", nil, limen.EffectRead},
		{"block", limen.Block, 0, "topos/plan", nil, limen.EffectExternal},
		{"flag", limen.Flag, 1, "topos/progressive", &yes, limen.EffectWrite},
		{"remember", limen.Ask, 1, "topos/confirm", &yes, limen.EffectWrite},
		{"mcp", limen.Ask, 1, "topos/confirm", nil, limen.EffectWrite},
	}
	for _, c := range cases {
		r, ok := got[c.id]
		if !ok {
			t.Errorf("%s: no record", c.id)
			continue
		}
		if r.Verdict != c.verdict || r.ReviewProbability != c.prob || r.Source != c.source || r.Action.Effect != c.effect {
			t.Errorf("%s: %+v", c.id, r)
		}
		if (r.Approve == nil) != (c.approve == nil) || (r.Approve != nil && *r.Approve != *c.approve) {
			t.Errorf("%s: approve %v, want %v", c.id, r.Approve, c.approve)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", c.id, err)
		}
	}
	first := got["ask-allow"]
	if first.Action.Key != (limen.Key{Org: topos.DefaultOrg, Subject: "https://issuer.example|usr_42", ActionID: ids[0] + "/ask-allow"}) {
		t.Errorf("key %+v", first.Action.Key)
	}
	if first.Action.Actor == nil || first.Action.Actor.ID != "agt_1" || first.Action.Actor.Session != ids[0] {
		t.Errorf("actor %+v", first.Action.Actor)
	}
	if env := first.Action.Environment; env == nil || env.Kind != "host" || !env.Credentialed {
		t.Errorf("host environment %+v", env)
	}
	if len(first.Action.Signals) != 1 || first.Action.Signals[0].Source != "rules/1" || *first.Action.Signals[0].Score != 0.5 {
		t.Errorf("signals %+v", first.Action.Signals)
	}
	var in map[string]string
	if json.Unmarshal(first.Action.Input, &in) != nil || in["command"] != "go test ./..." {
		t.Errorf("input %s", first.Action.Input)
	}
	if first.At.IsZero() || !first.At.After(topostest.Start) {
		t.Errorf("time %v", first.At)
	}
	sandboxed := got["c1"]
	if sandboxed.Action.Org != "acme" || sandboxed.Action.Environment.Kind != "sandbox" || sandboxed.Action.Environment.Credentialed {
		t.Errorf("sandboxed %+v", sandboxed.Action)
	}
	if got["c1"].Action.Actor.ID != "coder" {
		t.Errorf("an agent with no id is named: %+v", got["c1"].Action.Actor)
	}
}

func TestOverrides(t *testing.T) {
	data := t.TempDir()
	topostest.Write(t, data,
		topostest.Session{Initiator: "u", Org: "acme", Calls: []topostest.Call{{ToolUseID: "a", Name: "bash", Verdict: "ask", Mode: "confirm"}}},
		topostest.Session{Calls: []topostest.Call{{ToolUseID: "b", Name: "bash", Verdict: "ask"}}},
	)
	rs, err := collect(t, data, topos.Options{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Action.Org != "o" || r.Action.Subject != "s" {
			t.Errorf("override not applied: %+v", r.Action.Key)
		}
	}
	rs, err = collect(t, data, topos.Options{})
	if err != nil {
		t.Fatal(err)
	}
	subjects := []string{rs[0].Action.Subject, rs[1].Action.Subject}
	if !slices.Contains(subjects, topos.DefaultSubject) {
		t.Errorf("a session with no initiator is not the default subject: %v", subjects)
	}
	for _, r := range rs {
		if r.Action.ActionID[len(r.Action.ActionID)-1] == 'b' && r.Source != "topos" {
			t.Errorf("a call with no mode has source %q", r.Source)
		}
	}
}

// Topos v0.66.0 and later send a person's confirmation of a network
// approval to a decision service as an answer to the call whose connection
// raised it, and the importer reads it so. For a call Topos showed, the
// record's answer is the first, its review; for a call that ran unseen, the
// last, the label Limen learns. A message in place of a confirmation, an
// approval nothing waited for, and a confirmation of an approval that names
// no call or no approval give no answer.
func TestApprovalAnswers(t *testing.T) {
	data := t.TempDir()
	rate := 0.05
	topostest.Write(t, data, topostest.Session{
		Initiator: "u",
		Raw:       []topostest.Raw{{Type: session.TypeUserToolConfirmation, Payload: `{"approval_id":"apr_unknown","decision":"allow"}`}},
		Calls: []topostest.Call{
			{ToolUseID: "ran", Name: "bash", Verdict: "allow", Mode: "confirm", Approvals: []topostest.Approval{
				{Host: "a.example.com", Answer: "allow"}, {Host: "b.example.com", Answer: "deny"},
			}},
			{ToolUseID: "asked", Name: "bash", Verdict: "ask", Mode: "confirm", Answer: "allow", Approvals: []topostest.Approval{
				{Host: "c.example.com", Answer: "deny"},
			}},
			{ToolUseID: "flagged", Name: "bash", Verdict: "flag", Mode: "progressive", ReviewProbability: &rate, Approvals: []topostest.Approval{
				{Host: "d.example.com", Answer: "deny"},
			}},
			{ToolUseID: "unanswered", Name: "bash", Verdict: "allow", Mode: "confirm", Approvals: []topostest.Approval{
				{Host: "e.example.com", Message: "Use the mirror."}, {Host: "f.example.com", Verdict: "block", Answer: "allow"}, {Host: "g.example.com"},
			}},
			{ToolUseID: "unsettled", Name: "bash", Verdict: "allow", Mode: "confirm", Approvals: []topostest.Approval{
				{Host: "h.example.com", Answer: "deny", Unsettled: true},
			}},
			{ToolUseID: "", Name: "bash", Verdict: "ask", Approvals: []topostest.Approval{{Host: "i.example.com", Answer: "allow"}}},
		},
	})
	rs, err := collect(t, data, topos.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 6 {
		t.Fatalf("%d records, want 6", len(rs))
	}
	got := byID(rs)
	yes, no := true, false
	for id, want := range map[string]*bool{"ran": &no, "asked": &yes, "flagged": &no, "unanswered": nil, "unsettled": &no, "": nil} {
		r, ok := got[id]
		if !ok {
			t.Errorf("%q: no record", id)
			continue
		}
		if (r.Approve == nil) != (want == nil) || (r.Approve != nil && *r.Approve != *want) {
			t.Errorf("%q: approve %v, want %v", id, r.Approve, want)
		}
	}
}

// Calls that cannot become a record are skipped with their reason, and
// unknown or malformed events do not stop the import.
func TestSkips(t *testing.T) {
	data := t.TempDir()
	topostest.Write(t, data, topostest.Session{
		Initiator: "u",
		Raw: []topostest.Raw{
			{Type: "custom.event", Payload: `{"x":1}`},
			{Type: session.TypeAgentToolUse, Payload: `{"tool_use_id":"bad","name":5}`},
			{Type: session.TypeUserToolConfirmation, Payload: `{"tool_use_id":7}`},
			{Type: session.TypeUserToolConfirmation, Payload: `{"tool_use_id":"odd","decision":"maybe"}`},
		},
		Calls: []topostest.Call{
			{ToolUseID: "old", Name: "bash"},
			{ToolUseID: "weird", Name: "bash", Verdict: "deny"},
			{ToolUseID: "secret", Name: "bash", Verdict: "ask", Redact: true},
			{ToolUseID: "noname", Name: "", Verdict: "ask"},
			{ToolUseID: "badscore", Name: "bash", Verdict: "allow", Risk: &session.Risk{Score: 7, Source: "rules/1"}},
			{ToolUseID: "odd", Name: "bash", Verdict: "ask", Answer: "allow"},
			// A scripted model that restarts in a new process repeats an ID.
			{ToolUseID: "odd", Name: "bash", Verdict: "allow"},
		},
	})
	var skips []topos.Skip
	rs, err := collect(t, data, topos.Options{Skipped: func(s topos.Skip) { skips = append(skips, s) }})
	if err != nil {
		t.Fatal(err)
	}
	type skip struct{ id, reason string }
	var seen []skip
	for _, s := range skips {
		seen = append(seen, skip{s.ToolUseID, s.Reason})
	}
	for _, want := range []skip{
		{"", topos.ReasonUndecodable},
		{"old", topos.ReasonNoVerdict},
		{"weird", topos.ReasonUnknownVerdict},
		{"", topos.ReasonRedacted},
		{"noname", topos.ReasonInvalid},
		{"odd", topos.ReasonRepeated},
	} {
		if !slices.Contains(seen, want) {
			t.Errorf("missing skip %+v in %+v", want, seen)
		}
	}
	if len(seen) != 6 {
		t.Errorf("%d skips, want 6: %+v", len(seen), seen)
	}
	got := byID(rs)
	if r, ok := got["badscore"]; !ok || len(r.Action.Signals) != 0 {
		t.Errorf("a score outside [0, 1] is dropped, not the call: %+v", r)
	}
	// The first well-formed confirmation answers the call; one with an
	// unknown decision does not count.
	if r, ok := got["odd"]; !ok || r.Approve == nil || !*r.Approve {
		t.Errorf("odd: %+v", r)
	}
	if len(rs) != 2 {
		t.Errorf("%d records, want 2", len(rs))
	}
	// Without a callback, skips are silent.
	if _, err := collect(t, data, topos.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestNoSessions(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := collect(t, missing, topos.Options{})
	if !errors.Is(err, topos.ErrNoSessions) {
		t.Errorf("missing directory: %v", err)
	}
	if _, statErr := os.Stat(missing); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("the importer created the data directory")
	}
	empty := t.TempDir()
	if err := os.Mkdir(filepath.Join(empty, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	rs, err := collect(t, empty, topos.Options{})
	if err != nil || len(rs) != 0 {
		t.Errorf("an empty store: %d records, %v", len(rs), err)
	}
}

func TestStopsWhenAsked(t *testing.T) {
	data := t.TempDir()
	topostest.Write(t, data,
		topostest.Session{Initiator: "u", Calls: []topostest.Call{{ToolUseID: "a", Name: "bash", Verdict: "ask"}, {ToolUseID: "b", Name: "bash", Verdict: "ask"}}},
		topostest.Session{Initiator: "u", Calls: []topostest.Call{{ToolUseID: "c", Name: "bash", Verdict: "ask"}}},
	)
	n := 0
	for range topos.Records(data, topos.Options{}) {
		n++
		break
	}
	if n != 1 {
		t.Errorf("read %d after a break", n)
	}
}

// A data directory that cannot be read reports the failure.
func TestUnreadable(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "sessions"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, data, topos.Options{}); err == nil {
		t.Error("read a sessions file as a directory")
	}
}

func TestEffectOf(t *testing.T) {
	for name, want := range map[string]limen.Effect{
		"read": limen.EffectRead, "grep": limen.EffectRead, "write": limen.EffectWrite,
		"web_fetch": limen.EffectExternal, "todo": limen.EffectNone, "unknown_tool": limen.EffectWrite,
	} {
		if got := topos.EffectOf(name); got != want {
			t.Errorf("EffectOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// An auto-mode session recorded each call's review probability, and the
// importer keeps it: an automatic allow drawn for review at a rate of 0.05
// weighs twenty, not one, in an error estimate.
func TestRecordedReviewProbability(t *testing.T) {
	data := t.TempDir()
	rate, one, zero := 0.05, 1.0, 0.0
	topostest.Write(t, data, topostest.Session{Initiator: "u", Calls: []topostest.Call{
		{ToolUseID: "drawn", Name: "bash", Verdict: "flag", Mode: "progressive", Answer: "deny", ReviewProbability: &rate},
		{ToolUseID: "missed", Name: "bash", Verdict: "allow", Mode: "progressive", ReviewProbability: &rate},
		{ToolUseID: "asked", Name: "bash", Verdict: "ask", Mode: "progressive", ReviewProbability: &one},
		{ToolUseID: "ruled", Name: "bash", Verdict: "allow", Mode: "confirm", ReviewProbability: &zero},
		{ToolUseID: "old", Name: "bash", Verdict: "flag", Mode: "progressive"},
	}})
	rs, err := collect(t, data, topos.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(rs)
	for id, want := range map[string]float64{"drawn": 0.05, "missed": 0.05, "asked": 1, "ruled": 0, "old": 1} {
		if r, ok := got[id]; !ok || r.ReviewProbability != want {
			t.Errorf("%s: review probability %v, want %v", id, r.ReviewProbability, want)
		}
	}
}

// A log of Topos v0.65.0 or later names the layer of the rules that
// decided each call, and the layer completes the source, so the ledger
// reports each layer apart. The rule within the layer is not part of it,
// and a layer this Topos version does not name is left out.
func TestDecidedByNamesTheSource(t *testing.T) {
	data := t.TempDir()
	rate := 0.05
	topostest.Write(t, data, topostest.Session{Initiator: "u", Calls: []topostest.Call{
		{ToolUseID: "listed", Name: "bash", Verdict: "allow", Mode: "confirm", DecidedBy: session.DecidedAlwaysAllow, DecidedByRule: "bash:go test*"},
		{ToolUseID: "scored", Name: "bash", Verdict: "flag", Mode: "progressive", DecidedBy: session.DecidedScore, DecidedByRule: "flag_at", ReviewProbability: &rate},
		{ToolUseID: "learned", Name: "bash", Verdict: "allow", Mode: "progressive", DecidedBy: session.DecidedLearned, DecidedByRule: "learned/1", ReviewProbability: &rate},
		{ToolUseID: "future", Name: "bash", Verdict: "ask", Mode: "confirm", DecidedBy: "a_layer_from_a_later_topos"},
		{ToolUseID: "old", Name: "bash", Verdict: "ask", Mode: "confirm"},
	}})
	rs, err := collect(t, data, topos.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(rs)
	for id, want := range map[string]string{
		"listed":  "topos/confirm/always_allow",
		"scored":  "topos/progressive/score",
		"learned": "topos/progressive/learned",
		"future":  "topos/confirm",
		"old":     "topos/confirm",
	} {
		if r, ok := got[id]; !ok || r.Source != want {
			t.Errorf("%s: source %q, want %q", id, r.Source, want)
		}
	}
}

// A call carries whether a person began its turn, as Topos sends it with
// the action, so a replay featurizes the action as the live session did. A
// log written before Topos recorded it carries no attribute.
func TestAttended(t *testing.T) {
	data := t.TempDir()
	yes, no := true, false
	topostest.Write(t, data, topostest.Session{Initiator: "u", Calls: []topostest.Call{
		{ToolUseID: "watched", Name: "bash", Verdict: "ask", Mode: "confirm", Attended: &yes},
		{ToolUseID: "triggered", Name: "bash", Verdict: "ask", Mode: "confirm", Attended: &no},
		{ToolUseID: "old", Name: "bash", Verdict: "ask", Mode: "confirm"},
	}})
	rs, err := collect(t, data, topos.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(rs)
	for id, want := range map[string]any{"watched": true, "triggered": false} {
		if a := got[id].Action.Attributes; len(a) != 1 || a[topos.AttributeAttended] != want {
			t.Errorf("%s: attributes %v, want attended %v", id, a, want)
		}
		if err := got[id].Validate(); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	if a := got["old"].Action.Attributes; a != nil {
		t.Errorf("an old log: attributes %v", a)
	}
}
