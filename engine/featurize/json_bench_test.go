// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"

	"latere.ai/x/limen"
)

// jbShellAction is a shell tool call whose command is about n bytes long, in
// the form agents send it: an object with a command string.
func jbShellAction(n int) limen.Action {
	const line = "go test -run TestEngine -count=1 ./engine/... && git diff --stat origin/main -- engine/store\n"
	cmd := strings.Repeat(line, n/len(line)+1)[:n]
	input, err := json.Marshal(map[string]string{"command": cmd, "description": "Run the engine tests"})
	if err != nil {
		panic(err)
	}
	return limen.Action{
		Org: "org_8fd2c1", Subject: "https://issuer.example|usr_alice", ActionID: "act_00000001",
		Kind:   "tool",
		Name:   "bash",
		Effect: limen.EffectWrite,
		Input:  input,
	}
}

// Of runs for every action Limen predicts; for a shell tool call it first
// reads the command out of the action's input JSON.
func BenchmarkJSONShellInputDecode(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small", 300}, {"medium", 10 << 10}} {
		b.Run(size.name, func(b *testing.B) {
			a := jbShellAction(size.n)
			b.ReportAllocs()
			b.SetBytes(int64(len(a.Input)))
			for b.Loop() {
				f := Of(a)
				if len(f.A) != DimA {
					b.Fatal(len(f.A))
				}
			}
		})
	}
}

// command is the JSON step alone: the field lookup in a tool call's input.
func BenchmarkJSONCommandFieldDecode(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
		raw  func(string) jsontext.Value
	}{
		{"string", 300, func(s string) jsontext.Value { v, _ := json.Marshal(map[string]string{"command": s}); return v }},
		{"array", 300, func(s string) jsontext.Value {
			v, _ := json.Marshal(map[string][]string{"cmd": {"bash", "-lc", s}})
			return v
		}},
	} {
		b.Run(size.name, func(b *testing.B) {
			input := size.raw(jbShellAction(size.n).Name + strings.Repeat("ls -la /workspace && ", size.n/21))
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				if _, ok := command(input); !ok {
					b.Fatal("no command")
				}
			}
		})
	}
}
