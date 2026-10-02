// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"math"
	"testing"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/engine/store/storetest"
)

func TestMemory(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return store.NewMemory() })
}

// A case that cannot be encoded is refused, not stored half.
func TestMemoryRefusesUnencodable(t *testing.T) {
	m := store.NewMemory()
	c := storetest.Case("o", "s", "a", 0)
	c.Action.Attributes = map[string]any{"x": math.NaN()}
	if err := m.Put(context.Background(), c); err == nil {
		t.Fatal("stored a case that cannot be encoded")
	}
	if _, ok, _ := m.Case(context.Background(), c.Action.Key); ok {
		t.Error("an unencodable case was stored")
	}
	if err := m.Close(); err != nil {
		t.Error(err)
	}
}

// Limen shows the case as the contract does: no input, no action on the
// decision, and copies of the answers.
func TestCaseLimen(t *testing.T) {
	c := storetest.Case("o", "s", "a", 0)
	c.Action.Input = []byte(`{"command":"ls"}`)
	act := c.Action
	c.Decision.Action = &act
	lc := c.Limen()
	if lc.Action.Input != nil || lc.Decision.Action != nil {
		t.Errorf("Limen kept the input or the decision's action: %+v", lc)
	}
	lc.Answers[0].Approve = false
	lc.Prediction.Approve = 0.9
	lc.Decision.Verdict = limen.Block
	if !c.Answers[0].Approve || c.Prediction.Approve != 0.5 || c.Decision.Verdict != limen.Ask {
		t.Error("a write to the contract's case reached the store's case")
	}
	empty := store.Case{}.Limen()
	if empty.Prediction != nil || empty.Decision != nil {
		t.Errorf("an empty case shows %+v", empty)
	}
}
