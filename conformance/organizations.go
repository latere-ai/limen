// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/estimate"
)

// The rules of spec 010: removing a saved profile, a person's own profile
// met with the organization's, and the report of an organization.

// The three profiles a hosted console names, each stricter than the one
// before it.
var (
	balanced = limen.Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}
	careful  = limen.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}
	strict   = limen.Costs{FalseAllow: 20, FalseBlock: 4, Ask: 1}
)

// other is a second person of the organization.
const other = "https://issuer.example|usr_conformance_other"

func thresholds(t *testing.T, c limen.Costs) limen.Thresholds {
	t.Helper()
	th, err := c.Thresholds()
	must(t, err)
	return th
}

func putProfile(t *testing.T, s limen.Service, sc limen.Scope, c limen.Costs, audit float64, certify limen.Certify) limen.Profile {
	t.Helper()
	p, err := s.PutProfile(ctx, limen.Profile{Scope: sc, Costs: c, AuditRate: audit, Certify: certify})
	must(t, err)
	return p
}

// of is action id of the person who.
func of(who, id string) limen.Action {
	a := action(id)
	a.Subject = who
	return a
}

// profileRemoved: removing an agent's profile sends the agent back to the
// organization's, and removing the organization's to the default; a
// second removal is no error, and a recorded prediction keeps its
// thresholds.
func profileRemoved(t *testing.T, target Target) {
	s := target.New(t)
	whole := limen.Scope{Org: org}
	agent := limen.Scope{Org: org, Agent: "agt_conformance"}
	putProfile(t, s, whole, limen.Costs{FalseAllow: 20, FalseBlock: 5, Ask: 1}, 0.1, limen.Certify{})
	putProfile(t, s, agent, strict, 0.2, limen.Certify{})
	first := predict(t, s, action("rm1"))
	require(t, first.Profile == agent && first.Thresholds == thresholds(t, strict), "before the removal: %+v", first)
	must(t, s.DeleteProfile(ctx, agent))
	must(t, s.DeleteProfile(ctx, agent))
	p := predict(t, s, action("rm2"))
	expect(t, p.Profile == whole && p.Thresholds == thresholds(t, limen.Costs{FalseAllow: 20, FalseBlock: 5, Ask: 1}) && p.AuditRate == 0.1,
		"after the agent's removal: %+v, want the organization's profile", p)
	got, err := s.Profile(ctx, agent)
	must(t, err)
	expect(t, got.Scope == whole && !got.Default && got.AuditRate == 0.1, "the agent's scope reads %+v, want the organization's", got)
	again := predict(t, s, action("rm1"))
	expect(t, again == first, "a recorded prediction changed after the removal: %+v, first %+v", again, first)
	c := oneCase(t, s, "rm1")
	expect(t, c.Prediction != nil && c.Prediction.Thresholds == first.Thresholds, "the recorded prediction %+v", c.Prediction)
	must(t, s.DeleteProfile(ctx, whole))
	p = predict(t, s, action("rm3"))
	d, err := s.Profile(ctx, whole)
	must(t, err)
	expect(t, d.Default && p.Thresholds == thresholds(t, limen.DefaultCosts) && p.AuditRate == limen.DefaultAuditRate,
		"after the organization's removal: %+v and %+v, want the default", d, p)
}

// profileRemovalErrors: a removal and a read name their scope by the rules
// of a scope, and break them with invalid_query.
func profileRemovalErrors(t *testing.T, target Target) {
	s := target.New(t)
	code(t, s.DeleteProfile(ctx, limen.Scope{}), limen.CodeInvalidQuery, "org")
	code(t, s.DeleteProfile(ctx, limen.Scope{Org: org, Kind: "tool", Subject: subject}), limen.CodeInvalidQuery, "kind")
	code(t, s.DeleteProfile(ctx, limen.Scope{Org: org, Agent: "agt", Subject: subject}), limen.CodeInvalidQuery, "agent")
	code(t, s.DeleteProfile(ctx, limen.Scope{Org: subject, Subject: other}), limen.CodeInvalidQuery, "subject")
	_, err := s.Profile(ctx, limen.Scope{Org: org, Agent: "agt", Subject: subject})
	code(t, err, limen.CodeInvalidQuery, "agent")
	_, err = s.Profile(ctx, limen.Scope{Org: subject, Subject: other})
	code(t, err, limen.CodeInvalidQuery, "subject")
}

