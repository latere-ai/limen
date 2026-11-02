// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package conformance is the suite every implementation of
// [limen.Service] passes: the engine in process, the HTTP client against a
// running limend, and the stub other repositories test with. A case names
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
// stamps records with its own clock. The cases of the report of an
// organization need weeks that have closed; they run against
// Target.Clocked, which returns a service whose clock the case sets, and
// skip with that reason when it is nil.
package conformance

import (
	"context"
	"errors"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
)

// Target is the implementation the suite runs against.
type Target struct {
	// New returns a fresh, empty service.
	New func(t *testing.T) limen.Service
	// Steered, when not nil, returns a fresh, empty service whose
	// predictions come from predict.
	Steered func(t *testing.T, predict func(limen.Action) (approve, uncertainty float64)) limen.Service
	// Clocked, when not nil, returns a fresh, empty service that stamps
	// every record with the time now returns, a clock the case sets.
	Clocked func(t *testing.T, now func() time.Time) limen.Service
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
	{"CoverCapsAllow", coverCapsAllow},
	{"ScopeOfAnAction", scopeOfAnAction},
	{"AnswerWeights", answerWeights},
	{"ConsentOfAProfile", consentOfAProfile},
	{"AskReasons", askReasons},
	{"AnswerFields", answerFields},
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
	{"Certificate", certificate},
	{"PendingAndPages", pendingAndPages},
	{"CasesNeverReturnInput", casesNeverReturnInput},
	{"ForgetIsScoped", forgetIsScoped},
	{"FailClosed", failClosed},
	{"Replay", replay},
	{"Explanations", explanations},
	{"ExplanationsOfSteeredVerdicts", explanationsOfSteeredVerdicts},
	{"LearnedEntries", learnedEntries},
	{"Previews", previews},
	{"PreviewErrors", previewErrors},
	{"ProfileRemoved", profileRemoved},
	{"ProfileRemovalErrors", profileRemovalErrors},
	{"PersonProfileIsTheMeet", personProfileIsTheMeet},
	{"PersonProfileNeverLoosens", personProfileNeverLoosens},
	{"PersonProfileRead", personProfileRead},
	{"PersonProfileErrors", personProfileErrors},
	{"ForgetRemovesThePersonProfile", forgetRemovesThePersonProfile},
	{"ReportWeeks", reportWeeks},
	{"ReportNeedsAGroup", reportNeedsAGroup},
	{"ReportPoolsTheEstimate", reportPoolsTheEstimate},
	{"ReportAnswerRule", reportAnswerRule},
	{"ReportMergesSmallAgents", reportMergesSmallAgents},
	{"ReportCountsReviewsBeforeTheClose", reportCountsReviewsBeforeTheClose},
	{"ReportKeepsAClosedWeek", reportKeepsAClosedWeek},
	{"ReportNamesNoPerson", reportNamesNoPerson},
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

func key(id string) limen.Key { return limen.Key{Org: org, Subject: subject, ActionID: id} }

func action(id string) limen.Action {
	sens := 0.4
	return limen.Action{
		Key:         key(id),
		Actor:       &limen.Actor{Kind: "agent", ID: "agt_conformance", Session: "ses_1"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      limen.EffectWrite,
		Input:       []byte(`{"command":"go test ./..."}`),
		Target:      &limen.Target{Kind: "path", Sensitivity: &sens},
		Environment: &limen.Environment{Kind: "sandbox"},
	}
}

func window() (time.Time, time.Time) {
	now := time.Now()
	return now.Add(-time.Hour), now.Add(time.Hour)
}

// code requires err to carry the code and, when field is set, to name the
// field, alone or at the end of a path.
func code(t *testing.T, err error, want limen.Code, field string) {
	t.Helper()
	var e *limen.Error
	require(t, errors.As(err, &e) && e.Code == want, "error %v, want code %s", err, want)
	named := field == "" || slices.ContainsFunc(e.Fields, func(f string) bool { return f == field || strings.HasSuffix(f, "."+field) })
	expect(t, named, "error %v names fields %v, want %s", err, e.Fields, field)
}

func must(t *testing.T, err error) {
	t.Helper()
	require(t, err == nil, "%v", err)
}

func predict(t *testing.T, s limen.Service, a limen.Action) limen.Prediction {
	t.Helper()
	p, err := s.Predict(ctx, a)
	must(t, err)
	return p
}

// decide records a decision, sending the action so it needs no prediction.
func decide(t *testing.T, s limen.Service, a limen.Action, v limen.Verdict, p float64, source string) {
	t.Helper()
	must(t, s.Record(ctx, limen.Decision{Key: a.Key, Verdict: v, ReviewProbability: p, Source: source, Action: &a}))
}

func answer(t *testing.T, s limen.Service, k limen.Key, approve bool) {
	t.Helper()
	must(t, s.Answer(ctx, limen.Answer{Key: k, Approve: approve}))
}

func reliability(t *testing.T, s limen.Service, of string) limen.Reliability {
	t.Helper()
	from, to := window()
	r, err := s.Reliability(ctx, limen.ReliabilityQuery{Org: org, Subject: subject, Of: of, From: from, To: to})
	must(t, err)
	return r
}

func cases(t *testing.T, s limen.Service, q limen.CaseQuery) limen.Page {
	t.Helper()
	if q.Org == "" {
		q.Org, q.Subject = org, subject
	}
	page, err := s.Cases(ctx, q)
	must(t, err)
	return page
}

func oneCase(t *testing.T, s limen.Service, id string) limen.Case {
	t.Helper()
	page := cases(t, s, limen.CaseQuery{ActionID: id})
	require(t, len(page.Entries) == 1, "case %s: %d entries", id, len(page.Entries))
	return page.Entries[0]
}

// value is the estimate's value, or -1 when it is null.
func value(e limen.Estimate) float64 {
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
	_, err := s.Predict(ctx, limen.Action{})
	code(t, err, limen.CodeInvalidAction, "org")
	code(t, s.Record(ctx, limen.Decision{}), limen.CodeInvalidDecision, "verdict")
	code(t, s.Answer(ctx, limen.Answer{}), limen.CodeInvalidAnswer, "action_id")
	_, err = s.Cases(ctx, limen.CaseQuery{})
	code(t, err, limen.CodeInvalidQuery, "subject")
	_, err = s.Reliability(ctx, limen.ReliabilityQuery{})
	code(t, err, limen.CodeInvalidQuery, "of")
	_, err = s.PutProfile(ctx, limen.Profile{})
	code(t, err, limen.CodeInvalidProfile, "org")
	_, err = s.Profile(ctx, limen.Scope{})
	code(t, err, limen.CodeInvalidQuery, "org")
	code(t, s.Forget(ctx, "", ""), limen.CodeInvalidQuery, "org")
	bad := action("v1")
	bad.Effect = "delete"
	_, err = s.Predict(ctx, bad)
	code(t, err, limen.CodeInvalidAction, "effect")
}

// unknownIsAnAsk: an action about a subject Limen knows nothing about has
// an approval of one half, which is an ask under the default profile and
// under a saved profile at each end of the costs Limen accepts.
func unknownIsAnAsk(t *testing.T, target Target) {
	s := target.New(t)
	p := predict(t, s, action("u1"))
	ok := p.Verdict == limen.Ask && p.Approve == 0.5 && p.Source != "" && p.Key == key("u1")
	expect(t, ok, "prediction %+v, want an ask at 0.5", p)
	for i, costs := range []limen.Costs{
		{FalseAllow: 2.001, FalseBlock: 2.001, Ask: 1},
		{FalseAllow: 1e6, FalseBlock: 2.001, Ask: 1},
		{FalseAllow: 2.001, FalseBlock: 1e6, Ask: 1},
	} {
		_, err := s.PutProfile(ctx, limen.Profile{Org: org, Costs: costs, AuditRate: 0.05})
		must(t, err)
		id := "u" + strconv.Itoa(i+2)
		p := predict(t, s, action(id))
		expect(t, p.Verdict == limen.Ask && p.Approve == 0.5, "costs %+v: prediction %+v, want an ask at 0.5", costs, p)
	}
}

// predictionFollowsTheProfile: the suggestion is the thresholds' verdict
// for the approval after the cover rule, and the thresholds and audit rate
// are those of the profile that applies.
func predictionFollowsTheProfile(t *testing.T, target Target) {
	s := target.New(t)
	saved, err := s.PutProfile(ctx, limen.Profile{Org: org, Costs: limen.Costs{FalseAllow: 20, FalseBlock: 5, Ask: 1}, AuditRate: 0.1})
	must(t, err)
	p := predict(t, s, action("p1"))
	followsTheCap(t, "prediction", p.Approve, p.Verdict, p.AskKind, p.Thresholds, p.Cover)
	expect(t, p.EvidenceRatio >= 0 && p.EvidenceRatio <= 1, "evidence ratio %v outside [0, 1]", p.EvidenceRatio)
	want, _ := saved.Costs.Thresholds()
	expect(t, p.Thresholds == want && p.AuditRate == 0.1 && p.Profile == saved.Scope,
		"prediction %+v, want thresholds %+v, audit rate 0.1, profile %+v", p, want, saved.Scope)
}

// steeredVerdicts: approvals on either side of the default thresholds give
// allow, ask and block, once the person's approvals cover the action.
func steeredVerdicts(t *testing.T, target Target) {
	if target.Steered == nil {
		t.Skip("the target learns its predictions; Target.Steered is nil")
	}
	for approve, want := range map[float64]limen.Verdict{0.97: limen.Allow, 0.5: limen.Ask, 0.1: limen.Block} {
		s := target.Steered(t, func(limen.Action) (float64, float64) { return approve, 0.1 })
		meetTheCover(t, s, "s")
		p := predict(t, s, action("s1"))
		expect(t, p.Verdict == want && p.Approve == approve, "approve %v: %+v, want %q", approve, p, want)
	}
}

// inBand and capped are the kinds of an ask in the ask band, and of an
// allow the cover rule turned into an ask.
var (
	inBand = []string{limen.AskUnsure, limen.AskSplit, limen.AskShort}
	capped = []string{limen.AskWider, limen.AskFew}
)

// followsTheCap checks a verdict against the thresholds and the cover
// rule: it is the thresholds' verdict, and an allow is covered; or it is an
// ask of kind wider or few in place of their allow, with a cover that does
// not cover the action. An ask in the band has a kind of the band, and
// only an ask has a kind.
func followsTheCap(t *testing.T, what string, approve float64, v limen.Verdict, kind string, th limen.Thresholds, c limen.Cover) {
	t.Helper()
	want := th.Verdict(approve)
	switch {
	case v == want && v == limen.Ask:
		expect(t, slices.Contains(inBand, kind), "%s: an ask in the band of kind %q", what, kind)
	case v == want:
		expect(t, kind == "", "%s: a %s of kind %q", what, v, kind)
		expect(t, v != limen.Allow || c.Covered, "%s: an allow with the cover %+v, which does not cover it", what, c)
	case v == limen.Ask && want == limen.Allow:
		expect(t, !c.Covered && slices.Contains(capped, kind), "%s: an ask of kind %q in place of an allow, with the cover %+v", what, kind, c)
	default:
		t.Errorf("%s: verdict %q, the thresholds give %q for %v", what, v, want, approve)
	}
	expect(t, c.MinWeight >= limen.MinWeight && c.MinSessions >= limen.MinSessions,
		"%s: the cover %+v has settings below the contract's", what, c)
}

// in is the action id in the session ses; an empty ses is no session.
func in(id, ses string) limen.Action {
	a := action(id)
	a.Actor.Session = ses
	return a
}

// considered is how a person who opened the details answers: the answer
// has weight 1 under weights/1, whatever its time.
var considered = limen.HowAnswered{DetailsOpened: new(true)}

// approved records an ask for a and the person's considered answer to it.
func approved(t *testing.T, s limen.Service, a limen.Action, approve bool) {
	t.Helper()
	decide(t, s, a, limen.Ask, 1, "test/1")
	must(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve, HowAnswered: considered}))
}

