// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package server serves a [limen.Service] over the HTTP API of spec 003 and
// api/openapi.yaml: the routes under /v1, the error envelope, the body
// limits, and the authorization model.
//
// Every request is authenticated first, by an [Authenticator] that turns a
// bearer into a [Caller]. Its body or query is then decoded and validated
// against the contract, so a malformed request is answered with the route's
// invalid_* code before anyone is asked about it. Then the route asks the
// installation's authorizer, a latere.ai/x/pkg/authz Authorizer, exactly one
// action of latere.ai/x/limen/authorizer about a resource of the kind and
// fields spec 003 fixes for that action, and acts only on an allow. A deny
// is forbidden and an authorizer that gives no decision is
// authorizer_unavailable; neither reaches the service.
//
// The caller is in general not the subject a request names: a harness
// predicts for the person its agent works for. Whether it may is the
// authorizer's decision, made from the resource's org and subject fields;
// nothing in a body grants it. An answer's by field is kept only from a
// caller relaying for another subject, which the authorizer has allowed to
// answer for them; anyone else answers as themselves.
//
// With no endpoint configured, [OwnerPolicy] decides: a subject acts on its
// own cases and reports, and the admins it lists act on everything,
// profiles and replays included. [Local] is the single-person setup, a
// static token whose subject is that policy's admin.
//
// An error the service returns that is not a contract error means Limen
// could not answer, a store down or a deadline passed, and is served as
// unavailable (503), the answer on which a decision point holds the action
// for a person. A panic is internal (500).
package server

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"latere.ai/x/pkg/authz"

	"latere.ai/x/limen"
	"latere.ai/x/limen/authorizer"
)

// The default body bounds: spec 003's 1 MiB for a JSON body, and a bound on
// a whole replay upload beside its record count.
const (
	DefaultMaxBodyBytes   = 1 << 20
	DefaultMaxReplayBytes = 256 << 20
)

// RequestIDHeader carries the request id in and out. An id the caller sends
// is kept when it is short and printable, so a decision point can correlate
// its own logs; otherwise the server mints one.
const RequestIDHeader = "X-Request-Id"

// Options configure the handler.
type Options struct {
	// Authenticator identifies the caller of every request. With none, every
	// request is unauthenticated.
	Authenticator Authenticator
	// Authorizer answers every route's question; a *authz.Client asks an
	// operator's endpoint. Nil is [OwnerPolicy] with no admins.
	Authorizer authz.Authorizer
	// Logger receives the developer detail of every 5xx answer; nil logs
	// nothing.
	Logger *slog.Logger
	// MaxBodyBytes bounds a JSON body and a replay line; zero is
	// DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// MaxReplayBytes bounds a whole replay upload; zero is
	// DefaultMaxReplayBytes. limen.MaxReplayRecords bounds its records.
	MaxReplayBytes int64
}

// Local is the setup of a server one person runs for themselves: the
// static bearer token authenticates them as subject, and the owner policy
// makes that subject its admin, so they may act for any subject, set
// profiles and replay history.
func Local(token, subject string) Options {
	return Options{Authenticator: StaticToken(token, subject), Authorizer: OwnerPolicy{Admins: []string{subject}}}
}

// handler is the served API.
type handler struct {
	svc       limen.Service
	authn     Authenticator
	authz     authz.Authorizer
	log       *slog.Logger
	maxBody   int64
	maxReplay int64
}

// route is one row of the served API. The routes test holds this table to
// api/openapi.yaml in both directions.
type route struct {
	method, path, action string
	serve                func(h *handler, c *call) error
}

var routes = []route{
	{http.MethodPost, "/v1/predictions", authorizer.ActionCasePredict, (*handler).predict},
	{http.MethodPost, "/v1/decisions", authorizer.ActionCaseDecide, (*handler).decide},
	{http.MethodPost, "/v1/answers", authorizer.ActionCaseAnswer, (*handler).answer},
	{http.MethodGet, "/v1/cases", authorizer.ActionCaseList, (*handler).cases},
	{http.MethodGet, "/v1/reliability", authorizer.ActionReliabilityRead, (*handler).reliability},
	{http.MethodGet, "/v1/profiles", authorizer.ActionProfileRead, (*handler).profile},
	{http.MethodPut, "/v1/profiles", authorizer.ActionProfileWrite, (*handler).putProfile},
	{http.MethodPost, "/v1/replays", authorizer.ActionReplayCreate, (*handler).replay},
	{http.MethodPost, "/v1/subjects/forget", authorizer.ActionSubjectForget, (*handler).forget},
}

