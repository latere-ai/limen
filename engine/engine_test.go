// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	"latere.ai/x/pkg/verdict"

	"latere.ai/x/limen"
	"latere.ai/x/limen/conformance"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
)

var ctx = context.Background()

func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(t *testing.T) limen.Service {
			e := engine.New(engine.Options{Debounce: time.Millisecond})
			t.Cleanup(func() { e.Close() })
			return e
		},
	})
}

func TestConformanceInline(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(t *testing.T) limen.Service {
			e := engine.New(engine.Options{Inline: 1})
			t.Cleanup(func() { e.Close() })
			return e
		},
	})
}

// clock is a settable clock for tests.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

func bash(subject, id, command string) limen.Action {
	in, _ := json.Marshal(map[string]string{"command": command})
	return limen.Action{
		Org:         "o",
		Subject:     subject,
		ActionID:    id,
		Actor:       &limen.Actor{Kind: "agent", ID: "agt", Session: "ses"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      limen.EffectWrite,
		Input:       in,
		Environment: &limen.Environment{Kind: "sandbox"},
	}
}

// decide records the decision a decision point under the default profile
// would make with no review draw, and returns whether the person sees it.
func decide(t *testing.T, s limen.Service, a limen.Action, p limen.Prediction) bool {
	t.Helper()
	v, prob := verdict.Decide(p.Verdict, limen.Allow, p.AuditRate, 0.99)
	if err := s.Record(ctx, limen.Decision{Key: a.Key, Verdict: v, ReviewProbability: prob, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	return v.Shown()
}

// A person who approves every test run and denies every recursive delete
// teaches the engine to allow the one and block the other, while a new
// subject still gets an ask.
func TestLearns(t *testing.T) {
	c := &clock{now: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}
	e := engine.New(engine.Options{Inline: 1, Clock: c.Now})
	defer e.Close()
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Costs: limen.Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		for _, step := range []struct {
			cmd     string
			approve bool
		}{{"go test ./...", true}, {"rm -rf build", false}} {
			a := bash("s", fmt.Sprintf("%s-%d", step.cmd, i), step.cmd)
			p, err := e.Predict(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			// Under manual mode every call is asked, so every answer is a review.
			if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual"}); err != nil {
				t.Fatal(err)
			}
			_ = p
			if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: step.approve}); err != nil {
				t.Fatal(err)
			}
		}
	}
	test, err := e.Predict(ctx, bash("s", "final-test", "go test ./pkg/..."))
	if err != nil {
		t.Fatal(err)
	}
	del, err := e.Predict(ctx, bash("s", "final-rm", "rm -rf dist"))
	if err != nil {
		t.Fatal(err)
	}
	if test.Verdict != limen.Allow || test.Approve < 0.65 {
		t.Errorf("a familiar test run: %+v", test)
	}
	if del.Verdict != limen.Block || del.Approve > 0.35 {
		t.Errorf("a familiar recursive delete: %+v", del)
	}
	fresh, err := e.Predict(ctx, bash("someone else", "x", "go test ./..."))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Verdict != limen.Ask || fresh.Approve != 0.5 {
		t.Errorf("a subject with no answers: %+v", fresh)
	}
	if test.Source != engine.Source || test.Uncertainty <= 0 || test.Reason == "" {
		t.Errorf("prediction fields: %+v", test)
	}
}

// train teaches subject s to approve test runs, under any engine.
func train(t *testing.T, s limen.Service, subject string, n int) {
	t.Helper()
	trainIDs(t, s, subject, "t", n)
}

func trainIDs(t *testing.T, s limen.Service, subject, prefix string, n int) {
	t.Helper()
	for i := range n {
		a := bash(subject, fmt.Sprintf("%s-%d", prefix, i), "go test ./...")
		if _, err := s.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Answer(ctx, limen.Answer{Key: a.Key, Approve: true}); err != nil {
			t.Fatal(err)
		}
	}
}