// meetTheCover approves, with considered answers, limen.MinWeight actions
// of the family and scope of action, over limen.MinSessions sessions, so
// that they cover it. Their ids start with prefix.
func meetTheCover(t *testing.T, s limen.Service, prefix string) {
	t.Helper()
	for i := range max(limen.MinWeight, limen.MinSessions) {
		approved(t, s, in(prefix+"-cover-"+strconv.Itoa(i), "ses_cover_"+strconv.Itoa(i%max(limen.MinSessions, 1))), true)
	}
}

// coverCapsAllow (spec 011): an allow the thresholds give is an ask until
// considered approvals of the action's family, whose scope the action is
// within, have limen.MinWeight in total from limen.MinSessions sessions.
// The ask is wider while no approval of any weight covers the action, and
// few after. A denial never counts, nor an approval a later denial
// replaced, nor an approval of another family, nor a quick approval; an
// action with no session counts as its UTC day; a block is never changed;
// the cap changes no number of the prediction. An explanation and a learned
// entry hold the same. The cover rests on the ledger alone, so a learned
// target is held to the same covers.
func coverCapsAllow(t *testing.T, target Target) {
	steered := target.Steered != nil
	var s limen.Service
	if steered {
		s = target.Steered(t, func(a limen.Action) (float64, float64) {
			if strings.HasPrefix(a.ActionID, "cvb") {
				return 0.1, 0.1
			}
			return 0.97, 0.1
		})
	} else {
		s = target.New(t)
		// The lowest allow threshold a profile can have short of 1/2, so
		// that a learned target allows on few approvals.
		_, err := s.PutProfile(ctx, limen.Profile{Org: org, Costs: limen.Costs{FalseAllow: 2.001, FalseBlock: 2.001, Ask: 1}, AuditRate: 0.05})
		must(t, err)
	}
	probes := 0
	check := func(a limen.Action, weight float64, sessions int, wider bool) {
		t.Helper()
		probes++
		a.Key = key("cvp" + strconv.Itoa(probes))
		p := predict(t, s, a)
		what := "probe " + a.ActionID
		followsTheCap(t, what, p.Approve, p.Verdict, p.AskKind, p.Thresholds, p.Cover)
		want := limen.Cover{Covered: weight >= limen.MinWeight && sessions >= limen.MinSessions, Weight: weight, Sessions: sessions,
			MinWeight: limen.MinWeight, MinSessions: limen.MinSessions}
		expect(t, p.Cover == want, "%s: the cover %+v, want %+v", what, p.Cover, want)
		if p.Thresholds.Verdict(p.Approve) == limen.Allow && !want.Covered {
			kind := map[bool]string{true: limen.AskWider, false: limen.AskFew}[wider]
			expect(t, p.Verdict == limen.Ask && p.AskKind == kind && p.Reason != "", "%s: %+v, want an ask of kind %s", what, p, kind)
		}
		if steered {
			v := map[bool]limen.Verdict{true: limen.Allow, false: limen.Ask}[want.Covered]
			expect(t, p.Verdict == v && p.Approve == 0.97 && p.Uncertainty == 0.1, "%s: %+v, want %q at 0.97", what, p, v)
		}
		x, err := s.Explain(ctx, a.Key)
		must(t, err)
		followsTheCap(t, what+", the explanation", x.Approve, x.Verdict, x.AskKind, x.Thresholds, x.Cover.Cover)
		expect(t, x.Cover.Cover == want, "%s: the explanation's cover %+v, want %+v", what, x.Cover.Cover, want)
	}
	probe := in("", "ses_1")
	check(probe, 0, 0, true)
	approved(t, s, in("cv1", "ses_1"), true)
	check(probe, 1, 1, false)
	approved(t, s, in("cv2", "ses_1"), true)
	approved(t, s, in("cv3", "ses_1"), true)
	check(probe, 3, 1, false)
	approved(t, s, in("cvd", "ses_2"), false)
	approved(t, s, command("cvo", "ls -la"), true)
	quick := in("cvq", "ses_3")
	decide(t, s, quick, limen.Ask, 1, "test/1")
	answer(t, s, quick.Key, true)
	check(probe, 3, 1, false)
	// A wider action: the approvals do not cover it.
	public := probe
	public.Scope = &limen.ReachClaim{Crosses: limen.Crosses{Public: limen.BoundYes}}
	check(public, 0, 0, true)
	approved(t, s, in("cvn", ""), true)
	check(probe, 4, 2, false)
	answer(t, s, key("cvn"), false)
	check(probe, 3, 1, false)
	approved(t, s, in("cv4", "ses_2"), true)
	check(probe, 4, 2, false)

	l, err := s.Learned(ctx, limen.LearnedQuery{Org: org, Subject: subject})
	must(t, err)
	for _, e := range l.Entries {
		followsTheCap(t, "entry "+e.Label, e.Approve, e.Verdict, e.AskKind, e.Thresholds, e.Cover)
	}
	if steered {
		b := predict(t, s, in("cvb1", "ses_1"))
		expect(t, b.Verdict == limen.Block && b.AskKind == "" && b.Cover.Covered, "a block: %+v", b)
		f := target.Steered(t, func(limen.Action) (float64, float64) { return 0.97, 0.1 })
		approved(t, f, in("cvf", "ses_1"), true)
		l, err := f.Learned(ctx, limen.LearnedQuery{Org: org, Subject: subject})
		must(t, err)
		want := limen.Cover{Weight: 1, Sessions: 1, MinWeight: limen.MinWeight, MinSessions: limen.MinSessions}
		expect(t, len(l.Entries) == 1 && l.Entries[0].Verdict == limen.Ask && l.Entries[0].AskKind == limen.AskFew && l.Entries[0].Cover == want,
			"the learned entry of one approval: %+v, want a few ask with %+v", l.Entries, want)
	}
}

