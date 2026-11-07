// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/clientip"

	"latere.ai/x/limen"
	"latere.ai/x/limen/stub"
)

// from sends one request from the peer address remote, with a token only
// when withToken is set.
func from(h http.Handler, remote string, withToken bool, method, target string, headers ...string) *httptest.ResponseRecorder {
	hr := httptest.NewRequest(method, target, nil)
	hr.RemoteAddr = remote
	if withToken {
		hr.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		hr.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hr)
	return w
}

// A burst from one address is answered rate_limited with the seconds to
// wait, while another address still passes; requests with no token, the
// version route and the base path's twin of every route count too.
func TestAddressRequestLimit(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	s := &spy{Service: stub.New(stub.Options{})}
	h := New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}},
		AddressRequestsPerMinute: 4, BasePath: "/v1/oversight", Now: func() time.Time { return now }})
	const a, b = "192.0.2.1:5000", "192.0.2.2:5000"
	if w := from(h, a, true, http.MethodGet, "/v1/profiles?org=o"); w.Code != http.StatusOK {
		t.Fatalf("with a token: %d %s", w.Code, w.Body.String())
	}
	// No token: unauthenticated, and still counted.
	wantCode(t, from(h, a, false, http.MethodGet, "/v1/profiles?org=o"), limen.CodeUnauthenticated)
	if w := from(h, a, false, http.MethodGet, "/v1/version"); w.Code != http.StatusOK {
		t.Fatalf("version: %d", w.Code)
	}
	if w := from(h, a, true, http.MethodGet, "/v1/oversight/profiles?org=o"); w.Code != http.StatusOK {
		t.Fatalf("base path twin: %d", w.Code)
	}
	calls := s.calls.Load()
	w := from(h, a, true, http.MethodGet, "/v1/profiles?org=o")
	wantCode(t, w, limen.CodeRateLimited)
	if got := w.Header().Get("Retry-After"); got != "15" {
		t.Errorf("Retry-After %q, want 15", got)
	}
	if d := errorOf(t, w).Error.Details.Detail; d != "4 requests a minute from one client address to one replica" {
		t.Errorf("detail %q", d)
	}
	if s.calls.Load() != calls {
		t.Error("a limited request reached the service")
	}
	// The version route and a request with no token are limited as well.
	wantCode(t, from(h, a, true, http.MethodGet, "/v1/version"), limen.CodeRateLimited)
	wantCode(t, from(h, a, false, http.MethodGet, "/v1/oversight/version"), limen.CodeRateLimited)
	wantCode(t, from(h, a, false, http.MethodPost, "/v1/replays"), limen.CodeRateLimited)
	if w := from(h, b, true, http.MethodGet, "/v1/profiles?org=o"); w.Code != http.StatusOK {
		t.Errorf("another address: %d %s", w.Code, w.Body.String())
	}
	now = now.Add(15 * time.Second)
	if w := from(h, a, true, http.MethodGet, "/v1/profiles?org=o"); w.Code != http.StatusOK {
		t.Errorf("after the wait: %d %s", w.Code, w.Body.String())
	}
}

// Zero is the default, 6000 a minute, and a negative value turns the limit
// off.
func TestAddressRequestLimitDefaultAndOff(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	h := New(stub.New(stub.Options{}), Options{Now: clock})
	for i := range DefaultAddressRequestsPerMinute {
		if w := from(h, "192.0.2.1:1", false, http.MethodGet, "/v1/version"); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := from(h, "192.0.2.1:1", false, http.MethodGet, "/v1/version")
	wantCode(t, w, limen.CodeRateLimited)
	if d := errorOf(t, w).Error.Details.Detail; !strings.HasPrefix(d, "6000 requests a minute") {
		t.Errorf("detail %q", d)
	}
	off := New(stub.New(stub.Options{}), Options{AddressRequestsPerMinute: -1, Now: clock})
	for i := range DefaultAddressRequestsPerMinute + 10 {
		if w := from(off, "192.0.2.1:1", false, http.MethodGet, "/v1/version"); w.Code != http.StatusOK {
			t.Fatalf("unlimited request %d: %d", i, w.Code)
		}
	}
}

// Behind a trusted proxy the forwarded address is the client's, so two
// clients behind it have a limit each; behind a peer that is not trusted
// the header is the caller's word and counts for nothing.
func TestAddressLimitFollowsTrustedProxies(t *testing.T) {
	trusted, err := clientip.ParseTrusted("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	h := New(stub.New(stub.Options{}), Options{AddressRequestsPerMinute: 2, TrustedProxies: trusted,
		Now: func() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }})
	const xff = "X-Forwarded-For"
	for range 2 {
		if w := from(h, "10.0.0.1:80", false, http.MethodGet, "/v1/version", xff, "198.51.100.1"); w.Code != http.StatusOK {
			t.Fatalf("first client: %d", w.Code)
		}
	}
	wantCode(t, from(h, "10.0.0.1:80", false, http.MethodGet, "/v1/version", xff, "198.51.100.1"), limen.CodeRateLimited)
	if w := from(h, "10.0.0.1:80", false, http.MethodGet, "/v1/version", xff, "198.51.100.2"); w.Code != http.StatusOK {
		t.Errorf("a second client behind the same proxy: %d", w.Code)
	}
	// A client that sends its own header from an address nobody trusts
	// cannot take a new address with each request.
	for i := range 2 {
		if w := from(h, "192.0.2.9:80", false, http.MethodGet, "/v1/version", xff, fmt.Sprintf("203.0.113.%d", i)); w.Code != http.StatusOK {
			t.Fatalf("untrusted peer %d: %d", i, w.Code)
		}
	}
	wantCode(t, from(h, "192.0.2.9:80", false, http.MethodGet, "/v1/version", xff, "203.0.113.50"), limen.CodeRateLimited)
}