// A model fitted in the background is stored, and a new engine over the
// same store predicts with it at once, without fitting.
func TestBackgroundFitAndReload(t *testing.T) {
	st := store.NewMemory()
	e := engine.New(engine.Options{Store: st, Debounce: time.Millisecond})
	train(t, e, "s", 30)
	if err := e.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	want, err := e.Predict(ctx, bash("s", "probe", "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	if _, ok, _ := st.Snapshot(ctx, "o", "s", engine.Version); !ok {
		t.Fatal("no snapshot was stored")
	}
	again := engine.New(engine.Options{Store: st, Inline: 1000})
	defer again.Close()
	got, err := again.Predict(ctx, bash("s", "probe2", "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != want.Verdict || got.Approve < 0.6 {
		t.Errorf("reloaded %+v, before %+v", got, want)
	}
}

// A small cache evicts models. An evicted model fitted through every
// answer reloads its stored snapshot, keeps what it learned, and fits
// nothing (spec 004, criterion 8).
func TestEvictionReloads(t *testing.T) {
	st := newFaulty("", 0)
	e := engine.New(engine.Options{Store: st, Inline: 1, CacheBytes: 1})
	defer e.Close()
	train(t, e, "a", 12)
	if err := e.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	train(t, e, "b", 12)
	fits, loads := st.called("PutSnapshot"), st.called("Snapshot")
	p, err := e.Predict(ctx, bash("a", "probe", "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Approve < 0.6 {
		t.Errorf("an evicted model lost what it learned: %+v", p)
	}
	if n := st.called("PutSnapshot") - fits; n != 0 {
		t.Errorf("the reload fitted %d times", n)
	}
	if n := st.called("Snapshot") - loads; n != 1 {
		t.Errorf("the reload read the snapshot %d times, want 1", n)
	}
}

func TestForgetDropsTheModel(t *testing.T) {
	st := store.NewMemory()
	e := engine.New(engine.Options{Store: st, Inline: 1})
	defer e.Close()
	train(t, e, "s", 12)
	if err := e.Forget(ctx, "o", "s"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Snapshot(ctx, "o", "s", engine.Version); ok {
		t.Error("a snapshot survived Forget")
	}
	p, err := e.Predict(ctx, bash("s", "probe", "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Approve != 0.5 {
		t.Errorf("a forgotten subject still predicts %+v", p)
	}
	if err := e.Forget(ctx, "", ""); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("forget with no subject: %v", err)
	}
}

func recs(rs []limen.ReplayRecord) iter.Seq2[limen.ReplayRecord, error] {
	return func(yield func(limen.ReplayRecord, error) bool) {
		for _, r := range rs {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// A replay predicts each record before learning its answer: on a history
// of approved test runs it suggests ask at first and allow later.
func TestReplayLearnsInOrder(t *testing.T) {
	e := engine.New(engine.Options{Debounce: time.Millisecond})
	defer e.Close()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	yes := true
	var rs []limen.ReplayRecord
	for i := range 60 {
		rs = append(rs, limen.ReplayRecord{
			At: start.Add(time.Duration(i) * time.Minute), Action: bash("s", fmt.Sprintf("r-%d", i), "go test ./..."),
			Verdict: limen.Ask, ReviewProbability: 1, Source: "manual", Approve: &yes,
		})
	}
	// Out of order on purpose: a replay sorts by time.
	rs[0], rs[59] = rs[59], rs[0]
	rep, err := e.Replay(ctx, recs(rs), limen.ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Records != 60 || rep.Answered != 60 || rep.Would[limen.Ask] == 0 || rep.Would[limen.Allow] == 0 || rep.Kept {
		t.Errorf("report %+v", rep)
	}
	page, err := e.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s"})
	if err != nil || len(page.Entries) != 0 {
		t.Errorf("a replay without keep kept %d cases, %v", len(page.Entries), err)
	}
	rep, err = e.Replay(ctx, recs(rs), limen.ReplayOptions{Keep: true})
	if err != nil || !rep.Kept {
		t.Fatalf("keep: %+v, %v", rep, err)
	}
	p, err := e.Predict(ctx, bash("s", "after", "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Verdict != limen.Allow {
		t.Errorf("a kept replay did not teach the live model: %+v", p)
	}
	if _, err := e.Replay(ctx, recs(rs[:1]), limen.ReplayOptions{Keep: true}); limen.CodeOf(err) != limen.CodeAlreadyDecided {
		t.Errorf("replaying a kept record again: %v", err)
	}
}

func TestReplayErrors(t *testing.T) {
	e := engine.New(engine.Options{})
	defer e.Close()
	boom := errors.New("boom")
	if _, err := e.Replay(ctx, func(yield func(limen.ReplayRecord, error) bool) { yield(limen.ReplayRecord{}, boom) }, limen.ReplayOptions{}); !errors.Is(err, boom) {
		t.Errorf("source error: %v", err)
	}
	if _, err := e.Replay(ctx, recs([]limen.ReplayRecord{{}}), limen.ReplayOptions{}); limen.CodeOf(err) != limen.CodeInvalidReplay {
		t.Errorf("invalid record: %v", err)
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	yes := true
	ok := limen.ReplayRecord{At: time.Now(), Action: bash("s", "a", "ls"), Verdict: limen.Ask, ReviewProbability: 1, Source: "m", Approve: &yes}
	if _, err := e.Replay(c, recs([]limen.ReplayRecord{ok}), limen.ReplayOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled replay: %v", err)
	}
}

// The model's time is the subject's decision count: two subjects with the
// same history at different wall-clock speeds predict the same.
func TestTimeCountsDecisions(t *testing.T) {
	fast := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	e := engine.New(engine.Options{Inline: 1, Clock: fast.Now})
	defer e.Close()
	train(t, e, "fast", 15)
	slow := engine.New(engine.Options{Inline: 1, Clock: func() time.Time { return fast.Now().Add(1000 * time.Hour) }})
	defer slow.Close()
	train(t, slow, "fast", 15)
	a, err := e.Predict(ctx, bash("fast", "p", "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := slow.Predict(ctx, bash("fast", "p", "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Approve != b.Approve {
		t.Errorf("wall-clock speed changed the prediction: %v vs %v", a.Approve, b.Approve)
	}
}

func TestConcurrentSubjects(t *testing.T) {
	e := engine.New(engine.Options{Debounce: time.Millisecond, Workers: 4})
	defer e.Close()
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() { trainIDs(t, e, fmt.Sprintf("s%d", w%3), fmt.Sprintf("w%d", w), 10) })
	}
	wg.Wait()
	if err := e.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

// A decision can arrive before any prediction, carrying its action, as
// after a fallback; the answer to it still trains the model.
func TestDecisionWithoutPrediction(t *testing.T) {
	e := engine.New(engine.Options{Inline: 1})
	defer e.Close()
	for i := range 12 {
		a := bash("s", fmt.Sprintf("f-%d", i), "go test ./...")
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual", Action: &a}); err != nil {
			t.Fatal(err)
		}
		if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: true}); err != nil {
			t.Fatal(err)
		}
	}
	probe := bash("s", "probe", "go test ./...")
	p, err := e.Predict(ctx, probe)
	if err != nil {
		t.Fatal(err)
	}
	if p.Approve <= 0.5 {
		t.Errorf("answers to unpredicted decisions did not train: %+v", p)
	}
	if shown := decide(t, e, probe, p); shown != p.Verdict.Shown() {
		t.Errorf("verdict %q shown %v", p.Verdict, shown)
	}
}

// Models are keyed by version and never mix (spec 004, criterion 10, as
// far as one version is built): an engine never reads the snapshot of
// another version, which here would fail to decode, and its fits leave
// that snapshot as it was, so the engine of the other version keeps
// serving from it. Forget removes the snapshots of every version.
func TestVersionsKeepTheirSnapshots(t *testing.T) {
	st := store.NewMemory()
	other := []byte("the snapshot of another model version")
	if err := st.PutSnapshot(ctx, "o", "s", "0", other); err != nil {
		t.Fatal(err)
	}
	e := engine.New(engine.Options{Store: st, Inline: 1})
	defer e.Close()
	train(t, e, "s", 12)
	if p := predictOne(t, e, "s", "probe"); p.Approve <= 0.6 || p.Source != engine.Source {
		t.Errorf("the model of this version: %+v", p)
	}
	if b, ok, err := st.Snapshot(ctx, "o", "s", "0"); err != nil || !ok || string(b) != string(other) {
		t.Errorf("the snapshot of the other version: %q, %v, %v", b, ok, err)
	}
	if _, ok, err := st.Snapshot(ctx, "o", "s", engine.Version); err != nil || !ok {
		t.Errorf("no snapshot of version %s: %v", engine.Version, err)
	}
	if err := e.Forget(ctx, "o", "s"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Snapshot(ctx, "o", "s", "0"); ok {
		t.Error("Forget kept the snapshot of another version")
	}
}
