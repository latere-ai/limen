// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"math"
	"math/rand/v2"
	"testing"

	"latere.ai/x/limen"
)

func TestAutomatic(t *testing.T) {
	cases := []struct {
		v      limen.Verdict
		review float64
		want   limen.Verdict
	}{
		{limen.Allow, 0.05, limen.Allow},
		{limen.Flag, 0.05, limen.Allow},
		{limen.Flag, 1, limen.Allow},
		{limen.Block, 0, limen.Block},
		{limen.Ask, 0.05, limen.Block},
		{limen.Ask, 1, ""},
		{"", 0.5, ""},
	}
	for _, c := range cases {
		if got := Automatic(c.v, c.review); got != c.want {
			t.Errorf("Automatic(%q, %v) = %q, want %q", c.v, c.review, got, c.want)
		}
	}
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestRate(t *testing.T) {
	allow := func(p float64, shown, reviewed, approve bool) Row {
		return Row{Verdict: limen.Allow, Review: p, Shown: shown, Reviewed: reviewed, Approve: approve}
	}
	t.Run("not estimable is null, not zero", func(t *testing.T) {
		e := Rate([]Row{allow(0, false, false, false), allow(0, false, false, false)}, FalseAllow)
		if e.Value != nil || e.Interval != nil || e.Covered != 0 {
			t.Errorf("%+v", e)
		}
		if e := Rate(nil, FalseAllow); e.Value != nil || e.Covered != 0 {
			t.Errorf("empty: %+v", e)
		}
	})
	t.Run("weighting", func(t *testing.T) {
		// Twenty automatic allows under 5% audits that were not drawn, one
		// drawn and denied, and one forced review that was approved: the
		// denied audit stands for twenty allows.
		rows := []Row{allow(0.05, true, true, false), allow(1, true, true, true)}
		for range 20 {
			rows = append(rows, allow(0.05, false, false, false))
		}
		e := Rate(rows, FalseAllow)
		if e.Value == nil || !near(*e.Value, 20.0/22, 1e-12) {
			t.Fatalf("value %v, want %v", e.Value, 20.0/22)
		}
		if e.Reviews != 2 || e.Audits != 1 || e.Unanswered != 0 || e.Covered != 1 {
			t.Errorf("%+v", e)
		}
		if e.Interval == nil || e.Interval[0] > *e.Value || e.Interval[1] < *e.Value {
			t.Errorf("interval %v does not contain %v", e.Interval, *e.Value)
		}
	})
	t.Run("covered", func(t *testing.T) {
		rows := []Row{allow(1, true, true, true), allow(0, false, false, false), allow(0, false, false, false), allow(0.5, false, false, false)}
		e := Rate(rows, FalseAllow)
		if e.Covered != 0.5 || e.Value == nil || *e.Value != 0 {
			t.Errorf("%+v", e)
		}
	})
	t.Run("unanswered", func(t *testing.T) {
		rows := []Row{allow(1, true, false, false), allow(1, true, true, false)}
		e := Rate(rows, FalseAllow)
		if e.Unanswered != 1 || e.Reviews != 1 {
			t.Fatalf("%+v", e)
		}
		// Missing at random: the one review stands for both shown actions.
		if e.Value == nil || *e.Value != 1 {
			t.Errorf("value %v, want 1", e.Value)
		}
		if e := Rate([]Row{allow(1, true, false, false)}, FalseAllow); e.Value != nil || e.Unanswered != 1 {
			t.Errorf("only unanswered: %+v", e)
		}
	})
	t.Run("nothing drawn is zero with an open interval", func(t *testing.T) {
		e := Rate([]Row{allow(0.05, false, false, false)}, FalseAllow)
		if e.Value == nil || *e.Value != 0 || *e.Interval != [2]float64{0, 1} {
			t.Errorf("%+v", e)
		}
	})
	t.Run("false block counts approvals of blocks", func(t *testing.T) {
		rows := []Row{
			{Verdict: limen.Block, Review: 1, Shown: true, Reviewed: true, Approve: true},
			{Verdict: limen.Block, Review: 1, Shown: true, Reviewed: true, Approve: false},
			allow(1, true, true, false),
		}
		e := Rate(rows, FalseBlock)
		if e.Value == nil || *e.Value != 0.5 || e.Reviews != 2 {
			t.Errorf("%+v", e)
		}
	})
	t.Run("clamped to one", func(t *testing.T) {
		rows := []Row{allow(0.05, true, true, false), allow(0.05, false, false, false)}
		if e := Rate(rows, FalseAllow); e.Value == nil || *e.Value != 1 {
			t.Errorf("value %v, want 1", e.Value)
		}
	})
}

// Over many draws, the estimate's mean is the realized false-allow rate
// among covered allows, with audits at 5% mixed with forced reviews.
func TestRateIsUnbiased(t *testing.T) {
	const (
		n     = 400
		rate  = 0.05
		seeds = 4000
	)
	rng := rand.New(rand.NewPCG(1, 2))
	// The realized errors are fixed; only the review draws vary.
	errs := make([]bool, n)
	forced := make([]bool, n)
	truth := 0.0
	for i := range n {
		errs[i] = rng.Float64() < 0.1
		forced[i] = rng.Float64() < 0.2
		if errs[i] {
			truth++
		}
	}
	truth /= n
	sum := 0.0
	for range seeds {
		rows := make([]Row, n)
		for i := range n {
			p := rate
			if forced[i] {
				p = 1
			}
			shown := rng.Float64() < p
			rows[i] = Row{Verdict: limen.Allow, Review: p, Shown: shown, Reviewed: shown, Approve: !errs[i]}
		}
		e := Rate(rows, FalseAllow)
		sum += *e.Value
	}
	if mean := sum / seeds; !near(mean, truth, 0.004) {
		t.Errorf("mean %v, truth %v", mean, truth)
	}
}

// The interval covers the truth in about 95% of draws.
func TestIntervalCoverage(t *testing.T) {
	const n, rate, seeds = 2000, 0.1, 1000
	rng := rand.New(rand.NewPCG(3, 4))
	errs := make([]bool, n)
	truth := 0.0
	for i := range n {
		errs[i] = rng.Float64() < 0.08
		if errs[i] {
			truth++
		}
	}
	truth /= n
	hits := 0
	for range seeds {
		rows := make([]Row, n)
		for i := range n {
			shown := rng.Float64() < rate
			rows[i] = Row{Verdict: limen.Allow, Review: rate, Shown: shown, Reviewed: shown, Approve: !errs[i]}
		}
		iv := Rate(rows, FalseAllow).Interval
		if iv[0] <= truth && truth <= iv[1] {
			hits++
		}
	}
	if c := float64(hits) / seeds; c < 0.92 {
		t.Errorf("coverage %v", c)
	}
}

func TestCertificate(t *testing.T) {
	audit := func(approve bool) Row {
		return Row{Verdict: limen.Allow, Review: 0.05, Shown: true, Reviewed: true, Approve: approve}
	}
	rows := []Row{audit(true), audit(false)}
	for range 12 {
		rows = append(rows, audit(true))
	}
	// Forced reviews, blocks and unreviewed allows do not count either way.
	rows = append(rows,
		Row{Verdict: limen.Allow, Review: 1, Shown: true, Reviewed: true, Approve: false},
		Row{Verdict: limen.Block, Review: 0.05, Shown: true, Reviewed: true, Approve: false},
		Row{Verdict: limen.Allow, Review: 0.05})
	c := Certificate(rows, limen.Certify{Alpha: 0.25, Delta: 0.05})
	if c.Clean != 12 || c.Needed != 12 || !c.Holds {
		t.Errorf("%+v", c)
	}
	c = Certificate(rows, limen.Certify{})
	if c.Alpha != 0.01 || c.Needed != 300 || c.Holds {
		t.Errorf("default: %+v", c)
	}
	c = Certificate(rows, limen.Certify{Alpha: 2, Delta: 0.05})
	if c.Holds || c.Needed != 0 || c.Clean != 12 {
		t.Errorf("invalid: %+v", c)
	}
}

// The default claim, a false-allow rate below 1% at 95% confidence, holds
// on 300 clean audits and not on 299 (spec 008, criterion 6; paper,
// Proposition 5(b)). A denial before the run does not count against it.
func TestCertificateAt300(t *testing.T) {
	audit := func(approve bool) Row {
		return Row{Verdict: limen.Allow, Review: 0.05, Shown: true, Reviewed: true, Approve: approve}
	}
	rows := []Row{audit(false)}
	for range 299 {
		rows = append(rows, audit(true))
	}
	if c := Certificate(rows, limen.DefaultCertify); c.Clean != 299 || c.Needed != 300 || c.Holds {
		t.Errorf("299 clean audits: %+v", c)
	}
	rows = append(rows, audit(true))
	if c := Certificate(rows, limen.DefaultCertify); c.Clean != 300 || c.Needed != 300 || !c.Holds {
		t.Errorf("300 clean audits: %+v", c)
	}
	rows = append(rows, audit(false))
	if c := Certificate(rows, limen.DefaultCertify); c.Clean != 0 || c.Holds {
		t.Errorf("a denial after 300 clean audits: %+v", c)
	}
}

func TestWilson(t *testing.T) {
	iv := wilson(0.5, 100)
	if !near(iv[0], 0.4038, 1e-3) || !near(iv[1], 0.5962, 1e-3) {
		t.Errorf("wilson(0.5, 100) = %v", iv)
	}
	if iv := wilson(0, 10); iv[0] != 0 || iv[1] <= 0 {
		t.Errorf("wilson(0, 10) = %v", iv)
	}
}
