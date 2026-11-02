// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"iter"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/conformance"
	"latere.ai/x/limen/estimate"
	"latere.ai/x/limen/stub"
)

// faulty is the stub with a defect: each hook that is set replaces the
// stub's method of the same name, and can call the stub. The stub's clock
// is now, or at while a hook sets it.
type faulty struct {
	*stub.Service
	now           func() time.Time
	at            time.Time
	deleteProfile func(context.Context, limen.Scope) error
	report        func(context.Context, limen.ReportQuery) (limen.Report, error)
	predict       func(context.Context, limen.Action) (limen.Prediction, error)
	record        func(context.Context, limen.Decision) error
	answer        func(context.Context, limen.Answer) error
	cases         func(context.Context, limen.CaseQuery) (limen.Page, error)
	reliability   func(context.Context, limen.ReliabilityQuery) (limen.Reliability, error)
	profile       func(context.Context, limen.Scope) (limen.Profile, error)
	putProfile    func(context.Context, limen.Profile) (limen.Profile, error)
	replay        func(context.Context, iter.Seq2[limen.ReplayRecord, error], limen.ReplayOptions) (limen.ReplayReport, error)
	forget        func(ctx context.Context, org, subject string) error
	explain       func(context.Context, limen.Key) (limen.Explanation, error)
	learned       func(context.Context, limen.LearnedQuery) (limen.Learned, error)
	preview       func(context.Context, limen.PreviewRequest) (limen.Preview, error)
}

func (f *faulty) Predict(ctx context.Context, a limen.Action) (limen.Prediction, error) {
	if f.predict != nil {
		return f.predict(ctx, a)
	}
	return f.Service.Predict(ctx, a)
}

func (f *faulty) Record(ctx context.Context, d limen.Decision) error {
	if f.record != nil {
		return f.record(ctx, d)
	}
	return f.Service.Record(ctx, d)
}

func (f *faulty) Answer(ctx context.Context, a limen.Answer) error {
	if f.answer != nil {
		return f.answer(ctx, a)
	}
	return f.Service.Answer(ctx, a)
}

func (f *faulty) Cases(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
	if f.cases != nil {
		return f.cases(ctx, q)
	}
	return f.Service.Cases(ctx, q)
}

func (f *faulty) Reliability(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
	if f.reliability != nil {
		return f.reliability(ctx, q)
	}
	return f.Service.Reliability(ctx, q)
}

func (f *faulty) Profile(ctx context.Context, sc limen.Scope) (limen.Profile, error) {
	if f.profile != nil {
		return f.profile(ctx, sc)
	}
	return f.Service.Profile(ctx, sc)
}

func (f *faulty) PutProfile(ctx context.Context, p limen.Profile) (limen.Profile, error) {
	if f.putProfile != nil {
		return f.putProfile(ctx, p)
	}
	return f.Service.PutProfile(ctx, p)
}

func (f *faulty) DeleteProfile(ctx context.Context, sc limen.Scope) error {
	if f.deleteProfile != nil {
		return f.deleteProfile(ctx, sc)
	}
	return f.Service.DeleteProfile(ctx, sc)
}

func (f *faulty) Report(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
	if f.report != nil {
		return f.report(ctx, q)
	}
	return f.Service.Report(ctx, q)
}

// clock is the stub's clock: at while a hook sets it, and now otherwise.
func (f *faulty) clock() time.Time {
	if !f.at.IsZero() {
		return f.at
	}
	return f.now()
}

// eachWeek applies edit to every week of a report the stub returns.
func (f *faulty) eachWeek(edit func(w *limen.WeekReport)) func(context.Context, limen.ReportQuery) (limen.Report, error) {
	return func(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
		r, err := f.Service.Report(ctx, q)
		for i := range r.Weeks {
			edit(&r.Weeks[i])
		}
		return r, err
	}
}

