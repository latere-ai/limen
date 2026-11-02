// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"latere.ai/x/limen"
)

// A prediction's reason names the approval probability, the threshold
// that decided, and the suggested verdict, in the words of docs/terms.md.
func TestReason(t *testing.T) {
	th := limen.Thresholds{AllowAbove: 0.9, BlockBelow: 0.25}
	cases := []struct {
		approve float64
		answers int
		want    string
	}{
		{0.5, 0, "Limen has no answers from this person yet. It suggests ask."},
		{0.95, 3, "The approval probability is 0.95, above the allow threshold (0.90). Limen suggests allow."},
		{0.10, 3, "The approval probability is 0.10, below the block threshold (0.25). Limen suggests block."},
		{0.58, 3, "The approval probability is 0.58, between the block threshold (0.25) and the allow threshold (0.90). Limen suggests ask."},
		// Two decimals would print these equal to the threshold they passed.
		{0.904, 3, "The approval probability is 0.904, above the allow threshold (0.900). Limen suggests allow."},
		{0.2496, 3, "The approval probability is 0.2496, below the block threshold (0.2500). Limen suggests block."},
		{0.9, 3, "The approval probability is 0.90, between the block threshold (0.25) and the allow threshold (0.90). Limen suggests ask."},
	}
	for _, c := range cases {
		if got := reason(c.approve, th, c.answers); got != c.want {
			t.Errorf("reason(%v, %d answers):\n got %q\nwant %q", c.approve, c.answers, got, c.want)
		}
	}
}

// Probabilities that no number of decimals separates print at the most.
func TestDigitsAtMost(t *testing.T) {
	if d := digits(0.9000001, 0.9); d != 6 {
		t.Errorf("digits = %d, want 6", d)
	}
}

// The reason of an allow the answer minimum held names the approvals and
// the sessions counted and the minimum they miss, in the words of
// docs/terms.md, for each part of the minimum an operator can turn off.
func TestHeldReason(t *testing.T) {
	th := limen.Thresholds{AllowAbove: 0.65, BlockBelow: 0.35}
	const head = "The approval probability is 0.73, above the allow threshold (0.65). "
	cases := []struct {
		m       limen.Minimum
		counted bool
		want    string
	}{
		{limen.Minimum{Allows: 1, Sessions: 1, MinAllows: 3, MinSessions: 2}, true,
			"The person approved actions with this label 1 time, in 1 session. Limen needs 3 approvals or more, in 2 sessions or more, before it suggests allow. Limen suggests ask."},
		{limen.Minimum{Allows: 4, Sessions: 1, MinAllows: 3, MinSessions: 2}, true,
			"The person approved actions with this label 4 times, in 1 session. Limen needs 3 approvals or more, in 2 sessions or more, before it suggests allow. Limen suggests ask."},
		{limen.Minimum{MinAllows: 1, MinSessions: 2}, true,
			"The person has not approved an action with this label. Limen needs 1 approval or more, in 2 sessions or more, before it suggests allow. Limen suggests ask."},
		{limen.Minimum{Allows: 2, Sessions: 2, MinAllows: 3}, true,
			"The person approved actions with this label 2 times, in 2 sessions. Limen needs 3 approvals or more before it suggests allow. Limen suggests ask."},
		{limen.Minimum{Allows: 5, Sessions: 1, MinSessions: 2}, true,
			"The person approved actions with this label 5 times, in 1 session. Limen needs approvals in 2 sessions or more before it suggests allow. Limen suggests ask."},
		{limen.Minimum{MinAllows: 3, MinSessions: 2}, false,
			"Limen could not count the approvals of this person for this label. Limen suggests ask."},
	}
	for _, c := range cases {
		if got := heldReason(0.729, th, c.m, c.counted); got != head+c.want {
			t.Errorf("heldReason(%+v, %v):\n got %q\nwant %q", c.m, c.counted, got, head+c.want)
		}
	}
}
