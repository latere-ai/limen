// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
)

var monday = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

func who(i int) string { return fmt.Sprintf("https://issuer.example|usr_%d", i) }

// kase is a decided case of subject by agent, received an hour into the
// week, with the prediction learned when it is not empty.
func kase(subject, agent string, v limen.Verdict, prob float64, learned limen.Verdict) ReportCase {
	k := limen.Key{Org: "o", Subject: subject, ActionID: fmt.Sprintf("%s/%d", subject, rand.Int())}
	a := limen.Action{Key: k, Kind: "tool", Name: "bash", Effect: limen.EffectWrite}
	if agent != "" {
		a.Actor = &limen.Actor{Kind: "agent", ID: agent}
	}
	c := ReportCase{Case: limen.Case{Action: a, Decision: &limen.Decision{Key: k, Verdict: v, ReviewProbability: prob, Source: "rules/1"}},
		ReceivedAt: monday.Add(time.Hour)}
	if learned != "" {
		c.Prediction = &limen.Prediction{Key: k, Verdict: learned, Source: "learned/1"}
	}
	return c
}

// answered gives the case its first answer at at, the review of a shown
// case.
func answered(c ReportCase, approve bool, at time.Time) ReportCase {
	c.Answers = append(slices.Clone(c.Answers), limen.RecordedAnswer{Approve: approve, By: c.Action.Subject, At: at, Review: c.Decision.Verdict.Shown()})
	return c
}

var inWeek = monday.Add(2 * 24 * time.Hour)

func TestWeeksAndOpenWeek(t *testing.T) {
	got := Weeks(limen.ReportQuery{Org: "o", From: monday, To: monday.Add(3 * limen.ReportWeek)})
	if len(got) != 3 || !got[0].Equal(monday) || !got[2].Equal(monday.Add(2*limen.ReportWeek)) {
		t.Errorf("Weeks = %v", got)
	}
	w := OpenWeek(monday)
	if w.State != limen.WeekOpen || !w.To.Equal(monday.Add(limen.ReportWeek)) || w.Total != nil {
		t.Errorf("OpenWeek = %+v", w)
	}
}

// A closed week with four persons is too_few with no number; with five it
// is ready, and each count is the sum over the persons (spec 010,
// criterion 9).
func TestWeekNeedsAGroup(t *testing.T) {
	var cases []ReportCase
	for i := range 4 {
		cases = append(cases, kase(who(i), "", limen.Allow, 0.05, ""), kase(who(i), "", limen.Ask, 1, ""))
	}
	// Outside the week, or with no decision: never counted.
	late := kase(who(9), "", limen.Allow, 0, "")
	late.ReceivedAt = monday.Add(limen.ReportWeek)
	early := kase(who(8), "", limen.Allow, 0, "")
	early.ReceivedAt = monday.Add(-time.Nanosecond)
	undecided := kase(who(7), "", limen.Allow, 0, "")
	undecided.Decision = nil
	cases = append(cases, late, early, undecided)
	w := Week(monday, cases)
	if w.State != limen.WeekTooFew || w.Total != nil || w.Agents != nil || w.Other != nil {
		t.Fatalf("four persons: %+v", w)
	}
	b, _ := json.Marshal(w)
	if strings.Contains(string(b), "subjects") || strings.Contains(string(b), "4") {
		t.Errorf("a too_few week tells how many took part: %s", b)
	}
	cases = append(cases, kase(who(4), "", limen.Block, 0, ""), kase(who(4), "", limen.Flag, 1, ""))
	w = Week(monday, cases)
	if w.State != limen.WeekReady || w.Total == nil {
		t.Fatalf("five persons: %+v", w)
	}
	want := limen.VerdictCounts{Allow: 4, Flag: 1, Ask: 4, Block: 1}
	if w.Total.Subjects != 5 || w.Total.Decisions != want {
		t.Errorf("total %+v, want 5 persons and %+v", *w.Total, want)
	}
	if w.Agents != nil || w.Other != nil {
		t.Errorf("a week with no agent divides: %+v", w)
	}
}

// A review after the week closed is unanswered in that week (spec 010,
// criterion 12).
func TestWeekCountsReviewsBeforeTheClose(t *testing.T) {
	closed := monday.Add(limen.ReportWeek + limen.ReportClosesAfter)
	var cases []ReportCase
	for i := range 5 {
		cases = append(cases, answered(kase(who(i), "", limen.Flag, 1, ""), false, closed.Add(-time.Nanosecond)))
	}
	cases = append(cases, answered(kase(who(0), "", limen.Flag, 1, ""), false, closed))
	w := Week(monday, cases)
	fa := w.Total.Applied.FalseAllow
	if w.Total.FlagReviews.Reviews != 5 || *w.Total.FlagReviews.Denied != 5 || fa.Reviews != 5 || fa.Unanswered != 1 {
		t.Errorf("a review at the close counted: %+v, %+v", w.Total.FlagReviews, *fa)
	}
	if cases[5].Answers[0].Review != true {
		t.Error("the week changed the caller's case")
	}
}

