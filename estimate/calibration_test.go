// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"latere.ai/x/limen"
)

// rows returns n rows of predictions with the approval probability stated,
// each with the review probability p, and shown and answered as the
// functions of their index say.
func rows(n int, stated, p float64, shown, approve func(i int) bool) []Row {
	out := make([]Row, n)
	for i := range out {
		s := shown(i)
		out[i] = Row{Verdict: limen.Ask, Stated: stated, Review: p, Shown: s, Reviewed: s, Approve: s && approve(i)}
	}
	return out
}

func always(int) bool { return true }

// below is true for the first n indices.
func below(n int) func(int) bool { return func(i int) bool { return i < n } }

// The bins of a report against values calculated by hand (spec 011,
// criterion 16).
func TestCalibrate(t *testing.T) {
	var in []Row
	// [0.9, 1]: 30 asks, 29 approved; 20 automatic allows under audits at
	// 0.25, 4 drawn and approved; 5 predictions with no decision. The bin
	// counts denials: the one denial at weight 1, over the 50 covered
	// cases, is 0.02, so observed is 0.98. The Kish size is 46^2 / 94.
	in = append(in, rows(30, 0.95, 1, always, func(i int) bool { return i > 0 })...)
	in = append(in, rows(20, 0.95, 0.25, below(4), always)...)
	in = append(in, rows(5, 0.95, 0, func(int) bool { return false }, always)...)
	// [0.1, 0.2): 25 asks, 20 approved. The bin counts approvals: 0.8,
	// stated 0.15, further from 1/2 than 0.8: overconfident. The 5 denials
	// are considered answers.
	low := rows(25, 0.15, 1, always, below(20))
	for i := 20; i < 25; i++ {
		low[i].Considered = true
	}
	in = append(in, low...)
	// [0.5, 0.6): 22 asks, 21 approved: observed 21/22, stated 0.55,
	// nearer to 1/2: underconfident.
	in = append(in, rows(22, 0.55, 1, always, below(21))...)
	// [0.3, 0.4): 19 asks, a Kish size of 19: not estimable.
	in = append(in, rows(19, 0.35, 1, always, always)...)
	slices.Reverse(in)

	q := limen.CalibrationQuery{Org: "o", Subject: "s", From: time.Unix(0, 0), To: time.Unix(1, 0)}
	c := Calibrate(q, in)
	if c.CalibrationQuery != q || len(c.Bins) != limen.CalibrationBins {
		t.Fatalf("%+v", c)
	}
	for i, b := range c.Bins {
		if from, to := limen.CalibrationEdges(i); b.From != from || b.To != to {
			t.Errorf("bin %d: edges %v, %v", i, b.From, b.To)
		}
	}
	high, lo, mid, few := c.Bins[9], c.Bins[1], c.Bins[5], c.Bins[3]

	neff := 46.0 * 46 / 94
	wantIv := wilson(0.02, neff)
	if high.Cases != 55 || high.Covered != 50.0/55 || !near(*high.Stated, 0.95, 1e-12) || high.Reviews != 34 || high.Considered != 0 ||
		!near(*high.Observed, 0.98, 1e-12) || !near(high.Interval[0], 1-wantIv[1], 1e-12) || !near(high.Interval[1], 1-wantIv[0], 1e-12) ||
		high.State != limen.Calibrated {
		t.Errorf("[0.9, 1]: %+v, observed %v, interval %v", high, *high.Observed, *high.Interval)
	}
	if !near(high.Interval[0], 0.822, 1e-3) || !near(high.Interval[1], 0.998, 1e-3) {
		t.Errorf("[0.9, 1]: interval %v, by hand [0.822, 0.998]", *high.Interval)
	}
	if lo.Cases != 25 || lo.Covered != 1 || !near(*lo.Stated, 0.15, 1e-12) || *lo.Observed != 0.8 || lo.Reviews != 25 || lo.Considered != 5 ||
		*lo.Interval != wilson(0.8, 25) || lo.State != limen.Overconfident {
		t.Errorf("[0.1, 0.2): %+v", lo)
	}
	if mid.Cases != 22 || !near(*mid.Observed, 21.0/22, 1e-12) || !near(*mid.Stated, 0.55, 1e-12) || mid.State != limen.Underconfident {
		t.Errorf("[0.5, 0.6): %+v, observed %v", mid, *mid.Observed)
	}
	if few.Cases != 19 || few.Reviews != 19 || !near(*few.Stated, 0.35, 1e-12) || few.Observed != nil || few.Interval != nil || few.State != limen.NotEstimable {
		t.Errorf("[0.3, 0.4): %+v", few)
	}
	for _, i := range []int{0, 2, 4, 6, 7, 8} {
		if b := c.Bins[i]; b.Cases != 0 || b.Covered != 0 || b.Stated != nil || b.Observed != nil || b.State != limen.NotEstimable {
			t.Errorf("empty bin %d: %+v", i, b)
		}
	}
	wantErr := (55*0.03 + 25*0.65 + 22*(21.0/22-0.55)) / (55 + 25 + 22)
	if c.Error == nil || !near(*c.Error, wantErr, 1e-12) {
		t.Errorf("error %v, want %v", c.Error, wantErr)
	}
	if c.Estimable != 102.0/121 {
		t.Errorf("estimable %v, want 102/121", c.Estimable)
	}
}

