// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package redact renders addresses and transport errors for a log line or
// a printed message without the parts that can hold a secret.
package redact

import (
	"errors"
	"fmt"
	"net/url"
)

// URL renders raw as scheme://host/path: the user info, the query and the
// fragment are dropped, because a key or a password can ride in any of
// them. A value that is not a URL renders as "(not a URL)".
func URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(not a URL)"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// Error renders err for a log line. A transport error names the URL it
// asked, so that URL is redacted.
func Error(err error) string {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Sprintf("%s %q: %v", ue.Op, URL(ue.URL), ue.Err)
	}
	return err.Error()
}
