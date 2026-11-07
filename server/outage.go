// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package server

import "latere.ai/x/limen"

// outage is an authorizer_unavailable error whose answer to the caller
// names no address, and whose cause is for the log only.
type outage struct {
	answer *limen.Error
	cause  string
}

func (o *outage) Error() string { return o.answer.Error() }

func (o *outage) Unwrap() error { return o.answer }