// scopeOfAnAction (spec 011): a prediction gives the scope Limen used,
// read from the input and merged with the claims, in words of the
// vocabularies alone; a claim widens and never narrows; a value outside the
// vocabulary is unknown, and it is not an error.
func scopeOfAnAction(t *testing.T, target Target) {
	s := target.New(t)
	push := command("sc1", "git push --force origin release/feature-secret-plan")
	push.Environment = &limen.Environment{Kind: "sandbox", Repositories: limen.RepositoriesPublic}
	p := predict(t, s, push)
	want := limen.Reach{Undo: limen.UndoIrreversible, Crosses: limen.Crosses{Machine: limen.BoundNo, Remote: limen.BoundYes, Network: limen.BoundNo, Public: limen.BoundYes},
		TargetClass: limen.ClassPrimary, Contact: limen.Unknown, TargetKind: "path"}
	expect(t, p.Scope == want, "a force push: scope %+v, want %+v", p.Scope, want)
	claimed := action("sc2")
	claimed.Scope = &limen.ReachClaim{Undo: limen.UndoNone, Contact: "sometimes", Crosses: limen.Crosses{Network: limen.BoundYes}}
	claimed.Target = &limen.Target{Kind: "path", Class: "galaxy"}
	claimed.Environment = &limen.Environment{Kind: "sandbox", Holds: []string{"moat", limen.HoldEgress}, Repositories: "secret"}
	p = predict(t, s, claimed)
	want = limen.Reach{Undo: limen.UndoReversible, Crosses: limen.Crosses{Machine: limen.BoundNo, Remote: limen.BoundYes, Network: limen.BoundYes, Public: limen.BoundNo},
		TargetClass: limen.Unknown, Contact: limen.Unknown, TargetKind: "path"}
	expect(t, p.Scope == want, "go test with claims: scope %+v, want %+v", p.Scope, want)
	for _, id := range []string{"sc1", "sc2"} {
		c := oneCase(t, s, id)
		expect(t, c.Prediction != nil && c.Prediction.Scope.Undo != "", "case %s keeps no scope: %+v", id, c.Prediction)
	}
	page := cases(t, s, limen.CaseQuery{})
	for _, c := range page.Entries {
		if c.Prediction == nil {
			continue
		}
		for _, w := range []string{c.Prediction.Scope.Undo, c.Prediction.Scope.TargetClass, c.Prediction.Scope.Contact, c.Prediction.Scope.TargetKind} {
			expect(t, !strings.Contains(w, "secret") && !strings.Contains(w, "plan"), "the scope of %s holds %q", c.Action.ActionID, w)
		}
	}
}

