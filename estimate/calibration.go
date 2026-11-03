// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"math"

	"latere.ai/x/limen"
)

// Calibrate is the calibration report of spec 011 over the rows of
// [limen.OfLearned] in the window of q: each row goes to the bin of its
// Stated approval probability, and each bin compares the mean Stated of its
// covered rows with the approval rate their reviews measured. Every ledger
// builds its report here, so two of them report the same bins for the
// same cases.
//
// The observed rate of a bin is the Horvitz-Thompson estimate of [Rate]
// over the covered rows of the bin, each review with weight 1/p and no
// other weight. A bin below 1/2 estimates the share of approvals, as the
// false-block rate counts the approvals of blocks. A bin of 1/2 or more
// estimates the share of denials, as the false-allow rate counts the
// denials of allows, and observes one minus it: the observed rate of the
// bins above the allow threshold is one minus the false-allow rate of
// their allows. Both are unbiased (Proposition 8 of the research). Each
// counts the event that is rare where a calibrated model puts the bin, so
// its variance is near the binomial variance that the Wilson interval at
// the Kish effective size assumes when the bin's reviews are audits.
//
// A bin whose reviews have a Kish effective size below
// [limen.CalibrationMinEffective] is [limen.NotEstimable], with a nil
// Observed. Error and Estimable read the other bins only.
func Calibrate(q limen.CalibrationQuery, rows []Row) limen.Calibration {
	c := limen.Calibration{CalibrationQuery: q, Bins: make([]limen.CalibrationBin, limen.CalibrationBins)}
	in := make([][]Row, limen.CalibrationBins)
	for _, r := range rows {
		if i := limen.CalibrationBinOf(r.Stated); i >= 0 {
			in[i] = append(in[i], r)
		}
	}
	var cases, estimable int
	var distance float64
	for i := range c.Bins {
		b := calibrationBin(i, in[i])
		c.Bins[i] = b
		cases += b.Cases
		if b.State != limen.NotEstimable {
			estimable += b.Cases
			distance += float64(b.Cases) * math.Abs(*b.Stated-*b.Observed)
		}
	}
	if estimable > 0 {
		e := distance / float64(estimable)
		c.Error = &e
	}
	if cases > 0 {
		c.Estimable = float64(estimable) / float64(cases)
	}
	return c
}

// calibrationBin is bin i of a report, from the rows whose Stated it holds.
func calibrationBin(i int, rows []Row) limen.CalibrationBin {
	b := limen.CalibrationBin{Cases: len(rows)}
	b.From, b.To = limen.CalibrationEdges(i)
	upper := b.From >= 0.5
	every := func(Row) bool { return true }
	// The event is an approval below 1/2 and a denial from 1/2 on.
	h := thompson(rows, every, func(r Row) bool { return r.Approve != upper })
	b.Reviews, b.Considered = h.reviews, h.considered
	var stated float64
	if h.covered > 0 {
		b.Covered = float64(h.covered) / float64(h.n)
		for _, r := range rows {
			if r.Review > 0 {
				stated += r.Stated
			}
		}
		stated /= float64(h.covered)
		b.Stated = &stated
	}
	if h.value != nil && h.effective >= limen.CalibrationMinEffective {
		v, iv := *h.value, *h.interval
		if upper {
			v, iv = 1-v, [2]float64{1 - iv[1], 1 - iv[0]}
		}
		b.Observed, b.Interval = &v, &iv
	}
	b.State = limen.CalibrationState(stated, b.Observed, b.Interval)
	return b
}

// CalibrationOf is the state of the bin of approve in the report, the
// [limen.Explanation.Calibration] of a prediction with that approval
// probability.
func CalibrationOf(c limen.Calibration, approve float64) string {
	b, ok := c.Bin(approve)
	if !ok {
		return ""
	}
	return b.State
}
