// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net/http"

	"latere.ai/x/pkg/authkit/jwt"
	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/bearer"
)

// Caller is who sent a request: the rendered subject, "<issuer>|<sub>",
// its two halves apart, and every verified claim of the credential,
// verbatim. Nothing here reads a claim for meaning; the authorizer does.
type Caller struct {
	Subject string
	Issuer  string
	Sub     string
	Claims  map[string]any
}

// Authenticator identifies the caller of a request, or fails.
type Authenticator interface {
	Authenticate(r *http.Request) (Caller, error)
}

// AuthenticatorFunc adapts a function to [Authenticator].
type AuthenticatorFunc func(r *http.Request) (Caller, error)

// Authenticate implements [Authenticator].
func (f AuthenticatorFunc) Authenticate(r *http.Request) (Caller, error) { return f(r) }

// The refusals an authenticator here gives; the server answers each with
// unauthenticated, the refusal as its detail.
var (
	ErrNoBearer   = errors.New("the request carries no bearer token")
	ErrWrongToken = errors.New("the bearer token is not this server's")
)

// StaticToken authenticates one bearer token as one subject, compared in
// constant time: the setup of a server one person runs on their own
// machine. subject is a rendered subject, or any stable name.
func StaticToken(token, subject string) Authenticator {
	iss, sub, _ := authz.SplitSubject(subject)
	return AuthenticatorFunc(func(r *http.Request) (Caller, error) {
		got, ok := bearer.FromRequest(r)
		if !ok {
			return Caller{}, ErrNoBearer
		}
		if token == "" || !bearer.Equal(got, token) {
			return Caller{}, ErrWrongToken
		}
		return Caller{Subject: subject, Issuer: iss, Sub: sub}, nil
	})
}

// TokenVerifier verifies a bearer token and names its issuer and subject.
// latere.ai/x/pkg/authkit/jwt's Validator is the one a server uses, adapted
// with [VerifierFunc]:
//
//	server.VerifierFunc(func(raw string) (string, string, error) {
//		c, err := v.Validate(raw)
//		if err != nil {
//			return "", "", err
//		}
//		return c.Iss, c.Sub, nil
//	})
//
// The adapter lives with the binary that configures the issuers and
// audiences, so a test may verify with a stand-in.
type TokenVerifier interface {
	Verify(raw string) (issuer, sub string, err error)
}

// VerifierFunc adapts a function to [TokenVerifier].
type VerifierFunc func(raw string) (issuer, sub string, err error)

// Verify implements [TokenVerifier].
func (f VerifierFunc) Verify(raw string) (string, string, error) { return f(raw) }

// JWT authenticates a bearer JWT with v: the caller's subject is the
// token's issuer and sub rendered by authz.Subject, and its claims are the
// payload of the token v verified, read verbatim by the shared verifier's
// decoder.
func JWT(v TokenVerifier) Authenticator {
	return AuthenticatorFunc(func(r *http.Request) (Caller, error) {
		raw, ok := bearer.FromRequest(r)
		if !ok {
			return Caller{}, ErrNoBearer
		}
		iss, sub, err := v.Verify(raw)
		if err != nil {
			return Caller{}, err
		}
		if sub == "" {
			return Caller{}, errors.New("the verified token names no subject")
		}
		var claims map[string]any
		if err := jwt.DecodePayload(raw, &claims); err != nil {
			return Caller{}, err
		}
		return Caller{Subject: authz.Subject(iss, sub), Issuer: iss, Sub: sub, Claims: claims}, nil
	})
}

// OwnerPolicy is the authorizer of a server with no endpoint configured,
// latere.ai/x/pkg/authz's shared owner policy read over Cribrum's resources:
// a caller acts on the cases, reports and data of its own subject, the
// subject a Case, Reliability or Subject resource names; the Admins act on
// everything, profiles and replays included, which no subject owns; the
// anonymous caller and the probe are refused.
type OwnerPolicy struct {
	Admins []string
}

// Authorize implements authz.Authorizer.
func (p OwnerPolicy) Authorize(_ context.Context, req authz.Request) (authz.Decision, error) {
	owner := req.Resource.String("subject")
	return authz.Policy{Admins: p.Admins}.Decide(req, authz.Object{Exists: owner != "", Owner: owner}), nil
}

var _ authz.Authorizer = OwnerPolicy{}