// answerWeights (spec 011): an approval counts toward the cover with
// weight 1 only if it is considered. Quick approvals, by Limen's clock, the
// client's or the decision point's, one gesture for many, a standing rule,
// and an approval late in a long session count 0, unless the person opened
// the details. The rules need the time between a decision and its answer,
// so they run against Clocked.
func answerWeights(t *testing.T, target Target) {
	if target.Clocked == nil {
		t.Skip("the target has no clock the case can set; Target.Clocked is nil")
	}
	now := time.Now().Add(-time.Hour)
	s := target.Clocked(t, func() time.Time { return now })
	n := 0
	give := func(ses string, delay time.Duration, how limen.HowAnswered) {
		t.Helper()
		n++
		a := in("aw"+strconv.Itoa(n), ses)
		decide(t, s, a, limen.Ask, 1, "test/1")
		now = now.Add(delay)
		must(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: true, HowAnswered: how}))
		now = now.Add(time.Second)
	}
	probe := func(weight float64, what string) {
		t.Helper()
		n++
		p := predict(t, s, in("aw"+strconv.Itoa(n), "ses_probe"))
		expect(t, p.Cover.Weight == weight, "%s: weight %v, want %v", what, p.Cover.Weight, weight)
	}
	ms := func(x int64) *int64 { return &x }
	give("ses_a", 3*time.Second, limen.HowAnswered{})
	probe(1, "a slow approval")
	give("ses_a", time.Second, limen.HowAnswered{})
	probe(1, "a quick approval")
	give("ses_a", time.Second, limen.HowAnswered{DetailsOpened: new(true)})
	probe(2, "a quick approval with its details")
	give("ses_a", 5*time.Second, limen.HowAnswered{AnswerMS: ms(800)})
	probe(2, "quick on the client")
	give("ses_a", 5*time.Second, limen.HowAnswered{WaitedMS: ms(1500), AnswerMS: ms(9000)})
	probe(2, "quick for the decision point")
	give("ses_a", 5*time.Second, limen.HowAnswered{Surface: limen.SurfaceBulk})
	probe(2, "one gesture for many")
	give("ses_a", 5*time.Second, limen.HowAnswered{Surface: limen.SurfaceRule, DetailsOpened: new(true)})
	probe(2, "a standing rule")
	give("ses_a", 5*time.Second, limen.HowAnswered{Surface: "wristwatch", AnswerMS: ms(5000)})
	probe(3, "a surface outside the list")
	// Late in a long session: 50 reviews come before the next approval.
	for range limen.AnswerFatigue {
		give("ses_long", 3*time.Second, limen.HowAnswered{DetailsOpened: new(true)})
	}
	p := predict(t, s, in("aw-before", "ses_probe"))
	before := p.Cover.Weight
	give("ses_long", 3*time.Second, limen.HowAnswered{})
	probe(before, "an approval after 50 reviews in its session")
	give("ses_long", 3*time.Second, limen.HowAnswered{DetailsOpened: new(true)})
	probe(before+1, "a late approval with its details")
}

// consentOfAProfile (spec 011): a profile can make the settings of the
// cover rule greater and never smaller, a value below zero is refused, and
// the meet of the organization's profile and a person's own takes the
// greater of each.
func consentOfAProfile(t *testing.T, target Target) {
	s := target.New(t)
	_, err := s.PutProfile(ctx, limen.Profile{Org: org, Costs: limen.DefaultCosts, Consent: limen.Consent{MinWeight: -1}})
	code(t, err, limen.CodeInvalidProfile, "consent.min_weight")
	saved, err := s.PutProfile(ctx, limen.Profile{Org: org, Costs: limen.DefaultCosts, AuditRate: 0.05, Consent: limen.Consent{MinWeight: 5, MinSessions: 1}})
	must(t, err)
	expect(t, saved.Consent == limen.Consent{MinWeight: 5, MinSessions: 1}, "saved %+v", saved.Consent)
	p := predict(t, s, action("cs1"))
	expect(t, p.Cover.MinWeight == 5 && p.Cover.MinSessions == limen.MinSessions, "a profile of 5 and 1: the cover %+v", p.Cover)
	_, err = s.PutProfile(ctx, limen.Profile{Org: org, Subject: subject, Costs: limen.DefaultCosts, AuditRate: 0.05, Consent: limen.Consent{MinWeight: 4, MinSessions: 6}})
	must(t, err)
	p = predict(t, s, action("cs2"))
	expect(t, p.Cover.MinWeight == 5 && p.Cover.MinSessions == 6 && p.Profile.Subject == subject, "the meet: the cover %+v of %+v", p.Cover, p.Profile)
	x, err := s.Explain(ctx, key("cs2"))
	must(t, err)
	expect(t, x.Cover.MinWeight == 5 && x.Cover.MinSessions == 6, "the explanation's cover %+v", x.Cover)
}

// askReasons (spec 011): the reason of an ask states no probability, in a
// prediction and in an explanation, whatever its kind.
func askReasons(t *testing.T, target Target) {
	decimal := func(r string) bool {
		for i := 1; i+1 < len(r); i++ {
			if r[i] == '.' && r[i-1] >= '0' && r[i-1] <= '9' && r[i+1] >= '0' && r[i+1] <= '9' {
				return true
			}
		}
		return strings.Contains(r, "probability")
	}
	var services []limen.Service
	services = append(services, target.New(t))
	if target.Steered != nil {
		services = append(services, target.Steered(t, func(limen.Action) (float64, float64) { return 0.97, 0.1 }),
			target.Steered(t, func(limen.Action) (float64, float64) { return 0.6, 0.1 }))
	}
	for i, s := range services {
		approved(t, s, in("ar0", "ses_1"), true)
		for _, a := range []limen.Action{action("ar1"), command("ar2", "rm -rf ~/x")} {
			p := predict(t, s, a)
			x, err := s.Explain(ctx, a.Key)
			must(t, err)
			for what, r := range map[string]string{"prediction": p.Reason, "explanation": x.Reason} {
				if p.Verdict == limen.Ask {
					expect(t, r != "" && !decimal(r), "service %d, %s %s of kind %s: the reason %q states a probability", i, what, a.ActionID, p.AskKind, r)
				}
			}
		}
	}
}

// answerFields (spec 011): an answer keeps how the person gave it, a
// surface outside the list is dropped and is no error, and a time outside
// its range is refused.
func answerFields(t *testing.T, target Target) {
	s := target.New(t)
	a := action("af1")
	decide(t, s, a, limen.Ask, 1, "test/1")
	ms := func(x int64) *int64 { return &x }
	how := limen.HowAnswered{AnswerMS: ms(4200), WaitedMS: ms(5000), DetailsOpened: new(false), SuggestionShown: new(true),
		Surface: limen.SurfaceNotification, Remembered: true}
	must(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: true, HowAnswered: how}))
	must(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: false, Surface: "wristwatch"}))
	got := oneCase(t, s, "af1").Answers
	require(t, len(got) == 2, "answers %+v", got)
	kept := got[0].HowAnswered
	expect(t, kept.AnswerMS != nil && *kept.AnswerMS == 4200 && kept.WaitedMS != nil && *kept.WaitedMS == 5000 &&
		kept.DetailsOpened != nil && !*kept.DetailsOpened && kept.SuggestionShown != nil && *kept.SuggestionShown &&
		kept.Surface == limen.SurfaceNotification && kept.Remembered, "kept %+v", kept)
	expect(t, got[1].Surface == "", "a surface outside the list is kept: %+v", got[1])
	code(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: true, AnswerMS: ms(limen.MaxAnswerMS + 1)}), limen.CodeInvalidAnswer, "answer_ms")
	code(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: true, WaitedMS: ms(-1)}), limen.CodeInvalidAnswer, "waited_ms")
}

