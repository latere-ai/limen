// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package gp is Limen's Gaussian-process probit classifier: the posterior
// over a person's latent tolerance f, learned from their approve and deny
// answers, and the approval probability it predicts for a new action.
//
// The likelihood is Pr(approve | f) = Phi(f) (the paper, Ou, "Learning When
// to Ask", arXiv:2605.19151, Definition 1). [FitWeighted] tempers it with a
// weight w_i in [0, 1] for each answer, log p(y | f) = sum_i w_i log
// Phi(y_i f_i) (spec 011, learned/2): the gradient and the curvature of
// answer i are w_i times those of the probit, so the log likelihood stays
// concave and Newton's method converges as before. The posterior is approximated by
// Laplace's method: [Fit] finds the posterior mode by Newton's method in the
// numerically stable form of Rasmussen and Williams, Gaussian Processes for
// Machine Learning (2006), Algorithm 3.1, and [Posterior.Predict] follows
// their Algorithm 3.2: the latent mean mu = k_*^T grad log p(y | f_hat), the
// variance sigma^2 = k_** - v^T v with v = L^{-1} (W^{1/2} k_*), and the
// approval probability Phi(mu / sqrt(1 + sigma^2)), which is exact under the
// Gaussian approximation. L is the Cholesky factor of
// B = I + W^{1/2} K W^{1/2}.
//
// A [Posterior] never changes after Fit returns it. Predictions read it
// from any number of goroutines while the next posterior is being fitted,
// and a caller publishes the new one by swapping a pointer. A prediction
// costs one kernel evaluation and one multiply-add per training point for
// the mean, and a triangular solve, about n^2/2 multiply-adds, for the
// variance; a fit costs a Cholesky factorization of an n by n matrix per
// Newton step. Fit keeps the most recent [DefaultMaxLabels] answers, which
// bounds both. The same inputs give bitwise the same posterior.
//
// The package is a port of the research implementation,
// github.com/changkun/trustcalib at tag v3 (go/gp, Apache-2.0; the Go code
// is no longer on that repository's main branch), itself a port of the
// paper's Python reference, and reproduces the reference's golden fixtures:
// the fitted mode, the gradient, the log marginal likelihood, and the
// predictions.
package gp

import (
	"errors"
	"math"

	"gonum.org/v1/gonum/mat"

	"latere.ai/x/limen/engine/kernel"
)

// DefaultMaxLabels is the number of most recent answers [Fit] keeps when
// [Options.MaxLabels] is zero. At 512 a fit factorizes 512 by 512 matrices
// and a posterior holds about 1 MiB.
const DefaultMaxLabels = 512

// Options tune a fit. The zero value is the default of every field.
type Options struct {
	// Jitter is added to the kernel matrix's diagonal; zero is 1e-6.
	Jitter float64
	// MaxIter bounds the Newton iterations; zero or less is 100.
	MaxIter int
	// Tol stops the iterations once the objective changes by less; zero is
	// 1e-6.
	Tol float64
	// MaxLabels keeps the most recent answers, the last ones in the order
	// given; zero is DefaultMaxLabels and a negative value keeps them all.
	MaxLabels int
}

func (o Options) withDefaults() Options {
	if o.Jitter == 0 {
		o.Jitter = 1e-6
	}
	if o.MaxIter <= 0 {
		o.MaxIter = 100
	}
	if o.Tol == 0 {
		o.Tol = 1e-6
	}
	if o.MaxLabels == 0 {
		o.MaxLabels = DefaultMaxLabels
	}
	return o
}

// Posterior is a fitted, immutable Laplace posterior.
type Posterior struct {
	k     kernel.Kernel
	train []kernel.Point
	grad  []float64 // grad log p(y | f_hat), with the answers' weights, the weights of the mean
	sW    []float64 // W^{1/2} at the mode
	l     []float64 // lower Cholesky factor of B, row-major n by n
	fHat  []float64
	logZ  float64
}

// Prediction is the posterior predictive at one point.
type Prediction struct {
	// Mean and Var are the latent mean and variance.
	Mean float64
	Var  float64
	// Approve is Phi(Mean / sqrt(1 + Var)), the probability of approval.
	Approve float64
}

// Prior returns the posterior of a person who has answered nothing: mean
// zero, the kernel's prior variance, and an approval probability of one
// half everywhere.
func Prior(k kernel.Kernel) *Posterior { return &Posterior{k: k} }

// Fit returns the Laplace posterior given points and their answers, in
// arrival order. It keeps the most recent o.MaxLabels of them. With no
// answers it returns [Prior]. It is [FitWeighted] with each weight 1.
func Fit(k kernel.Kernel, points []kernel.Point, approve []bool, o Options) (*Posterior, error) {
	return FitWeighted(k, points, approve, nil, o)
}

