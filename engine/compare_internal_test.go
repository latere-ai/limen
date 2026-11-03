// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
)

// A verdict of learned/2 is looser than one of learned/1 when it is more
// permissive, and outside the cover rule unless it is an allow of a
// covered action: an ask where learned/1 blocks is outside it, because the
// cover rule holds no block.
func TestLooser(t *testing.T) {
	p := func(v limen.Verdict, kind string, covered bool) limen.Prediction {
		return limen.Prediction{Verdict: v, AskKind: kind, Cover: limen.Cover{Covered: covered}}
	}
	for _, c := range []struct {
		base, next      limen.Prediction
		looser, outside bool
	}{
		{p(limen.Block, "", false), p(limen.Ask, limen.AskShort, false), true, true},
		{p(limen.Ask, limen.AskSplit, true), p(limen.Allow, "", true), true, false},
		{p(limen.Ask, limen.AskSplit, false), p(limen.Allow, "", false), true, true},
		{p(limen.Block, "", true), p(limen.Allow, "", true), true, false},
		{p(limen.Allow, "", true), p(limen.Ask, limen.AskShort, true), false, false},
		{p(limen.Ask, limen.AskShort, true), p(limen.Block, "", true), false, false},
		{p(limen.Ask, limen.AskShort, true), p(limen.Ask, limen.AskUnsure, true), false, false},
	} {
		if l, o := looser(c.base, c.next); l != c.looser || o != c.outside {
			t.Errorf("%s to %s: looser %v outside %v, want %v %v", Kind(c.base), Kind(c.next), l, o, c.looser, c.outside)
		}
	}
	if k := Kind(limen.Prediction{Verdict: limen.Ask}); k != "ask" {
		t.Errorf("an ask with no kind: %q", k)
	}
}

// A case whose times are out of order is replayed in a possible order: its
// decision not before its arrival, and its answers not before its
// decision. A case with no decision has no answer to replay.
func TestStepOfKeepsTheOrder(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	c := store.Case{ReceivedAt: t0, DecidedAt: t0.Add(-time.Minute), Action: limen.Action{Org: "o", Subject: "s", ActionID: "a"},
		Decision: &limen.Decision{Verdict: limen.Ask, Source: "m", ReviewProbability: 1},
		Answers:  []limen.RecordedAnswer{{Approve: true, At: t0.Add(-time.Hour)}, {Approve: false, At: t0.Add(time.Hour)}}}
	s := stepOf(c)
	if !s.decidedAt.Equal(t0) || len(s.answerAt) != 2 || !s.answerAt[0].Equal(t0) || !s.answerAt[1].Equal(t0.Add(time.Hour)) {
		t.Errorf("step %+v", s)
	}
	c.Decision = nil
	if s := stepOf(c); s.decision != nil || len(s.answers) != 0 {
		t.Errorf("a case with no decision: %+v", s)
	}
}
