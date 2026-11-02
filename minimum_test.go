// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "testing"

// An action counts under its actor's session, and an action with no
// session under one of its own that no session name can match.
func TestSessionOf(t *testing.T) {
	with := Action{ActionID: "ses/1", Actor: &Actor{Kind: "agent", Session: "ses"}}
	empty := Action{ActionID: "ses", Actor: &Actor{Kind: "agent"}}
	none := Action{ActionID: "ses"}
	if SessionOf(with) == SessionOf(empty) || SessionOf(empty) != SessionOf(none) {
		t.Errorf("sessions %q, %q, %q", SessionOf(with), SessionOf(empty), SessionOf(none))
	}
	other := with
	other.ActionID = "ses/2"
	if SessionOf(with) != SessionOf(other) {
		t.Errorf("two actions of one session count under %q and %q", SessionOf(with), SessionOf(other))
	}
}

// A tally counts the actions whose latest answer approves, and their
// sessions; Hold turns an allow into an ask until both minimums are met,
// and leaves every other verdict.
func TestTallyAndHold(t *testing.T) {
	in := func(id, ses string) Action {
		return Action{ActionID: id, Actor: &Actor{Kind: "agent", Session: ses}}
	}
	yes, no := RecordedAnswer{Approve: true}, RecordedAnswer{}
	var tally Tally
	tally.Add(in("1", "a"), []RecordedAnswer{yes})
	tally.Add(in("2", "a"), []RecordedAnswer{yes})
	tally.Add(in("3", "b"), []RecordedAnswer{no})
	tally.Add(in("4", "b"), []RecordedAnswer{yes, no})
	tally.Add(in("5", "b"), nil)
	m := tally.Minimum(MinAllows, MinSessions)
	if m != (Minimum{Allows: 2, Sessions: 1, MinAllows: 3, MinSessions: 2}) || m.Met() || !m.Held() {
		t.Fatalf("tally %+v", m)
	}
	for _, v := range []Verdict{Ask, Block} {
		if got, held := Hold(v, m); got != v || held.Held() {
			t.Errorf("Hold(%s) = %s, %+v", v, got, held)
		}
	}
	if got, held := Hold(Allow, m); got != Ask || held != m {
		t.Errorf("Hold(allow) under %+v = %s, %+v", m, got, held)
	}
	tally.Add(in("6", "b"), []RecordedAnswer{no, yes})
	m = tally.Minimum(MinAllows, MinSessions)
	if got, held := Hold(Allow, m); !m.Met() || got != Allow || held.Held() {
		t.Errorf("Hold(allow) under %+v = %s, %+v", m, got, held)
	}
	if got, held := Hold(Allow, (&Tally{}).Minimum(0, 0)); got != Allow || held.Held() {
		t.Errorf("Hold(allow) with no minimum = %s, %+v", got, held)
	}
}
