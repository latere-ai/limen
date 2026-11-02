// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
)

var careful = limen.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}

// The claims hosted Topos sends with a publish (Topos spec 101): a preview
// or an update can be undone from a copy and stays private; access for
// anyone with the link cannot be undone and is public.
var (
	updateClaim = limen.ReachClaim{Undo: limen.UndoRecoverable, Contact: limen.ContactRepeat,
		Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundNo}}
	publicClaim = limen.ReachClaim{Undo: limen.UndoIrreversible, Contact: limen.ContactRepeat,
		Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundYes}}
)

// publish is a call of the publish tool, as hosted Topos sends it: an
// external effect from a person's machine, in the session ses, that
// updates the person's own app.
func publish(id, ses string) limen.Action {
	c := updateClaim
	return limen.Action{Org: "o", Subject: "s", ActionID: ses + "/" + id, Actor: &limen.Actor{Kind: "agent", ID: "agt", Session: ses},
		Kind: "tool", Name: "publish", Effect: limen.EffectExternal, Environment: &limen.Environment{Kind: "host", Credentialed: true},
		Target: &limen.Target{Kind: "app", Class: limen.ClassOwn}, Scope: &c}
}

// makePublic is publish with the claims of access for anyone.
func makePublic(id, ses string) limen.Action {
	a := publish(id, ses)
	c := publicClaim
	a.Scope = &c
	return a
}

// coverEngine is an inline engine under the costs, with options o.
func coverEngine(t *testing.T, costs limen.Costs, o engine.Options) *engine.Engine {
	t.Helper()
	o.Inline = 1
	e := engine.New(o)
	t.Cleanup(func() { e.Close() })
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Costs: costs, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	return e
}

// cover is the cover of weight in sessions under the contract's settings.
func cover(weight float64, sessions int) limen.Cover {
	return limen.Cover{Covered: weight >= limen.MinWeight && sessions >= limen.MinSessions, Weight: weight, Sessions: sessions,
		MinWeight: limen.MinWeight, MinSessions: limen.MinSessions}
}

