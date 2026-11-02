// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/client"
	"latere.ai/x/limen/conformance"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/server"
	"latere.ai/x/limen/stub"
)

const (
	token  = "conformance-token"
	caller = "https://issuer.example|usr_conformance"
)

// serve puts svc behind a local server and returns a client of it.
func serve(t *testing.T, svc limen.Service) *client.Client {
	t.Helper()
	ts := httptest.NewServer(server.New(svc, server.Local(token, caller)))
	t.Cleanup(ts.Close)
	c, err := client.New(ts.URL, client.Options{HTTP: ts.Client(), Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The client, the server and the engine together hold the contract.
func TestConformanceEngine(t *testing.T) {
	over := func(t *testing.T, now func() time.Time) limen.Service {
		e := engine.New(engine.Options{Inline: 1, Clock: now})
		t.Cleanup(func() { e.Close() })
		return serve(t, e)
	}
	conformance.Run(t, conformance.Target{
		New:     func(t *testing.T) limen.Service { return over(t, nil) },
		Clocked: over,
	})
}

// The same against the stub, with its steered predictions.
func TestConformanceStub(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(t *testing.T) limen.Service { return serve(t, stub.New(stub.Options{})) },
		Steered: func(t *testing.T, predict func(limen.Action) (float64, float64)) limen.Service {
			return serve(t, stub.New(stub.Options{Predict: predict}))
		},
		Clocked: func(t *testing.T, now func() time.Time) limen.Service {
			return serve(t, stub.New(stub.Options{Clock: now}))
		},
	})
}

func TestNew(t *testing.T) {
	if _, err := client.New("http://x", client.Options{}); err == nil {
		t.Error("a client with no HTTP client")
	}
	for _, u := range []string{"", "ftp://x", "http://", "://x"} {
		if _, err := client.New(u, client.Options{HTTP: http.DefaultClient}); err == nil {
			t.Errorf("base URL %q accepted", u)
		}
	}
	if _, err := client.New("http://x", client.Options{HTTP: http.DefaultClient, BasePath: "v1/oversight"}); err == nil {
		t.Error("a relative base path accepted")
	}
}

// With a base path the client calls the routes under it.
func TestBasePath(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	c, err := client.New(ts.URL, client.Options{HTTP: ts.Client(), BasePath: "/v1/oversight/"})
	if err != nil {
		t.Fatal(err)
	}
	c.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	c.Explain(ctx, limen.Key{Org: "o", Subject: "s", ActionID: "a"})
	c.Preview(ctx, limen.PreviewRequest{Org: "o", Subject: "s"})
	want := []string{"GET /v1/oversight/learned", "GET /v1/oversight/explanations", "POST /v1/oversight/previews"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("paths %v, want %v", got, want)
	}
}

var ctx = context.Background()

// An answer outside the envelope keeps a code a caller can branch on.
func TestResponsesWithoutEnvelope(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   limen.Code
	}{
		{http.StatusServiceUnavailable, "upstream down", limen.CodeUnavailable},
		{http.StatusBadGateway, "", limen.CodeUnavailable},
		{http.StatusGatewayTimeout, strings.Repeat("x", 500), limen.CodeUnavailable},
		{http.StatusNotFound, "404 page not found", limen.CodeInternal},
		{http.StatusTeapot, `{"error":{}}`, limen.CodeInternal},
	}
	for _, c := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		cl, err := client.New(ts.URL+"/", client.Options{HTTP: ts.Client()})
		if err != nil {
			t.Fatal(err)
		}
		_, err = cl.Predict(ctx, limen.Action{})
		if got := limen.CodeOf(err); got != c.code {
			t.Errorf("%d %q: code %q (%v), want %q", c.status, c.body, got, err, c.code)
		}
		ts.Close()
	}
}

// The envelope's code, fields and detail come back as a *limen.Error, with
// the request id in the detail.
func TestEnvelopeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_action","message":"m","details":{"request_id":"r1","detail":"kind is empty","fields":["kind"]}}}`))
	}))
	defer ts.Close()
	cl, _ := client.New(ts.URL, client.Options{HTTP: ts.Client()})
	_, err := cl.Predict(ctx, limen.Action{})
	var e *limen.Error
	if !errors.As(err, &e) || e.Code != limen.CodeInvalidAction || len(e.Fields) != 1 || e.Fields[0] != "kind" || !strings.Contains(e.Detail, "r1") {
		t.Errorf("%#v", err)
	}
}