// The authorizer is told the client address: a trusted proxy's forwarded
// address, and the peer's own address from anyone else.
func TestAuthorizerSeesTheClientAddress(t *testing.T) {
	trusted, err := clientip.ParseTrusted("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	az := authorizerFunc(func(_ context.Context, r authz.Request) (authz.Decision, error) {
		seen = r.Request.IP
		return authz.Decision{Allow: true}, nil
	})
	h := New(stub.New(stub.Options{}), Options{Authenticator: StaticToken(tok, admin), Authorizer: az, TrustedProxies: trusted})
	for _, c := range []struct{ peer, forwarded, want string }{
		{"10.0.0.1:80", "198.51.100.7", "198.51.100.7"},
		{"192.0.2.9:80", "198.51.100.7", "192.0.2.9"},
		{"10.0.0.1:80", "", "10.0.0.1"},
	} {
		seen = ""
		var headers []string
		if c.forwarded != "" {
			headers = []string{"X-Forwarded-For", c.forwarded}
		}
		if w := from(h, c.peer, true, http.MethodGet, "/v1/profiles?org=o", headers...); w.Code != http.StatusOK {
			t.Fatalf("%+v: %d %s", c, w.Code, w.Body.String())
		}
		if seen != c.want {
			t.Errorf("%+v: the authorizer saw %q", c, seen)
		}
	}
}

// deadlineWriter records the read deadline a handler sets, or fails to set
// it with err.
type deadlineWriter struct {
	http.ResponseWriter
	set time.Time
	err error
}

func (d *deadlineWriter) SetReadDeadline(t time.Time) error {
	d.set = t
	return d.err
}

// A replay extends its own read deadline to ReplayReadTimeout before it
// reads the body; a deadline that cannot be set is not served on.
func TestReplayExtendsItsReadDeadline(t *testing.T) {
	s := &spy{Service: stub.New(stub.Options{})}
	h := New(s, Local(tok, admin))
	replay := func(w *deadlineWriter) {
		hr := httptest.NewRequest(http.MethodPost, "/v1/replays", strings.NewReader(record(t, 1)+"\n"))
		hr.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(w, hr)
	}
	rec := httptest.NewRecorder()
	w := &deadlineWriter{ResponseWriter: rec}
	before := time.Now()
	replay(w)
	after := time.Now()
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body.String())
	}
	if w.set.Before(before.Add(ReplayReadTimeout)) || w.set.After(after.Add(ReplayReadTimeout)) {
		t.Errorf("read deadline %v, want %v after the request began", w.set, ReplayReadTimeout)
	}
	// A writer that cannot take deadlines at all, like the recorder, is served.
	if w := do(t, h, req{http.MethodPost, "/v1/replays", record(t, 2) + "\n"}); w.Code != http.StatusOK {
		t.Errorf("a writer with no deadlines: %d %s", w.Code, w.Body.String())
	}
	s.calls.Store(0)
	rec = httptest.NewRecorder()
	replay(&deadlineWriter{ResponseWriter: rec, err: errors.New("connection closed")})
	wantCode(t, rec, limen.CodeUnavailable)
	if s.calls.Load() != 0 {
		t.Error("a replay whose deadline could not be set reached the service")
	}
}

