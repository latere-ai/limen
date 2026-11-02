// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goccy/go-yaml"

	"latere.ai/x/pkg/authz"
	azstub "latere.ai/x/pkg/authz/stub"

	"latere.ai/x/limen"
	"latere.ai/x/limen/authorizer"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/stub"
)

const (
	tok   = "secret"
	admin = "https://issuer.example|usr_admin"
	alice = "https://issuer.example|usr_alice"
	bob   = "https://issuer.example|usr_bob"
)

// spy counts the calls that reach the service.
type spy struct {
	limen.Service
	calls atomic.Int64
}

func (s *spy) Predict(ctx context.Context, a limen.Action) (limen.Prediction, error) {
	s.calls.Add(1)
	return s.Service.Predict(ctx, a)
}
func (s *spy) Record(ctx context.Context, d limen.Decision) error {
	s.calls.Add(1)
	return s.Service.Record(ctx, d)
}
func (s *spy) Answer(ctx context.Context, a limen.Answer) error {
	s.calls.Add(1)
	return s.Service.Answer(ctx, a)
}
func (s *spy) Cases(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
	s.calls.Add(1)
	return s.Service.Cases(ctx, q)
}
func (s *spy) Reliability(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
	s.calls.Add(1)
	return s.Service.Reliability(ctx, q)
}
func (s *spy) Profile(ctx context.Context, sc limen.Scope) (limen.Profile, error) {
	s.calls.Add(1)
	return s.Service.Profile(ctx, sc)
}
func (s *spy) PutProfile(ctx context.Context, p limen.Profile) (limen.Profile, error) {
	s.calls.Add(1)
	return s.Service.PutProfile(ctx, p)
}
func (s *spy) DeleteProfile(ctx context.Context, sc limen.Scope) error {
	s.calls.Add(1)
	return s.Service.DeleteProfile(ctx, sc)
}
func (s *spy) Report(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
	s.calls.Add(1)
	return s.Service.Report(ctx, q)
}
func (s *spy) Replay(ctx context.Context, r iter.Seq2[limen.ReplayRecord, error], o limen.ReplayOptions) (limen.ReplayReport, error) {
	s.calls.Add(1)
	return s.Service.Replay(ctx, r, o)
}
func (s *spy) Forget(ctx context.Context, org, subject string) error {
	s.calls.Add(1)
	return s.Service.Forget(ctx, org, subject)
}
func (s *spy) Explain(ctx context.Context, k limen.Key) (limen.Explanation, error) {
	s.calls.Add(1)
	return s.Service.Explain(ctx, k)
}
func (s *spy) Learned(ctx context.Context, q limen.LearnedQuery) (limen.Learned, error) {
	s.calls.Add(1)
	return s.Service.Learned(ctx, q)
}
func (s *spy) Preview(ctx context.Context, r limen.PreviewRequest) (limen.Preview, error) {
	s.calls.Add(1)
	return s.Service.Preview(ctx, r)
}

func action(subject, id string) limen.Action {
	return limen.Action{Org: "o", Subject: subject, ActionID: id, Kind: "tool", Name: "bash", Effect: limen.EffectWrite,
		Input: json.RawMessage(`{"command":"ls"}`)}
}

func body(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type req struct {
	method, target, body string
}

// do sends one request as the bearer of tok.
func do(t *testing.T, h http.Handler, r req, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	hr := httptest.NewRequest(r.method, r.target, strings.NewReader(r.body))
	hr.Header.Set("Authorization", "Bearer "+tok)
	for i := 0; i+1 < len(headers); i += 2 {
		hr.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hr)
	return w
}

// errorOf reads the envelope of an error answer.
func errorOf(t *testing.T, w *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("status %d, body %q is not the envelope: %v", w.Code, w.Body.String(), err)
	}
	if e.Error.Message != e.Error.Code.Message() || e.Error.Details.RequestID == "" {
		t.Errorf("envelope %+v", e)
	}
	return e
}

func wantCode(t *testing.T, w *httptest.ResponseRecorder, code limen.Code, fields ...string) {
	t.Helper()
	if w.Code != code.Status() {
		t.Fatalf("status %d, want %d (%s): %s", w.Code, code.Status(), code, w.Body.String())
	}
	e := errorOf(t, w)
	if e.Error.Code != code {
		t.Fatalf("code %q, want %q", e.Error.Code, code)
	}
	for _, f := range fields {
		if !slices.Contains(e.Error.Details.Fields, f) {
			t.Errorf("fields %v lack %q", e.Error.Details.Fields, f)
		}
	}
}

