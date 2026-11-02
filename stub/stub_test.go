// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package stub_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/conformance"
	"latere.ai/x/limen/stub"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(*testing.T) limen.Service { return stub.New(stub.Options{}) },
		Steered: func(_ *testing.T, predict func(limen.Action) (float64, float64)) limen.Service {
			return stub.New(stub.Options{Predict: predict})
		},
		Clocked: func(_ *testing.T, now func() time.Time) limen.Service { return stub.New(stub.Options{Clock: now}) },
	})
}

var ctx = context.Background()

func act(id string) limen.Action {
	return limen.Action{
		Org: "o", Subject: "s", ActionID: id,
		Kind:   "tool",
		Name:   "bash",
		Effect: limen.EffectWrite,
		Input:  []byte(`{"command":"ls"}`),
	}
}

func codeOf(t *testing.T, err error, want limen.Code) {
	t.Helper()
	if limen.CodeOf(err) != want {
		t.Fatalf("error %v, want %s", err, want)
	}
}

func TestCanceledContext(t *testing.T) {
	s := stub.New(stub.Options{})
	c, cancel := context.WithCancel(ctx)
	cancel()
	checks := []error{
		func() error { _, err := s.Predict(c, act("a")); return err }(),
		s.Record(c, limen.Decision{}),
		s.Answer(c, limen.Answer{}),
		func() error { _, err := s.Cases(c, limen.CaseQuery{}); return err }(),
		func() error { _, err := s.Reliability(c, limen.ReliabilityQuery{}); return err }(),
		func() error { _, err := s.Profile(c, limen.Scope{}); return err }(),
		func() error { _, err := s.PutProfile(c, limen.Profile{}); return err }(),
		func() error {
			_, err := s.Replay(c, func(func(limen.ReplayRecord, error) bool) {}, limen.ReplayOptions{})
			return err
		}(),
		s.Forget(c, "o", "s"),
		s.DeleteProfile(c, limen.Scope{Org: "o"}),
		func() error { _, err := s.Report(c, limen.ReportQuery{}); return err }(),
	}
	for i, err := range checks {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("call %d: %v", i, err)
		}
	}
}

func TestCursor(t *testing.T) {
	s := stub.New(stub.Options{})
	for _, cur := range []string{"x", "-1"} {
		_, err := s.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s", Cursor: cur})
		codeOf(t, err, limen.CodeInvalidQuery)
	}
	page, err := s.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s", Cursor: "7"})
	if err != nil || len(page.Entries) != 0 || page.Entries == nil {
		t.Errorf("a cursor past the end: %+v, %v", page, err)
	}
}

