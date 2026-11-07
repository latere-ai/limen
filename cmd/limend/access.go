// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"slices"
	"strings"

	"latere.ai/x/pkg/authkit/jwt"
	"latere.ai/x/pkg/authz"

	"latere.ai/x/limen/authorizer"
	"latere.ai/x/limen/internal/redact"
	"latere.ai/x/limen/server"
)

// access is who may call the server, read from the environment: one
// bearer for one person (LIMEN_TOKEN), or tokens from the operator's
// issuers (LIMEN_OIDC_ISSUERS), decided by an authorization endpoint
// (LIMEN_AUTHORIZER_URL) or by the shared owner policy with LIMEN_ADMINS
// as its admins.
type access struct {
	options server.Options
	// warm reads the issuers' key sets once, so a wrong issuer stops the
	// start instead of refusing every request; nil for one bearer.
	warm func(context.Context) error
	// describe says how callers are checked, for the start line.
	describe string
}

// errConfig is a configuration the operator must correct.
type errConfig struct{ msg string }

func (e *errConfig) Error() string { return e.msg }

func configErr(format string, a ...any) error { return &errConfig{fmt.Sprintf(format, a...)} }

// readAccess builds the access the environment configures. client reads
// the issuers' key sets and asks the authorization endpoint.
func readAccess(getenv func(string) string, client *http.Client) (access, error) {
	token := strings.TrimSpace(getenv("LIMEN_TOKEN"))
	issuers := list(getenv("LIMEN_OIDC_ISSUERS"))
	switch {
	case token != "" && len(issuers) > 0:
		return access{}, configErr("set LIMEN_TOKEN or LIMEN_OIDC_ISSUERS, not both. LIMEN_TOKEN is for one person, LIMEN_OIDC_ISSUERS for a hosted server.")
	case token != "":
		for _, k := range []string{"LIMEN_OIDC_AUDIENCE", "LIMEN_ADMINS", "LIMEN_AUTHORIZER_URL"} {
			if strings.TrimSpace(getenv(k)) != "" {
				return access{}, configErr("%s applies only with LIMEN_OIDC_ISSUERS. Remove it, or use LIMEN_OIDC_ISSUERS in place of LIMEN_TOKEN.", k)
			}
		}
		subject := cmp.Or(strings.TrimSpace(getenv("LIMEN_SUBJECT")), "local")
		return access{options: server.Local(token, subject), describe: "one bearer token, for " + subject}, nil
	case len(issuers) == 0:
		return access{}, configErr("set LIMEN_TOKEN to a secret, then start limend again. Decision points send this secret as their bearer token. For a hosted server, set LIMEN_OIDC_ISSUERS and LIMEN_OIDC_AUDIENCE.")
	}
	if strings.TrimSpace(getenv("LIMEN_SUBJECT")) != "" {
		return access{}, configErr("LIMEN_SUBJECT applies only with LIMEN_TOKEN. A hosted server takes each caller's subject from its token.")
	}
	audiences := list(getenv("LIMEN_OIDC_AUDIENCE"))
	if len(audiences) == 0 {
		return access{}, configErr("set LIMEN_OIDC_AUDIENCE to the audiences that a token for this server carries, for example limen.")
	}
	for i, iss := range issuers {
		issuers[i] = strings.TrimRight(iss, "/")
	}
	v := jwt.New(jwt.Config{
		Issuers:    issuers,
		Audiences:  audiences,
		HTTPClient: client,
		// The family's age bound: a token older than a day is refused
		// whatever its exp, and a token with no iat has no age to hold.
		MaxTokenAge:     jwt.DefaultMaxTokenAge,
		RequireIssuedAt: true,
	})
	a := access{
		options: server.Options{Authenticator: server.JWT(server.VerifierFunc(func(raw string) (string, string, error) {
			c, err := v.Validate(raw)
			if err != nil {
				return "", "", err
			}
			return strings.TrimRight(c.Iss, "/"), c.Sub, nil
		}))},
		warm: v.Warm,
	}
	admins := list(getenv("LIMEN_ADMINS"))
	url := strings.TrimSpace(getenv("LIMEN_AUTHORIZER_URL"))
	if url == "" {
		a.options.Authorizer = server.OwnerPolicy{Admins: admins}
		a.describe = fmt.Sprintf("tokens from %s, with the owner policy and %d %s", strings.Join(issuers, ", "), len(admins), plural(len(admins), "admin", "admins"))
		return a, nil
	}
	if len(admins) > 0 {
		return access{}, configErr("LIMEN_ADMINS applies only without LIMEN_AUTHORIZER_URL. The authorization endpoint decides who is an admin.")
	}
	if u, err := neturl.Parse(url); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return access{}, configErr("LIMEN_AUTHORIZER_URL is not an http or https URL.")
	}
	bearer, err := authorizerToken(getenv)
	if err != nil {
		return access{}, err
	}
	az, err := authz.NewClient(authz.Options{URL: url, Token: bearer, HTTP: client, Vocabulary: authorizer.Vocabulary()})
	if err != nil {
		return access{}, configErr("LIMEN_AUTHORIZER_URL: %v", err)
	}
	a.options.Authorizer = az
	a.describe = fmt.Sprintf("tokens from %s, with the authorization endpoint %s", strings.Join(issuers, ", "), redact.URL(url))
	return a, nil
}

// authorizerToken is the bearer for the authorization endpoint, from
// LIMEN_AUTHORIZER_TOKEN or the file LIMEN_AUTHORIZER_TOKEN_FILE names.
func authorizerToken(getenv func(string) string) (string, error) {
	token := strings.TrimSpace(getenv("LIMEN_AUTHORIZER_TOKEN"))
	path := strings.TrimSpace(getenv("LIMEN_AUTHORIZER_TOKEN_FILE"))
	switch {
	case token != "" && path != "":
		return "", configErr("set LIMEN_AUTHORIZER_TOKEN or LIMEN_AUTHORIZER_TOKEN_FILE, not both.")
	case token != "":
		return token, nil
	case path == "":
		return "", configErr("set LIMEN_AUTHORIZER_TOKEN or LIMEN_AUTHORIZER_TOKEN_FILE to the bearer of the authorization endpoint.")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", configErr("cannot read LIMEN_AUTHORIZER_TOKEN_FILE: %v", err)
	}
	if token = strings.TrimSpace(string(b)); token == "" {
		return "", configErr("the file in LIMEN_AUTHORIZER_TOKEN_FILE is empty.")
	}
	return token, nil
}

// list splits a comma-separated value and drops empty entries.
func list(v string) []string {
	var out []string
	for f := range strings.SplitSeq(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return slices.Clip(out)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// isConfig reports whether err is a configuration the operator must
// correct, which exits 2, rather than a failure, which exits 1.
func isConfig(err error) bool {
	_, ok := errors.AsType[*errConfig](err)
	return ok
}
