// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package kernel holds the covariance functions of Limen's Gaussian-process
// model of a person's tolerance, over feature points (tool block, context
// block, time). It is a port of the kernels of the research implementation,
// github.com/changkun/trustcalib at tag v3 (go/kernel, Apache-2.0), reworked
// to evaluate one pair of points at a time with the standard library alone,
// and it reproduces that implementation's golden fixtures.
//
// The model is the latent tolerance of the paper (Ou, "Learning When to Ask",
// arXiv:2605.19151, Definition 1),
//
//	f(x, t) = tau(t) - r(x) + h(x, t),
//
// where r is the static risk of an action, tau the person's tolerance at
// time t, and h a small interaction. [Additive] mirrors that decomposition
// term by term,
//
//	k((x, t), (x', t')) = SStatic * k_x(x, x')                   static risk r
//	                    + SGlobal * k_t(t, t')                   shared tolerance tau
//	                    + SInter  * k_x(x, x') * k_t(t, t')      interaction h
//
// with k_x = k_tool * k_ctx, two squared-exponential kernels
// exp(-d^2 / (2 l^2)) over the tool and context blocks, and
// k_t = exp(-|t - t'| / Lambda), the Ornstein-Uhlenbeck covariance. Only the
// time-coupled terms decay, so what the model learned about how risky an
// action is persists while the person's tolerance is free to drift
// (Proposition 4(c)). [Product], the separable SIGMA2 * k_x * k_t, makes
// everything decay at rate 1/Lambda, so every action an answer has not
// touched for a few Lambda returns to the prior and is asked about again
// (Proposition 4(a, b)); it is kept as the baseline the paper compares
// against.
//
// Time is measured in decisions: T is the count of the person's decisions
// when the action arrived, which Limen assigns, and Lambda = 90 is the
// research's setting in those units. Every block has unit self-similarity,
// and sums and Schur products of positive semidefinite kernels are
// positive semidefinite, so both kernels are covariance functions whose
// prior variance [Kernel.Var] is the same at every point.
package kernel

import (
	"errors"
	"math"
)

// Point is one action as the model sees it: the tool block (what is done),
// the context block (to what, where, for which task), and the time T in
// decisions. Points compared by a kernel have blocks of equal length; an
// entry missing from the shorter block counts as zero.
type Point struct {
	Tool []float64
	Ctx  []float64
	T    float64
}

// Kernel is a covariance function over points.
type Kernel interface {
	// Cov returns k(a, b).
	Cov(a, b Point) float64
	// Var returns k(x, x), the prior variance, which is the same for every
	// point.
	Var() float64
	// Validate reports a hyperparameter that is not a positive finite
	// number (a scale may be zero).
	Validate() error
}

// Parted is a kernel that is a sum of terms: Parts returns each term of
// k(a, b), in a fixed order, summing to Cov(a, b). A posterior's mean splits
// into the same terms (spec 009).
type Parted interface {
	Kernel
	Parts(a, b Point) [3]float64
}

var (
	_ Kernel = Product{}
	_ Parted = Additive{}
)

// Product is the separable kernel Sigma2 * k_tool * k_ctx * k_time.
type Product struct {
	Sigma2 float64 // prior variance
	LTool  float64 // lengthscale of the tool block
	LCtx   float64 // lengthscale of the context block
	Lambda float64 // forgetting scale, in decisions
}

// DefaultProduct is the product kernel of the paper's experiments.
func DefaultProduct() Product {
	return Product{Sigma2: 1.6, LTool: 1.1, LCtx: 1.2, Lambda: 90}
}

// Cov returns Sigma2 * k_x(a, b) * k_t(a.T, b.T).
func (k Product) Cov(a, b Point) float64 {
	return k.Sigma2 * Static(a, b, k.LTool, k.LCtx) * Time(a.T, b.T, k.Lambda)
}

// Var returns Sigma2.
func (k Product) Var() float64 { return k.Sigma2 }

