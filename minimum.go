// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

// The answer minimum is an interim rule that holds until the consent
// design replaces it: Limen suggests [Allow] for an action only after the
// person has approved actions with the same label at least MinAllows
// times, in at least MinSessions distinct sessions. One approval is not a
// pattern, and approvals from one session can be the habit of one task.
// It applies after the model and the thresholds: an allow the thresholds
// give that misses a minimum becomes an [Ask], and an ask or a block is
// never changed. An implementation that cannot count the approvals
// suggests ask.
//
// An action counts when its latest answer, the one the model learns,
// approves; a denial never counts, and an approval a later denial replaced
// no longer counts. Only the person's own cases count: those of the same
// org and subject. [SessionOf] names the session each one counts under.
const (
	MinAllows   = 3
	MinSessions = 2
)

// Minimum is the answer minimum that turned an allow into an ask: the
// person's approvals of actions with the action's label, the sessions
// they came from, and the minimums. It is set on a [Prediction], an
// [Explanation] or a [LearnedEntry] only when it changed the verdict, so
// that one of Allows and Sessions is below its minimum, and it is the zero
// value otherwise ([Minimum.Held]).
type Minimum struct {
	// Allows counts the person's actions with the label whose latest
	// answer approves.
	Allows int `json:"allows"`
	// Sessions counts the distinct sessions of those actions.
	Sessions int `json:"sessions"`
	// MinAllows and MinSessions are the minimums the implementation
	// applies: [MinAllows] and [MinSessions] unless its operator set
	// others. Zero is no minimum.
	MinAllows   int `json:"min_allows"`
	MinSessions int `json:"min_sessions"`
}

// Met reports whether the counts reach both minimums.
func (m Minimum) Met() bool { return m.Allows >= m.MinAllows && m.Sessions >= m.MinSessions }

// Held reports whether m is set: whether the answer minimum turned an
// allow into an ask.
func (m Minimum) Held() bool { return m != Minimum{} }

// SessionOf names the session an action counts under for the answer
// minimum: the actor's session when the decision point sends one, and
// otherwise a session of the action's own, so an action with no session
// never shares one with another action. Limen never reads a session out of
// the action id.
func SessionOf(a Action) string {
	if a.Actor != nil && a.Actor.Session != "" {
		return "session " + a.Actor.Session
	}
	return "action " + a.ActionID
}

// Tally counts a person's approvals of actions with one label toward the
// answer minimum. The zero value is an empty tally.
type Tally struct {
	allows   int
	sessions map[string]bool
}

// Add counts the action a when answers, its recorded answers oldest first,
// end with an approval. A case with no answer, or whose latest answer is a
// denial, adds nothing.
func (t *Tally) Add(a Action, answers []RecordedAnswer) {
	if len(answers) == 0 || !answers[len(answers)-1].Approve {
		return
	}
	if t.sessions == nil {
		t.sessions = map[string]bool{}
	}
	t.allows++
	t.sessions[SessionOf(a)] = true
}

// Minimum is the tally against the minimums minAllows and minSessions.
func (t Tally) Minimum(minAllows, minSessions int) Minimum {
	return Minimum{Allows: t.allows, Sessions: len(t.sessions), MinAllows: minAllows, MinSessions: minSessions}
}

// Hold applies the answer minimum m to v, the verdict the thresholds give:
// an allow becomes an ask when m is not met, and every other verdict stays.
// The returned Minimum is m when it changed the verdict and the zero value
// otherwise, as a prediction carries it.
func Hold(v Verdict, m Minimum) (Verdict, Minimum) {
	if v != Allow || m.Met() {
		return v, Minimum{}
	}
	return Ask, m
}
