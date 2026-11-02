// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
)

var careful = limen.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}

// publish is a call of a tool that publishes, as hosted Topos sends it:
// an external effect from a person's machine, in the session ses.
func publish(id, ses string) limen.Action {
	return limen.Action{Org: "o", Subject: "s", ActionID: ses + "/" + id, Actor: &limen.Actor{Kind: "agent", ID: "agt", Session: ses},
		Kind: "tool", Name: "publish", Effect: limen.EffectExternal, Environment: &limen.Environment{Kind: "host", Credentialed: true}}
}

// minimumEngine is an inline engine under the costs, with options o.
func minimumEngine(t *testing.T, costs limen.Costs, o engine.Options) *engine.Engine {
	t.Helper()
	o.Inline = 1
	e := engine.New(o)
	t.Cleanup(func() { e.Close() })
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Costs: costs, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	return e
}

// held is the minimum of allows approvals in sessions sessions, under the
// contract's minimums.
func held(allows, sessions int) limen.Minimum {
	return limen.Minimum{Allows: allows, Sessions: sessions, MinAllows: limen.MinAllows, MinSessions: limen.MinSessions}
}

// The owner's case: one approval of a publish gives 0.73 with an evidence
// ratio of 0.54. Under Balanced, whose allow threshold is 0.65, the next
// publish ran on its own after that one answer. The answer minimum holds it
// as a split ask, which a decision point shows the person, with a reason
// that names the counts. Under Careful it was an ask and stays one, with no
// minimum.
func TestOneApprovalIsNoPattern(t *testing.T) {
	for _, c := range []struct {
		name  string
		costs limen.Costs
		want  limen.Minimum
	}{{"balanced", balanced, held(1, 1)}, {"careful", careful, limen.Minimum{}}} {
		t.Run(c.name, func(t *testing.T) {
			e := minimumEngine(t, c.costs, engine.Options{})
			answer(t, e, publish("a", "ses-1"), true)
			p, err := e.Predict(ctx, publish("b", "ses-1"))
			if err != nil {
				t.Fatal(err)
			}
			if p.Verdict != limen.Ask || p.AskKind != limen.AskSplit || p.Minimum != c.want ||
				fmt.Sprintf("%.3f %.2f", p.Approve, p.EvidenceRatio) != "0.729 0.54" {
				t.Fatalf("one approval: %+v, want a split ask held by %+v", p, c.want)
			}
			if c.want.Held() {
				want := "The approval probability is 0.73, above the allow threshold (0.65). The person approved actions with this label 1 time, in 1 session. " +
					"Limen needs 3 approvals or more, in 2 sessions or more, before it suggests allow. Limen suggests ask."
				if p.Reason != want {
					t.Errorf("reason %q\nwant   %q", p.Reason, want)
				}
			}
		})
	}
}

// Approvals from one session are not a pattern either; approvals in two
// sessions are. A denial never counts, and an approval that a later denial
// replaced no longer counts.
func TestAllowsNeedTwoSessions(t *testing.T) {
	e := minimumEngine(t, balanced, engine.Options{})
	probes := 0
	expect := func(want limen.Minimum) limen.Prediction {
		t.Helper()
		probes++
		p, err := e.Predict(ctx, publish(fmt.Sprintf("probe-%d", probes), "ses-1"))
		if err != nil {
			t.Fatal(err)
		}
		v := limen.Ask
		if !want.Held() {
			v = limen.Allow
		}
		if p.Verdict != v || p.Minimum != want || p.Approve <= 0.65 {
			t.Fatalf("probe %d: %+v, want %s held by %+v", probes, p, v, want)
		}
		return p
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		answer(t, e, publish(id, "ses-1"), true)
	}
	expect(held(3, 1))
	answer(t, e, publish("d1", "ses-2"), false)
	expect(held(3, 1))
	answer(t, e, publish("a4", "ses-2"), true)
	p := expect(limen.Minimum{})
	if p.AskKind != "" || !strings.HasSuffix(p.Reason, "Limen suggests allow.") {
		t.Errorf("an allow past the minimum: %+v", p)
	}
	// The person changes their mind about the one approval in ses-2, and
	// approves more in ses-1, which lifts the approval probability again
	// but not the count of sessions.
	if err := e.Answer(ctx, limen.Answer{Key: publish("a4", "ses-2").Key, Approve: false}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a5", "a6", "a7"} {
		answer(t, e, publish(id, "ses-1"), true)
	}
	expect(held(6, 1))
}

