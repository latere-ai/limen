// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package conformance is the suite every implementation of
// [cribrum.Service] passes: the engine in process, the HTTP client against a
// running cribrumd, and the stub other repositories test with. A case names
// one rule of the contract and fails with the rule's name.
//
// [Run] takes a [Target]. Target.New returns a fresh, empty service for
// one case. Most rules hold whatever the service predicts, so they run
// against New. A few need a chosen prediction, such as "an approval of
// 0.97 is an allow under the default profile"; they run against
// Target.Steered, which returns a service whose predictions come from the
// function the case passes. An implementation that learns its predictions
// leaves Steered nil, and those cases skip with that reason.
//
// Every case builds its own actions with keys unique to the case, and
// reads reliability over a window around the current time, so a service
// stamps records with its own clock.
package conformance

import (
	"context"
	"errors"
	"iter"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/cribrum"
)

// Target is the implementation the suite runs against.
type Target struct {
	// New returns a fresh, empty service.
	New func(t *testing.T) cribrum.Service
	// Steered, when not nil, returns a fresh, empty service whose
	// predictions come from predict.
	Steered func(t *testing.T, predict func(cribrum.Action) (approve, uncertainty float64)) cribrum.Service
}

type rule struct {
	name string
	run  func(t *testing.T, target Target)
}

// rules is the suite, one entry per rule of the contract.
var rules = []rule{
	{"Validation", validation},
	{"UnknownIsAnAsk", unknownIsAnAsk},
	{"PredictionFollowsTheProfile", predictionFollowsTheProfile},
	{"SteeredVerdicts", steeredVerdicts},
	{"ProfilePrecedence", profilePrecedence},
	{"ProfileDefaults", profileDefaults},
	{"AskTooCostly", askTooCostly},
	{"IdempotentPredict", idempotentPredict},
	{"OneDecision", oneDecision},
	{"DecisionNeedsAnAction", decisionNeedsAnAction},
	{"NoDecision", noDecision},
	{"AnswerRoles", answerRoles},
	{"ReviewsWeightedByProbability", reviewsWeightedByProbability},
	{"NullIsNotZero", nullIsNotZero},
	{"Unanswered", unanswered},
	{"LaterAnswersNeverEstimate", laterAnswersNeverEstimate},
	{"Sources", sources},
	{"Learned", learned},
	{"PendingAndPages", pendingAndPages},
	{"CasesNeverReturnInput", casesNeverReturnInput},
	{"ForgetIsScoped", forgetIsScoped},
	{"FailClosed", failClosed},
	{"Replay", replay},
}

// Run runs every rule against the target.
func Run(t *testing.T, target Target) {
	require(t, target.New != nil, "conformance: Target.New is nil")
	for _, r := range rules {
		t.Run(r.name, func(t *testing.T) { r.run(t, target) })
	}
}

// expect reports a broken rule and lets the case go on.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

// require reports a broken rule the rest of the case depends on.
func require(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

const (
	org     = "org_conformance"
	subject = "https://issuer.example|usr_conformance"
)

var ctx = context.Background()

func key(id string) cribrum.Key { return cribrum.Key{Org: org, Subject: subject, ActionID: id} }

func action(id string) cribrum.Action {
	sens := 0.4
	return cribrum.Action{
		Key:         key(id),
		Actor:       &cribrum.Actor{Kind: "agent", ID: "agt_conformance", Session: "ses_1"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      cribrum.EffectWrite,
		Input:       []byte(`{"command":"go test ./..."}`),
		Target:      &cribrum.Target{Kind: "path", ID: "/work", Sensitivity: &sens},
		Environment: &cribrum.Environment{Kind: "sandbox"},
	}
}

func window() (time.Time, time.Time) {
	now := time.Now()
	return now.Add(-time.Hour), now.Add(time.Hour)
}

// code requires err to carry the code and, when field is set, to name the
// field, alone or at the end of a path.
func code(t *testing.T, err error, want cribrum.Code, field string) {
	t.Helper()
	var e *cribrum.Error
	require(t, errors.As(err, &e) && e.Code == want, "error %v, want code %s", err, want)
	named := field == "" || slices.ContainsFunc(e.Fields, func(f string) bool { return f == field || strings.HasSuffix(f, "."+field) })
	expect(t, named, "error %v names fields %v, want %s", err, e.Fields, field)
}

func must(t *testing.T, err error) {
	t.Helper()
	require(t, err == nil, "%v", err)
}

func predict(t *testing.T, s cribrum.Service, a cribrum.Action) cribrum.Prediction {
	t.Helper()
	p, err := s.Predict(ctx, a)
	must(t, err)
	return p
}

// decide records a decision, sending the action so it needs no prediction.
func decide(t *testing.T, s cribrum.Service, a cribrum.Action, v cribrum.Verdict, p float64, source string) {
	t.Helper()
	must(t, s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: v, ReviewProbability: p, Source: source, Action: &a}))
}

