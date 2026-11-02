// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
)

// answer predicts, records an ask and answers it, as manual mode does.
func answer(t *testing.T, e *engine.Engine, a limen.Action, approve bool) limen.Prediction {
	t.Helper()
	p, err := e.Predict(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve}); err != nil {
		t.Fatal(err)
	}
	return p
}

// A person who approves test runs and denies recursive deletes.
func taught(t *testing.T, n int) *engine.Engine {
	t.Helper()
	c := &clock{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	e := engine.New(engine.Options{Inline: 1, Clock: c.Now})
	t.Cleanup(func() { e.Close() })
	for i := range n {
		answer(t, e, across(bash("s", fmt.Sprintf("t-%d", i), "go test ./..."), i), true)
		answer(t, e, across(bash("s", fmt.Sprintf("r-%d", i), "rm -rf build"), i), false)
	}
	return e
}

// Spec 009, criteria 1 and 2: an explanation's parts sum to its mean, its
// shares are a distribution's part, and it explains the prediction the
// same model made, naming that model.
func TestExplanationIsThePrediction(t *testing.T) {
	e := taught(t, 15)
	a := bash("s", "probe", "go test ./pkg/...")
	p, err := e.Predict(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	x, err := e.Explain(ctx, a.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.Approve != p.Approve || x.Uncertainty != p.Uncertainty || x.Verdict != p.Verdict || x.Thresholds != p.Thresholds {
		t.Errorf("explained %+v, predicted %+v", x, p)
	}
	// The prediction carries the explanation's evidence, so a decision point
	// can tell an ask on little evidence from one on evidence at once.
	if p.EvidenceRatio != x.EvidenceRatio || p.AskKind != x.AskKind || p.EvidenceRatio <= 0 || p.EvidenceRatio >= 1 {
		t.Errorf("predicted evidence %v %q, explained %v %q", p.EvidenceRatio, p.AskKind, x.EvidenceRatio, x.AskKind)
	}
	if x.PredictedBy == nil || *x.PredictedBy != x.Model || x.Model.Source != engine.Source || x.Model.Answers != 30 {
		t.Errorf("model %+v, predicted by %+v", x.Model, x.PredictedBy)
	}
	if x.Parts == nil || math.Abs(x.Parts.Action+x.Parts.Recent+x.Parts.Interaction-x.Mean) > 1e-9 {
		t.Errorf("parts %+v, mean %v", x.Parts, x.Mean)
	}
	if x.Verdict != limen.Allow || x.AskKind != "" {
		t.Errorf("a familiar test run: %s %q", x.Verdict, x.AskKind)
	}
	sum, ups, downs := 0.0, 0, 0
	for _, ev := range x.Evidence {
		sum += ev.Share
		if ev.Approve {
			ups++
		} else {
			downs++
		}
		if ev.Label == "" || ev.ActionID == "" || ev.At.IsZero() {
			t.Errorf("evidence %+v", ev)
		}
	}
	if ups != 3 || downs != 2 || sum > 1+1e-12 || x.Evidence[0].Share < x.Evidence[len(x.Evidence)-1].Share {
		t.Errorf("evidence of an allow: %d for, %d against, shares %v: %+v", ups, downs, sum, x.Evidence)
	}
	if x.Evidence[0].Label != "bash: go test" || !x.Evidence[0].Approve {
		t.Errorf("the answer that moved it most: %+v", x.Evidence[0])
	}
}

// Spec 009, criteria 3 and 11: a block, like an allow, has no kind of ask,
// on the prediction and on its explanation, and its strongest evidence is
// on its side: three denials and two approvals.
func TestBlockHasNoAskKind(t *testing.T) {
	e := taught(t, 15)
	a := bash("s", "probe", "rm -rf dist")
	p, err := e.Predict(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	x, err := e.Explain(ctx, a.Key)
	if err != nil {
		t.Fatal(err)
	}
	if p.Verdict != limen.Block || p.AskKind != "" || x.Verdict != limen.Block || x.AskKind != "" {
		t.Fatalf("a familiar recursive delete: predicted %s %q, explained %s %q", p.Verdict, p.AskKind, x.Verdict, x.AskKind)
	}
	ups, downs := 0, 0
	for _, ev := range x.Evidence {
		if ev.Approve {
			ups++
		} else {
			downs++
		}
	}
	if downs != 3 || ups != 2 || x.Evidence[0].Approve {
		t.Errorf("evidence of a block: %d against, %d for: %+v", downs, ups, x.Evidence)
	}
}

// Spec 009, criterion 3: an ask on no evidence is unsure, an ask on
// evidence that disagrees is split, and an allow or a block has no kind.
func TestAskKinds(t *testing.T) {
	e := taught(t, 0)
	fresh := bash("s", "fresh", "go test ./...")
	if p, err := e.Predict(ctx, fresh); err != nil || p.AskKind != limen.AskUnsure || p.EvidenceRatio != 0 {
		t.Fatalf("the first prediction for a person: %+v, %v", p, err)
	}
	x, err := e.Explain(ctx, fresh.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.Verdict != limen.Ask || x.AskKind != limen.AskUnsure || x.EvidenceRatio != 0 || len(x.Evidence) != 0 {
		t.Errorf("a person with no answers: %+v", x)
	}
	for i := range 40 {
		answer(t, e, bash("s", fmt.Sprintf("m-%d", i), "make release"), i%2 == 0)
	}
	split := bash("s", "split", "make release")
	if p, err := e.Predict(ctx, split); err != nil || p.AskKind != limen.AskSplit || p.EvidenceRatio < engine.EvidenceCut {
		t.Fatalf("a prediction on answers that disagree: %+v, %v", p, err)
	}
	x, err = e.Explain(ctx, split.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.Verdict != limen.Ask || x.AskKind != limen.AskSplit || x.EvidenceRatio < engine.EvidenceCut {
		t.Errorf("an action the person goes both ways on: %s %q ratio %v", x.Verdict, x.AskKind, x.EvidenceRatio)
	}
	ups, downs := 0, 0
	for _, ev := range x.Evidence {
		if ev.Approve {
			ups++
		} else {
			downs++
		}
	}
	if ups != 3 || downs != 3 {
		t.Errorf("evidence of an ask: %d for, %d against", ups, downs)
	}
}

// Spec 009, criterion 3, as amended on 2026-11-02: a split ask does not
// mean answers that disagree. Two approvals of an action and no denial,
// under Careful, remove more than half of the prior variance at it while
// the approval probability is still below the allow threshold, 0.90: the
// owner's live case. Its evidence and its learned entry hold approvals
// only, and more approvals move it out of the ask band to an allow.
func TestSplitOnAnswersThatAgree(t *testing.T) {
	e := taught(t, 0)
	careful := limen.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Costs: careful, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	th, err := careful.Thresholds()
	if err != nil || math.Abs(th.AllowAbove-0.9) > 1e-12 || math.Abs(th.BlockBelow-0.25) > 1e-12 {
		t.Fatalf("Careful's thresholds: %+v, %v", th, err)
	}
	publish := func(id string, i int) limen.Action {
		return across(limen.Action{Org: "o", Subject: "s", ActionID: id, Actor: &limen.Actor{Kind: "agent", ID: "agt"},
			Kind: "tool", Name: "publish", Effect: limen.EffectExternal}, i)
	}
	answer(t, e, publish("a-0", 0), true)
	answer(t, e, publish("a-1", 1), true)
	probe := publish("probe", 0)
	p, err := e.Predict(ctx, probe)
	if err != nil {
		t.Fatal(err)
	}
	if p.Thresholds != th || p.Verdict != limen.Ask || p.AskKind != limen.AskSplit || p.EvidenceRatio < engine.EvidenceCut ||
		p.Approve <= 0.5 || p.Approve >= th.AllowAbove {
		t.Fatalf("two approvals and no denial: %+v", p)
	}
	x, err := e.Explain(ctx, probe.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.AskKind != limen.AskSplit || len(x.Evidence) != 2 {
		t.Fatalf("the explanation of a split ask on two approvals: %+v", x)
	}
	for _, ev := range x.Evidence {
		if !ev.Approve {
			t.Errorf("a denial in the evidence of two approvals: %+v", ev)
		}
	}
	l, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Approved != 2 || l.Entries[0].Denied != 0 || l.Entries[0].AskKind != limen.AskSplit {
		t.Fatalf("what the model learned from two approvals: %+v", l.Entries)
	}
	for i := 2; p.Verdict == limen.Ask; i++ {
		if i > 20 {
			t.Fatalf("%d approvals and no denial are still an ask: %+v", i, p)
		}
		if p.AskKind != limen.AskSplit {
			t.Fatalf("after %d approvals: %+v", i, p)
		}
		answer(t, e, publish(fmt.Sprintf("a-%d", i), i), true)
		if p, err = e.Predict(ctx, publish(fmt.Sprintf("probe-%d", i), i)); err != nil {
			t.Fatal(err)
		}
	}
	if p.Verdict != limen.Allow || p.AskKind != "" || p.Approve <= th.AllowAbove {
		t.Errorf("more approvals: %+v, want an allow above %v", p, th.AllowAbove)
	}
}

// Spec 009, criterion 4: after the model learns more, an explanation is the
// current model's and says the prediction came from an earlier one; an
// action without a prediction is explained under the profile that applies.
func TestExplanationAfterARefit(t *testing.T) {
	e := taught(t, 5)
	a := bash("s", "early", "go test ./...")
	if _, err := e.Predict(ctx, a); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		answer(t, e, bash("s", fmt.Sprintf("later-%d", i), "go test ./..."), true)
	}
	x, err := e.Explain(ctx, a.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.PredictedBy == nil || x.PredictedBy.Answers != 10 || x.Model.Answers != 15 {
		t.Errorf("model %+v, predicted by %+v", x.Model, x.PredictedBy)
	}
	d := bash("s", "decided-only", "go test ./...")
	if err := e.Record(ctx, limen.Decision{Key: d.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "rules", Action: &d}); err != nil {
		t.Fatal(err)
	}
	x, err = e.Explain(ctx, d.Key)
	if err != nil {
		t.Fatal(err)
	}
	def, _ := limen.DefaultCosts.Thresholds()
	if x.PredictedBy != nil || x.Thresholds != def {
		t.Errorf("an action without a prediction: %+v", x)
	}
	if _, err := e.Explain(ctx, limen.Key{Org: "o", Subject: "s", ActionID: "nothing"}); limen.CodeOf(err) != limen.CodeNoCase {
		t.Errorf("an unknown action: %v", err)
	}
	if _, err := e.Explain(ctx, limen.Key{Org: "o"}); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("an empty key: %v", err)
	}
}

// What the model learned: one entry per label with its answers, the
// suggestion now under the profile that applies, and the trend.
func TestLearned(t *testing.T) {
	e := taught(t, 12)
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Kind: "tool", Agent: "agt", Costs: limen.Costs{FalseAllow: 50, FalseBlock: 4, Ask: 1}, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	l, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 2 || l.Model.Answers != 24 {
		t.Fatalf("learned %+v", l)
	}
	by := map[string]limen.LearnedEntry{}
	for _, x := range l.Entries {
		by[x.Label] = x
	}
	if x := by["bash: go test"]; x.Approved != 12 || x.Denied != 0 || x.Verdict != limen.Allow || x.Kind != "tool" || x.Name != "bash" || x.AskKind != "" || x.EvidenceRatio <= 0 {
		t.Errorf("test runs: %+v", x)
	}
	if x := by["bash: rm -r"]; x.Approved != 0 || x.Denied != 12 || x.Verdict != limen.Block {
		t.Errorf("recursive deletes: %+v", x)
	}
	if len(l.Recent) == 0 || len(l.Recent) > 20 {
		t.Fatalf("trend of %d points", len(l.Recent))
	}
	for i := 1; i < len(l.Recent); i++ {
		if l.Recent[i].T <= l.Recent[i-1].T || l.Recent[i].At.Before(l.Recent[i-1].At) {
			t.Errorf("trend out of order at %d: %+v", i, l.Recent)
		}
	}
	strict, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s", Agent: "agt", Kind: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if th := strict.Entries[0].Thresholds; math.Abs(th.AllowAbove-0.98) > 1e-12 {
		t.Errorf("the agent's profile: %+v", th)
	}
	none, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s", Kind: "egress"})
	if err != nil || len(none.Entries) != 0 {
		t.Errorf("another kind: %+v %v", none, err)
	}
	empty, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "nobody"})
	if err != nil || len(empty.Entries) != 0 || len(empty.Recent) != 0 || empty.Entries == nil || empty.Recent == nil {
		t.Errorf("a person with nothing learned: %+v %v", empty, err)
	}
	if _, err := e.Learned(ctx, limen.LearnedQuery{}); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("an empty query: %v", err)
	}
}

// Spec 009, criterion 6: a preview re-thresholds the recorded predictions
// and refuses costs that would decide on no evidence.
func TestPreview(t *testing.T) {
	e := taught(t, 12)
	for i := range 3 {
		if _, err := e.Predict(ctx, bash("s", fmt.Sprintf("probe-%d", i), "go test ./...")); err != nil {
			t.Fatal(err)
		}
	}
	page, err := e.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s", Limit: limen.MaxPageLimit})
	if err != nil {
		t.Fatal(err)
	}
	r := limen.PreviewRequest{Org: "o", Subject: "s", From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Costs: limen.Costs{FalseAllow: 2.857, FalseBlock: 2.857, Ask: 1}, AuditRate: 0.05}
	p, err := e.Preview(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	th, _ := r.Costs.Thresholds()
	want := map[limen.Verdict]int{limen.Allow: 0, limen.Ask: 0, limen.Block: 0}
	answered, allowedDenied, blockedApproved := 0, 0, 0
	for _, c := range page.Entries {
		v := th.Verdict(c.Prediction.Approve)
		want[v]++
		if n := len(c.Answers); n > 0 {
			answered++
			ok := c.Answers[n-1].Approve
			if v == limen.Allow && !ok {
				allowedDenied++
			}
			if v == limen.Block && ok {
				blockedApproved++
			}
		}
	}
	if p.Records != len(page.Entries) || p.Answered != answered || p.AllowedDenied != allowedDenied || p.BlockedApproved != blockedApproved {
		t.Errorf("preview %+v", p)
	}
	for v, n := range want {
		if p.Would[v] != n {
			t.Errorf("would %s %d, want %d", v, p.Would[v], n)
		}
	}
	if math.Abs(p.ExpectedAsks-(float64(want[limen.Ask])+0.05*float64(want[limen.Allow]+want[limen.Block]))) > 1e-12 {
		t.Errorf("expected asks %v", p.ExpectedAsks)
	}
	r.Costs = limen.Costs{FalseAllow: 1.5, FalseBlock: 4, Ask: 1}
	if _, err := e.Preview(ctx, r); limen.CodeOf(err) != limen.CodeAskTooCostly {
		t.Errorf("costs that decide on no evidence: %v", err)
	}
	r.Costs, r.To = limen.DefaultCosts, r.From
	if _, err := e.Preview(ctx, r); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("an empty window: %v", err)
	}
}
