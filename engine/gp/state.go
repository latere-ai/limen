// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package gp

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"latere.ai/x/limen/engine/kernel"
)

// State is everything a fitted posterior holds except its kernel, for a
// store to keep and [Restore] to rebuild: loading a stored state is a read,
// where a fit is a factorization. The kernel is not part of it, because a
// model's kernel is fixed by its version and a state restored under a
// different kernel would predict nonsense.
type State struct {
	Train []kernel.Point
	Grad  []float64 // the weights of the mean, grad log p(y | f_hat)
	SW    []float64 // W^{1/2} at the mode
	L     []float64 // lower Cholesky factor of B, row-major n by n
	FHat  []float64
	LogZ  float64
}

// State returns a deep copy of the posterior's state.
func (p *Posterior) State() State {
	return State{
		Train: clone(p.train),
		Grad:  slices.Clone(p.grad),
		SW:    slices.Clone(p.sW),
		L:     slices.Clone(p.l),
		FHat:  slices.Clone(p.fHat),
		LogZ:  p.logZ,
	}
}

// Restore rebuilds a posterior from a state and the kernel it was fitted
// with. It refuses a state whose parts disagree in size or hold a value that
// is not finite, so a damaged store fails loudly rather than predicting.
func Restore(k kernel.Kernel, s State) (*Posterior, error) {
	if err := k.Validate(); err != nil {
		return nil, err
	}
	n := len(s.Train)
	if len(s.Grad) != n || len(s.SW) != n || len(s.FHat) != n || len(s.L) != n*n {
		return nil, fmt.Errorf("gp: state sizes disagree: %d points, %d weights, %d curvatures, %d modes, %d factor entries",
			n, len(s.Grad), len(s.SW), len(s.FHat), len(s.L))
	}
	for _, v := range [][]float64{s.Grad, s.SW, s.L, s.FHat, {s.LogZ}} {
		for _, x := range v {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, errors.New("gp: state holds a value that is not finite")
			}
		}
	}
	for i := range n {
		if !(s.L[i*n+i] > 0) {
			return nil, fmt.Errorf("gp: state factor has a non-positive diagonal at %d", i)
		}
	}
	if n == 0 {
		return Prior(k), nil
	}
	return &Posterior{
		k: k, train: clone(s.Train), grad: slices.Clone(s.Grad), sW: slices.Clone(s.SW),
		l: slices.Clone(s.L), fHat: slices.Clone(s.FHat), logZ: s.LogZ,
	}, nil
}