func answer(t *testing.T, s cribrum.Service, k cribrum.Key, approve bool) {
	t.Helper()
	must(t, s.Answer(ctx, cribrum.Answer{Key: k, Approve: approve}))
}

func reliability(t *testing.T, s cribrum.Service, of string) cribrum.Reliability {
	t.Helper()
	from, to := window()
	r, err := s.Reliability(ctx, cribrum.ReliabilityQuery{Org: org, Subject: subject, Of: of, From: from, To: to})
	must(t, err)
	return r
}

func cases(t *testing.T, s cribrum.Service, q cribrum.CaseQuery) cribrum.Page {
	t.Helper()
	if q.Org == "" {
		q.Org, q.Subject = org, subject
	}
	page, err := s.Cases(ctx, q)
	must(t, err)
	return page
}

func oneCase(t *testing.T, s cribrum.Service, id string) cribrum.Case {
	t.Helper()
	page := cases(t, s, cribrum.CaseQuery{ActionID: id})
	require(t, len(page.Entries) == 1, "case %s: %d entries", id, len(page.Entries))
	return page.Entries[0]
}

// value is the estimate's value, or -1 when it is null.
func value(e cribrum.Estimate) float64 {
	v := -1.0
	if e.Value != nil {
		v = *e.Value
	}
	return v
}

// validation: every request type is validated, with its own code and the
// field at fault.
func validation(t *testing.T, target Target) {
	s := target.New(t)
	_, err := s.Predict(ctx, cribrum.Action{})
	code(t, err, cribrum.CodeInvalidAction, "org")
	code(t, s.Record(ctx, cribrum.Decision{}), cribrum.CodeInvalidDecision, "verdict")
	code(t, s.Answer(ctx, cribrum.Answer{}), cribrum.CodeInvalidAnswer, "action_id")
	_, err = s.Cases(ctx, cribrum.CaseQuery{})
	code(t, err, cribrum.CodeInvalidQuery, "subject")
	_, err = s.Reliability(ctx, cribrum.ReliabilityQuery{})
	code(t, err, cribrum.CodeInvalidQuery, "of")
	_, err = s.PutProfile(ctx, cribrum.Profile{})
	code(t, err, cribrum.CodeInvalidProfile, "org")
	_, err = s.Profile(ctx, cribrum.Scope{})
	code(t, err, cribrum.CodeInvalidQuery, "org")
	code(t, s.Forget(ctx, "", ""), cribrum.CodeInvalidQuery, "org")
	bad := action("v1")
	bad.Effect = "delete"
	_, err = s.Predict(ctx, bad)
	code(t, err, cribrum.CodeInvalidAction, "effect")
}

// unknownIsAnAsk: an action about a subject Cribrum knows nothing about is
// an ask under the default profile.
func unknownIsAnAsk(t *testing.T, target Target) {
	p := predict(t, target.New(t), action("u1"))
	ok := p.Verdict == cribrum.Ask && p.Approve >= 0 && p.Approve <= 1 && p.Source != "" && p.Key == key("u1")
	expect(t, ok, "prediction %+v, want an ask", p)
}

// predictionFollowsTheProfile: the suggestion is the thresholds' verdict
// for the approval, and the thresholds and audit rate are those of the
// profile that applies.
func predictionFollowsTheProfile(t *testing.T, target Target) {
	s := target.New(t)
	saved, err := s.PutProfile(ctx, cribrum.Profile{Org: org, Costs: cribrum.Costs{FalseAllow: 20, FalseBlock: 5, Ask: 1}, AuditRate: 0.1})
	must(t, err)
	p := predict(t, s, action("p1"))
	expect(t, p.Verdict == p.Thresholds.Verdict(p.Approve), "verdict %q, the thresholds give %q for %v", p.Verdict, p.Thresholds.Verdict(p.Approve), p.Approve)
	want, _ := saved.Costs.Thresholds()
	expect(t, p.Thresholds == want && p.AuditRate == 0.1 && p.Profile == saved.Scope,
		"prediction %+v, want thresholds %+v, audit rate 0.1, profile %+v", p, want, saved.Scope)
}

