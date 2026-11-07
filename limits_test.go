// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// text is a string of n bytes.
func text(n int) string { return strings.Repeat("a", n) }

// A string a client sends is accepted at MaxStringBytes and refused one
// byte past it, with the code of the route and the path of the field.
func TestStringLengths(t *testing.T) {
	if MaxStringBytes != 256 {
		t.Fatalf("MaxStringBytes = %d, want 256", MaxStringBytes)
	}
	run := func(t *testing.T, name string, code Code, mk func() any, validate func(any) error, sets []struct {
		field string
		set   func(any, string)
	}) {
		t.Helper()
		for _, s := range sets {
			t.Run(name+"/"+s.field, func(t *testing.T) {
				ok := mk()
				s.set(ok, text(MaxStringBytes))
				if err := validate(ok); err != nil {
					t.Fatalf("%d bytes: %v", MaxStringBytes, err)
				}
				long := mk()
				s.set(long, text(MaxStringBytes+1))
				err := validate(long)
				fields := fieldsOf(t, err, code)
				if !slices.Contains(fields, s.field) {
					t.Fatalf("%d bytes: fields %v, want %s", MaxStringBytes+1, fields, s.field)
				}
				if d := detailOf(t, err); !strings.Contains(d, s.field+" ") || !strings.Contains(d, "longer than 256 bytes") {
					t.Errorf("detail %q does not say %s is longer than 256 bytes", d, s.field)
				}
				if d := detailOf(t, err); len(d) > 4096 {
					t.Errorf("detail is %d bytes; it must not echo the value", len(d))
				}
			})
		}
	}
	type act = func(*Action, string)
	actions := func() []struct {
		field string
		set   func(any, string)
	} {
		var out []struct {
			field string
			set   func(any, string)
		}
		for _, s := range []struct {
			field string
			set   act
		}{
			{"org", func(a *Action, v string) { a.Org = v }},
			{"subject", func(a *Action, v string) { a.Subject = v }},
			{"action_id", func(a *Action, v string) { a.ActionID = v }},
			{"kind", func(a *Action, v string) { a.Kind = v }},
			{"name", func(a *Action, v string) { a.Name = v }},
			{"actor.kind", func(a *Action, v string) { a.Actor.Kind = v }},
			{"actor.id", func(a *Action, v string) { a.Actor.ID = v }},
			{"actor.session", func(a *Action, v string) { a.Actor.Session = v }},
			{"target.kind", func(a *Action, v string) { a.Target.Kind = v }},
			{"target.id", func(a *Action, v string) { a.Target.ID = v }},
			{"target.class", func(a *Action, v string) { a.Target.Class = v }},
			{"environment.kind", func(a *Action, v string) { a.Environment.Kind = v }},
			{"environment.repositories", func(a *Action, v string) { a.Environment.Repositories = v }},
			{"environment.holds[0]", func(a *Action, v string) { a.Environment.Holds = []string{v} }},
			{"scope.undo", func(a *Action, v string) { a.Scope = &ReachClaim{Undo: v} }},
			{"scope.contact", func(a *Action, v string) { a.Scope = &ReachClaim{Contact: v} }},
			{"attributes", func(a *Action, v string) { a.Attributes[v] = 1 }},
			{"attributes.remote", func(a *Action, v string) { a.Attributes["remote"] = v }},
			{"signals[0].source", func(a *Action, v string) { a.Signals = []Signal{{Source: v, Score: f(0.5)}} }},
		} {
			out = append(out, struct {
				field string
				set   func(any, string)
			}{s.field, func(x any, v string) { s.set(unwrapAction(x), v) }})
		}
		return out
	}
	newAction := func() any {
		a := validAction()
		a.Target.Class = "own"
		a.Environment.Repositories = "private"
		a.Attributes = map[string]any{"remote": "origin"}
		return &a
	}
	run(t, "action", CodeInvalidAction, newAction, func(x any) error { return x.(*Action).Validate() }, actions())

	run(t, "decision", CodeInvalidDecision,
		func() any {
			a := validAction()
			return &Decision{Key: key, Verdict: Allow, ReviewProbability: 0.05, Source: "learned/3", Action: &a}
		},
		func(x any) error { return x.(*Decision).Validate() },
		[]struct {
			field string
			set   func(any, string)
		}{
			{"source", func(x any, v string) { x.(*Decision).Source = v }},
			{"org", func(x any, v string) { d := x.(*Decision); d.Org = v; d.Action.Org = v }},
			{"subject", func(x any, v string) { d := x.(*Decision); d.Subject = v; d.Action.Subject = v }},
			{"action_id", func(x any, v string) { d := x.(*Decision); d.ActionID = v; d.Action.ActionID = v }},
			{"action.name", func(x any, v string) { x.(*Decision).Action.Name = v }},
			{"action.attributes.remote", func(x any, v string) { x.(*Decision).Action.Attributes = map[string]any{"remote": v} }},
		})

	run(t, "answer", CodeInvalidAnswer,
		func() any { return &Answer{Key: key, Approve: true} },
		func(x any) error { return x.(*Answer).Validate() },
		[]struct {
			field string
			set   func(any, string)
		}{
			{"org", func(x any, v string) { x.(*Answer).Org = v }},
			{"subject", func(x any, v string) { x.(*Answer).Subject = v }},
			{"action_id", func(x any, v string) { x.(*Answer).ActionID = v }},
			{"by", func(x any, v string) { x.(*Answer).By = v }},
			{"surface", func(x any, v string) { x.(*Answer).Surface = v }},
		})

	run(t, "profile", CodeInvalidProfile,
		func() any { p := DefaultProfile(Scope{Org: "o"}); return &p },
		func(x any) error { return x.(*Profile).Validate() },
		[]struct {
			field string
			set   func(any, string)
		}{
			{"org", func(x any, v string) { x.(*Profile).Org = v }},
			{"kind", func(x any, v string) { x.(*Profile).Kind = v }},
			{"agent", func(x any, v string) { x.(*Profile).Agent = v }},
			{"subject", func(x any, v string) {
				p := x.(*Profile)
				p.Kind, p.Agent, p.Subject = "", "", v
			}},
		})

	at := time.Date(2026, 9, 2, 9, 12, 3, 0, time.UTC)
	run(t, "replay", CodeInvalidReplay,
		func() any {
			return &ReplayRecord{At: at, Action: validAction(), Verdict: Ask, ReviewProbability: 1, Source: "rules/1", Approve: b(true)}
		},
		func(x any) error { return x.(*ReplayRecord).Validate() },
		[]struct {
			field string
			set   func(any, string)
		}{
			{"source", func(x any, v string) { x.(*ReplayRecord).Source = v }},
			{"surface", func(x any, v string) { x.(*ReplayRecord).Surface = v }},
			{"action.org", func(x any, v string) { x.(*ReplayRecord).Action.Org = v }},
			{"action.subject", func(x any, v string) { x.(*ReplayRecord).Action.Subject = v }},
			{"action.action_id", func(x any, v string) { x.(*ReplayRecord).Action.ActionID = v }},
			{"action.kind", func(x any, v string) { x.(*ReplayRecord).Action.Kind = v }},
			{"action.name", func(x any, v string) { x.(*ReplayRecord).Action.Name = v }},
			{"action.actor.session", func(x any, v string) { x.(*ReplayRecord).Action.Actor.Session = v }},
		})

	// The queries a server parses from a URL are held to the same length.
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	q := func(name string, mk func() any, validate func(any) error, sets ...struct {
		field string
		set   func(any, string)
	}) {
		run(t, name, CodeInvalidQuery, mk, validate, sets)
	}
	type fs = struct {
		field string
		set   func(any, string)
	}
	q("key", func() any { k := key; return &k }, func(x any) error { return x.(*Key).Validate() },
		fs{"org", func(x any, v string) { x.(*Key).Org = v }},
		fs{"subject", func(x any, v string) { x.(*Key).Subject = v }},
		fs{"action_id", func(x any, v string) { x.(*Key).ActionID = v }})
	q("cases", func() any { return &CaseQuery{Org: "o", Subject: "s"} }, func(x any) error { return x.(*CaseQuery).Validate() },
		fs{"org", func(x any, v string) { x.(*CaseQuery).Org = v }},
		fs{"subject", func(x any, v string) { x.(*CaseQuery).Subject = v }},
		fs{"action_id", func(x any, v string) { x.(*CaseQuery).ActionID = v }},
		fs{"cursor", func(x any, v string) { x.(*CaseQuery).Cursor = v }})
	q("learned", func() any { return &LearnedQuery{Org: "o", Subject: "s"} }, func(x any) error { return x.(*LearnedQuery).Validate() },
		fs{"org", func(x any, v string) { x.(*LearnedQuery).Org = v }},
		fs{"subject", func(x any, v string) { x.(*LearnedQuery).Subject = v }},
		fs{"agent", func(x any, v string) { x.(*LearnedQuery).Agent = v }},
		fs{"kind", func(x any, v string) { x.(*LearnedQuery).Kind = v }})
	q("preview", func() any {
		return &PreviewRequest{Org: "o", Subject: "s", From: from, To: from.Add(time.Hour), Costs: DefaultCosts, AuditRate: 0.05}
	}, func(x any) error { return x.(*PreviewRequest).Validate() },
		fs{"org", func(x any, v string) { x.(*PreviewRequest).Org = v }},
		fs{"subject", func(x any, v string) { x.(*PreviewRequest).Subject = v }})
	q("reliability", func() any {
		return &ReliabilityQuery{Org: "o", Subject: "s", Of: OfApplied, From: from, To: from.Add(time.Hour)}
	}, func(x any) error { return x.(*ReliabilityQuery).Validate() },
		fs{"org", func(x any, v string) { x.(*ReliabilityQuery).Org = v }},
		fs{"subject", func(x any, v string) { x.(*ReliabilityQuery).Subject = v }},
		fs{"of", func(x any, v string) { x.(*ReliabilityQuery).Of = v }})
	q("calibration", func() any {
		return &CalibrationQuery{Org: "o", Subject: "s", From: from, To: from.Add(time.Hour)}
	}, func(x any) error { return x.(*CalibrationQuery).Validate() },
		fs{"org", func(x any, v string) { x.(*CalibrationQuery).Org = v }},
		fs{"subject", func(x any, v string) { x.(*CalibrationQuery).Subject = v }})
	q("report", func() any {
		mon := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
		return &ReportQuery{Org: "o", From: mon, To: mon.Add(ReportWeek)}
	}, func(x any) error { return x.(*ReportQuery).Validate() },
		fs{"org", func(x any, v string) { x.(*ReportQuery).Org = v }})
	q("scope", func() any { return &Scope{Org: "o"} }, func(x any) error { return x.(*Scope).Validate() },
		fs{"org", func(x any, v string) { x.(*Scope).Org = v }},
		fs{"kind", func(x any, v string) { x.(*Scope).Kind = v }},
		fs{"agent", func(x any, v string) { x.(*Scope).Agent = v }})
	q("person", func() any { return &[2]string{"o", "s"} }, func(x any) error { p := x.(*[2]string); return ValidatePerson(p[0], p[1]) },
		fs{"org", func(x any, v string) { x.(*[2]string)[0] = v }},
		fs{"subject", func(x any, v string) { x.(*[2]string)[1] = v }})
}

