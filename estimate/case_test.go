// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"testing"

	"latere.ai/x/cribrum"
)

func TestRowOf(t *testing.T) {
	key := cribrum.Key{Org: "o", Subject: "s", ActionID: "a"}
	act := cribrum.Action{Key: key, Kind: "tool", Name: "bash", Effect: cribrum.EffectWrite,
		Signals: []cribrum.Signal{
			{Source: "classifier/x", Verdict: cribrum.Flag},
			{Source: "rules/1", Score: new(float64)},
			{Source: "judge/y", Verdict: cribrum.Block},
		}}
	pred := &cribrum.Prediction{Key: key, Verdict: cribrum.Ask, Source: "learned/1"}
	dec := &cribrum.Decision{Key: key, Verdict: cribrum.Flag, ReviewProbability: 0.05, Source: "topos/auto"}
	reviewed := cribrum.Case{Action: act, Prediction: pred, Decision: dec,
		Answers: []cribrum.RecordedAnswer{{Approve: false, Review: true}, {Approve: true}}}

	cases := []struct {
		name string
		c    cribrum.Case
		of   string
		ok   bool
		want Row
	}{
		{"learned", reviewed, cribrum.OfLearned, true, Row{Verdict: cribrum.Ask, Review: 0.05, Shown: true, Reviewed: true, Approve: false}},
		{"applied: a sampled flag is an allow", reviewed, cribrum.OfApplied, true, Row{Verdict: cribrum.Allow, Review: 0.05, Shown: true, Reviewed: true}},
		{"by decision source", reviewed, "topos/auto", true, Row{Verdict: cribrum.Allow, Review: 0.05, Shown: true, Reviewed: true}},
		{"a flagged signal is an allow", reviewed, "classifier/x", true, Row{Verdict: cribrum.Allow, Review: 0.05, Shown: true, Reviewed: true}},
		{"a blocking signal", reviewed, "judge/y", true, Row{Verdict: cribrum.Block, Review: 0.05, Shown: true, Reviewed: true}},
		{"a score-only signal gives no row", reviewed, "rules/1", false, Row{}},
		{"an unknown source gives no row", reviewed, "nobody", false, Row{}},
		{"no prediction", cribrum.Case{Action: act, Decision: dec}, cribrum.OfLearned, false, Row{}},
		{"a fallback prediction is no suggestion", cribrum.Case{Action: act, Prediction: &cribrum.Prediction{Verdict: cribrum.Ask, Source: cribrum.SourceUnavailable}}, cribrum.OfLearned, false, Row{}},
		{"no decision for applied", cribrum.Case{Action: act, Prediction: pred}, cribrum.OfApplied, false, Row{}},
		{"undecided learned row", cribrum.Case{Action: act, Prediction: pred}, cribrum.OfLearned, true, Row{Verdict: cribrum.Ask}},
	}
	for _, c := range cases {
		got, ok := RowOf(c.c, c.of)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestReviewAndPending(t *testing.T) {
	shown := &cribrum.Decision{Verdict: cribrum.Ask, ReviewProbability: 1}
	hidden := &cribrum.Decision{Verdict: cribrum.Allow, ReviewProbability: 0.05}
	cases := []struct {
		name    string
		c       cribrum.Case
		review  bool
		pending bool
	}{
		{"undecided", cribrum.Case{}, false, false},
		{"shown and waiting", cribrum.Case{Decision: shown}, false, true},
		{"shown and reviewed", cribrum.Case{Decision: shown, Answers: []cribrum.RecordedAnswer{{Review: true}}}, true, false},
		{"not shown", cribrum.Case{Decision: hidden}, false, false},
		{"not shown, a later answer", cribrum.Case{Decision: hidden, Answers: []cribrum.RecordedAnswer{{Review: false}}}, false, false},
	}
	for _, c := range cases {
		if got := ReviewOf(c.c) != nil; got != c.review {
			t.Errorf("%s: ReviewOf present %v, want %v", c.name, got, c.review)
		}
		if got := Pending(c.c); got != c.pending {
			t.Errorf("%s: Pending %v, want %v", c.name, got, c.pending)
		}
	}
}