// personProfileIsTheMeet: a person's own profile makes the organization's
// stricter for that person alone, in a prediction, in what the model
// learned, in an explanation with no recorded prediction, and in the
// claim of a certificate, and a preview keeps the costs it names.
func personProfileIsTheMeet(t *testing.T, target Target) {
	s := target.New(t)
	putProfile(t, s, limen.Scope{Org: org}, balanced, 0.05, limen.Certify{})
	putProfile(t, s, limen.Scope{Org: org, Subject: subject}, careful, 0.1, limen.Certify{Alpha: 0.005, Delta: 0.01})
	mine, theirs := of(subject, "mt1"), of(other, "mt1")
	p := predict(t, s, mine)
	expect(t, p.Thresholds == thresholds(t, careful) && p.AuditRate == 0.1 && p.Profile == limen.Scope{Org: org, Subject: subject},
		"the person's prediction %+v, want Careful, audit rate 0.1, and the subject in its profile", p)
	q := predict(t, s, theirs)
	expect(t, q.Thresholds == thresholds(t, balanced) && q.AuditRate == 0.05 && q.Profile == limen.Scope{Org: org},
		"another person's prediction %+v, want Balanced", q)
	for _, a := range []limen.Action{mine, theirs} {
		must(t, s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "test/1"}))
		answer(t, s, a.Key, true)
	}
	for who, want := range map[string]limen.Costs{subject: careful, other: balanced} {
		l, err := s.Learned(ctx, limen.LearnedQuery{Org: org, Subject: who})
		must(t, err)
		require(t, len(l.Entries) == 1, "learned %+v", l)
		expect(t, l.Entries[0].Thresholds == thresholds(t, want), "%s learned thresholds %+v, want %+v", who, l.Entries[0].Thresholds, thresholds(t, want))
	}
	unpredicted := of(subject, "mt2")
	decide(t, s, unpredicted, limen.Ask, 1, "test/1")
	x, err := s.Explain(ctx, unpredicted.Key)
	must(t, err)
	expect(t, x.Thresholds == thresholds(t, careful), "the explanation of an action with no prediction has %+v, want Careful", x.Thresholds)
	from, to := window()
	r, err := s.Reliability(ctx, limen.ReliabilityQuery{Org: org, Subject: subject, Of: limen.OfApplied, From: from, To: to})
	must(t, err)
	expect(t, r.Certificate.Alpha == 0.005 && r.Certificate.Delta == 0.01, "the person's certificate tests %+v, want the stricter claim", r.Certificate)
	r, err = s.Reliability(ctx, limen.ReliabilityQuery{Org: org, Subject: other, Of: limen.OfApplied, From: from, To: to})
	must(t, err)
	expect(t, r.Certificate.Alpha == limen.DefaultCertify.Alpha, "another person's certificate tests %+v", r.Certificate)
	pv, err := s.Preview(ctx, limen.PreviewRequest{Org: org, Subject: subject, From: from, To: to, Costs: balanced, AuditRate: 0.05})
	must(t, err)
	expect(t, pv.Thresholds == thresholds(t, balanced), "a preview of Balanced for the person has %+v", pv.Thresholds)
}

// personProfileNeverLoosens: a person's profile that is less strict than
// the organization's changes nothing, until the organization's becomes
// less strict than it.
func personProfileNeverLoosens(t *testing.T, target Target) {
	s := target.New(t)
	putProfile(t, s, limen.Scope{Org: org}, strict, 0.05, limen.Certify{})
	putProfile(t, s, limen.Scope{Org: org, Subject: subject}, careful, 0.05, limen.Certify{})
	p := predict(t, s, action("nl1"))
	expect(t, p.Thresholds == thresholds(t, strict) && p.Profile == limen.Scope{Org: org},
		"under Strict a person's Careful gives %+v, want Strict with no subject", p)
	putProfile(t, s, limen.Scope{Org: org}, balanced, 0.05, limen.Certify{})
	p = predict(t, s, action("nl2"))
	expect(t, p.Thresholds == thresholds(t, careful) && p.Profile == limen.Scope{Org: org, Subject: subject},
		"under Balanced a person's Careful gives %+v, want Careful with the subject", p)
}

