// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
)

// answered returns an engine and the n answered cases of its subject s.
func answered(tb testing.TB, n int) (*Engine, []store.Case) {
	tb.Helper()
	e := New(Options{Debounce: time.Hour})
	tb.Cleanup(func() { e.Close() })
	in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	for i := range n {
		a := limen.Action{Org: "o", Subject: "s", ActionID: fmt.Sprint(i), Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in}
		if _, err := e.Predict(context.Background(), a); err != nil {
			tb.Fatal(err)
		}
		if err := e.Record(context.Background(), limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "m"}); err != nil {
			tb.Fatal(err)
		}
		if err := e.Answer(context.Background(), limen.Answer{Key: a.Key, Approve: i%4 != 0}); err != nil {
			tb.Fatal(err)
		}
	}
	cases, err := e.st.Cases(context.Background(), "o", "s")
	if err != nil {
		tb.Fatal(err)
	}
	return e, cases
}

// BenchmarkCalibrationOf is the part of an explanation that its
// calibration adds, for a person with 512 and with 4096 cases: one more
// linear pass over the cases the explanation has read, and the weights of
// their answers. It is small beside BenchmarkExplain, so the engine keeps
// no cache of reports.
func BenchmarkCalibrationOf(b *testing.B) {
	for _, n := range []int{512, 4096} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			e, cases := answered(b, n)
			recs := records(cases)
			c := cases[n/2]
			for b.Loop() {
				if e.calibrationOf(c, cases, recs) == "" {
					b.Fatal("no calibration")
				}
			}
		})
	}
}
