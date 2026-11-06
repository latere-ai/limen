// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/pkg/authkit/issuertest"
	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/authz/stub"

	"latere.ai/x/limen"
	"latere.ai/x/limen/authorizer"
	"latere.ai/x/limen/client"
)

// as is a client of the server at addr that sends token.
func as(t *testing.T, addr, token string) *client.Client {
	t.Helper()
	c, err := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func action(subject, id string) limen.Action {
	in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	return limen.Action{Org: "org_7", Subject: subject, ActionID: id, Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in}
}

// A hosted server takes each caller's subject from a token of the
// operator's issuer: an admin acts for every person, a person for
// themselves only, and a token for another audience is refused.
func TestHostedOwnerPolicy(t *testing.T) {
	iss := issuertest.New(t, issuertest.WithDefaultAudience("limen"))
	service := authz.Subject(iss.URL(), "svc_topos")
	person := authz.Subject(iss.URL(), "usr_42")
	addr, stop := startWith(t, map[string]string{
		"LIMEN_OIDC_ISSUERS":  iss.URL() + "/",
		"LIMEN_OIDC_AUDIENCE": "limen, api.example",
		"LIMEN_ADMINS":        service,
		"LIMEN_DATA_DIR":      t.TempDir(),
	})
	ctx := context.Background()

	harness := as(t, addr, iss.Mint(issuertest.Claims{Sub: "svc_topos"}))
	a := action(person, "ses_1/call_1")
	if _, err := harness.Predict(ctx, a); err != nil {
		t.Fatalf("the admin predicts for a person: %v", err)
	}
	if err := harness.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "topos/confirm"}); err != nil {
		t.Fatalf("the admin records: %v", err)
	}

	own := as(t, addr, iss.Mint(issuertest.Claims{Sub: "usr_42"}))
	if page, err := own.Cases(ctx, limen.CaseQuery{Org: "org_7", Subject: person}); err != nil || len(page.Entries) != 1 {
		t.Errorf("a person reads their own cases: %+v, %v", page, err)
	}
	other := as(t, addr, iss.Mint(issuertest.Claims{Sub: "usr_99"}))
	if _, err := other.Cases(ctx, limen.CaseQuery{Org: "org_7", Subject: person}); limen.CodeOf(err) != limen.CodeForbidden {
		t.Errorf("another person reads the cases: %v", err)
	}
	elsewhere := as(t, addr, iss.Mint(issuertest.Claims{Sub: "svc_topos", Aud: issuertest.StringList{"arca"}}))
	if _, err := elsewhere.Predict(ctx, action(person, "ses_1/call_2")); limen.CodeOf(err) != limen.CodeUnauthenticated {
		t.Errorf("a token for another audience: %v", err)
	}
	if _, err := as(t, addr, "not-a-token").Predict(ctx, action(person, "ses_1/call_3")); limen.CodeOf(err) != limen.CodeUnauthenticated {
		t.Errorf("a bearer that is no token: %v", err)
	}
	if code, log := stop(); code != exitOK || !strings.Contains(log, "with the owner policy and 1 admin") {
		t.Errorf("stop: exit %d: %s", code, log)
	}
}

// With an authorization endpoint, limend asks it each question in its
// vocabulary, with the bearer from the token file.
func TestHostedAuthorizer(t *testing.T) {
	iss := issuertest.New(t, issuertest.WithDefaultAudience("limen"))
	az := stub.New(t, stub.WithVocabulary(authorizer.Vocabulary()), stub.WithAllow())
	az.Allow(stub.Rule{Subject: authz.Subject(iss.URL(), "svc_topos"), Action: authorizer.ActionCasePredict})
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(az.Token()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, stop := startWith(t, map[string]string{
		"LIMEN_OIDC_ISSUERS":          iss.URL(),
		"LIMEN_OIDC_AUDIENCE":         "limen",
		"LIMEN_AUTHORIZER_URL":        az.URL(),
		"LIMEN_AUTHORIZER_TOKEN_FILE": tokenFile,
		"LIMEN_DATA_DIR":              t.TempDir(),
	})
	ctx := context.Background()
	harness := as(t, addr, iss.Mint(issuertest.Claims{Sub: "svc_topos"}))
	a := action(authz.Subject(iss.URL(), "usr_42"), "ses_1/call_1")
	if _, err := harness.Predict(ctx, a); err != nil {
		t.Fatalf("an allowed prediction: %v", err)
	}
	if err := harness.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "topos/confirm"}); limen.CodeOf(err) != limen.CodeForbidden {
		t.Errorf("a decision the endpoint does not allow: %v", err)
	}
	if reqs := az.Requests(); len(reqs) < 2 || reqs[0].Action != authorizer.ActionCasePredict {
		t.Errorf("the endpoint heard %+v", reqs)
	}
	if code, log := stop(); code != exitOK || !strings.Contains(log, "with the authorization endpoint") {
		t.Errorf("stop: exit %d: %s", code, log)
	}
}

func TestAccessConfiguration(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	const iss = "https://issuer.example"
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LIMEN_TOKEN": "x", "LIMEN_OIDC_ISSUERS": iss}, "not both"},
		{map[string]string{"LIMEN_TOKEN": "x", "LIMEN_ADMINS": "a"}, "LIMEN_ADMINS applies only with LIMEN_OIDC_ISSUERS"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss}, "set LIMEN_OIDC_AUDIENCE"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss, "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_SUBJECT": "me"}, "LIMEN_SUBJECT applies only with LIMEN_TOKEN"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss, "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_AUTHORIZER_URL": "http://az", "LIMEN_ADMINS": "a"}, "LIMEN_ADMINS applies only without"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss, "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_AUTHORIZER_URL": "http://az"}, "set LIMEN_AUTHORIZER_TOKEN"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss, "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_AUTHORIZER_URL": "http://az", "LIMEN_AUTHORIZER_TOKEN": "t", "LIMEN_AUTHORIZER_TOKEN_FILE": empty}, "not both"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss, "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_AUTHORIZER_URL": "http://az", "LIMEN_AUTHORIZER_TOKEN_FILE": empty}, "is empty"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss, "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_AUTHORIZER_URL": "http://az", "LIMEN_AUTHORIZER_TOKEN_FILE": empty + "-missing"}, "cannot read LIMEN_AUTHORIZER_TOKEN_FILE"},
		{map[string]string{"LIMEN_OIDC_ISSUERS": iss, "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_AUTHORIZER_URL": "://bad", "LIMEN_AUTHORIZER_TOKEN": "t"}, "LIMEN_AUTHORIZER_URL"},
	}
	for _, c := range cases {
		_, err := readAccess(env(c.env), http.DefaultClient)
		if err == nil || !isConfig(err) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.env, err, c.want)
		}
	}
	if isConfig(os.ErrNotExist) {
		t.Error("a plain error is not a configuration error")
	}
	if got := list(" a, ,b ,"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("list: %q", got)
	}
}

// An issuer whose key set cannot be read stops the start.
func TestHostedIssuerUnreachable(t *testing.T) {
	restore(t)
	var out, errOut syncBuffer
	code := run(nil, &out, &errOut, env(map[string]string{
		"LIMEN_OIDC_ISSUERS": "http://127.0.0.1:1", "LIMEN_OIDC_AUDIENCE": "limen", "LIMEN_DATA_DIR": t.TempDir(), "LIMEN_ADDR": "127.0.0.1:0",
	}))
	if code != exitFail || !strings.Contains(errOut.String(), "cannot read the key sets") {
		t.Errorf("exit %d: %s", code, errOut.String())
	}
}
