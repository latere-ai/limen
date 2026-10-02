// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package api_test holds openapi.yaml to the Go contract. The document is
// written by hand, so drift between it and the types is the failure to
// guard against: a field one side names and the other does not, a field
// one side always writes and the other calls optional, an enum that
// differs from the constants, an error sentence or status that differs
// from the code table, or a route asking an action, about a resource
// kind, that the authorizer vocabulary does not pair.
package api_test

import (
	"fmt"
	"maps"
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

// jsonFields returns a struct's JSON names and, for each, whether the
// encoder may omit it (omitempty or omitzero). An embedded struct with no
// tag is flattened into its parent, as encoding/json does.
func jsonFields(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for f := range typ.Fields() {
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			maps.Copy(out, jsonFields(f.Type))
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

// schemaTypes pairs each schema with the Go type it describes.
var schemaTypes = map[string]any{
	"Action": limen.Action{}, "Actor": limen.Actor{}, "Target": limen.Target{},
	"Environment": limen.Environment{}, "Signal": limen.Signal{},
	"Prediction": limen.Prediction{}, "Thresholds": limen.Thresholds{}, "Scope": limen.Scope{},
	"Decision": limen.Decision{}, "Answer": limen.Answer{}, "Case": limen.Case{},
	"RecordedAnswer": limen.RecordedAnswer{}, "Page": limen.Page{},
	"Profile": limen.Profile{}, "Costs": limen.Costs{}, "Certify": limen.Certify{},
	"Reliability": limen.Reliability{}, "Estimate": limen.Estimate{}, "Certificate": limen.Certificate{},
	"ReplayRecord": limen.ReplayRecord{}, "ReplayReport": limen.ReplayReport{},
}

func TestSchemasMatchTypes(t *testing.T) {
	schemas := at(t, load(t), "components", "schemas")
	for name, v := range schemaTypes {
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
				t.Errorf("%s.%s: required=%v but omittable=%v", name, f, req, omit)
			}
		}
	}
	// A request the server has no Go type for still names what it requires.
	if got := strs(at(t, schemas, "ForgetRequest")["required"]); !slices.Equal(got, []string{"org", "subject"}) {
		t.Errorf("ForgetRequest requires %v", got)
	}
}

// Receivers ignore fields they do not know, so no schema forbids them.
func TestNoClosedSchemas(t *testing.T) {
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case doc:
			if ap, ok := x["additionalProperties"]; ok && ap == false {
				t.Errorf("%s sets additionalProperties: false", path)
			}
			for k, c := range x {
				walk(path+"."+k, c)
			}
		case []any:
			for i, c := range x {
				walk(path+"["+strconv.Itoa(i)+"]", c)
			}
		}
	}
	walk("", load(t))
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
	check("Suggestion", strs(at(t, schemas, "Suggestion")["enum"]),
		string(limen.Allow), string(limen.Ask), string(limen.Block))
	check("Action.effect", strs(at(t, schemas, "Action", "properties", "effect")["enum"]),
		string(limen.EffectNone), string(limen.EffectRead), string(limen.EffectWrite), string(limen.EffectExternal))
	if ref := at(t, schemas, "Prediction", "properties", "verdict")["$ref"]; ref != "#/components/schemas/Suggestion" {
		t.Errorf("Prediction.verdict is %v, want the Suggestion schema", ref)
	}
}

// codesOf returns the codes a response stands for: those listed in
// x-limen-codes, or the code its component is named after.
func codesOf(name string, r doc) []limen.Code {
	if l := strs(r["x-limen-codes"]); len(l) > 0 {
		out := make([]limen.Code, len(l))
		for i, c := range l {
			out[i] = limen.Code(c)
		}
		return out
	}
	return []limen.Code{limen.Code(name)}
}

// Every code has one response whose description is its status, its code,
// and its sentence, and every other response names codes of one status.
func TestResponsesMatchErrorTable(t *testing.T) {
	responses := at(t, load(t), "components", "responses")
	known := map[limen.Code]bool{}
	for _, c := range limen.Codes() {
		known[c] = true
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
	for name, r := range responses {
		if known[limen.Code(name)] {
			continue
		}
		codes := codesOf(name, r.(doc))
		if len(codes) < 2 {
			t.Errorf("response %s is no code and lists %v", name, codes)
		}
		for _, c := range codes {
			if !known[c] {
				t.Errorf("response %s lists %q, which is not in the table", name, c)
			} else if c.Status() != codes[0].Status() {
				t.Errorf("response %s mixes statuses: %s is %d, %s is %d", name, c, c.Status(), codes[0], codes[0].Status())
			}
		}
	}
}

// Every operation asks one action of the vocabulary about the kind the
// vocabulary pairs it with, every action is asked by some operation, every
// error response sits under its codes' status, and every code is served by
// some route.
func TestOperations(t *testing.T) {
	d := load(t)
	paths := at(t, d, "paths")
	components := at(t, d, "components", "responses")
	vocab := authorizer.Vocabulary()
	used := map[string]bool{}
	served := map[limen.Code]bool{}
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
			kind, ok := vocab.Kind(action)
			if !ok {
				t.Errorf("%s: x-limen-action %q is not in the vocabulary", where, action)
			}
			used[action] = true
			res, _ := o["x-limen-resource"].(doc)
			if res == nil || res["kind"] != kind || len(strs(res["fields"])) == 0 || !slices.Contains(strs(res["fields"]), "org") {
				t.Errorf("%s: x-limen-resource %v, want kind %s with fields naming org", where, res, kind)
			}
			for status, r := range o["responses"].(doc) {
				rd := r.(doc)
				name := ""
				if ref, ok := rd["$ref"].(string); ok {
					name = strings.TrimPrefix(ref, "#/components/responses/")
					rd, ok = components[name].(doc)
					if !ok {
						t.Errorf("%s: %s refers to a missing response", where, ref)
						continue
					}
				} else if len(strs(rd["x-limen-codes"])) == 0 {
					if s, _ := strconv.Atoi(status); s >= 400 {
						t.Errorf("%s: error %s names no code", where, status)
					}
					continue
				}
				for _, c := range codesOf(name, rd) {
					served[c] = true
					if strconv.Itoa(c.Status()) != status {
						t.Errorf("%s: %s under %s, its status is %d", where, c, status, c.Status())
					}
				}
			}
		}
	}
	for _, a := range vocab.Actions {
		if !used[a.Name] {
			t.Errorf("action %s is asked by no route", a.Name)
		}
	}
	for _, c := range limen.Codes() {
		if !served[c] {
			t.Errorf("code %s is served by no route", c)
		}
	}
}
