// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"testing"

	"latere.ai/x/cribrum"
	"latere.ai/x/cribrum/conformance"
	"latere.ai/x/cribrum/stub"
)

// The suite passes against the stub, steered and not.
func TestRunAgainstTheStub(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(*testing.T) cribrum.Service { return stub.New(stub.Options{}) },
		Steered: func(_ *testing.T, predict func(cribrum.Action) (float64, float64)) cribrum.Service {
			return stub.New(stub.Options{Predict: predict})
		},
	})
}

// Without Steered, the cases that need a chosen prediction skip and the
// rest still run.
func TestRunUnsteered(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(*testing.T) cribrum.Service { return stub.New(stub.Options{}) },
	})
}
