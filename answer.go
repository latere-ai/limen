// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "time"

// AnswerKind says where an answer came from.
type AnswerKind string

const (
	// AnswerDecision is the person's decision on the call: at an ask,
	// or in the review of a flagged or audited call. It is the default.
	AnswerDecision AnswerKind = ""
	// AnswerContest is a later dispute of a call that already ran, for
	// example a reviewer rejecting its result. It trains the model but
	// never enters an error estimate, because nothing fixed the
	// probability with which a call is contested.
	AnswerContest AnswerKind = "contest"
)

// Answer is the person's decision on an assessed call. Who answered is
// the verified subject of the caller's credential, never a field of the
// answer.
type Answer struct {
	Approve bool       `json:"approve"`
	Kind    AnswerKind `json:"kind,omitempty"`
	At      time.Time  `json:"at"`
}

// Validate reports every field of a that breaks the contract, as one
// [*Error] with [CodeInvalidAnswer], or nil.
func (a Answer) Validate() error {
	var p problems
	if a.Kind != AnswerDecision && a.Kind != AnswerContest {
		p.add("kind", "is not one of the empty string, contest")
	}
	if a.At.IsZero() {
		p.add("at", "is not set")
	}
	return p.err(CodeInvalidAnswer)
}
