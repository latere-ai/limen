// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/featurize"
	"latere.ai/x/limen/engine/store"
)

var jbBase = time.Date(2026, 10, 5, 9, 30, 0, 0, time.UTC)

// jbCase is a decided, answered case as the engine stores it after a review.
func jbCase(i int) store.Case {
	rev := true
	sens := 0.4
	a := limen.Action{
		Org: "org_8fd2c1", Subject: "https://issuer.example|usr_alice", ActionID: fmt.Sprintf("act_%08d", i),
		Actor:       &limen.Actor{Kind: "agent", ID: "agent_coder", Session: "sess_3a91"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      limen.EffectWrite,
		Reversible:  &rev,
		Input:       jsontext.Value(`{"command":"go test ./engine/..."}`),
		Target:      &limen.Target{Kind: "path", ID: "/workspace/engine/store/memory.go", Sensitivity: &sens, Class: "workspace"},
		Environment: &limen.Environment{Kind: "sandbox", Holds: []string{"no_network"}},
		Attributes:  map[string]any{"branch": "main", "protected": true, "files": 3},
		Signals:     []limen.Signal{{Source: "rules/1", Verdict: limen.Ask}},
	}
	f := featurize.Of(a)
	a.Input = nil
	pred := limen.Prediction{
		Key: a.Key, Verdict: limen.Ask, Approve: 0.62, Uncertainty: 0.18, EvidenceRatio: 0.41, AskKind: limen.AskUnsure,
		Thresholds: limen.Thresholds{AllowAbove: 0.9, BlockBelow: 0.05}, Profile: limen.Scope{Org: a.Org, Kind: "tool"},
		AuditRate: 0.05, Source: "learned/17", Reason: "You have approved few actions like this one, so Limen asks.",
	}
	by := int64(17)
	at := jbBase.Add(time.Duration(i) * time.Minute)
	waited := int64(4200)
	return store.Case{
		T: int64(i), ReceivedAt: at, Digest: "9f2c4b7a1d0e8c33b5a6f1e2d4c7a90b8e3f5d6c7b8a9f0e1d2c3b4a59687766",
		Action: a, Featurizer: "shell/3", A: f.A, C: f.C, Label: "run a command", Labels: "labels/2",
		Scope:    &limen.Reach{Undo: "easy", TargetClass: "workspace", Contact: "none", TargetKind: "path"},
		Families: []string{"shell", "workspace"}, Prediction: &pred, PredictedBy: &by,
		Decision:  &limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "learned/17"},
		DecidedAt: at.Add(time.Second),
		Answers:   []limen.RecordedAnswer{{Approve: true, By: a.Subject, At: at.Add(5 * time.Second), Review: true, WaitedMS: &waited}},
	}
}

func jbMemory(b *testing.B, n int) *store.Memory {
	b.Helper()
	m := store.NewMemory()
	for i := range n {
		if err := m.Put(context.Background(), jbCase(i)); err != nil {
			b.Fatal(err)
		}
	}
	return m
}

func jbCaseSize(b *testing.B) int64 {
	b.Helper()
	enc, err := json.Marshal(jbCase(0), json.Deterministic(true), jsontext.AllowInvalidUTF8(true))
	if err != nil {
		b.Fatal(err)
	}
	return int64(len(enc))
}

// Put serializes the case the engine just decided.
func BenchmarkJSONMemoryCasePutMarshal(b *testing.B) {
	m := store.NewMemory()
	c := jbCase(0)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(jbCaseSize(b))
	for b.Loop() {
		if err := m.Put(ctx, c); err != nil {
			b.Fatal(err)
		}
	}
}

// Case reads one stored case back, as an answer or an idempotent retry does.
func BenchmarkJSONMemoryCaseGetUnmarshal(b *testing.B) {
	m := jbMemory(b, 1)
	k := jbCase(0).Action.Key
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(jbCaseSize(b))
	for b.Loop() {
		_, ok, err := m.Case(ctx, k)
		if err != nil || !ok {
			b.Fatal(ok, err)
		}
	}
}

// Cases reads a subject's whole history, which fitting a model does.
func BenchmarkJSONMemoryCasesUnmarshal(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 12}, {"large", 200}} {
		b.Run(size.name, func(b *testing.B) {
			m := jbMemory(b, size.n)
			c := jbCase(0)
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(jbCaseSize(b) * int64(size.n))
			for b.Loop() {
				cs, err := m.Cases(ctx, c.Action.Org, c.Action.Subject)
				if err != nil || len(cs) != size.n {
					b.Fatal(len(cs), err)
				}
			}
		})
	}
}