// New returns the handler that serves svc.
func New(svc limen.Service, o Options) http.Handler {
	h := &handler{
		svc:       svc,
		authn:     o.Authenticator,
		authz:     o.Authorizer,
		log:       o.Logger,
		maxBody:   cmp.Or(o.MaxBodyBytes, DefaultMaxBodyBytes),
		maxReplay: cmp.Or(o.MaxReplayBytes, DefaultMaxReplayBytes),
	}
	if h.authz == nil {
		h.authz = OwnerPolicy{}
	}
	if h.log == nil {
		h.log = slog.New(slog.DiscardHandler)
	}
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.method+" "+rt.path, h.serve(rt))
	}
	return mux
}

// call is one request in flight.
type call struct {
	w      http.ResponseWriter
	r      *http.Request
	id     string
	action string
	caller Caller
}

func (h *handler) serve(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &call{w: w, r: r, id: requestID(r), action: rt.action}
		w.Header().Set(RequestIDHeader, c.id)
		defer func() {
			if p := recover(); p != nil {
				h.fail(c, &limen.Error{Code: limen.CodeInternal, Detail: fmt.Sprintf("%s %s: panic: %v", r.Method, r.URL.Path, p)})
			}
		}()
		if err := h.authenticate(c); err != nil {
			h.fail(c, err)
			return
		}
		if err := rt.serve(h, c); err != nil {
			h.fail(c, err)
		}
	})
}

