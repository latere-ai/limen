// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestURLKeepsOnlySchemeHostAndPath(t *testing.T) {
	for raw, want := range map[string]string{
		"https://user:secret@authz.example/check?key=SECRET#frag": "https://authz.example/check",
		"http://authz.internal:8080":                              "http://authz.internal:8080",
		"not a url":                                               "(not a URL)",
		"://":                                                     "(not a URL)",
	} {
		if got := URL(raw); got != want {
			t.Errorf("URL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestErrorRedactsTheURLOfATransportError(t *testing.T) {
	err := fmt.Errorf("ask: %w", &url.Error{Op: "Post", URL: "http://user:secret@authz.internal/check?key=SECRET", Err: errors.New("connection refused")})
	got := Error(err)
	if !strings.Contains(got, `"http://authz.internal/check"`) || !strings.Contains(got, "connection refused") {
		t.Errorf("Error = %q", got)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "SECRET") {
		t.Errorf("Error holds a secret: %q", got)
	}
	if got := Error(errors.New("plain")); got != "plain" {
		t.Errorf("Error(plain) = %q", got)
	}
}