func unwrapAction(x any) *Action { return x.(*Action) }

// An oversized attribute key is named by "attributes" alone, so the field
// path never carries the key.
func TestLongAttributeKeyIsNotEchoed(t *testing.T) {
	a := validAction()
	a.Attributes = map[string]any{text(10_000): map[string]any{}}
	err := a.Validate()
	for _, f := range fieldsOf(t, err, CodeInvalidAction) {
		if len(f) > 64 {
			t.Errorf("field path of %d bytes", len(f))
		}
	}
	if d := detailOf(t, err); len(d) > 4096 {
		t.Errorf("detail of %d bytes", len(d))
	}
}

// ValidatePerson names an organization and a subject, as a route that
// reads no other field does.
func TestValidatePersonEmpty(t *testing.T) {
	if err := ValidatePerson("o", "s"); err != nil {
		t.Fatal(err)
	}
	if fields := fieldsOf(t, ValidatePerson("", ""), CodeInvalidQuery); !slices.Equal(fields, []string{"org", "subject"}) {
		t.Errorf("fields %v", fields)
	}
}

// A new person over the cap of an organization is a 403 with its own code,
// so a caller can tell it from a missing permission.
func TestQuotaExceededRow(t *testing.T) {
	if CodeQuotaExceeded != "quota_exceeded" || CodeQuotaExceeded.Status() != 403 || CodeQuotaExceeded.Message() == "" {
		t.Errorf("quota_exceeded: %q, %d, %q", CodeQuotaExceeded, CodeQuotaExceeded.Status(), CodeQuotaExceeded.Message())
	}
	if !slices.Contains(Codes(), CodeQuotaExceeded) {
		t.Error("Codes() omits quota_exceeded")
	}
}

