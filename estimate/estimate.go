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
// leaves the estimator unchanged when nothing is missing.
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
	var (
		n, covered         int
		est                limen.Estimate
		errW, revW, shownW float64
		sumW, sumW2        float64
	)
	for _, r := range rows {
		if r.Verdict != target {
			continue
		}
		n++
		if !(r.Review > 0) {
			continue
		}
		covered++
		w := 1 / math.Min(r.Review, 1)
		if r.Shown {
			shownW += w
			if !r.Reviewed {
				est.Unanswered++
			}
		}
		if !r.Shown || !r.Reviewed {
			continue
		}
		est.Reviews++
		if r.Review < 1 {
			est.Audits++
		}
		revW += w
		sumW += w
		sumW2 += w * w
		if (side == FalseAllow && !r.Approve) || (side == FalseBlock && r.Approve) {
			errW += w
		}
	}
	if n > 0 {
		est.Covered = float64(covered) / float64(n)
	}
	if covered == 0 {
		return est
	}
	if shownW > 0 && revW == 0 {
		// Actions were shown but none has its review yet: nothing observed.
		return est
	}
	total := errW
	if revW > 0 {
		total *= shownW / revW
	}
	v := math.Min(total/float64(covered), 1)
	est.Value = &v
	iv := [2]float64{0, 1}
	if sumW2 > 0 {
		iv = wilson(v, sumW*sumW/sumW2)
	}
	est.Interval = &iv
	return est
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
// ends the run. A zero certify takes [limen.DefaultCertify]; an invalid one
// reports a certificate that does not hold.
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
	}
	needed, err := limen.CertificationSize(certify.Alpha, certify.Delta)
	if err != nil {
		return c
	}
	c.Needed = needed
	c.Holds = c.Clean >= needed
	return c
}
