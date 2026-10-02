// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "time"

// MaxReplayRecords bounds one replay.
const MaxReplayRecords = 100000

// ReplayRecord is one action as a decision point recorded it: when it was
// proposed, the action, the decision applied with its review probability,
// and the person's answer if there was one.
type ReplayRecord struct {
	At                time.Time `json:"at"`
	Action            Action    `json:"action"`
	Verdict           Verdict   `json:"verdict"`
	ReviewProbability float64   `json:"review_probability"`
	Source            string    `json:"source"`
	// Approve is the person's answer, or nil when there was none.
	Approve *bool `json:"approve,omitempty"`
}

// Validate reports every field of r that breaks the contract, as one
// [*Error] with [CodeInvalidReplay], or nil.
func (r ReplayRecord) Validate() error {
	var p problems
	if r.At.IsZero() {
		p.add("at", "is not set")
	}
	r.Action.check(&p, "action.")
	if !r.Verdict.Valid() {
		p.add("verdict", "is not one of allow, flag, ask, block")
	}
	checkReview(&p, "", r.Verdict, r.ReviewProbability)
	if r.Source == "" {
		p.add("source", "is empty")
	}
	return p.err(CodeInvalidReplay)
}

// ReplayOptions control a replay.
type ReplayOptions struct {
	// Keep learns the records into the live models and the ledger, as if
	// they had arrived as decisions and answers. Without it, a replay
	// learns in a scratch model and keeps nothing.
	Keep bool `json:"keep,omitempty"`
}

// ReplayReport is what Limen would have suggested on a recorded history:
// each record is predicted in time order before its own answer is learned.
type ReplayReport struct {
	Records  int `json:"records"`
	Answered int `json:"answered"`
	// Would counts Limen's suggestions on the records.
	Would map[Verdict]int `json:"would"`
	// AllowedDenied counts answered records Limen would have allowed and the
	// person denied; BlockedApproved those it would have blocked and the
	// person approved.
	AllowedDenied   int `json:"allowed_denied"`
	BlockedApproved int `json:"blocked_approved"`
	// FalseAllow estimates the false-allow rate of Limen's would-be allows
	// from the records' answers, weighted by their review probabilities.
	FalseAllow Estimate `json:"false_allow"`
	// Kept is true when the records were learned into the live models.
	Kept bool `json:"kept"`
}