// A report with nothing to estimate has a null error, which is not zero.
func TestCalibrateNothingEstimable(t *testing.T) {
	c := Calibrate(limen.CalibrationQuery{}, nil)
	if c.Error != nil || c.Estimable != 0 || len(c.Bins) != limen.CalibrationBins {
		t.Errorf("no rows: %+v", c)
	}
	c = Calibrate(limen.CalibrationQuery{}, rows(40, 0.7, 0, func(int) bool { return false }, always))
	if b := c.Bins[7]; c.Error != nil || c.Estimable != 0 || b.Cases != 40 || b.Covered != 0 || b.Stated != nil || b.State != limen.NotEstimable {
		t.Errorf("nothing covered: %+v", c)
	}
	// Automatic allows under audits with none drawn: covered, and nothing
	// observed.
	c = Calibrate(limen.CalibrationQuery{}, rows(40, 0.95, 0.05, func(int) bool { return false }, always))
	if b := c.Bins[9]; b.Covered != 1 || b.Reviews != 0 || b.Observed != nil || b.State != limen.NotEstimable {
		t.Errorf("nothing drawn: %+v", b)
	}
}

// A shown case with no review is missing at random, as in a rate: the
// reviews stand for it, and it adds nothing to the effective size.
func TestCalibrateUnanswered(t *testing.T) {
	in := rows(30, 0.25, 1, always, below(6))
	for i := range 6 {
		in[i].Reviewed, in[i].Approve = false, false
	}
	// 24 reviews of 30 shown cases, none approved: 0, over a Kish size of
	// 24, further from 1/2 than the stated 0.25: underconfident.
	b := Calibrate(limen.CalibrationQuery{}, in).Bins[2]
	if b.Reviews != 24 || b.Observed == nil || *b.Observed != 0 || *b.Interval != wilson(0, 24) || b.State != limen.Underconfident {
		t.Errorf("%+v", b)
	}
	for i := range 6 {
		in[i].Approve = true
	}
	if b := Calibrate(limen.CalibrationQuery{}, in).Bins[2]; *b.Observed != 0 {
		t.Errorf("an approval without a review counts: %+v", b)
	}
}

// A bin of 1/2 or more counts denials and a bin below counts approvals, so
// each estimate is one minus the other's error rate where the decisions
// agree: the observed rate of the bins above the allow threshold is one
// minus the false-allow rate of their allows.
func TestCalibrateIsOneMinusTheFalseAllowRate(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 21))
	for range 100 {
		var in []Row
		for range 200 {
			p := []float64{0, 0.05, 1}[rng.IntN(3)]
			shown := p > 0 && rng.Float64() < p
			in = append(in, Row{Verdict: limen.Allow, Stated: 0.9 + rng.Float64()/10, Review: p, Shown: shown, Reviewed: shown, Approve: rng.Float64() < 0.9})
		}
		b := Calibrate(limen.CalibrationQuery{}, in).Bins[9]
		r := Rate(in, FalseAllow)
		if b.Observed == nil {
			continue
		}
		if !near(*b.Observed, 1-*r.Value, 1e-12) || !near(b.Interval[0], 1-r.Interval[1], 1e-12) || !near(b.Interval[1], 1-r.Interval[0], 1e-12) {
			t.Fatalf("observed %v %v, false-allow rate %v %v", *b.Observed, *b.Interval, *r.Value, *r.Interval)
		}
	}
}

// The weights of answers change only the count of considered reviews
// (spec 011, criterion 15).
func TestCalibrateIgnoresTheWeights(t *testing.T) {
	in := rows(40, 0.95, 1, always, below(38))
	all, none := slices.Clone(in), slices.Clone(in)
	for i := range all {
		all[i].Considered = true
	}
	a, n := Calibrate(limen.CalibrationQuery{}, all), Calibrate(limen.CalibrationQuery{}, none)
	if a.Bins[9].Considered != 40 || n.Bins[9].Considered != 0 {
		t.Fatalf("considered %d and %d", a.Bins[9].Considered, n.Bins[9].Considered)
	}
	a.Bins[9].Considered = 0
	if *a.Error != *n.Error || *a.Bins[9].Observed != *n.Bins[9].Observed || a.Bins[9].State != n.Bins[9].State {
		t.Errorf("the weights moved the report: %+v, %+v", a.Bins[9], n.Bins[9])
	}
}

