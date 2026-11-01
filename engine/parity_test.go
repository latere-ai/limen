// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/stub"
)

// The engine and the stub report identical estimates for an identical
// ledger (spec 008, criterion 7; spec 006, criterion 4). One scripted
// history of decisions of every verdict and review probability, two
// decision sources, a signal source, reviews, later answers, answers to
// actions nobody saw, and actions not yet answered goes to both, at the
// same times, under a profile with its own claim. Every report, over the
// whole history and over a part of it, is the same.
func TestEngineAndStubReportAlike(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	now := start
	clock := func() time.Time { return now }
	e := engine.New(engine.Options{Inline: 1, Clock: clock})
	defer e.Close()
	s := stub.New(stub.Options{Clock: clock})
	services := []limen.Service{e, s}

	profile := limen.Profile{Org: "o", Costs: limen.DefaultCosts, AuditRate: 0.1, Certify: limen.Certify{Alpha: 0.2, Delta: 0.1}}
	for _, svc := range services {
		if _, err := svc.PutProfile(ctx, profile); err != nil {
			t.Fatal(err)
		}
	}
	decisions := []struct {
		verdict limen.Verdict
		prob    float64
	}{
		{limen.Allow, 0}, {limen.Allow, 0.1}, {limen.Flag, 0.1}, {limen.Flag, 1},
		{limen.Ask, 1}, {limen.Ask, 0.1}, {limen.Block, 0}, {limen.Block, 0.1},
	}
	signals := []limen.Verdict{"", limen.Allow, limen.Flag, limen.Ask, limen.Block}
	rng := rand.New(rand.NewPCG(4, 8))
	for i := range 200 {
		a := bash("s", fmt.Sprintf("p-%d", i), "go test ./...")
		if v := signals[rng.IntN(len(signals))]; v != "" {
			a.Signals = []limen.Signal{{Source: "classifier/x", Verdict: v}}
		}
		d := decisions[rng.IntN(len(decisions))]
		source := []string{"rules/1", "topos/progressive"}[rng.IntN(2)]
		answers := rng.IntN(4) // none, one, two, or none for a shown action
		now = now.Add(time.Minute)
		for _, svc := range services {
			if err := svc.Record(ctx, limen.Decision{Key: a.Key, Verdict: d.verdict, ReviewProbability: d.prob, Source: source, Action: &a}); err != nil {
				t.Fatal(err)
			}
		}
		for range answers % 3 {
			approve := rng.Float64() < 0.8
			now = now.Add(time.Second)
			for _, svc := range services {
				if err := svc.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	windows := [][2]time.Time{{start, now.Add(time.Minute)}, {start.Add(50 * time.Minute), start.Add(150 * time.Minute)}}
	for _, of := range []string{limen.OfApplied, limen.OfLearned, "rules/1", "topos/progressive", "classifier/x"} {
		for _, w := range windows {
			q := limen.ReliabilityQuery{Org: "o", Subject: "s", Of: of, From: w[0], To: w[1]}
			want, err := s.Reliability(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Reliability(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s over %v: the engine reports %+v, the stub %+v", of, w, got, want)
			}
			if of == limen.OfApplied && w == windows[0] && (want.FalseAllow.Value == nil || want.FalseBlock.Value == nil || want.FalseAllow.Unanswered == 0 || want.Certificate.Clean == 0) {
				t.Errorf("the script does not reach every part of a report: %+v", want)
			}
		}
	}
}
