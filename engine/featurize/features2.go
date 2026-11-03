// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"math"
	"slices"

	"latere.ai/x/limen"
)

// Version2 names features/2 (spec 011): features/1 with the scope of the
// action in place of three of its groups. The model of learned/2 reads it.
const Version2 = "features/2"

// The widths of the two blocks of features/2.
const (
	DimA2 = 63
	DimC2 = 61
)

// The offsets of the groups of A in features/2.
const (
	a2Effect  = 0  // 4, as features/1
	a2Undo    = 4  // 3: a thermometer over reversible, recoverable, irreversible
	a2Kind    = 7  // 8, as features/1
	a2Tokens  = 15 // 32, as features/1
	a2Classes = 47 // 8, as features/1
	a2Actor   = 55 // 3, as features/1
	a2Crosses = 58 // 5: machine, remote, network, organization, public
)

// The offsets of the groups of C in features/2.
const (
	c2Sensitivity = 0  // 1, as features/1
	c2TargetKind  = 1  // 8, as features/1
	c2Class       = 9  // 3: a thermometer over own, shared, primary
	c2Env         = 12 // 4, as features/1
	c2Credential  = 16 // 1, as features/1
	c2Holds       = 17 // 3: egress, protected_refs, checkpoints
	c2Contact     = 20 // 1
	c2Attributes  = 21 // 16, as features/1
	c2Signals     = 37 // 24, as features/1
)

// The encodings of the scope. A step of a thermometer of three is
// sqrt(2/3), so its least and greatest values are at squared distance 2. A
// boundary and contact are one dimension each, at 0, sqrt(2)/2 and
// sqrt(2), so yes and no are at squared distance 2. Unknown is half of
// each step, between the least and the greatest value.
var (
	step = math.Sqrt(2.0 / 3)
	half = math.Sqrt2 / 2
)

// Of2 is features/2 of the action a, from f, the features/1 vectors of a,
// and s, the scope of a. Every group of features/2 is a group of
// features/1, a dimension of the scope, or the holds of the environment,
// so Of2 reads no input: a stored case, whose input is gone, gets the
// vectors it got when it was new, from the features/1 vectors and the
// scope it keeps. When f is not a features/1 vector, Of2 featurizes the
// action again with [Of]. Of2 never fails.
//
// features/2 drops the irreversibility group of A, which undo holds, and
// the destructive and outside-workspace groups of C, which undo and
// crosses.machine hold. Each new group meets the scaling rule of
// features/1: two actions that differ only in that group are at squared
// distance 2 or less. Each boundary of crosses is its own group, so a
// boundary that one action crosses and the other does not adds 2.
func Of2(a limen.Action, f Features, s limen.Reach) Features {
	if len(f.A) != DimA || len(f.C) != DimC {
		b := a
		b.Input = nil
		f = Of(b)
	}
	A := make([]float64, 0, DimA2)
	A = append(A, f.A[aEffect:aIrrev]...)
	A = thermometer(A, limen.Undos(), s.Undo)
	A = append(A, f.A[aKind:DimA]...)
	for _, b := range []limen.Bound{s.Crosses.Machine, s.Crosses.Remote, s.Crosses.Network, s.Crosses.Organization, s.Crosses.Public} {
		A = append(A, boundary(b))
	}

	C := make([]float64, 0, DimC2)
	C = append(C, f.C[cSensitivity:cEnv]...)
	C = thermometer(C, limen.TargetClasses(), s.TargetClass)
	C = append(C, f.C[cEnv:cDestructive]...)
	for _, h := range limen.Holds() {
		v := 0.0
		if a.Environment.Held(h) {
			v = step
		}
		C = append(C, v)
	}
	switch limen.Contacts().Word(s.Contact) {
	case limen.ContactRepeat:
		C = append(C, 0)
	case limen.ContactFirst:
		C = append(C, math.Sqrt2)
	default:
		C = append(C, half)
	}
	C = append(C, f.C[cAttributes:DimC]...)
	return Features{A: A, C: C}
}

// thermometer appends the encoding of w in the order o of four values: the
// first i of three steps for the value of rank i, and half of each step
// for unknown, an empty word, or a word outside o.
func thermometer(v []float64, o limen.Order, w string) []float64 {
	r := slices.Index(o, w)
	for i := range len(o) - 1 {
		switch {
		case r < 0:
			v = append(v, step/2)
		case i < r:
			v = append(v, step)
		default:
			v = append(v, 0)
		}
	}
	return v
}

// boundary is the encoding of one boundary of crosses.
func boundary(b limen.Bound) float64 {
	switch b {
	case limen.BoundNo:
		return 0
	case limen.BoundYes:
		return math.Sqrt2
	}
	return half
}
