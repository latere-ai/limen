// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"regexp"
	"testing"

	"latere.ai/x/limen"
	"latere.ai/x/limen/estimate"
)

// A prediction's reason names the approval probability, the threshold
// that decided, and the suggested verdict, in the words of docs/terms.md,
// for an allow and a block. An ask states no probability (spec 011).
func TestReason(t *testing.T) {
	th := limen.Thresholds{AllowAbove: 0.9, BlockBelow: 0.25}
	j := func(approve float64, kind string) estimate.Judged {
		v := th.Verdict(approve)
		if kind != "" {
			v = limen.Ask
		}
		return estimate.Judged{Verdict: v, Kind: kind, Cover: limen.Cover{Weight: 1, Sessions: 1, MinWeight: 3, MinSessions: 2}}
	}
	cases := []struct {
		approve float64
		answers int
		kind    string
		want    string
	}{
		{0.5, 0, limen.AskUnsure, "Limen has no answers from this person yet. It suggests ask."},
		{0.95, 3, "", "The approval probability is 0.95, above the allow threshold (0.90). Limen suggests allow."},
		{0.10, 3, "", "The approval probability is 0.10, below the block threshold (0.25). Limen suggests block."},
		{0.58, 3, limen.AskUnsure, "Limen has few answers from this person about actions like this. Limen suggests ask."},
		{0.58, 3, limen.AskSplit, "The answers of this person about actions like this do not agree. Limen suggests ask."},
		{0.80, 3, limen.AskShort, "The answers of this person about actions like this agree, but they are not enough yet to settle this action. Limen suggests ask."},
		{0.95, 3, limen.AskWider, `This person never allowed an action of this reach: "publish: make public". Limen suggests ask.`},
		{0.95, 3, limen.AskFew, "This person allowed actions of this reach. Limen needs 3 considered approvals from 2 sessions before it suggests allow. Limen suggests ask."},
		// Two decimals would print these equal to the threshold they passed.
		{0.904, 3, "", "The approval probability is 0.904, above the allow threshold (0.900). Limen suggests allow."},
		{0.2496, 3, "", "The approval probability is 0.2496, below the block threshold (0.2500). Limen suggests block."},
	}
	for _, c := range cases {
		if got := reason(c.approve, th, c.answers, j(c.approve, c.kind), "publish: make public"); got != c.want {
			t.Errorf("reason(%v, %d answers, %q):\n got %q\nwant %q", c.approve, c.answers, c.kind, got, c.want)
		}
	}
}

// The reason of an ask holds no probability and no decimal number, for
// each kind of an ask (spec 011, criterion 14).
func TestAskReasonHoldsNoNumber(t *testing.T) {
	decimal := regexp.MustCompile(`\d\.\d|probability`)
	th := limen.Thresholds{AllowAbove: 0.65, BlockBelow: 0.35}
	for _, kind := range limen.AskKinds() {
		for _, answers := range []int{0, 7} {
			r := reason(0.729, th, answers, estimate.Judged{Verdict: limen.Ask, Kind: kind, Cover: limen.Cover{MinWeight: 3, MinSessions: 2}}, "publish: update")
			if decimal.MatchString(r) {
				t.Errorf("%s with %d answers: %q", kind, answers, r)
			}
		}
	}
}

// Probabilities that no number of decimals separates print at the most.
func TestDigitsAtMost(t *testing.T) {
	if d := digits(0.9000001, 0.9); d != 6 {
		t.Errorf("digits = %d, want 6", d)
	}
}