// profilePrecedence: kind and agent, then agent, then kind, then the
// organization, then the default.
func profilePrecedence(t *testing.T, target Target) {
	s := target.New(t)
	put := func(sc limen.Scope, fa float64) {
		_, err := s.PutProfile(ctx, limen.Profile{Scope: sc, Costs: limen.Costs{FalseAllow: fa, FalseBlock: 4, Ask: 1}, AuditRate: 0.05})
		must(t, err)
	}
	put(limen.Scope{Org: org}, 10)
	put(limen.Scope{Org: org, Kind: "tool"}, 20)
	put(limen.Scope{Org: org, Agent: "agt_conformance"}, 30)
	put(limen.Scope{Org: org, Kind: "tool", Agent: "agt_conformance"}, 40)
	for sc, fa := range map[limen.Scope]float64{
		{Org: org, Kind: "tool", Agent: "agt_conformance"}:   40,
		{Org: org, Kind: "egress", Agent: "agt_conformance"}: 30,
		{Org: org, Kind: "tool", Agent: "agt_other"}:         20,
		{Org: org, Kind: "egress"}:                           10,
	} {
		p, err := s.Profile(ctx, sc)
		must(t, err)
		expect(t, p.Costs.FalseAllow == fa && !p.Default, "scope %+v: profile %+v, want false_allow %v", sc, p, fa)
	}
	other, err := s.Profile(ctx, limen.Scope{Org: "org_other"})
	must(t, err)
	expect(t, other.Default, "another organization's profile %+v is not the default", other)
	pr := predict(t, s, action("pp1"))
	want, _ := limen.Costs{FalseAllow: 40, FalseBlock: 4, Ask: 1}.Thresholds()
	expect(t, pr.Thresholds == want && pr.Profile.Kind == "tool" && pr.Profile.Agent == "agt_conformance",
		"prediction %+v, want the kind and agent profile", pr)
}

// profileDefaults: a scope with no saved profile reads the default; a
// saved profile derives its thresholds and fills a zero certify.
func profileDefaults(t *testing.T, target Target) {
	s := target.New(t)
	d, err := s.Profile(ctx, limen.Scope{Org: org})
	must(t, err)
	want, _ := limen.DefaultCosts.Thresholds()
	expect(t, d.Default && d.Costs == limen.DefaultCosts && d.Certify == limen.DefaultCertify && d.Thresholds == want && d.AuditRate == limen.DefaultAuditRate,
		"default %+v", d)
	saved, err := s.PutProfile(ctx, limen.Profile{Org: org, Costs: limen.Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1},
		Thresholds: limen.Thresholds{AllowAbove: 0.1, BlockBelow: 0.9}, Default: true})
	must(t, err)
	expect(t, !saved.Default && saved.Certify == limen.DefaultCertify && math.Abs(saved.Thresholds.AllowAbove-0.65) < 1e-9,
		"saved %+v", saved)
	got, err := s.Profile(ctx, limen.Scope{Org: org})
	must(t, err)
	expect(t, got == saved, "read back %+v, want %+v", got, saved)
}

// askTooCostly: costs that would decide on no evidence are refused.
func askTooCostly(t *testing.T, target Target) {
	_, err := target.New(t).PutProfile(ctx, limen.Profile{Org: org, Costs: limen.Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1}})
	code(t, err, limen.CodeAskTooCostly, "costs.false_allow")
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
	code(t, err, limen.CodeActionConflict, "")
}

// oneDecision: an action is decided once.
func oneDecision(t *testing.T, target Target) {
	s := target.New(t)
	a := action("d1")
	predict(t, s, a)
	must(t, s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Allow, ReviewProbability: 0.05, Source: "test/1"}))
	code(t, s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "test/1"}), limen.CodeAlreadyDecided, "")
}

// decisionNeedsAnAction: with no prediction the decision carries the
// action, which must match a prediction that does exist.
func decisionNeedsAnAction(t *testing.T, target Target) {
	s := target.New(t)
	a := action("n1")
	code(t, s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "test/1"}), limen.CodeInvalidDecision, "action")
	decide(t, s, a, limen.Ask, 1, "test/1")
	other := action("n1")
	other.Name = "write"
	_, err := s.Predict(ctx, other)
	code(t, err, limen.CodeActionConflict, "")
	b := action("n2")
	predict(t, s, b)
	b.Name = "write"
	code(t, s.Record(ctx, limen.Decision{Key: b.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "test/1", Action: &b}), limen.CodeActionConflict, "")
}

// noDecision: an answer needs a recorded decision.
func noDecision(t *testing.T, target Target) {
	s := target.New(t)
	code(t, s.Answer(ctx, limen.Answer{Key: key("x1"), Approve: true}), limen.CodeNoDecision, "")
	predict(t, s, action("x2"))
	code(t, s.Answer(ctx, limen.Answer{Key: key("x2"), Approve: true}), limen.CodeNoDecision, "")
}

// answerRoles: the first answer to a shown action is its review; later
// answers, and answers to actions nobody was shown, train only.
func answerRoles(t *testing.T, target Target) {
	s := target.New(t)
	shown, hidden := action("r1"), action("r2")
	decide(t, s, shown, limen.Ask, 1, "test/1")
	decide(t, s, hidden, limen.Allow, 0.05, "test/1")
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
		decide(t, s, action("w"+string(rune('a'+i))), limen.Allow, 0.05, "test/1")
	}
	audited, forced := action("wx"), action("wy")
	decide(t, s, audited, limen.Flag, 0.05, "test/1")
	decide(t, s, forced, limen.Flag, 1, "test/1")
	answer(t, s, audited.Key, false)
	answer(t, s, forced.Key, true)
	fa := reliability(t, s, limen.OfApplied).FalseAllow
	expect(t, math.Abs(value(fa)-20.0/22) < 1e-9, "value %v, want %v", value(fa), 20.0/22)
	expect(t, fa.Reviews == 2 && fa.Audits == 1 && fa.Covered == 1 && fa.Unanswered == 0 && fa.Interval != nil, "estimate %+v", fa)
}

// nullIsNotZero: with no action reviewable, a rate is null.
func nullIsNotZero(t *testing.T, target Target) {
	s := target.New(t)
	decide(t, s, action("z1"), limen.Allow, 0, "test/1")
	r := reliability(t, s, limen.OfApplied)
	expect(t, r.FalseAllow.Value == nil && r.FalseAllow.Covered == 0 && r.FalseBlock.Value == nil, "%+v", r)
	r = reliability(t, s, "classifier/never-seen")
	expect(t, r.FalseAllow.Value == nil, "an unknown source %+v", r)
}

// unanswered: an action shown and not yet reviewed is counted, not
// dropped.
func unanswered(t *testing.T, target Target) {
	s := target.New(t)
	decide(t, s, action("q1"), limen.Flag, 1, "test/1")
	fa := reliability(t, s, limen.OfApplied).FalseAllow
	expect(t, fa.Unanswered == 1 && fa.Reviews == 0 && fa.Value == nil, "%+v", fa)
}

// laterAnswersNeverEstimate: only reviews enter an estimate.
func laterAnswersNeverEstimate(t *testing.T, target Target) {
	s := target.New(t)
	hidden, shown := action("l1"), action("l2")
	decide(t, s, hidden, limen.Allow, 0, "test/1")
	answer(t, s, hidden.Key, false)
	decide(t, s, shown, limen.Flag, 1, "test/1")
	answer(t, s, shown.Key, true)
	answer(t, s, shown.Key, false)
	fa := reliability(t, s, limen.OfApplied).FalseAllow
	expect(t, value(fa) == 0 && fa.Reviews == 1 && fa.Covered == 0.5, "%+v", fa)
}

