// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"latere.ai/x/cribrum"
	"latere.ai/x/cribrum/engine"
	"latere.ai/x/cribrum/engine/store"
)

var errStore = errors.New("store: down")

// faulty fails the named method from its nth call on, and otherwise passes
// through to a memory store.
type faulty struct {
	*store.Memory
	fail  string
	after int
	// mu guards calls: the engine's background fitters call the store from
	// their own goroutines.
	mu    sync.Mutex
	calls map[string]int
}

func newFaulty(fail string, after int) *faulty {
	return &faulty{Memory: store.NewMemory(), fail: fail, after: after, calls: map[string]int{}}
}

func (f *faulty) hit(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name]++
	if name == f.fail && f.calls[name] > f.after {
		return errStore
	}
	return nil
}

func (f *faulty) called(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *faulty) Case(ctx context.Context, k cribrum.Key) (store.Case, bool, error) {
	if err := f.hit("Case"); err != nil {
		return store.Case{}, false, err
	}
	return f.Memory.Case(ctx, k)
}

func (f *faulty) Put(ctx context.Context, c store.Case) error {
	if err := f.hit("Put"); err != nil {
		return err
	}
	return f.Memory.Put(ctx, c)
}

func (f *faulty) NextT(ctx context.Context, org, subject string) (int64, error) {
	if err := f.hit("NextT"); err != nil {
		return 0, err
	}
	return f.Memory.NextT(ctx, org, subject)
}

func (f *faulty) Cases(ctx context.Context, org, subject string) ([]store.Case, error) {
	if err := f.hit("Cases"); err != nil {
		return nil, err
	}
	return f.Memory.Cases(ctx, org, subject)
}

func (f *faulty) Profile(ctx context.Context, s cribrum.Scope) (cribrum.Profile, bool, error) {
	if err := f.hit("Profile"); err != nil {
		return cribrum.Profile{}, false, err
	}
	return f.Memory.Profile(ctx, s)
}

func (f *faulty) PutProfile(ctx context.Context, p cribrum.Profile) error {
	if err := f.hit("PutProfile"); err != nil {
		return err
	}
	return f.Memory.PutProfile(ctx, p)
}

func (f *faulty) Snapshot(ctx context.Context, org, subject, version string) ([]byte, bool, error) {
	if err := f.hit("Snapshot"); err != nil {
		return nil, false, err
	}
	return f.Memory.Snapshot(ctx, org, subject, version)
}

func (f *faulty) PutSnapshot(ctx context.Context, org, subject, version string, b []byte) error {
	if err := f.hit("PutSnapshot"); err != nil {
		return err
	}
	return f.Memory.PutSnapshot(ctx, org, subject, version, b)
}

// Every store failure reaches the caller as an error, never as a verdict.
func TestStoreFailuresSurface(t *testing.T) {
	a := bash("s", "a", "go test ./...")
	ask := cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "m"}
	withAction := ask
	withAction.Action = &a
	steps := map[string]func(e *engine.Engine) error{
		"predict": func(e *engine.Engine) error { _, err := e.Predict(ctx, a); return err },
		"record": func(e *engine.Engine) error {
			return e.Record(ctx, withAction)
		},
		"answer": func(e *engine.Engine) error { return e.Answer(ctx, cribrum.Answer{Key: a.Key, Approve: true}) },
		"cases": func(e *engine.Engine) error {
			_, err := e.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s"})
			return err
		},
		"reliability": func(e *engine.Engine) error {
			_, err := e.Reliability(ctx, cribrum.ReliabilityQuery{Org: "o", Subject: "s", Of: cribrum.OfApplied, From: time.Unix(0, 0), To: time.Now().Add(time.Hour)})
			return err
		},
		"profile": func(e *engine.Engine) error { _, err := e.Profile(ctx, cribrum.Scope{Org: "o"}); return err },
		"put profile": func(e *engine.Engine) error {
			_, err := e.PutProfile(ctx, cribrum.Profile{Org: "o", Costs: cribrum.DefaultCosts})
			return err
		},
	}
	for _, method := range []string{"Case", "Put", "NextT", "Cases", "Profile", "PutProfile", "Snapshot"} {
		for name, step := range steps {
			f := newFaulty(method, 0)
			e := engine.New(engine.Options{Store: f, Inline: 1})
			err := step(e)
			e.Close()
			if f.called(method) > 0 && !errors.Is(err, errStore) {
				t.Errorf("%s failing, %s: %v", method, name, err)
			}
		}
	}
}

