// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package client is [limen.Service] over the HTTP API of spec 003, for a Go
// decision point that reaches Limen over the network rather than embedding
// the engine: it changes nothing but its constructor.
//
// An answer in the API's error envelope comes back as a *limen.Error with
// the code, the fields and the developer detail the server sent, so
// limen.CodeOf reads it as it would read the engine's. A failure to reach
// the server is an ordinary error, which limen.FailClosed turns into an ask.
//
// The client sends through the *http.Client its caller passes, which is
// where the caller's tracing transport goes; it builds none of its own.
package client

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/limen"
)

// Options configure a [Client].
type Options struct {
	// HTTP sends the calls. Required: a caller passes its instrumented
	// client.
	HTTP *http.Client
	// Token is the bearer token sent with every call; empty sends none.
	Token string
	// BasePath is the prefix the routes sit under in place of /v1, the
	// server's LIMEN_BASE_PATH behind a public route such as
	// "/v1/oversight"; empty is /v1.
	BasePath string
}

// Client is a [limen.Service] that calls a Limen server.
type Client struct {
	base   string
	prefix string
	http   *http.Client
	token  string
}

var _ limen.Service = (*Client)(nil)

// New returns a client of the server at baseURL, the URL the /v1 routes sit
// under, such as "http://127.0.0.1:8080" or a platform's mounted prefix. It
// sends nothing.
func New(baseURL string, o Options) (*Client, error) {
	if o.HTTP == nil {
		return nil, errors.New("client: an HTTP client is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("client: base URL %q is not an http or https URL", baseURL)
	}
	prefix := strings.TrimRight(o.BasePath, "/")
	if prefix == "" {
		prefix = "/v1"
	}
	if !strings.HasPrefix(prefix, "/") {
		return nil, fmt.Errorf("client: base path %q is not an absolute path", o.BasePath)
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), prefix: prefix, http: o.HTTP, token: o.Token}, nil
}

// do sends one call and decodes a 2xx body into out when out is not nil.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, contentType string, body io.Reader, out any) error {
	u := c.base + c.prefix + strings.TrimPrefix(path, "/v1")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("client: %s %s: reading the response: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		return responseError(method, path, resp.StatusCode, raw)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(raw, out, lenientJSON); err != nil {
		return fmt.Errorf("client: %s %s: decoding a %d response: %w", method, path, resp.StatusCode, err)
	}
	return nil
}

// lenientJSON reads and writes the bodies of the API. The input of an action
// is a tool call's arguments, written by an agent, so repeated names and
// invalid UTF-8 inside it pass through instead of failing the call.
var lenientJSON = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
)

// envelope is the API's error body.
type envelope struct {
	Error struct {
		Code    limen.Code `json:"code"`
		Message string     `json:"message"`
		Details struct {
			RequestID string   `json:"request_id"`
			Detail    string   `json:"detail"`
			Fields    []string `json:"fields"`
		} `json:"details"`
	} `json:"error"`
}

// responseError reads an error answer. The envelope keeps its code; an
// answer without one, from a proxy or a route the server does not serve,
// is unavailable when the status says the server could not answer and
// internal otherwise.
func responseError(method, path string, status int, raw []byte) error {
	var env envelope
	if json.Unmarshal(raw, &env, lenientJSON) == nil && env.Error.Code != "" {
		d := env.Error.Details
		detail := d.Detail
		if d.RequestID != "" {
			detail = strings.TrimSpace(detail + " (request " + d.RequestID + ")")
		}
		return &limen.Error{Code: env.Error.Code, Fields: d.Fields, Detail: detail}
	}
	code := limen.CodeInternal
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		code = limen.CodeUnavailable
	}
	excerpt := string(raw)
	if len(excerpt) > 200 {
		excerpt = excerpt[:200]
	}
	return &limen.Error{Code: code, Detail: fmt.Sprintf("%s %s answered %d without the error envelope: %q", method, path, status, excerpt)}
}

func (c *Client) json(ctx context.Context, method, path string, in, out any) error {
	b, err := json.Marshal(in, lenientJSON)
	if err != nil {
		return fmt.Errorf("client: encoding the %s %s body: %w", method, path, err)
	}
	return c.do(ctx, method, path, nil, "application/json", bytes.NewReader(b), out)
}

// Predict implements [limen.Predictor].
func (c *Client) Predict(ctx context.Context, a limen.Action) (limen.Prediction, error) {
	var p limen.Prediction
	return p, c.json(ctx, http.MethodPost, "/v1/predictions", a, &p)
}

// Record implements [limen.Ledger].
func (c *Client) Record(ctx context.Context, d limen.Decision) error {
	return c.json(ctx, http.MethodPost, "/v1/decisions", d, nil)
}

// Answer implements [limen.Ledger].
func (c *Client) Answer(ctx context.Context, a limen.Answer) error {
	return c.json(ctx, http.MethodPost, "/v1/answers", a, nil)
}