// sources: reliability is reported for a signal's source and for a
// decision's source.
func sources(t *testing.T, target Target) {
	s := target.New(t)
	judged := action("j1")
	judged.Signals = []limen.Signal{{Source: "classifier/conformance", Verdict: limen.Allow}}
	decide(t, s, judged, limen.Ask, 1, "rules/1")
	answer(t, s, judged.Key, false)
	fa := reliability(t, s, "classifier/conformance").FalseAllow
	expect(t, value(fa) == 1 && fa.Reviews == 1, "signal source %+v", fa)
	ruled := action("j2")
	decide(t, s, ruled, limen.Flag, 0.5, "rules/1")
	answer(t, s, ruled.Key, true)
	fa = reliability(t, s, "rules/1").FalseAllow
	expect(t, value(fa) == 0 && fa.Reviews == 1 && fa.Audits == 1, "decision source %+v", fa)
}

// learned: Limen's own suggestions are a source.
func learned(t *testing.T, target Target) {
	if target.Steered == nil {
		t.Skip("the target learns its predictions; Target.Steered is nil")
	}
	s := target.Steered(t, func(limen.Action) (float64, float64) { return 0.97, 0.1 })
	meetTheCover(t, s, "le")
	a := action("le1")
	p := predict(t, s, a)
	require(t, p.Verdict == limen.Allow, "prediction %+v, want an allow", p)
	must(t, s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Flag, ReviewProbability: 0.05, Source: "test/1"}))
	answer(t, s, a.Key, true)
	fa := reliability(t, s, limen.OfLearned).FalseAllow
	expect(t, value(fa) == 0 && fa.Reviews == 1 && fa.Audits == 1, "%+v", fa)
}

// certificate: the certificate counts the most recent audited allows the
// person approved, back to the first denial, and needs the size of the
// organization's claim (paper, Proposition 5(b)). A review with
// probability one is no audit, and an allow nobody saw is no review. It
// counts the considered answers among the clean audits, and the claim does
// not depend on them.
func certificate(t *testing.T, target Target) {
	s := target.New(t)
	c := reliability(t, s, limen.OfApplied).Certificate
	expect(t, c.Alpha == limen.DefaultCertify.Alpha && c.Delta == limen.DefaultCertify.Delta && c.Needed == 300 && c.Clean == 0 && !c.Holds,
		"with the default claim and no audits: %+v", c)
	claim := limen.Certify{Alpha: 0.5, Delta: 0.5}
	_, err := s.PutProfile(ctx, limen.Profile{Org: org, Costs: limen.DefaultCosts, AuditRate: 0.05, Certify: claim})
	must(t, err)
	for _, step := range []struct {
		id      string
		verdict limen.Verdict
		prob    float64
		approve *bool
		how     limen.HowAnswered
	}{
		{"ct1", limen.Flag, 0.05, new(true), considered},
		{"ct2", limen.Flag, 0.05, new(false), considered},
		{"ct3", limen.Flag, 0.05, new(true), limen.HowAnswered{}},
		{"ct4", limen.Flag, 1, new(true), considered},
		{"ct5", limen.Allow, 0.05, nil, limen.HowAnswered{}},
		{"ct6", limen.Flag, 0.05, new(true), considered},
	} {
		a := action(step.id)
		decide(t, s, a, step.verdict, step.prob, "test/1")
		if step.approve != nil {
			must(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: *step.approve, HowAnswered: step.how}))
		}
	}
	needed, err := limen.CertificationSize(claim.Alpha, claim.Delta)
	must(t, err)
	c = reliability(t, s, limen.OfApplied).Certificate
	expect(t, c.Alpha == claim.Alpha && c.Delta == claim.Delta && c.Clean == 2 && c.Needed == needed && c.Holds,
		"certificate %+v, want 2 clean audits of %d needed", c, needed)
	// The quick approval of ct3 is no considered answer, and the claim does
	// not read it (spec 011).
	expect(t, c.Considered == 1, "certificate %+v, want 1 considered audit of the 2 clean", c)
}