// A 5xx answers with the code, its message and the request id, and no
// detail: the text of a store error or a panic stays in the log.
func TestServerErrorsAnswerOnlyWithTheRequestID(t *testing.T) {
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	predict := req{http.MethodPost, "/v1/predictions", body(t, action(admin, "e1"))}
	for _, c := range []struct {
		name string
		svc  failing
		code limen.Code
		leak string
	}{
		{"store error", failing{err: errors.New("store: open /var/lib/limen/ledger.db: disk gone")}, limen.CodeUnavailable, "/var/lib/limen"},
		{"panic", failing{panic: true}, limen.CodeInternal, "panic: boom"},
		{"contract internal", failing{err: &limen.Error{Code: limen.CodeInternal, Detail: "usr_secret subject"}}, limen.CodeInternal, "usr_secret"},
	} {
		logged.Reset()
		h := New(c.svc, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}}, Logger: log})
		w := do(t, h, predict)
		wantCode(t, w, c.code)
		if strings.Contains(w.Body.String(), c.leak) {
			t.Errorf("%s: the body holds %q: %s", c.name, c.leak, w.Body.String())
		}
		e := errorOf(t, w)
		if e.Error.Details.Detail != "" || len(e.Error.Details.Fields) != 0 {
			t.Errorf("%s: detail %q fields %v", c.name, e.Error.Details.Detail, e.Error.Details.Fields)
		}
		if !strings.Contains(logged.String(), c.leak) || !strings.Contains(logged.String(), e.Error.Details.RequestID) {
			t.Errorf("%s: the log lacks the detail or the request id: %s", c.name, logged.String())
		}
	}
}

// A 4xx keeps its detail and fields.
func TestClientErrorsKeepTheirDetail(t *testing.T) {
	h := New(stub.New(stub.Options{}), Local(tok, admin))
	w := do(t, h, req{http.MethodPost, "/v1/predictions", `{"org":3}`})
	wantCode(t, w, limen.CodeInvalidAction, "org")
	if errorOf(t, w).Error.Details.Detail == "" {
		t.Error("a 4xx lost its detail")
	}
}

// countingBody is first, then an endless supply of one replay line, and
// counts the bytes a handler reads from it. It ends after 64 MiB.
type countingBody struct {
	first io.Reader
	line  []byte
	left  []byte
	n     atomic.Int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	if c.first != nil {
		n, err := c.first.Read(p)
		c.n.Add(int64(n))
		if err == io.EOF {
			c.first = nil
			err = nil
		}
		return n, err
	}
	if c.n.Load() > 64<<20 {
		return 0, io.EOF
	}
	if len(c.left) == 0 {
		c.left = c.line
	}
	n := copy(p, c.left)
	c.left = c.left[n:]
	c.n.Add(int64(n))
	return n, nil
}

func replayLine(t *testing.T, org, id string) string {
	t.Helper()
	a := action(alice, id)
	a.Org = org
	return body(t, limen.ReplayRecord{At: time.Unix(1, 0).UTC(), Action: a, Verdict: limen.Allow, Source: "rules/1"}) + "\n"
}

// A replay is refused at the first organization the caller may not replay
// for, having read only the lines up to it: a deny or an outage stops the
// read at once, and nothing is replayed.
func TestReplayAsksBeforeReadingTheBody(t *testing.T) {
	for _, c := range []struct {
		name string
		az   func(asked *[]string) authz.Authorizer
		code limen.Code
	}{
		{"deny", func(asked *[]string) authz.Authorizer {
			return authorizerFunc(func(_ context.Context, r authz.Request) (authz.Decision, error) {
				*asked = append(*asked, r.Resource.String("org"))
				return authz.Decision{Reason: "not_owner"}, nil
			})
		}, limen.CodeForbidden},
		{"outage", func(asked *[]string) authz.Authorizer {
			return authorizerFunc(func(_ context.Context, r authz.Request) (authz.Decision, error) {
				*asked = append(*asked, r.Resource.String("org"))
				return authz.Decision{}, errors.New("endpoint down")
			})
		}, limen.CodeAuthorizerUnavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked []string
			s := &spy{Service: stub.New(stub.Options{})}
			h := New(s, Options{Authenticator: StaticToken(tok, bob), Authorizer: c.az(&asked)})
			cb := &countingBody{first: strings.NewReader(replayLine(t, "org_a", "r0")), line: []byte(replayLine(t, "org_a", "rn"))}
			hr := httptest.NewRequest(http.MethodPost, "/v1/replays", cb)
			hr.Header.Set("Authorization", "Bearer "+tok)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, hr)
			wantCode(t, w, c.code)
			if got := cb.n.Load(); got > 1<<20 {
				t.Errorf("read %d bytes of the body before refusing", got)
			}
			if len(asked) != 1 || asked[0] != "org_a" {
				t.Errorf("asked about %v, want org_a once", asked)
			}
			if s.calls.Load() != 0 {
				t.Error("a refused replay reached the service")
			}
		})
	}
}

