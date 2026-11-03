// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package gp

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"latere.ai/x/limen/engine/kernel"
)

// The tempered probit of learned/2 (spec 011, criterion 19). Its gradient
// and its curvature W match central differences of the weighted log
// likelihood sum w log Phi(y f), at random latent values and weights.
func TestWeightedProbitDerivatives(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 19))
	const eps = 1e-5
	for trial := range 20 {
		n := 1 + rng.IntN(8)
		f, y, wt := make([]float64, n), make([]float64, n), make([]float64, n)
		for i := range n {
			f[i] = -4 + 8*rng.Float64()
			y[i] = []float64{-1, 1}[rng.IntN(2)]
			wt[i] = rng.Float64()
		}
		sum, grad, w := probit(f, y, wt)
		ll := func(f []float64) float64 { s, _, _ := probit(f, y, wt); return s }
		g := func(f []float64, i int) float64 { _, gr, _ := probit(f, y, wt); return gr[i] }
		want := 0.0
		for i := range n {
			want += wt[i] * logCDF(y[i]*f[i])
		}
		if math.Abs(sum-want) > 1e-12 {
			t.Errorf("trial %d: log likelihood %v, want %v", trial, sum, want)
		}
		for i := range n {
			fp, fm := slices.Clone(f), slices.Clone(f)
			fp[i] += eps
			fm[i] -= eps
			if d := (ll(fp) - ll(fm)) / (2 * eps); math.Abs(d-grad[i]) > 1e-6 {
				t.Errorf("trial %d: grad[%d] = %.10g, central difference %.10g", trial, i, grad[i], d)
			}
			// W is the negative second derivative, floored at 1e-10.
			if d := -(g(fp, i) - g(fm, i)) / (2 * eps); math.Abs(max(d, 1e-10)-w[i]) > 1e-6 {
				t.Errorf("trial %d: W[%d] = %.10g, central difference %.10g", trial, i, w[i], d)
			}
			if w[i] < 1e-10 {
				t.Errorf("trial %d: W[%d] = %v, below its floor", trial, i, w[i])
			}
			// The cross derivatives are zero: an answer's term reads its
			// own latent value alone.
			for j := range n {
				if j == i {
					continue
				}
				if d := (g(fp, j) - g(fm, j)) / (2 * eps); d != 0 {
					t.Errorf("trial %d: d grad[%d] / d f[%d] = %v, want 0", trial, j, i, d)
				}
			}
		}
	}
}

// With each weight 1, the weighted fit is the fit of learned/1 bit for bit,
// so it gives the golden fixtures of the research within the same
// tolerance (criterion 19).
func TestWeightsOfOneAreTheGolden(t *testing.T) {
	for _, name := range []string{"laplace_fit.json", "laplace_fit_additive.json"} {
		var fix struct {
			Kernel      kernelJSON `json:"kernel"`
			Train       pointsJSON `json:"train"`
			Y01         []int      `json:"y01"`
			FHat        []float64  `json:"f_hat"`
			Grad        []float64  `json:"grad"`
			LogMarginal float64    `json:"log_marginal"`
		}
		load(t, name, &fix)
		ones := make([]float64, len(fix.Y01))
		for i := range ones {
			ones[i] = 1
		}
		p, err := FitWeighted(fix.Kernel.kernel(), fix.Train.points(), approve(fix.Y01), ones, Options{})
		if err != nil {
			t.Fatal(err)
		}
		closeTo(t, name+" f_hat", p.fHat, fix.FHat, 1e-9)
		closeTo(t, name+" grad", p.grad, fix.Grad, 1e-9)
		closeTo(t, name+" log_marginal", []float64{p.LogMarginal()}, []float64{fix.LogMarginal}, 1e-9)
		plain := must(Fit(fix.Kernel.kernel(), fix.Train.points(), approve(fix.Y01), Options{}))
		if !slices.Equal(p.fHat, plain.fHat) || !slices.Equal(p.grad, plain.grad) || !slices.Equal(p.l, plain.l) ||
			p.LogMarginal() != plain.LogMarginal() {
			t.Errorf("%s: weights of 1 differ from the unweighted fit", name)
		}
	}
}

// randWeights are n weights in [0, 1], one in five of them 0.
func randWeights(rng *rand.Rand, n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		if rng.IntN(5) > 0 {
			w[i] = 0.05 + 0.95*rng.Float64()
		}
	}
	return w
}

