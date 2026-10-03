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
	}
	for _, c := range cases {
		if got := reason(c.approve, th, c.answers); got != c.want {
			t.Errorf("reason(%v, %d answers):\n got %q\nwant %q", c.approve, c.answers, got, c.want)
		}
	}
}