// FitWeighted returns the Laplace posterior of the tempered probit: each
// answer's log likelihood times its weight, sum_i w_i log Phi(y_i f_i).
// weights holds one weight in [0, 1] for each answer, or is nil for each
// weight 1, which is [Fit] bit for bit. It keeps the most recent o.MaxLabels
// answers first, and then leaves out each answer of weight 0, which adds
// nothing to the likelihood: the posterior is the one without that answer,
// and the window of the answers it reads is the one an unweighted fit reads.
// With no answer of weight above 0 it returns [Prior].
func FitWeighted(k kernel.Kernel, points []kernel.Point, approve []bool, weights []float64, o Options) (*Posterior, error) {
	if len(points) != len(approve) {
		return nil, errors.New("gp: the number of answers differs from the number of points")
	}
	if weights != nil && len(weights) != len(points) {
		return nil, errors.New("gp: the number of weights differs from the number of points")
	}
	for _, w := range weights {
		if !(w >= 0 && w <= 1) {
			return nil, errors.New("gp: a weight is not in [0, 1]")
		}
	}
	if err := k.Validate(); err != nil {
		return nil, err
	}
	o = o.withDefaults()
	if o.MaxLabels > 0 && len(points) > o.MaxLabels {
		points = points[len(points)-o.MaxLabels:]
		approve = approve[len(approve)-o.MaxLabels:]
		if weights != nil {
			weights = weights[len(weights)-o.MaxLabels:]
		}
	}
	if weights != nil {
		var (
			kept []kernel.Point
			ans  []bool
			wts  []float64
		)
		for i, w := range weights {
			if w > 0 {
				kept, ans, wts = append(kept, points[i]), append(ans, approve[i]), append(wts, w)
			}
		}
		points, approve, weights = kept, ans, wts
	}
	if len(points) == 0 {
		return Prior(k), nil
	}
	return fit(k, clone(points), approve, weights, o)
}

// fit is Rasmussen and Williams' Algorithm 3.1 with labels in {-1, +1},
// and with the weights of the answers in the likelihood when wts is not
// nil.
func fit(k kernel.Kernel, train []kernel.Point, approve []bool, wts []float64, o Options) (*Posterior, error) {
	n := len(train)
	y := make([]float64, n)
	for i, a := range approve {
		y[i] = -1
		if a {
			y[i] = 1
		}
	}
	K := mat.NewSymDense(n, nil)
	for i := range n {
		for j := i; j < n; j++ {
			K.SetSym(i, j, k.Cov(train[i], train[j]))
		}
		K.SetSym(i, i, K.At(i, i)+o.Jitter)
	}

	f := make([]float64, n)
	a := make([]float64, n)
	var chol mat.Cholesky
	last := math.Inf(-1)
	for range o.MaxIter {
		ll, grad, w := probit(f, y, wts)
		sW := sqrtAll(w)
		if !factorize(&chol, K, sW) {
			return nil, errors.New("gp: B is not positive definite; a point or an answer is not finite")
		}
		b := make([]float64, n)
		for i := range n {
			b[i] = w[i]*f[i] + grad[i]
		}
		// a = b - W^{1/2} B^{-1} (W^{1/2} K b), and f = K a.
		a = newton(&chol, K, b, sW)
		f = mulVec(K, a)
		obj := -0.5*dot(a, f) + ll
		if math.Abs(obj-last) < o.Tol {
			break
		}
		last = obj
	}

	ll, grad, w := probit(f, y, wts)
	sW := sqrtAll(w)
	if !factorize(&chol, K, sW) {
		return nil, errors.New("gp: B is not positive definite at the mode")
	}
	var L mat.TriDense
	chol.LTo(&L)
	l := make([]float64, n*n)
	for i := range n {
		for j := 0; j <= i; j++ {
			l[i*n+j] = L.At(i, j)
		}
	}
	return &Posterior{
		k: k, train: train, grad: grad, sW: sW, l: l, fHat: f,
		// Rasmussen and Williams (3.32): log Z = -a^T f / 2 + log p(y | f)
		// - sum log diag(L), and sum log diag(L) = log det(B) / 2.
		logZ: -0.5*dot(a, f) + ll - 0.5*chol.LogDet(),
	}, nil
}

// Predict returns the posterior predictive at q.
func (p *Posterior) Predict(q kernel.Point) Prediction {
	n := len(p.train)
	prior := p.k.Var()
	if n == 0 {
		return Prediction{Mean: 0, Var: prior, Approve: 0.5}
	}
	// x = W^{1/2} k_*, then forward substitution L v = x in place.
	mean := 0.0
	v := make([]float64, n)
	for i, t := range p.train {
		ks := p.k.Cov(t, q)
		mean += ks * p.grad[i]
		v[i] = p.sW[i] * ks
	}
	reduction := 0.0
	for i := range n {
		row := p.l[i*n : i*n+i]
		s := v[i]
		for j, lij := range row {
			s -= lij * v[j]
		}
		v[i] = s / p.l[i*n+i]
		reduction += v[i] * v[i]
	}
	variance := max(prior-reduction, 1e-12)
	return Prediction{Mean: mean, Var: variance, Approve: cdf(mean / math.Sqrt(1+variance))}
}

