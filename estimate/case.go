// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import "latere.ai/x/cribrum"

// ReviewOf returns the case's review, the first answer to an action its
// decision showed a person, or nil when it has none.
func ReviewOf(c cribrum.Case) *cribrum.RecordedAnswer {
	for i := range c.Answers {
		if c.Answers[i].Review {
			return &c.Answers[i]
		}
	}
	return nil
}

// Pending reports whether the case waits for a review: its decision showed
// the action to a person, and nobody has answered yet.
func Pending(c cribrum.Case) bool {
	return c.Decision != nil && c.Decision.Verdict.Shown() && ReviewOf(c) == nil
}

// RowOf is the case's row for the source named by of, and false when that
// source gave no verdict on the case. Every ledger builds its rows here, so
// two implementations report the same estimates for the same cases.
//
// The source's verdict is the prediction's for [cribrum.OfLearned] (a
// fallback prediction is no suggestion and gives no row), [Automatic] of the
// decision for [cribrum.OfApplied] and for a decision source, and the
// signal's for a signal source, where a flag counts as an allow because the
// action ran.
func RowOf(c cribrum.Case, of string) (Row, bool) {
	var r Row
	if c.Decision != nil {
		r.Review = c.Decision.ReviewProbability
		r.Shown = c.Decision.Verdict.Shown()
		if rv := ReviewOf(c); rv != nil {
			r.Reviewed, r.Approve = true, rv.Approve
		}
	}
	switch {
	case of == cribrum.OfLearned:
		if c.Prediction == nil || c.Prediction.Source == cribrum.SourceUnavailable {
			return r, false
		}
		r.Verdict = c.Prediction.Verdict
	case of == cribrum.OfApplied || (c.Decision != nil && c.Decision.Source == of):
		if c.Decision == nil {
			return r, false
		}
		r.Verdict = Automatic(c.Decision.Verdict, c.Decision.ReviewProbability)
	default:
		for _, s := range c.Action.Signals {
			if s.Source == of && s.Verdict != "" {
				r.Verdict = s.Verdict
				if r.Verdict == cribrum.Flag {
					r.Verdict = cribrum.Allow
				}
			}
		}
	}
	return r, r.Verdict != ""
}
