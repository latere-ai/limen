// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/conformance"
	"latere.ai/x/limen/stub"
)

// The suite passes against the stub, steered and not.
func TestRunAgainstTheStub(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(*testing.T) limen.Service { return stub.New(stub.Options{}) },
		Steered: func(_ *testing.T, predict func(limen.Action) (float64, float64)) limen.Service {
			return stub.New(stub.Options{Predict: predict})
		},
		Clocked: func(_ *testing.T, now func() time.Time) limen.Service { return stub.New(stub.Options{Clock: now}) },
	})
}

// Without Steered, the cases that need a chosen prediction skip and the
// rest still run.
func TestRunUnsteered(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(*testing.T) limen.Service { return stub.New(stub.Options{}) },
	})
}