// Explanation is a prediction taken apart in the model's own terms
// (spec 009). The mean is a weighted sum over the fitted answers,
// k(x_i, q) times the gradient of the log likelihood at the mode, so each
// answer's term is its exact share of the mean. Under [FitWeighted] the
// gradient carries the answer's weight, so the terms still sum to the
// mean.
type Explanation struct {
	Prediction
	// Prior is k(q, q), the variance before any answer, and EvidenceRatio
	// 1 - Var / Prior, the share of it the answers removed.
	Prior         float64
	EvidenceRatio float64
	// Points are the fitted answers' points, in the order they were fitted,
	// and Contributions each one's term of Mean; they sum to Mean.
	Points        []kernel.Point
	Contributions []float64
	// Parts split Mean by the kernel's terms when it is [kernel.Parted],
	// and is nil otherwise; they sum to Mean.
	Parts []float64
}

// Explain returns the prediction at q with its explanation.
func (p *Posterior) Explain(q kernel.Point) Explanation {
	pr := p.Predict(q)
	prior := p.k.Var()
	e := Explanation{Prediction: pr, Prior: prior, EvidenceRatio: 1 - pr.Var/prior, Points: clone(p.train)}
	e.Contributions = make([]float64, len(p.train))
	parted, ok := p.k.(kernel.Parted)
	if ok {
		e.Parts = make([]float64, 3)
	}
	for i, t := range p.train {
		e.Contributions[i] = p.k.Cov(t, q) * p.grad[i]
		if ok {
			for j, part := range parted.Parts(t, q) {
				e.Parts[j] += part * p.grad[i]
			}
		}
	}
	return e
}

// PredictAll returns the posterior predictive at each point.
func (p *Posterior) PredictAll(qs []kernel.Point) []Prediction {
	out := make([]Prediction, len(qs))
	for i, q := range qs {
		out[i] = p.Predict(q)
	}
	return out
}

// LogMarginal is the Laplace approximation of the log marginal likelihood
// of the answers, zero for the prior. After [FitWeighted] it is the one of
// the tempered likelihood.
func (p *Posterior) LogMarginal() float64 { return p.logZ }

// Len is the number of answers the posterior was fitted on.
func (p *Posterior) Len() int { return len(p.train) }

// Kernel is the kernel the posterior was fitted with.
func (p *Posterior) Kernel() kernel.Kernel { return p.k }

// factorize builds B = I + W^{1/2} K W^{1/2}, positive definite by
// construction for finite inputs, and factorizes it.
func factorize(chol *mat.Cholesky, K *mat.SymDense, sW []float64) bool {
	n := len(sW)
	B := mat.NewSymDense(n, nil)
	for i := range n {
		for j := i; j < n; j++ {
			v := sW[i] * K.At(i, j) * sW[j]
			if i == j {
				v++
			}
			B.SetSym(i, j, v)
		}
	}
	return chol.Factorize(B)
}

// newton returns a = b - W^{1/2} B^{-1} (W^{1/2} (K b)). The solve cannot
// fail after a successful factorization.
func newton(chol *mat.Cholesky, K *mat.SymDense, b, sW []float64) []float64 {
	kb := mulVec(K, b)
	rhs := mat.NewVecDense(len(b), nil)
	for i := range b {
		rhs.SetVec(i, sW[i]*kb[i])
	}
	var x mat.VecDense
	_ = chol.SolveVecTo(&x, rhs)
	a := make([]float64, len(b))
	for i := range b {
		a[i] = b[i] - sW[i]*x.AtVec(i)
	}
	return a
}

func mulVec(K *mat.SymDense, v []float64) []float64 {
	var out mat.VecDense
	out.MulVec(K, mat.NewVecDense(len(v), v))
	res := make([]float64, len(v))
	for i := range res {
		res[i] = out.AtVec(i)
	}
	return res
}

func sqrtAll(w []float64) []float64 {
	out := make([]float64, len(w))
	for i, v := range w {
		out[i] = math.Sqrt(v)
	}
	return out
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// clone deep-copies points, so a caller's later writes cannot reach a
// posterior.
func clone(points []kernel.Point) []kernel.Point {
	out := make([]kernel.Point, len(points))
	for i, p := range points {
		out[i] = kernel.Point{Tool: append([]float64(nil), p.Tool...), Ctx: append([]float64(nil), p.Ctx...), T: p.T}
	}
	return out
}
