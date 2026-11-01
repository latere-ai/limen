// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/featurize"
	"latere.ai/x/limen/engine/gp"
	"latere.ai/x/limen/engine/kernel"
	"latere.ai/x/limen/engine/store"
)

// EvidenceCut is the evidence ratio below which an ask is
// [limen.AskUnsure] and at or above which it is [limen.AskSplit]: whether
// the person's answers removed less or more than half of the prior
// variance at the action (spec 009).
const EvidenceCut = 0.5

// The number of answers an explanation lists on each side.
const (
	evidenceFor     = 3
	evidenceAgainst = 2
)

// maxTrend bounds the points of a learned model's trend.
const maxTrend = 20

var _ limen.Explainer = (*Engine)(nil)

// askKind names the uncertainty behind a verdict: the kind of an ask, and
// nothing for an allow or a block.
func askKind(v limen.Verdict, evidenceRatio float64) string {
	switch {
	case v != limen.Ask:
		return ""
	case evidenceRatio < EvidenceCut:
		return limen.AskUnsure
	}
	return limen.AskSplit
}

// evidenceRatio is 1 - σ²/v₀, the share of the prior variance at a point
// that the answers removed, held to [0, 1] against rounding.
func evidenceRatio(variance, prior float64) float64 {
	if prior <= 0 {
		return 0
	}
	return min(max(1-variance/prior, 0), 1)
}

// point is the case as the model sees it at time t. A case featurized by
// another featurizer version is featurized again from its stored action,
// as a fit does.
func point(c store.Case, t int64) kernel.Point {
	if c.Featurizer != featurize.Version {
		f := featurize.Of(c.Action)
		c.A, c.C = f.A, f.C
	}
	return kernel.Point{Tool: c.A, Ctx: c.C, T: float64(t)}
}

// label is the case's label, or for a case from before labels, the label
// of its stored action, whose input is gone.
func label(c store.Case) string {
	if c.Label != "" {
		return c.Label
	}
	return featurize.Label(c.Action)
}

// latest is the case's most recent answer; ok is false for a case with
// none.
func latest(c store.Case) (limen.RecordedAnswer, bool) {
	if len(c.Answers) == 0 {
		return limen.RecordedAnswer{}, false
	}
	return c.Answers[len(c.Answers)-1], true
}

func parts(ex gp.Explanation) *limen.Parts {
	if ex.Parts == nil {
		return nil
	}
	return &limen.Parts{Action: ex.Parts[0], Recent: ex.Parts[1], Interaction: ex.Parts[2]}
}

// Explain implements [limen.Explainer].
func (e *Engine) Explain(ctx context.Context, k limen.Key) (limen.Explanation, error) {
	if err := ctx.Err(); err != nil {
		return limen.Explanation{}, err
	}
	if err := k.Validate(); err != nil {
		return limen.Explanation{}, err
	}
	defer e.lock(k.Org, k.Subject)()
	c, ok, err := e.st.Case(ctx, k)
	if err != nil {
		return limen.Explanation{}, err
	}
	if !ok {
		return limen.Explanation{}, &limen.Error{Code: limen.CodeNoCase, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("no case %s for %s in %s", k.ActionID, k.Subject, k.Org)}
	}
	var th limen.Thresholds
	if c.Prediction != nil {
		th = c.Prediction.Thresholds
	} else {
		prof, err := e.profile(ctx, limen.ScopeOf(c.Action))
		if err != nil {
			return limen.Explanation{}, err
		}
		th = prof.Thresholds
	}
	m, err := e.model(ctx, subjectKey{k.Org, k.Subject})
	if err != nil {
		return limen.Explanation{}, err
	}
	post := m.post.Load()
	ex := post.Explain(point(c, c.T))
	v := th.Verdict(ex.Approve)
	out := limen.Explanation{
		Key:           k,
		Model:         post.ref(),
		Approve:       ex.Approve,
		Uncertainty:   math.Sqrt(ex.Var),
		Verdict:       v,
		Thresholds:    th,
		Mean:          ex.Mean,
		Parts:         parts(ex),
		EvidenceRatio: ex.EvidenceRatio,
		AskKind:       askKind(v, ex.EvidenceRatio),
		Evidence:      []limen.Evidence{},
	}
	if c.Prediction != nil && c.PredictedBy != nil {
		out.PredictedBy = &limen.ModelRef{Source: c.Prediction.Source, Answers: *c.PredictedBy}
	}
	if len(ex.Contributions) > 0 {
		cases, err := e.st.Cases(ctx, k.Org, k.Subject)
		if err != nil {
			return limen.Explanation{}, err
		}
		out.Evidence = evidence(ex, cases, v)
	}
	return out, nil
}

