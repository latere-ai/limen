// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "time"

// The cover rule (spec 011): Limen suggests [Allow] for an action only if
// the approvals of the person cover it. An approval covers an action of
// the same family whose scope is within the scope of the approval. For each
// family of the action, the covering approvals with weight must have a
// total weight of MinWeight or more, from MinSessions or more different
// sessions. The rule applies after the model and the thresholds: an allow
// the thresholds give that is not covered becomes an [Ask] of kind
// [AskWider] or [AskFew], and an ask or a block is never changed.
//
// A case counts when its latest answer, the one the model learns,
// approves. Only the person's own cases count, in the window of the model,
// the most recent answered cases it is fitted on. [SessionOf] names the
// session each one counts under.
const (
	MinWeight   = 3
	MinSessions = 2
)

// The weight of an answer, weights/1 (spec 011), is 0 or 1 in the cover
// rule. A denial has weight 1, except from a standing rule. An approval
// has weight 0 if a standing rule gave it, if one gesture answered many
// actions, if it came faster than AnswerFloor, if it followed a lean the
// client showed, or if AnswerFatigue or more reviews came before it in its
// session; each rule but the first needs the details to be unopened. The
// weight applies to the cover rule only. The estimates and the
// certificate never read it.
const (
	WeightsVersion = "weights/1"
	AnswerFloor    = 2 * time.Second
	AnswerFatigue  = 50
)

// SessionOf names the session a case counts under for the cover rule and
// for the position of an answer: the actor's session when the decision
// point sends one, and otherwise the UTC day the case was received, one
// session for each day. Limen never reads a session out of the action id.
func SessionOf(a Action, received time.Time) string {
	if a.Actor != nil && a.Actor.Session != "" {
		return "session " + a.Actor.Session
	}
	return "day " + received.UTC().Format(time.DateOnly)
}

// Cover is the cover rule's result for one action: whether the action is
// covered, and the weight and the sessions of its covering approvals,
// against the settings that applied.
type Cover struct {
	Covered bool `json:"covered"`
	// Weight is the total weight of the covering approvals, and Sessions
	// the number of their different sessions, for the family of the action
	// with the least weight. If a family misses a setting, only the
	// families that miss one are compared.
	Weight   float64 `json:"weight"`
	Sessions int     `json:"sessions"`
	// MinWeight and MinSessions are the settings that applied: the greater
	// of the installation's and the cost profile's.
	MinWeight   int `json:"min_weight"`
	MinSessions int `json:"min_sessions"`
}

// ExplainedCover is the cover of an explanation: the cover and the
// approvals of the family that are nearest to covering the action.
type ExplainedCover struct {
	Cover
	// Nearest are up to three approved cases of a family of the action
	// whose scope the action exceeds, closest first: the fewest dimensions
	// in which the action exceeds the case, and then the most recent.
	Nearest []Approval `json:"nearest,omitempty"`
}

// Approval is one approved case an explanation names.
type Approval struct {
	ActionID string `json:"action_id"`
	Label    string `json:"label"`
	// At is the time of the latest answer of the case.
	At time.Time `json:"at"`
}

// Consent is what a cost profile sets for the cover rule. A profile can
// make the settings of the installation greater and never smaller: the
// setting that applies is the greater of the two. Zero is the setting of
// the installation, and a value below zero is refused.
type Consent struct {
	MinWeight   int `json:"min_weight,omitzero"`
	MinSessions int `json:"min_sessions,omitzero"`
}

// Minimum is the answer minimum of v0.6, the interim rule the cover rule
// replaced.
//
// Deprecated: Limen no longer sets it. A prediction, an explanation and a
// learned entry keep the field for the readers of v0.6, because the name
// of a field never changes. Read [Cover].
type Minimum struct {
	Allows      int `json:"allows"`
	Sessions    int `json:"sessions"`
	MinAllows   int `json:"min_allows"`
	MinSessions int `json:"min_sessions"`
}
