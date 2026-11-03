// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"latere.ai/x/limen"
)

// Spec 009, criterion 10: each entry of what the model learned carries the
// evidence ratio and the kind of an ask that an explanation at the entry's
// point carries, the label's latest answered case at the person's current
// time.
func TestLearnedEntriesAreExplanations(t *testing.T) {
	ctx := context.Background()
	e := New(Options{Inline: 1})
	t.Cleanup(func() { e.Close() })
	n := 0
	ask := func(id, cmd string, approve bool) {
		in, _ := json.Marshal(map[string]string{"command": cmd})
		n++
		a := limen.Action{Org: "o", Subject: "s", ActionID: id, Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in,
			Actor: &limen.Actor{Kind: "agent", Session: fmt.Sprint(n / 2 % 2)}}
		if _, err := e.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual"}); err != nil {
			t.Fatal(err)
		}
		if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve, DetailsOpened: new(true)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 30 {
		ask(fmt.Sprintf("t-%d", i), "go test ./...", true)
		ask(fmt.Sprintf("m-%d", i), "make release", i%2 == 0)
	}
	ask("once", "curl https://example.com", false)

	l, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.model(ctx, subjectKey{"o", "s"})
	if err != nil {
		t.Fatal(err)
	}
	cases, err := e.st.Cases(ctx, "o", "s")
	if err != nil {
		t.Fatal(err)
	}
	now := cases[len(cases)-1].T + 1
	kinds := map[string]string{}
	for _, entry := range l.Entries {
		x, found := -1.0, false
		for _, c := range cases {
			if label(c) == entry.Label && len(c.Answers) > 0 {
				x, found = m.post.Load().Explain(e.ver.point(c, now)).EvidenceRatio, true
			}
		}
		if !found {
			t.Fatalf("entry %q has no case", entry.Label)
		}
		inBand := entry.AskKind == limen.AskUnsure || entry.AskKind == limen.AskSplit || entry.AskKind == limen.AskShort
		if math.Abs(entry.EvidenceRatio-x) > 1e-12 || (inBand && (entry.AskKind == limen.AskUnsure) != (x < EvidenceCut)) {
			t.Errorf("entry %+v, an explanation at its point has ratio %v", entry, x)
		}
		kinds[entry.Label] = entry.AskKind
	}
	if kinds["bash: make"] != limen.AskSplit {
		t.Errorf("an action the person goes both ways on: %q, want split", kinds["bash: make"])
	}
	if k, ok := kinds["bash: go test"]; !ok || k != "" {
		t.Errorf("a familiar test run: %q (%v)", k, ok)
	}
}