// The error rate of a cell is one estimate over the rows of all its cases,
// not a mean of the persons' rates.
func TestWeekPoolsTheEstimate(t *testing.T) {
	var cases []ReportCase
	var applied, learned []Row
	for i := range 5 {
		for j := range 2 + 3*i {
			c := kase(who(i), "", limen.Allow, 0.25, limen.Allow)
			if j == 0 {
				c = answered(kase(who(i), "", limen.Flag, 0.25, limen.Allow), i%2 == 0, inWeek)
			}
			cases = append(cases, c)
			r, _ := RowOf(c.Case, limen.OfApplied)
			applied = append(applied, r)
			r, _ = RowOf(c.Case, limen.OfLearned)
			learned = append(learned, r)
		}
	}
	w := Week(monday, cases)
	if got, want := *w.Total.Applied.FalseAllow, Rate(applied, FalseAllow); !reflect.DeepEqual(got, want) {
		t.Errorf("applied false allow %+v, want %+v", got, want)
	}
	if got, want := *w.Total.Learned.FalseAllow, Rate(learned, FalseAllow); !reflect.DeepEqual(got, want) {
		t.Errorf("learned false allow %+v, want %+v", got, want)
	}
	if v := w.Total.Applied.FalseBlock; v == nil || v.Value != nil {
		t.Errorf("a rate with no review is not null: %+v", v)
	}
}

// A field read from reviews shows only when its reviews come from five
// persons or more and no person gave more than half (spec 010,
// criterion 10).
func TestWeekAnswerRule(t *testing.T) {
	flags := func(perPerson ...int) []ReportCase {
		var out []ReportCase
		for i, n := range perPerson {
			out = append(out, kase(who(i), "", limen.Allow, 0, ""))
			for range n {
				out = append(out, answered(kase(who(i), "", limen.Flag, 1, ""), false, inWeek))
			}
		}
		return out
	}
	for _, c := range []struct {
		name string
		per  []int
		show bool
	}{
		{"no reviews", []int{0, 0, 0, 0, 0}, true},
		{"four persons review", []int{1, 1, 1, 1, 0}, false},
		{"five persons review", []int{1, 1, 1, 1, 1}, true},
		{"one person gave half", []int{4, 1, 1, 1, 1}, true},
		{"one person gave more than half", []int{5, 1, 1, 1, 1}, false},
	} {
		w := Week(monday, flags(c.per...))
		tot := w.Total
		shown := tot.FlagReviews.Denied != nil && tot.Applied.FalseAllow != nil
		listed := slices.Contains(tot.Withheld, "flag_reviews.denied") && slices.Contains(tot.Withheld, "applied.false_allow")
		if shown != c.show || listed == c.show {
			t.Errorf("%s: shown %v, withheld %v", c.name, shown, tot.Withheld)
		}
		if c.show && tot.Applied.FalseAllow.Reviews > 0 && *tot.FlagReviews.Denied != tot.FlagReviews.Reviews {
			t.Errorf("%s: denied %d of %d", c.name, *tot.FlagReviews.Denied, tot.FlagReviews.Reviews)
		}
		if tot.Learned.FalseAllow == nil || tot.Learned.FalseAllow.Value != nil {
			t.Errorf("%s: no prediction gives a null learned rate, got %+v", c.name, tot.Learned.FalseAllow)
		}
	}
}