// A failed fit keeps the last model serving, and a failed snapshot write
// fails the fit that the caller asked for.
func TestFitFailures(t *testing.T) {
	f := newFaulty("PutSnapshot", 0)
	e := engine.New(engine.Options{Store: f, Inline: 1})
	defer e.Close()
	a := bash("s", "a", "go test ./...")
	if _, err := e.Predict(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := e.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Answer(ctx, cribrum.Answer{Key: a.Key, Approve: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Predict(ctx, bash("s", "b", "go test ./...")); !errors.Is(err, errStore) {
		t.Errorf("an inline fit that cannot store its snapshot: %v", err)
	}

	bg := engine.New(engine.Options{Store: newFaulty("PutSnapshot", 0), Debounce: time.Millisecond})
	defer bg.Close()
	train(t, bg, "s", 3)
	time.Sleep(50 * time.Millisecond)
	if err := bg.Flush(ctx); !errors.Is(err, errStore) {
		t.Errorf("flush with a failing snapshot store: %v", err)
	}
	if _, err := bg.Predict(ctx, bash("s", "z", "ls")); err != nil {
		t.Errorf("the last model stopped serving: %v", err)
	}
}

// A damaged snapshot fails loudly rather than predicting.
func TestDamagedSnapshot(t *testing.T) {
	st := store.NewMemory()
	if err := st.PutSnapshot(ctx, "o", "s", engine.Version, []byte("not a snapshot")); err != nil {
		t.Fatal(err)
	}
	e := engine.New(engine.Options{Store: st, Inline: 1})
	defer e.Close()
	if _, err := e.Predict(ctx, bash("s", "a", "ls")); err == nil {
		t.Error("predicted from a damaged snapshot")
	}
}

// A case featurized by another featurizer version is featurized again from
// its stored action when the model trains.
func TestRefeaturizes(t *testing.T) {
	st := store.NewMemory()
	for i := range 10 {
		a := bash("s", fmt.Sprintf("old-%d", i), "go test ./...")
		a.Input = nil
		c := store.Case{T: int64(i), ReceivedAt: time.Now(), Action: a, Featurizer: "features/0", A: []float64{1}, C: []float64{1},
			Decision: &cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "m"},
			Answers:  []cribrum.RecordedAnswer{{Approve: true, Review: true}}}
		if err := st.Put(ctx, c); err != nil {
			t.Fatal(err)
		}
		if _, err := st.NextT(ctx, "o", "s"); err != nil {
			t.Fatal(err)
		}
	}
	e := engine.New(engine.Options{Store: st, Inline: 1})
	defer e.Close()
	p, err := e.Predict(ctx, bash("s", "new", "go test ./..."))
	if err != nil {
		t.Fatal(err)
	}
	if p.Approve <= 0.5 {
		t.Errorf("old cases did not train after re-featurizing: %+v", p)
	}
}

func TestQueryValidation(t *testing.T) {
	e := engine.New(engine.Options{})
	defer e.Close()
	c, cancel := context.WithCancel(ctx)
	cancel()
	a := bash("s", "a", "ls")
	canceled := []error{
		func() error { _, err := e.Predict(c, a); return err }(),
		e.Record(c, cribrum.Decision{}),
		e.Answer(c, cribrum.Answer{}),
		func() error { _, err := e.Cases(c, cribrum.CaseQuery{}); return err }(),
		func() error { _, err := e.Reliability(c, cribrum.ReliabilityQuery{}); return err }(),
		func() error { _, err := e.Profile(c, cribrum.Scope{}); return err }(),
		func() error { _, err := e.PutProfile(c, cribrum.Profile{}); return err }(),
		e.Forget(c, "o", "s"),
	}
	for i, err := range canceled {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("call %d under a canceled context: %v", i, err)
		}
	}
	if _, err := e.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s", Cursor: "x"}); cribrum.CodeOf(err) != cribrum.CodeInvalidQuery {
		t.Errorf("bad cursor: %v", err)
	}
	if _, err := e.Profile(ctx, cribrum.Scope{}); cribrum.CodeOf(err) != cribrum.CodeInvalidQuery {
		t.Errorf("profile with no org: %v", err)
	}
	if _, err := e.PutProfile(ctx, cribrum.Profile{Org: "o", Costs: cribrum.Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1}}); cribrum.CodeOf(err) != cribrum.CodeAskTooCostly {
		t.Errorf("costs that decide on no evidence: %v", err)
	}
	if err := e.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "m"}); cribrum.CodeOf(err) != cribrum.CodeInvalidDecision {
		t.Errorf("a decision with no prediction and no action: %v", err)
	}
	other := a
	other.Name = "write"
	if _, err := e.Predict(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := e.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "m", Action: &other}); cribrum.CodeOf(err) != cribrum.CodeActionConflict {
		t.Errorf("a decision carrying a different action: %v", err)
	}
}