func requestID(r *http.Request) string {
	id := r.Header.Get(RequestIDHeader)
	if id != "" && len(id) <= 128 && printable(id) {
		return id
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func printable(s string) bool {
	for _, c := range []byte(s) {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func (h *handler) authenticate(c *call) error {
	if h.authn == nil {
		return &limen.Error{Code: limen.CodeUnauthenticated, Detail: "the server has no authenticator configured"}
	}
	caller, err := h.authn.Authenticate(c.r)
	if err != nil {
		return &limen.Error{Code: limen.CodeUnauthenticated, Detail: err.Error()}
	}
	if caller.Subject == "" {
		return &limen.Error{Code: limen.CodeUnauthenticated, Detail: "the authenticator named no subject"}
	}
	c.caller = caller
	return nil
}

// authorize asks the route's action about one resource and returns nil
// only on an allow.
func (h *handler) authorize(c *call, res authz.Resource) error {
	ip, _, err := net.SplitHostPort(c.r.RemoteAddr)
	if err != nil {
		ip = c.r.RemoteAddr
	}
	req := authz.Request{
		Subject: c.caller.Subject, Issuer: c.caller.Issuer, Sub: c.caller.Sub, Claims: c.caller.Claims,
		Action: c.action, Resource: res,
		Request: authz.Caller{ID: c.id, IP: ip, UserAgent: c.r.UserAgent()},
	}
	d, err := h.authz.Authorize(c.r.Context(), req)
	if err != nil {
		return &limen.Error{Code: limen.CodeAuthorizerUnavailable, Detail: fmt.Sprintf("authorize %s on %s %s: %v", c.action, res.Kind, res.ID, err)}
	}
	if !d.Allow {
		return &limen.Error{Code: limen.CodeForbidden, Detail: fmt.Sprintf("authorizer denied %s on %s %s to %s: %s", c.action, res.Kind, res.ID, c.caller.Subject, d.Reason)}
	}
	return nil
}

// The resources of spec 003. The id is what the resource is about, so an
// authorizer client can cache its answer: the subject for the subject's
// ledger, the organization for a replay, and the scope for a profile. A
// profile's action kind travels as action_kind, because the shared
// contract's flat resource object reserves kind for the resource's own
// kind and would drop a field of that name.
func caseResource(org, subject string) authz.Resource {
	return authz.NewResource(authorizer.KindCase, subject, map[string]any{"org": org, "subject": subject})
}

func reliabilityResource(org, subject string) authz.Resource {
	return authz.NewResource(authorizer.KindReliability, subject, map[string]any{"org": org, "subject": subject})
}

func profileResource(s limen.Scope) authz.Resource {
	return authz.NewResource(authorizer.KindProfile, s.Org+"/"+s.Kind+"/"+s.Agent,
		map[string]any{"org": s.Org, "action_kind": s.Kind, "agent": s.Agent})
}

func replayResource(org string) authz.Resource {
	return authz.NewResource(authorizer.KindReplay, org, map[string]any{"org": org})
}

func subjectResource(org, subject string) authz.Resource {
	return authz.NewResource(authorizer.KindSubject, subject, map[string]any{"org": org, "subject": subject})
}

// envelope is the family's error body.
type envelope struct {
	Error envelopeError `json:"error"`
}

type envelopeError struct {
	Code    limen.Code     `json:"code"`
	Message string         `json:"message"`
	Details envelopeDetail `json:"details"`
}

type envelopeDetail struct {
	RequestID string   `json:"request_id"`
	Detail    string   `json:"detail,omitempty"`
	Fields    []string `json:"fields,omitempty"`
}

// fail writes err as the error envelope. A contract error keeps its code; a
// canceled request is answered as unavailable without a log line, since the
// caller left; any other error means the service could not answer.
func (h *handler) fail(c *call, err error) {
	var le *limen.Error
	if !errors.As(err, &le) {
		le = &limen.Error{Code: limen.CodeUnavailable, Detail: err.Error()}
	}
	status := le.Code.Status()
	if status >= 500 && !errors.Is(err, context.Canceled) {
		h.log.Error("limen request failed", "request_id", c.id, "method", c.r.Method, "path", c.r.URL.Path,
			"code", string(le.Code), "detail", le.Detail)
	}
	body := envelope{Error: envelopeError{Code: le.Code, Message: le.Code.Message(),
		Details: envelopeDetail{RequestID: c.id, Detail: le.Detail, Fields: le.Fields}}}
	writeJSON(c.w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func noContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// decode reads a JSON body into v. Unknown fields are ignored, so a newer
// client works against an older server. A body that is not JSON of the
// right shape is the route's invalid code, naming the field a type error
// points at; a body past the bound is body_too_large.
func (h *handler) decode(c *call, v any, invalid limen.Code) error {
	body := http.MaxBytesReader(c.w, c.r.Body, h.maxBody)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		return bodyError(err, invalid, "body")
	}
	return nil
}

func bodyError(err error, invalid limen.Code, where string) error {
	if mbe, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return &limen.Error{Code: limen.CodeBodyTooLarge, Detail: fmt.Sprintf("%s exceeds %d bytes", where, mbe.Limit)}
	}
	e := &limen.Error{Code: invalid, Detail: where + ": " + err.Error()}
	if ute, ok := errors.AsType[*json.UnmarshalTypeError](err); ok && ute.Field != "" {
		e.Fields = []string{ute.Field}
	}
	return e
}

func (h *handler) predict(c *call) error {
	var a limen.Action
	if err := h.decode(c, &a, limen.CodeInvalidAction); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if err := h.authorize(c, caseResource(a.Org, a.Subject)); err != nil {
		return err
	}
	p, err := h.svc.Predict(c.r.Context(), a)
	if err != nil {
		return err
	}
	writeJSON(c.w, http.StatusOK, p)
	return nil
}

func (h *handler) decide(c *call) error {
	var d limen.Decision
	if err := h.decode(c, &d, limen.CodeInvalidDecision); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}
	if err := h.authorize(c, caseResource(d.Org, d.Subject)); err != nil {
		return err
	}
	if err := h.svc.Record(c.r.Context(), d); err != nil {
		return err
	}
	noContent(c.w)
	return nil
}

func (h *handler) answer(c *call) error {
	var a limen.Answer
	if err := h.decode(c, &a, limen.CodeInvalidAnswer); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if err := h.authorize(c, caseResource(a.Org, a.Subject)); err != nil {
		return err
	}
	// A caller answering for its own subject answers as itself. A caller
	// relaying for another subject has just been allowed case.answer for
	// them, the permission that lets it name who answered.
	if c.caller.Subject == a.Subject || a.By == "" {
		a.By = c.caller.Subject
	}
	if err := h.svc.Answer(c.r.Context(), a); err != nil {
		return err
	}
	noContent(c.w)
	return nil
}

func (h *handler) cases(c *call) error {
	q := c.r.URL.Query()
	cq := limen.CaseQuery{Org: q.Get("org"), Subject: q.Get("subject"), ActionID: q.Get("action_id"), Cursor: q.Get("cursor")}
	var fields []string
	if s := q.Get("pending"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			fields = append(fields, "pending")
		}
		cq.Pending = b
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > limen.MaxPageLimit {
			fields = append(fields, "limit")
		}
		cq.Limit = n
	}
	if err := cq.Validate(); err != nil || len(fields) > 0 {
		return queryError(err, fields)
	}
	if err := h.authorize(c, caseResource(cq.Org, cq.Subject)); err != nil {
		return err
	}
	page, err := h.svc.Cases(c.r.Context(), cq)
	if err != nil {
		return err
	}
	if page.Entries == nil {
		page.Entries = []limen.Case{}
	}
	writeJSON(c.w, http.StatusOK, page)
	return nil
}

// queryError merges the fields a query's parsing found at fault with the
// contract's validation of what did parse, into one invalid_query.
func queryError(err error, fields []string) error {
	e := &limen.Error{Code: limen.CodeInvalidQuery}
	if le, ok := errors.AsType[*limen.Error](err); ok {
		e.Fields, e.Detail = le.Fields, le.Detail
	}
	for _, f := range fields {
		if !slices.Contains(e.Fields, f) {
			e.Fields = append(e.Fields, f)
		}
	}
	if len(fields) > 0 {
		e.Detail = cmp.Or(e.Detail+"; ", "") + fmt.Sprintf("query parameters %v do not parse", fields)
	}
	return e
}

func (h *handler) reliability(c *call) error {
	q := c.r.URL.Query()
	rq := limen.ReliabilityQuery{Org: q.Get("org"), Subject: q.Get("subject"), Of: q.Get("of")}
	var fields []string
	for _, p := range []struct {
		name string
		to   *time.Time
	}{{"from", &rq.From}, {"to", &rq.To}} {
		if s := q.Get(p.name); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				fields = append(fields, p.name)
			}
			*p.to = t
		}
	}
	if err := rq.Validate(); err != nil || len(fields) > 0 {
		return queryError(err, fields)
	}
	if err := h.authorize(c, reliabilityResource(rq.Org, rq.Subject)); err != nil {
		return err
	}
	rel, err := h.svc.Reliability(c.r.Context(), rq)
	if err != nil {
		return err
	}
	writeJSON(c.w, http.StatusOK, rel)
	return nil
}