// Small agents merge into the other agents, the smallest agent cell joins
// a small Other, the parts add up to the total, and a field withheld in
// one part is withheld in all (spec 010, criteria 10 and 11).
func TestWeekMergesSmallAgents(t *testing.T) {
	var cases []ReportCase
	add := func(agent string, persons []int, flagsBy ...int) {
		for _, p := range persons {
			cases = append(cases, kase(who(p), agent, limen.Allow, 0, ""))
		}
		for _, p := range flagsBy {
			cases = append(cases, answered(kase(who(p), agent, limen.Flag, 1, ""), true, inWeek))
		}
	}
	// agt_big: 7 persons with reviews from 6; agt_mid: 5 persons with
	// reviews from 2; agt_small: 3 persons; no agent: 1 person.
	add("agt_big", []int{0, 1, 2, 3, 4, 5, 6}, 0, 1, 2, 3, 4, 5)
	add("agt_mid", []int{0, 1, 2, 3, 7}, 7, 7)
	add("agt_small", []int{8, 9, 10})
	add("", []int{11})
	w := Week(monday, cases)
	// Other holds agt_small and the case with no agent: 4 persons, so the
	// smaller cell, agt_mid, joins it.
	if len(w.Agents) != 1 || w.Agents[0].Agent != "agt_big" || w.Other == nil {
		t.Fatalf("agents %+v, other %+v", w.Agents, w.Other)
	}
	if w.Agents[0].Subjects != 7 || w.Other.Subjects != 9 || w.Total.Subjects != 12 {
		t.Errorf("persons: agent %d, other %d, total %d", w.Agents[0].Subjects, w.Other.Subjects, w.Total.Subjects)
	}
	sum := w.Agents[0].Decisions
	sum.Allow += w.Other.Decisions.Allow
	sum.Flag += w.Other.Decisions.Flag
	if sum != w.Total.Decisions || w.Agents[0].FlagReviews.Reviews+w.Other.FlagReviews.Reviews != w.Total.FlagReviews.Reviews {
		t.Errorf("the parts do not add up: %+v and %+v, total %+v", w.Agents[0], *w.Other, *w.Total)
	}
	// Other's reviews come from one person, so every part withholds the
	// denials, although agt_big alone could show them; the total shows
	// them.
	for _, c := range []limen.ReportCell{w.Agents[0].ReportCell, *w.Other} {
		if c.FlagReviews.Denied != nil || !slices.Contains(c.Withheld, "flag_reviews.denied") {
			t.Errorf("a part shows the denials: %+v", c)
		}
	}
	if w.Total.FlagReviews.Denied == nil {
		t.Errorf("the total withholds the denials: %+v", w.Total.Withheld)
	}

	// With no agent cell left, the week has the total alone.
	w = Week(monday, cases[len(cases)-4:])
	if w.State != limen.WeekTooFew {
		t.Fatalf("four persons: %+v", w)
	}
	var few []ReportCase
	for i := range 6 {
		few = append(few, kase(who(i), fmt.Sprintf("agt_%d", i%2), limen.Allow, 0, ""))
	}
	w = Week(monday, few)
	if w.Total == nil || w.Agents != nil || w.Other != nil {
		t.Errorf("two agents of three persons each: %+v", w)
	}
	// A tie goes into Other by the lower agent id.
	var tie []ReportCase
	for i := range 5 {
		tie = append(tie, kase(who(i), "agt_b", limen.Allow, 0, ""), kase(who(i), "agt_a", limen.Allow, 0, ""))
	}
	tie = append(tie, kase(who(9), "", limen.Allow, 0, ""))
	w = Week(monday, tie)
	if len(w.Agents) != 1 || w.Agents[0].Agent != "agt_b" || w.Other.Subjects != 6 {
		t.Errorf("a tie: agents %+v, other %+v", w.Agents, w.Other)
	}
}

// passes is the answer rule written apart from the code under test, for
// a week whose cases all arrived inside it.
func passes(cases []ReportCase, field string) bool {
	closed := monday.Add(limen.ReportWeek + limen.ReportClosesAfter)
	by := map[string]int{}
	for _, c := range cases {
		lc := c.Case
		if rv := ReviewOf(lc); rv != nil && !rv.At.Before(closed) {
			lc.Answers = nil
		}
		switch field {
		case "flag_reviews.denied":
			if lc.Decision.Verdict == limen.Flag && ReviewOf(lc) != nil {
				by[lc.Action.Subject]++
			}
			continue
		}
		of, side := limen.OfApplied, limen.Allow
		if strings.HasPrefix(field, "learned") {
			of = limen.OfLearned
		}
		if strings.HasSuffix(field, "block") {
			side = limen.Block
		}
		if r, ok := RowOf(lc, of); ok && r.Verdict == side && r.Review > 0 && r.Shown && r.Reviewed {
			by[lc.Action.Subject]++
		}
	}
	n, most := 0, 0
	for _, v := range by {
		n += v
		most = max(most, v)
	}
	return n == 0 || (len(by) >= limen.ReportMinSubjects && 2*most <= n)
}

// shows reports whether the cell shows the field, and fails when it both
// shows and withholds it or does neither.
func shows(t *testing.T, c limen.ReportCell, field string) bool {
	t.Helper()
	var present bool
	switch field {
	case "flag_reviews.denied":
		present = c.FlagReviews.Denied != nil
	case "applied.false_allow":
		present = c.Applied.FalseAllow != nil
	case "applied.false_block":
		present = c.Applied.FalseBlock != nil
	case "learned.false_allow":
		present = c.Learned.FalseAllow != nil
	case "learned.false_block":
		present = c.Learned.FalseBlock != nil
	}
	if present == slices.Contains(c.Withheld, field) {
		t.Fatalf("%s is present %v and withheld %v", field, present, c.Withheld)
	}
	return present
}