// simulate is one ledger of n predictions of a model whose stated
// probability is the truth moved by shift: asks in [0.2, 0.8] are always
// shown, and the other predictions are audited at the rate.
func simulate(rng *rand.Rand, n int, rate, shift float64) []Row {
	in := make([]Row, n)
	for i := range in {
		p := rng.Float64()
		truth := min(max(p+shift, 0), 1)
		review := rate
		if p >= 0.2 && p <= 0.8 {
			review = 1
		}
		shown := rng.Float64() < review
		in[i] = Row{Verdict: limen.Ask, Stated: p, Review: review, Shown: shown, Reviewed: shown, Approve: rng.Float64() < truth}
	}
	return in
}

// A calibrated model is calibrated in each estimable bin in about 95% of
// seeded ledgers, and a model that states 0.1 less than the truth is not
// (spec 011, criterion 16). One ledger cannot show it: a 95% interval
// misses one bin in twenty, so a calibrated model has a bin outside its
// interval in about two ledgers of five. Measured: 94.5% of the estimable
// bins, each bin 91% or more, and 26% with the shift.
func TestCalibratedModelIsCalibrated(t *testing.T) {
	const ledgers, n, rate = 400, 4000, 0.1
	share := func(seed uint64, shift float64) ([]float64, float64) {
		rng := rand.New(rand.NewPCG(seed, 31))
		var hits, total [limen.CalibrationBins]int
		for range ledgers {
			for i, b := range Calibrate(limen.CalibrationQuery{}, simulate(rng, n, rate, shift)).Bins {
				if b.State == limen.NotEstimable {
					continue
				}
				total[i]++
				if b.State == limen.Calibrated {
					hits[i]++
				}
			}
		}
		out := make([]float64, len(hits))
		h, tt := 0, 0
		for i := range hits {
			out[i] = float64(hits[i]) / float64(total[i])
			h += hits[i]
			tt += total[i]
		}
		return out, float64(h) / float64(tt)
	}
	bins, all := share(1, 0)
	t.Logf("calibrated: %.3f in all, by bin %.3f", all, bins)
	for i, s := range bins {
		if s < 0.88 {
			t.Errorf("a calibrated model is calibrated in bin %d of %.0f%% of the ledgers", i, 100*s)
		}
	}
	if all < 0.93 || all > 0.97 {
		t.Errorf("a calibrated model is calibrated in %.1f%% of the estimable bins, want about 95%%", 100*all)
	}
	_, off := share(2, 0.1)
	t.Logf("calibrated with a shift of 0.1: %.3f", off)
	if all := off; all > 0.5 {
		t.Errorf("a model that states 0.1 less than the truth is calibrated in %.1f%% of the estimable bins", 100*all)
	}
}

// Counted as the approval rate, the bins of the audited allows would miss
// a calibrated model far more often than 1 in 20: the share of audits
// drawn moves the count of approvals, and a 95% interval for a rate near 1
// does not hold that. This is why a bin of 1/2 or more counts denials.
func TestApprovalsOfAuditsVary(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	const ledgers, n, rate = 2000, 1000, 0.05
	byApprovals, byDenials := 0, 0
	for range ledgers {
		in := make([]Row, n)
		for i := range in {
			shown := rng.Float64() < rate
			in[i] = Row{Stated: 0.95, Review: rate, Shown: shown, Reviewed: shown, Approve: rng.Float64() < 0.95}
		}
		a := thompson(in, func(Row) bool { return true }, func(r Row) bool { return r.Approve })
		if a.value != nil && a.interval[0] <= 0.95 && 0.95 <= a.interval[1] {
			byApprovals++
		}
		if b := Calibrate(limen.CalibrationQuery{}, in).Bins[9]; b.State == limen.Calibrated {
			byDenials++
		}
	}
	a, d := float64(byApprovals)/ledgers, float64(byDenials)/ledgers
	t.Logf("in the interval: approvals %.3f, denials %.3f", a, d)
	if a > 0.8 || d < 0.92 {
		t.Errorf("the truth is in the interval of approvals in %.1f%%, of denials in %.1f%% of the ledgers", 100*a, 100*d)
	}
}

func TestCalibrationOf(t *testing.T) {
	c := Calibrate(limen.CalibrationQuery{}, rows(25, 0.15, 1, always, below(20)))
	for _, p := range []float64{0.1, 0.15, math.Nextafter(0.2, 0)} {
		if got := CalibrationOf(c, p); got != limen.Overconfident {
			t.Errorf("CalibrationOf(%v) = %q", p, got)
		}
	}
	if got := CalibrationOf(c, 0.2); got != limen.NotEstimable {
		t.Errorf("CalibrationOf(0.2) = %q", got)
	}
	if got := CalibrationOf(c, math.NaN()); got != "" {
		t.Errorf("CalibrationOf(NaN) = %q", got)
	}
	if got := CalibrationOf(limen.Calibration{}, 0.5); got != "" {
		t.Errorf("a report with no bins: %q", got)
	}
}
