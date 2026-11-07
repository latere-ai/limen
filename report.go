// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"context"
	"strconv"
	"time"
)

// Reporter reports an organization's agents across all its persons, by
// week, and never one person (spec 010). Each number of a report comes
// from a group of persons, and a week a report showed never changes.
type Reporter interface {
	// Report returns one entry per week of the query, oldest first.
	Report(ctx context.Context, q ReportQuery) (Report, error)
}

// The fixed numbers of a report of an organization. An installation
// cannot change them.
const (
	// ReportMinSubjects is the minimum group: a week, an agent's cell and
	// the other agents' cell show their numbers only when this many
	// persons took part, and a number read from reviews needs reviews from
	// this many persons.
	ReportMinSubjects = 5
	// ReportWeek is the length of a week, which starts on a Monday at
	// 00:00 UTC.
	ReportWeek = 7 * 24 * time.Hour
	// ReportClosesAfter is how long after its end a week closes: a review
	// counts in the week only when it arrived before, and the week shows
	// its numbers only after.
	ReportClosesAfter = 7 * 24 * time.Hour
	// ReportMaxWeeks bounds the weeks of one report.
	ReportMaxWeeks = 13
)

// The states of a week of a report.
const (
	// WeekOpen is a week that has not closed: it shows only its bounds.
	WeekOpen = "open"
	// WeekTooFew is a closed week in which fewer than [ReportMinSubjects]
	// persons took part: it shows only its bounds, and not how many.
	WeekTooFew = "too_few"
	// WeekReady is a closed week with a group: it shows its numbers.
	WeekReady = "ready"
)

// AnswerFields returns the JSON paths of a cell's fields that read
// reviews, which the answer rule can withhold, in the order a cell's
// Withheld lists them.
func AnswerFields() []string {
	return []string{"flag_reviews.denied", "applied.false_allow", "applied.false_block", "learned.false_allow", "learned.false_block"}
}

// WeekStart reports whether t is a Monday at 00:00 UTC, the time a week
// of a report starts.
func WeekStart(t time.Time) bool {
	u := t.UTC()
	return u.Weekday() == time.Monday && u.Hour() == 0 && u.Minute() == 0 && u.Second() == 0 && u.Nanosecond() == 0
}

// WeekClosed reports whether the week that starts at from is closed at
// now: [ReportClosesAfter] after its end.
func WeekClosed(from, now time.Time) bool {
	return !now.Before(from.Add(ReportWeek + ReportClosesAfter))
}

// ReportQuery names the organization and the weeks of a report: from and
// to are each a Monday at 00:00 UTC, and to is after from by at most
// [ReportMaxWeeks] weeks. Fixed weeks that never overlap keep two reports
// from differing by one person's hour.
type ReportQuery struct {
	Org  string    `json:"org"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Validate reports every field of q that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil.
func (q ReportQuery) Validate() error {
	var p problems
	if q.Org == "" {
		p.add("org", "is empty")
	}
	p.long("org", q.Org)
	from := weekBound(&p, "from", q.From)
	to := weekBound(&p, "to", q.To)
	switch {
	case !from || !to:
	case !q.From.Before(q.To):
		p.add("to", "is not after from")
	case q.To.Sub(q.From) > ReportMaxWeeks*ReportWeek:
		p.add("to", "is more than "+strconv.Itoa(ReportMaxWeeks)+" weeks after from")
	}
	return p.err(CodeInvalidQuery)
}

// weekBound holds one bound of a report to a week's start, and reports
// whether it holds.
func weekBound(p *problems, name string, t time.Time) bool {
	switch {
	case t.IsZero():
		p.add(name, "is not set")
	case !WeekStart(t):
		p.add(name, "is not a Monday at 00:00 UTC")
	default:
		return true
	}
	return false
}

// Report is the report of an organization: one entry per week of the
// query, oldest first. It names no person, no action and no time finer
// than a week; the only names in it are agent ids.
type Report struct {
	Org  string    `json:"org"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// MinSubjects is [ReportMinSubjects].
	MinSubjects int          `json:"min_subjects"`
	Weeks       []WeekReport `json:"weeks"`
}

// WeekReport is one week of a report. An open or too_few week has only
// its bounds and its state. A ready week has Total, and, when the week
// divides by agent, Agents and Other, whose counts add up to Total's.
//
// A week is kept the first time it is read closed, and every later read
// returns it unchanged: a late answer, a kept replay and a Forget change
// no week a report showed.
type WeekReport struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	State string    `json:"state"`
	// Total is the whole organization in the week.
	Total *ReportCell `json:"total,omitzero"`
	// Agents has one cell for each agent with [ReportMinSubjects] persons
	// or more in the week, by agent id.
	Agents []AgentCell `json:"agents,omitempty"`
	// Other has every other case of the week: the agents with no cell of
	// their own and the cases with no agent. It is absent when there are
	// none.
	Other *ReportCell `json:"other,omitzero"`
}

// ReportCell is the numbers of one group of a week's cases. Subjects,
// Decisions and FlagReviews.Reviews follow the group rule alone. Each
// field read from reviews, FlagReviews.Denied and the four error rates,
// is shown only when its reviews come from [ReportMinSubjects] persons or
// more and no person gave more than half of them; otherwise it is absent
// and its JSON path is in Withheld.
type ReportCell struct {
	// Subjects counts the persons with a decided case in the cell.
	Subjects  int           `json:"subjects"`
	Decisions VerdictCounts `json:"decisions"`
	// FlagReviews is what the reviews of the cell's flags said.
	FlagReviews FlagReviews `json:"flag_reviews"`
	// Applied and Learned are the error rates of the verdicts applied and
	// of Limen's suggestions, each one estimate over the rows of every
	// case of the cell, as a reliability report computes it for one
	// person. A review after the week closed counts as unanswered.
	Applied SourceRates `json:"applied"`
	Learned SourceRates `json:"learned"`
	// Withheld lists the fields the answer rule removed. Never nil.
	Withheld []string `json:"withheld"`
}

// AgentCell is the cell of one agent, named by the actor id of its cases.
type AgentCell struct {
	Agent string `json:"agent"`
	ReportCell
}

// VerdictCounts counts decided cases by the verdict applied.
type VerdictCounts struct {
	Allow int `json:"allow"`
	Flag  int `json:"flag"`
	Ask   int `json:"ask"`
	Block int `json:"block"`
}

// FlagReviews counts the cell's cases with the verdict flag applied that
// have a review from before the week closed, and among them the reviews
// that denied the action: how often a person who saw an action that ran
// said it should have asked.
type FlagReviews struct {
	Reviews int `json:"reviews"`
	// Denied is absent when the answer rule withholds it.
	Denied *int `json:"denied,omitzero"`
}

// SourceRates are one source's two error rates in a cell. A rate is
// absent when the answer rule withholds it; a rate with no review is
// present with a null value, as in a reliability report.
type SourceRates struct {
	FalseAllow *Estimate `json:"false_allow,omitzero"`
	FalseBlock *Estimate `json:"false_block,omitzero"`
}