func predicted(t *testing.T, e *engine.Engine, a limen.Action) limen.Prediction {
	t.Helper()
	p, err := e.Predict(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The measurement of spec 011's Overview (criterion 10): one approval of a
// publish gives 0.729 with an evidence ratio of 0.54. Under Balanced, whose
// allow threshold is 0.65, the next publish of the same reach is a few ask,
// and a publish that makes the app public is a wider ask. The cap changes
// no number of the prediction. Under Careful it is a short ask.
func TestOneApprovalIsNoPattern(t *testing.T) {
	e := coverEngine(t, balanced, engine.Options{})
	answer(t, e, publish("a", "ses-1"), true)
	p := predicted(t, e, publish("b", "ses-1"))
	if p.Verdict != limen.Ask || p.AskKind != limen.AskFew || p.Cover != cover(1, 1) ||
		fmt.Sprintf("%.3f %.2f", p.Approve, p.EvidenceRatio) != "0.729 0.54" {
		t.Fatalf("one approval: %+v, want a few ask at 0.729 and 0.54", p)
	}
	want := "This person allowed actions of this reach. Limen needs 3 considered approvals from 2 sessions before it suggests allow. Limen suggests ask."
	if p.Reason != want {
		t.Errorf("reason %q\nwant   %q", p.Reason, want)
	}
	pub := predicted(t, e, makePublic("c", "ses-1"))
	if pub.Verdict != limen.Ask || pub.AskKind != limen.AskWider || pub.Cover != cover(0, 0) ||
		fmt.Sprintf("%.2f", pub.Approve) != "0.73" {
		t.Fatalf("making the app public: %+v, want a wider ask at 0.73", pub)
	}
	if pub.Reason != `This person never allowed an action of this reach: "publish: make public". Limen suggests ask.` {
		t.Errorf("reason %q", pub.Reason)
	}
	if p.Scope.Undo != limen.UndoRecoverable || pub.Scope.Crosses.Public != limen.BoundYes || p.Scope.TargetKind != "app" || p.Scope.TargetClass != limen.ClassOwn {
		t.Errorf("scopes %+v and %+v", p.Scope, pub.Scope)
	}
	x, err := e.Explain(ctx, pub.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.AskKind != limen.AskWider || len(x.Cover.Nearest) != 1 || x.Cover.Nearest[0].Label != "publish: update" || x.Cover.Nearest[0].ActionID != "ses-1/a" {
		t.Fatalf("the explanation of a wider ask: %+v", x.Cover)
	}

	c := coverEngine(t, careful, engine.Options{})
	answer(t, c, publish("a", "ses-1"), true)
	if p := predicted(t, c, publish("b", "ses-1")); p.Verdict != limen.Ask || p.AskKind != limen.AskShort || p.Cover != cover(1, 1) {
		t.Fatalf("one approval under Careful: %+v", p)
	}
}

// Approvals from one session do not cover an action; approvals in two
// sessions do. A denial never counts, and an approval that a later denial
// replaced no longer counts.
func TestCoverNeedsTwoSessions(t *testing.T) {
	e := coverEngine(t, balanced, engine.Options{})
	probes := 0
	expect := func(want limen.Cover) limen.Prediction {
		t.Helper()
		probes++
		p := predicted(t, e, publish(fmt.Sprintf("probe-%d", probes), "ses-1"))
		v := limen.Ask
		if want.Covered {
			v = limen.Allow
		}
		if p.Verdict != v || p.Cover != want || p.Approve <= 0.65 {
			t.Fatalf("probe %d: %+v, want %s with %+v", probes, p, v, want)
		}
		return p
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		answer(t, e, publish(id, "ses-1"), true)
	}
	expect(cover(3, 1))
	answer(t, e, publish("d1", "ses-2"), false)
	expect(cover(3, 1))
	answer(t, e, publish("a4", "ses-2"), true)
	p := expect(cover(4, 2))
	if p.AskKind != "" || !strings.HasSuffix(p.Reason, "Limen suggests allow.") {
		t.Errorf("a covered allow: %+v", p)
	}
	if err := e.Answer(ctx, limen.Answer{Key: publish("a4", "ses-2").Key, Approve: false}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a5", "a6", "a7"} {
		answer(t, e, publish(id, "ses-1"), true)
	}
	expect(cover(6, 1))
}

// A case without a session counts as the UTC day it was received: one
// session for a day, a second for the next.
func TestNoSessionIsItsDay(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)}
	e := coverEngine(t, balanced, engine.Options{Clock: c.Now})
	alone := func(id string) limen.Action {
		a := publish(id, "x")
		a.Actor = nil
		return a
	}
	for i := range 3 {
		answer(t, e, alone(fmt.Sprintf("a%d", i)), true)
	}
	if p := predicted(t, e, alone("probe-1")); p.Verdict != limen.Ask || p.Cover != cover(3, 1) {
		t.Fatalf("three approvals on one day: %+v", p)
	}
	c.mu.Lock()
	c.now = c.now.Add(24 * time.Hour)
	c.mu.Unlock()
	answer(t, e, alone("next-day"), true)
	if p := predicted(t, e, alone("probe-2")); p.Verdict != limen.Allow || p.Cover != cover(4, 2) {
		t.Fatalf("approvals on two days: %+v", p)
	}
}

// The cap holds allows only: a block stays a block, with its cover.
func TestCapNeverTouchesABlock(t *testing.T) {
	e := coverEngine(t, balanced, engine.Options{})
	for i := range 3 {
		answer(t, e, publish(fmt.Sprintf("d%d", i), "ses-1"), false)
	}
	p := predicted(t, e, publish("probe", "ses-1"))
	if p.Verdict != limen.Block || p.AskKind != "" || p.Cover != cover(0, 0) {
		t.Fatalf("three denials: %+v", p)
	}
	x, err := e.Explain(ctx, p.Key)
	if err != nil || x.Verdict != limen.Block || !strings.HasSuffix(x.Reason, "Limen suggests block.") || x.Cover.Cover != cover(0, 0) {
		t.Fatalf("the explanation of a block: %+v, %v", x, err)
	}
}

// Approvals of another family count nothing toward this one.
func TestCoverIsPerFamily(t *testing.T) {
	e := coverEngine(t, balanced, engine.Options{})
	for i := range 4 {
		other := publish(fmt.Sprintf("o%d", i), fmt.Sprintf("ses-%d", i))
		other.Name = "deploy"
		answer(t, e, other, true)
	}
	answer(t, e, publish("a", "ses-1"), true)
	if p := predicted(t, e, publish("probe", "ses-2")); p.Cover != cover(1, 1) {
		t.Fatalf("one approval of publish and four of deploy: %+v", p)
	}
}

// An explanation and a learned entry are held by the approvals now; an
// explanation of a prediction made before the cover was met is an allow
// once it is.
func TestExplanationsAndEntriesAreHeld(t *testing.T) {
	e := coverEngine(t, balanced, engine.Options{})
	answer(t, e, publish("a1", "ses-1"), true)
	probe := publish("probe", "ses-1")
	predicted(t, e, probe)
	x, err := e.Explain(ctx, probe.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.Verdict != limen.Ask || x.AskKind != limen.AskFew || x.Cover.Cover != cover(1, 1) || len(x.Cover.Nearest) != 0 || len(x.Evidence) != 1 {
		t.Fatalf("the explanation of one approval: %+v", x)
	}
	l, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Verdict != limen.Ask || l.Entries[0].Cover != cover(1, 1) || l.Entries[0].Label != "publish: update" {
		t.Fatalf("the learned entry of one approval: %+v", l.Entries)
	}
	answer(t, e, publish("a2", "ses-2"), true)
	answer(t, e, publish("a3", "ses-2"), true)
	if x, err = e.Explain(ctx, probe.Key); err != nil || x.Verdict != limen.Allow || x.Cover.Cover != cover(3, 2) || !strings.HasSuffix(x.Reason, "Limen suggests allow.") {
		t.Fatalf("the explanation once the cover is met: %+v, %v", x, err)
	}
	l, err = e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s", Kind: "tool"})
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Verdict != limen.Allow || l.Entries[0].Cover != cover(3, 2) {
		t.Fatalf("the learned entry once the cover is met: %+v, %v", l.Entries, err)
	}
}

// A replay holds its would-be allows to the cover rule, as a live
// prediction does. A record answered with no answered_at has its answer at
// the time of the decision, so its approval is too quick and builds no
// cover; with answered_at, the same history over two sessions allows.
func TestReplayIsHeld(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	history := func(sessions int, answered time.Duration) []limen.ReplayRecord {
		var rs []limen.ReplayRecord
		for i := range 12 {
			at := start.Add(time.Duration(i) * time.Minute)
			r := limen.ReplayRecord{At: at, Action: publish(fmt.Sprintf("r-%d", i), fmt.Sprintf("ses-%d", i%sessions)),
				Verdict: limen.Ask, ReviewProbability: 1, Source: "manual", Approve: new(true)}
			if answered > 0 {
				r.AnsweredAt = at.Add(answered)
			}
			rs = append(rs, r)
		}
		return rs
	}
	for _, c := range []struct {
		sessions int
		answered time.Duration
		o        engine.Options
		allows   bool
	}{
		{1, 5 * time.Second, engine.Options{}, false},
		{2, 5 * time.Second, engine.Options{}, true},
		{2, 0, engine.Options{}, false},
		{2, time.Second, engine.Options{}, false},
		{2, 0, engine.Options{AnswerFloor: -1}, true},
		{1, 0, engine.Options{MinWeight: -1, MinSessions: -1}, true},
	} {
		e := coverEngine(t, balanced, c.o)
		rep, err := e.Replay(ctx, recs(history(c.sessions, c.answered)), limen.ReplayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := rep.Would[limen.Allow] > 0; got != c.allows {
			t.Errorf("%d sessions, answered after %v, options %+v: the replay would allow %d", c.sessions, c.answered, c.o, rep.Would[limen.Allow])
		}
	}
	// A kept replay keeps how the person answered, and when.
	e := coverEngine(t, balanced, engine.Options{})
	rs := history(2, 5*time.Second)
	rs[0].HowAnswered = limen.HowAnswered{Surface: limen.SurfaceQueue}
	if _, err := e.Replay(ctx, recs(rs), limen.ReplayOptions{Keep: true}); err != nil {
		t.Fatal(err)
	}
	page, err := e.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s", ActionID: rs[0].Action.ActionID})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Answers[0].Surface != limen.SurfaceQueue || !page.Entries[0].Answers[0].At.Equal(rs[0].AnsweredAt) {
		t.Fatalf("a kept record %+v, %v", page, err)
	}
}

// The options set the settings: a negative value turns one off, and a cost
// profile can only make them greater.
func TestCoverOptions(t *testing.T) {
	for _, c := range []struct {
		o       engine.Options
		consent limen.Consent
		want    limen.Cover
	}{
		{engine.Options{}, limen.Consent{}, cover(1, 1)},
		{engine.Options{MinWeight: -1, MinSessions: -1}, limen.Consent{}, limen.Cover{Covered: true, Weight: 1, Sessions: 1}},
		{engine.Options{MinWeight: 1, MinSessions: 1}, limen.Consent{}, limen.Cover{Covered: true, Weight: 1, Sessions: 1, MinWeight: 1, MinSessions: 1}},
		{engine.Options{MinWeight: -1}, limen.Consent{}, limen.Cover{Weight: 1, Sessions: 1, MinSessions: limen.MinSessions}},
		{engine.Options{MinSessions: -1}, limen.Consent{}, limen.Cover{Weight: 1, Sessions: 1, MinWeight: limen.MinWeight}},
		{engine.Options{MinWeight: 5, MinSessions: 3}, limen.Consent{}, limen.Cover{Weight: 1, Sessions: 1, MinWeight: 5, MinSessions: 3}},
		{engine.Options{MinWeight: -1, MinSessions: -1}, limen.Consent{MinWeight: 2}, limen.Cover{Weight: 1, Sessions: 1, MinWeight: 2}},
		{engine.Options{}, limen.Consent{MinWeight: 1, MinSessions: 1}, cover(1, 1)},
	} {
		e := coverEngine(t, balanced, c.o)
		if c.consent != (limen.Consent{}) {
			if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Costs: balanced, AuditRate: 0.05, Consent: c.consent}); err != nil {
				t.Fatal(err)
			}
		}
		answer(t, e, publish("a", "ses-1"), true)
		p := predicted(t, e, publish("probe", "ses-1"))
		if p.Cover != c.want || (p.Verdict == limen.Allow) != c.want.Covered {
			t.Errorf("options %+v, consent %+v: %+v, want %+v", c.o, c.consent, p, c.want)
		}
	}
	// The settings of weights/1: a quick approval counts with the floor
	// off, and an approval late in a session with the fatigue rule off.
	for _, c := range []struct {
		o    engine.Options
		want float64
	}{{engine.Options{}, 0}, {engine.Options{AnswerFloor: -1}, 1}, {engine.Options{AnswerFloor: 500 * time.Millisecond}, 1}} {
		// The clock moves a second at each reading, so an answer comes a
		// second after its decision.
		c.o.Clock = (&clock{now: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}).Now
		e := coverEngine(t, balanced, c.o)
		answerHow(t, e, publish("a", "ses-1"), true, limen.HowAnswered{})
		if p := predicted(t, e, publish("probe", "ses-1")); p.Cover.Weight != c.want {
			t.Errorf("a quick approval, options %+v: weight %v, want %v", c.o, p.Cover.Weight, c.want)
		}
	}
	for _, c := range []struct {
		o    engine.Options
		want float64
	}{{engine.Options{AnswerFloor: -1, AnswerFatigue: 2}, 2}, {engine.Options{AnswerFloor: -1, AnswerFatigue: -1}, 3}} {
		e := coverEngine(t, balanced, c.o)
		for i := range 3 {
			answerHow(t, e, publish(fmt.Sprintf("a%d", i), "ses-1"), true, limen.HowAnswered{})
		}
		if p := predicted(t, e, publish("probe", "ses-1")); p.Cover.Weight != c.want {
			t.Errorf("three approvals in a session, options %+v: weight %v, want %v", c.o, p.Cover.Weight, c.want)
		}
	}
}

