// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package estimate computes the error rates a reliability report carries,
// from the ledger's rows for one source, one subject and one window. It is
// pure arithmetic over [Row] values, shared by every implementation of the
// ledger so that two of them report the same numbers for the same rows.
//
// The estimator is Horvitz-Thompson over the rows the decisions covered:
// each review counts with weight 1/p, p the review probability the
// decision recorded, and the weighted errors are divided by the number of
// covered rows. When every shown action has its review, this is unbiased
// for the realized error rate among covered rows (Proposition 8 of the
// research). A shown action with no review yet is treated as missing at
// random: the reviewed weights are scaled up to the shown weights, which
// leaves the estimator unchanged when nothing is missing. The calibration
// report of spec 011 reads the same estimate in each bin of the stated
// approval probability ([Calibrate]).
package estimate

import (
	"math"
	"slices"

	"latere.ai/x/limen"
)

// Side selects which error a [Rate] estimates.
type Side int

const (
	// FalseAllow is the share of the source's allows the person would have
	// denied.
	FalseAllow Side = iota
	// FalseBlock is the share of the source's blocks the person would have
	// approved.
	FalseBlock
)

// Row is one action in a window as a reliability report sees it.
type Row struct {
	// Verdict is the source's verdict on the action: what a prediction or a
	// signal said, or, for a decision source, [Automatic] of the decision.
	Verdict limen.Verdict
	// Review is the review probability the action's decision recorded; zero
	// when the action has no decision.
	Review float64
	// Shown is true when the decision showed the action to a person.
	Shown bool
	// Reviewed is true when the action has its review, the first answer to
	// a shown action; Approve is that answer.
	Reviewed bool
	Approve  bool
	// Considered is true when the review has weight 1 under weights/1
	// (spec 011). Only the certificate's count of considered audits reads
	// it: no rate and no claim depends on the weight of an answer.
	Considered bool
	// Stated is the approval probability of the source's prediction, for
	// [limen.OfLearned]; only the calibration report reads it.
	Stated float64
}

// Automatic maps a recorded decision to the automatic verdict it stands
// for: an allow or a flag ran without a person deciding first, so both are
// [limen.Allow]; a block, and an ask drawn from a block for review (review
// probability below one), are [limen.Block]; an ask with review probability
// one was a person's decision from the start and is no automatic verdict,
// so it returns the empty verdict.
func Automatic(v limen.Verdict, review float64) limen.Verdict {
	switch {
	case v == limen.Allow || v == limen.Flag:
		return limen.Allow
	case v == limen.Block || (v == limen.Ask && review < 1):
		return limen.Block
	}
	return ""
}

// z is the two-sided 95% normal quantile.
const z = 1.959963984540054

// Rate estimates one error rate from the rows.
//
// The rows on the side's verdict ([limen.Allow] for [FalseAllow],
// [limen.Block] for [FalseBlock]) are the population. Covered rows are
// those with a positive review probability; the value is nil when there are
// none, because nothing could have been observed. Otherwise the value is
// the Horvitz-Thompson estimate of the errors among covered rows, adjusted
// for unanswered reviews as described in the package documentation,
// divided by the number of covered rows, and clamped to [0, 1]: a single
// estimate from a few heavily weighted reviews can exceed one, and a rate
// above one is never what a reader should see.
//
// The interval is Wilson's score interval at 95% around the estimate, with
// the Kish effective sample size n = (sum w)^2 / sum w^2 of the reviews'
// weights standing in for the number of trials, so a review at probability
// 0.05 counts for less than twenty reviews at probability one. With no
// review the interval is [0, 1].
func Rate(rows []Row, side Side) limen.Estimate {
	target := limen.Allow
	if side == FalseBlock {
		target = limen.Block
	}
	// An error is a denial of an allow, or an approval of a block.
	h := thompson(rows, func(r Row) bool { return r.Verdict == target }, func(r Row) bool { return r.Approve == (side == FalseBlock) })
	est := limen.Estimate{Value: h.value, Interval: h.interval, Reviews: h.reviews, Audits: h.audits, Unanswered: h.unanswered}
	if h.n > 0 {
		est.Covered = float64(h.covered) / float64(h.n)
	}
	return est
}

