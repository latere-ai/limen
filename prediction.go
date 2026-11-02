// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

// SourceUnavailable is the [Prediction.Source] of a prediction
// [FailClosed] produced because the predictor failed or timed out. Its
// verdict is [Ask]; its numbers are zero and mean nothing, because no
// model produced them, and Limen holds no record of it.
const SourceUnavailable = "learned/unavailable"

// Prediction is Limen's opinion about one action, for the decision point
// to compose with its own rules.
type Prediction struct {
	Key
	// Verdict is the suggestion, [Allow], [Ask] or [Block]:
	// Thresholds.Verdict(Approve). The decision point composes it with its
	// ceiling and review sampling before applying anything.
	Verdict Verdict `json:"verdict"`
	// Approve is the posterior predictive probability that the subject
	// approves the action, given every answer Limen has from them.
	Approve float64 `json:"approve"`
	// Uncertainty is the posterior standard deviation of the latent
	// value behind Approve: two actions with the same Approve can differ
	// here, one well known and one never seen.
	Uncertainty float64 `json:"uncertainty"`
	// EvidenceRatio is the share of the prior variance at the action that
	// the subject's answers removed, 1 - σ²/v₀ (spec 009): near 0 for an
	// action unlike any answered one, near 1 for a well-known one.
	EvidenceRatio float64 `json:"evidence_ratio"`
	// AskKind is [AskUnsure] or [AskSplit] when Verdict is [Ask]: whether
	// the ask rests on little evidence, so more answers will move it, or on
	// answers that disagree, so they will not. A decision point can let the
	// rules decide an unsure ask and keep a split one (spec 009). Empty for
	// an allow or a block.
	AskKind string `json:"ask_kind,omitempty"`
	// Thresholds and AuditRate come from the cost profile that applies,
	// named by Profile, and are recorded with the prediction because a
	// profile can change. Profile has the subject when the person's own
	// profile made the organization's stricter ([Stricter]).
	Thresholds Thresholds `json:"thresholds"`
	Profile    Scope      `json:"profile"`
	// AuditRate is the share of automatic verdicts the profile asks a
	// person to review at random, the rate the decision point passes to
	// its review draw.
	AuditRate float64 `json:"audit_rate"`
	// Source names the model: "learned/<version>", or [SourceUnavailable].
	Source string `json:"source"`
	// Reason is one sentence for the person who reads the record, saying
	// why the suggestion is what it is. It is never shown to an agent.
	Reason string `json:"reason"`
}

// Thresholds place an approval probability on the verdict scale: above
// AllowAbove is an allow, below BlockBelow a block, and everything between,
// both ends included, an ask. They are derived from a cost profile
// ([Costs.Thresholds]) and are never tuned on data.
type Thresholds struct {
	AllowAbove float64 `json:"allow_above"`
	BlockBelow float64 `json:"block_below"`
}

// Verdict is the verdict the thresholds give an approval probability: the
// choice among allow, ask and block with the least expected cost for the
// costs the thresholds came from (Chow's reject option). A probability
// outside [0, 1], or NaN, is an [Ask].
func (t Thresholds) Verdict(approve float64) Verdict {
	switch {
	case !unit(approve):
		return Ask
	case approve > t.AllowAbove:
		return Allow
	case approve < t.BlockBelow:
		return Block
	default:
		return Ask
	}
}
