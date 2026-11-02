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
	"latere.ai/x/limen/engine/store"
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

// The engine and the stub give the same scope and cover for the same
// ledger, and the same verdict and kind of a capped ask for the same
// approval probability (spec 011, criterion 11). One scripted history of
// shell actions and publishes, of several reaches, sessions and days, with
// answers given in every way weights/1 reads, goes to both at the same
// times; then each probe gets the same scope and cover from each, in its
// prediction, its explanation and the learned entries. The lean a client
// showed is read from a recorded prediction, which the history leaves out
// because the two predict different probabilities.
func TestEngineAndStubCoverAlike(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	now := start
	clock := func() time.Time { return now }
	st := store.NewMemory()
	e := engine.New(engine.Options{Inline: 1, Clock: clock, Store: st})
	defer e.Close()
	approves := map[string]float64{}
	s := stub.New(stub.Options{Clock: clock, Predict: func(a limen.Action) (float64, float64) {
		if p, ok := approves[a.ActionID]; ok {
			return p, 0.1
		}
		return 0.5, 1
	}})
	services := []limen.Service{e, s}
	for _, svc := range services {
		if _, err := svc.PutProfile(ctx, limen.Profile{Org: "o", Costs: balanced, AuditRate: 0.05}); err != nil {
			t.Fatal(err)
		}
	}
	actions := []func(id string) limen.Action{
		func(id string) limen.Action { return bash("s", id, "go test ./...") },
		func(id string) limen.Action { return bash("s", id, "git push origin feature && go test ./...") },
		func(id string) limen.Action { return bash("s", id, "git push --force origin main") },
		func(id string) limen.Action { return bash("s", id, "rm -rf ~/.cache") },
		func(id string) limen.Action { return publish(id, "x") },
		func(id string) limen.Action { return makePublic(id, "x") },
	}
	hows := []limen.HowAnswered{{}, considered, {Surface: limen.SurfaceBulk}, {Surface: limen.SurfaceRule}, {SuggestionShown: new(true)},
		{AnswerMS: new(int64(900))}, {WaitedMS: new(int64(60000)), AnswerMS: new(int64(4000))}}
	rng := rand.New(rand.NewPCG(9, 3))
	for i := range 150 {
		a := actions[rng.IntN(len(actions))](fmt.Sprintf("h-%d", i))
		a.Actor.Session = []string{"", "ses-a", "ses-b", "ses-c"}[rng.IntN(4)]
		approves[a.ActionID] = rng.Float64()
		now = now.Add(time.Duration(rng.IntN(5)) * time.Hour)
		// The decision carries the action, so neither records a prediction
		// whose approval probability a lean shown to the person would read.
		for _, svc := range services {
			if err := svc.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual", Action: &a}); err != nil {
				t.Fatal(err)
			}
		}
		if rng.IntN(5) == 0 {
			continue
		}
		approve, how := rng.Float64() < 0.8, hows[rng.IntN(len(hows))]
		now = now.Add(time.Duration(rng.IntN(5)) * time.Second)
		for _, svc := range services {
			if err := svc.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve, HowAnswered: how}); err != nil {
				t.Fatal(err)
			}
		}
	}
	kinds := map[string]bool{}
	var warm []limen.Prediction
	for i, mk := range actions {
		a := mk(fmt.Sprintf("probe-%d", i))
		a.Actor.Session = "ses-a"
		pe, err := e.Predict(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		approves[a.ActionID] = pe.Approve
		warm = append(warm, pe)
		ps, err := s.Predict(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if pe.Scope != ps.Scope || pe.Cover != ps.Cover {
			t.Errorf("probe %d: the engine reads %+v %+v, the stub %+v %+v", i, pe.Scope, pe.Cover, ps.Scope, ps.Cover)
		}
		if (pe.Verdict == limen.Allow || pe.AskKind == limen.AskWider || pe.AskKind == limen.AskFew) && (pe.Verdict != ps.Verdict || pe.AskKind != ps.AskKind) {
			t.Errorf("probe %d: the engine suggests %s %q, the stub %s %q", i, pe.Verdict, pe.AskKind, ps.Verdict, ps.AskKind)
		}
		kinds[pe.AskKind] = true
		xe, err := e.Explain(ctx, a.Key)
		if err != nil {
			t.Fatal(err)
		}
		xs, err := s.Explain(ctx, a.Key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(xe.Cover, xs.Cover) {
			t.Errorf("probe %d: the engine explains %+v, the stub %+v", i, xe.Cover, xs.Cover)
		}
	}
	le, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := s.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	covers := func(l limen.Learned) map[string]limen.Cover {
		out := map[string]limen.Cover{}
		for _, x := range l.Entries {
			out[x.Label] = x.Cover
		}
		return out
	}
	if ce, cs := covers(le), covers(ls); !reflect.DeepEqual(ce, cs) || len(ce) < 4 {
		t.Errorf("the learned entries: the engine %+v, the stub %+v", ce, cs)
	}
	if !kinds[limen.AskWider] && !kinds[limen.AskFew] && !kinds[""] {
		t.Errorf("the script reaches no capped ask and no allow: %v", kinds)
	}
	// An engine that loads the ledger from the store, as after a restart,
	// reads the same scopes and covers as the engine that kept its ledger
	// with each write.
	e.Close()
	cold := engine.New(engine.Options{Inline: 1, Clock: clock, Store: st})
	defer cold.Close()
	for i, mk := range actions {
		a := mk(fmt.Sprintf("cold-probe-%d", i))
		a.Actor.Session = "ses-a"
		pc, err := cold.Predict(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if pc.Scope != warm[i].Scope || pc.Cover != warm[i].Cover {
			t.Errorf("probe %d: the loaded ledger reads %+v %+v, the kept one %+v %+v", i, pc.Scope, pc.Cover, warm[i].Scope, warm[i].Cover)
		}
	}
}
