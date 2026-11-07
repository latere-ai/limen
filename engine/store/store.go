// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package store is where the engine keeps the ledger, the cost profiles,
// the fitted models and the kept weeks of the reports of organizations: one interface, with an in-memory implementation
// here and a file store in store/file for one process, self-hosted or
// hosted on a volume (spec 004). A store that more than one server shares
// is not built.
//
// The store keeps records and nothing else. Every rule of the contract
// (idempotency, one decision per action, which answer is a review) is the
// engine's; the engine serializes the operations on one subject, so a store
// needs no transactions across calls, only atomic single calls.
package store

import (
	"context"
	"time"

	"latere.ai/x/limen"
)

// Case is one action as the store keeps it. Action never holds the input:
// the engine reads the input once, at prediction time, and keeps the
// features it extracted.
type Case struct {
	// T is the case's ordinal in its subject's history, the kernel's time.
	T int64 `json:"t"`
	// ReceivedAt is when Limen first saw the case, which report windows read.
	ReceivedAt time.Time    `json:"received_at"`
	Digest     string       `json:"digest"`
	Action     limen.Action `json:"action"`
	// Featurizer names the featurizer A and C came from.
	Featurizer string    `json:"featurizer"`
	A          []float64 `json:"a"`
	C          []float64 `json:"c"`
	// Label names the action for a person, from the featurizer's closed
	// vocabulary, never from the input; Labels names that vocabulary. A
	// case from before labels has neither (spec 009).
	Label  string `json:"label,omitempty"`
	Labels string `json:"labels,omitempty"`
	// Scope is the reach of the action as Limen read it when the case was
	// made, and Families its families for the cover rule, both in words of
	// the vocabularies alone (spec 011). A case from before labels/2 has
	// neither, and the cover rule reads them from its records.
	Scope      *limen.Reach      `json:"scope,omitzero"`
	Families   []string          `json:"families,omitempty"`
	Prediction *limen.Prediction `json:"prediction,omitzero"`
	// PredictedBy is the number of answers the model that made Prediction
	// was fitted through, nil for a case from before models were named.
	PredictedBy *int64                 `json:"predicted_by,omitzero"`
	Decision    *limen.Decision        `json:"decision,omitzero"`
	DecidedAt   time.Time              `json:"decided_at,omitzero"`
	Answers     []limen.RecordedAnswer `json:"answers,omitempty"`
}

// Limen returns the case as the contract shows it.
func (c Case) Contract() limen.Case {
	out := limen.Case{Action: c.Action, Answers: append([]limen.RecordedAnswer(nil), c.Answers...)}
	out.Action.Input = nil
	if c.Prediction != nil {
		p := *c.Prediction
		out.Prediction = &p
	}
	if c.Decision != nil {
		d := *c.Decision
		d.Action = nil
		out.Decision = &d
	}
	return out
}

// Store keeps cases, profiles and model snapshots.
type Store interface {
	// Case returns the case for the key, and false when there is none.
	Case(ctx context.Context, k limen.Key) (Case, bool, error)
	// Put stores the case under its action's key, replacing any earlier one.
	Put(ctx context.Context, c Case) error
	// NextT returns the next ordinal of the subject's history, starting at
	// zero, and never the same ordinal twice for one subject.
	NextT(ctx context.Context, org, subject string) (int64, error)
	// Cases returns the subject's cases in the order of T.
	Cases(ctx context.Context, org, subject string) ([]Case, error)
	// Profile returns the profile saved for exactly this scope, and false
	// when there is none.
	Profile(ctx context.Context, s limen.Scope) (limen.Profile, bool, error)
	PutProfile(ctx context.Context, p limen.Profile) error
	// DeleteProfile removes the profile saved for exactly this scope, and
	// nothing else. It is idempotent.
	DeleteProfile(ctx context.Context, s limen.Scope) error
	// Subjects returns, sorted, the subjects of the organization that have
	// a case: the persons a report of the organization reads.
	Subjects(ctx context.Context, org string) ([]string, error)
	// ProfileSubjects returns, sorted, the subjects of the organization
	// that have an own profile saved, which are persons the organization
	// holds a record of even when they have no case.
	ProfileSubjects(ctx context.Context, org string) ([]string, error)
	// WeekReport returns the kept week of the organization that starts at
	// from, and false when none is kept.
	WeekReport(ctx context.Context, org string, from time.Time) (limen.WeekReport, bool, error)
	// PutWeekReport keeps a closed week of the organization, under its
	// From. A kept week holds no person's record, so Forget leaves it.
	PutWeekReport(ctx context.Context, org string, w limen.WeekReport) error
	// Snapshot returns a model's stored state, and false when there is none.
	Snapshot(ctx context.Context, org, subject, version string) ([]byte, bool, error)
	PutSnapshot(ctx context.Context, org, subject, version string, state []byte) error
	// Forget deletes every case, the counter and every snapshot of the
	// subject in the organization. It is idempotent. It leaves the
	// profiles, which the engine removes, and the kept weeks.
	Forget(ctx context.Context, org, subject string) error
	Close() error
}