// A server that cannot be reached, or that answers 200 with a body that is
// not the route's, is an ordinary error, not a contract one.
func TestTransportErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	cl, _ := client.New(ts.URL, client.Options{HTTP: ts.Client()})
	if _, err := cl.Predict(ctx, limen.Action{}); err == nil || limen.CodeOf(err) != limen.CodeInternal {
		t.Errorf("a garbled body: %v", err)
	}
	ts.Close()
	if _, err := cl.Profile(ctx, limen.Scope{Org: "o"}); err == nil {
		t.Error("a closed server answered")
	}
	cl2, _ := client.New("http://127.0.0.1:1", client.Options{HTTP: &http.Client{Timeout: time.Second}})
	p, err := limen.FailClosed(cl2, time.Second, nil).Predict(ctx, limen.Action{Org: "o", Subject: "s", ActionID: "a"})
	if err != nil || p.Verdict != limen.Ask || p.Source != limen.SourceUnavailable {
		t.Errorf("FailClosed around an unreachable server: %+v, %v", p, err)
	}
	if _, err := cl.Predict(ctx, limen.Action{Attributes: map[string]any{"x": func() {}}}); err == nil {
		t.Error("an action that cannot be encoded was sent")
	}
	if err := cl.Record(context.Background(), limen.Decision{}); err == nil {
		t.Error("a closed server recorded")
	}
	bad, _ := client.New("http://127.0.0.1:1", client.Options{HTTP: http.DefaultClient})
	c, cancel := context.WithCancel(ctx)
	cancel()
	if err := bad.Answer(c, limen.Answer{}); !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled call: %v", err)
	}
}

// Every query parameter the client knows reaches the server.
func TestQueries(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	cl, _ := client.New(ts.URL, client.Options{HTTP: ts.Client()})
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	_, _ = cl.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s", ActionID: "a", Pending: true, Cursor: "c", Limit: 5})
	_, _ = cl.Reliability(ctx, limen.ReliabilityQuery{Org: "o", Subject: "s", Of: "learned", From: from, To: from.Add(time.Hour)})
	_, _ = cl.Reliability(ctx, limen.ReliabilityQuery{Org: "o", Subject: "s", Of: "learned"})
	_, _ = cl.Profile(ctx, limen.Scope{Org: "o", Kind: "tool", Agent: "x"})
	_, _ = cl.Replay(ctx, func(yield func(limen.ReplayRecord, error) bool) {}, limen.ReplayOptions{Keep: true})
	want := []string{
		"GET /v1/cases?action_id=a&cursor=c&limit=5&org=o&pending=true&subject=s",
		"GET /v1/reliability?from=2026-10-01T00%3A00%3A00Z&of=learned&org=o&subject=s&to=2026-10-01T01%3A00%3A00Z",
		"GET /v1/reliability?of=learned&org=o&subject=s",
		"GET /v1/profiles?agent=x&kind=tool&org=o",
		"POST /v1/replays?keep=true",
	}
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Errorf("call %d: %q, want %q", i, got, w)
			break
		}
	}
}

// A replay the server answers before reading it all does not hang, and a
// record the encoder cannot write ends the upload.
func TestReplayEarlyAnswer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":{"code":"body_too_large","message":"m","details":{"request_id":"r"}}}`))
	}))
	defer ts.Close()
	cl, _ := client.New(ts.URL, client.Options{HTTP: ts.Client()})
	many := func(yield func(limen.ReplayRecord, error) bool) {
		for range 100000 {
			if !yield(limen.ReplayRecord{Source: strings.Repeat("s", 100)}, nil) {
				return
			}
		}
	}
	if _, err := cl.Replay(ctx, many, limen.ReplayOptions{}); limen.CodeOf(err) != limen.CodeBodyTooLarge {
		t.Errorf("an early answer: %v", err)
	}
	unencodable := func(yield func(limen.ReplayRecord, error) bool) {
		yield(limen.ReplayRecord{Action: limen.Action{Attributes: map[string]any{"f": func() {}}}}, nil)
	}
	if _, err := cl.Replay(ctx, unencodable, limen.ReplayOptions{}); err == nil {
		t.Error("an unencodable record was sent")
	}
}
