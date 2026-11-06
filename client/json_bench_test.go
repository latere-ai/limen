// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/client"
)

var jbBase = time.Date(2026, 10, 5, 9, 30, 0, 0, time.UTC)

// jbTransport answers every call from memory with a fixed body and reads the
// whole request body first, as a real transport does.
type jbTransport struct {
	status int
	body   []byte
}

func (t jbTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return nil, err
		}
		_ = r.Body.Close()
	}
	return &http.Response{
		StatusCode: t.status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(t.body)),
		Request:    r,
	}, nil
}

func jbClient(b *testing.B, status int, body []byte) *client.Client {
	b.Helper()
	c, err := client.New("http://limen.invalid", client.Options{
		HTTP:  &http.Client{Transport: jbTransport{status: status, body: body}},
		Token: "secret",
	})
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func jbAction(i int) limen.Action {
	rev := true
	return limen.Action{
		Org: "org_8fd2c1", Subject: "https://issuer.example|usr_alice", ActionID: fmt.Sprintf("act_%08d", i),
		Actor:       &limen.Actor{Kind: "agent", ID: "agent_coder", Session: "sess_3a91"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      limen.EffectWrite,
		Reversible:  &rev,
		Input:       jsontext.Value(`{"command":"go test -run TestEngine -count=1 ./engine/... && git diff --stat origin/main"}`),
		Target:      &limen.Target{Kind: "path", ID: "/workspace/engine/store/memory.go", Class: "workspace"},
		Environment: &limen.Environment{Kind: "sandbox", Holds: []string{"no_network"}},
		Attributes:  map[string]any{"branch": "main", "protected": true, "files": 3},
		Signals:     []limen.Signal{{Source: "rules/1", Verdict: limen.Ask}},
	}
}

func jbPrediction(a limen.Action) limen.Prediction {
	return limen.Prediction{
		Key: a.Key, Verdict: limen.Ask, Approve: 0.62, Uncertainty: 0.18, EvidenceRatio: 0.41, AskKind: limen.AskUnsure,
		Thresholds: limen.Thresholds{AllowAbove: 0.9, BlockBelow: 0.05}, Profile: limen.Scope{Org: a.Org, Kind: "tool"},
		AuditRate: 0.05, Source: "learned/17", Reason: "You have approved few actions like this one, so Limen asks.",
	}
}

func jbCase(i int) limen.Case {
	a := jbAction(i)
	a.Input = nil
	p := jbPrediction(a)
	waited := int64(4200)
	return limen.Case{
		Action: a, Prediction: &p,
		Decision: &limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "learned/17"},
		Answers: []limen.RecordedAnswer{{Approve: true, By: a.Subject, At: jbBase.Add(time.Duration(i) * time.Minute),
			Review: true, WaitedMS: &waited}},
	}
}

// Predict is the call a decision point makes before every tool call: it
// encodes the action and decodes the prediction.
func BenchmarkJSONPredictCallRoundTrip(b *testing.B) {
	a := jbAction(1)
	reply, err := json.Marshal(jbPrediction(a))
	if err != nil {
		b.Fatal(err)
	}
	req, err := json.Marshal(a)
	if err != nil {
		b.Fatal(err)
	}
	c := jbClient(b, http.StatusOK, reply)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(req) + len(reply)))
	for b.Loop() {
		p, err := c.Predict(ctx, a)
		if err != nil {
			b.Fatal(err)
		}
		if p.Verdict != limen.Ask {
			b.Fatal(p.Verdict)
		}
	}
}

// Cases decodes a page of recorded actions, as the console and exports read.
func BenchmarkJSONCasesPageDecode(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 12}, {"large", 250}} {
		b.Run(size.name, func(b *testing.B) {
			page := limen.Page{NextCursor: "c_0042"}
			for i := range size.n {
				page.Entries = append(page.Entries, jbCase(i))
			}
			reply, err := json.Marshal(page)
			if err != nil {
				b.Fatal(err)
			}
			c := jbClient(b, http.StatusOK, reply)
			q := limen.CaseQuery{Org: "org_8fd2c1", Subject: "https://issuer.example|usr_alice", Limit: 500}
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(len(reply)))
			for b.Loop() {
				got, err := c.Cases(ctx, q)
				if err != nil || len(got.Entries) != size.n {
					b.Fatal(len(got.Entries), err)
				}
			}
		})
	}
}

// Replay streams a decision point's history as newline-delimited JSON.
func BenchmarkJSONReplayUploadEncode(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 1}, {"medium", 20}, {"large", 400}} {
		b.Run(size.name, func(b *testing.B) {
			recs := make([]limen.ReplayRecord, size.n)
			var total int
			for i := range recs {
				yes := i%3 != 0
				at := jbBase.Add(time.Duration(i) * time.Second)
				recs[i] = limen.ReplayRecord{At: at, Action: jbAction(i), Verdict: limen.Ask, ReviewProbability: 1,
					Source: "rules/1", Approve: &yes, AnsweredAt: at.Add(5 * time.Second)}
				line, err := json.Marshal(recs[i])
				if err != nil {
					b.Fatal(err)
				}
				total += len(line) + 1
			}
			reply := []byte(`{"records":` + fmt.Sprint(size.n) + `,"answered":` + fmt.Sprint(size.n) +
				`,"would":{"ask":` + fmt.Sprint(size.n) + `},"allowed_denied":0,"blocked_approved":0,"false_allow":{},"kept":false}`)
			c := jbClient(b, http.StatusOK, reply)
			src := func() iter.Seq2[limen.ReplayRecord, error] {
				return func(yield func(limen.ReplayRecord, error) bool) {
					for _, r := range recs {
						if !yield(r, nil) {
							return
						}
					}
				}
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(total))
			for b.Loop() {
				rep, err := c.Replay(ctx, src(), limen.ReplayOptions{})
				if err != nil || rep.Records != size.n {
					b.Fatal(rep.Records, err)
				}
			}
		})
	}
}

// An error answer is decoded into the API's error envelope.
func BenchmarkJSONErrorEnvelopeDecode(b *testing.B) {
	reply := []byte(`{"error":{"code":"invalid_action","message":"The action is not valid.","details":{"request_id":"req_7c1e9a","detail":"effect: unknown value","fields":["effect","kind"]}}}`)
	c := jbClient(b, http.StatusBadRequest, reply)
	a := jbAction(1)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(reply)))
	for b.Loop() {
		_, err := c.Predict(ctx, a)
		if err == nil || !strings.Contains(err.Error(), "invalid_action") {
			b.Fatal(err)
		}
	}
}