// pendingAndPages: the review queue holds shown actions with no review;
// pages chain by cursor, oldest first.
func pendingAndPages(t *testing.T, target Target) {
	s := target.New(t)
	decide(t, s, action("g1"), limen.Ask, 1, "test/1")
	decide(t, s, action("g2"), limen.Allow, 0, "test/1")
	decide(t, s, action("g3"), limen.Flag, 1, "test/1")
	answer(t, s, key("g3"), true)
	page := cases(t, s, limen.CaseQuery{Pending: true})
	expect(t, len(page.Entries) == 1 && page.Entries[0].Action.ActionID == "g1", "pending %+v", page)
	first := cases(t, s, limen.CaseQuery{Limit: 2})
	require(t, len(first.Entries) == 2 && first.NextCursor != "", "first page %+v", first)
	second := cases(t, s, limen.CaseQuery{Limit: 2, Cursor: first.NextCursor})
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

// forgetIsScoped: forgetting a subject in one organization removes its
// cases, its reviews and its reports there, and leaves the other
// organizations' records.
func forgetIsScoped(t *testing.T, target Target) {
	s := target.New(t)
	here, there := action("f1"), action("f1")
	there.Org = "org_conformance_other"
	for _, a := range []limen.Action{here, there} {
		decide(t, s, a, limen.Flag, 0.5, "test/1")
		answer(t, s, a.Key, false)
	}
	reviews := func(o string) int {
		from, to := window()
		r, err := s.Reliability(ctx, limen.ReliabilityQuery{Org: o, Subject: subject, Of: limen.OfApplied, From: from, To: to})
		must(t, err)
		return r.FalseAllow.Reviews
	}
	require(t, reviews(org) == 1 && reviews(there.Org) == 1, "a review in each organization before Forget")
	must(t, s.Forget(ctx, org, subject))
	must(t, s.Forget(ctx, org, subject))
	page := cases(t, s, limen.CaseQuery{})
	expect(t, len(page.Entries) == 0, "the forgotten organization keeps %+v", page)
	expect(t, reviews(org) == 0, "the forgotten organization still reports its review")
	page = cases(t, s, limen.CaseQuery{Org: there.Org, Subject: subject})
	expect(t, len(page.Entries) == 1 && len(page.Entries[0].Answers) == 1 && reviews(there.Org) == 1, "the other organization has %+v", page)
}

type failing struct{}

func (failing) Predict(context.Context, limen.Action) (limen.Prediction, error) {
	return limen.Prediction{}, errors.New("conformance: the predictor is down")
}

type hanging struct{}

func (hanging) Predict(_ context.Context, a limen.Action) (limen.Prediction, error) {
	time.Sleep(100 * time.Millisecond)
	return limen.Prediction{Key: a.Key, Verdict: limen.Allow}, nil
}

// failClosed: when the predictor fails or hangs, the decision point asks,
// records the decision with the action, and the answer still lands.
func failClosed(t *testing.T, target Target) {
	s := target.New(t)
	for i, p := range []limen.Predictor{limen.FailClosed(failing{}, time.Second, nil), limen.FailClosed(hanging{}, 10*time.Millisecond, nil)} {
		a := action("fc" + string(rune('a'+i)))
		pr, err := p.Predict(ctx, a)
		require(t, err == nil && pr.Verdict == limen.Ask && pr.Source == limen.SourceUnavailable && pr.Key == a.Key, "fallback %+v, %v", pr, err)
		decide(t, s, a, limen.Ask, 1, "test/fallback")
		answer(t, s, a.Key, true)
		c := oneCase(t, s, a.ActionID)
		expect(t, c.Decision != nil && len(c.Answers) == 1 && c.Answers[0].Review, "case %+v", c)
	}
}

func records(rs ...limen.ReplayRecord) iter.Seq2[limen.ReplayRecord, error] {
	return func(yield func(limen.ReplayRecord, error) bool) {
		for _, r := range rs {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// replay: a replay reports what Limen would have suggested; without keep
// it keeps nothing, with keep it records the history.
func replay(t *testing.T, target Target) {
	at := time.Now().Add(-time.Minute)
	yes, no := true, false
	recs := []limen.ReplayRecord{
		{At: at, Action: action("h1"), Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1", Approve: &yes},
		{At: at.Add(time.Second), Action: action("h2"), Verdict: limen.Allow, ReviewProbability: 0, Source: "rules/1"},
		{At: at.Add(2 * time.Second), Action: action("h3"), Verdict: limen.Flag, ReviewProbability: 0.05, Source: "rules/1", Approve: &no},
	}
	s := target.New(t)
	rep, err := s.Replay(ctx, records(recs...), limen.ReplayOptions{})
	must(t, err)
	total := 0
	for _, n := range rep.Would {
		total += n
	}
	expect(t, rep.Records == 3 && rep.Answered == 2 && total == 3 && !rep.Kept, "report %+v", rep)
	page := cases(t, s, limen.CaseQuery{})
	expect(t, len(page.Entries) == 0, "a replay without keep kept %+v", page)
	rep, err = s.Replay(ctx, records(recs...), limen.ReplayOptions{Keep: true})
	must(t, err)
	expect(t, rep.Kept, "report %+v", rep)
	page = cases(t, s, limen.CaseQuery{})
	expect(t, len(page.Entries) == 3, "a kept replay has %+v", page)
	bad := recs[0]
	bad.Action.Kind = ""
	_, err = s.Replay(ctx, records(bad), limen.ReplayOptions{})
	code(t, err, limen.CodeInvalidReplay, "action.kind")
	boom := errors.New("conformance: the source failed")
	_, err = s.Replay(ctx, func(yield func(limen.ReplayRecord, error) bool) { yield(limen.ReplayRecord{}, boom) }, limen.ReplayOptions{})
	expect(t, errors.Is(err, boom), "a failing source: %v", err)
}

// command is action id running the shell command cmd.
func command(id, cmd string) limen.Action {
	a := action(id)
	a.Input = []byte(`{"command":` + strconv.Quote(cmd) + `}`)
	return a
}

// consistent checks the fields of a verdict every model must agree on: it
// follows the thresholds and the cover rule ([followsTheCap]), and the
// evidence ratio is in [0, 1].
func consistent(t *testing.T, what string, approve, ratio float64, v limen.Verdict, kind string, th limen.Thresholds, c limen.Cover) {
	t.Helper()
	followsTheCap(t, what, approve, v, kind, th, c)
	expect(t, ratio >= 0 && ratio <= 1, "%s: evidence ratio %v outside [0, 1]", what, ratio)
}

// explanations (spec 009): an explanation names its action and its model;
// by the model that made the prediction it repeats the prediction; its
// parts and its evidence add up; an action with no record is no_case.
func explanations(t *testing.T, target Target) {
	s := target.New(t)
	_, err := s.Explain(ctx, limen.Key{Org: org})
	code(t, err, limen.CodeInvalidQuery, "subject")
	_, err = s.Explain(ctx, key("none"))
	code(t, err, limen.CodeNoCase, "action_id")

	a := action("e1")
	p := predict(t, s, a)
	x, err := s.Explain(ctx, a.Key)
	must(t, err)
	require(t, x.Key == a.Key && x.Model.Source != "" && x.Evidence != nil, "explanation %+v", x)
	consistent(t, "explanation", x.Approve, x.EvidenceRatio, x.Verdict, x.AskKind, x.Thresholds, x.Cover.Cover)
	expect(t, x.Thresholds == p.Thresholds, "thresholds %+v, the prediction's %+v", x.Thresholds, p.Thresholds)
	require(t, x.PredictedBy != nil && *x.PredictedBy == x.Model && x.Model.Answers == 0, "predicted by %+v, explained by %+v", x.PredictedBy, x.Model)
	expect(t, math.Abs(x.Approve-p.Approve) < 1e-9 && math.Abs(x.Uncertainty-p.Uncertainty) < 1e-9 && x.Verdict == p.Verdict,
		"by the same model the explanation %+v differs from the prediction %+v", x, p)
	expect(t, x.Verdict != limen.Ask || x.AskKind == limen.AskUnsure, "with no answers an ask is %q, want unsure", x.AskKind)
	expect(t, len(x.Evidence) == 0, "evidence %+v with no answers", x.Evidence)

	decide(t, s, a, limen.Ask, 1, "test/1")
	answer(t, s, a.Key, true)
	b := action("e2")
	predict(t, s, b)
	decide(t, s, b, limen.Ask, 1, "test/1")
	answer(t, s, b.Key, false)
	for _, k := range []limen.Key{a.Key, b.Key} {
		x, err := s.Explain(ctx, k)
		must(t, err)
		consistent(t, "explanation "+k.ActionID, x.Approve, x.EvidenceRatio, x.Verdict, x.AskKind, x.Thresholds, x.Cover.Cover)
		expect(t, x.Model.Answers <= 2, "a model fitted through %d of 2 answers", x.Model.Answers)
		if x.Parts != nil {
			sum := x.Parts.Action + x.Parts.Recent + x.Parts.Interaction
			expect(t, math.Abs(sum-x.Mean) < 1e-9, "parts %+v sum to %v, the mean is %v", *x.Parts, sum, x.Mean)
		}
		total := 0.0
		for _, e := range x.Evidence {
			expect(t, (e.ActionID == "e1" && e.Approve) || (e.ActionID == "e2" && !e.Approve), "evidence %+v is no answer given", e)
			expect(t, e.Label != "" && !strings.Contains(e.Label, "./..."), "evidence label %q", e.Label)
			total += e.Share
		}
		expect(t, total <= 1+1e-9, "evidence shares sum to %v", total)
	}
}

// explanationsOfSteeredVerdicts: an allow and a block have no kind of
// uncertainty, and an ask has one.
func explanationsOfSteeredVerdicts(t *testing.T, target Target) {
	if target.Steered == nil {
		t.Skip("the target learns its predictions; Target.Steered is nil")
	}
	for approve, want := range map[float64]limen.Verdict{0.97: limen.Allow, 0.5: limen.Ask, 0.1: limen.Block} {
		s := target.Steered(t, func(limen.Action) (float64, float64) { return approve, 0.1 })
		meetTheCover(t, s, "es")
		predict(t, s, action("es1"))
		x, err := s.Explain(ctx, key("es1"))
		must(t, err)
		expect(t, x.Verdict == want && x.Approve == approve, "approve %v: %+v, want %q", approve, x, want)
		consistent(t, "steered", x.Approve, x.EvidenceRatio, x.Verdict, x.AskKind, x.Thresholds, x.Cover.Cover)
	}
}

// learnedEntries (spec 009): one entry per label of the person's answered
// actions, most answered first, with the counts of their answers and a
// verdict under the profile that applies; a kind filters them.
func learnedEntries(t *testing.T, target Target) {
	s := target.New(t)
	_, err := s.Learned(ctx, limen.LearnedQuery{Org: org})
	code(t, err, limen.CodeInvalidQuery, "subject")
	l, err := s.Learned(ctx, limen.LearnedQuery{Org: org, Subject: subject})
	must(t, err)
	expect(t, len(l.Entries) == 0 && l.Recent != nil && l.Model.Source != "", "with no answers: %+v", l)

	saved, err := s.PutProfile(ctx, limen.Profile{Org: org, Kind: "tool", Agent: "agt_x", Costs: limen.Costs{FalseAllow: 20, FalseBlock: 5, Ask: 1}, AuditRate: 0.1})
	must(t, err)
	for _, c := range []struct {
		id, cmd string
		approve bool
	}{
		{"l1", "go test ./...", true},
		{"l2", "go test ./engine", false},
		{"l3", "git push --force origin main", false},
		{"l4", "ls -la /secret/path", true},
	} {
		a := command(c.id, c.cmd)
		predict(t, s, a)
		decide(t, s, a, limen.Ask, 1, "test/1")
		answer(t, s, a.Key, c.approve)
	}
	predict(t, s, command("l5", "cat unanswered"))
	l, err = s.Learned(ctx, limen.LearnedQuery{Org: org, Subject: subject, Agent: "agt_x"})
	must(t, err)
	got := map[string][2]int{}
	var labels []string
	for _, e := range l.Entries {
		got[e.Label] = [2]int{e.Approved, e.Denied}
		labels = append(labels, e.Label)
		consistent(t, "entry "+e.Label, e.Approve, e.EvidenceRatio, e.Verdict, e.AskKind, e.Thresholds, e.Cover)
		want, _ := saved.Costs.Thresholds()
		expect(t, e.Kind == "tool" && e.Name == "bash" && e.Thresholds == want, "entry %+v, want the thresholds %+v of the agent's profile", e, want)
		expect(t, !strings.Contains(e.Label, "secret") && !strings.Contains(e.Label, "origin"), "label %q holds input", e.Label)
	}
	force := "bash: git push --force, protected target, cannot undo"
	want := map[string][2]int{"bash: go test": {1, 1}, force: {0, 1}, "bash: ls": {1, 0}}
	expect(t, len(got) == len(want) && got["bash: go test"] == want["bash: go test"] && got[force] == want[force] && got["bash: ls"] == want["bash: ls"],
		"entries %v, want %v", got, want)
	expect(t, len(labels) > 0 && labels[0] == "bash: go test", "entries %v, want the most answered first", labels)
	expect(t, l.Model.Answers <= 4 && len(l.Recent) <= 20, "model %+v, %d trend points", l.Model, len(l.Recent))

	l, err = s.Learned(ctx, limen.LearnedQuery{Org: org, Subject: subject, Kind: "message"})
	must(t, err)
	expect(t, len(l.Entries) == 0, "kind message: %+v", l.Entries)
}

// previews (spec 009): a preview re-thresholds the recorded predictions in
// its window under the costs it names, holds an allow to the cover each
// prediction recorded (spec 011), and counts the errors among the answered
// ones.
func previews(t *testing.T, target Target) {
	approvals := map[string]float64{"pv1": 0.97, "pv2": 0.5, "pv3": 0.1, "pv4": 0.97, "pv5": 0.1}
	newService := func() limen.Service {
		if target.Steered == nil {
			return target.New(t)
		}
		return target.Steered(t, func(a limen.Action) (float64, float64) { return approvals[a.ActionID], 0.1 })
	}
	s := newService()
	if target.Steered != nil {
		meetTheCover(t, s, "pv")
	}
	costs := limen.Costs{FalseAllow: 20, FalseBlock: 5, Ask: 1}
	th, err := costs.Thresholds()
	must(t, err)
	want := limen.Preview{Would: map[limen.Verdict]int{limen.Allow: 0, limen.Ask: 0, limen.Block: 0}}
	for _, id := range []string{"pv1", "pv2", "pv3", "pv4", "pv5"} {
		a := action(id)
		p := predict(t, s, a)
		// A preview holds an allow to the cover the prediction recorded.
		v := th.Verdict(p.Approve)
		if v == limen.Allow && !p.Cover.Covered {
			v = limen.Ask
		}
		want.Records++
		want.Would[v]++
		if id == "pv4" || id == "pv5" {
			decide(t, s, a, limen.Ask, 1, "test/1")
			answer(t, s, a.Key, id == "pv5")
			want.Answered++
			if v == limen.Allow && id == "pv4" {
				want.AllowedDenied++
			}
			if v == limen.Block && id == "pv5" {
				want.BlockedApproved++
			}
		}
	}
	from, to := window()
	got, err := s.Preview(ctx, limen.PreviewRequest{Org: org, Subject: subject, From: from, To: to, Costs: costs, AuditRate: 0.5})
	must(t, err)
	expect(t, got.Thresholds == th && got.Records == want.Records && got.Answered == want.Answered &&
		got.AllowedDenied == want.AllowedDenied && got.BlockedApproved == want.BlockedApproved,
		"preview %+v, want %+v", got, want)
	for _, v := range []limen.Verdict{limen.Allow, limen.Ask, limen.Block} {
		expect(t, got.Would[v] == want.Would[v], "would %s %d, want %d", v, got.Would[v], want.Would[v])
	}
	asks := float64(want.Would[limen.Ask]) + 0.5*float64(want.Would[limen.Allow]+want.Would[limen.Block])
	expect(t, math.Abs(got.ExpectedAsks-asks) < 1e-9, "expected asks %v, want %v", got.ExpectedAsks, asks)
	if target.Steered != nil {
		expect(t, got.AllowedDenied == 1 && got.BlockedApproved == 1, "steered preview %+v, want one allowed denial and one blocked approval", got)
	}

	past, err := s.Preview(ctx, limen.PreviewRequest{Org: org, Subject: subject, From: from.Add(-48 * time.Hour), To: from, Costs: costs})
	must(t, err)
	expect(t, past.Records == 0 && past.Would[limen.Ask] == 0, "a window with no records: %+v", past)
}

// previewErrors: a preview validates its window and its costs as a profile
// does, ask_too_costly included.
func previewErrors(t *testing.T, target Target) {
	s := target.New(t)
	from, to := window()
	_, err := s.Preview(ctx, limen.PreviewRequest{Org: org, Subject: subject, From: to, To: from, Costs: limen.DefaultCosts})
	code(t, err, limen.CodeInvalidQuery, "to")
	_, err = s.Preview(ctx, limen.PreviewRequest{Org: org, From: from, To: to, Costs: limen.DefaultCosts})
	code(t, err, limen.CodeInvalidQuery, "subject")
	_, err = s.Preview(ctx, limen.PreviewRequest{Org: org, Subject: subject, From: from, To: to, Costs: limen.Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1}})
	code(t, err, limen.CodeAskTooCostly, "")
}
