// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"math"
	"time"
)

// The calibration report (spec 011) puts the approval probabilities Limen
// stated for a person beside the approval rates the person's reviews
// measured, in ten bins of [Prediction.Approve]: [0, 0.1), [0.1, 0.2), and
// so on to [0.9, 1], the last bin closed. A client shows an approval
// probability as a number, or in words that state a probability, only
// where the bin of the prediction is [Calibrated] in the person's report
// for the last [CalibrationWindow], and for a bin above the allow
// threshold only while the certificate of [OfLearned] also holds.
const (
	// CalibrationBins is the number of bins of a report.
	CalibrationBins = 10
	// CalibrationMinEffective is the least Kish effective size of the
	// reviews of a bin for its approval rate to be estimated. Below it the
	// bin is [NotEstimable].
	CalibrationMinEffective = 20
	// CalibrationWindow is the window of the report an explanation reads
	// its [Explanation.Calibration] from: the last 90 days.
	CalibrationWindow = 90 * 24 * time.Hour
)

// The states of a bin of a calibration report.
const (
	// Calibrated is a bin whose stated probability is inside the interval
	// of its observed approval rate, the edges included.
	Calibrated = "calibrated"
	// Overconfident is a bin whose stated probability is outside the
	// interval and further from 1/2 than the observed rate: Limen was surer
	// than the reviews show.
	Overconfident = "overconfident"
	// Underconfident is a bin whose stated probability is outside the
	// interval and not further from 1/2 than the observed rate, an equal
	// distance included.
	Underconfident = "underconfident"
	// NotEstimable is a bin whose reviews have a Kish effective size below
	// [CalibrationMinEffective]: its observed rate is null, which is not
	// zero.
	NotEstimable = "not_estimable"
)

// CalibrationStates lists the states of a bin, in the order of their
// constants.
func CalibrationStates() []string {
	return []string{Calibrated, Overconfident, Underconfident, NotEstimable}
}

// CalibrationBinOf returns the index of the bin of a calibration report
// that holds the approval probability p, from 0 to [CalibrationBins] - 1,
// and -1 for a p outside [0, 1] or NaN. Bin i holds [i/10, (i+1)/10), and
// the last bin also holds 1.
func CalibrationBinOf(p float64) int {
	if !unit(p) {
		return -1
	}
	i := min(int(p*CalibrationBins), CalibrationBins-1)
	// p*10 can round across an edge; the edges are i/10 as float64.
	switch {
	case p < binEdge(i):
		i--
	case i < CalibrationBins-1 && p >= binEdge(i+1):
		i++
	}
	return i
}

// binEdge is the lower edge of bin i, the upper edge of bin i - 1.
func binEdge(i int) float64 { return float64(i) / CalibrationBins }

// CalibrationEdges returns the edges of bin i of a calibration report.
func CalibrationEdges(i int) (from, to float64) { return binEdge(i), binEdge(i + 1) }

// CalibrationState is the state of a bin by its definition: [NotEstimable]
// when the observed rate or the interval is nil, else [Calibrated] when
// stated is in the interval, the edges included, [Overconfident] when
// stated is further from 1/2 than observed, and [Underconfident] in the
// other cases.
func CalibrationState(stated float64, observed *float64, interval *[2]float64) string {
	switch {
	case observed == nil || interval == nil:
		return NotEstimable
	case stated >= interval[0] && stated <= interval[1]:
		return Calibrated
	case math.Abs(stated-0.5) > math.Abs(*observed-0.5):
		return Overconfident
	}
	return Underconfident
}

// CalibrationQuery names whose calibration report to give, over which
// window of receive times.
type CalibrationQuery struct {
	Org     string    `json:"org"`
	Subject string    `json:"subject"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
}

// Validate reports every field of q that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil.
func (q CalibrationQuery) Validate() error {
	var p problems
	if q.Org == "" {
		p.add("org", "is empty")
	}
	if q.Subject == "" {
		p.add("subject", "is empty")
	}
	checkWindow(&p, q.From, q.To)
	return p.err(CodeInvalidQuery)
}

// Calibration is the calibration report of one person over one window: the
// predictions of Limen for the cases first received in the window, in
// [CalibrationBins] bins of their approval probability. A fallback
// prediction of [FailClosed] is no statement of Limen and is in no bin.
type Calibration struct {
	CalibrationQuery
	// Bins has one entry per bin, lowest first. Never nil.
	Bins []CalibrationBin `json:"bins"`
	// Error is the mean distance between Stated and Observed over the bins
	// that are not [NotEstimable], each bin weighted by its Cases; nil when
	// no bin is estimable, which is not zero.
	Error *float64 `json:"error"`
	// Estimable is the share of the cases of the report that are in a bin
	// that is not [NotEstimable]; 0 for a report with no case.
	Estimable float64 `json:"estimable"`
}

// Bin returns the bin of the report that holds the approval probability
// p, and false when no bin does.
func (c Calibration) Bin(p float64) (CalibrationBin, bool) {
	i := CalibrationBinOf(p)
	if i < 0 {
		return CalibrationBin{}, false
	}
	from, _ := CalibrationEdges(i)
	for _, b := range c.Bins {
		if b.From == from {
			return b, true
		}
	}
	return CalibrationBin{}, false
}

// CalibrationBin is one bin of a calibration report.
type CalibrationBin struct {
	// From and To are the edges of the bin: it holds the predictions with
	// From <= approve < To, and the last bin also approve = 1.
	From float64 `json:"from"`
	To   float64 `json:"to"`
	// Cases counts the predictions in the bin, and Covered is the share of
	// them whose decision had a review probability above zero, 0 for a bin
	// with no case.
	Cases   int     `json:"cases"`
	Covered float64 `json:"covered"`
	// Stated is the mean approval probability of the covered cases, nil
	// when the bin has none.
	Stated *float64 `json:"stated"`
	// Observed is the approval rate of the covered cases, estimated from
	// their reviews, each with weight one over its review probability, as
	// a reliability [Estimate] is. It is nil when the bin is
	// [NotEstimable]: null is not zero. Interval is its Wilson 95% interval
	// at the Kish effective size of the reviews, present with Observed.
	Observed *float64    `json:"observed"`
	Interval *[2]float64 `json:"interval,omitempty"`
	// Reviews counts the reviews of the covered cases, and Considered
	// those whose answer has weight 1 under weights/1. No number of the
	// bin reads the weight.
	Reviews    int `json:"reviews"`
	Considered int `json:"considered"`
	// State is [Calibrated], [Overconfident], [Underconfident] or
	// [NotEstimable].
	State string `json:"state"`
}