// A replay of two organizations whose second is denied replays nothing,
// and each organization is asked once, in the order its first record
// appears.
func TestReplayDeniedForALaterOrganization(t *testing.T) {
	var asked []string
	az := authorizerFunc(func(_ context.Context, r authz.Request) (authz.Decision, error) {
		org := r.Resource.String("org")
		asked = append(asked, org)
		return authz.Decision{Allow: org != "org_b"}, nil
	})
	s := &spy{Service: stub.New(stub.Options{})}
	h := New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: az})
	lines := replayLine(t, "org_a", "r1") + replayLine(t, "org_a", "r2") + replayLine(t, "org_b", "r3") + replayLine(t, "org_a", "r4")
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/replays", lines}), limen.CodeForbidden)
	if strings.Join(asked, ",") != "org_a,org_b" {
		t.Errorf("asked %v, want org_a then org_b", asked)
	}
	if s.calls.Load() != 0 {
		t.Error("a replay with a denied organization reached the service")
	}
	// Allowed for both: one question each, and every record replayed.
	asked = nil
	ok := New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: authorizerFunc(func(_ context.Context, r authz.Request) (authz.Decision, error) {
		asked = append(asked, r.Resource.String("org"))
		return authz.Decision{Allow: true}, nil
	})})
	if w := do(t, ok, req{http.MethodPost, "/v1/replays", lines}); w.Code != http.StatusOK {
		t.Fatalf("allowed replay: %d %s", w.Code, w.Body.String())
	}
	if strings.Join(asked, ",") != "org_a,org_b" || s.calls.Load() != 1 {
		t.Errorf("asked %v, service calls %d", asked, s.calls.Load())
	}
}

// A line that does not parse, or is not valid, is invalid_replay before
// anyone is asked about it, and a malformed line after an allowed first
// record still replays nothing.
func TestReplayValidatesTheLineBeforeAsking(t *testing.T) {
	var asked int
	az := authorizerFunc(func(context.Context, authz.Request) (authz.Decision, error) {
		asked++
		return authz.Decision{Reason: "not_owner"}, nil
	})
	h := New(stub.New(stub.Options{}), Options{Authenticator: StaticToken(tok, bob), Authorizer: az})
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/replays", "{not json\n" + replayLine(t, "org_a", "r1")}), limen.CodeInvalidReplay, "records[0]")
	if asked != 0 {
		t.Errorf("asked %d times about a malformed line", asked)
	}
	allow := New(&spy{Service: stub.New(stub.Options{})}, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}}})
	wantCode(t, do(t, allow, req{http.MethodPost, "/v1/replays", replayLine(t, "org_a", "r1") + "{not json\n"}), limen.CodeInvalidReplay, "records[1]")
}

// A forget with a subject past limen.MaxStringBytes is invalid_query
// before the authorizer is asked, so an oversized string never leaves the
// server.
func TestForgetValidatesLengthsBeforeAsking(t *testing.T) {
	var asked int
	az := authorizerFunc(func(context.Context, authz.Request) (authz.Decision, error) {
		asked++
		return authz.Decision{Allow: true}, nil
	})
	s := &spy{Service: stub.New(stub.Options{})}
	h := New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: az})
	for name, b := range map[string]forgetRequest{
		"long subject": {Org: "o", Subject: strings.Repeat("a", limen.MaxStringBytes+1)},
		"long org":     {Org: strings.Repeat("a", limen.MaxStringBytes+1), Subject: "s"},
	} {
		w := do(t, h, req{http.MethodPost, "/v1/subjects/forget", body(t, b)})
		field := "subject"
		if name == "long org" {
			field = "org"
		}
		wantCode(t, w, limen.CodeInvalidQuery, field)
		if asked != 0 || s.calls.Load() != 0 {
			t.Fatalf("%s: authorizer asked %d times, service called %d times", name, asked, s.calls.Load())
		}
	}
	ok := forgetRequest{Org: "o", Subject: strings.Repeat("a", limen.MaxStringBytes)}
	if w := do(t, h, req{http.MethodPost, "/v1/subjects/forget", body(t, ok)}); w.Code != http.StatusNoContent || asked != 1 {
		t.Errorf("256 bytes: %d, asked %d: %s", w.Code, asked, w.Body.String())
	}
}

// quotaService refuses every new person the way an engine at its cap does.
type quotaService struct{ limen.Service }

func (quotaService) Predict(context.Context, limen.Action) (limen.Prediction, error) {
	return limen.Prediction{}, &limen.Error{Code: limen.CodeQuotaExceeded, Detail: "the organization has 1 people, the most it may have"}
}

// An engine that refuses a new person is answered 403 quota_exceeded, and
// a 4xx keeps its detail.
func TestQuotaExceededIsForbiddenWithDetail(t *testing.T) {
	h := New(quotaService{stub.New(stub.Options{})}, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}}})
	w := do(t, h, req{http.MethodPost, "/v1/predictions", body(t, action(alice, "a1"))})
	wantCode(t, w, limen.CodeQuotaExceeded)
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", w.Code)
	}
	if d := errorOf(t, w).Error.Details.Detail; d != "the organization has 1 people, the most it may have" {
		t.Errorf("detail %q", d)
	}
}
