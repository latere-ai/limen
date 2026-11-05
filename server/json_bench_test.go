// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
)

// jbService answers every route a benchmark drives with fixed values, so the
// time measured is the handler's decoding and encoding.
type jbService struct {
	limen.Service
	pred limen.Prediction
	page limen.Page
}

func (s jbService) Predict(context.Context, limen.Action) (limen.Prediction, error) {
	return s.pred, nil
}

func (s jbService) Cases(context.Context, limen.CaseQuery) (limen.Page, error) {
	return s.page, nil
}

func (s jbService) Replay(_ context.Context, recs iter.Seq2[limen.ReplayRecord, error], _ limen.ReplayOptions) (limen.ReplayReport, error) {
	rep := limen.ReplayReport{Would: map[limen.Verdict]int{}}
	for r, err := range recs {
		if err != nil {
			return limen.ReplayReport{}, err
		}
		rep.Records++
		rep.Would[r.Verdict]++
	}
	return rep, nil
}

var jbBase = time.Date(2026, 10, 5, 9, 30, 0, 0, time.UTC)

// jbText returns n bytes of deterministic, shell-looking text.
func jbText(n int) string {
	const line = "go test -run TestEngine -count=1 ./engine/... && git diff --stat origin/main -- engine/store\n"
	return strings.Repeat(line, n/len(line)+1)[:n]
}

// jbAction is a shell tool call whose command is about inputBytes long.
func jbAction(i, inputBytes int) limen.Action {
	cmd, err := json.Marshal(map[string]string{"command": jbText(inputBytes), "description": "Run the engine tests"})
	if err != nil {
		panic(err)
	}
	rev := true
	return limen.Action{
		Org: "org_8fd2c1", Subject: "https://issuer.example|usr_alice", ActionID: fmt.Sprintf("act_%08d", i),
		Actor:       &limen.Actor{Kind: "agent", ID: "agent_coder", Session: "sess_3a91"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      limen.EffectWrite,
		Reversible:  &rev,
		Input:       cmd,
		Target:      &limen.Target{Kind: "path", ID: "/workspace/engine/store/memory.go", Class: "workspace"},
		Environment: &limen.Environment{Kind: "sandbox", Credentialed: false, Holds: []string{"no_network"}},
		Attributes:  map[string]any{"branch": "main", "protected": true, "files": 3},
		Signals:     []limen.Signal{{Source: "rules/1", Verdict: limen.Ask}},
	}
}

func jbPrediction(a limen.Action) limen.Prediction {
	return limen.Prediction{
		Key:           a.Key,
		Verdict:       limen.Ask,
		Approve:       0.62,
		Uncertainty:   0.18,
		EvidenceRatio: 0.41,
		AskKind:       limen.AskUnsure,
		Thresholds:    limen.Thresholds{AllowAbove: 0.9, BlockBelow: 0.05},
		Profile:       limen.Scope{Org: a.Org, Kind: "tool"},
		AuditRate:     0.05,
		Source:        "learned/17",
		Reason:        "You have approved few actions like this one, so Limen asks.",
	}
}

func jbCase(i int) limen.Case {
	a := jbAction(i, 120)
	a.Input = nil
	p := jbPrediction(a)
	at := jbBase.Add(time.Duration(i) * time.Minute)
	waited := int64(4200)
	return limen.Case{
		Action:     a,
		Prediction: &p,
		Decision:   &limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "learned/17"},
		Answers:    []limen.RecordedAnswer{{Approve: true, By: a.Subject, At: at, Review: true, WaitedMS: &waited}},
	}
}

func jbReplayBody(n int) string {
	var b strings.Builder
	for i := range n {
		a := jbAction(i, 120)
		yes := i%3 != 0
		rec := limen.ReplayRecord{At: jbBase.Add(time.Duration(i) * time.Second), Action: a, Verdict: limen.Ask,
			ReviewProbability: 1, Source: "rules/1", Approve: &yes, AnsweredAt: jbBase.Add(time.Duration(i)*time.Second + 5*time.Second)}
		line, err := json.Marshal(rec)
		if err != nil {
			panic(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// jbServe sends one request through h and returns the response length.
func jbServe(b *testing.B, h http.Handler, method, target, body string) int {
	b.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		b.Fatalf("%s %s: %d %s", method, target, w.Code, w.Body.String())
	}
	return w.Body.Len()
}

// A prediction request is the hot path: every tool call an agent proposes is
// decoded, validated and answered with a prediction.
func BenchmarkJSONPredictRequestRoundTrip(b *testing.B) {
	for _, size := range []struct {
		name  string
		input int
	}{{"small", 200}, {"medium", 10 << 10}, {"large", 200 << 10}} {
		b.Run(size.name, func(b *testing.B) {
			a := jbAction(1, size.input)
			body, err := json.Marshal(a)
			if err != nil {
				b.Fatal(err)
			}
			h := New(jbService{pred: jbPrediction(a)}, Local(tok, admin))
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				jbServe(b, h, http.MethodPost, "/v1/predictions", string(body))
			}
		})
	}
}

// The case listing is the console's page of recorded actions.
func BenchmarkJSONCasesPageEncode(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 12}, {"large", 250}} {
		b.Run(size.name, func(b *testing.B) {
			page := limen.Page{NextCursor: "c_0042"}
			for i := range size.n {
				page.Entries = append(page.Entries, jbCase(i))
			}
			encoded, err := json.Marshal(page)
			if err != nil {
				b.Fatal(err)
			}
			h := New(jbService{page: page}, Local(tok, admin))
			target := "/v1/cases?org=org_8fd2c1&subject=https%3A%2F%2Fissuer.example%7Cusr_alice&limit=500"
			b.ReportAllocs()
			b.SetBytes(int64(len(encoded)))
			for b.Loop() {
				jbServe(b, h, http.MethodGet, target, "")
			}
		})
	}
}

// A replay uploads a decision point's history as newline-delimited JSON.
func BenchmarkJSONReplayBodyDecode(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 20}, {"large", 400}} {
		b.Run(size.name, func(b *testing.B) {
			body := jbReplayBody(size.n)
			h := New(jbService{}, Local(tok, admin))
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				jbServe(b, h, http.MethodPost, "/v1/replays", body)
			}
		})
	}
}

// writeJSON is the one encoder every route answers through.
func BenchmarkJSONWriteJSONPredictionEncode(b *testing.B) {
	p := jbPrediction(jbAction(1, 120))
	encoded, err := json.Marshal(p)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(encoded)))
	for b.Loop() {
		w := httptest.NewRecorder()
		writeJSON(w, http.StatusOK, p)
		if w.Code != http.StatusOK {
			b.Fatal(w.Code)
		}
	}
}
