// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/estimate"
)

var _ limen.Reporter = (*Engine)(nil)

// Report implements [limen.Reporter] (spec 010). A week that has not
// closed is open. A closed week is read from the store when it is kept,
// and otherwise built by latere.ai/x/limen/estimate from the cases of
// every person of the organization and kept, so every later read returns
// it unchanged: a late answer, a kept replay and a Forget change no week a
// report showed. The cases are read one person at a time, each under that
// person's lock, off the path of any prediction.
func (e *Engine) Report(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
	if err := ctx.Err(); err != nil {
		return limen.Report{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Report{}, err
	}
	defer e.reportLock(q.Org)()
	now := e.clock()
	out := limen.Report{Org: q.Org, From: q.From.UTC(), To: q.To.UTC(), MinSubjects: limen.ReportMinSubjects, Weeks: []limen.WeekReport{}}
	var build []int
	for _, from := range estimate.Weeks(q) {
		if !limen.WeekClosed(from, now) {
			out.Weeks = append(out.Weeks, estimate.OpenWeek(from))
			continue
		}
		w, ok, err := e.st.WeekReport(ctx, q.Org, from)
		if err != nil {
			return limen.Report{}, err
		}
		if !ok {
			build = append(build, len(out.Weeks))
			w.From = from
		}
		out.Weeks = append(out.Weeks, w)
	}
	if len(build) == 0 {
		return out, nil
	}
	first, last := out.Weeks[build[0]].From, out.Weeks[build[len(build)-1]].From.Add(limen.ReportWeek)
	cases, err := e.orgCases(ctx, q.Org, first, last)
	if err != nil {
		return limen.Report{}, err
	}
	for _, i := range build {
		w := estimate.Week(out.Weeks[i].From, cases)
		if err := e.st.PutWeekReport(ctx, q.Org, w); err != nil {
			return limen.Report{}, err
		}
		out.Weeks[i] = w
	}
	return out, nil
}

// orgCases reads the decided cases of every person of the organization
// received in [from, to), one person at a time under their lock.
func (e *Engine) orgCases(ctx context.Context, org string, from, to time.Time) ([]estimate.ReportCase, error) {
	subjects, err := e.st.Subjects(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []estimate.ReportCase
	for _, subject := range subjects {
		unlock := e.lock(org, subject)
		cases, err := e.st.Cases(ctx, org, subject)
		unlock()
		if err != nil {
			return nil, err
		}
		for _, c := range cases {
			if c.Decision != nil && !c.ReceivedAt.Before(from) && c.ReceivedAt.Before(to) {
				out = append(out, estimate.ReportCase{Case: c.Contract(), ReceivedAt: c.ReceivedAt})
			}
		}
	}
	return out, nil
}

func (e *Engine) reportLock(org string) func() {
	return lockIn(e, e.reports, org)
}