// steeredVerdicts: approvals on either side of the default thresholds give
// allow, ask and block.
func steeredVerdicts(t *testing.T, target Target) {
	if target.Steered == nil {
		t.Skip("the target learns its predictions; Target.Steered is nil")
	}
	for approve, want := range map[float64]cribrum.Verdict{0.97: cribrum.Allow, 0.5: cribrum.Ask, 0.1: cribrum.Block} {
		s := target.Steered(t, func(cribrum.Action) (float64, float64) { return approve, 0.1 })
		p := predict(t, s, action("s1"))
		expect(t, p.Verdict == want && p.Approve == approve, "approve %v: %+v, want %q", approve, p, want)
	}
}

// profilePrecedence: kind and agent, then agent, then kind, then the
// organization, then the default.
func profilePrecedence(t *testing.T, target Target) {
	s := target.New(t)
	put := func(sc cribrum.Scope, fa float64) {
		_, err := s.PutProfile(ctx, cribrum.Profile{Scope: sc, Costs: cribrum.Costs{FalseAllow: fa, FalseBlock: 4, Ask: 1}, AuditRate: 0.05})
		must(t, err)
	}
	put(cribrum.Scope{Org: org}, 10)
	put(cribrum.Scope{Org: org, Kind: "tool"}, 20)
	put(cribrum.Scope{Org: org, Agent: "agt_conformance"}, 30)
	put(cribrum.Scope{Org: org, Kind: "tool", Agent: "agt_conformance"}, 40)
	for sc, fa := range map[cribrum.Scope]float64{
		{Org: org, Kind: "tool", Agent: "agt_conformance"}:   40,
		{Org: org, Kind: "egress", Agent: "agt_conformance"}: 30,
		{Org: org, Kind: "tool", Agent: "agt_other"}:         20,
		{Org: org, Kind: "egress"}:                           10,
	} {
		p, err := s.Profile(ctx, sc)
		must(t, err)
		expect(t, p.Costs.FalseAllow == fa && !p.Default, "scope %+v: profile %+v, want false_allow %v", sc, p, fa)
	}
	other, err := s.Profile(ctx, cribrum.Scope{Org: "org_other"})
	must(t, err)
	expect(t, other.Default, "another organization's profile %+v is not the default", other)
	pr := predict(t, s, action("pp1"))
	want, _ := cribrum.Costs{FalseAllow: 40, FalseBlock: 4, Ask: 1}.Thresholds()
	expect(t, pr.Thresholds == want && pr.Profile.Kind == "tool" && pr.Profile.Agent == "agt_conformance",
		"prediction %+v, want the kind and agent profile", pr)
}

// profileDefaults: a scope with no saved profile reads the default; a
// saved profile derives its thresholds and fills a zero certify.
func profileDefaults(t *testing.T, target Target) {
	s := target.New(t)
	d, err := s.Profile(ctx, cribrum.Scope{Org: org})
	must(t, err)
	want, _ := cribrum.DefaultCosts.Thresholds()
	expect(t, d.Default && d.Costs == cribrum.DefaultCosts && d.Certify == cribrum.DefaultCertify && d.Thresholds == want && d.AuditRate == cribrum.DefaultAuditRate,
		"default %+v", d)
	saved, err := s.PutProfile(ctx, cribrum.Profile{Org: org, Costs: cribrum.Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1},
		Thresholds: cribrum.Thresholds{AllowAbove: 0.1, BlockBelow: 0.9}, Default: true})
	must(t, err)
	expect(t, !saved.Default && saved.Certify == cribrum.DefaultCertify && math.Abs(saved.Thresholds.AllowAbove-0.65) < 1e-9,
		"saved %+v", saved)
	got, err := s.Profile(ctx, cribrum.Scope{Org: org})
	must(t, err)
	expect(t, got == saved, "read back %+v, want %+v", got, saved)
}

// askTooCostly: costs that would decide on no evidence are refused.
func askTooCostly(t *testing.T, target Target) {
	_, err := target.New(t).PutProfile(ctx, cribrum.Profile{Org: org, Costs: cribrum.Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1}})
	code(t, err, cribrum.CodeAskTooCostly, "costs.false_allow")
}