// An action's signals and an environment's holds are bounded in count: at
// the bound they pass, one past it they fail naming the list, and a
// decision or replay record carrying the action meets the same bound.
func TestListCountBounds(t *testing.T) {
	if MaxSignals != 64 || MaxHolds != 64 {
		t.Fatalf("MaxSignals = %d, MaxHolds = %d, want 64 each", MaxSignals, MaxHolds)
	}
	lists := []struct {
		field string
		set   func(a *Action, n int)
	}{
		{"signals", func(a *Action, n int) {
			a.Signals = make([]Signal, n)
			for i := range a.Signals {
				a.Signals[i] = Signal{Source: "rules/1", Verdict: Allow}
			}
		}},
		{"environment.holds", func(a *Action, n int) {
			a.Environment.Holds = make([]string, n)
			for i := range a.Environment.Holds {
				a.Environment.Holds[i] = HoldEgress
			}
		}},
	}
	at := time.Date(2026, 9, 2, 9, 12, 3, 0, time.UTC)
	for _, l := range lists {
		t.Run(l.field, func(t *testing.T) {
			bound := MaxSignals
			if l.field != "signals" {
				bound = MaxHolds
			}
			edit := func(n int) Action {
				a := validAction()
				l.set(&a, n)
				return a
			}
			ok, over := edit(bound), edit(bound+1)
			if err := ok.Validate(); err != nil {
				t.Fatalf("%d entries: %v", bound, err)
			}
			err := over.Validate()
			if fields := fieldsOf(t, err, CodeInvalidAction); !slices.Equal(fields, []string{l.field}) {
				t.Fatalf("%d entries: fields %v, want [%s]", bound+1, fields, l.field)
			}
			if d := detailOf(t, err); !strings.Contains(d, "has more than 64 entries") {
				t.Errorf("detail %q", d)
			}
			dec := func(a Action) Decision {
				return Decision{Key: a.Key, Verdict: Ask, ReviewProbability: 1, Source: "rules/1", Action: &a}
			}
			rec := func(a Action) ReplayRecord {
				return ReplayRecord{At: at, Action: a, Verdict: Ask, ReviewProbability: 1, Source: "rules/1", Approve: b(true)}
			}
			if err := dec(ok).Validate(); err != nil {
				t.Errorf("decision at the bound: %v", err)
			}
			if err := rec(ok).Validate(); err != nil {
				t.Errorf("record at the bound: %v", err)
			}
			if fields := fieldsOf(t, dec(over).Validate(), CodeInvalidDecision); !slices.Contains(fields, "action."+l.field) {
				t.Errorf("decision fields %v", fields)
			}
			if fields := fieldsOf(t, rec(over).Validate(), CodeInvalidReplay); !slices.Contains(fields, "action."+l.field) {
				t.Errorf("record fields %v", fields)
			}
		})
	}
}

// detailOf returns the detail of err, which must be an *Error.
func detailOf(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%v is not an *Error", err)
	}
	return e.Detail
}