// startOfWeek is the Monday at 00:00 UTC at or before t.
func startOfWeek(t time.Time) time.Time {
	d := t.UTC().Truncate(24 * time.Hour)
	for d.Weekday() != time.Monday {
		d = d.Add(-24 * time.Hour)
	}
	return d
}

func (f *faulty) Replay(ctx context.Context, rs iter.Seq2[limen.ReplayRecord, error], o limen.ReplayOptions) (limen.ReplayReport, error) {
	if f.replay != nil {
		return f.replay(ctx, rs, o)
	}
	return f.Service.Replay(ctx, rs, o)
}

func (f *faulty) Forget(ctx context.Context, org, subject string) error {
	if f.forget != nil {
		return f.forget(ctx, org, subject)
	}
	return f.Service.Forget(ctx, org, subject)
}

func (f *faulty) Explain(ctx context.Context, k limen.Key) (limen.Explanation, error) {
	if f.explain != nil {
		return f.explain(ctx, k)
	}
	return f.Service.Explain(ctx, k)
}

func (f *faulty) Learned(ctx context.Context, q limen.LearnedQuery) (limen.Learned, error) {
	if f.learned != nil {
		return f.learned(ctx, q)
	}
	return f.Service.Learned(ctx, q)
}

func (f *faulty) Preview(ctx context.Context, r limen.PreviewRequest) (limen.Preview, error) {
	if f.preview != nil {
		return f.preview(ctx, r)
	}
	return f.Service.Preview(ctx, r)
}

// defect breaks one rule of the contract in a fresh stub.
type defect struct {
	// rule is the case of the suite that must catch the defect.
	rule string
	// what the defect is, for the message of a case that misses it.
	what string
	seed func(f *faulty)
}

// unless passes err through, and returns nil in place of an error with the
// code.
func unless(err error, c limen.Code) error {
	if limen.CodeOf(err) == c {
		return nil
	}
	return err
}

