// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"maps"
	"slices"
	"time"

	"latere.ai/x/limen"
)

// ReportCase is one case of a person of an organization as the report of
// the organization reads it: the case and when Limen received it.
type ReportCase struct {
	limen.Case
	ReceivedAt time.Time
}

// Weeks returns the start of each week of the query, oldest first.
func Weeks(q limen.ReportQuery) []time.Time {
	var out []time.Time
	for w := q.From.UTC(); w.Before(q.To); w = w.Add(limen.ReportWeek) {
		out = append(out, w)
	}
	return out
}

// OpenWeek is the entry of a week that has not closed: its bounds alone.
func OpenWeek(from time.Time) limen.WeekReport {
	return limen.WeekReport{From: from.UTC(), To: from.Add(limen.ReportWeek).UTC(), State: limen.WeekOpen}
}

// Week is the entry of the closed week that starts at from (spec 010),
// built from the cases of every person of the organization. Every ledger
// builds its weeks here, so two of them report the same week for the same
// cases.
//
// The week holds the cases with a decision and a receive time in it, and
// a person took part when one of their cases is there. A review that
// arrived after the week closed counts as unanswered. With fewer than
// [limen.ReportMinSubjects] persons the week is too_few and shows
// nothing more. Otherwise Total holds the whole week, and the cases divide
// by agent:
//
//  1. An agent with fewer than the minimum of persons goes into Other,
//     beside the cases with no agent.
//  2. While Other has cases and fewer than the minimum of persons, the
//     agent cell with the fewest persons goes into it, the lower agent id
//     first on a tie.
//  3. When no agent cell is left, the week has Total alone.
//
// Each field read from reviews shows in a cell only when its reviews come
// from the minimum of persons and no person gave more than half of them.
// Across the division a field shows in the agents' cells and Other only
// when it shows in each of them, so no withheld number is Total less the
// others.
func Week(from time.Time, cases []ReportCase) limen.WeekReport {
	w := OpenWeek(from)
	w.State = limen.WeekTooFew
	to := from.Add(limen.ReportWeek)
	closed := to.Add(limen.ReportClosesAfter)
	var in []limen.Case
	for _, c := range cases {
		if c.Decision == nil || c.ReceivedAt.Before(from) || !c.ReceivedAt.Before(to) {
			continue
		}
		in = append(in, reviewedBefore(c.Case, closed))
	}
	total := newGroup(in)
	if len(total.subjects) < limen.ReportMinSubjects {
		return w
	}
	w.State = limen.WeekReady
	cell := total.cell(total.passing())
	w.Total = &cell

	byAgent := map[string][]limen.Case{}
	var other []limen.Case
	for _, c := range in {
		if a := agentOf(c); a != "" {
			byAgent[a] = append(byAgent[a], c)
		} else {
			other = append(other, c)
		}
	}
	type part struct {
		agent string
		g     group
	}
	var parts []part
	for _, a := range slices.Sorted(maps.Keys(byAgent)) {
		g := newGroup(byAgent[a])
		if len(g.subjects) < limen.ReportMinSubjects {
			other = append(other, byAgent[a]...)
			continue
		}
		parts = append(parts, part{a, g})
	}
	for len(other) > 0 && len(parts) > 0 && len(newGroup(other).subjects) < limen.ReportMinSubjects {
		i := 0
		for j, p := range parts {
			if len(p.g.subjects) < len(parts[i].g.subjects) {
				i = j
			}
		}
		other = append(other, parts[i].g.cases...)
		parts = slices.Delete(parts, i, i+1)
	}
	if len(parts) == 0 {
		return w
	}
	groups := make([]group, 0, len(parts)+1)
	for _, p := range parts {
		groups = append(groups, p.g)
	}
	if len(other) > 0 {
		groups = append(groups, newGroup(other))
	}
	shown := map[string]bool{}
	for _, f := range limen.AnswerFields() {
		shown[f] = true
		for _, g := range groups {
			shown[f] = shown[f] && g.passing()[f]
		}
	}
	for i, p := range parts {
		w.Agents = append(w.Agents, limen.AgentCell{Agent: p.agent, ReportCell: groups[i].cell(shown)})
	}
	if len(other) > 0 {
		c := groups[len(groups)-1].cell(shown)
		w.Other = &c
	}
	return w
}

