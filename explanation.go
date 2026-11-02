// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"context"
	"time"
)

// Explainer shows a person what Limen's suggestions are made of (spec 009):
// why one prediction is what it is, what the model has learned about the
// person, and what another cost profile would have done with their recorded
// predictions. It reads and changes nothing. A decision point never hands
// what it returns to an agent.
type Explainer interface {
	// Explain returns the explanation of one action's prediction, by the
	// person's current model. An action Limen has no record of is
	// [CodeNoCase].
	Explain(ctx context.Context, k Key) (Explanation, error)
	// Learned returns what the model has learned about the person.
	Learned(ctx context.Context, q LearnedQuery) (Learned, error)
	// Preview re-applies another profile's thresholds to the person's
	// recorded predictions in a window.
	Preview(ctx context.Context, r PreviewRequest) (Preview, error)
}

// Validate reports every empty field of k as one [*Error] with
// [CodeInvalidQuery], or nil: a key named in a query.
func (k Key) Validate() error {
	var p problems
	k.check(&p, "")
	return p.err(CodeInvalidQuery)
}

// The two kinds of uncertainty behind a suggested [Ask], told apart by
// the evidence ratio at the action (spec 009).
const (
	// AskUnsure is an ask on little evidence: the person's answers
	// removed less than half of the prior variance at the action, so they
	// say little about it, and more answers can move it.
	AskUnsure = "unsure"
	// AskSplit is an ask on evidence: the person's answers removed half
	// of the prior variance at the action or more, and the approval
	// probability is still inside the profile's ask band, or above it with
	// too few approvals for the answer minimum ([Minimum]). The answers
	// disagree, or agree but not yet strongly enough for the thresholds,
	// as two approvals of one action can under a 0.90 allow threshold.
	// More answers that agree can still move it out of the band; answers
	// that disagree keep it there. The paper's Proposition 3 holds only in
	// the limit of much evidence with mixed answers. AskSplit alone does
	// not say the answers disagree: an [Explanation] whose Evidence holds
	// an approval and a denial with one label does, as does a
	// [LearnedEntry] with Approved and Denied both above zero.
	AskSplit = "split"
)

// ModelRef names one fitted model of a person: the source of its
// predictions and the number of the person's answers it was fitted
// through. Two predictions with the same ModelRef came from the same
// posterior.
type ModelRef struct {
	Source  string `json:"source"`
	Answers int64  `json:"answers"`
}

// Explanation takes one prediction apart in the model's own terms
// (spec 009). The model's latent mean is a weighted sum over the person's
// fitted answers, so each answer's share of it is exact, not an estimate,
// and so is the split into the parts the model is built from.
type Explanation struct {
	Key
	// Model is the model that explains: the person's current one.
	// PredictedBy is the model that made the recorded prediction, absent
	// when the action has no prediction or one from before models were
	// named. When the two differ the model has learned since, and the
	// explanation is of what it suggests now.
	Model       ModelRef  `json:"model"`
	PredictedBy *ModelRef `json:"predicted_by,omitempty"`
	// Approve, Uncertainty and Verdict are the prediction Model makes for
	// the action, at the action's own time, under Thresholds: those of the
	// recorded prediction, or of the profile that applies now. Verdict is
	// held to the answer minimum by the person's approvals now, as a
	// prediction is, with Minimum set when it turned an allow into an ask.
	Approve     float64    `json:"approve"`
	Uncertainty float64    `json:"uncertainty"`
	Verdict     Verdict    `json:"verdict"`
	Thresholds  Thresholds `json:"thresholds"`
	Minimum     Minimum    `json:"minimum,omitzero"`
	// Reason is one sentence for the person, saying why Verdict is what
	// it is, as a prediction's reason does. An implementation from before
	// it leaves it empty.
	Reason string `json:"reason,omitempty"`
	// Mean is the latent mean; Parts split it and sum to it, and are absent
	// for a model whose kernel has no parts.
	Mean  float64 `json:"mean"`
	Parts *Parts  `json:"parts,omitempty"`
	// EvidenceRatio is the share of the prior variance the person's answers
	// removed at this action, in [0, 1).
	EvidenceRatio float64 `json:"evidence_ratio"`
	// AskKind is [AskUnsure] or [AskSplit] when Verdict is [Ask].
	AskKind string `json:"ask_kind,omitempty"`
	// Evidence are the answers that moved the prediction most: up to three
	// toward the verdict's side and two against it, or three each way for
	// an ask, ordered by Share. Never nil.
	Evidence []Evidence `json:"evidence"`
}

// Parts split a latent mean by the additive model's terms.
type Parts struct {
	// Action is what the model knows about this kind of action at any
	// time; Recent the person's tolerance lately, which every answer moves
	// whatever its action; Interaction the small term between them.
	Action      float64 `json:"action"`
	Recent      float64 `json:"recent"`
	Interaction float64 `json:"interaction"`
}

