// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import "latere.ai/x/limen"

// The factors of weights/2 (spec 011): the weight of an approval that one
// gesture gave for many actions, or that was faster than the floor, and
// the factor of each of the rules of a lean and of a long session.
const (
	GradedWeight = 0.2
	GradedFactor = 0.5
)

// Graded gives the weights of each record's answers under weights/2, the
// policy of the fit of learned/2 (spec 011, decision 3): the rules of
// weights/1 give weights between 0 and 1 in place of 0. A standing rule
// still gives 0. One gesture for many, and an answer faster than the
// floor, give [GradedWeight]. An answer that followed a shown lean, and
// one late in a long session, multiply the weight by [GradedFactor] each.
// A denial has weight 1, except from a standing rule, and an approval
// whose details were opened has weight 1. The cover rule keeps weights/1
// ([Policy.Weigh]), and no estimate reads either.
func (p Policy) Graded(recs []Record) []Weights { return p.weigh(recs, p.graded) }

// graded is the weight of one answer of r under weights/2 at its position.
func (p Policy) graded(r Record, a limen.RecordedAnswer, position int) float64 {
	f := p.rules(r, a, position)
	if f.standing {
		return 0
	}
	w := 1.0
	if f.bulk || f.quick {
		w = GradedWeight
	}
	if f.lean {
		w *= GradedFactor
	}
	if f.late {
		w *= GradedFactor
	}
	return w
}