// idempotentPredict: a repeat returns the first prediction; the input,
// never kept, does not tell two actions apart; any other field does.
func idempotentPredict(t *testing.T, target Target) {
	s := target.New(t)
	a := action("i1")
	first := predict(t, s, a)
	again := predict(t, s, a)
	expect(t, again == first, "repeat %+v, first %+v", again, first)
	a.Input = []byte(`{"command":"rm -rf /"}`)
	again = predict(t, s, a)
	expect(t, again == first, "repeat with another input %+v, first %+v", again, first)
	a.Name = "write"
	_, err := s.Predict(ctx, a)
	code(t, err, cribrum.CodeActionConflict, "")
}

// oneDecision: an action is decided once.
func oneDecision(t *testing.T, target Target) {
	s := target.New(t)
	a := action("d1")
	predict(t, s, a)
	must(t, s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Allow, ReviewProbability: 0.05, Source: "test/1"}))
	code(t, s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "test/1"}), cribrum.CodeAlreadyDecided, "")
}

// decisionNeedsAnAction: with no prediction the decision carries the
// action, which must match a prediction that does exist.
func decisionNeedsAnAction(t *testing.T, target Target) {
	s := target.New(t)
	a := action("n1")
	code(t, s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "test/1"}), cribrum.CodeInvalidDecision, "action")
	decide(t, s, a, cribrum.Ask, 1, "test/1")
	other := action("n1")
	other.Name = "write"
	_, err := s.Predict(ctx, other)
	code(t, err, cribrum.CodeActionConflict, "")
	b := action("n2")
	predict(t, s, b)
	b.Name = "write"
	code(t, s.Record(ctx, cribrum.Decision{Key: b.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "test/1", Action: &b}), cribrum.CodeActionConflict, "")
}

// noDecision: an answer needs a recorded decision.
func noDecision(t *testing.T, target Target) {
	s := target.New(t)
	code(t, s.Answer(ctx, cribrum.Answer{Key: key("x1"), Approve: true}), cribrum.CodeNoDecision, "")
	predict(t, s, action("x2"))
	code(t, s.Answer(ctx, cribrum.Answer{Key: key("x2"), Approve: true}), cribrum.CodeNoDecision, "")
}

// answerRoles: the first answer to a shown action is its review; later
// answers, and answers to actions nobody was shown, train only.
func answerRoles(t *testing.T, target Target) {
	s := target.New(t)
	shown, hidden := action("r1"), action("r2")
	decide(t, s, shown, cribrum.Ask, 1, "test/1")
	decide(t, s, hidden, cribrum.Allow, 0.05, "test/1")
	answer(t, s, shown.Key, true)
	answer(t, s, shown.Key, false)
	answer(t, s, hidden.Key, false)
	got := oneCase(t, s, "r1").Answers
	expect(t, len(got) == 2 && got[0].Review && !got[1].Review && got[0].Approve && !got[1].Approve && got[0].By != "",
		"answers to a shown action %+v", got)
	got = oneCase(t, s, "r2").Answers
	expect(t, len(got) == 1 && !got[0].Review, "answers to a hidden action %+v", got)
}

// reviewsWeightedByProbability: a review counts with weight one over its
// decision's review probability.
func reviewsWeightedByProbability(t *testing.T, target Target) {
	s := target.New(t)
	for i := range 20 {
		decide(t, s, action("w"+string(rune('a'+i))), cribrum.Allow, 0.05, "test/1")
	}
	audited, forced := action("wx"), action("wy")
	decide(t, s, audited, cribrum.Flag, 0.05, "test/1")
	decide(t, s, forced, cribrum.Flag, 1, "test/1")
	answer(t, s, audited.Key, false)
	answer(t, s, forced.Key, true)
	fa := reliability(t, s, cribrum.OfApplied).FalseAllow
	expect(t, math.Abs(value(fa)-20.0/22) < 1e-9, "value %v, want %v", value(fa), 20.0/22)
	expect(t, fa.Reviews == 2 && fa.Audits == 1 && fa.Covered == 1 && fa.Unanswered == 0 && fa.Interval != nil, "estimate %+v", fa)
}

// nullIsNotZero: with no action reviewable, a rate is null.
func nullIsNotZero(t *testing.T, target Target) {
	s := target.New(t)
	decide(t, s, action("z1"), cribrum.Allow, 0, "test/1")
	r := reliability(t, s, cribrum.OfApplied)
	expect(t, r.FalseAllow.Value == nil && r.FalseAllow.Covered == 0 && r.FalseBlock.Value == nil, "%+v", r)
	r = reliability(t, s, "classifier/never-seen")
	expect(t, r.FalseAllow.Value == nil, "an unknown source %+v", r)
}