// personProfileRead: a read with a subject returns what the person saved,
// not the meet, and the organization's profile when the person saved none.
func personProfileRead(t *testing.T, target Target) {
	s := target.New(t)
	orgs := putProfile(t, s, limen.Scope{Org: org}, careful, 0.05, limen.Certify{})
	mine := limen.Scope{Org: org, Subject: subject}
	got, err := s.Profile(ctx, mine)
	must(t, err)
	expect(t, got == orgs, "with no profile of the person, the read is %+v, want the organization's %+v", got, orgs)
	saved := putProfile(t, s, mine, balanced, 0.2, limen.Certify{})
	expect(t, saved.Scope == mine && saved.Thresholds == thresholds(t, balanced), "saved %+v", saved)
	got, err = s.Profile(ctx, mine)
	must(t, err)
	expect(t, got == saved, "the read is %+v, want what the person saved %+v", got, saved)
	got, err = s.Profile(ctx, limen.Scope{Org: org})
	must(t, err)
	expect(t, got == orgs, "the organization's profile reads %+v", got)
	got, err = s.Profile(ctx, limen.Scope{Org: org, Subject: other})
	must(t, err)
	expect(t, got == orgs, "another person reads %+v, want the organization's", got)
}

// personProfileErrors: a person's own profile has no kind and no agent,
// and in the context of a person it is that person's.
func personProfileErrors(t *testing.T, target Target) {
	s := target.New(t)
	save := func(sc limen.Scope) error {
		_, err := s.PutProfile(ctx, limen.Profile{Scope: sc, Costs: careful, AuditRate: 0.05})
		return err
	}
	code(t, save(limen.Scope{Org: org, Kind: "tool", Subject: subject}), limen.CodeInvalidProfile, "kind")
	code(t, save(limen.Scope{Org: org, Agent: "agt", Subject: subject}), limen.CodeInvalidProfile, "agent")
	code(t, save(limen.Scope{Org: subject, Subject: other}), limen.CodeInvalidProfile, "subject")
	must(t, save(limen.Scope{Org: subject, Subject: subject}))
	_, err := s.Profile(ctx, limen.Scope{Org: org, Agent: "agt", Subject: subject})
	code(t, err, limen.CodeInvalidQuery, "agent")
}

// forgetRemovesThePersonProfile: Forget removes the person's own profile
// in the organization, and no other person's.
func forgetRemovesThePersonProfile(t *testing.T, target Target) {
	s := target.New(t)
	putProfile(t, s, limen.Scope{Org: org}, balanced, 0.05, limen.Certify{})
	putProfile(t, s, limen.Scope{Org: org, Subject: subject}, strict, 0.05, limen.Certify{})
	theirs := putProfile(t, s, limen.Scope{Org: org, Subject: other}, strict, 0.05, limen.Certify{})
	p := predict(t, s, action("fp1"))
	require(t, p.Thresholds == thresholds(t, strict), "before Forget: %+v", p)
	must(t, s.Forget(ctx, org, subject))
	p = predict(t, s, action("fp2"))
	expect(t, p.Thresholds == thresholds(t, balanced) && p.Profile == limen.Scope{Org: org},
		"after Forget the person's prediction is %+v, want the organization's", p)
	got, err := s.Profile(ctx, limen.Scope{Org: org, Subject: subject})
	must(t, err)
	expect(t, got.Subject == "", "after Forget the person's profile reads %+v", got)
	got, err = s.Profile(ctx, limen.Scope{Org: org, Subject: other})
	must(t, err)
	expect(t, got == theirs, "Forget removed another person's profile: %+v", got)
}

// clock is a time a case sets.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// monday starts the first week the report cases use, long closed.
var monday = time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)

// week is the start of the n-th week after monday.
func week(n int) time.Time { return monday.Add(time.Duration(n) * limen.ReportWeek) }

// closes is when the week that starts at w closes.
func closes(w time.Time) time.Time { return w.Add(limen.ReportWeek + limen.ReportClosesAfter) }