// reviewedBefore is the case as a week that closed at closed sees it: a
// review that arrived later is no review yet.
func reviewedBefore(c limen.Case, closed time.Time) limen.Case {
	c.Answers = slices.Clone(c.Answers)
	for i := range c.Answers {
		if c.Answers[i].Review && !c.Answers[i].At.Before(closed) {
			c.Answers[i].Review = false
		}
	}
	return c
}

// agentOf is the agent that proposed the case, or "" for none.
func agentOf(c limen.Case) string {
	if a := c.Action.Actor; a != nil && a.Kind == "agent" {
		return a.ID
	}
	return ""
}

// group is one cell's cases and what the rules of a report read of them.
type group struct {
	cases    []limen.Case
	subjects map[string]bool
	// reads counts, for each field read from reviews, the reviews it reads
	// by person.
	reads   map[string]map[string]int
	applied []Row
	learned []Row
}

func newGroup(cases []limen.Case) group {
	g := group{cases: cases, subjects: map[string]bool{}, reads: map[string]map[string]int{}}
	for _, f := range limen.AnswerFields() {
		g.reads[f] = map[string]int{}
	}
	read := func(field, subject string, r Row, side limen.Verdict) {
		if r.Verdict == side && r.Review > 0 && r.Shown && r.Reviewed {
			g.reads[field][subject]++
		}
	}
	for _, c := range cases {
		s := c.Action.Subject
		g.subjects[s] = true
		if c.Decision.Verdict == limen.Flag && ReviewOf(c) != nil {
			g.reads["flag_reviews.denied"][s]++
		}
		if r, ok := RowOf(c, limen.OfApplied); ok {
			g.applied = append(g.applied, r)
			read("applied.false_allow", s, r, limen.Allow)
			read("applied.false_block", s, r, limen.Block)
		}
		if r, ok := RowOf(c, limen.OfLearned); ok {
			g.learned = append(g.learned, r)
			read("learned.false_allow", s, r, limen.Allow)
			read("learned.false_block", s, r, limen.Block)
		}
	}
	return g
}

// passing reports, for each field read from reviews, whether the answer
// rule lets it show: it reads no review, or its reviews come from the
// minimum of persons and no person gave more than half of them.
func (g group) passing() map[string]bool {
	out := map[string]bool{}
	for f, by := range g.reads {
		total, most := 0, 0
		for _, n := range by {
			total += n
			most = max(most, n)
		}
		out[f] = total == 0 || (len(by) >= limen.ReportMinSubjects && 2*most <= total)
	}
	return out
}

// cell is the group's numbers, with the fields that read reviews shown
// only where show says so.
func (g group) cell(show map[string]bool) limen.ReportCell {
	c := limen.ReportCell{Subjects: len(g.subjects), Withheld: []string{}}
	denied := 0
	for _, k := range g.cases {
		switch k.Decision.Verdict {
		case limen.Allow:
			c.Decisions.Allow++
		case limen.Flag:
			c.Decisions.Flag++
			if rv := ReviewOf(k); rv != nil {
				c.FlagReviews.Reviews++
				if !rv.Approve {
					denied++
				}
			}
		case limen.Ask:
			c.Decisions.Ask++
		case limen.Block:
			c.Decisions.Block++
		}
	}
	rates := map[string]limen.Estimate{
		"applied.false_allow": Rate(g.applied, FalseAllow),
		"applied.false_block": Rate(g.applied, FalseBlock),
		"learned.false_allow": Rate(g.learned, FalseAllow),
		"learned.false_block": Rate(g.learned, FalseBlock),
	}
	slots := map[string]**limen.Estimate{
		"applied.false_allow": &c.Applied.FalseAllow,
		"applied.false_block": &c.Applied.FalseBlock,
		"learned.false_allow": &c.Learned.FalseAllow,
		"learned.false_block": &c.Learned.FalseBlock,
	}
	for _, f := range limen.AnswerFields() {
		switch {
		case !show[f]:
			c.Withheld = append(c.Withheld, f)
		case f == "flag_reviews.denied":
			c.FlagReviews.Denied = &denied
		default:
			e := rates[f]
			*slots[f] = &e
		}
	}
	return c
}