// unanswered: an action shown and not yet reviewed is counted, not
// dropped.
func unanswered(t *testing.T, target Target) {
	s := target.New(t)
	decide(t, s, action("q1"), cribrum.Flag, 1, "test/1")
	fa := reliability(t, s, cribrum.OfApplied).FalseAllow
	expect(t, fa.Unanswered == 1 && fa.Reviews == 0 && fa.Value == nil, "%+v", fa)
}

// laterAnswersNeverEstimate: only reviews enter an estimate.
func laterAnswersNeverEstimate(t *testing.T, target Target) {
	s := target.New(t)
	hidden, shown := action("l1"), action("l2")
	decide(t, s, hidden, cribrum.Allow, 0, "test/1")
	answer(t, s, hidden.Key, false)
	decide(t, s, shown, cribrum.Flag, 1, "test/1")
	answer(t, s, shown.Key, true)
	answer(t, s, shown.Key, false)
	fa := reliability(t, s, cribrum.OfApplied).FalseAllow
	expect(t, value(fa) == 0 && fa.Reviews == 1 && fa.Covered == 0.5, "%+v", fa)
}

// sources: reliability is reported for a signal's source and for a
// decision's source.
func sources(t *testing.T, target Target) {
	s := target.New(t)
	judged := action("j1")
	judged.Signals = []cribrum.Signal{{Source: "classifier/conformance", Verdict: cribrum.Allow}}
	decide(t, s, judged, cribrum.Ask, 1, "rules/1")
	answer(t, s, judged.Key, false)
	fa := reliability(t, s, "classifier/conformance").FalseAllow
	expect(t, value(fa) == 1 && fa.Reviews == 1, "signal source %+v", fa)
	ruled := action("j2")
	decide(t, s, ruled, cribrum.Flag, 0.5, "rules/1")
	answer(t, s, ruled.Key, true)
	fa = reliability(t, s, "rules/1").FalseAllow
	expect(t, value(fa) == 0 && fa.Reviews == 1 && fa.Audits == 1, "decision source %+v", fa)
}

// learned: Cribrum's own suggestions are a source.
func learned(t *testing.T, target Target) {
	if target.Steered == nil {
		t.Skip("the target learns its predictions; Target.Steered is nil")
	}
	s := target.Steered(t, func(cribrum.Action) (float64, float64) { return 0.97, 0.1 })
	a := action("le1")
	p := predict(t, s, a)
	require(t, p.Verdict == cribrum.Allow, "prediction %+v, want an allow", p)
	must(t, s.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Flag, ReviewProbability: 0.05, Source: "test/1"}))
	answer(t, s, a.Key, true)
	fa := reliability(t, s, cribrum.OfLearned).FalseAllow
	expect(t, value(fa) == 0 && fa.Reviews == 1 && fa.Audits == 1, "%+v", fa)
}

// pendingAndPages: the review queue holds shown actions with no review;
// pages chain by cursor, oldest first.
func pendingAndPages(t *testing.T, target Target) {
	s := target.New(t)
	decide(t, s, action("g1"), cribrum.Ask, 1, "test/1")
	decide(t, s, action("g2"), cribrum.Allow, 0, "test/1")
	decide(t, s, action("g3"), cribrum.Flag, 1, "test/1")
	answer(t, s, key("g3"), true)
	page := cases(t, s, cribrum.CaseQuery{Pending: true})
	expect(t, len(page.Entries) == 1 && page.Entries[0].Action.ActionID == "g1", "pending %+v", page)
	first := cases(t, s, cribrum.CaseQuery{Limit: 2})
	require(t, len(first.Entries) == 2 && first.NextCursor != "", "first page %+v", first)
	second := cases(t, s, cribrum.CaseQuery{Limit: 2, Cursor: first.NextCursor})
	expect(t, len(second.Entries) == 1 && second.NextCursor == "", "second page %+v", second)
	var ids []string
	for _, c := range append(first.Entries, second.Entries...) {
		ids = append(ids, c.Action.ActionID)
	}
	expect(t, slices.Equal(ids, []string{"g1", "g2", "g3"}), "order %v, want oldest first", ids)
}

// casesNeverReturnInput: the input is read at prediction and never kept.
func casesNeverReturnInput(t *testing.T, target Target) {
	s := target.New(t)
	predict(t, s, action("c1"))
	c := oneCase(t, s, "c1")
	expect(t, len(c.Action.Input) == 0 && c.Action.Key == key("c1") && c.Prediction != nil, "case %+v", c)
}