// defects has one defect for each rule of the suite (spec 006, criterion
// 1). A defect can break more rules than its own; the test asks only that
// its own rule catches it.
var defects = []defect{
	{"Validation", "an invalid action gets a prediction", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			if a.Validate() != nil {
				return limen.Prediction{Key: a.Key, Verdict: limen.Ask, AskKind: limen.AskUnsure}, nil
			}
			return f.Service.Predict(ctx, a)
		}
	}},
	{"UnknownIsAnAsk", "a person with no answers is allowed", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			p, err := f.Service.Predict(ctx, a)
			p.Approve, p.Verdict, p.AskKind = 0.97, limen.Allow, ""
			return p, err
		}
	}},
	{"PredictionFollowsTheProfile", "a prediction has the default audit rate under any profile", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			p, err := f.Service.Predict(ctx, a)
			p.AuditRate = limen.DefaultAuditRate
			return p, err
		}
	}},
	{"SteeredVerdicts", "every prediction is an ask", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			p, err := f.Service.Predict(ctx, a)
			p.Verdict, p.AskKind = limen.Ask, limen.AskUnsure
			return p, err
		}
	}},
	{"CoverCapsAllow", "an allow ignores the cover rule", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			p, err := f.Service.Predict(ctx, a)
			if p.AskKind == limen.AskWider || p.AskKind == limen.AskFew {
				p.Verdict, p.AskKind = limen.Allow, ""
			}
			return p, err
		}
	}},
	{"ScopeOfAnAction", "a claim makes the scope narrower", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			p, err := f.Service.Predict(ctx, a)
			if a.Scope != nil && a.Scope.Undo != "" {
				p.Scope.Undo = a.Scope.Undo
			}
			if p.Scope.Undo == limen.UndoIrreversible {
				p.Scope.Undo = limen.UndoRecoverable
			}
			return p, err
		}
	}},
	{"AnswerWeights", "a quick approval counts", func(f *faulty) {
		f.answer = func(ctx context.Context, a limen.Answer) error {
			if a.Surface == "" {
				a.DetailsOpened = new(true)
			}
			return f.Service.Answer(ctx, a)
		}
	}},
	{"ConsentOfAProfile", "a profile loses its settings of consent", func(f *faulty) {
		f.putProfile = func(ctx context.Context, p limen.Profile) (limen.Profile, error) {
			if p.Consent.MinWeight >= 0 && p.Consent.MinSessions >= 0 {
				p.Consent = limen.Consent{}
			}
			return f.Service.PutProfile(ctx, p)
		}
	}},
	{"AskReasons", "an ask states its probability", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			p, err := f.Service.Predict(ctx, a)
			if p.Verdict == limen.Ask {
				p.Reason = "The approval probability is " + strconv.FormatFloat(p.Approve, 'f', 2, 64) + ". Limen suggests ask."
			}
			return p, err
		}
	}},
	{"AnswerFields", "an answer drops how the person gave it", func(f *faulty) {
		f.answer = func(ctx context.Context, a limen.Answer) error {
			if a.Validate() == nil {
				a.HowAnswered = limen.HowAnswered{}
			}
			return f.Service.Answer(ctx, a)
		}
	}},
	{"ProfilePrecedence", "the profile of an agent is never found", func(f *faulty) {
		f.profile = func(ctx context.Context, sc limen.Scope) (limen.Profile, error) {
			sc.Agent = ""
			return f.Service.Profile(ctx, sc)
		}
	}},
	{"ProfileDefaults", "a saved profile keeps a zero certify", func(f *faulty) {
		f.putProfile = func(ctx context.Context, p limen.Profile) (limen.Profile, error) {
			saved, err := f.Service.PutProfile(ctx, p)
			if p.Certify == (limen.Certify{}) {
				saved.Certify = limen.Certify{}
			}
			return saved, err
		}
	}},
	{"AskTooCostly", "costs that decide on no evidence are saved", func(f *faulty) {
		f.putProfile = func(ctx context.Context, p limen.Profile) (limen.Profile, error) {
			saved, err := f.Service.PutProfile(ctx, p)
			if limen.CodeOf(err) == limen.CodeAskTooCostly {
				return p, nil
			}
			return saved, err
		}
	}},
	{"IdempotentPredict", "the input tells two actions apart", func(f *faulty) {
		inputs := map[limen.Key]string{}
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			if in, seen := inputs[a.Key]; seen && in != string(a.Input) {
				return limen.Prediction{}, &limen.Error{Code: limen.CodeActionConflict, Fields: []string{"action_id"}}
			}
			inputs[a.Key] = string(a.Input)
			return f.Service.Predict(ctx, a)
		}
	}},
	{"OneDecision", "a second decision is accepted", func(f *faulty) {
		f.record = func(ctx context.Context, d limen.Decision) error {
			return unless(f.Service.Record(ctx, d), limen.CodeAlreadyDecided)
		}
	}},
	{"DecisionNeedsAnAction", "a decision without an action for an unknown action is accepted", func(f *faulty) {
		f.record = func(ctx context.Context, d limen.Decision) error {
			err := f.Service.Record(ctx, d)
			if d.Validate() == nil && d.Action == nil {
				return unless(err, limen.CodeInvalidDecision)
			}
			return err
		}
	}},
	{"NoDecision", "an answer without a decision is accepted", func(f *faulty) {
		f.answer = func(ctx context.Context, a limen.Answer) error {
			return unless(f.Service.Answer(ctx, a), limen.CodeNoDecision)
		}
	}},
	{"AnswerRoles", "every answer is a review", func(f *faulty) {
		f.cases = func(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
			page, err := f.Service.Cases(ctx, q)
			for i := range page.Entries {
				for j := range page.Entries[i].Answers {
					page.Entries[i].Answers[j].Review = true
				}
			}
			return page, err
		}
	}},
	{"ReviewsWeightedByProbability", "an allow is recorded with review probability zero", func(f *faulty) {
		f.record = func(ctx context.Context, d limen.Decision) error {
			if d.Verdict == limen.Allow {
				d.ReviewProbability = 0
			}
			return f.Service.Record(ctx, d)
		}
	}},
	{"NullIsNotZero", "a rate nobody could see is zero", func(f *faulty) {
		f.reliability = func(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
			r, err := f.Service.Reliability(ctx, q)
			for _, e := range []*limen.Estimate{&r.FalseAllow, &r.FalseBlock} {
				if e.Value == nil && e.Unanswered == 0 {
					e.Value = new(0.0)
				}
			}
			return r, err
		}
	}},
	{"Unanswered", "a shown action with no review is not counted", func(f *faulty) {
		f.reliability = func(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
			r, err := f.Service.Reliability(ctx, q)
			r.FalseAllow.Unanswered, r.FalseBlock.Unanswered = 0, 0
			return r, err
		}
	}},
	{"LaterAnswersNeverEstimate", "the latest answer to a shown action is its review", func(f *faulty) {
		f.reliability = func(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
			r, err := f.Service.Reliability(ctx, q)
			if err != nil {
				return r, err
			}
			page, err := f.Service.Cases(ctx, limen.CaseQuery{Org: q.Org, Subject: q.Subject, Limit: limen.MaxPageLimit})
			var rows []estimate.Row
			for _, c := range page.Entries {
				for i := range c.Answers {
					c.Answers[i].Review = c.Decision.Verdict.Shown() && i == len(c.Answers)-1
				}
				if row, ok := estimate.RowOf(c, q.Of); ok {
					rows = append(rows, row)
				}
			}
			r.FalseAllow = estimate.Rate(rows, estimate.FalseAllow)
			return r, err
		}
	}},
	{"Sources", "a source of decisions or signals reports every decision", func(f *faulty) {
		f.reliability = func(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
			of := q.Of
			if of != limen.OfLearned {
				q.Of = limen.OfApplied
			}
			r, err := f.Service.Reliability(ctx, q)
			r.Of = of
			return r, err
		}
	}},
	{"Learned", "the suggestions of Limen report nothing", func(f *faulty) {
		f.reliability = func(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
			r, err := f.Service.Reliability(ctx, q)
			if q.Of == limen.OfLearned {
				r.FalseAllow, r.FalseBlock = limen.Estimate{}, limen.Estimate{}
			}
			return r, err
		}
	}},
	{"Certificate", "the certificate tests the default claim under any profile", func(f *faulty) {
		f.reliability = func(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
			r, err := f.Service.Reliability(ctx, q)
			c := &r.Certificate
			c.Alpha, c.Delta, c.Needed = limen.DefaultCertify.Alpha, limen.DefaultCertify.Delta, 300
			c.Holds = c.Clean >= c.Needed
			return r, err
		}
	}},
	{"PendingAndPages", "the review queue lists every case", func(f *faulty) {
		f.cases = func(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
			q.Pending = false
			return f.Service.Cases(ctx, q)
		}
	}},
	{"CasesNeverReturnInput", "a case keeps the input of its action", func(f *faulty) {
		inputs := map[limen.Key][]byte{}
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			inputs[a.Key] = a.Input
			return f.Service.Predict(ctx, a)
		}
		f.cases = func(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
			page, err := f.Service.Cases(ctx, q)
			for i := range page.Entries {
				page.Entries[i].Action.Input = inputs[page.Entries[i].Action.Key]
			}
			return page, err
		}
	}},
	{"ForgetIsScoped", "a person is forgotten in every organization", func(f *faulty) {
		orgs := map[string]bool{}
		f.record = func(ctx context.Context, d limen.Decision) error {
			orgs[d.Org] = true
			return f.Service.Record(ctx, d)
		}
		f.forget = func(ctx context.Context, org, subject string) error {
			if err := f.Service.Forget(ctx, org, subject); err != nil {
				return err
			}
			for o := range orgs {
				if err := f.Service.Forget(ctx, o, subject); err != nil {
					return err
				}
			}
			return nil
		}
	}},
	{"FailClosed", "an answer to an action Limen never predicted is dropped", func(f *faulty) {
		predicted := map[limen.Key]bool{}
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			predicted[a.Key] = true
			return f.Service.Predict(ctx, a)
		}
		f.answer = func(ctx context.Context, a limen.Answer) error {
			if !predicted[a.Key] {
				return a.Validate()
			}
			return f.Service.Answer(ctx, a)
		}
	}},
	{"Replay", "a replay without keep keeps the history", func(f *faulty) {
		f.replay = func(ctx context.Context, rs iter.Seq2[limen.ReplayRecord, error], o limen.ReplayOptions) (limen.ReplayReport, error) {
			rep, err := f.Service.Replay(ctx, rs, limen.ReplayOptions{Keep: true})
			rep.Kept = o.Keep
			return rep, err
		}
	}},
	{"Explanations", "an action with no record is explained as an ask", func(f *faulty) {
		f.explain = func(ctx context.Context, k limen.Key) (limen.Explanation, error) {
			x, err := f.Service.Explain(ctx, k)
			if limen.CodeOf(err) == limen.CodeNoCase {
				return limen.Explanation{Key: k, Verdict: limen.Ask, AskKind: limen.AskUnsure, Evidence: []limen.Evidence{}}, nil
			}
			return x, err
		}
	}},
	{"ExplanationsOfSteeredVerdicts", "every explanation has the kind of an ask", func(f *faulty) {
		f.explain = func(ctx context.Context, k limen.Key) (limen.Explanation, error) {
			x, err := f.Service.Explain(ctx, k)
			x.AskKind = limen.AskUnsure
			return x, err
		}
	}},
	{"LearnedEntries", "the least answered entries come first", func(f *faulty) {
		f.learned = func(ctx context.Context, q limen.LearnedQuery) (limen.Learned, error) {
			l, err := f.Service.Learned(ctx, q)
			slices.Reverse(l.Entries)
			return l, err
		}
	}},
	{"Previews", "a preview applies the default thresholds", func(f *faulty) {
		f.preview = func(ctx context.Context, r limen.PreviewRequest) (limen.Preview, error) {
			p, err := f.Service.Preview(ctx, r)
			p.Thresholds, _ = limen.DefaultCosts.Thresholds()
			return p, err
		}
	}},
	{"PreviewErrors", "a preview accepts costs that decide on no evidence", func(f *faulty) {
		f.preview = func(ctx context.Context, r limen.PreviewRequest) (limen.Preview, error) {
			p, err := f.Service.Preview(ctx, r)
			return p, unless(err, limen.CodeAskTooCostly)
		}
	}},
	{"ProfileRemoved", "a removal removes nothing", func(f *faulty) {
		f.deleteProfile = func(_ context.Context, sc limen.Scope) error { return sc.Validate() }
	}},
	{"ProfileRemovalErrors", "a removal of a person's scope with a kind or an agent removes the person's profile", func(f *faulty) {
		f.deleteProfile = func(ctx context.Context, sc limen.Scope) error {
			if sc.Subject != "" {
				sc.Kind, sc.Agent = "", ""
			}
			return f.Service.DeleteProfile(ctx, sc)
		}
	}},
	{"PersonProfileIsTheMeet", "a person's own profile is saved and never applied", func(f *faulty) {
		f.putProfile = func(ctx context.Context, p limen.Profile) (limen.Profile, error) {
			if p.Subject == "" || p.Validate() != nil {
				return f.Service.PutProfile(ctx, p)
			}
			p.Thresholds, _ = p.Costs.Thresholds()
			if p.Certify == (limen.Certify{}) {
				p.Certify = limen.DefaultCertify
			}
			return p, nil
		}
	}},
	{"PersonProfileNeverLoosens", "a person's own profile replaces the organization's", func(f *faulty) {
		f.predict = func(ctx context.Context, a limen.Action) (limen.Prediction, error) {
			p, err := f.Service.Predict(ctx, a)
			if own, oerr := f.Service.Profile(ctx, limen.Scope{Org: a.Org, Subject: a.Subject}); err == nil && oerr == nil && own.Subject != "" {
				p.Thresholds, p.AuditRate, p.Profile = own.Thresholds, own.AuditRate, own.Scope
			}
			return p, err
		}
	}},
	{"PersonProfileRead", "a read with a subject returns the organization's profile", func(f *faulty) {
		f.profile = func(ctx context.Context, sc limen.Scope) (limen.Profile, error) {
			if sc.Validate() == nil {
				sc.Subject = ""
			}
			return f.Service.Profile(ctx, sc)
		}
	}},
	{"PersonProfileErrors", "a person's own profile with a kind or an agent is saved without them", func(f *faulty) {
		f.putProfile = func(ctx context.Context, p limen.Profile) (limen.Profile, error) {
			if p.Subject != "" {
				p.Kind, p.Agent = "", ""
			}
			return f.Service.PutProfile(ctx, p)
		}
	}},
	{"ForgetRemovesThePersonProfile", "Forget keeps the person's own profile", func(f *faulty) {
		f.forget = func(ctx context.Context, org, subject string) error {
			own, _ := f.Service.Profile(ctx, limen.Scope{Org: org, Subject: subject})
			if err := f.Service.Forget(ctx, org, subject); err != nil {
				return err
			}
			if own.Subject != "" {
				_, err := f.Service.PutProfile(ctx, own)
				return err
			}
			return nil
		}
	}},
	{"ReportWeeks", "a report accepts bounds that are not on a Monday", func(f *faulty) {
		f.report = func(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
			if !q.From.IsZero() && !q.To.IsZero() {
				q.From, q.To = startOfWeek(q.From), startOfWeek(q.To)
				if !q.To.After(q.From) {
					q.To = q.From.Add(limen.ReportWeek)
				}
				if q.To.Sub(q.From) > limen.ReportMaxWeeks*limen.ReportWeek {
					q.To = q.From.Add(limen.ReportMaxWeeks * limen.ReportWeek)
				}
			}
			return f.Service.Report(ctx, q)
		}
	}},
	{"ReportNeedsAGroup", "a week with four persons shows its numbers", func(f *faulty) {
		f.report = f.eachWeek(func(w *limen.WeekReport) {
			if w.State == limen.WeekTooFew {
				w.State, w.Total = limen.WeekReady, &limen.ReportCell{Subjects: 4, Withheld: []string{}}
			}
		})
	}},
	{"ReportPoolsTheEstimate", "the error rate of a cell is the mean of its persons' rates", func(f *faulty) {
		var subjects []string
		f.record = func(ctx context.Context, d limen.Decision) error {
			if !slices.Contains(subjects, d.Subject) {
				subjects = append(subjects, d.Subject)
			}
			return f.Service.Record(ctx, d)
		}
		f.report = func(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
			r, err := f.Service.Report(ctx, q)
			for _, w := range r.Weeks {
				if w.Total == nil || w.Total.Applied.FalseAllow == nil {
					continue
				}
				sum, n := 0.0, 0
				for _, s := range subjects {
					rel, err := f.Service.Reliability(ctx, limen.ReliabilityQuery{Org: q.Org, Subject: s, Of: limen.OfApplied, From: w.From, To: w.To})
					if err == nil && rel.FalseAllow.Value != nil {
						sum += *rel.FalseAllow.Value
						n++
					}
				}
				if n > 0 {
					mean := sum / float64(n)
					w.Total.Applied.FalseAllow.Value = &mean
				}
			}
			return r, err
		}
	}},
	{"ReportAnswerRule", "a cell lists no field as withheld", func(f *faulty) {
		f.report = f.eachWeek(func(w *limen.WeekReport) {
			if w.Total != nil {
				w.Total.Withheld = []string{}
			}
		})
	}},
	{"ReportMergesSmallAgents", "the other agents are left out", func(f *faulty) {
		f.report = f.eachWeek(func(w *limen.WeekReport) { w.Other = nil })
	}},
	{"ReportCountsReviewsBeforeTheClose", "an answer is stamped with the time of its decision", func(f *faulty) {
		decided := map[limen.Key]time.Time{}
		f.record = func(ctx context.Context, d limen.Decision) error {
			decided[d.Key] = f.now()
			return f.Service.Record(ctx, d)
		}
		f.answer = func(ctx context.Context, a limen.Answer) error {
			f.at = decided[a.Key]
			defer func() { f.at = time.Time{} }()
			return f.Service.Answer(ctx, a)
		}
	}},
	{"ReportKeepsAClosedWeek", "a Forget changes a kept week", func(f *faulty) {
		forgot := false
		f.forget = func(ctx context.Context, org, subject string) error {
			forgot = true
			return f.Service.Forget(ctx, org, subject)
		}
		f.report = f.eachWeek(func(w *limen.WeekReport) {
			if forgot && w.Total != nil {
				w.Total.Subjects--
			}
		})
	}},
	{"ReportNamesNoPerson", "a cell names its agent by a person who ran it", func(f *faulty) {
		var who string
		f.record = func(ctx context.Context, d limen.Decision) error {
			who = d.Subject
			return f.Service.Record(ctx, d)
		}
		f.report = f.eachWeek(func(w *limen.WeekReport) {
			for i := range w.Agents {
				w.Agents[i].Agent = who
			}
		})
	}},
}

