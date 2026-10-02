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

	"latere.ai/x/cribrum"
	"latere.ai/x/cribrum/conformance"
	"latere.ai/x/cribrum/stub"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(*testing.T) cribrum.Service { return stub.New(stub.Options{}) },
		Steered: func(_ *testing.T, predict func(cribrum.Action) (float64, float64)) cribrum.Service {
			return stub.New(stub.Options{Predict: predict})
		},
	})
}

var ctx = context.Background()

func act(id string) cribrum.Action {
	return cribrum.Action{
		Org: "o", Subject: "s", ActionID: id,
		Kind:   "tool",
		Name:   "bash",
		Effect: cribrum.EffectWrite,
		Input:  []byte(`{"command":"ls"}`),
	}
}

func codeOf(t *testing.T, err error, want cribrum.Code) {
	t.Helper()
	if cribrum.CodeOf(err) != want {
		t.Fatalf("error %v, want %s", err, want)
	}
}

func TestCanceledContext(t *testing.T) {
	s := stub.New(stub.Options{})
	c, cancel := context.WithCancel(ctx)
	cancel()
	checks := []error{
		func() error { _, err := s.Predict(c, act("a")); return err }(),
		s.Record(c, cribrum.Decision{}),
		s.Answer(c, cribrum.Answer{}),
		func() error { _, err := s.Cases(c, cribrum.CaseQuery{}); return err }(),
		func() error { _, err := s.Reliability(c, cribrum.ReliabilityQuery{}); return err }(),
		func() error { _, err := s.Profile(c, cribrum.Scope{}); return err }(),
		func() error { _, err := s.PutProfile(c, cribrum.Profile{}); return err }(),
		func() error {
			_, err := s.Replay(c, func(func(cribrum.ReplayRecord, error) bool) {}, cribrum.ReplayOptions{})
			return err
		}(),
		s.Forget(c, "o", "s"),
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
		_, err := s.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s", Cursor: cur})
		codeOf(t, err, cribrum.CodeInvalidQuery)
	}
	page, err := s.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s", Cursor: "7"})
	if err != nil || len(page.Entries) != 0 || page.Entries == nil {
		t.Errorf("a cursor past the end: %+v, %v", page, err)
	}
}

