// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"slices"
	"time"
)

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
	// HowAnswered is how the person gave the answer, as the client and
	// the decision point measured it (spec 011). It sets the weight of the
	// answer for the cover rule.
	HowAnswered
}

// Validate reports every field of a that breaks the contract, as one
// [*Error] with [CodeInvalidAnswer], or nil.
func (a Answer) Validate() error {
	var p problems
	a.Key.check(&p, "")
	a.HowAnswered.check(&p, "")
	return p.err(CodeInvalidAnswer)
}

// MaxAnswerMS bounds [HowAnswered.AnswerMS]: one day.
const MaxAnswerMS = 86_400_000

// The surfaces a person answers on.
const (
	// SurfacePrompt is a question in the conversation while the action
	// waits.
	SurfacePrompt = "prompt"
	// SurfaceNotification is a system notification.
	SurfaceNotification = "notification"
	// SurfaceQueue is a list of waiting questions or of reviews.
	SurfaceQueue = "queue"
	// SurfaceBulk is one gesture that answers more than one action.
	SurfaceBulk = "bulk"
	// SurfaceRule is a standing rule, not the person at that time.
	SurfaceRule = "rule"
)

// Surfaces lists the surfaces, in the order of their constants.
func Surfaces() []string {
	return []string{SurfacePrompt, SurfaceNotification, SurfaceQueue, SurfaceBulk, SurfaceRule}
}

// HowAnswered is how a person gave an answer (spec 011). The decision
// point relays it from the client of the person, and leaves out each field
// it does not know. No field holds text.
type HowAnswered struct {
	// AnswerMS is the milliseconds from the moment the question was
	// visible to the person to the answer, measured by the client, from 0
	// to [MaxAnswerMS].
	AnswerMS *int64 `json:"answer_ms,omitzero"`
	// WaitedMS is the milliseconds from the decision that showed the
	// action to the answer, measured by the decision point, 0 or more.
	WaitedMS *int64 `json:"waited_ms,omitzero"`
	// DetailsOpened is true if the person opened the details of the action
	// (the full input, the change, the target) before the answer, and
	// false if the client offered them and the person did not open them.
	DetailsOpened *bool `json:"details_opened,omitzero"`
	// SuggestionShown is true if the client showed the suggestion of Limen
	// (the verdict, the approval probability, the reason, or an
	// explanation) before the answer.
	SuggestionShown *bool `json:"suggestion_shown,omitzero"`
	// Surface is where the person answered, from [Surfaces]. A surface
	// outside the list is no surface, and it is not an error.
	Surface string `json:"surface,omitempty"`
	// Remembered is true if the answer also made a standing rule for
	// later actions. The rule itself is never sent.
	Remembered bool `json:"remembered,omitzero"`
}

func (h HowAnswered) check(p *problems, at string) {
	if h.AnswerMS != nil && (*h.AnswerMS < 0 || *h.AnswerMS > MaxAnswerMS) {
		p.add(at+"answer_ms", "is outside [0, 86400000]")
	}
	if h.WaitedMS != nil && *h.WaitedMS < 0 {
		p.add(at+"waited_ms", "is below 0")
	}
}

// Kept is h as Limen keeps it: a surface outside the list is dropped, so
// a kept answer holds words of the vocabulary alone.
func (h HowAnswered) Kept() HowAnswered {
	if !slices.Contains(Surfaces(), h.Surface) {
		h.Surface = ""
	}
	return h
}

// Case is everything Limen holds about one action: the action without its
// input, the prediction if one was made, the decision, and the answers.
type Case struct {
	Action     Action           `json:"action"`
	Prediction *Prediction      `json:"prediction,omitzero"`
	Decision   *Decision        `json:"decision,omitzero"`
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
	// HowAnswered is how the person gave the answer, as it was sent.
	HowAnswered
}

// CaseQuery selects one subject's cases, oldest first, one page at a time.
type CaseQuery struct {
	Org     string `json:"org"`
	Subject string `json:"subject"`
	// ActionID narrows the page to one action.
	ActionID string `json:"action_id,omitempty"`
	// Pending narrows the page to actions a decision showed a person that
	// have no review yet: the review queue.
	Pending bool `json:"pending,omitzero"`
	// Cursor is the previous page's NextCursor; empty for the first page.
	Cursor string `json:"cursor,omitempty"`
	// Limit bounds the page, from 1 to [MaxPageLimit]; zero is
	// [DefaultPageLimit].
	Limit int `json:"limit,omitzero"`
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
