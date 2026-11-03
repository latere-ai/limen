// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/estimate"
)

// Calibration implements [limen.Ledger] with latere.ai/x/limen/estimate.
// The window holds the cases first received in [From, To).
func (e *Engine) Calibration(ctx context.Context, q limen.CalibrationQuery) (limen.Calibration, error) {
	if err := ctx.Err(); err != nil {
		return limen.Calibration{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Calibration{}, err
	}
	cases, err := e.st.Cases(ctx, q.Org, q.Subject)
	if err != nil {
		return limen.Calibration{}, err
	}
	return calibration(q, cases, e.policy.Weigh(records(cases))), nil
}

// calibration is the calibration report of q over the subject's cases,
// with w the weights of their answers, which give each bin its count of
// considered reviews and nothing else. The row of the learned source
// reads the prediction, the decision and the answers of a case only, so
// the case is not copied.
func calibration(q limen.CalibrationQuery, cases []store.Case, w []estimate.Weights) limen.Calibration {
	var rows []estimate.Row
	for i, c := range cases {
		if c.ReceivedAt.Before(q.From) || !c.ReceivedAt.Before(q.To) {
			continue
		}
		if r, ok := estimate.RowOf(limen.Case{Prediction: c.Prediction, Decision: c.Decision, Answers: c.Answers}, limen.OfLearned); ok {
			r.Considered = w[i].Review == 1
			rows = append(rows, r)
		}
	}
	return estimate.Calibrate(q, rows)
}

// calibrationOf is the [limen.Explanation.Calibration] of case c: the
// state of the bin of its recorded prediction in the person's report for
// the last [limen.CalibrationWindow], now included, read from the
// person's cases and their records, which the explanation has in hand. A
// case with no prediction has none.
func (e *Engine) calibrationOf(c store.Case, cases []store.Case, recs []estimate.Record) string {
	if c.Prediction == nil || c.Prediction.Source == limen.SourceUnavailable {
		return ""
	}
	now := e.clock()
	q := limen.CalibrationQuery{Org: c.Action.Org, Subject: c.Action.Subject,
		From: now.Add(-limen.CalibrationWindow), To: now.Add(time.Nanosecond)}
	return estimate.CalibrationOf(calibration(q, cases, e.policy.Weigh(recs)), c.Prediction.Approve)
}