// Over random ledgers, no week or cell shows numbers from fewer than five
// persons, the parts add up to the total, every field shown follows the
// answer rule, and a field shows in the parts only when it shows in each
// (spec 010, criterion 11).
func TestWeekProperties(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 2026))
	verdicts := []struct {
		v    limen.Verdict
		prob float64
	}{{limen.Allow, 0}, {limen.Allow, 0.2}, {limen.Flag, 0.2}, {limen.Flag, 1}, {limen.Ask, 1}, {limen.Ask, 0.2}, {limen.Block, 0.2}, {limen.Block, 0}}
	suggestions := []limen.Verdict{"", limen.Allow, limen.Ask, limen.Block}
	ready, divided, withheld := 0, 0, 0
	for range 400 {
		persons := 1 + rng.IntN(14)
		agents := rng.IntN(5)
		var cases []ReportCase
		for range rng.IntN(120) {
			agent := ""
			if agents > 0 && rng.IntN(6) > 0 {
				agent = fmt.Sprintf("agt_%d", rng.IntN(agents))
			}
			// A few persons cause most cases, as in a team.
			p := rng.IntN(persons)
			if rng.IntN(3) == 0 {
				p = 0
			}
			d := verdicts[rng.IntN(len(verdicts))]
			c := kase(who(p), agent, d.v, d.prob, suggestions[rng.IntN(len(suggestions))])
			if rng.IntN(3) > 0 {
				at := inWeek
				if rng.IntN(5) == 0 {
					at = monday.Add(limen.ReportWeek + limen.ReportClosesAfter)
				}
				c = answered(c, rng.IntN(4) > 0, at)
			}
			cases = append(cases, c)
		}
		w := Week(monday, cases)
		if w.State == limen.WeekTooFew {
			if w.Total != nil || w.Agents != nil || w.Other != nil {
				t.Fatalf("a too_few week has numbers: %+v", w)
			}
			continue
		}
		ready++
		if w.Total.Subjects < limen.ReportMinSubjects {
			t.Fatalf("a ready week of %d persons", w.Total.Subjects)
		}
		for _, f := range limen.AnswerFields() {
			if shows(t, *w.Total, f) != passes(cases, f) {
				t.Fatalf("total: %s shown %v against the rule", f, !passes(cases, f))
			}
		}
		if w.Agents == nil {
			if w.Other != nil {
				t.Fatalf("other with no agent cell: %+v", w)
			}
			continue
		}
		divided++
		parts := map[string][]ReportCase{}
		cells := map[string]limen.ReportCell{}
		for _, a := range w.Agents {
			cells[a.Agent] = a.ReportCell
		}
		var sum limen.VerdictCounts
		reviews := 0
		for _, c := range cases {
			key := agentOf(c.Case)
			if _, ok := cells[key]; !ok || key == "" {
				key = "\x00other"
			}
			parts[key] = append(parts[key], c)
		}
		if w.Other != nil {
			cells["\x00other"] = *w.Other
		}
		if len(parts) != len(cells) {
			t.Fatalf("%d parts of the cases, %d cells", len(parts), len(cells))
		}
		for key, c := range cells {
			if c.Subjects < limen.ReportMinSubjects {
				t.Fatalf("cell %q of %d persons", key, c.Subjects)
			}
			sum.Allow += c.Decisions.Allow
			sum.Flag += c.Decisions.Flag
			sum.Ask += c.Decisions.Ask
			sum.Block += c.Decisions.Block
			reviews += c.FlagReviews.Reviews
		}
		if sum != w.Total.Decisions || reviews != w.Total.FlagReviews.Reviews {
			t.Fatalf("the parts add up to %+v and %d, the total is %+v and %d", sum, reviews, w.Total.Decisions, w.Total.FlagReviews.Reviews)
		}
		for _, f := range limen.AnswerFields() {
			all := true
			for key := range cells {
				all = all && passes(parts[key], f)
			}
			for key, c := range cells {
				if shows(t, c, f) != all {
					t.Fatalf("cell %q: %s shown %v, each part passes %v", key, f, !all, all)
				}
				if !all {
					withheld++
				}
			}
			if all && !shows(t, *w.Total, f) {
				t.Fatalf("%s shows in every part and not in the total", f)
			}
		}
	}
	if ready < 100 || divided < 30 || withheld < 30 {
		t.Errorf("the ledgers reach too little: %d ready, %d divided, %d withheld", ready, divided, withheld)
	}
}
