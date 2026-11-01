// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package gp

import (
	"math"
	"math/rand/v2"
	"testing"

	"latere.ai/x/limen/engine/kernel"
)

// Spec 009, criteria 1 and 2: the contributions and the parts each sum to
// the mean, and the explained prediction is the prediction.
func TestExplainSumsToTheMean(t *testing.T) {
	for seed := range uint64(12) {
		rng := rand.New(rand.NewPCG(seed, seed))
		pts, ys := randTrain(rng, 5+int(seed)*7)
		post := must(Fit(kernel.DefaultAdditive(), pts, ys, Options{}))
		for range 5 {
			q, _ := randTrain(rng, 1)
			q[0].T = float64(len(pts)) + rng.Float64()*20
			e := post.Explain(q[0])
			if p := post.Predict(q[0]); p != e.Prediction {
				t.Fatalf("seed %d: explained %+v, predicted %+v", seed, e.Prediction, p)
			}
			sum := 0.0
			for _, c := range e.Contributions {
				sum += c
			}
			if math.Abs(sum-e.Mean) > 1e-9 {
				t.Errorf("seed %d: contributions sum to %v, mean %v", seed, sum, e.Mean)
			}
			if len(e.Parts) != 3 || math.Abs(e.Parts[0]+e.Parts[1]+e.Parts[2]-e.Mean) > 1e-9 {
				t.Errorf("seed %d: parts %v, mean %v", seed, e.Parts, e.Mean)
			}
			if !(e.EvidenceRatio >= 0 && e.EvidenceRatio < 1) || math.Abs(e.EvidenceRatio-(1-e.Var/e.Prior)) > 1e-12 {
				t.Errorf("seed %d: evidence ratio %v of var %v prior %v", seed, e.EvidenceRatio, e.Var, e.Prior)
			}
			if len(e.Points) != len(e.Contributions) || len(e.Points) != post.Len() {
				t.Errorf("seed %d: %d points, %d contributions, %d fitted", seed, len(e.Points), len(e.Contributions), post.Len())
			}
		}
	}
}

// An approval pushes the mean up and a denial down: a person who approved
// one action and denied another gets a positive and a negative term.
func TestExplainSigns(t *testing.T) {
	a := kernel.Point{Tool: []float64{1, 0}, Ctx: []float64{0}, T: 0}
	d := kernel.Point{Tool: []float64{0, 1}, Ctx: []float64{0}, T: 1}
	post := must(Fit(kernel.DefaultAdditive(), []kernel.Point{a, d}, []bool{true, false}, Options{}))
	q := a
	q.T = 2
	e := post.Explain(q)
	if !(e.Contributions[0] > 0 && e.Contributions[1] < 0) {
		t.Errorf("contributions %v, want the approval positive and the denial negative", e.Contributions)
	}
}

// A kernel without parts explains with contributions alone, and a person
// with no answers has nothing to explain.
func TestExplainWithoutParts(t *testing.T) {
	pts, ys := randTrain(rand.New(rand.NewPCG(5, 5)), 20)
	e := must(Fit(kernel.DefaultProduct(), pts, ys, Options{})).Explain(pts[0])
	if e.Parts != nil || len(e.Contributions) != 20 {
		t.Errorf("product kernel: parts %v, %d contributions", e.Parts, len(e.Contributions))
	}
	p := Prior(kernel.DefaultAdditive()).Explain(pts[0])
	if p.Mean != 0 || p.EvidenceRatio != 0 || len(p.Contributions) != 0 || p.Approve != 0.5 {
		t.Errorf("prior explanation %+v", p)
	}
}