// Each action without a session counts as a session of its own, so three
// approvals of actions that carry no session meet the minimum.
func TestNoSessionIsItsOwn(t *testing.T) {
	e := minimumEngine(t, balanced, engine.Options{})
	for i := range 3 {
		a := publish(fmt.Sprintf("a%d", i), "x")
		a.Actor = nil
		answer(t, e, a, true)
	}
	p, err := e.Predict(ctx, publish("probe", "ses-1"))
	if err != nil || p.Verdict != limen.Allow || p.Minimum.Held() {
		t.Fatalf("three approvals with no session: %+v, %v", p, err)
	}
}

// The minimum holds allows only: a block stays a block, and an ask an ask.
func TestMinimumNeverTouchesABlock(t *testing.T) {
	e := minimumEngine(t, balanced, engine.Options{})
	for i := range 3 {
		answer(t, e, publish(fmt.Sprintf("d%d", i), "ses-1"), false)
	}
	p, err := e.Predict(ctx, publish("probe", "ses-1"))
	if err != nil || p.Verdict != limen.Block || p.Minimum.Held() {
		t.Fatalf("three denials: %+v, %v", p, err)
	}
	x, err := e.Explain(ctx, p.Key)
	if err != nil || x.Verdict != limen.Block || x.Minimum.Held() || !strings.HasSuffix(x.Reason, "Limen suggests block.") {
		t.Fatalf("the explanation of a block: %+v, %v", x, err)
	}
}

// Approvals of another label count nothing toward this one.
func TestMinimumIsPerLabel(t *testing.T) {
	e := minimumEngine(t, balanced, engine.Options{})
	for i := range 4 {
		other := publish(fmt.Sprintf("o%d", i), fmt.Sprintf("ses-%d", i))
		other.Name = "deploy"
		answer(t, e, other, true)
	}
	answer(t, e, publish("a", "ses-1"), true)
	p, err := e.Predict(ctx, publish("probe", "ses-2"))
	if err != nil || p.Minimum != held(1, 1) {
		t.Fatalf("one approval of publish and four of deploy: %+v, %v", p, err)
	}
}

// An explanation and a learned entry are held by the approvals now, with
// the counts; an explanation of a prediction made before the minimum was
// met is an allow once it is.
func TestExplanationsAndEntriesAreHeld(t *testing.T) {
	e := minimumEngine(t, balanced, engine.Options{})
	answer(t, e, publish("a1", "ses-1"), true)
	probe := publish("probe", "ses-1")
	if _, err := e.Predict(ctx, probe); err != nil {
		t.Fatal(err)
	}
	x, err := e.Explain(ctx, probe.Key)
	if err != nil {
		t.Fatal(err)
	}
	if x.Verdict != limen.Ask || x.AskKind != limen.AskSplit || x.Minimum != held(1, 1) || !strings.Contains(x.Reason, "1 time, in 1 session") || len(x.Evidence) != 1 {
		t.Fatalf("the explanation of one approval: %+v", x)
	}
	l, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Verdict != limen.Ask || l.Entries[0].Minimum != held(1, 1) || l.Entries[0].Approved != 1 {
		t.Fatalf("the learned entry of one approval: %+v", l.Entries)
	}
	answer(t, e, publish("a2", "ses-2"), true)
	answer(t, e, publish("a3", "ses-2"), true)
	if x, err = e.Explain(ctx, probe.Key); err != nil || x.Verdict != limen.Allow || x.Minimum.Held() || !strings.HasSuffix(x.Reason, "Limen suggests allow.") {
		t.Fatalf("the explanation once the minimum is met: %+v, %v", x, err)
	}
	// A kind filter leaves the tally of the label whole.
	l, err = e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s", Kind: "tool"})
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Verdict != limen.Allow || l.Entries[0].Minimum.Held() {
		t.Fatalf("the learned entry once the minimum is met: %+v, %v", l.Entries, err)
	}
}