// clocked is a fresh service on a clock set to the first day of monday's
// week, or a skip when the target sets no clock.
func clocked(t *testing.T, target Target) (limen.Service, *clock) {
	if target.Clocked == nil {
		t.Skip("the target sets no clock; Target.Clocked is nil")
	}
	c := &clock{now: monday.Add(9 * time.Hour)}
	return target.Clocked(t, c.Now), c
}

// member is the n-th person of the organization.
func member(n int) string { return fmt.Sprintf("https://issuer.example|usr_conformance_%d", n) }

// by is action id of member n, proposed by agent, or by no agent when
// agent is empty.
func by(n int, agent, id string) limen.Action {
	a := of(member(n), id)
	a.Actor = nil
	if agent != "" {
		a.Actor = &limen.Actor{Kind: "agent", ID: agent}
	}
	return a
}

func report(t *testing.T, s limen.Service, from, to time.Time) limen.Report {
	t.Helper()
	r, err := s.Report(ctx, limen.ReportQuery{Org: org, From: from, To: to})
	must(t, err)
	require(t, len(r.Weeks) == int(to.Sub(from)/limen.ReportWeek), "a report of %v to %v has %d weeks", from, to, len(r.Weeks))
	return r
}

// ready requires the week to be ready with its total.
func ready(t *testing.T, w limen.WeekReport) limen.ReportCell {
	t.Helper()
	require(t, w.State == limen.WeekReady && w.Total != nil, "week %v is %s, want ready with a total: %+v", w.From, w.State, w)
	return *w.Total
}

// reportWeeks: a report's bounds are Mondays at 00:00 UTC at most 13
// weeks apart, and a week that has not closed shows its bounds alone.
func reportWeeks(t *testing.T, target Target) {
	s := target.New(t)
	for _, c := range []struct {
		q     limen.ReportQuery
		field string
	}{
		{limen.ReportQuery{From: week(0), To: week(1)}, "org"},
		{limen.ReportQuery{Org: org, From: week(0).Add(time.Hour), To: week(1)}, "from"},
		{limen.ReportQuery{Org: org, From: week(0), To: week(1).Add(-24 * time.Hour)}, "to"},
		{limen.ReportQuery{Org: org, From: week(1), To: week(1)}, "to"},
		{limen.ReportQuery{Org: org, From: week(0), To: week(limen.ReportMaxWeeks + 1)}, "to"},
	} {
		_, err := s.Report(ctx, c.q)
		code(t, err, limen.CodeInvalidQuery, c.field)
	}
	now := time.Now().UTC()
	this := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for this.Weekday() != time.Monday {
		this = this.Add(-24 * time.Hour)
	}
	for i := range limen.ReportMinSubjects {
		decide(t, s, of(member(i), "rw1"), limen.Allow, 0.05, "test/1")
	}
	r := report(t, s, this.Add(-2*limen.ReportWeek), this.Add(limen.ReportWeek))
	expect(t, r.Org == org && r.From.Equal(this.Add(-2*limen.ReportWeek)) && r.To.Equal(this.Add(limen.ReportWeek)) && r.MinSubjects == limen.ReportMinSubjects,
		"the report's head %+v", r)
	for i, want := range []string{limen.WeekTooFew, limen.WeekOpen, limen.WeekOpen} {
		w := r.Weeks[i]
		from := this.Add(time.Duration(i-2) * limen.ReportWeek)
		expect(t, w.State == want && w.From.Equal(from) && w.To.Equal(from.Add(limen.ReportWeek)) && w.Total == nil && w.Agents == nil && w.Other == nil,
			"week %d: %+v, want %s with its bounds alone", i, w, want)
	}
}