// horvitz is one Horvitz-Thompson estimate and the counts of the rows it
// rests on.
type horvitz struct {
	// n counts the rows in the population and covered those with a
	// positive review probability.
	n, covered int
	// reviews counts the reviewed covered rows, audits those with a review
	// probability below one, considered those whose review is considered,
	// and unanswered the shown covered rows with no review.
	reviews, audits, considered, unanswered int
	// effective is the Kish effective size of the reviews' weights, 0 with
	// no review.
	effective float64
	// value is the estimated share of the covered rows with the event, and
	// nil when nothing could be observed; interval is its Wilson 95%
	// interval at the effective size, [0, 1] with no review.
	value    *float64
	interval *[2]float64
}

// thompson is the Horvitz-Thompson estimate, over the rows in reports, of
// the share of covered rows with event, as [Rate] describes it. Every
// estimate of the package is this one, so a rate and a calibration report
// weigh a review alike.
func thompson(rows []Row, in, event func(Row) bool) horvitz {
	var (
		h                    horvitz
		eventW, revW, shownW float64
		sumW, sumW2          float64
	)
	for _, r := range rows {
		if !in(r) {
			continue
		}
		h.n++
		if !(r.Review > 0) {
			continue
		}
		h.covered++
		w := 1 / math.Min(r.Review, 1)
		if r.Shown {
			shownW += w
			if !r.Reviewed {
				h.unanswered++
			}
		}
		if !r.Shown || !r.Reviewed {
			continue
		}
		h.reviews++
		if r.Review < 1 {
			h.audits++
		}
		if r.Considered {
			h.considered++
		}
		revW += w
		sumW += w
		sumW2 += w * w
		if event(r) {
			eventW += w
		}
	}
	if sumW2 > 0 {
		h.effective = sumW * sumW / sumW2
	}
	if h.covered == 0 {
		return h
	}
	if shownW > 0 && revW == 0 {
		// Actions were shown but none has its review yet: nothing observed.
		return h
	}
	total := eventW
	if revW > 0 {
		total *= shownW / revW
	}
	v := math.Min(total/float64(h.covered), 1)
	h.value = &v
	iv := [2]float64{0, 1}
	if sumW2 > 0 {
		iv = wilson(v, h.effective)
	}
	h.interval = &iv
	return h
}

// wilson is the 95% Wilson score interval for a proportion p observed over
// n trials, clamped to [0, 1].
func wilson(p, n float64) [2]float64 {
	z2 := z * z
	d := 1 + z2/n
	c := (p + z2/(2*n)) / d
	h := z / d * math.Sqrt(p*(1-p)/n+z2/(4*n*n))
	return [2]float64{math.Max(0, c-h), math.Min(1, c+h)}
}

// Certificate tests the claim certify against rows in chronological order:
// the most recent run of clean audited allows, reviews with probability
// below one on the source's allows that the person approved. A denied one
// ends the run. Considered counts the audits of the run whose answer is
// considered; the claim does not read it. A zero certify takes
// [limen.DefaultCertify]; an invalid one reports a certificate that does
// not hold.
func Certificate(rows []Row, certify limen.Certify) limen.Certificate {
	if certify == (limen.Certify{}) {
		certify = limen.DefaultCertify
	}
	c := limen.Certificate{Alpha: certify.Alpha, Delta: certify.Delta}
	for _, r := range slices.Backward(rows) {
		if r.Verdict != limen.Allow || !r.Shown || !r.Reviewed || !(r.Review > 0 && r.Review < 1) {
			continue
		}
		if !r.Approve {
			break
		}
		c.Clean++
		if r.Considered {
			c.Considered++
		}
	}
	needed, err := limen.CertificationSize(certify.Alpha, certify.Delta)
	if err != nil {
		return c
	}
	c.Needed = needed
	c.Holds = c.Clean >= needed
	return c
}
