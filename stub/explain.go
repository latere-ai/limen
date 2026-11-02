// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package stub

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"latere.ai/x/limen"
)

// The stub has no model to take apart. Its explanations hold every field a
// model does not decide, as the engine's do: the recorded prediction, the
// thresholds, the verdict and the kind of an ask, the labels and the
// answers; the mean is the probit of the approval probability, and there
// are no parts, no evidence and no trend. Its previews are the engine's
// arithmetic on the same records.

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

// holdLocked applies the answer minimum to v, the verdict the thresholds
// give an action with the label l of the subject in org, with the
// approvals the stub holds now.
func (s *Service) holdLocked(v limen.Verdict, org, subject, l string) (limen.Verdict, limen.Minimum) {
	if v != limen.Allow || (s.minAllows == 0 && s.minSessions == 0) {
		return v, limen.Minimum{}
	}
	var t limen.Tally
	for _, e := range s.cases {
		if e.action.Org == org && e.action.Subject == subject && e.label == l {
			t.Add(e.action, e.answers)
		}
	}
	return limen.Hold(v, t.Minimum(s.minAllows, s.minSessions))
}

// reasonOf is the stub's reason: the approval against the ask band, and
// the answer minimum when it held an allow.
func reasonOf(approve float64, th limen.Thresholds, held limen.Minimum) string {
	r := fmt.Sprintf("approval %.2f against the ask band %.2f to %.2f", approve, th.BlockBelow, th.AllowAbove)
	if held.Held() {
		r += fmt.Sprintf(", held by the answer minimum: %d of %d approvals, %d of %d sessions",
			held.Allows, held.MinAllows, held.Sessions, held.MinSessions)
	}
	return r
}

// minimumOf is the minimum an option sets: zero is the contract's value
// def, and a negative value no minimum.
func minimumOf(option, def int) int {
	switch {
	case option == 0:
		return def
	case option < 0:
		return 0
	}
	return option
}

// optionOf is the option that sets the minimum n: no minimum is a
// negative option.
func optionOf(n int) int {
	if n == 0 {
		return -1
	}
	return n
}

func probit(p float64) float64 { return math.Sqrt2 * math.Erfinv(2*p-1) }

func kindOf(v limen.Verdict) string {
	if v == limen.Ask {
		return limen.AskUnsure
	}
	return ""
}

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
	th := s.effectiveLocked(limen.ScopeOf(e.action), k.Subject).Thresholds
	if e.prediction != nil {
		approve, uncertainty, th = e.prediction.Approve, e.prediction.Uncertainty, e.prediction.Thresholds
	}
	v, held := s.holdLocked(th.Verdict(approve), k.Org, k.Subject, e.label)
	out := limen.Explanation{
		Key: k, Model: limen.ModelRef{Source: Source, Answers: s.answersLocked(k.Org, k.Subject)},
		Approve: approve, Uncertainty: uncertainty, Verdict: v, Thresholds: th, Minimum: held,
		Reason: reasonOf(approve, th, held), Mean: probit(approve), AskKind: kindOf(v), Evidence: []limen.Evidence{},
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
		th := s.effectiveLocked(limen.Scope{Org: q.Org, Kind: g.rep.action.Kind, Agent: q.Agent}, q.Subject).Thresholds
		v, held := s.holdLocked(th.Verdict(approve), q.Org, q.Subject, l)
		out.Entries = append(out.Entries, limen.LearnedEntry{
			Label: l, Kind: g.rep.action.Kind, Name: g.rep.action.Name, Approved: g.approved, Denied: g.denied,
			Approve: approve, Uncertainty: uncertainty, Verdict: v, AskKind: kindOf(v), Thresholds: th, Minimum: held,
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
		v := th.Verdict(e.prediction.Approve)
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
