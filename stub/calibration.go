// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package stub

import (
	"context"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/estimate"
)

// Calibration implements [limen.Ledger] with latere.ai/x/limen/estimate.
// The window holds the actions first recorded in [From, To).
func (s *Service) Calibration(ctx context.Context, q limen.CalibrationQuery) (limen.Calibration, error) {
	if err := ctx.Err(); err != nil {
		return limen.Calibration{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Calibration{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calibrationLocked(q), nil
}

// calibrationLocked is the calibration report of q over the subject's
// entries. The weights of the answers give each bin its count of
// considered reviews and nothing else.
func (s *Service) calibrationLocked(q limen.CalibrationQuery) limen.Calibration {
	entries := s.subjectLocked(q.Org, q.Subject)
	w := s.policy.Weigh(records(entries))
	var rows []estimate.Row
	for i, e := range entries {
		if e.at.Before(q.From) || !e.at.Before(q.To) {
			continue
		}
		if r, ok := estimate.RowOf(caseOf(e), limen.OfLearned); ok {
			r.Considered = w[i].Review == 1
			rows = append(rows, r)
		}
	}
	return estimate.Calibrate(q, rows)
}

// calibrationOfLocked is the [limen.Explanation.Calibration] of entry e:
// the state of the bin of its recorded prediction in the person's report
// for the last [limen.CalibrationWindow], now included. An entry with no
// prediction has none.
func (s *Service) calibrationOfLocked(e *entry) string {
	if e.prediction == nil {
		return ""
	}
	now := s.clock()
	q := limen.CalibrationQuery{Org: e.action.Org, Subject: e.action.Subject,
		From: now.Add(-limen.CalibrationWindow), To: now.Add(time.Nanosecond)}
	return estimate.CalibrationOf(s.calibrationLocked(q), e.prediction.Approve)
}