func TestClockAndWindow(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := stub.New(stub.Options{Clock: func() time.Time { return now }})
	a := act("w1")
	if err := s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Flag, ReviewProbability: 1, Source: "r", Action: &a}); err != nil {
		t.Fatal(err)
	}
	if err := s.Answer(ctx, cribrum.Answer{Key: a.Key, Approve: false, By: "https://issuer.example|usr_lead"}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s"})
	if got := page.Entries[0].Answers[0]; !got.At.Equal(now) || got.By != "https://issuer.example|usr_lead" {
		t.Errorf("answer %+v", got)
	}
	in := cribrum.ReliabilityQuery{Org: "o", Subject: "s", Of: cribrum.OfApplied, From: now.Add(-time.Hour), To: now.Add(time.Hour)}
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
	a.Signals = []cribrum.Signal{{Source: "classifier/x", Verdict: cribrum.Flag}}
	if err := s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "r", Action: &a}); err != nil {
		t.Fatal(err)
	}
	if err := s.Answer(ctx, cribrum.Answer{Key: a.Key, Approve: false}); err != nil {
		t.Fatal(err)
	}
	q := cribrum.ReliabilityQuery{Org: "o", Subject: "s", Of: "classifier/x", From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	r, err := s.Reliability(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.FalseAllow.Value == nil || *r.FalseAllow.Value != 1 || r.FalseAllow.Reviews != 1 {
		t.Errorf("a flagged signal is not an allow: %+v", r.FalseAllow)
	}
}

// A fallback prediction is not Cribrum's suggestion, so it is not a row of
// the learned source.
func TestLearnedSkipsUnavailable(t *testing.T) {
	s := stub.New(stub.Options{Predict: func(cribrum.Action) (float64, float64) { return 0.99, 0 }})
	a := act("u1")
	if _, err := s.Predict(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := act("u2")
	if err := s.Record(ctx, cribrum.Decision{Key: b.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "r", Action: &b}); err != nil {
		t.Fatal(err)
	}
	from := time.Now().Add(-time.Hour)
	r, err := s.Reliability(ctx, cribrum.ReliabilityQuery{Org: "o", Subject: "s", Of: cribrum.OfLearned, From: from, To: from.Add(2 * time.Hour)})
	if err != nil || r.FalseAllow.Covered != 0 {
		t.Errorf("%+v, %v", r.FalseAllow, err)
	}
}

func seq(rs ...cribrum.ReplayRecord) iter.Seq2[cribrum.ReplayRecord, error] {
	return func(yield func(cribrum.ReplayRecord, error) bool) {
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
	s := stub.New(stub.Options{Predict: func(a cribrum.Action) (float64, float64) {
		if a.ActionID == "allow" {
			return 0.99, 0
		}
		return 0.01, 0
	}})
	if _, err := s.PutProfile(ctx, cribrum.Profile{Org: "o", Costs: cribrum.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	// Out of order on purpose: a replay runs in time order.
	rep, err := s.Replay(ctx, seq(
		cribrum.ReplayRecord{At: at.Add(time.Minute), Action: act("block"), Verdict: cribrum.Ask, ReviewProbability: 1, Source: "r", Approve: &yes},
		cribrum.ReplayRecord{At: at, Action: act("allow"), Verdict: cribrum.Flag, ReviewProbability: 1, Source: "r", Approve: &no},
	), cribrum.ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.AllowedDenied != 1 || rep.BlockedApproved != 1 || rep.Would[cribrum.Allow] != 1 || rep.Would[cribrum.Block] != 1 {
		t.Errorf("report %+v", rep)
	}
	if rep.FalseAllow.Value == nil || *rep.FalseAllow.Value != 1 {
		t.Errorf("false allow %+v", rep.FalseAllow)
	}
	// A replay reuses the decisions of a kept history at its peril: one
	// decision per action.
	dup := cribrum.ReplayRecord{At: at, Action: act("allow"), Verdict: cribrum.Allow, Source: "r"}
	if _, err := s.Replay(ctx, seq(dup, dup), cribrum.ReplayOptions{}); cribrum.CodeOf(err) != cribrum.CodeAlreadyDecided {
		t.Errorf("a duplicate record: %v", err)
	}
	conflicting := dup
	conflicting.Action.Name = "write"
	if _, err := s.Replay(ctx, seq(dup, conflicting), cribrum.ReplayOptions{}); cribrum.CodeOf(err) != cribrum.CodeActionConflict {
		t.Errorf("a conflicting record: %v", err)
	}
}

func TestReplayErrors(t *testing.T) {
	s := stub.New(stub.Options{})
	bad := cribrum.ReplayRecord{Action: act("x")}
	_, err := s.Replay(ctx, seq(bad), cribrum.ReplayOptions{})
	var e *cribrum.Error
	if !errors.As(err, &e) || e.Code != cribrum.CodeInvalidReplay || !strings.HasPrefix(e.Fields[0], "records[0].") {
		t.Errorf("an invalid record: %v", err)
	}
	many := func(yield func(cribrum.ReplayRecord, error) bool) {
		r := cribrum.ReplayRecord{At: time.Now(), Action: act("m"), Verdict: cribrum.Allow, Source: "r"}
		for range cribrum.MaxReplayRecords + 1 {
			if !yield(r, nil) {
				return
			}
		}
	}
	_, err = s.Replay(ctx, many, cribrum.ReplayOptions{})
	codeOf(t, err, cribrum.CodeBodyTooLarge)
}

func TestForgetValidation(t *testing.T) {
	s := stub.New(stub.Options{})
	var e *cribrum.Error
	if err := s.Forget(ctx, "", ""); !errors.As(err, &e) || len(e.Fields) != 2 {
		t.Errorf("%v", err)
	}
	if err := s.Forget(ctx, "o", ""); cribrum.CodeOf(err) != cribrum.CodeInvalidQuery {
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
			if err := s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "r"}); err != nil {
				t.Error(err)
			}
			if err := s.Answer(ctx, cribrum.Answer{Key: a.Key, Approve: true}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if page, _ := s.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s", Limit: cribrum.MaxPageLimit}); len(page.Entries) != 32 {
		t.Errorf("%d cases", len(page.Entries))
	}
}
