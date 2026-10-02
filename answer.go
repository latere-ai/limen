// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "time"

// Answer is a person's decision about an action whose decision is
// recorded. Limen stamps the time it arrives.
//
// What an answer counts for follows from the decision, not from the
// caller: the first answer to an action the decision showed a person (a
// [Flag] or an [Ask]) is that action's review, which enters error
// estimates with weight one over the decision's review probability. Every
// other answer, such as a dispute of an action that ran without anyone
// being asked, or a person changing their mind, teaches the model and
// never enters an estimate, because nothing fixed the probability with
// which it was given. The latest answer is the one the model learns.
type Answer struct {
	Key
	Approve bool `json:"approve"`
	// By is who answered, when the caller relays the answer for someone
	// else, such as a harness forwarding a person's confirmation. It is
	// honored only when the authorizer allows the caller to relay for the
	// subject; otherwise the answerer is the caller. The answer trains the
	// subject's model either way.
	By string `json:"by,omitempty"`
}

// Validate reports every field of a that breaks the contract, as one
// [*Error] with [CodeInvalidAnswer], or nil.
func (a Answer) Validate() error {
	var p problems
	a.check(&p, "")
	return p.err(CodeInvalidAnswer)
}

// Case is everything Limen holds about one action: the action without its
// input, the prediction if one was made, the decision, and the answers.
type Case struct {
	Action     Action           `json:"action"`
	Prediction *Prediction      `json:"prediction,omitempty"`
	Decision   *Decision        `json:"decision,omitempty"`
	Answers    []RecordedAnswer `json:"answers,omitempty"`
}

// RecordedAnswer is an answer as Limen keeps it.
type RecordedAnswer struct {
	Approve bool      `json:"approve"`
	By      string    `json:"by"`
	At      time.Time `json:"at"`
	// Review is true for the answer that reviews an action the decision
	// showed a person, the one answer that enters error estimates.
	Review bool `json:"review"`
}

// CaseQuery selects one subject's cases, oldest first, one page at a time.
type CaseQuery struct {
	Org     string `json:"org"`
	Subject string `json:"subject"`
	// ActionID narrows the page to one action.
	ActionID string `json:"action_id,omitempty"`
	// Pending narrows the page to actions a decision showed a person that
	// have no review yet: the review queue.
	Pending bool `json:"pending,omitempty"`
	// Cursor is the previous page's NextCursor; empty for the first page.
	Cursor string `json:"cursor,omitempty"`
	// Limit bounds the page, from 1 to [MaxPageLimit]; zero is
	// [DefaultPageLimit].
	Limit int `json:"limit,omitempty"`
}

// The page bounds of [Ledger.Cases].
const (
	DefaultPageLimit = 100
	MaxPageLimit     = 500
)

// Validate reports every field of q that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil.
func (q CaseQuery) Validate() error {
	var p problems
	if q.Org == "" {
		p.add("org", "is empty")
	}
	if q.Subject == "" {
		p.add("subject", "is empty")
	}
	if q.Limit < 0 || q.Limit > MaxPageLimit {
		p.add("limit", "is outside [0, 500]")
	}
	return p.err(CodeInvalidQuery)
}

// Page is one page of cases. NextCursor is empty on the last page.
type Page struct {
	Entries    []Case `json:"entries"`
	NextCursor string `json:"next_cursor,omitempty"`
}
