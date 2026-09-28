// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

// SourceUnavailable is the [Assessment.Source] of an assessment
// [FailClosed] produced because the gate failed or timed out. Such an
// assessment has no ID, and its Approve, Uncertainty, Score and Thresholds
// are zero and mean nothing: no model produced them.
const SourceUnavailable = "learned/unavailable"

// Assessment is Limen's answer for one [Action].
type Assessment struct {
	// ID names the assessment for [Gate.Answer]: "asm_" and an opaque
	// suffix.
	ID     string `json:"id"`
	CallID string `json:"call_id"`
	// Verdict is what the harness applies. It is never more permissive than
	// the action's ceiling. Without an audit it is Thresholds.Verdict(Score)
	// narrowed by the ceiling; with one it is [Flag] for an audited allow
	// and [Ask] for an audited block.
	Verdict Verdict `json:"verdict"`
	// Approve is the posterior predictive probability that the subject
	// approves the call, given every answer Limen has from them.
	Approve float64 `json:"approve"`
	// Uncertainty is the posterior standard deviation of the latent
	// tolerance behind Approve. Two calls with the same Approve can differ
	// here: one well known, one never seen.
	Uncertainty float64 `json:"uncertainty"`
	// Score is 1 - Approve, the probability of a deny: the risk score a
	// harness that orders its layers by score reads.
	Score      float64    `json:"score"`
	Thresholds Thresholds `json:"thresholds"`
	// Profile is the id of the cost profile the thresholds come from.
	Profile string `json:"profile"`
	// Audit is set when the call was sampled for review. The person's
	// answer is then a label with a known sampling probability, the only
	// kind an unbiased error estimate can use. It is absent when the
	// ceiling already makes the person see the call, a ceiling of [Flag]
	// or [Ask], because the answer then arrives with probability one.
	Audit *Audit `json:"audit,omitempty"`
	// Source names the model that produced the assessment:
	// "learned/<version>", or [SourceUnavailable].
	Source string `json:"source"`
	// Reason is one sentence for the person who reads the decision record,
	// saying why the verdict is what it is.
	Reason string `json:"reason"`
}

// Thresholds place a score on the verdict scale. A score below AskAt is
// an allow, a score above BlockAt is a block, and everything between,
// both ends included, is an ask. They are derived from a cost profile
// ([Costs.Thresholds]) and are never tuned on data.
type Thresholds struct {
	AskAt   float64 `json:"ask_at"`
	BlockAt float64 `json:"block_at"`
}

// Verdict is the verdict the thresholds give a score: the Bayes-optimal
// choice among allow, ask, and block for the costs the thresholds came
// from. A score outside [0, 1] or NaN is an [Ask].
func (t Thresholds) Verdict(score float64) Verdict {
	switch {
	case !unit(score):
		return Ask
	case score < t.AskAt:
		return Allow
	case score > t.BlockAt:
		return Block
	default:
		return Ask
	}
}

// Band is the same thresholds in approval probability: Limen asks when
// the probability of approval lies in [low, high].
func (t Thresholds) Band() (low, high float64) {
	return 1 - t.BlockAt, 1 - t.AskAt
}

// Audit marks a call whose automatic verdict is shown to the person
// anyway, so that their answer is a label with a known probability.
type Audit struct {
	// Of is the automatic verdict under review: [Allow] or [Block].
	Of Verdict `json:"of"`
	// Probability is the chance with which this call was sampled, fixed
	// when it was assessed. An answer's weight in an error estimate is
	// 1/Probability. A caller never sets it.
	Probability float64 `json:"probability"`
}
