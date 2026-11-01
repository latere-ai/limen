// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"context"
	"iter"
	"time"
)

// Predictor is the hot path: what a decision point asks before it
// decides. The engine implements it in process and the HTTP client over
// the network; a decision point codes against the interface and wraps it
// with [FailClosed].
type Predictor interface {
	// Predict returns Limen's prediction for one action. It is idempotent
	// on the action's Key: a repeat with the same action returns the first
	// prediction, and a repeat with a different action fails with
	// [CodeActionConflict].
	Predict(ctx context.Context, a Action) (Prediction, error)
}

// Ledger keeps decisions and answers and reports error rates. A decision
// point records every decision it makes, not only the ones a person sees,
// because the share of actions a source allowed is the denominator of its
// error rate.
type Ledger interface {
	// Record keeps one decision. An action takes one decision; a second
	// fails with [CodeAlreadyDecided]. A decision for an action Limen made
	// no prediction for must carry the action, or it fails with
	// [CodeInvalidDecision] on the field action; an action it carries must
	// be the predicted one, or it fails with [CodeActionConflict].
	Record(ctx context.Context, d Decision) error
	// Answer keeps one answer to an action whose decision is recorded, and
	// fails with [CodeNoDecision] for any other.
	Answer(ctx context.Context, a Answer) error
	// Cases lists one subject's cases; with Pending, the review queue.
	Cases(ctx context.Context, q CaseQuery) (Page, error)
	// Reliability reports one source's error rates for one person over one
	// window.
	Reliability(ctx context.Context, q ReliabilityQuery) (Reliability, error)
}

// Admin is what an organization's operator uses: cost profiles, replays of
// recorded history, and forgetting a person.
type Admin interface {
	// Profile returns the profile that applies to the scope: the most
	// specific saved one, or [DefaultProfile].
	Profile(ctx context.Context, s Scope) (Profile, error)
	// PutProfile validates the profile, derives its thresholds, fills a
	// zero Certify with [DefaultCertify], stores it, and returns it.
	PutProfile(ctx context.Context, p Profile) (Profile, error)
	// Replay predicts recorded actions in time order, each before its own
	// answer is learned, and reports what Limen would have suggested.
	Replay(ctx context.Context, records iter.Seq2[ReplayRecord, error], o ReplayOptions) (ReplayReport, error)
	// Forget deletes everything Limen holds about a subject in an
	// organization: cases, answers, and the model learned from them. It is
	// idempotent.
	Forget(ctx context.Context, org, subject string) error
}

// Service is all of Limen.
type Service interface {
	Predictor
	Ledger
	Admin
	Explainer
}

// FailClosed wraps a predictor so that a decision point never waits on it
// past timeout and never reads a failure as an allow.
//
// When Predict returns an error, or does not return within timeout, the
// wrapper returns, with a nil error, a prediction for the action's key
// whose verdict is [Ask] and whose source is [SourceUnavailable], and hands
// the error to report when report is not nil. A decision point then
// composes it like any suggestion: verdict.Decide narrows the ask by the
// ceiling and shows the action to a person. Because Limen may hold no
// record of the action, the decision point sends the action with its
// [Decision]. A timeout of zero or less sets no bound.
func FailClosed(p Predictor, timeout time.Duration, report func(error)) Predictor {
	return failClosed{p: p, timeout: timeout, report: report}
}

type failClosed struct {
	p       Predictor
	timeout time.Duration
	report  func(error)
}

func (f failClosed) Predict(ctx context.Context, a Action) (Prediction, error) {
	if f.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}
	type result struct {
		p   Prediction
		err error
	}
	done := make(chan result, 1)
	go func() {
		p, err := f.p.Predict(ctx, a)
		done <- result{p, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		r.err = ctx.Err()
	}
	if r.err == nil {
		return r.p, nil
	}
	if f.report != nil {
		f.report(r.err)
	}
	return Prediction{
		Key:     a.Key,
		Verdict: Ask,
		Source:  SourceUnavailable,
		Reason:  "Limen could not give a prediction for this action in time. The action waits for a person.",
	}, nil
}
