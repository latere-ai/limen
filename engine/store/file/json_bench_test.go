// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package file_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/featurize"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/engine/store/file"
)

var jbBase = time.Date(2026, 10, 5, 9, 30, 0, 0, time.UTC)

const (
	jbOrg     = "org_8fd2c1"
	jbSubject = "https://issuer.example|usr_alice"
)

// jbCase is a decided, answered case as the engine stores it after a review.
func jbCase(i int) store.Case {
	rev := true
	a := limen.Action{
		Org: jbOrg, Subject: jbSubject, ActionID: fmt.Sprintf("act_%08d", i),
		Actor:       &limen.Actor{Kind: "agent", ID: "agent_coder", Session: "sess_3a91"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      limen.EffectWrite,
		Reversible:  &rev,
		Input:       jsontext.Value(`{"command":"go test ./engine/..."}`),
		Target:      &limen.Target{Kind: "path", ID: "/workspace/engine/store/memory.go", Class: "workspace"},
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

// jbSeed writes n cases to a new store in dir and closes it.
func jbSeed(b *testing.B, dir string, n int) {
	b.Helper()
	s, err := file.OpenWith(dir, file.Options{NoSync: true})
	if err != nil {
		b.Fatal(err)
	}
	for i := range n {
		if err := s.Put(context.Background(), jbCase(i)); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
}

func jbLogSize(b *testing.B, dir string) int64 {
	b.Helper()
	var total int64
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p) == "cases.ndjson" {
			fi, e := d.Info()
			if e != nil {
				return e
			}
			total += fi.Size()
		}
		return err
	})
	if err != nil {
		b.Fatal(err)
	}
	return total
}

// Opening a subject replays its log: one line of JSON per stored case. This
// is the cost of the first request for a subject after a restart.
func BenchmarkJSONFileLogReplayUnmarshal(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 12}, {"large", 200}} {
		b.Run(size.name, func(b *testing.B) {
			dir := b.TempDir()
			jbSeed(b, dir, size.n)
			k := jbCase(0).Action.Key
			b.ReportAllocs()
			b.SetBytes(jbLogSize(b, dir))
			for b.Loop() {
				s, err := file.OpenWith(dir, file.Options{NoSync: true})
				if err != nil {
					b.Fatal(err)
				}
				if _, ok, err := s.Case(context.Background(), k); err != nil || !ok {
					b.Fatal(ok, err)
				}
				if err := s.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Cases decodes every stored line of a loaded subject.
func BenchmarkJSONFileCasesUnmarshal(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 12}, {"large", 200}} {
		b.Run(size.name, func(b *testing.B) {
			dir := b.TempDir()
			jbSeed(b, dir, size.n)
			s, err := file.OpenWith(dir, file.Options{NoSync: true})
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(jbLogSize(b, dir))
			for b.Loop() {
				cs, err := s.Cases(ctx, jbOrg, jbSubject)
				if err != nil || len(cs) != size.n {
					b.Fatal(len(cs), err)
				}
			}
		})
	}
}

// Put encodes the case and appends it as one line of the subject's log.
func BenchmarkJSONFileCasePutMarshal(b *testing.B) {
	enc, err := json.Marshal(jbCase(0), json.Deterministic(true), jsontext.AllowInvalidUTF8(true))
	if err != nil {
		b.Fatal(err)
	}
	s, err := file.OpenWith(b.TempDir(), file.Options{NoSync: true})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c := jbCase(0)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(enc)))
	for b.Loop() {
		if err := s.Put(ctx, c); err != nil {
			b.Fatal(err)
		}
	}
}

// PutProfile writes the organization's profile file whole, indented.
func BenchmarkJSONFileProfilesPutMarshal(b *testing.B) {
	var ps []limen.Profile
	for i := range 40 {
		ps = append(ps, limen.Profile{Org: jbOrg, Kind: "tool", Agent: fmt.Sprintf("agent_%02d", i),
			Costs: limen.DefaultCosts, AuditRate: 0.05})
	}
	enc, err := json.Marshal(ps, json.Deterministic(true), jsontext.AllowInvalidUTF8(true), jsontext.WithIndent("  "))
	if err != nil {
		b.Fatal(err)
	}
	s, err := file.OpenWith(b.TempDir(), file.Options{NoSync: true})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	for _, p := range ps {
		if err := s.PutProfile(ctx, p); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(enc)))
	i := 0
	for b.Loop() {
		p := ps[i%len(ps)]
		p.AuditRate = 0.05 + float64(i%5)/100
		if err := s.PutProfile(ctx, p); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
