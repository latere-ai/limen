// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package api_test holds openapi.yaml to the Go contract. The document is
// written by hand, so drift between it and the types is the failure to
// guard against: a field one side names and the other does not, a
// required field the other side may omit, an error sentence or status that
// differs from the code table, or a route asking an action the
// authorizer vocabulary does not have.
package api_test

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"latere.ai/x/limen"
	"latere.ai/x/limen/authorizer"
)

type doc = map[string]any

func load(t *testing.T) doc {
	t.Helper()
	raw, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := yaml.Unmarshal(raw, &d); err != nil {
		t.Fatalf("openapi.yaml: %v", err)
	}
	return d
}

func at(t *testing.T, d any, path ...string) doc {
	t.Helper()
	cur := d
	for _, p := range path {
		m, ok := cur.(doc)
		if !ok {
			t.Fatalf("%s: not a mapping", strings.Join(path, "."))
		}
		cur = m[p]
	}
	m, ok := cur.(doc)
	if !ok {
		t.Fatalf("%s: missing", strings.Join(path, "."))
	}
	return m
}

func strs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

// jsonFields returns a struct's JSON names and, for each, whether the tag
// omits it when empty or zero.
func jsonFields(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for f := range typ.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out[name] = strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero")
	}
	return out
}

func TestSchemasMatchTypes(t *testing.T) {
	schemas := at(t, load(t), "components", "schemas")
	types := map[string]any{
		"Action": limen.Action{}, "Target": limen.Target{}, "Machine": limen.Machine{},
		"Signal": limen.Signal{}, "Assessment": limen.Assessment{}, "Thresholds": limen.Thresholds{},
		"Audit": limen.Audit{}, "Answer": limen.Answer{}, "Record": limen.Record{},
		"Profile": limen.Profile{}, "Costs": limen.Costs{}, "Certify": limen.Certify{},
		"Reliability": limen.Reliability{}, "Estimate": limen.Estimate{}, "Certificate": limen.Certificate{},
		"ReplayRecord": limen.ReplayRecord{}, "ReplayReport": limen.ReplayReport{},
		"RecordPage": limen.Page{},
	}
	for name, v := range types {
		s := at(t, schemas, name)
		props := at(t, s, "properties")
		fields := jsonFields(reflect.TypeOf(v))
		var inDoc, inGo []string
		for p := range props {
			inDoc = append(inDoc, p)
		}
		for f := range fields {
			inGo = append(inGo, f)
		}
		sort.Strings(inDoc)
		sort.Strings(inGo)
		if !slices.Equal(inDoc, inGo) {
			t.Errorf("%s: properties %v, Go fields %v", name, inDoc, inGo)
			continue
		}
		// A field the encoder always writes is required in the document, and
		// a field it may omit is not.
		required := strs(s["required"])
		for f, omit := range fields {
			if req := slices.Contains(required, f); req == omit {
				t.Errorf("%s.%s: required=%v but omitempty=%v", name, f, req, omit)
			}
		}
		if s["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties is not false", name)
		}
	}
}

func TestEnumsMatchConstants(t *testing.T) {
	schemas := at(t, load(t), "components", "schemas")
	check := func(what string, got []string, want ...string) {
		if !slices.Equal(got, want) {
			t.Errorf("%s: enum %v, want %v", what, got, want)
		}
	}
	check("Verdict", strs(at(t, schemas, "Verdict")["enum"]),
		string(limen.Allow), string(limen.Flag), string(limen.Ask), string(limen.Block))
	check("Action.effect", strs(at(t, schemas, "Action", "properties", "effect")["enum"]),
		string(limen.EffectNone), string(limen.EffectRead), string(limen.EffectWrite), string(limen.EffectExternal))
	check("Machine.kind", strs(at(t, schemas, "Machine", "properties", "kind")["enum"]),
		string(limen.MachineHost), string(limen.MachineSandbox))
	check("Signal.verdict", strs(at(t, schemas, "Signal", "properties", "verdict")["enum"]),
		string(limen.Allow), string(limen.Block))
	check("Audit.of", strs(at(t, schemas, "Audit", "properties", "of")["enum"]),
		string(limen.Allow), string(limen.Block))
	check("Answer.kind", strs(at(t, schemas, "Answer", "properties", "kind")["enum"]),
		string(limen.AnswerDecision), string(limen.AnswerContest))
}

// Every code has one response whose description is its status, its code,
// and its sentence, and no response names a code outside the table.
func TestResponsesMatchErrorTable(t *testing.T) {
	responses := at(t, load(t), "components", "responses")
	for _, c := range limen.Codes() {
		r, ok := responses[string(c)].(doc)
		if !ok {
			t.Errorf("%s has no response", c)
			continue
		}
		want := fmt.Sprintf("%d %s: %s", c.Status(), c, c.Message())
		if r["description"] != want {
			t.Errorf("%s:\n got %q\nwant %q", c, r["description"], want)
		}
	}
	if len(responses) != len(limen.Codes()) {
		t.Errorf("%d responses, %d codes", len(responses), len(limen.Codes()))
	}
}

// Every operation asks one action of the vocabulary, every action is asked
// by some operation, and every error response sits under its code's status.
func TestOperations(t *testing.T) {
	paths := at(t, load(t), "paths")
	vocab := authorizer.Vocabulary()
	used := map[string]bool{}
	ids := map[string]bool{}
	for path, item := range paths {
		for method, op := range item.(doc) {
			o := op.(doc)
			where := strings.ToUpper(method) + " " + path
			if !strings.HasPrefix(path, "/v1/") {
				t.Errorf("%s: not under /v1/", where)
			}
			id := fmt.Sprint(o["operationId"])
			if ids[id] {
				t.Errorf("%s: operationId %s used twice", where, id)
			}
			ids[id] = true
			action := fmt.Sprint(o["x-limen-action"])
			if !vocab.Known(action) {
				t.Errorf("%s: x-limen-action %q is not in the vocabulary", where, action)
			}
			used[action] = true
			for status, r := range o["responses"].(doc) {
				ref, _ := r.(doc)["$ref"].(string)
				if ref == "" {
					continue
				}
				code := limen.Code(strings.TrimPrefix(ref, "#/components/responses/"))
				if strconv.Itoa(code.Status()) != status {
					t.Errorf("%s: %s under %s, its status is %d", where, code, status, code.Status())
				}
			}
		}
	}
	for _, a := range vocab.Actions {
		if !used[a.Name] {
			t.Errorf("action %s is asked by no route", a.Name)
		}
	}
}