// reportNeedsAGroup: a closed week with four persons is too_few and shows
// no number, not even how many; with five it is ready, and each count is
// the sum over the persons.
func reportNeedsAGroup(t *testing.T, target Target) {
	s, c := clocked(t, target)
	for i := range 4 {
		decide(t, s, by(i, "", "ng1"), limen.Allow, 0.05, "test/1")
	}
	c.Set(week(1).Add(time.Hour))
	want := limen.VerdictCounts{}
	for i := range 5 {
		for j := range i + 1 {
			decide(t, s, by(i, "", fmt.Sprintf("ng2-%d", j)), limen.Allow, 0.05, "test/1")
			want.Allow++
		}
		decide(t, s, by(i, "", "ng3"), limen.Flag, 1, "test/1")
		want.Flag++
		if i < 3 {
			decide(t, s, by(i, "", "ng4"), limen.Block, 0, "test/1")
			want.Block++
		}
	}
	c.Set(closes(week(1)))
	r := report(t, s, week(0), week(2))
	few := r.Weeks[0]
	expect(t, few.State == limen.WeekTooFew && few.Total == nil && few.Agents == nil && few.Other == nil, "four persons: %+v", few)
	b, err := json.Marshal(few)
	must(t, err)
	var fields map[string]any
	must(t, json.Unmarshal(b, &fields))
	expect(t, len(fields) == 3 && fields["state"] == limen.WeekTooFew, "a too_few week shows more than its bounds: %s", b)
	total := ready(t, r.Weeks[1])
	expect(t, total.Subjects == 5 && total.Decisions == want, "five persons: %d persons and %+v, want 5 and %+v", total.Subjects, total.Decisions, want)
}

// reportPoolsTheEstimate: the error rate of a cell is one estimate over
// the rows of all its cases, not a mean of the persons' rates.
func reportPoolsTheEstimate(t *testing.T, target Target) {
	s, c := clocked(t, target)
	var rows []estimate.Row
	for i := range 5 {
		for j := range 1 + 2*i {
			decide(t, s, by(i, "", fmt.Sprintf("pe-%d", j)), limen.Allow, 0.25, "test/1")
			rows = append(rows, estimate.Row{Verdict: limen.Allow, Review: 0.25})
		}
		a := by(i, "", "pe-flag")
		decide(t, s, a, limen.Flag, 0.25, "test/1")
		answer(t, s, a.Key, i%2 == 0)
		rows = append(rows, estimate.Row{Verdict: limen.Allow, Review: 0.25, Shown: true, Reviewed: true, Approve: i%2 == 0})
	}
	c.Set(closes(week(0)))
	total := ready(t, report(t, s, week(0), week(1)).Weeks[0])
	require(t, total.Applied.FalseAllow != nil, "the false allow rate is withheld: %v", total.Withheld)
	want := estimate.Rate(rows, estimate.FalseAllow)
	expect(t, reflect.DeepEqual(*total.Applied.FalseAllow, want), "the false allow rate %s, want the pooled estimate %s", shown(*total.Applied.FalseAllow), shown(want))
}

// shown prints an estimate with its value and interval, not their
// addresses.
func shown(e limen.Estimate) string {
	iv := "none"
	if e.Interval != nil {
		iv = fmt.Sprint(*e.Interval)
	}
	return fmt.Sprintf("{value %v, interval %s, covered %v, reviews %d, audits %d, unanswered %d}", value(e), iv, e.Covered, e.Reviews, e.Audits, e.Unanswered)
}

// reportAnswerRule: a field read from reviews shows only when its reviews
// come from five persons or more and no person gave more than half.
func reportAnswerRule(t *testing.T, target Target) {
	s, c := clocked(t, target)
	// Each week, six persons take part; reviews[i] is the number of
	// denied flags person i reviewed.
	weeks := []struct {
		reviews []int
		show    bool
	}{
		{[]int{1, 1, 1, 1, 0, 0}, false},
		{[]int{5, 1, 1, 1, 1, 0}, false},
		{[]int{4, 1, 1, 1, 1, 0}, true},
	}
	for n, w := range weeks {
		c.Set(week(n).Add(time.Hour))
		for i, k := range w.reviews {
			decide(t, s, by(i, "", fmt.Sprintf("ar%d", n)), limen.Allow, 0, "test/1")
			for j := range k {
				a := by(i, "", fmt.Sprintf("ar%d-%d", n, j))
				decide(t, s, a, limen.Flag, 1, "test/1")
				answer(t, s, a.Key, false)
			}
		}
	}
	c.Set(closes(week(len(weeks) - 1)))
	r := report(t, s, week(0), week(len(weeks)))
	for n, w := range weeks {
		total := ready(t, r.Weeks[n])
		for _, f := range []string{"flag_reviews.denied", "applied.false_allow"} {
			present := total.Applied.FalseAllow != nil
			if f == "flag_reviews.denied" {
				present = total.FlagReviews.Denied != nil
			}
			listed := slices.Contains(total.Withheld, f)
			expect(t, present == w.show && listed != w.show, "week %d, reviews %v: %s present %v, withheld %v", n, w.reviews, f, present, total.Withheld)
		}
		if w.show && total.FlagReviews.Denied != nil {
			expect(t, *total.FlagReviews.Denied == 8 && total.FlagReviews.Reviews == 8, "week %d: %+v", n, total.FlagReviews)
		}
	}
}

