// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/stub"
)

// The engine and the stub give the same calibration report for the same
// ledger, and the same calibration on each explanation (spec 006,
// criterion 4; spec 011). The stub states the probabilities the engine
// predicted, and both record the same decisions and answers at the same
// times: asks, audited and unaudited allows and blocks, and undecided
// predictions, answered in every way weights/1 reads.
func TestEngineAndStubCalibrateAlike(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	now := start
	clock := func() time.Time { return now }
	e := engine.New(engine.Options{Inline: 8, Clock: clock})
	defer e.Close()
	approves := map[string]float64{}
	s := stub.New(stub.Options{Clock: clock, Predict: func(a limen.Action) (float64, float64) { return approves[a.ActionID], 0.1 }})
	services := []limen.Service{e, s}
	commands := []struct {
		cmd     string
		approve float64
	}{{"go test ./...", 0.97}, {"rm -rf build", 0.1}, {"git push origin feature", 0.6}, {"ls", 0.99}}
	decisions := []struct {
		verdict limen.Verdict
		prob    float64
	}{{limen.Ask, 1}, {limen.Ask, 1}, {limen.Ask, 1}, {limen.Flag, 0.2}, {limen.Allow, 0.2}, {limen.Allow, 0}, {limen.Block, 0.2}, {limen.Ask, 0.2}, {"", 0}}
	hows := []limen.HowAnswered{{}, considered, {Surface: limen.SurfaceBulk}, {SuggestionShown: new(true)}}
	rng := rand.New(rand.NewPCG(7, 1))
	for i := range 400 {
		c := commands[rng.IntN(len(commands))]
		a := bash("s", fmt.Sprintf("c-%d", i), c.cmd)
		now = now.Add(time.Minute)
		p, err := e.Predict(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		approves[a.ActionID] = p.Approve
		if _, err := s.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		d := decisions[rng.IntN(len(decisions))]
		if d.verdict == "" {
			continue
		}
		for _, svc := range services {
			if err := svc.Record(ctx, limen.Decision{Key: a.Key, Verdict: d.verdict, ReviewProbability: d.prob, Source: "test"}); err != nil {
				t.Fatal(err)
			}
		}
		if rng.IntN(6) == 0 {
			continue
		}
		approve, how := rng.Float64() < c.approve, hows[rng.IntN(len(hows))]
		now = now.Add(time.Duration(rng.IntN(5)) * time.Second)
		for _, svc := range services {
			if err := svc.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve, HowAnswered: how}); err != nil {
				t.Fatal(err)
			}
		}
	}
	states := map[string]bool{}
	for _, w := range [][2]time.Time{{start, now.Add(time.Minute)}, {start.Add(100 * time.Minute), start.Add(300 * time.Minute)}} {
		q := limen.CalibrationQuery{Org: "o", Subject: "s", From: w[0], To: w[1]}
		want, err := s.Calibration(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.Calibration(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("over %v: the engine reports %+v, the stub %+v", w, got, want)
		}
		for _, b := range want.Bins {
			states[b.State] = true
		}
	}
	if !states[limen.NotEstimable] || len(states) < 2 {
		t.Errorf("the script reaches the states %v only", states)
	}
	for i := range 400 {
		k := limen.Key{Org: "o", Subject: "s", ActionID: fmt.Sprintf("c-%d", i)}
		xe, err := e.Explain(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		xs, err := s.Explain(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		if xe.Calibration != xs.Calibration || xe.Calibration == "" {
			t.Fatalf("%s: the engine explains %q, the stub %q", k.ActionID, xe.Calibration, xs.Calibration)
		}
	}
}

// An explanation reads the state of the bin of its recorded prediction in
// the report of the last 90 days, at the time it is asked for: a case that
// left the window no longer counts, and an action with no prediction has
// no calibration (spec 011).
func TestExplanationReadsTheLast90Days(t *testing.T) {
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	e := engine.New(engine.Options{Inline: 1, Clock: func() time.Time { return now }})
	defer e.Close()
	var keys []limen.Key
	for i := range 24 {
		a := bash("s", fmt.Sprintf("old-%d", i), "go test ./...")
		if _, err := e.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, a.Key)
	}
	for i, k := range keys {
		if err := e.Record(ctx, limen.Decision{Key: k, Verdict: limen.Ask, ReviewProbability: 1, Source: "m"}); err != nil {
			t.Fatal(err)
		}
		if err := e.Answer(ctx, limen.Answer{Key: k, Approve: i%6 != 0}); err != nil {
			t.Fatal(err)
		}
	}
	explain := func(k limen.Key) limen.Explanation {
		t.Helper()
		x, err := e.Explain(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	// 24 predictions of 0.5, 20 of the reviews approve: 5/6 is far from
	// 0.5 for 24 reviews, and 0.5 is nearer to 1/2.
	c, err := e.Calibration(ctx, limen.CalibrationQuery{Org: "o", Subject: "s", From: now.Add(-limen.CalibrationWindow), To: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if b := c.Bins[5]; b.Cases != 24 || b.State != limen.Underconfident {
		t.Fatalf("the bin of 0.5: %+v", b)
	}
	if x := explain(keys[0]); x.Calibration != limen.Underconfident {
		t.Errorf("at once: %q", x.Calibration)
	}
	now = now.Add(limen.CalibrationWindow - time.Second)
	if x := explain(keys[3]); x.Calibration != limen.Underconfident {
		t.Errorf("a second before the cases leave the window: %q", x.Calibration)
	}
	now = now.Add(2 * time.Second)
	if x := explain(keys[3]); x.Calibration != limen.NotEstimable {
		t.Errorf("after the cases left the window: %q", x.Calibration)
	}
	a := bash("s", "unpredicted", "go test ./...")
	if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "m", Action: &a}); err != nil {
		t.Fatal(err)
	}
	if x := explain(a.Key); x.Calibration != "" {
		t.Errorf("an action with no prediction: %q", x.Calibration)
	}
}

// A store that fails surfaces as an error, and a query is held to the
// contract before anything is read.
func TestCalibrationErrors(t *testing.T) {
	q := limen.CalibrationQuery{Org: "o", Subject: "s", From: time.Unix(0, 0), To: time.Now().Add(time.Hour)}
	f := newFaulty("Cases", 0)
	e := engine.New(engine.Options{Store: f, Inline: 1})
	defer e.Close()
	if _, err := e.Calibration(ctx, q); !errors.Is(err, errStore) {
		t.Errorf("a store that fails: %v", err)
	}
	if _, err := e.Calibration(ctx, limen.CalibrationQuery{Org: "o"}); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("a query with no subject: %v", err)
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.Calibration(c, q); !errors.Is(err, context.Canceled) {
		t.Errorf("under a canceled context: %v", err)
	}
}

// history returns an engine whose subject s has n answered cases, the
// model fitted once on the most recent 512 of them and held in memory.
func history(tb testing.TB, n int) *engine.Engine {
	tb.Helper()
	e := engine.New(engine.Options{Debounce: time.Hour})
	tb.Cleanup(func() { e.Close() })
	for i := range n {
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
	return e
}

// BenchmarkExplain is one explanation of a recorded case against a warm
// model, for a person with 512 and with 4096 cases. An explanation reads
// every case of the person, so its time grows with the history.
func BenchmarkExplain(b *testing.B) {
	for _, n := range []int{512, 4096} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			e := history(b, n)
			k := limen.Key{Org: "o", Subject: "s", ActionID: fmt.Sprintf("t-%d", n/2)}
			var ds []time.Duration
			for b.Loop() {
				start := time.Now()
				if _, err := e.Explain(ctx, k); err != nil {
					b.Fatal(err)
				}
				ds = append(ds, time.Since(start))
			}
			b.ReportMetric(float64(p99(ds).Microseconds())/1000, "p99-ms")
		})
	}
}
