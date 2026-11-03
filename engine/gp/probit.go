// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package gp

import "math"

const log2pi = 1.8378770664093454835606594728112352797227949472755668 // log(2 pi)

// logPDF is the log standard-normal density, -z^2/2 - log(2 pi)/2.
func logPDF(z float64) float64 { return -0.5*z*z - 0.5*log2pi }

// logCDF is a numerically stable log of the standard-normal distribution
// function, matching scipy's log_ndtr. Computed naively, log Phi(z)
// underflows to -Inf below about z = -37, so three regimes are used:
//
//   - z >= 0:   log1p(-erfc(z/sqrt 2)/2), which avoids cancellation near 1;
//   - z >= -37: log(erfc(-z/sqrt 2)/2), where erfc is accurate and finite;
//   - z < -37:  the asymptotic expansion of the lower tail in log space,
//     Phi(z) = phi(z)/(-z) (1 - 1/z^2 + 3/z^4 - 15/z^6 + ...).
func logCDF(z float64) float64 {
	switch {
	case z >= 0:
		return math.Log1p(-0.5 * math.Erfc(z/math.Sqrt2))
	case z >= -37:
		return math.Log(0.5 * math.Erfc(-z/math.Sqrt2))
	}
	z2 := z * z
	series, term := 1.0, 1.0
	for k := 1; k <= 6; k++ {
		term *= -float64(2*k-1) / z2
		series += term
	}
	return logPDF(z) - math.Log(-z) + math.Log(series)
}

// cdf is the standard-normal distribution function.
func cdf(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

// probit returns, at latent values f with labels y in {-1, +1}, the log
// likelihood sum log Phi(y f), its gradient, and the negative Hessian's
// diagonal W (Rasmussen and Williams, equations 3.15 and 3.16):
//
//	z    = y f
//	grad = y phi(z) / Phi(z)
//	W    = (phi(z)/Phi(z))^2 + z phi(z)/Phi(z)  >= 0
//
// With weights wt, not nil, the log likelihood is the tempered sum
// sum wt log Phi(y f), so each answer's term, its gradient and its W are
// wt times the probit's; W stays zero or more, so the log likelihood stays
// concave. A nil wt is each weight 1, the same arithmetic bit for bit.
//
// The ratio phi/Phi is evaluated as exp(log phi - log Phi), so it stays
// finite where Phi(z) underflows. W is floored at 1e-10, which keeps the
// Newton system positive definite.
func probit(f, y, wt []float64) (sum float64, grad, w []float64) {
	grad = make([]float64, len(f))
	w = make([]float64, len(f))
	for i := range f {
		z := y[i] * f[i]
		lp := logCDF(z)
		r := math.Exp(logPDF(z) - lp)
		g, h := y[i]*r, r*r+z*r
		if wt != nil {
			lp, g, h = wt[i]*lp, wt[i]*g, wt[i]*h
		}
		grad[i] = g
		w[i] = max(h, 1e-10)
		sum += lp
	}
	return sum, grad, w
}
