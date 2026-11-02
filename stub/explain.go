// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package stub

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/estimate"
)

// The stub has no model to take apart. Its explanations hold every field a
// model does not decide, as the engine's do: the recorded prediction, the
// thresholds, the verdict and the kind of an ask, the cover and the
// approvals nearest to it, the labels and the answers; the mean is the
// probit of the approval probability, and there are no parts, no evidence
// and no trend. Its previews are the engine's arithmetic on the same
// records.

var _ limen.Explainer = (*Service)(nil)

// answersLocked counts the subject's answers, which names the stub's
// "model" as the engine's fit count names its own.
func (s *Service) answersLocked(org, subject string) int64 {
	var n int64
	for _, e := range s.cases {
		if e.action.Org == org && e.action.Subject == subject {
			n += int64(len(e.answers))
		}
	}
	return n
}

// subjectLocked is the subject's entries in org, oldest first.
func (s *Service) subjectLocked(org, subject string) []*entry {
	var out []*entry
	for _, e := range s.sortedLocked() {
		if e.action.Org == org && e.action.Subject == subject {
			out = append(out, e)
		}
	}
	return out
}

// records are entries as the cover rule reads them, as the engine reads
// its cases.
func records(entries []*entry) []estimate.Record {
	out := make([]estimate.Record, len(entries))
	for i, e := range entries {
		r := estimate.Record{T: int64(e.seq), ActionID: e.action.ActionID, Label: e.label, Families: e.families, Scope: e.scope,
			Session: limen.SessionOf(e.action, e.at), DecidedAt: e.decidedAt, Answers: e.answers}
		r.Shown = e.decision != nil && e.decision.Verdict.Shown()
		if e.prediction != nil {
			approve := e.prediction.Approve
			r.Suggested = &approve
		}
		out[i] = r
	}
	return out
}

// settings are the settings of the cover rule under the profile p.
func (s *Service) settings(p limen.Profile) estimate.Settings {
	return estimate.Settings{MinWeight: max(s.minWeight, p.Consent.MinWeight), MinSessions: max(s.minSessions, p.Consent.MinSessions),
		Window: estimate.DefaultWindow}
}

// judgeLocked is the suggestion for the entry e at the approval
// probability approve under the profile p, with the cover rule over the
// subject's entries now. The stub's evidence ratio is zero.
func (s *Service) judgeLocked(e *entry, approve float64, p limen.Profile) estimate.Judged {
	recs := records(s.subjectLocked(e.action.Org, e.action.Subject))
	return estimate.Judge(approve, 0, p.Thresholds, e.families, e.scope, recs, s.policy.Weigh(recs), s.settings(p))
}

// reasonOf is the stub's reason: for an allow or a block, the approval
// against the ask band; for an ask, the reason every implementation gives,
// with no probability.
func reasonOf(approve float64, th limen.Thresholds, j estimate.Judged, label string) string {
	if j.Verdict == limen.Ask {
		return estimate.AskReason(j.Kind, label, j.Cover)
	}
	return fmt.Sprintf("approval %.2f against the ask band %.2f to %.2f", approve, th.BlockBelow, th.AllowAbove)
}

// settingOf is the setting an option sets: zero is the contract's value
// def, and a negative value turns the setting off.
func settingOf[T int | time.Duration](option, def T) T {
	switch {
	case option == 0:
		return def
	case option < 0:
		return 0
	}
	return option
}

// optionOf is the option that sets n: a setting that is off is a negative
// option.
func optionOf[T int | time.Duration](n T) T {
	if n == 0 {
		return -1
	}
	return n
}

func probit(p float64) float64 { return math.Sqrt2 * math.Erfinv(2*p-1) }