// At the mode of the tempered posterior, f_hat = K grad with the weighted
// gradient, and the contributions of an explanation sum to the mean within
// 1e-9 (criterion 19).
func TestWeightedModeAndExplanation(t *testing.T) {
	for seed := range uint64(8) {
		rng := rand.New(rand.NewPCG(seed+40, seed))
		pts, ys := randTrain(rng, 10+int(seed)*9)
		wt := randWeights(rng, len(pts))
		k := kernel.DefaultAdditive()
		p := must(FitWeighted(k, pts, ys, wt, Options{}))
		resid := 0.0
		for i, a := range p.train {
			kg := 0.0
			for j, b := range p.train {
				c := k.Cov(a, b)
				if i == j {
					c += 1e-6
				}
				kg += c * p.grad[j]
			}
			resid += (p.fHat[i] - kg) * (p.fHat[i] - kg)
		}
		if math.Sqrt(resid) > 1e-8 {
			t.Errorf("seed %d: stationarity residual %.3g", seed, math.Sqrt(resid))
		}
		for range 4 {
			q, _ := randTrain(rng, 1)
			q[0].T = float64(len(pts)) + rng.Float64()*20
			e := p.Explain(q[0])
			sum := 0.0
			for _, c := range e.Contributions {
				sum += c
			}
			if math.Abs(sum-e.Mean) > 1e-9 || math.Abs(e.Parts[0]+e.Parts[1]+e.Parts[2]-e.Mean) > 1e-9 {
				t.Errorf("seed %d: contributions %v and parts %v, mean %v", seed, sum, e.Parts, e.Mean)
			}
		}
	}
}

// An answer of weight 0 changes nothing: the posterior is bit for bit the
// one without that answer (criterion 19). Each weight 0 is the prior.
func TestWeightZeroChangesNothing(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 23))
	pts, ys := randTrain(rng, 30)
	wt := randWeights(rng, len(pts))
	wt[0], wt[17] = 0, 0
	k := kernel.DefaultAdditive()
	with := must(FitWeighted(k, pts, ys, wt, Options{}))
	var (
		kept []kernel.Point
		ans  []bool
		wts  []float64
	)
	for i := range pts {
		if wt[i] > 0 {
			kept, ans, wts = append(kept, pts[i]), append(ans, ys[i]), append(wts, wt[i])
		}
	}
	without := must(FitWeighted(k, kept, ans, wts, Options{}))
	if !slices.Equal(with.fHat, without.fHat) || !slices.Equal(with.l, without.l) || with.Len() != len(kept) ||
		with.LogMarginal() != without.LogMarginal() {
		t.Error("an answer of weight 0 changed the posterior")
	}
	q := kernel.Point{Tool: pts[0].Tool, Ctx: pts[0].Ctx, T: 40}
	if with.Predict(q) != without.Predict(q) {
		t.Error("an answer of weight 0 changed a prediction")
	}
	zeros := make([]float64, len(pts))
	if p := must(FitWeighted(k, pts, ys, zeros, Options{})); p.Len() != 0 || p.Predict(q).Approve != 0.5 {
		t.Errorf("each weight 0: %d answers fitted", p.Len())
	}
}

// The cap of the window comes before the answers of weight 0 leave the
// fit, so the window is the one of an unweighted fit: the most recent
// answers, whatever their weights.
func TestWeightedWindow(t *testing.T) {
	rng := rand.New(rand.NewPCG(29, 29))
	pts, ys := randTrain(rng, 20)
	wt := randWeights(rng, len(pts))
	wt[15] = 0
	k := kernel.DefaultAdditive()
	capped := must(FitWeighted(k, pts, ys, wt, Options{MaxLabels: 10}))
	last := must(FitWeighted(k, pts[10:], ys[10:], wt[10:], Options{MaxLabels: -1}))
	nonzero := 0
	for _, w := range wt[10:] {
		if w > 0 {
			nonzero++
		}
	}
	if capped.Len() != nonzero || nonzero == 10 || !slices.Equal(capped.fHat, last.fHat) {
		t.Errorf("capped fit of %d answers differs from the last ten less the %d of weight 0", capped.Len(), 10-nonzero)
	}
}

// A greater weight on an approval moves the approval probability at its
// action further from one half: a graded weight is between no answer and
// a full answer.
func TestWeightIsGraded(t *testing.T) {
	a := kernel.Point{Tool: []float64{1, 0}, Ctx: []float64{0}, T: 0}
	q := a
	q.T = 1
	k := kernel.DefaultAdditive()
	last := 0.5
	for _, w := range []float64{0, 0.05, 0.2, 0.5, 1} {
		p := must(FitWeighted(k, []kernel.Point{a}, []bool{true}, []float64{w}, Options{})).Predict(q).Approve
		if w > 0 && !(p > last) {
			t.Errorf("weight %v: %v, not above %v", w, p, last)
		}
		last = p
	}
	// One full approval is the measurement of spec 011: 0.729.
	if math.Abs(last-0.729) > 5e-4 {
		t.Errorf("one approval of weight 1: %v, want 0.729", last)
	}
}

func TestFitWeightedErrors(t *testing.T) {
	k := kernel.DefaultAdditive()
	one := []kernel.Point{{Tool: []float64{0}, Ctx: []float64{0}}}
	for _, w := range [][]float64{{}, {0.5, 0.5}, {-0.1}, {1.5}, {math.NaN()}, {math.Inf(1)}} {
		if _, err := FitWeighted(k, one, []bool{true}, w, Options{}); err == nil {
			t.Errorf("weights %v were accepted", w)
		}
	}
}
