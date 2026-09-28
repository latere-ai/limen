// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "time"

// OfLearned is the [ReliabilityQuery.Of] that asks about Limen's own
// automatic verdicts rather than a signal source's.
const OfLearned = "learned"

// ReliabilityQuery names whose error rates to report, for which person,
// over which window.
type ReliabilityQuery struct {
	Subject string `json:"subject"`
	// Of is [OfLearned] or a [Signal.Source], e.g. "judge/claude-auto-mode".
	Of   string    `json:"of"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Validate reports every field of q that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil.
func (q ReliabilityQuery) Validate() error {
	var p problems
	if q.Subject == "" {
		p.add("subject", "is empty")
	}
	if q.Of == "" {
		p.add("of", "is empty")
	}
	switch {
	case q.From.IsZero():
		p.add("from", "is not set")
	case q.To.IsZero():
		p.add("to", "is not set")
	case !q.From.Before(q.To):
		p.add("to", "is not after from")
	}
	return p.err(CodeInvalidQuery)
}

// Reliability reports how often the automatic verdicts of Limen, or of
// one signal source, disagree with one person over one window. The rates
// are for that person and that window only: the same judge is more or less
// reliable depending on whose decisions it stands in for and when.
type Reliability struct {
	Subject string    `json:"subject"`
	Of      string    `json:"of"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	// FalseAllow estimates the share of allowed calls the person would
	// have denied; FalseBlock the share of blocked calls they would have
	// approved.
	FalseAllow  Estimate    `json:"false_allow"`
	FalseBlock  Estimate    `json:"false_block"`
	Certificate Certificate `json:"certificate"`
}

// Estimate is one error rate. Each answered call counts with weight
// 1/probability, the probability with which it was going to receive an
// answer: one for a call a person had to decide, the audit rate for an
// audited automatic verdict (Horvitz-Thompson). The estimate is unbiased,
// and it is noisy when the window holds few labels.
type Estimate struct {
	// Value is nil when no answer in the window has a known probability,
	// which means the rate cannot be estimated, not that it is zero.
	Value *float64 `json:"value"`
	// Interval is a 95% interval for the rate, present with Value.
	Interval *[2]float64 `json:"interval,omitempty"`
	// Labels counts the answered calls the estimate rests on; Audits
	// counts those among them that were sampled for review.
	Labels int `json:"labels"`
	Audits int `json:"audits"`
	// Unanswered counts calls in the window sampled for review that nobody
	// has answered. The estimate treats them as missing at random; a count
	// that is large next to Audits weakens it, and answering them is the
	// remedy.
	Unanswered int `json:"unanswered"`
}

// Certificate tests the profile's claim, a false-allow rate below Alpha at
// confidence 1 - Delta, against the most recent run of clean audits.
type Certificate struct {
	Alpha float64 `json:"alpha"`
	Delta float64 `json:"delta"`
	// Clean counts consecutive audited allows, most recent first, that the
	// person approved. A denied audit ends the run.
	Clean int `json:"clean"`
	// Needed is [CertificationSize] of Alpha and Delta.
	Needed int  `json:"needed"`
	Holds  bool `json:"holds"`
}