// reportMergesSmallAgents: an agent with fewer than five persons goes into
// the other agents; while those have fewer than five, the smallest agent
// cell joins them; the parts add up to the total; and a field one part
// withholds, every part withholds.
func reportMergesSmallAgents(t *testing.T, target Target) {
	s, c := clocked(t, target)
	decideAll := func(agent string, members []int) {
		for _, m := range members {
			decide(t, s, by(m, agent, "ma-"+agent), limen.Allow, 0, "test/1")
		}
	}
	// agt_a: 7 persons, 5 of whom review a flag; agt_b: 5 persons;
	// agt_c: 2 persons; no agent: 1 person, who reviews a flag.
	decideAll("agt_a", []int{0, 1, 2, 3, 4, 5, 6})
	decideAll("agt_b", []int{0, 1, 2, 3, 4})
	decideAll("agt_c", []int{7, 8})
	decideAll("", []int{9})
	for _, m := range []int{0, 1, 2, 3, 4} {
		a := by(m, "agt_a", "ma-flag")
		decide(t, s, a, limen.Flag, 1, "test/1")
		answer(t, s, a.Key, m != 0)
	}
	a := by(9, "", "ma-flag")
	decide(t, s, a, limen.Flag, 1, "test/1")
	answer(t, s, a.Key, false)
	c.Set(closes(week(0)))
	w := report(t, s, week(0), week(1)).Weeks[0]
	total := ready(t, w)
	require(t, w.Other != nil && len(w.Agents) == 1, "agents %+v, other %+v; want agt_a and the others", w.Agents, w.Other)
	cells := []limen.ReportCell{w.Agents[0].ReportCell, *w.Other}
	expect(t, w.Agents[0].Agent == "agt_a" && w.Agents[0].Subjects == 7 && w.Other.Subjects == 8 && total.Subjects == 10,
		"agt_a has %d persons and the others %d of %d, want agt_a with 7 and agt_b, agt_c and no agent with 8 of 10",
		w.Agents[0].Subjects, w.Other.Subjects, total.Subjects)
	var sum limen.VerdictCounts
	reviews := 0
	for _, cell := range cells {
		expect(t, cell.Subjects >= limen.ReportMinSubjects, "a cell of %d persons", cell.Subjects)
		sum.Allow += cell.Decisions.Allow
		sum.Flag += cell.Decisions.Flag
		sum.Ask += cell.Decisions.Ask
		sum.Block += cell.Decisions.Block
		reviews += cell.FlagReviews.Reviews
		expect(t, cell.FlagReviews.Denied == nil && slices.Contains(cell.Withheld, "flag_reviews.denied"),
			"the others' denials come from one person, so every part withholds them: %+v", cell)
	}
	expect(t, sum == total.Decisions && reviews == total.FlagReviews.Reviews, "the parts add up to %+v and %d reviews, the total is %+v and %d",
		sum, reviews, total.Decisions, total.FlagReviews.Reviews)
	expect(t, total.FlagReviews.Denied != nil && *total.FlagReviews.Denied == 2, "the total's denials: %+v, withheld %v", total.FlagReviews, total.Withheld)
}

