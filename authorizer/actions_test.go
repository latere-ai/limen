// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package authorizer

import (
	"testing"

	"latere.ai/x/pkg/authz"
)

func TestVocabulary(t *testing.T) {
	v := Vocabulary()
	if v.Core != Core {
		t.Errorf("core %q, want %q", v.Core, Core)
	}
	if len(v.Actions) != len(table) {
		t.Fatalf("%d actions, want %d", len(v.Actions), len(table))
	}
	for _, a := range table {
		if !v.Known(a.Name) {
			t.Errorf("%s is not known", a.Name)
		}
		if k, ok := v.Kind(a.Name); !ok || k != a.Kind {
			t.Errorf("%s acts on %q, want %q", a.Name, k, a.Kind)
		}
	}
	for _, k := range v.Kinds() {
		if v.Label(k) == k {
			t.Errorf("kind %s has no label", k)
		}
	}
	if v.Known("case.delete") {
		t.Error("an action outside the table is known")
	}
}

// A malformed table, here one action named twice, is refused when the
// vocabulary is built, so it can never reach an endpoint.
func TestMalformedTablePanics(t *testing.T) {
	dup := []authz.Action{{Name: ActionProfileRead, Kind: KindProfile}, {Name: ActionProfileRead, Kind: KindProfile}}
	defer func() {
		if recover() == nil {
			t.Error("a duplicate action did not panic")
		}
	}()
	must(build(dup))
}
