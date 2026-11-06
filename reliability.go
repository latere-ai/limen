// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "time"

// The two sources a reliability query names besides the sources of
// signals and decisions.
const (
	// OfLearned asks about Limen's own suggestions.
	OfLearned = "learned"
	// OfApplied asks about the verdicts decision points applied, whatever
	// decided them.
	OfApplied = "applied"
)

// ReliabilityQuery names whose error rates to report, for which person,
// over which window.
type ReliabilityQuery struct {
	Org     string `json:"org"`
	Subject string `json:"subject"`
	// Of is [OfLearned], [OfApplied], a [Decision.Source] such as
	// "rules/1", or a [Signal.Source] such as "classifier/claude-auto-mode".
	Of   string    `json:"of"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Validate reports every field of q that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil.
func (q ReliabilityQuery) Validate() error {
	var p problems
	if q.Org == "" {
		p.add("org", "is empty")
	}
	if q.Subject == "" {
		p.add("subject", "is empty")
	}
	if q.Of == "" {
		p.add("of", "is empty")
	}
	checkWindow(&p, q.From, q.To)
	return p.err(CodeInvalidQuery)
}

// checkWindow holds a window to its bounds: each bound is set, and to is
// after from. A bound that is not set is at fault whatever the other is.
func checkWindow(p *problems, from, to time.Time) {
	if from.IsZero() {
		p.add("from", "is not set")
	}
	switch {
	case to.IsZero():
		p.add("to", "is not set")
	case !from.IsZero() && !from.Before(to):
		p.add("to", "is not after from")
	}
}

// Reliability reports how often one source's verdicts disagree with one
// person over one window. A source allowed an action when its verdict was
// [Allow] or [Flag]: the action ran with no one's prior approval. It
// blocked an action when its verdict was [Block], or, for a decision, an
// [Ask] with a review probability below one, which is an automatic block
// the review draw held for a person. Counting those drawn actions is what
// puts the reviews inside the source's allows and blocks. The rates are
// for that person and that window only: the same source is more or less
// reliable depending on whose decisions it stands in for, and when.
type Reliability struct {
	ReliabilityQuery
	// FalseAllow estimates the share of the source's allows the person
	// would have denied; FalseBlock the share of its blocks they would have
	// approved.
	FalseAllow  Estimate    `json:"false_allow"`
	FalseBlock  Estimate    `json:"false_block"`
	Certificate Certificate `json:"certificate"`
}

// Estimate is one error rate. Each review counts with weight one over its
// decision's review probability (Horvitz-Thompson), so the estimate is
// unbiased over the actions it covers, and noisy when the window holds few
// reviews. Shown actions not yet answered are treated as missing at
// random: the reviews' weights are scaled by the weighted share of shown
// actions that have one.
type Estimate struct {
	// Value is nil when no action in the window had a positive review
	// probability: the rate cannot be estimated, which is not zero.
	Value *float64 `json:"value"`
	// Interval is a 95% interval for the rate, present with Value.
	Interval *[2]float64 `json:"interval,omitzero"`
	// Covered is the share of the source's allows (or blocks) in the window
	// whose review probability was positive. The estimate speaks for that
	// share only.
	Covered float64 `json:"covered"`
	// Reviews counts the reviews the estimate rests on, and Audits those
	// among them whose review probability was below one.
	Reviews int `json:"reviews"`
	Audits  int `json:"audits"`
	// Unanswered counts actions in the window a decision showed a person
	// that have no review yet. The estimate treats them as missing at
	// random; a count that is large next to Reviews weakens it.
	Unanswered int `json:"unanswered"`
}

// Certificate tests a profile's claim, a false-allow rate below Alpha at
// confidence 1 - Delta, against the source's most recent run of clean
// audited allows inside the window.
type Certificate struct {
	Alpha float64 `json:"alpha"`
	Delta float64 `json:"delta"`
	// Clean counts consecutive audited allows, most recent first, that the
	// person approved. A denied one ends the run.
	Clean int `json:"clean"`
	// Needed is [CertificationSize] of Alpha and Delta.
	Needed int  `json:"needed"`
	Holds  bool `json:"holds"`
	// Considered counts the audits in the clean run whose answer is
	// considered, with weight 1 under weights/1 (spec 011). The claim and
	// its arithmetic do not read it: a reader can see whether the
	// certificate rests on quick answers.
	Considered int `json:"considered"`
}
