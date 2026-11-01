// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
)

// predictOne predicts a new test run for subject s.
func predictOne(t testing.TB, e *engine.Engine, s, id string) limen.Prediction {
	t.Helper()
	p, err := e.Predict(ctx, bash(s, id, "go test ./x"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A prediction never fits (spec 004, criterion 6). With answers waiting
// for the background fitter, a thousand predictions fit nothing and serve
// the posterior they find. The time in the bubble is a fake clock, so the
// fitter's debounce passes only when the test sleeps.
func TestPredictionNeverFits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := newFaulty("", 0)
		e := engine.New(engine.Options{Store: st})
		defer e.Close()
		train(t, e, "s", 30)
		for i := range 1000 {
			if p := predictOne(t, e, "s", fmt.Sprintf("probe-%d", i)); p.Approve != 0.5 {
				t.Fatalf("prediction %d moved before any fit: %+v", i, p)
			}
		}
		if n := st.called("PutSnapshot"); n != 0 {
			t.Fatalf("%d fits during predictions", n)
		}
		time.Sleep(5 * time.Second)
		if n := st.called("PutSnapshot"); n != 1 {
			t.Fatalf("%d fits once the debounce passed, want 1", n)
		}
		if p := predictOne(t, e, "s", "after"); p.Approve <= 0.6 {
			t.Errorf("the fitted model does not serve: %+v", p)
		}
	})
}

// A model with new answers is refitted in the background within five
// seconds of its newest answer, and the next prediction uses the new
// snapshot (spec 004, criterion 7). The clock is the fake one of the
// bubble: the fit is due after the one second debounce, and answers that
// arrive after a fit are fitted by the next one.
func TestBackgroundFitWithinFiveSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := newFaulty("", 0)
		e := engine.New(engine.Options{Store: st, Workers: 2})
		defer e.Close()
		train(t, e, "s", 25)
		if p := predictOne(t, e, "s", "before"); p.Approve != 0.5 {
			t.Fatalf("a model with no fit: %+v", p)
		}
		start := time.Now()
		time.Sleep(time.Second - time.Millisecond)
		if n := st.called("PutSnapshot"); n != 0 {
			t.Fatalf("%d fits before the debounce passed", n)
		}
		time.Sleep(2 * time.Millisecond)
		if n := st.called("PutSnapshot"); n != 1 {
			t.Fatalf("%d fits %v after the newest answer, want 1", n, time.Since(start))
		}
		first := predictOne(t, e, "s", "after")
		if first.Approve <= 0.6 {
			t.Fatalf("the next prediction does not use the new snapshot: %+v", first)
		}

		trainIDs(t, e, "s", "more", 10)
		time.Sleep(5 * time.Second)
		if n := st.called("PutSnapshot"); n != 2 {
			t.Fatalf("%d fits after more answers, want 2", n)
		}
		if p := predictOne(t, e, "s", "later"); p.Approve <= first.Approve {
			t.Errorf("ten more approvals did not move the model: %+v after %+v", p, first)
		}
	})
}

// warm512 returns an engine whose subject s has a fitted snapshot of 512
// answered cases, the label cap, fitted once and held in memory.
func warm512(tb testing.TB) (*engine.Engine, *faulty) {
	tb.Helper()
	st := newFaulty("", 0)
	e := engine.New(engine.Options{Store: st, Debounce: time.Hour})
	tb.Cleanup(func() { e.Close() })
	for i := range 512 {
		cmd := "go test ./..."
		approve := i%4 != 0
		if !approve {
			cmd = "rm -rf build"
		}
		a := bash("s", fmt.Sprintf("t-%d", i), cmd)
		if _, err := e.Predict(ctx, a); err != nil {
			tb.Fatal(err)
		}
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual"}); err != nil {
			tb.Fatal(err)
		}
		if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve}); err != nil {
			tb.Fatal(err)
		}
	}
	if err := e.Flush(ctx); err != nil {
		tb.Fatal(err)
	}
	if n := st.called("PutSnapshot"); n != 1 {
		tb.Fatalf("%d fits to warm the model, want 1", n)
	}
	return e, st
}

// p99 is the 99th percentile of the durations.
func p99(ds []time.Duration) time.Duration {
	slices.Sort(ds)
	return ds[(len(ds)*99+99)/100-1]
}

// Against a warm snapshot at n = 512, predictions read the snapshot in
// memory: they neither fit nor load it (spec 004, criterion 6). The time
// they take is logged; BenchmarkPredictWarm512 reports its 99th
// percentile, which the spec bounds at 5 milliseconds on the reference
// runner.
func TestWarmPredictionAt512(t *testing.T) {
	if testing.Short() {
		t.Skip("fits a model of 512 answers")
	}
	e, st := warm512(t)
	loads := st.called("Snapshot")
	ds := make([]time.Duration, 0, 500)
	for i := range cap(ds) {
		start := time.Now()
		predictOne(t, e, "s", fmt.Sprintf("probe-%d", i))
		ds = append(ds, time.Since(start))
	}
	if n := st.called("PutSnapshot"); n != 1 {
		t.Errorf("%d fits after the predictions, want the one that warmed the model", n)
	}
	if n := st.called("Snapshot"); n != loads {
		t.Errorf("predictions loaded the snapshot %d times", n-loads)
	}
	t.Logf("p99 of %d predictions at n = 512: %v", len(ds), p99(ds))
}

// BenchmarkPredictWarm512 is criterion 6's benchmark: one prediction of a
// new action against a warm snapshot of 512 answered cases, with the 99th
// percentile reported as p99-ms.
func BenchmarkPredictWarm512(b *testing.B) {
	e, _ := warm512(b)
	var ds []time.Duration
	i := 0
	for b.Loop() {
		start := time.Now()
		predictOne(b, e, "s", fmt.Sprintf("probe-%d", i))
		ds = append(ds, time.Since(start))
		i++
	}
	b.ReportMetric(float64(p99(ds).Microseconds())/1000, "p99-ms")
}