// Cases implements [limen.Ledger].
func (c *Client) Cases(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
	v := url.Values{"org": {q.Org}, "subject": {q.Subject}}
	if q.ActionID != "" {
		v.Set("action_id", q.ActionID)
	}
	if q.Pending {
		v.Set("pending", "true")
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit != 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	var p limen.Page
	return p, c.do(ctx, http.MethodGet, "/v1/cases", v, "", nil, &p)
}

// Reliability implements [limen.Ledger].
func (c *Client) Reliability(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
	v := url.Values{"org": {q.Org}, "subject": {q.Subject}, "of": {q.Of}}
	if !q.From.IsZero() {
		v.Set("from", q.From.Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		v.Set("to", q.To.Format(time.RFC3339Nano))
	}
	var r limen.Reliability
	return r, c.do(ctx, http.MethodGet, "/v1/reliability", v, "", nil, &r)
}

// Calibration implements [limen.Ledger].
func (c *Client) Calibration(ctx context.Context, q limen.CalibrationQuery) (limen.Calibration, error) {
	v := url.Values{"org": {q.Org}, "subject": {q.Subject}}
	if !q.From.IsZero() {
		v.Set("from", q.From.Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		v.Set("to", q.To.Format(time.RFC3339Nano))
	}
	var r limen.Calibration
	return r, c.do(ctx, http.MethodGet, "/v1/calibration", v, "", nil, &r)
}

// scopeValues is the query that names a scope.
func scopeValues(s limen.Scope) url.Values {
	v := url.Values{"org": {s.Org}}
	for name, x := range map[string]string{"kind": s.Kind, "agent": s.Agent, "subject": s.Subject} {
		if x != "" {
			v.Set(name, x)
		}
	}
	return v
}

// Profile implements [limen.Admin].
func (c *Client) Profile(ctx context.Context, s limen.Scope) (limen.Profile, error) {
	var p limen.Profile
	return p, c.do(ctx, http.MethodGet, "/v1/profiles", scopeValues(s), "", nil, &p)
}

// DeleteProfile implements [limen.Admin].
func (c *Client) DeleteProfile(ctx context.Context, s limen.Scope) error {
	return c.do(ctx, http.MethodDelete, "/v1/profiles", scopeValues(s), "", nil, nil)
}

// PutProfile implements [limen.Admin].
func (c *Client) PutProfile(ctx context.Context, p limen.Profile) (limen.Profile, error) {
	var out limen.Profile
	return out, c.json(ctx, http.MethodPut, "/v1/profiles", p, &out)
}

// Forget implements [limen.Admin].
func (c *Client) Forget(ctx context.Context, org, subject string) error {
	return c.json(ctx, http.MethodPost, "/v1/subjects/forget", map[string]string{"org": org, "subject": subject}, nil)
}

// Replay implements [limen.Admin]. The records are streamed as
// newline-delimited JSON while the source yields them; an error from the
// source ends the upload and is returned as it came.
func (c *Client) Replay(ctx context.Context, records iter.Seq2[limen.ReplayRecord, error], o limen.ReplayOptions) (limen.ReplayReport, error) {
	pr, pw := io.Pipe()
	var srcErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		enc := jsontext.NewEncoder(pw, lenientJSON)
		for r, err := range records {
			if err != nil {
				srcErr = err
				pw.CloseWithError(err)
				return
			}
			if err := json.MarshalEncode(enc, r); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		_ = pw.Close()
	}()
	var q url.Values
	if o.Keep {
		q = url.Values{"keep": {"true"}}
	}
	var rep limen.ReplayReport
	err := c.do(ctx, http.MethodPost, "/v1/replays", q, "application/x-ndjson", pr, &rep)
	// Unblock the writer when the server answered before reading it all.
	pr.CloseWithError(errors.New("client: the replay upload ended"))
	<-done
	if srcErr != nil {
		return limen.ReplayReport{}, srcErr
	}
	return rep, err
}

// Explain implements [limen.Explainer].
func (c *Client) Explain(ctx context.Context, k limen.Key) (limen.Explanation, error) {
	v := url.Values{"org": {k.Org}, "subject": {k.Subject}, "action_id": {k.ActionID}}
	var x limen.Explanation
	return x, c.do(ctx, http.MethodGet, "/v1/explanations", v, "", nil, &x)
}

// Learned implements [limen.Explainer].
func (c *Client) Learned(ctx context.Context, q limen.LearnedQuery) (limen.Learned, error) {
	v := url.Values{"org": {q.Org}, "subject": {q.Subject}}
	if q.Agent != "" {
		v.Set("agent", q.Agent)
	}
	if q.Kind != "" {
		v.Set("kind", q.Kind)
	}
	var l limen.Learned
	return l, c.do(ctx, http.MethodGet, "/v1/learned", v, "", nil, &l)
}

// Preview implements [limen.Explainer].
func (c *Client) Preview(ctx context.Context, r limen.PreviewRequest) (limen.Preview, error) {
	var p limen.Preview
	return p, c.json(ctx, http.MethodPost, "/v1/previews", r, &p)
}

// Report implements [limen.Reporter].
func (c *Client) Report(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
	v := url.Values{"org": {q.Org}}
	if !q.From.IsZero() {
		v.Set("from", q.From.Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		v.Set("to", q.To.Format(time.RFC3339Nano))
	}
	var r limen.Report
	return r, c.do(ctx, http.MethodGet, "/v1/reports", v, "", nil, &r)
}