// target is the suite's target for a stub seeded with d.
func (d defect) target() conformance.Target {
	seeded := func(o stub.Options, now func() time.Time) limen.Service {
		f := &faulty{now: now}
		o.Clock = f.clock
		f.Service = stub.New(o)
		d.seed(f)
		return f
	}
	return conformance.Target{
		New: func(*testing.T) limen.Service { return seeded(stub.Options{}, time.Now) },
		Steered: func(_ *testing.T, predict func(limen.Action) (float64, float64)) limen.Service {
			return seeded(stub.Options{Predict: predict}, time.Now)
		},
		Clocked: func(_ *testing.T, now func() time.Time) limen.Service { return seeded(stub.Options{}, now) },
	}
}

// defectEnv names the defect TestDefective runs the suite against.
const defectEnv = "LIMEN_CONFORMANCE_DEFECT"

// TestDefective runs one rule of the suite against the stub seeded with the
// rule's defect. It runs only when TestDefects starts it, and then fails.
func TestDefective(t *testing.T) {
	name := os.Getenv(defectEnv)
	if name == "" {
		t.Skip("runs only as a child of TestDefects")
	}
	i := slices.IndexFunc(defects, func(d defect) bool { return d.rule == name })
	if i < 0 {
		t.Fatalf("no defect for the rule %s", name)
	}
	conformance.Run(t, defects[i].target())
}

// Each rule of the suite fails against a service seeded with its defect
// (spec 006, criterion 1), so a suite that passes says something. A rule
// cannot fail inside this test binary without failing the test, so each
// defect runs in a child process of the binary, and the child must fail on
// that rule.
func TestDefects(t *testing.T) {
	names := make([]string, len(defects))
	for i, d := range defects {
		names[i] = d.rule
	}
	if rules := conformance.RuleNames(); !slices.Equal(names, rules) {
		t.Fatalf("the defects are for the rules %v, want one for each rule of the suite, in order: %v", names, rules)
	}
	for _, d := range defects {
		t.Run(d.rule, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(os.Args[0], "-test.run=^TestDefective$/^"+d.rule+"$", "-test.count=1")
			cmd.Env = append(os.Environ(), defectEnv+"="+d.rule)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "--- FAIL: TestDefective/"+d.rule+" (") {
				t.Errorf("the rule %s passed against a service where %s: %v\n%s", d.rule, d.what, err, out)
			}
			// A rule catches the defect by its own check, not by a panic.
			if strings.Contains(string(out), "panic:") {
				t.Errorf("the rule %s panicked against a service where %s:\n%s", d.rule, d.what, out)
			}
		})
	}
}
