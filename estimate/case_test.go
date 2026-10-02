// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"testing"

	"latere.ai/x/limen"
)

func TestRowOf(t *testing.T) {
	key := limen.Key{Org: "o", Subject: "s", ActionID: "a"}
	act := limen.Action{Key: key, Kind: "tool", Name: "bash", Effect: limen.EffectWrite,
		Signals: []limen.Signal{
			{Source: "classifier/x", Verdict: limen.Flag},
			{Source: "rules/1", Score: new(float64)},
			{Source: "judge/y", Verdict: limen.Block},
		}}
	pred := &limen.Prediction{Key: key, Verdict: limen.Ask, Source: "learned/1"}
	dec := &limen.Decision{Key: key, Verdict: limen.Flag, ReviewProbability: 0.05, Source: "topos/auto"}
	reviewed := limen.Case{Action: act, Prediction: pred, Decision: dec,
		Answers: []limen.RecordedAnswer{{Approve: false, Review: true}, {Approve: true}}}

	cases := []struct {
		name string
		c    limen.Case
		of   string
		ok   bool
		want Row
	}{
		{"learned", reviewed, limen.OfLearned, true, Row{Verdict: limen.Ask, Review: 0.05, Shown: true, Reviewed: true, Approve: false}},
		{"applied: a sampled flag is an allow", reviewed, limen.OfApplied, true, Row{Verdict: limen.Allow, Review: 0.05, Shown: true, Reviewed: true}},
		{"by decision source", reviewed, "topos/auto", true, Row{Verdict: limen.Allow, Review: 0.05, Shown: true, Reviewed: true}},
		{"a flagged signal is an allow", reviewed, "classifier/x", true, Row{Verdict: limen.Allow, Review: 0.05, Shown: true, Reviewed: true}},
		{"a blocking signal", reviewed, "judge/y", true, Row{Verdict: limen.Block, Review: 0.05, Shown: true, Reviewed: true}},
		{"a score-only signal gives no row", reviewed, "rules/1", false, Row{}},
		{"an unknown source gives no row", reviewed, "nobody", false, Row{}},
		{"no prediction", limen.Case{Action: act, Decision: dec}, limen.OfLearned, false, Row{}},
		{"a fallback prediction is no suggestion", limen.Case{Action: act, Prediction: &limen.Prediction{Verdict: limen.Ask, Source: limen.SourceUnavailable}}, limen.OfLearned, false, Row{}},
		{"no decision for applied", limen.Case{Action: act, Prediction: pred}, limen.OfApplied, false, Row{}},
		{"undecided learned row", limen.Case{Action: act, Prediction: pred}, limen.OfLearned, true, Row{Verdict: limen.Ask}},
	}
	for _, c := range cases {
		got, ok := RowOf(c.c, c.of)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestReviewAndPending(t *testing.T) {
	shown := &limen.Decision{Verdict: limen.Ask, ReviewProbability: 1}
	hidden := &limen.Decision{Verdict: limen.Allow, ReviewProbability: 0.05}
	cases := []struct {
		name    string
		c       limen.Case
		review  bool
		pending bool
	}{
		{"undecided", limen.Case{}, false, false},
		{"shown and waiting", limen.Case{Decision: shown}, false, true},
		{"shown and reviewed", limen.Case{Decision: shown, Answers: []limen.RecordedAnswer{{Review: true}}}, true, false},
		{"not shown", limen.Case{Decision: hidden}, false, false},
		{"not shown, a later answer", limen.Case{Decision: hidden, Answers: []limen.RecordedAnswer{{Review: false}}}, false, false},
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