// valid is one successful request per route, against a service where
// action "seed" for alice is predicted and decided as an ask.
func valid(t *testing.T) []req {
	a := action(alice, "a1")
	now := time.Now().UTC()
	rec := limen.ReplayRecord{At: now, Action: action(alice, "r1"), Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1"}
	return []req{
		{http.MethodPost, "/v1/predictions", body(t, a)},
		{http.MethodPost, "/v1/decisions", body(t, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1", Action: &a})},
		{http.MethodPost, "/v1/answers", body(t, limen.Answer{Org: "o", Subject: alice, ActionID: "seed", Approve: true})},
		{http.MethodGet, "/v1/cases?org=o&subject=" + alice + "&pending=false&limit=10", ""},
		{http.MethodGet, "/v1/reliability?org=o&subject=" + alice + "&of=applied&from=" + now.Add(-time.Hour).Format(time.RFC3339) + "&to=" + now.Add(time.Hour).Format(time.RFC3339), ""},
		{http.MethodGet, "/v1/profiles?org=o&kind=tool&agent=x", ""},
		{http.MethodPut, "/v1/profiles", body(t, limen.Profile{Org: "o", Kind: "tool", Costs: limen.DefaultCosts, AuditRate: 0.05})},
		{http.MethodPost, "/v1/replays?keep=false", body(t, rec) + "\n\n"},
		{http.MethodPost, "/v1/subjects/forget", body(t, map[string]string{"org": "o", "subject": bob})},
		{http.MethodGet, "/v1/explanations?org=o&subject=" + alice + "&action_id=seed", ""},
		{http.MethodGet, "/v1/learned?org=o&subject=" + alice + "&kind=tool", ""},
		{http.MethodPost, "/v1/previews", body(t, preview(alice, now))},
		{http.MethodDelete, "/v1/profiles?org=o&agent=x", ""},
		{http.MethodGet, "/v1/reports?org=o&from=2026-09-28T00:00:00Z&to=2026-10-12T00:00:00Z", ""},
		{http.MethodGet, "/v1/profiles?org=o&subject=" + alice, ""},
		{http.MethodDelete, "/v1/profiles?org=o&subject=" + alice, ""},
	}
}

// preview is a valid preview request for subject around now.
func preview(subject string, now time.Time) limen.PreviewRequest {
	return limen.PreviewRequest{Org: "o", Subject: subject, From: now.Add(-time.Hour), To: now.Add(time.Hour), Costs: limen.DefaultCosts, AuditRate: 0.05}
}

// seeded returns a service holding alice's decided seed action.
func seeded(t *testing.T, svc limen.Service) limen.Service {
	t.Helper()
	a := action(alice, "seed")
	if _, err := svc.Predict(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := svc.Record(context.Background(), limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1"}); err != nil {
		t.Fatal(err)
	}
	return svc
}

func services(t *testing.T) map[string]limen.Service {
	e := engine.New(engine.Options{Inline: 1})
	t.Cleanup(func() { e.Close() })
	return map[string]limen.Service{"stub": stub.New(stub.Options{}), "engine": e}
}

// Every route answers its success status against the stub and the engine.
func TestRoutesServe(t *testing.T) {
	for name, svc := range services(t) {
		h := New(seeded(t, svc), Local(tok, admin))
		for _, r := range valid(t) {
			w := do(t, h, r)
			if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
				t.Errorf("%s %s %s: %d %s", name, r.method, r.target, w.Code, w.Body.String())
			}
			if w.Header().Get(RequestIDHeader) == "" {
				t.Errorf("%s %s: no request id", name, r.target)
			}
		}
	}
}

// The served routes are exactly the documented ones.
func TestRoutesMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var documented, served []string
	for path, ops := range doc.Paths {
		for method, op := range ops {
			documented = append(documented, strings.ToUpper(method)+" "+path)
			action := fmt.Sprint(op.(map[string]any)["x-limen-action"])
			i := slices.IndexFunc(routes, func(r route) bool { return r.method == strings.ToUpper(method) && r.path == path })
			if i >= 0 && routes[i].action != action {
				t.Errorf("%s %s asks %s, the document says %s", method, path, routes[i].action, action)
			}
		}
	}
	for _, r := range routes {
		served = append(served, r.method+" "+r.path)
	}
	slices.Sort(documented)
	slices.Sort(served)
	if !slices.Equal(documented, served) {
		t.Errorf("documented %v\nserved     %v", documented, served)
	}
}

// Each route asks exactly its action about exactly spec 003's resource.
func TestAuthorizerQuestions(t *testing.T) {
	az := azstub.New(t, azstub.WithVocabulary(authorizer.Vocabulary()))
	svc := seeded(t, stub.New(stub.Options{}))
	want := []struct {
		action, kind string
		fields       map[string]any
	}{
		{authorizer.ActionCasePredict, authorizer.KindCase, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionCaseDecide, authorizer.KindCase, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionCaseAnswer, authorizer.KindCase, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionCaseList, authorizer.KindCase, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionReliabilityRead, authorizer.KindReliability, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionProfileRead, authorizer.KindProfile, map[string]any{"org": "o", "action_kind": "tool", "agent": "x"}},
		{authorizer.ActionProfileWrite, authorizer.KindProfile, map[string]any{"org": "o", "action_kind": "tool", "agent": ""}},
		{authorizer.ActionReplayCreate, authorizer.KindReplay, map[string]any{"org": "o"}},
		{authorizer.ActionSubjectForget, authorizer.KindSubject, map[string]any{"org": "o", "subject": bob}},
		{authorizer.ActionCaseList, authorizer.KindCase, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionCaseList, authorizer.KindCase, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionReliabilityRead, authorizer.KindReliability, map[string]any{"org": "o", "subject": alice}},
		{authorizer.ActionProfileWrite, authorizer.KindProfile, map[string]any{"org": "o", "action_kind": "", "agent": "x"}},
		{authorizer.ActionReportRead, authorizer.KindReport, map[string]any{"org": "o"}},
		{authorizer.ActionProfileRead, authorizer.KindProfile, map[string]any{"org": "o", "action_kind": "", "agent": "", "subject": alice}},
		{authorizer.ActionProfileWrite, authorizer.KindProfile, map[string]any{"org": "o", "action_kind": "", "agent": "", "subject": alice}},
	}
	if len(want) != len(valid(t)) {
		t.Fatalf("%d questions for %d routes", len(want), len(valid(t)))
	}
	for i, r := range valid(t) {
		// A fresh client per route, since a client caches its decisions
		// and routes share questions.
		client, err := authz.NewClient(authz.Options{URL: az.URL(), Token: az.Token(), HTTP: &http.Client{}, Vocabulary: authorizer.Vocabulary()})
		if err != nil {
			t.Fatal(err)
		}
		h := New(svc, Options{Authenticator: StaticToken(tok, admin), Authorizer: client})
		az.ClearRequests()
		if w := do(t, h, r); w.Code >= 300 {
			t.Fatalf("%s: %d %s", r.target, w.Code, w.Body.String())
		}
		got := az.Requests()
		if len(got) != 1 {
			t.Fatalf("%s asked %d questions", r.target, len(got))
		}
		q := got[0]
		if q.Action != want[i].action || q.Resource.Kind != want[i].kind || q.Subject != admin || q.Request.ID == "" {
			t.Errorf("%s asked %+v", r.target, q)
		}
		if len(q.Resource.Fields) != len(want[i].fields) {
			t.Errorf("%s fields %v, want %v", r.target, q.Resource.Fields, want[i].fields)
		}
		for k, v := range want[i].fields {
			if q.Resource.Fields[k] != v {
				t.Errorf("%s field %s = %v, want %v", r.target, k, q.Resource.Fields[k], v)
			}
		}
	}
}

// A deny is forbidden and an outage authorizer_unavailable, and neither
// reaches the service.
func TestRefusalsActOnNothing(t *testing.T) {
	az := azstub.New(t)
	client, err := authz.NewClient(authz.Options{URL: az.URL(), Token: az.Token(), HTTP: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	s := &spy{Service: seeded(t, stub.New(stub.Options{}))}
	s.calls.Store(0)
	h := New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: client})
	az.Deny(azstub.Rule{}, "no")
	for _, r := range valid(t) {
		wantCode(t, do(t, h, r), limen.CodeForbidden)
	}
	// A fresh client, since the first one caches the denies.
	az.Fail(http.StatusInternalServerError)
	client, err = authz.NewClient(authz.Options{URL: az.URL(), Token: az.Token(), HTTP: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	h = New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: client})
	for _, r := range valid(t) {
		wantCode(t, do(t, h, r), limen.CodeAuthorizerUnavailable)
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("refused requests reached the service %d times", n)
	}
}

// Under the owner policy a caller acts for its own subject only, and only
// the admin sets profiles and replays history.
func TestSubjectBinding(t *testing.T) {
	h := New(seeded(t, stub.New(stub.Options{})), Options{Authenticator: StaticToken(tok, bob), Authorizer: OwnerPolicy{Admins: []string{admin}}})
	for _, r := range valid(t) {
		w := do(t, h, r)
		if r.target == "/v1/subjects/forget" {
			if w.Code != http.StatusNoContent {
				t.Errorf("bob forgetting himself: %d", w.Code)
			}
			continue
		}
		wantCode(t, w, limen.CodeForbidden)
	}
	own := action(bob, "b1")
	if w := do(t, h, req{http.MethodPost, "/v1/predictions", body(t, own)}); w.Code != http.StatusOK {
		t.Errorf("bob predicting for himself: %d %s", w.Code, w.Body.String())
	}
}

// A relay names who answered; anyone else answers as themselves.
func TestAnswerBy(t *testing.T) {
	svc := stub.New(stub.Options{})
	for _, id := range []string{"x1", "x2", "x3"} {
		a := action(alice, id)
		if _, err := svc.Predict(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		if err := svc.Record(context.Background(), limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	relay := New(svc, Local(tok, admin))
	self := New(svc, Options{Authenticator: StaticToken(tok, alice), Authorizer: OwnerPolicy{}})
	answer := func(h http.Handler, id, by string) {
		t.Helper()
		w := do(t, h, req{http.MethodPost, "/v1/answers", body(t, limen.Answer{Org: "o", Subject: alice, ActionID: id, Approve: true, By: by})})
		if w.Code != http.StatusNoContent {
			t.Fatalf("answer %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	answer(relay, "x1", "https://issuer.example|usr_lead")
	answer(relay, "x2", "")
	answer(self, "x3", "https://issuer.example|usr_forged")
	want := map[string]string{"x1": "https://issuer.example|usr_lead", "x2": admin, "x3": alice}
	page, err := svc.Cases(context.Background(), limen.CaseQuery{Org: "o", Subject: alice})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range page.Entries {
		if w, ok := want[c.Action.ActionID]; ok && c.Answers[0].By != w {
			t.Errorf("%s answered by %q, want %q", c.Action.ActionID, c.Answers[0].By, w)
		}
	}
}

func record(t *testing.T, i int) string {
	t.Helper()
	return body(t, limen.ReplayRecord{At: time.Unix(int64(i), 0).UTC(), Action: action(alice, fmt.Sprintf("r%d", i)),
		Verdict: limen.Allow, ReviewProbability: 0, Source: "rules/1"})
}

func TestReplayLimits(t *testing.T) {
	s := &spy{Service: stub.New(stub.Options{})}
	h := New(s, Local(tok, admin))
	line := record(t, 0) + "\n"
	var b strings.Builder
	for range limen.MaxReplayRecords + 1 {
		b.WriteString(line)
	}
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/replays", b.String()}), limen.CodeBodyTooLarge)
	if s.calls.Load() != 0 {
		t.Error("an oversized replay reached the service")
	}
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/replays", record(t, 1) + "\n{not json\n"}), limen.CodeInvalidReplay, "records[1]")
	bad := limen.ReplayRecord{At: time.Now(), Action: limen.Action{Org: "o", Subject: alice, ActionID: "z"}, Verdict: limen.Ask, ReviewProbability: 1, Source: "s"}
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/replays", body(t, bad)}), limen.CodeInvalidReplay, "records[0].action.kind")
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/replays?keep=maybe", line}), limen.CodeInvalidReplay, "keep")
	small := New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}}, MaxBodyBytes: 64})
	wantCode(t, do(t, small, req{http.MethodPost, "/v1/replays", line}), limen.CodeBodyTooLarge)
	tiny := New(s, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}}, MaxReplayBytes: 100})
	wantCode(t, do(t, tiny, req{http.MethodPost, "/v1/replays", line + line}), limen.CodeBodyTooLarge)
	if s.calls.Load() != 0 {
		t.Error("a refused replay reached the service")
	}
	if w := do(t, h, req{http.MethodPost, "/v1/replays?keep=true", ""}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"would":{}`) {
		t.Errorf("an empty replay: %d %s", w.Code, w.Body.String())
	}
}

func TestDecoding(t *testing.T) {
	h := New(stub.New(stub.Options{}), Local(tok, admin))
	extra := strings.Replace(body(t, action(alice, "u1")), `{`, `{"from_the_future":1,`, 1)
	if w := do(t, h, req{http.MethodPost, "/v1/predictions", extra}); w.Code != http.StatusOK {
		t.Errorf("an unknown field: %d %s", w.Code, w.Body.String())
	}
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/predictions", "{"}), limen.CodeInvalidAction)
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/predictions", `{"effect":3}`}), limen.CodeInvalidAction, "effect")
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/predictions", body(t, limen.Action{})}), limen.CodeInvalidAction, "org", "kind")
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/decisions", "[]"}), limen.CodeInvalidDecision)
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/decisions", "{}"}), limen.CodeInvalidDecision, "verdict")
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/answers", "{}"}), limen.CodeInvalidAnswer, "org")
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/answers", "x"}), limen.CodeInvalidAnswer)
	wantCode(t, do(t, h, req{http.MethodPut, "/v1/profiles", "x"}), limen.CodeInvalidProfile)
	wantCode(t, do(t, h, req{http.MethodPut, "/v1/profiles", body(t, limen.Profile{Org: "o", Costs: limen.Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1}})}), limen.CodeAskTooCostly)
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/subjects/forget", "x"}), limen.CodeInvalidQuery)
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/subjects/forget", "{}"}), limen.CodeInvalidQuery, "org", "subject")
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/previews", "x"}), limen.CodeInvalidQuery)
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/previews", `{"audit_rate":2}`}), limen.CodeInvalidQuery, "org", "subject", "from", "audit_rate")
	costly := preview(alice, time.Now())
	costly.Costs = limen.Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1}
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/previews", body(t, costly)}), limen.CodeAskTooCostly)
	late := preview(alice, time.Now())
	late.To = late.From
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/previews", body(t, late)}), limen.CodeInvalidQuery, "to")
	big := `{"org":"` + strings.Repeat("x", DefaultMaxBodyBytes) + `"}`
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/predictions", big}), limen.CodeBodyTooLarge)
}

func TestQueryErrors(t *testing.T) {
	h := New(stub.New(stub.Options{}), Local(tok, admin))
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/cases?org=o&subject=s&limit=0", ""}), limen.CodeInvalidQuery, "limit")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/cases?org=o&subject=s&limit=x&pending=maybe", ""}), limen.CodeInvalidQuery, "limit", "pending")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/cases?subject=s&limit=501", ""}), limen.CodeInvalidQuery, "org", "limit")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/reliability?org=o&subject=s&of=learned&from=yesterday&to=2026-10-02T00:00:00Z", ""}), limen.CodeInvalidQuery, "from")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/reliability?org=o&subject=s&of=learned", ""}), limen.CodeInvalidQuery, "from")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/profiles", ""}), limen.CodeInvalidQuery, "org")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/explanations?org=o", ""}), limen.CodeInvalidQuery, "subject", "action_id")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/explanations?org=o&subject=s&action_id=none", ""}), limen.CodeNoCase, "action_id")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/learned?subject=s", ""}), limen.CodeInvalidQuery, "org")
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		wantCode(t, do(t, h, req{method, "/v1/profiles?kind=tool", ""}), limen.CodeInvalidQuery, "org")
		wantCode(t, do(t, h, req{method, "/v1/profiles?org=o&subject=s&kind=tool&agent=x", ""}), limen.CodeInvalidQuery, "kind", "agent")
		wantCode(t, do(t, h, req{method, "/v1/profiles?org=" + alice + "&subject=" + bob, ""}), limen.CodeInvalidQuery, "subject")
	}
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/reports", ""}), limen.CodeInvalidQuery, "org", "from", "to")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/reports?org=o&from=monday&to=2026-10-12T00:00:00Z", ""}), limen.CodeInvalidQuery, "from")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/reports?org=o&from=2026-10-06T00:00:00Z&to=2026-10-12T00:00:00Z", ""}), limen.CodeInvalidQuery, "from")
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/reports?org=o&from=2026-10-05T00:00:00Z&to=2027-01-11T00:00:00Z", ""}), limen.CodeInvalidQuery, "to")
}

// A person's own profile in an organization is theirs to read, save and
// remove under the owner policy, and an admin's for every person; nobody
// else reads or saves it (spec 010, criterion 6).
func TestPersonProfileOwnership(t *testing.T) {
	svc := stub.New(stub.Options{})
	as := func(who string) http.Handler {
		return New(svc, Options{Authenticator: StaticToken(tok, who), Authorizer: OwnerPolicy{Admins: []string{admin}}})
	}
	own := func(who string) limen.Profile {
		return limen.Profile{Scope: limen.Scope{Org: "o", Subject: who}, Costs: limen.Costs{FalseAllow: 50, FalseBlock: 4, Ask: 1}, AuditRate: 0.1}
	}
	bobs := as(bob)
	if w := do(t, bobs, req{http.MethodPut, "/v1/profiles", body(t, own(bob))}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"subject":"`+bob+`"`) {
		t.Errorf("bob saving his own profile: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, bobs, req{http.MethodGet, "/v1/profiles?org=o&subject=" + bob, ""}); w.Code != http.StatusOK {
		t.Errorf("bob reading his own profile: %d %s", w.Code, w.Body.String())
	}
	for _, r := range []req{
		{http.MethodPut, "/v1/profiles", body(t, own(alice))},
		{http.MethodGet, "/v1/profiles?org=o&subject=" + alice, ""},
		{http.MethodDelete, "/v1/profiles?org=o&subject=" + alice, ""},
		{http.MethodGet, "/v1/profiles?org=o", ""},
		{http.MethodDelete, "/v1/profiles?org=o", ""},
	} {
		wantCode(t, do(t, bobs, r), limen.CodeForbidden)
	}
	admins := as(admin)
	if w := do(t, admins, req{http.MethodPut, "/v1/profiles", body(t, own(alice))}); w.Code != http.StatusOK {
		t.Errorf("an admin saving alice's profile: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, admins, req{http.MethodDelete, "/v1/profiles?org=o&subject=" + alice, ""}); w.Code != http.StatusNoContent {
		t.Errorf("an admin removing alice's profile: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, bobs, req{http.MethodDelete, "/v1/profiles?org=o&subject=" + bob, ""}); w.Code != http.StatusNoContent {
		t.Errorf("bob removing his own profile: %d %s", w.Code, w.Body.String())
	}
	p, err := svc.Profile(context.Background(), limen.Scope{Org: "o", Subject: bob})
	if err != nil || p.Subject != "" || !p.Default {
		t.Errorf("after the removal bob reads %+v, %v; want the organization's default", p, err)
	}
	// A resource never names two subjects: in a person's own context the
	// subject is the org.
	if got := profileResource(limen.Scope{Org: bob, Subject: bob}); got.String("subject") != bob || got.ID != bob+"///"+bob {
		t.Errorf("own context, own profile: %+v", got)
	}
}

// Only an admin reads the report of an organization under the owner
// policy, also a person who belongs to it (spec 010, criterion 14).
func TestReportOwnerPolicy(t *testing.T) {
	r := req{http.MethodGet, "/v1/reports?org=o&from=2026-09-28T00:00:00Z&to=2026-10-05T00:00:00Z", ""}
	svc := seeded(t, stub.New(stub.Options{}))
	wantCode(t, do(t, New(svc, Options{Authenticator: StaticToken(tok, alice), Authorizer: OwnerPolicy{Admins: []string{admin}}}), r), limen.CodeForbidden)
	w := do(t, New(svc, Local(tok, admin)), r)
	var got limen.Report
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Weeks) != 1 || got.MinSubjects != limen.ReportMinSubjects {
		t.Errorf("an admin reading the report: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), alice) {
		t.Errorf("the report names a person: %s", w.Body.String())
	}
}

// With a base path every route is served under it as well, with the same
// permissions and the same answers, and /v1 stays.
func TestBasePath(t *testing.T) {
	for _, bad := range []string{"v1/oversight", "/v1/oversight/", "/", "/v1", "/a b", "/a{x}"} {
		if CheckBasePath(bad) == nil {
			t.Errorf("CheckBasePath(%q) = nil", bad)
		}
	}
	for _, good := range []string{"", "/v1/oversight", "/limen"} {
		if err := CheckBasePath(good); err != nil {
			t.Errorf("CheckBasePath(%q) = %v", good, err)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("New took the base path /v1/")
			}
		}()
		New(stub.New(stub.Options{}), Options{BasePath: "/v1/"})
	}()

	h := New(seeded(t, stub.New(stub.Options{})), Options{Authenticator: StaticToken(tok, bob), Authorizer: OwnerPolicy{Admins: []string{admin}}, BasePath: "/v1/oversight"})
	for _, r := range valid(t) {
		under := r
		under.target = "/v1/oversight" + strings.TrimPrefix(r.target, "/v1")
		w, wu := do(t, h, r), do(t, h, under)
		if w.Code != wu.Code {
			t.Errorf("%s %s: %d under /v1, %d under the base path: %s", r.method, under.target, w.Code, wu.Code, wu.Body.String())
		}
	}
	own := "/oversight/explanations?org=o&subject=" + bob + "&action_id=none"
	wantCode(t, do(t, h, req{http.MethodGet, "/v1" + own, ""}), limen.CodeNoCase)
	if w := do(t, h, req{http.MethodGet, own, ""}); w.Code != http.StatusNotFound {
		t.Errorf("%s without /v1: %d", own, w.Code)
	}

	h = New(seeded(t, stub.New(stub.Options{})), Options{Authenticator: StaticToken(tok, alice), Authorizer: OwnerPolicy{}, BasePath: "/v1/oversight"})
	for _, path := range []string{"/v1/explanations", "/v1/oversight/explanations"} {
		w := do(t, h, req{http.MethodGet, path + "?org=o&subject=" + alice + "&action_id=seed", ""})
		var x limen.Explanation
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &x) != nil || x.ActionID != "seed" || x.Verdict != limen.Ask {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// A profile of a person's own context, an org that is their rendered
// subject, is theirs to read and write; an organization's profiles and every
// replay stay the admins'.
func TestOwnContextProfile(t *testing.T) {
	h := New(stub.New(stub.Options{}), Options{Authenticator: StaticToken(tok, bob), Authorizer: OwnerPolicy{Admins: []string{admin}}})
	mine := limen.Profile{Org: bob, Kind: "tool", Costs: limen.DefaultCosts, AuditRate: 0.05}
	if w := do(t, h, req{http.MethodPut, "/v1/profiles", body(t, mine)}); w.Code != http.StatusOK {
		t.Errorf("bob writing his own context's profile: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, req{http.MethodGet, "/v1/profiles?org=" + bob + "&kind=tool", ""}); w.Code != http.StatusOK {
		t.Errorf("bob reading his own context's profile: %d %s", w.Code, w.Body.String())
	}
	theirs := mine
	theirs.Org = alice
	wantCode(t, do(t, h, req{http.MethodPut, "/v1/profiles", body(t, theirs)}), limen.CodeForbidden)
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/profiles?org=" + alice, ""}), limen.CodeForbidden)
	org := mine
	org.Org = "o"
	wantCode(t, do(t, h, req{http.MethodPut, "/v1/profiles", body(t, org)}), limen.CodeForbidden)
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/profiles?org=o", ""}), limen.CodeForbidden)
	rec := limen.ReplayRecord{At: time.Now().UTC(), Action: limen.Action{Org: bob, Subject: bob, ActionID: "r1", Kind: "tool", Name: "bash", Effect: limen.EffectWrite},
		Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1"}
	wantCode(t, do(t, h, req{http.MethodPost, "/v1/replays?keep=false", body(t, rec) + "\n"}), limen.CodeForbidden)

	if got := profileResource(limen.Scope{Org: bob}).String("subject"); got != bob {
		t.Errorf("own context profile subject %q", got)
	}
	if got := profileResource(limen.Scope{Org: "o"}).String("subject"); got != "" {
		t.Errorf("organization profile subject %q", got)
	}
}

// failing answers every call with err, or panics.
type failing struct {
	limen.Service
	err   error
	panic bool
}

func (f failing) Predict(context.Context, limen.Action) (limen.Prediction, error) {
	if f.panic {
		panic("boom")
	}
	return limen.Prediction{}, f.err
}

func (f failing) Profile(context.Context, limen.Scope) (limen.Profile, error) {
	return limen.Profile{}, f.err
}

func TestErrorMapping(t *testing.T) {
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	predict := req{http.MethodPost, "/v1/predictions", body(t, action(admin, "e1"))}
	cases := []struct {
		svc  failing
		code limen.Code
	}{
		{failing{err: errors.New("store: down")}, limen.CodeUnavailable},
		{failing{err: context.DeadlineExceeded}, limen.CodeUnavailable},
		{failing{err: &limen.Error{Code: limen.CodeInternal, Detail: "bug"}}, limen.CodeInternal},
		{failing{err: &limen.Error{Code: limen.CodeActionConflict, Fields: []string{"action_id"}}}, limen.CodeActionConflict},
		{failing{panic: true}, limen.CodeInternal},
	}
	for _, c := range cases {
		h := New(c.svc, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}}, Logger: log})
		wantCode(t, do(t, h, predict), c.code)
	}
	if !strings.Contains(logged.String(), "store: down") || !strings.Contains(logged.String(), "panic: boom") {
		t.Errorf("5xx answers were not logged: %s", logged.String())
	}
	logged.Reset()
	h := New(failing{err: context.Canceled}, Options{Authenticator: StaticToken(tok, admin), Authorizer: OwnerPolicy{Admins: []string{admin}}, Logger: log})
	wantCode(t, do(t, h, req{http.MethodGet, "/v1/profiles?org=o", ""}), limen.CodeUnavailable)
	if logged.Len() != 0 {
		t.Errorf("a canceled request was logged: %s", logged.String())
	}
}

func TestAuthentication(t *testing.T) {
	svc := stub.New(stub.Options{})
	profile := req{http.MethodGet, "/v1/profiles?org=o", ""}
	wantCode(t, do(t, New(svc, Options{}), profile), limen.CodeUnauthenticated)
	h := New(svc, Local(tok, admin))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/profiles?org=o", nil))
	wantCode(t, w, limen.CodeUnauthenticated)
	wrong := httptest.NewRequest(http.MethodGet, "/v1/profiles?org=o", nil)
	wrong.Header.Set("Authorization", "Bearer nope")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, wrong)
	wantCode(t, w, limen.CodeUnauthenticated)
	wantCode(t, do(t, New(svc, Options{Authenticator: StaticToken("", admin)}), profile), limen.CodeUnauthenticated)
	nobody := AuthenticatorFunc(func(*http.Request) (Caller, error) { return Caller{}, nil })
	wantCode(t, do(t, New(svc, Options{Authenticator: nobody}), profile), limen.CodeUnauthenticated)
	if c, err := StaticToken(tok, admin).Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); !errors.Is(err, ErrNoBearer) || c.Subject != "" {
		t.Errorf("no bearer: %+v, %v", c, err)
	}
}

func jwtOf(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"ES256"}`)) + "." + payload + "." + enc([]byte("sig"))
}

func TestJWT(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	good := jwtOf(enc([]byte(`{"iss":"https://issuer.example","sub":"usr_alice","org_id":"o"}`)))
	verifier := VerifierFunc(func(raw string) (string, string, error) {
		switch raw {
		case "refused":
			return "", "", errors.New("signature")
		case "nosub":
			return "https://issuer.example", "", nil
		}
		return "https://issuer.example", "usr_alice", nil
	})
	a := JWT(verifier)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+good)
	c, err := a.Authenticate(r)
	if err != nil || c.Subject != alice || c.Issuer != "https://issuer.example" || c.Sub != "usr_alice" || c.Claims["org_id"] != "o" {
		t.Errorf("%+v, %v", c, err)
	}
	for _, raw := range []string{"refused", "nosub", "a.b", jwtOf("!!"), jwtOf(enc([]byte(`[1]`)))} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		if _, err := a.Authenticate(r); err == nil {
			t.Errorf("%q authenticated", raw)
		}
	}
	if _, err := a.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); !errors.Is(err, ErrNoBearer) {
		t.Errorf("no bearer: %v", err)
	}
	h := New(stub.New(stub.Options{}), Options{Authenticator: a, Authorizer: OwnerPolicy{}})
	hr := httptest.NewRequest(http.MethodGet, "/v1/cases?org=o&subject="+alice, nil)
	hr.Header.Set("Authorization", "Bearer "+good)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hr)
	if w.Code != http.StatusOK {
		t.Errorf("alice listing her own cases with a JWT: %d %s", w.Code, w.Body.String())
	}
}

func TestOwnerPolicy(t *testing.T) {
	p := OwnerPolicy{Admins: []string{admin}}
	ask := func(subject string, res authz.Resource) bool {
		d, err := p.Authorize(context.Background(), authz.Request{Subject: subject, Action: authorizer.ActionCaseList, Resource: res})
		if err != nil {
			t.Fatal(err)
		}
		return d.Allow
	}
	if !ask(alice, caseResource("o", alice)) || ask(bob, caseResource("o", alice)) || !ask(admin, caseResource("o", alice)) {
		t.Error("case ownership")
	}
	if ask(alice, profileResource(limen.Scope{Org: "o"})) || !ask(admin, replayResource("o")) || ask("", caseResource("o", "")) {
		t.Error("admin-only resources")
	}
	if ask(alice, reportResource("o")) || !ask(admin, reportResource("o")) {
		t.Error("the report of an organization is an admin's")
	}
	if !ask(alice, profileResource(limen.Scope{Org: "o", Subject: alice})) || ask(bob, profileResource(limen.Scope{Org: "o", Subject: alice})) {
		t.Error("a person's own profile is theirs")
	}
	if ask(admin, authz.NewResource(authorizer.KindCase, authz.ProbeID, nil)) {
		t.Error("the probe was allowed")
	}
	if !ask(bob, subjectResource("o", bob)) || ask(bob, reliabilityResource("o", alice)) {
		t.Error("subject and reliability ownership")
	}
}

func TestRequestID(t *testing.T) {
	h := New(stub.New(stub.Options{}), Local(tok, admin))
	profile := req{http.MethodGet, "/v1/profiles?org=o", ""}
	if got := do(t, h, profile, RequestIDHeader, "trace-123").Header().Get(RequestIDHeader); got != "trace-123" {
		t.Errorf("a caller's id was replaced: %q", got)
	}
	for _, bad := range []string{strings.Repeat("x", 200), "with space", "tab\tid"} {
		if got := do(t, h, profile, RequestIDHeader, bad).Header().Get(RequestIDHeader); got == bad || len(got) != 24 {
			t.Errorf("id %q kept as %q", bad, got)
		}
	}
	if got := do(t, h, profile).Header().Get(RequestIDHeader); len(got) != 24 {
		t.Errorf("minted id %q", got)
	}
}

// writeJSON answers 500 when a value cannot be encoded.
func TestWriteJSONFailure(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]any{"f": func() {}})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status %d", w.Code)
	}
}

// The remote address without a port is used as it is.
func TestAuthorizeAddress(t *testing.T) {
	var seen string
	az := authorizerFunc(func(_ context.Context, r authz.Request) (authz.Decision, error) {
		seen = r.Request.IP
		return authz.Decision{Allow: true}, nil
	})
	h := New(stub.New(stub.Options{}), Options{Authenticator: StaticToken(tok, admin), Authorizer: az})
	hr := httptest.NewRequest(http.MethodGet, "/v1/profiles?org=o", nil)
	hr.Header.Set("Authorization", "Bearer "+tok)
	hr.RemoteAddr = "unix"
	h.ServeHTTP(httptest.NewRecorder(), hr)
	if seen != "unix" {
		t.Errorf("ip %q", seen)
	}
}

type authorizerFunc func(context.Context, authz.Request) (authz.Decision, error)

func (f authorizerFunc) Authorize(ctx context.Context, r authz.Request) (authz.Decision, error) {
	return f(ctx, r)
}

// A replay body that fails mid-read is the route's invalid code.
func TestReplayReadError(t *testing.T) {
	h := New(stub.New(stub.Options{}), Local(tok, admin))
	hr := httptest.NewRequest(http.MethodPost, "/v1/replays", io.MultiReader(strings.NewReader(record(t, 0)+"\n"), errReader{}))
	hr.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hr)
	wantCode(t, w, limen.CodeInvalidReplay)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