// Validate reports a hyperparameter out of range.
func (k Product) Validate() error {
	return check(scale(k.Sigma2) && k.Sigma2 > 0, length(k.LTool), length(k.LCtx), length(k.Lambda))
}

// Additive is the kernel SStatic * k_x + SGlobal * k_time +
// SInter * k_x * k_time, whose static term never decays.
type Additive struct {
	SStatic float64 // scale of the static action risk
	SGlobal float64 // scale of the tolerance every action shares
	SInter  float64 // scale of the interaction that drifts per action
	LTool   float64 // lengthscale of the tool block
	LCtx    float64 // lengthscale of the context block
	Lambda  float64 // forgetting scale, in decisions
}

// DefaultAdditive is the additive kernel of the paper, fixed before any of
// its runs: scales 1.6, 1.0 and 0.6, lengthscales 1.1 and 1.2, and Lambda 90
// decisions.
func DefaultAdditive() Additive {
	return Additive{SStatic: 1.6, SGlobal: 1.0, SInter: 0.6, LTool: 1.1, LCtx: 1.2, Lambda: 90}
}

// Cov returns SStatic*k_x + SGlobal*k_t + SInter*k_x*k_t.
func (k Additive) Cov(a, b Point) float64 {
	kx := Static(a, b, k.LTool, k.LCtx)
	kt := Time(a.T, b.T, k.Lambda)
	return k.SStatic*kx + k.SGlobal*kt + k.SInter*kx*kt
}

// StaticCov returns the static term alone, SStatic * k_x(a, b): what the
// additive kernel keeps of two points' covariance however far apart in
// time they are.
func (k Additive) StaticCov(a, b Point) float64 {
	return k.SStatic * Static(a, b, k.LTool, k.LCtx)
}

// Parts returns the three terms of Cov in its order: the static term,
// SStatic * k_x, what is known about this kind of action at any time; the
// shared tolerance, SGlobal * k_t, which every answer moves whatever its
// action; and the interaction, SInter * k_x * k_t.
func (k Additive) Parts(a, b Point) [3]float64 {
	kx := Static(a, b, k.LTool, k.LCtx)
	kt := Time(a.T, b.T, k.Lambda)
	return [3]float64{k.SStatic * kx, k.SGlobal * kt, k.SInter * kx * kt}
}

// Var returns SStatic + SGlobal + SInter.
func (k Additive) Var() float64 { return k.SStatic + k.SGlobal + k.SInter }

// Validate reports a hyperparameter out of range.
func (k Additive) Validate() error {
	return check(scale(k.SStatic), scale(k.SGlobal), scale(k.SInter), k.Var() > 0,
		length(k.LTool), length(k.LCtx), length(k.Lambda))
}

// Static is k_tool(a, b) * k_ctx(a, b), the similarity of two actions
// regardless of time.
func Static(a, b Point, lTool, lCtx float64) float64 {
	return math.Exp(-sqdist(a.Tool, b.Tool)/(2*lTool*lTool) - sqdist(a.Ctx, b.Ctx)/(2*lCtx*lCtx))
}

// Time is the Ornstein-Uhlenbeck covariance exp(-|t - u| / lambda).
func Time(t, u, lambda float64) float64 {
	return math.Exp(-math.Abs(t-u) / lambda)
}

// sqdist is the squared Euclidean distance, an entry missing from the
// shorter vector counting as zero.
func sqdist(a, b []float64) float64 {
	if len(a) < len(b) {
		a, b = b, a
	}
	s := 0.0
	for i, x := range a {
		d := x
		if i < len(b) {
			d -= b[i]
		}
		s += d * d
	}
	return s
}

var errHyper = errors.New("kernel: a scale is negative or not finite, a lengthscale is not positive and finite, or every scale is zero")

func check(ok ...bool) error {
	for _, o := range ok {
		if !o {
			return errHyper
		}
	}
	return nil
}

func scale(x float64) bool  { return x >= 0 && !math.IsInf(x, 0) }
func length(x float64) bool { return x > 0 && !math.IsInf(x, 0) }