// evidence picks the answers that moved an explanation most: up to three
// toward the verdict's side and two against it, or three each way for an
// ask, ordered by share. An approval's term is positive and a denial's
// negative, since the kernel is positive and the gradient of the probit
// log likelihood has the answer's sign.
func evidence(ex gp.Explanation, cases []store.Case, v limen.Verdict) []limen.Evidence {
	byT := make(map[int64]store.Case, len(cases))
	for _, c := range cases {
		byT[c.T] = c
	}
	total := 0.0
	for _, x := range ex.Contributions {
		total += math.Abs(x)
	}
	type term struct {
		c     store.Case
		value float64
	}
	var ups, downs []term
	for i, x := range ex.Contributions {
		c, ok := byT[int64(ex.Points[i].T)]
		if !ok || x == 0 {
			continue
		}
		if x > 0 {
			ups = append(ups, term{c, x})
		} else {
			downs = append(downs, term{c, x})
		}
	}
	larger := func(a, b term) int { return cmp.Compare(math.Abs(b.value), math.Abs(a.value)) }
	slices.SortStableFunc(ups, larger)
	slices.SortStableFunc(downs, larger)
	nUp, nDown := evidenceFor, evidenceFor
	switch v {
	case limen.Allow, limen.Flag:
		nDown = evidenceAgainst
	case limen.Block:
		nUp = evidenceAgainst
	}
	picked := append(ups[:min(nUp, len(ups))], downs[:min(nDown, len(downs))]...)
	slices.SortStableFunc(picked, larger)
	out := make([]limen.Evidence, 0, len(picked))
	for _, t := range picked {
		a, _ := latest(t.c)
		out = append(out, limen.Evidence{ActionID: t.c.Action.ActionID, Label: label(t.c), At: a.At, Approve: a.Approve,
			Share: math.Abs(t.value) / total})
	}
	return out
}