// A prediction reads the cases from the ledger its model keeps, so a
// loaded model predicts while the store cannot list the cases. Fail
// closed: a model that cannot be loaded because the cases cannot be read
// makes a prediction fail, which the decision point's FailClosed turns
// into an ask, and so it does an explanation and a decision that brings a
// new action.
func TestCoverFailsClosed(t *testing.T) {
	f := newFaulty("", 0)
	e := coverEngine(t, balanced, engine.Options{Store: f})
	for i := range 3 {
		answer(t, e, publish(fmt.Sprintf("a%d", i), fmt.Sprintf("ses-%d", i)), true)
	}
	if p := predicted(t, e, publish("fit", "ses-1")); p.Verdict != limen.Allow {
		t.Fatalf("three approvals in three sessions: %+v", p)
	}
	f.mu.Lock()
	f.fail, f.after = "Cases", f.calls["Cases"]
	f.mu.Unlock()
	if p, err := e.Predict(ctx, publish("warm", "ses-1")); err != nil || p.Verdict != limen.Allow || p.Cover != cover(3, 3) {
		t.Errorf("a loaded model with no list of the cases: %+v, %v", p, err)
	}
	cold := coverEngine(t, balanced, engine.Options{Store: f})
	if _, err := cold.Predict(ctx, publish("probe", "ses-1")); err == nil {
		t.Error("a prediction whose cases cannot be read did not fail")
	}
	p := limen.FailClosed(cold, time.Second, nil)
	if fc, err := p.Predict(ctx, publish("probe-2", "ses-1")); err != nil || fc.Verdict != limen.Ask || fc.Cover != (limen.Cover{}) || fc.Scope != (limen.Reach{}) {
		t.Errorf("FailClosed: %+v, %v", fc, err)
	}
	if _, err := cold.Explain(ctx, publish("fit", "ses-1").Key); err == nil {
		t.Error("an explanation whose cases cannot be read did not fail")
	}
	if err := cold.Record(ctx, limen.Decision{Key: publish("new", "ses-1").Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual", Action: new(publish("new", "ses-1"))}); err == nil {
		t.Error("a decision whose cases cannot be read did not fail")
	}
}

// The table of docs/theory.md, "The cover rule": three considered
// approvals of a publish give 0.85 with an evidence ratio of 0.69. Under
// Balanced, they cover a publish of their reach in two sessions and not in
// one. Under Careful, 0.85 is in the ask band, and the agreeing answers
// make a short ask.
func TestTheoryTable(t *testing.T) {
	for _, c := range []struct {
		costs    limen.Costs
		sessions []string
		want     limen.Verdict
		kind     string
	}{
		{balanced, []string{"ses-1", "ses-1", "ses-1"}, limen.Ask, limen.AskFew},
		{balanced, []string{"ses-1", "ses-1", "ses-2"}, limen.Allow, ""},
		{careful, []string{"ses-1", "ses-1", "ses-1"}, limen.Ask, limen.AskShort},
		{careful, []string{"ses-1", "ses-1", "ses-2"}, limen.Ask, limen.AskShort},
	} {
		e := coverEngine(t, c.costs, engine.Options{})
		for i, ses := range c.sessions {
			answer(t, e, publish(fmt.Sprintf("a%d", i), ses), true)
		}
		p := predicted(t, e, publish("probe", "ses-1"))
		if p.Verdict != c.want || p.AskKind != c.kind || fmt.Sprintf("%.2f %.2f", p.Approve, p.EvidenceRatio) != "0.85 0.69" {
			t.Errorf("%+v in %v: %+v, want %s %q at 0.85 and 0.69", c.costs, c.sessions, p, c.want, c.kind)
		}
		if pub := predicted(t, e, makePublic("public", "ses-1")); c.costs == balanced && pub.AskKind != limen.AskWider {
			t.Errorf("making the app public after %v: %+v", c.sessions, pub)
		}
	}
}

// A split ask needs an approval and a denial in the family; agreeing
// answers, or a denial of another family, give a short one (criterion 22).
func TestSplitNeedsADenial(t *testing.T) {
	e := coverEngine(t, careful, engine.Options{})
	answer(t, e, publish("a1", "ses-1"), true)
	answer(t, e, publish("a2", "ses-2"), true)
	other := publish("o1", "ses-1")
	other.Name = "deploy"
	answer(t, e, other, false)
	if p := predicted(t, e, publish("probe-1", "ses-1")); p.Verdict != limen.Ask || p.AskKind != limen.AskShort {
		t.Fatalf("two approvals and a denial of another family: %+v", p)
	}
	answer(t, e, publish("d1", "ses-2"), false)
	p := predicted(t, e, publish("probe-2", "ses-1"))
	if p.Verdict != limen.Ask || p.AskKind != limen.AskSplit || p.EvidenceRatio < engine.EvidenceCut {
		t.Fatalf("an approval and a denial of the family: %+v", p)
	}
	if p.Reason != "The answers of this person about actions like this do not agree. Limen suggests ask." {
		t.Errorf("reason %q", p.Reason)
	}
}

// No stored label, scope or family holds a string of the input or of
// target.id (criterion 6, and criterion 2 for the name of a ref).
func TestStoredScopeHoldsNoInput(t *testing.T) {
	st := store.NewMemory()
	e := engine.New(engine.Options{Inline: 1, Store: st})
	t.Cleanup(func() { e.Close() })
	markers := []string{"zebra-ref", "quokka", "narwhal.example", "ibis-target", "okapi"}
	for i, cmd := range []string{
		"git push --force origin zebra-ref:main",
		"curl -d @quokka https://narwhal.example/x",
		"rm -rf ~/okapi && ./okapi.sh",
		"git push origin release/zebra-ref",
	} {
		a := bash("s", fmt.Sprintf("m%d", i), cmd)
		a.Target = &limen.Target{Kind: "ref", ID: "ibis-target"}
		answer(t, e, a, true)
	}
	a := publish("p", "ses-1")
	a.Target.ID = "ibis-target"
	answer(t, e, a, true)
	cases, err := st.Cases(ctx, "o", "s")
	if err != nil || len(cases) != 5 {
		t.Fatalf("%d cases, %v", len(cases), err)
	}
	for _, c := range cases {
		kept, _ := json.Marshal(struct {
			Label    string
			Scope    *limen.Reach
			Families []string
			P        limen.Reach
		}{c.Label, c.Scope, c.Families, c.Prediction.Scope})
		for _, m := range markers {
			if strings.Contains(string(kept), m) {
				t.Errorf("case %s keeps %q: %s", c.Action.ActionID, m, kept)
			}
		}
		if c.Scope == nil || c.Labels != "labels/2" || len(c.Families) == 0 {
			t.Errorf("case %s: scope %v, labels %q, families %v", c.Action.ActionID, c.Scope, c.Labels, c.Families)
		}
	}
	// The publish of an app the person approved before is a repeat.
	if p := predicted(t, e, func() limen.Action {
		b := publish("q", "ses-1")
		b.Target.ID = "ibis-target"
		b.Scope.Contact = ""
		return b
	}()); p.Scope.Contact != limen.ContactRepeat {
		t.Errorf("a second publish of the app: %+v", p.Scope)
	}
	if p := predicted(t, e, func() limen.Action {
		b := publish("r", "ses-1")
		b.Target.ID = "app_new"
		b.Scope.Contact = ""
		return b
	}()); p.Scope.Contact != limen.ContactFirst {
		t.Errorf("the first publish of another app: %+v", p.Scope)
	}
}

// An old case gets the scope of the migration table, and an old approval
// of unknown reach never covers a new action of known greatest reach
// (criterion 20).
func TestOldCasesMigrate(t *testing.T) {
	st := store.NewMemory()
	e := coverEngine(t, balanced, engine.Options{Store: st})
	for i, ses := range []string{"ses-1", "ses-1", "ses-2"} {
		answer(t, e, publish(fmt.Sprintf("a%d", i), ses), true)
		answer(t, e, bash("s", fmt.Sprintf("g%d", i), "go test ./..."), true)
	}
	// Make each case one the release before wrote: no scope, no families,
	// and labels of labels/1.
	cases, err := st.Cases(ctx, "o", "s")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		c.Scope, c.Families, c.Labels = nil, nil, "labels/1"
		c.Label = map[bool]string{true: "publish", false: "bash: go test"}[c.Action.Name == "publish"]
		c.Action.Scope, c.Action.Target = nil, nil
		if c.Action.Name == "bash" {
			c.Action.Actor.Session = "old-" + c.Action.ActionID
		}
		if err := st.Put(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	// The release after reads the cases that the release before wrote.
	e = coverEngine(t, balanced, engine.Options{Store: st})
	// The old publish approvals have an unknown undo, so they cover no new
	// publish of a known undo.
	if p := predicted(t, e, publish("new", "ses-1")); p.Verdict != limen.Ask || p.AskKind != limen.AskWider {
		t.Errorf("a new publish over old approvals: %+v", p)
	}
	// The old test runs were reversible and stayed on the machine, so they
	// cover a new one.
	if p := predicted(t, e, bash("s", "new-test", "go test ./x")); p.Verdict != limen.Allow || p.Cover.Weight != 3 {
		t.Errorf("a new test run over old approvals: %+v", p)
	}
	// An old approval of unknown reach never covers a force push to the
	// default branch.
	old := bash("s", "old-unknown", "x")
	answer(t, e, old, true)
	c, _, err := st.Case(ctx, old.Key)
	if err != nil {
		t.Fatal(err)
	}
	c.Scope, c.Families, c.Labels, c.Label = nil, nil, "labels/1", "bash: other command"
	if err := st.Put(ctx, c); err != nil {
		t.Fatal(err)
	}
	e = coverEngine(t, balanced, engine.Options{Store: st})
	if p := predicted(t, e, bash("s", "force", "git push --force origin main && ./x.sh")); p.Verdict == limen.Allow || p.AskKind != limen.AskWider {
		t.Errorf("a force push over an old approval of unknown reach: %+v", p)
	}
	// The learned entries keep the old labels beside the new.
	l, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, x := range l.Entries {
		labels[x.Label] = true
	}
	if !labels["publish"] || !labels["bash: go test"] {
		t.Errorf("entries %v", labels)
	}
}

// A preview applies the cover rule as each prediction recorded it.
func TestPreviewHoldsTheRecordedCover(t *testing.T) {
	e := coverEngine(t, balanced, engine.Options{})
	answer(t, e, publish("a1", "ses-1"), true)
	predicted(t, e, publish("probe", "ses-1"))
	r := limen.PreviewRequest{Org: "o", Subject: "s", From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Now().Add(time.Hour),
		Costs: balanced, AuditRate: 0.05}
	p, err := e.Preview(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// The first prediction knew no answer, so it was an ask; the second
	// was an allow that the cover held.
	if p.Records != 2 || p.Would[limen.Allow] != 0 || p.Would[limen.Ask] != 2 {
		t.Errorf("preview %+v", p)
	}
}

// The certificate counts the considered answers of its clean audits, and
// no estimate reads the weights.
func TestReliabilityCountsConsidered(t *testing.T) {
	e := coverEngine(t, balanced, engine.Options{})
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Costs: balanced, AuditRate: 0.05, Certify: limen.Certify{Alpha: 0.5, Delta: 0.5}}); err != nil {
		t.Fatal(err)
	}
	for i, how := range []limen.HowAnswered{considered, {}, considered} {
		a := publish(fmt.Sprintf("au%d", i), "ses-1")
		predicted(t, e, a)
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Flag, ReviewProbability: 0.05, Source: "manual"}); err != nil {
			t.Fatal(err)
		}
		if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: true, HowAnswered: how}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := e.Reliability(ctx, limen.ReliabilityQuery{Org: "o", Subject: "s", Of: limen.OfApplied, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Certificate.Clean != 3 || r.Certificate.Considered != 2 || r.FalseAllow.Reviews != 3 {
		t.Errorf("reliability %+v", r)
	}
}