func TestClockAndWindow(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := stub.New(stub.Options{Clock: func() time.Time { return now }})
	a := act("w1")
	if err := s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Flag, ReviewProbability: 1, Source: "r", Action: &a}); err != nil {
		t.Fatal(err)
	}
	if err := s.Answer(ctx, limen.Answer{Key: a.Key, Approve: false, By: "https://issuer.example|usr_lead"}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s"})
	if got := page.Entries[0].Answers[0]; !got.At.Equal(now) || got.By != "https://issuer.example|usr_lead" {
		t.Errorf("answer %+v", got)
	}
	in := limen.ReliabilityQuery{Org: "o", Subject: "s", Of: limen.OfApplied, From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	if r, _ := s.Reliability(ctx, in); r.FalseAllow.Value == nil || *r.FalseAllow.Value != 1 {
		t.Errorf("inside the window %+v", r.FalseAllow)
	}
	out := in
	out.From, out.To = now.Add(time.Second), now.Add(time.Hour)
	if r, _ := s.Reliability(ctx, out); r.FalseAllow.Value != nil {
		t.Errorf("outside the window %+v", r.FalseAllow)
	}
}

// A signal whose verdict is flag let the action run, so it counts among
// the source's allows: a judge that flagged an action the person denied
// made a false allow.
func TestSignalFlagIsAnAllow(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := stub.New(stub.Options{Clock: func() time.Time { return now }})
	a := act("f1")
	a.Signals = []limen.Signal{{Source: "classifier/x", Verdict: limen.Flag}}
	if err := s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "r", Action: &a}); err != nil {
		t.Fatal(err)
	}
	if err := s.Answer(ctx, limen.Answer{Key: a.Key, Approve: false}); err != nil {
		t.Fatal(err)
	}
	q := limen.ReliabilityQuery{Org: "o", Subject: "s", Of: "classifier/x", From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	r, err := s.Reliability(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.FalseAllow.Value == nil || *r.FalseAllow.Value != 1 || r.FalseAllow.Reviews != 1 {
		t.Errorf("a flagged signal is not an allow: %+v", r.FalseAllow)
	}
}

// A fallback prediction is not Limen's suggestion, so it is not a row of
// the learned source.
func TestLearnedSkipsUnavailable(t *testing.T) {
	s := stub.New(stub.Options{Predict: func(limen.Action) (float64, float64) { return 0.99, 0 }})
	a := act("u1")
	if _, err := s.Predict(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := act("u2")
	if err := s.Record(ctx, limen.Decision{Key: b.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "r", Action: &b}); err != nil {
		t.Fatal(err)
	}
	from := time.Now().Add(-time.Hour)
	r, err := s.Reliability(ctx, limen.ReliabilityQuery{Org: "o", Subject: "s", Of: limen.OfLearned, From: from, To: from.Add(2 * time.Hour)})
	if err != nil || r.FalseAllow.Covered != 0 {
		t.Errorf("%+v, %v", r.FalseAllow, err)
	}
}

func seq(rs ...limen.ReplayRecord) iter.Seq2[limen.ReplayRecord, error] {
	return func(yield func(limen.ReplayRecord, error) bool) {
		for _, r := range rs {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func TestReplay(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	yes, no := true, false
	s := stub.New(stub.Options{Predict: func(a limen.Action) (float64, float64) {
		if a.ActionID == "allow" {
			return 0.99, 0
		}
		return 0.01, 0
	}})
	if _, err := s.PutProfile(ctx, limen.Profile{Org: "o", Costs: limen.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	// Out of order on purpose: a replay runs in time order.
	rep, err := s.Replay(ctx, seq(
		limen.ReplayRecord{At: at.Add(time.Minute), Action: act("block"), Verdict: limen.Ask, ReviewProbability: 1, Source: "r", Approve: &yes},
		limen.ReplayRecord{At: at, Action: act("allow"), Verdict: limen.Flag, ReviewProbability: 1, Source: "r", Approve: &no},
	), limen.ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.AllowedDenied != 1 || rep.BlockedApproved != 1 || rep.Would[limen.Allow] != 1 || rep.Would[limen.Block] != 1 {
		t.Errorf("report %+v", rep)
	}
	if rep.FalseAllow.Value == nil || *rep.FalseAllow.Value != 1 {
		t.Errorf("false allow %+v", rep.FalseAllow)
	}
	// A replay reuses the decisions of a kept history at its peril: one
	// decision per action.
	dup := limen.ReplayRecord{At: at, Action: act("allow"), Verdict: limen.Allow, Source: "r"}
	if _, err := s.Replay(ctx, seq(dup, dup), limen.ReplayOptions{}); limen.CodeOf(err) != limen.CodeAlreadyDecided {
		t.Errorf("a duplicate record: %v", err)
	}
	conflicting := dup
	conflicting.Action.Name = "write"
	if _, err := s.Replay(ctx, seq(dup, conflicting), limen.ReplayOptions{}); limen.CodeOf(err) != limen.CodeActionConflict {
		t.Errorf("a conflicting record: %v", err)
	}
}

func TestReplayErrors(t *testing.T) {
	s := stub.New(stub.Options{})
	bad := limen.ReplayRecord{Action: act("x")}
	_, err := s.Replay(ctx, seq(bad), limen.ReplayOptions{})
	var e *limen.Error
	if !errors.As(err, &e) || e.Code != limen.CodeInvalidReplay || !strings.HasPrefix(e.Fields[0], "records[0].") {
		t.Errorf("an invalid record: %v", err)
	}
	many := func(yield func(limen.ReplayRecord, error) bool) {
		r := limen.ReplayRecord{At: time.Now(), Action: act("m"), Verdict: limen.Allow, Source: "r"}
		for range limen.MaxReplayRecords + 1 {
			if !yield(r, nil) {
				return
			}
		}
	}
	_, err = s.Replay(ctx, many, limen.ReplayOptions{})
	codeOf(t, err, limen.CodeBodyTooLarge)
}

func TestForgetValidation(t *testing.T) {
	s := stub.New(stub.Options{})
	var e *limen.Error
	if err := s.Forget(ctx, "", ""); !errors.As(err, &e) || len(e.Fields) != 2 {
		t.Errorf("%v", err)
	}
	if err := s.Forget(ctx, "o", ""); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("%v", err)
	}
}

// The stub is safe for concurrent use, as a decision point's goroutines
// share it.
func TestConcurrent(t *testing.T) {
	s := stub.New(stub.Options{})
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			a := act(string(rune('a' + i)))
			if _, err := s.Predict(ctx, a); err != nil {
				t.Error(err)
			}
			if err := s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "r"}); err != nil {
				t.Error(err)
			}
			if err := s.Answer(ctx, limen.Answer{Key: a.Key, Approve: true}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if page, _ := s.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s", Limit: limen.MaxPageLimit}); len(page.Entries) != 32 {
		t.Errorf("%d cases", len(page.Entries))
	}
}

// A report's kept week is the stub's own copy: a caller that changes a
// returned week changes no later read.
func TestReportKeepsItsCopy(t *testing.T) {
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	now := monday.Add(time.Hour)
	s := stub.New(stub.Options{Clock: func() time.Time { return now }})
	for i := range 5 {
		a := act("k")
		a.Subject = string(rune('a' + i))
		if err := s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Allow, ReviewProbability: 0, Source: "r", Action: &a}); err != nil {
			t.Fatal(err)
		}
	}
	now = monday.Add(limen.ReportWeek + limen.ReportClosesAfter)
	q := limen.ReportQuery{Org: "o", From: monday, To: monday.Add(limen.ReportWeek)}
	first, err := s.Report(ctx, q)
	if err != nil || first.Weeks[0].Total == nil {
		t.Fatalf("%+v, %v", first, err)
	}
	first.Weeks[0].Total.Subjects = 99
	again, err := s.Report(ctx, q)
	if err != nil || again.Weeks[0].Total.Subjects != 5 {
		t.Errorf("a write to a returned week reached the stub: %+v, %v", again.Weeks[0].Total, err)
	}
}
