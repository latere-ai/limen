// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json/v2"
	"testing"

	"latere.ai/x/limen"
)

// A digest is kept with the ledger, so the bytes it is taken over are fixed:
// map keys in order, nil slices as null, and <, > and & escaped.
func TestDigestBytes(t *testing.T) {
	a := limen.Action{Org: "o", Subject: "s", ActionID: "a", Kind: "tool", Name: "bash<&>",
		Effect: limen.EffectWrite, Attributes: map[string]any{"b": 1, "a": "x>y"}}
	b, err := json.Marshal(a, digestJSON)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"org":"o","subject":"s","action_id":"a","kind":"tool","name":"bash\u003c\u0026\u003e","effect":"write","attributes":{"a":"x\u003ey","b":1}}`
	if string(b) != want {
		t.Errorf("digest bytes\n got %s\nwant %s", b, want)
	}
	a.Attributes = nil
	a.Signals = nil
	if b, _ = json.Marshal(a, digestJSON); string(b) != `{"org":"o","subject":"s","action_id":"a","kind":"tool","name":"bash\u003c\u0026\u003e","effect":"write"}` {
		t.Errorf("digest bytes without attributes: %s", b)
	}
}
