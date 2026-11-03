// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"math/rand/v2"
	"testing"
	"time"

	"latere.ai/x/limen"
)

// weights/2 gives each rule of weights/1 its graded weight (spec 011,
// decision 3): a standing rule 0, one gesture for many and too quick 0.2,
// and a lean and a long session each a factor 0.5.
func TestGraded(t *testing.T) {
	yes := new(true)
	lean := 0.8
	late := func(r Record) Record { r.Session = "late"; return r }
	cases := []struct {
		name      string
		r         Record
		suggested *float64
		want      float64
	}{
		{"a slow approval", decidedAnswer(1, true, 3*time.Second, limen.HowAnswered{}), nil, 1},
		{"a quick approval", decidedAnswer(1, true, time.Second, limen.HowAnswered{}), nil, 0.2},
		{"one gesture for many", decidedAnswer(1, true, time.Minute, limen.HowAnswered{Surface: limen.SurfaceBulk}), nil, 0.2},
		{"one quick gesture for many", decidedAnswer(1, true, time.Second, limen.HowAnswered{Surface: limen.SurfaceBulk}), nil, 0.2},
		{"it followed the lean", decidedAnswer(1, true, time.Minute, limen.HowAnswered{SuggestionShown: yes}), &lean, 0.5},
		{"quick, and it followed the lean", decidedAnswer(1, true, time.Second, limen.HowAnswered{SuggestionShown: yes}), &lean, 0.1},
		{"late in a long session", late(decidedAnswer(9, true, time.Minute, limen.HowAnswered{})), nil, 0.5},
		{"late, and it followed the lean", late(decidedAnswer(9, true, time.Minute, limen.HowAnswered{SuggestionShown: yes})), &lean, 0.25},
		{"late, quick, and it followed the lean", late(decidedAnswer(9, true, time.Second, limen.HowAnswered{SuggestionShown: yes})), &lean, 0.05},
		{"each rule but a standing rule, with details", late(decidedAnswer(9, true, time.Second, limen.HowAnswered{Surface: limen.SurfaceBulk, SuggestionShown: yes, DetailsOpened: yes})), &lean, 1},
		{"an approval by a rule", decidedAnswer(1, true, time.Minute, limen.HowAnswered{Surface: limen.SurfaceRule, DetailsOpened: yes}), nil, 0},
		{"a quick denial", decidedAnswer(1, false, 0, limen.HowAnswered{SuggestionShown: yes}), &lean, 1},
		{"a denial by a rule", decidedAnswer(1, false, time.Minute, limen.HowAnswered{Surface: limen.SurfaceRule}), nil, 0},
	}
	// Two reviews in the session "late" come before each of its answers.
	before := []Record{late(decidedAnswer(2, true, time.Second, limen.HowAnswered{})), late(decidedAnswer(3, true, time.Second, limen.HowAnswered{}))}
	before[0].Answers[0].At, before[1].Answers[0].At = t0, t0
	p := Policy{Floor: limen.AnswerFloor, Fatigue: 2}
	for _, c := range cases {
		c.r.Suggested = c.suggested
		recs := append([]Record{c.r}, before...)
		w := p.Graded(recs)
		if w[0].Latest != c.want || w[0].Review != c.want {
			t.Errorf("%s: weights %+v, want %v", c.name, w[0], c.want)
		}
	}
	if w := DefaultPolicy().Graded([]Record{{T: 1}}); w[0] != (Weights{}) {
		t.Errorf("no answer: %+v", w[0])
	}
}

// weights/2 is weights/1 with the zeros graded: each answer of weight 1
// under weights/1 has weight 1 under weights/2, a standing rule is 0
// under both, and each other weight is between.
func TestGradedRefinesWeights(t *testing.T) {
	rng := rand.New(rand.NewPCG(2, 11))
	pick := func(xs ...string) string { return xs[rng.IntN(len(xs))] }
	flag := func() *bool {
		switch rng.IntN(3) {
		case 0:
			return nil
		case 1:
			return new(true)
		}
		return new(false)
	}
	for range 300 {
		var recs []Record
		for i := range 1 + rng.IntN(30) {
			how := limen.HowAnswered{Surface: pick("", limen.SurfacePrompt, limen.SurfaceBulk, limen.SurfaceRule, limen.SurfaceQueue),
				DetailsOpened: flag(), SuggestionShown: flag()}
			r := decidedAnswer(int64(i), rng.IntN(4) > 0, time.Duration(rng.IntN(5000))*time.Millisecond, how)
			r.Session = pick("a", "b")
			r.Shown = rng.IntN(5) > 0
			if rng.IntN(2) == 0 {
				s := rng.Float64()
				r.Suggested = &s
			}
			recs = append(recs, r)
		}
		p := Policy{Floor: limen.AnswerFloor, Fatigue: 1 + rng.IntN(6)}
		w1, w2 := p.Weigh(recs), p.Graded(recs)
		for i, r := range recs {
			a := r.Answers[0]
			switch {
			case w1[i].Latest == 1 && w2[i].Latest != 1:
				t.Fatalf("%+v: weight 1 under weights/1, %v under weights/2", r, w2[i].Latest)
			case a.Surface == limen.SurfaceRule && w2[i].Latest != 0:
				t.Fatalf("%+v: a standing rule has weight %v", r, w2[i].Latest)
			case a.Surface != limen.SurfaceRule && !(w2[i].Latest >= 0.05 && w2[i].Latest <= 1):
				t.Fatalf("%+v: weight %v", r, w2[i].Latest)
			}
		}
	}
}