func (h *handler) profile(c *call) error {
	q := c.r.URL.Query()
	s := limen.Scope{Org: q.Get("org"), Kind: q.Get("kind"), Agent: q.Get("agent")}
	if s.Org == "" {
		return &limen.Error{Code: limen.CodeInvalidQuery, Fields: []string{"org"}, Detail: "org is empty"}
	}
	if err := h.authorize(c, profileResource(s)); err != nil {
		return err
	}
	p, err := h.svc.Profile(c.r.Context(), s)
	if err != nil {
		return err
	}
	writeJSON(c.w, http.StatusOK, p)
	return nil
}

func (h *handler) putProfile(c *call) error {
	var p limen.Profile
	if err := h.decode(c, &p, limen.CodeInvalidProfile); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if err := h.authorize(c, profileResource(p.Scope)); err != nil {
		return err
	}
	saved, err := h.svc.PutProfile(c.r.Context(), p)
	if err != nil {
		return err
	}
	writeJSON(c.w, http.StatusOK, saved)
	return nil
}

// forgetRequest is the body of POST /v1/subjects/forget.
type forgetRequest struct {
	Org     string `json:"org"`
	Subject string `json:"subject"`
}

func (h *handler) forget(c *call) error {
	var f forgetRequest
	if err := h.decode(c, &f, limen.CodeInvalidQuery); err != nil {
		return err
	}
	var fields []string
	if f.Org == "" {
		fields = append(fields, "org")
	}
	if f.Subject == "" {
		fields = append(fields, "subject")
	}
	if len(fields) > 0 {
		return &limen.Error{Code: limen.CodeInvalidQuery, Fields: fields, Detail: "forget needs an org and a subject"}
	}
	if err := h.authorize(c, subjectResource(f.Org, f.Subject)); err != nil {
		return err
	}
	if err := h.svc.Forget(c.r.Context(), f.Org, f.Subject); err != nil {
		return err
	}
	noContent(c.w)
	return nil
}