// Learned implements [limen.Explainer].
func (e *Engine) Learned(ctx context.Context, q limen.LearnedQuery) (limen.Learned, error) {
	if err := ctx.Err(); err != nil {
		return limen.Learned{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Learned{}, err
	}
	defer e.lock(q.Org, q.Subject)()
	cases, err := e.st.Cases(ctx, q.Org, q.Subject)
	if err != nil {
		return limen.Learned{}, err
	}
	m, err := e.model(ctx, subjectKey{q.Org, q.Subject})
	if err != nil {
		return limen.Learned{}, err
	}
	post := m.post.Load()
	out := limen.Learned{Org: q.Org, Subject: q.Subject, Model: post.ref(), Entries: []limen.LearnedEntry{}, Recent: []limen.TrendPoint{}}
	var now int64
	for _, c := range cases {
		now = max(now, c.T+1)
	}
	// One entry per label, at its most recent answered case, which the
	// cases' order of T makes the last one seen.
	type group struct {
		rep              store.Case
		approved, denied int
	}
	groups := map[string]*group{}
	var order []string
	for _, c := range cases {
		a, ok := latest(c)
		if !ok || (q.Kind != "" && c.Action.Kind != q.Kind) {
			continue
		}
		l := label(c)
		g := groups[l]
		if g == nil {
			g = &group{}
			groups[l] = g
			order = append(order, l)
		}
		g.rep = c
		if a.Approve {
			g.approved++
		} else {
			g.denied++
		}
	}
	for _, l := range order {
		g := groups[l]
		prof, err := e.profile(ctx, limen.Scope{Org: q.Org, Kind: g.rep.Action.Kind, Agent: q.Agent})
		if err != nil {
			return limen.Learned{}, err
		}
		ex := post.Explain(point(g.rep, now))
		v := prof.Thresholds.Verdict(ex.Approve)
		out.Entries = append(out.Entries, limen.LearnedEntry{
			Label: l, Kind: g.rep.Action.Kind, Name: g.rep.Action.Name,
			Approved: g.approved, Denied: g.denied,
			Approve: ex.Approve, Uncertainty: math.Sqrt(ex.Var), Verdict: v,
			EvidenceRatio: ex.EvidenceRatio, AskKind: askKind(v, ex.EvidenceRatio),
			Thresholds: prof.Thresholds,
		})
	}
	slices.SortStableFunc(out.Entries, func(a, b limen.LearnedEntry) int {
		return cmp.Or(cmp.Compare(b.Approved+b.Denied, a.Approved+a.Denied), cmp.Compare(a.Label, b.Label))
	})
	if post.Len() > 0 && len(cases) > 0 {
		out.Recent = trend(post.Posterior, cases, now)
	}
	return out, nil
}

// trend samples the shared tolerance part at up to maxTrend evenly spaced
// decisions, the last one the latest. The part depends on time alone, so
// any case's features serve as the point.
func trend(post *gp.Posterior, cases []store.Case, now int64) []limen.TrendPoint {
	n := int64(min(maxTrend, len(cases)))
	at := cases[len(cases)-1]
	out := make([]limen.TrendPoint, 0, n)
	for i := range n {
		t := (now - 1) * (i + 1) / n
		c := cases[0]
		for _, x := range cases {
			if x.T > t {
				break
			}
			c = x
		}
		ex := post.Explain(point(at, t))
		if ex.Parts == nil {
			return []limen.TrendPoint{}
		}
		out = append(out, limen.TrendPoint{T: t, At: c.ReceivedAt, Value: ex.Parts[1]})
	}
	return out
}

// Preview implements [limen.Explainer].
func (e *Engine) Preview(ctx context.Context, r limen.PreviewRequest) (limen.Preview, error) {
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
	defer e.lock(r.Org, r.Subject)()
	cases, err := e.st.Cases(ctx, r.Org, r.Subject)
	if err != nil {
		return limen.Preview{}, err
	}
	return preview(r, th, cases), nil
}

// preview re-thresholds the recorded predictions in the window. The stub
// shares it, so both report the same counts for the same cases.
func preview(r limen.PreviewRequest, th limen.Thresholds, cases []store.Case) limen.Preview {
	out := limen.Preview{PreviewRequest: r, Thresholds: th,
		Would: map[limen.Verdict]int{limen.Allow: 0, limen.Ask: 0, limen.Block: 0}}
	for _, c := range cases {
		if c.Prediction == nil || c.ReceivedAt.Before(r.From) || !c.ReceivedAt.Before(r.To) {
			continue
		}
		out.Records++
		v := th.Verdict(c.Prediction.Approve)
		out.Would[v]++
		a, ok := latest(c)
		if !ok {
			continue
		}
		out.Answered++
		switch {
		case v == limen.Allow && !a.Approve:
			out.AllowedDenied++
		case v == limen.Block && a.Approve:
			out.BlockedApproved++
		}
	}
	out.ExpectedAsks = float64(out.Would[limen.Ask]) + r.AuditRate*float64(out.Would[limen.Allow]+out.Would[limen.Block])
	return out
}