// A replay holds its would-be allows to the minimum, as a live prediction
// does: a history of approvals in one session allows nothing, and the same
// history over two sessions allows.
func TestReplayIsHeld(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	history := func(sessions int) []limen.ReplayRecord {
		var rs []limen.ReplayRecord
		for i := range 12 {
			rs = append(rs, limen.ReplayRecord{At: start.Add(time.Duration(i) * time.Minute), Action: publish(fmt.Sprintf("r-%d", i), fmt.Sprintf("ses-%d", i%sessions)),
				Verdict: limen.Ask, ReviewProbability: 1, Source: "manual", Approve: new(true)})
		}
		return rs
	}
	for _, c := range []struct {
		sessions int
		o        engine.Options
		allows   bool
	}{
		{1, engine.Options{}, false},
		{2, engine.Options{}, true},
		{1, engine.Options{MinAllows: -1, MinSessions: -1}, true},
	} {
		e := minimumEngine(t, balanced, c.o)
		rep, err := e.Replay(ctx, recs(history(c.sessions)), limen.ReplayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := rep.Would[limen.Allow] > 0; got != c.allows {
			t.Errorf("%d sessions, options %+v: the replay would allow %d", c.sessions, c.o, rep.Would[limen.Allow])
		}
	}
}

// The options set the minimums: a negative value turns one count off, and
// both off is the rule before the minimum.
func TestMinimumOptions(t *testing.T) {
	for _, c := range []struct {
		o    engine.Options
		want limen.Minimum
	}{
		{engine.Options{}, held(1, 1)},
		{engine.Options{MinAllows: -1, MinSessions: -1}, limen.Minimum{}},
		{engine.Options{MinAllows: 1, MinSessions: 1}, limen.Minimum{}},
		{engine.Options{MinAllows: -1}, limen.Minimum{Allows: 1, Sessions: 1, MinSessions: limen.MinSessions}},
		{engine.Options{MinSessions: -1}, limen.Minimum{Allows: 1, Sessions: 1, MinAllows: limen.MinAllows}},
		{engine.Options{MinAllows: 5, MinSessions: 3}, limen.Minimum{Allows: 1, Sessions: 1, MinAllows: 5, MinSessions: 3}},
	} {
		e := minimumEngine(t, balanced, c.o)
		answer(t, e, publish("a", "ses-1"), true)
		p, err := e.Predict(ctx, publish("probe", "ses-1"))
		if err != nil || p.Minimum != c.want || (p.Verdict == limen.Allow) == c.want.Held() {
			t.Errorf("options %+v: %+v, %v, want %+v", c.o, p, err, c.want)
		}
	}
}

// Fail closed: when the approvals cannot be counted, the allow is held with
// none counted, and the reason says so; an explanation fails rather than
// guess.
func TestMinimumFailsClosed(t *testing.T) {
	f := newFaulty("", 0)
	e := minimumEngine(t, balanced, engine.Options{Store: f})
	for i := range 3 {
		answer(t, e, publish(fmt.Sprintf("a%d", i), fmt.Sprintf("ses-%d", i)), true)
	}
	// This prediction fits the model, so the next one reads the cases only
	// to count the approvals.
	if p, err := e.Predict(ctx, publish("fit", "ses-1")); err != nil || p.Verdict != limen.Allow {
		t.Fatalf("three approvals in three sessions: %+v, %v", p, err)
	}
	f.mu.Lock()
	f.fail, f.after = "Cases", f.calls["Cases"]
	f.mu.Unlock()
	p, err := e.Predict(ctx, publish("probe", "ses-1"))
	if err != nil {
		t.Fatal(err)
	}
	want := "The approval probability is 0.84, above the allow threshold (0.65). Limen could not count the approvals of this person for this label. Limen suggests ask."
	if p.Verdict != limen.Ask || p.Minimum != held(0, 0) || p.Reason != want {
		t.Fatalf("a prediction whose approvals cannot be counted: %+v", p)
	}
	if _, err := e.Explain(ctx, p.Key); err == nil {
		t.Error("an explanation whose approvals cannot be counted did not fail")
	}
}

// The table of docs/theory.md, "The answer minimum": three approvals of a
// publish give 0.85 with an evidence ratio of 0.69, an ask under both
// profiles in one session, and an allow under Balanced only, in two.
func TestTheoryTable(t *testing.T) {
	for _, c := range []struct {
		costs    limen.Costs
		sessions []string
		want     limen.Verdict
	}{
		{balanced, []string{"ses-1", "ses-1", "ses-1"}, limen.Ask},
		{balanced, []string{"ses-1", "ses-1", "ses-2"}, limen.Allow},
		{careful, []string{"ses-1", "ses-1", "ses-1"}, limen.Ask},
		{careful, []string{"ses-1", "ses-1", "ses-2"}, limen.Ask},
	} {
		e := minimumEngine(t, c.costs, engine.Options{})
		for i, ses := range c.sessions {
			answer(t, e, publish(fmt.Sprintf("a%d", i), ses), true)
		}
		p, err := e.Predict(ctx, publish("probe", "ses-1"))
		if err != nil || p.Verdict != c.want || fmt.Sprintf("%.2f %.2f", p.Approve, p.EvidenceRatio) != "0.85 0.69" {
			t.Errorf("%+v in %v: %+v, %v, want %s at 0.85 and 0.69", c.costs, c.sessions, p, err, c.want)
		}
	}
}