// replay reads every record before it asks or learns anything: a replay
// past limen.MaxReplayRecords is body_too_large with nothing learned, a
// record that does not parse or validate is invalid_replay naming its
// line, and the authorizer is asked replay.create once for every
// organization the records name.
func (h *handler) replay(c *call) error {
	var opts limen.ReplayOptions
	if s := c.r.URL.Query().Get("keep"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return &limen.Error{Code: limen.CodeInvalidReplay, Fields: []string{"keep"}, Detail: fmt.Sprintf("keep %q is not a boolean", s)}
		}
		opts.Keep = b
	}
	recs, err := h.readReplay(c)
	if err != nil {
		return err
	}
	var orgs []string
	for _, r := range recs {
		if !slices.Contains(orgs, r.Action.Org) {
			orgs = append(orgs, r.Action.Org)
		}
	}
	slices.Sort(orgs)
	for _, org := range orgs {
		if err := h.authorize(c, replayResource(org)); err != nil {
			return err
		}
	}
	report, err := h.svc.Replay(c.r.Context(), func(yield func(limen.ReplayRecord, error) bool) {
		for _, r := range recs {
			if !yield(r, nil) {
				return
			}
		}
	}, opts)
	if err != nil {
		return err
	}
	if report.Would == nil {
		report.Would = map[limen.Verdict]int{}
	}
	writeJSON(c.w, http.StatusOK, report)
	return nil
}

func (h *handler) readReplay(c *call) ([]limen.ReplayRecord, error) {
	body := &readErr{r: http.MaxBytesReader(c.w, c.r.Body, h.maxReplay)}
	sc := bufio.NewScanner(body)
	// The scanner bounds a line by the larger of its buffer's capacity and
	// the maximum, so the buffer starts no larger than the bound.
	sc.Buffer(make([]byte, 0, min(64<<10, int(h.maxBody))), int(h.maxBody))
	var recs []limen.ReplayRecord
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(trimSpace(b)) == 0 {
			continue
		}
		if len(recs) == limen.MaxReplayRecords {
			return nil, &limen.Error{Code: limen.CodeBodyTooLarge, Detail: fmt.Sprintf("more than %d records", limen.MaxReplayRecords)}
		}
		at := "records[" + strconv.Itoa(len(recs)) + "]"
		var r limen.ReplayRecord
		if err := json.Unmarshal(b, &r); err != nil {
			// A line cut short by a failed read is the read's error.
			if body.err != nil {
				return nil, bodyError(body.err, limen.CodeInvalidReplay, "body")
			}
			return nil, &limen.Error{Code: limen.CodeInvalidReplay, Fields: []string{at},
				Detail: fmt.Sprintf("line %d: %v", line, err)}
		}
		if err := r.Validate(); err != nil {
			le, ok := errors.AsType[*limen.Error](err)
			if !ok {
				return nil, err
			}
			fields := make([]string, len(le.Fields))
			for i, f := range le.Fields {
				fields[i] = at + "." + f
			}
			return nil, &limen.Error{Code: limen.CodeInvalidReplay, Fields: fields, Detail: fmt.Sprintf("line %d: %s", line, le.Detail)}
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, &limen.Error{Code: limen.CodeBodyTooLarge, Detail: fmt.Sprintf("line %d exceeds %d bytes", line+1, h.maxBody)}
		}
		return nil, bodyError(err, limen.CodeInvalidReplay, "body")
	}
	return recs, nil
}

// readErr remembers the first error its reader returned other than EOF,
// which a scanner hands back only after the data before it.
type readErr struct {
	r   io.Reader
	err error
}

func (e *readErr) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && err != io.EOF && e.err == nil {
		e.err = err
	}
	return n, err
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