// Explain implements [limen.Explainer].
func (s *Service) Explain(ctx context.Context, k limen.Key) (limen.Explanation, error) {
	if err := ctx.Err(); err != nil {
		return limen.Explanation{}, err
	}
	if err := k.Validate(); err != nil {
		return limen.Explanation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.cases[k]
	if e == nil {
		return limen.Explanation{}, &limen.Error{Code: limen.CodeNoCase, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("no case %s for %s in %s", k.ActionID, k.Subject, k.Org)}
	}
	approve, uncertainty := s.predict(e.action)
	p := s.effectiveLocked(limen.ScopeOf(e.action), k.Subject)
	if e.prediction != nil {
		approve, uncertainty, p.Thresholds = e.prediction.Approve, e.prediction.Uncertainty, e.prediction.Thresholds
	}
	j := s.judgeLocked(e, approve, p)
	recs := records(s.subjectLocked(k.Org, k.Subject))
	out := limen.Explanation{
		Key: k, Model: limen.ModelRef{Source: Source, Answers: s.answersLocked(k.Org, k.Subject)},
		Approve: approve, Uncertainty: uncertainty, Verdict: j.Verdict, Thresholds: p.Thresholds,
		Cover:  limen.ExplainedCover{Cover: j.Cover, Nearest: estimate.Nearest(e.families, e.scope, recs, s.settings(p), 3)},
		Reason: reasonOf(approve, p.Thresholds, j, e.label), Mean: probit(approve), AskKind: j.Kind, Evidence: []limen.Evidence{},
	}
	if e.predictedBy != nil {
		out.PredictedBy = &limen.ModelRef{Source: Source, Answers: *e.predictedBy}
	}
	return out, nil
}

// Learned implements [limen.Explainer].
func (s *Service) Learned(ctx context.Context, q limen.LearnedQuery) (limen.Learned, error) {
	if err := ctx.Err(); err != nil {
		return limen.Learned{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Learned{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type group struct {
		rep              *entry
		approved, denied int
	}
	groups := map[string]*group{}
	var order []string
	for _, e := range s.sortedLocked() {
		if e.action.Org != q.Org || e.action.Subject != q.Subject || len(e.answers) == 0 || (q.Kind != "" && e.action.Kind != q.Kind) {
			continue
		}
		g := groups[e.label]
		if g == nil {
			g = &group{}
			groups[e.label] = g
			order = append(order, e.label)
		}
		g.rep = e
		if e.answers[len(e.answers)-1].Approve {
			g.approved++
		} else {
			g.denied++
		}
	}
	out := limen.Learned{Org: q.Org, Subject: q.Subject, Model: limen.ModelRef{Source: Source, Answers: s.answersLocked(q.Org, q.Subject)},
		Entries: []limen.LearnedEntry{}, Recent: []limen.TrendPoint{}}
	for _, l := range order {
		g := groups[l]
		approve, uncertainty := s.predict(g.rep.action)
		p := s.effectiveLocked(limen.Scope{Org: q.Org, Kind: g.rep.action.Kind, Agent: q.Agent}, q.Subject)
		j := s.judgeLocked(g.rep, approve, p)
		out.Entries = append(out.Entries, limen.LearnedEntry{
			Label: l, Kind: g.rep.action.Kind, Name: g.rep.action.Name, Approved: g.approved, Denied: g.denied,
			Approve: approve, Uncertainty: uncertainty, Verdict: j.Verdict, AskKind: j.Kind, Thresholds: p.Thresholds, Cover: j.Cover,
		})
	}
	slices.SortStableFunc(out.Entries, func(a, b limen.LearnedEntry) int {
		return cmp.Or(cmp.Compare(b.Approved+b.Denied, a.Approved+a.Denied), cmp.Compare(a.Label, b.Label))
	})
	return out, nil
}

// Preview implements [limen.Explainer].
func (s *Service) Preview(ctx context.Context, r limen.PreviewRequest) (limen.Preview, error) {
	if err := ctx.Err(); err != nil {
		return limen.Preview{}, err
	}
	if err := r.Validate(); err != nil {
		return limen.Preview{}, err
	}
	th, err := r.Costs.Thresholds()
	if err != nil {
		return limen.Preview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := limen.Preview{PreviewRequest: r, Thresholds: th,
		Would: map[limen.Verdict]int{limen.Allow: 0, limen.Ask: 0, limen.Block: 0}}
	for _, e := range s.sortedLocked() {
		if e.action.Org != r.Org || e.action.Subject != r.Subject || e.prediction == nil || e.at.Before(r.From) || !e.at.Before(r.To) {
			continue
		}
		out.Records++
		v := estimate.Previewed(th, *e.prediction)
		out.Would[v]++
		if len(e.answers) == 0 {
			continue
		}
		out.Answered++
		ok := e.answers[len(e.answers)-1].Approve
		switch {
		case v == limen.Allow && !ok:
			out.AllowedDenied++
		case v == limen.Block && ok:
			out.BlockedApproved++
		}
	}
	out.ExpectedAsks = float64(out.Would[limen.Ask]) + r.AuditRate*float64(out.Would[limen.Allow]+out.Would[limen.Block])
	return out, nil
}