// forgetIsScoped: forgetting a subject in one organization leaves the
// other organizations' records.
func forgetIsScoped(t *testing.T, target Target) {
	s := target.New(t)
	here, there := action("f1"), action("f1")
	there.Org = "org_conformance_other"
	decide(t, s, here, cribrum.Ask, 1, "test/1")
	decide(t, s, there, cribrum.Ask, 1, "test/1")
	must(t, s.Forget(ctx, org, subject))
	must(t, s.Forget(ctx, org, subject))
	page := cases(t, s, cribrum.CaseQuery{})
	expect(t, len(page.Entries) == 0, "the forgotten organization keeps %+v", page)
	page = cases(t, s, cribrum.CaseQuery{Org: there.Org, Subject: subject})
	expect(t, len(page.Entries) == 1, "the other organization has %+v", page)
}

type failing struct{}

func (failing) Predict(context.Context, cribrum.Action) (cribrum.Prediction, error) {
	return cribrum.Prediction{}, errors.New("conformance: the predictor is down")
}

type hanging struct{}

func (hanging) Predict(_ context.Context, a cribrum.Action) (cribrum.Prediction, error) {
	time.Sleep(100 * time.Millisecond)
	return cribrum.Prediction{Key: a.Key, Verdict: cribrum.Allow}, nil
}

// failClosed: when the predictor fails or hangs, the decision point asks,
// records the decision with the action, and the answer still lands.
func failClosed(t *testing.T, target Target) {
	s := target.New(t)
	for i, p := range []cribrum.Predictor{cribrum.FailClosed(failing{}, time.Second, nil), cribrum.FailClosed(hanging{}, 10*time.Millisecond, nil)} {
		a := action("fc" + string(rune('a'+i)))
		pr, err := p.Predict(ctx, a)
		require(t, err == nil && pr.Verdict == cribrum.Ask && pr.Source == cribrum.SourceUnavailable && pr.Key == a.Key, "fallback %+v, %v", pr, err)
		decide(t, s, a, cribrum.Ask, 1, "test/fallback")
		answer(t, s, a.Key, true)
		c := oneCase(t, s, a.ActionID)
		expect(t, c.Decision != nil && len(c.Answers) == 1 && c.Answers[0].Review, "case %+v", c)
	}
}

func records(rs ...cribrum.ReplayRecord) iter.Seq2[cribrum.ReplayRecord, error] {
	return func(yield func(cribrum.ReplayRecord, error) bool) {
		for _, r := range rs {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// replay: a replay reports what Cribrum would have suggested; without keep
// it keeps nothing, with keep it records the history.
func replay(t *testing.T, target Target) {
	at := time.Now().Add(-time.Minute)
	yes, no := true, false
	recs := []cribrum.ReplayRecord{
		{At: at, Action: action("h1"), Verdict: cribrum.Ask, ReviewProbability: 1, Source: "rules/1", Approve: &yes},
		{At: at.Add(time.Second), Action: action("h2"), Verdict: cribrum.Allow, ReviewProbability: 0, Source: "rules/1"},
		{At: at.Add(2 * time.Second), Action: action("h3"), Verdict: cribrum.Flag, ReviewProbability: 0.05, Source: "rules/1", Approve: &no},
	}
	s := target.New(t)
	rep, err := s.Replay(ctx, records(recs...), cribrum.ReplayOptions{})
	must(t, err)
	total := 0
	for _, n := range rep.Would {
		total += n
	}
	expect(t, rep.Records == 3 && rep.Answered == 2 && total == 3 && !rep.Kept, "report %+v", rep)
	page := cases(t, s, cribrum.CaseQuery{})
	expect(t, len(page.Entries) == 0, "a replay without keep kept %+v", page)
	rep, err = s.Replay(ctx, records(recs...), cribrum.ReplayOptions{Keep: true})
	must(t, err)
	expect(t, rep.Kept, "report %+v", rep)
	page = cases(t, s, cribrum.CaseQuery{})
	expect(t, len(page.Entries) == 3, "a kept replay has %+v", page)
	bad := recs[0]
	bad.Action.Kind = ""
	_, err = s.Replay(ctx, records(bad), cribrum.ReplayOptions{})
	code(t, err, cribrum.CodeInvalidReplay, "action.kind")
	boom := errors.New("conformance: the source failed")
	_, err = s.Replay(ctx, func(yield func(cribrum.ReplayRecord, error) bool) { yield(cribrum.ReplayRecord{}, boom) }, cribrum.ReplayOptions{})
	expect(t, errors.Is(err, boom), "a failing source: %v", err)
}
