// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package gp

import (
	"bytes"
	"encoding/gob"
	"math"
	"testing"

	"latere.ai/x/limen/engine/kernel"
)

func fitted(t *testing.T) *Posterior {
	t.Helper()
	k := kernel.DefaultAdditive()
	var pts []kernel.Point
	var ys []bool
	for i := range 40 {
		x := float64(i%7) / 7
		pts = append(pts, kernel.Point{Tool: []float64{x, 1 - x}, Ctx: []float64{x * x}, T: float64(i)})
		ys = append(ys, i%3 != 0)
	}
	p, err := Fit(k, pts, ys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A state survives an encoding round trip and predicts bitwise the same.
func TestStateRoundTrip(t *testing.T) {
	p := fitted(t)
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(p.State()); err != nil {
		t.Fatal(err)
	}
	var s State
	if err := gob.NewDecoder(&buf).Decode(&s); err != nil {
		t.Fatal(err)
	}
	q, err := Restore(p.Kernel(), s)
	if err != nil {
		t.Fatal(err)
	}
	if q.Len() != p.Len() || q.LogMarginal() != p.LogMarginal() {
		t.Fatalf("restored %d answers, log Z %v; want %d, %v", q.Len(), q.LogMarginal(), p.Len(), p.LogMarginal())
	}
	for i := range 20 {
		x := float64(i) / 20
		pt := kernel.Point{Tool: []float64{x, 1 - x}, Ctx: []float64{x}, T: 45}
		if a, b := p.Predict(pt), q.Predict(pt); a != b {
			t.Fatalf("at %v: %+v, restored %+v", x, a, b)
		}
	}
}

// A state is a copy: writing to it reaches neither the posterior it came
// from nor one restored from it.
func TestStateIsACopy(t *testing.T) {
	p := fitted(t)
	s := p.State()
	q, err := Restore(p.Kernel(), s)
	if err != nil {
		t.Fatal(err)
	}
	pt := kernel.Point{Tool: []float64{0.3, 0.7}, Ctx: []float64{0.1}, T: 41}
	before := q.Predict(pt)
	s.Grad[0] += 100
	s.Train[0].Tool[0] = 9
	if p.Predict(pt) != before || q.Predict(pt) != before {
		t.Fatal("a write to the state reached a posterior")
	}
}

func TestRestoreRefusesDamage(t *testing.T) {
	p := fitted(t)
	k := p.Kernel()
	cases := map[string]func(*State){
		"sizes":    func(s *State) { s.Grad = s.Grad[1:] },
		"factor":   func(s *State) { s.L = s.L[1:] },
		"nan":      func(s *State) { s.FHat[0] = math.NaN() },
		"inf":      func(s *State) { s.LogZ = math.Inf(1) },
		"diagonal": func(s *State) { s.L[0] = 0 },
	}
	for name, damage := range cases {
		s := p.State()
		damage(&s)
		if _, err := Restore(k, s); err == nil {
			t.Errorf("%s: restored a damaged state", name)
		}
	}
	if _, err := Restore(kernel.Additive{SStatic: -1}, p.State()); err == nil {
		t.Error("restored under an invalid kernel")
	}
	q, err := Restore(k, State{})
	if err != nil || q.Len() != 0 || q.Predict(kernel.Point{}).Approve != 0.5 {
		t.Errorf("an empty state is not the prior: %v", err)
	}
}