// reportCountsReviewsBeforeTheClose: a review that arrives after the week
// closed is unanswered in that week.
func reportCountsReviewsBeforeTheClose(t *testing.T, target Target) {
	s, c := clocked(t, target)
	for i := range 5 {
		a := by(i, "", "rc1")
		decide(t, s, a, limen.Flag, 1, "test/1")
		if i > 0 {
			answer(t, s, a.Key, false)
		}
	}
	late := by(5, "", "rc1")
	decide(t, s, late, limen.Flag, 1, "test/1")
	c.Set(closes(week(0)).Add(-time.Second))
	answer(t, s, by(0, "", "rc1").Key, false)
	c.Set(closes(week(0)))
	answer(t, s, late.Key, false)
	total := ready(t, report(t, s, week(0), week(1)).Weeks[0])
	fa := total.Applied.FalseAllow
	require(t, fa != nil, "the false allow rate is withheld: %v", total.Withheld)
	expect(t, total.FlagReviews.Reviews == 5 && fa.Reviews == 5 && fa.Unanswered == 1,
		"%d flag reviews, and %d reviews with %d unanswered in the false allow rate, want the 5 before the close and 1 unanswered",
		total.FlagReviews.Reviews, fa.Reviews, fa.Unanswered)
}

// reportKeepsAClosedWeek: a week a report showed never changes, after a
// Forget of a person who took part, a review after the close, or a kept
// replay into the week.
func reportKeepsAClosedWeek(t *testing.T, target Target) {
	s, c := clocked(t, target)
	for i := range 6 {
		decide(t, s, by(i, "agt_k", "kc1"), limen.Allow, 0.05, "test/1")
		a := by(i, "agt_k", "kc2")
		decide(t, s, a, limen.Flag, 1, "test/1")
		if i > 0 {
			answer(t, s, a.Key, i%2 == 0)
		}
	}
	c.Set(closes(week(0)))
	first := report(t, s, week(0), week(1))
	ready(t, first.Weeks[0])
	must(t, s.Forget(ctx, org, member(1)))
	page := cases(t, s, limen.CaseQuery{Org: org, Subject: member(1)})
	require(t, len(page.Entries) == 0, "the Forget kept %d cases", len(page.Entries))
	answer(t, s, by(0, "agt_k", "kc2").Key, false)
	_, err := s.Replay(ctx, records(limen.ReplayRecord{At: week(0).Add(time.Hour), Action: by(7, "agt_k", "kc3"),
		Verdict: limen.Flag, ReviewProbability: 1, Source: "rules/1", Approve: new(false)}), limen.ReplayOptions{Keep: true})
	must(t, err)
	again := report(t, s, week(0), week(1))
	expect(t, reflect.DeepEqual(again, first), "the kept week changed:\n first %+v\n again %+v", first.Weeks[0], again.Weeks[0])
}

// times finds every RFC 3339 time in a JSON document.
var times = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T[0-9:.]+(Z|[+-]\d{2}:\d{2})`)

// reportNamesNoPerson: no field of a report holds a subject, an action id,
// a label or a time other than the bounds of its weeks.
func reportNamesNoPerson(t *testing.T, target Target) {
	s, c := clocked(t, target)
	for i := range 6 {
		a := command(fmt.Sprintf("act_unique_%d", i), "go test ./...")
		a.Subject = member(i)
		a.Actor = &limen.Actor{Kind: "agent", ID: "agt_names", Session: "ses_unique"}
		predict(t, s, a)
		must(t, s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Flag, ReviewProbability: 1, Source: "test/1"}))
		c.Set(c.Now().Add(37 * time.Minute))
		answer(t, s, a.Key, i != 0)
	}
	c.Set(closes(week(0)).Add(time.Hour))
	r := report(t, s, week(0), week(2))
	ready(t, r.Weeks[0])
	b, err := json.Marshal(r)
	must(t, err)
	doc := string(b)
	for _, banned := range []string{"usr_conformance", "issuer.example", "act_unique", "ses_unique", "go test", "bash", "test/1"} {
		expect(t, !strings.Contains(doc, banned), "the report holds %q: %s", banned, doc)
	}
	bounds := []time.Time{week(0), week(1), week(2)}
	for _, m := range times.FindAllString(doc, -1) {
		at, err := time.Parse(time.RFC3339Nano, m)
		expect(t, err == nil && slices.ContainsFunc(bounds, at.Equal), "the report holds the time %s, which bounds no week", m)
	}
	expect(t, len(r.Weeks[0].Agents) == 1 && r.Weeks[0].Agents[0].Agent == "agt_names", "the agents %+v", r.Weeks[0].Agents)
}