// Evidence is one answer's share of an explanation.
type Evidence struct {
	ActionID string    `json:"action_id"`
	Label    string    `json:"label"`
	At       time.Time `json:"at"`
	// Approve is the answer: an approval pushed toward running, a denial
	// toward stopping.
	Approve bool `json:"approve"`
	// Share is the answer's absolute contribution over the sum of all
	// absolute contributions, in [0, 1].
	Share float64 `json:"share"`
}

// LearnedQuery names whose model to show. Agent and Kind choose the
// profile that applies and Kind filters the entries.
type LearnedQuery struct {
	Org     string `json:"org"`
	Subject string `json:"subject"`
	Agent   string `json:"agent,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// Validate reports every field of q that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil.
func (q LearnedQuery) Validate() error {
	var p problems
	if q.Org == "" {
		p.add("org", "is empty")
	}
	if q.Subject == "" {
		p.add("subject", "is empty")
	}
	return p.err(CodeInvalidQuery)
}

// Learned is what the model has learned about a person (spec 009).
type Learned struct {
	Org     string   `json:"org"`
	Subject string   `json:"subject"`
	Model   ModelRef `json:"model"`
	// Entries has one entry per label among the person's answered cases,
	// most answered first. Never nil.
	Entries []LearnedEntry `json:"entries"`
	// Recent is the shared tolerance part of the model at up to 20 evenly
	// spaced points of the person's decisions, oldest first. Never nil.
	Recent []TrendPoint `json:"recent"`
}

// LearnedEntry is one kind of action the person has answered, and what the
// model suggests for it now: at its most recent case, moved to the
// person's current time, under the profile that applies to it, and held
// to the answer minimum, with Minimum set when it turned an allow into an
// ask.
// EvidenceRatio and AskKind are an explanation's at that point, so a scale
// can draw an unsure ask apart from a split one. A split entry has answers
// that disagree only when Approved and Denied are both above zero.
type LearnedEntry struct {
	Label         string     `json:"label"`
	Kind          string     `json:"kind"`
	Name          string     `json:"name"`
	Approved      int        `json:"approved"`
	Denied        int        `json:"denied"`
	Approve       float64    `json:"approve"`
	Uncertainty   float64    `json:"uncertainty"`
	Verdict       Verdict    `json:"verdict"`
	EvidenceRatio float64    `json:"evidence_ratio"`
	AskKind       string     `json:"ask_kind,omitempty"`
	Thresholds    Thresholds `json:"thresholds"`
	Minimum       Minimum    `json:"minimum,omitzero"`
}

// TrendPoint is the shared tolerance part at one point of a person's
// decisions: T is the decision's ordinal and At when it arrived.
type TrendPoint struct {
	T     int64     `json:"t"`
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// PreviewRequest names the costs to preview on a person's recorded
// predictions in a window.
type PreviewRequest struct {
	Org       string    `json:"org"`
	Subject   string    `json:"subject"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Costs     Costs     `json:"costs"`
	AuditRate float64   `json:"audit_rate"`
}

// Validate reports every field of r that breaks the contract, or nil. The
// costs are checked as a profile's are: costs that would let Limen decide
// on no evidence fail with [CodeAskTooCostly].
func (r PreviewRequest) Validate() error {
	var p problems
	if r.Org == "" {
		p.add("org", "is empty")
	}
	if r.Subject == "" {
		p.add("subject", "is empty")
	}
	checkWindow(&p, r.From, r.To)
	if !unit(r.AuditRate) {
		p.add("audit_rate", "is outside [0, 1]")
	}
	if err := p.err(CodeInvalidQuery); err != nil {
		return err
	}
	_, err := r.Costs.Thresholds()
	return err
}

// Preview is what the previewed costs would have done with the person's
// recorded predictions: the predictions as they were made, re-thresholded,
// with no model refitted.
type Preview struct {
	PreviewRequest
	Thresholds Thresholds `json:"thresholds"`
	// Records counts the predictions in the window and Would the verdicts
	// the thresholds give them.
	Records int             `json:"records"`
	Would   map[Verdict]int `json:"would"`
	// Answered counts the records with an answer; AllowedDenied those the
	// thresholds would allow and the person denied, BlockedApproved those
	// they would block and the person approved.
	Answered        int `json:"answered"`
	AllowedDenied   int `json:"allowed_denied"`
	BlockedApproved int `json:"blocked_approved"`
	// ExpectedAsks is the asks plus the audits the audit rate would draw
	// from the automatic verdicts.
	ExpectedAsks float64 `json:"expected_asks"`
}