// Cases pages by ordinal and filters by action id and by pending.
func TestCasesPaging(t *testing.T) {
	e := engine.New(engine.Options{Inline: 100})
	defer e.Close()
	for i := range 7 {
		a := bash("s", fmt.Sprintf("p-%d", i), "ls")
		if _, err := e.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		v := cribrum.Ask
		if i%2 == 0 {
			v = cribrum.Allow
		}
		prob := 1.0
		if v == cribrum.Allow {
			prob = 0
		}
		if err := e.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: v, ReviewProbability: prob, Source: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	var seen int
	cursor := ""
	for {
		page, err := e.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s", Cursor: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		seen += len(page.Entries)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if seen != 7 {
		t.Errorf("paged through %d cases, want 7", seen)
	}
	pending, err := e.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s", Pending: true})
	if err != nil || len(pending.Entries) != 3 {
		t.Errorf("pending %d, %v; want the 3 asks", len(pending.Entries), err)
	}
	one, err := e.Cases(ctx, cribrum.CaseQuery{Org: "o", Subject: "s", ActionID: "p-4"})
	if err != nil || len(one.Entries) != 1 || one.Entries[0].Action.Input != nil {
		t.Errorf("one case %+v, %v", one, err)
	}
}

// Profiles are copied into a replay's scratch engine, so its suggestions
// use the organization's costs.
func TestReplayUsesProfiles(t *testing.T) {
	e := engine.New(engine.Options{})
	defer e.Close()
	if _, err := e.PutProfile(ctx, cribrum.Profile{Org: "o", Kind: "tool", Costs: cribrum.Costs{FalseAllow: 3, FalseBlock: 3, Ask: 1}, AuditRate: 0.1}); err != nil {
		t.Fatal(err)
	}
	r := cribrum.ReplayRecord{At: time.Now(), Action: bash("s", "a", "ls"), Verdict: cribrum.Allow, ReviewProbability: 0.1, Source: "m"}
	rep, err := e.Replay(ctx, recs([]cribrum.ReplayRecord{r}), cribrum.ReplayOptions{})
	if err != nil || rep.Would[cribrum.Ask] != 1 {
		t.Errorf("%+v, %v", rep, err)
	}
	if rep.FalseAllow.Value != nil {
		t.Errorf("an unanswered history estimated %+v", rep.FalseAllow)
	}
}
