// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"latere.ai/x/limen"
)

func ptr[T any](x T) *T { return &x }

func bash(cmd string) limen.Action {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return limen.Action{Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in}
}

func finite(t *testing.T, f Features) {
	t.Helper()
	if len(f.A) != DimA || len(f.C) != DimC {
		t.Fatalf("dimensions %d, %d", len(f.A), len(f.C))
	}
	for _, v := range append(slices.Clone(f.A), f.C...) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("non-finite feature in %v %v", f.A, f.C)
		}
	}
}

func sub2(a, b []float64) float64 {
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s
}

func TestGroupLayout(t *testing.T) {
	if aActor+3 != DimA || aClasses+numClasses != aActor || aTokens+tokenBuckets != aClasses || aKind+kindBuckets != aTokens {
		t.Error("the groups of A do not tile its 56 dimensions")
	}
	if cSignals+3*signalBuckets != DimC || cAttributes+attrBuckets != cSignals || cTargetKind+kindBuckets != cEnv {
		t.Error("the groups of C do not tile its 56 dimensions")
	}
	if Version != "features/1" {
		t.Error(Version)
	}
}

// The empty action has the absent encodings and nothing else.
func TestEmptyAction(t *testing.T) {
	f := Of(limen.Action{})
	finite(t, f)
	wantA := make([]float64, DimA)
	wantA[aIrrev] = 0.5
	wantC := make([]float64, DimC)
	wantC[cSensitivity], wantC[cCredential] = 0.5, 0.5
	if !slices.Equal(f.A, wantA) || !slices.Equal(f.C, wantC) {
		t.Errorf("empty action:\nA %v\nC %v", f.A, f.C)
	}
}

func TestDeterministic(t *testing.T) {
	a := bash("cd x && rm -rf ../y | tee /etc/z")
	a.Attributes = map[string]any{"a": 1, "b": true, "c": "x", "d": 2.5, "e": -3, "f": decimal("4"), "g": uint8(1)}
	a.Signals = []limen.Signal{{Source: "rules/1", Score: ptr(0.4)}, {Source: "classifier/x", Verdict: limen.Block}}
	first := Of(a)
	for range 50 {
		if g := Of(a); !slices.Equal(g.A, first.A) || !slices.Equal(g.C, first.C) {
			t.Fatal("two featurizations differ")
		}
	}
}

func TestEncodings(t *testing.T) {
	f := Of(limen.Action{
		Kind:        "Tool",
		Name:        "mcp__github__merge_pull_request",
		Effect:      limen.EffectExternal,
		Reversible:  ptr(false),
		Actor:       &limen.Actor{Kind: "Agent"},
		Target:      &limen.Target{Kind: "ref", Sensitivity: ptr(0.9)},
		Environment: &limen.Environment{Kind: "sandbox", Credentialed: true},
	})
	finite(t, f)
	if f.A[aEffect+3] != 1 || f.A[aIrrev] != 1 || f.A[aActor] != 1 || f.A[aKind+bucket("tool", kindBuckets)] != 1 {
		t.Errorf("A %v", f.A)
	}
	if f.C[cSensitivity] != 0.9 || f.C[cTargetKind+bucket("ref", kindBuckets)] != 1 || f.C[cEnv+1] != 1 || f.C[cCredential] != 1 {
		t.Errorf("C %v", f.C)
	}
	// The tokens mcp, github, merge, pull, request, scaled to unit length.
	var norm float64
	for _, v := range f.A[aTokens : aTokens+tokenBuckets] {
		norm += v * v
	}
	if math.Abs(norm-1) > 1e-12 {
		t.Errorf("token norm %v", norm)
	}
	for _, c := range []struct {
		r    *bool
		want float64
	}{{nil, 0.5}, {ptr(true), 0}, {ptr(false), 1}} {
		if got := Of(limen.Action{Reversible: c.r}).A[aIrrev]; got != c.want {
			t.Errorf("reversible %v: %v", c.r, got)
		}
	}
	envs := map[string]int{"host": 0, "sandbox": 1, "service": 2, "vm": 3}
	for k, i := range envs {
		f := Of(limen.Action{Environment: &limen.Environment{Kind: k}})
		if f.C[cEnv+i] != 1 || f.C[cCredential] != 0 {
			t.Errorf("environment %s: %v", k, f.C[cEnv:cEnv+5])
		}
	}
	if f := Of(limen.Action{Environment: &limen.Environment{}}); slices.ContainsFunc(f.C[cEnv:cEnv+4], func(v float64) bool { return v != 0 }) {
		t.Error("an empty environment kind is not absent")
	}
	for _, c := range []struct {
		s    *float64
		want float64
	}{{nil, 0.5}, {ptr(math.NaN()), 0.5}, {ptr(-1.0), 0}, {ptr(2.0), 1}, {ptr(0.3), 0.3}} {
		if got := Of(limen.Action{Target: &limen.Target{Sensitivity: c.s}}).C[cSensitivity]; got != c.want {
			t.Errorf("sensitivity %v: %v", c.s, got)
		}
	}
	if f := Of(limen.Action{Actor: &limen.Actor{Kind: "robot"}, Effect: "bogus"}); slices.ContainsFunc(f.A[aActor:], func(v float64) bool { return v != 0 }) || slices.ContainsFunc(f.A[aEffect:aEffect+4], func(v float64) bool { return v != 0 }) {
		t.Error("an unknown actor or effect is not absent")
	}
}

func TestShellGroups(t *testing.T) {
	f := Of(bash("git push --force && rm -rf ~/x"))
	if f.C[cDestructive] != 1 || f.C[cOutside] != 1 {
		t.Errorf("destructive %v outside %v", f.C[cDestructive], f.C[cOutside])
	}
	classes := f.A[aClasses : aClasses+numClasses]
	want := 1 / math.Sqrt2 // vcs_remote and delete, scaled to unit length
	if math.Abs(classes[clsVCSRemote]-want) > 1e-12 || math.Abs(classes[clsDelete]-want) > 1e-12 {
		t.Errorf("classes %v", classes)
	}
	// The programs join the tool name's tokens.
	plain := Of(limen.Action{Kind: "tool", Name: "bash"})
	if slices.Equal(plain.A[aTokens:aTokens+tokenBuckets], f.A[aTokens:aTokens+tokenBuckets]) {
		t.Error("the programs added no tokens")
	}
	// The command may be an array, or under cmd.
	for _, in := range []string{`{"command":["rm","-rf","x"]}`, `{"cmd":"rm -rf x"}`, `{"cmd":["rm","-rf","x"]}`} {
		a := limen.Action{Kind: "tool", Name: "exec_command", Input: jsontext.Value(in)}
		if Of(a).C[cDestructive] != 1 {
			t.Errorf("%s is not read", in)
		}
	}
	// A tool that is not a shell, a kind that is not a tool, malformed
	// input, and an unusable command have zero shell groups.
	for _, a := range []limen.Action{
		{Kind: "tool", Name: "write", Input: jsontext.Value(`{"command":"rm -rf x"}`)},
		{Kind: "egress", Name: "bash", Input: jsontext.Value(`{"command":"rm -rf x"}`)},
		{Kind: "tool", Name: "bash", Input: jsontext.Value(`{`)},
		{Kind: "tool", Name: "bash", Input: jsontext.Value(`{"command":42}`)},
		{Kind: "tool", Name: "bash", Input: jsontext.Value(`{"other":"rm -rf x"}`)},
	} {
		f := Of(a)
		if f.C[cDestructive] != 0 || slices.ContainsFunc(f.A[aClasses:aClasses+numClasses], func(v float64) bool { return v != 0 }) {
			t.Errorf("%s %s %s was read as a shell command", a.Kind, a.Name, a.Input)
		}
	}
}

func TestTokens(t *testing.T) {
	cases := map[string][]string{
		"mcp__github__merge_pull_request": {"mcp", "github", "merge", "pull", "request"},
		"Bash":                            {"bash"},
		"a b cd 9 x99":                    {"cd", "x99"},
		"":                                nil,
		"apt-get":                         {"apt", "get"},
	}
	for in, want := range cases {
		if got := tokens(in); !slices.Equal(got, want) {
			t.Errorf("tokens(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAttributes(t *testing.T) {
	v := make([]float64, attrBuckets)
	attributes(v, map[string]any{"protected": true})
	if math.Abs(v[bucket("protected", attrBuckets)]-1/math.Sqrt2) > 1e-12 {
		t.Errorf("a true boolean: %v", v)
	}
	v = make([]float64, attrBuckets)
	attributes(v, map[string]any{"protected": false})
	if math.Abs(v[bucket("protected", attrBuckets)]+1/math.Sqrt2) > 1e-12 {
		t.Errorf("a false boolean: %v", v)
	}
	v = make([]float64, attrBuckets)
	attributes(v, map[string]any{"amount": 1.0})
	if v[bucket("amount", attrBuckets)] != 0.5 {
		t.Errorf("a number: %v", v)
	}
	v = make([]float64, attrBuckets)
	attributes(v, map[string]any{"branch": "main"})
	if math.Abs(v[bucket("branch=main", attrBuckets)]-1/math.Sqrt2) > 1e-12 {
		t.Errorf("a string: %v", v)
	}
	// A bucket is clipped to [-1, 1] before the group is scaled: many large
	// numbers in one key's bucket stay at one.
	v = make([]float64, attrBuckets)
	attrs := map[string]any{}
	for i := range 200 {
		attrs["k"+strconv.Itoa(i)] = 1e9
	}
	attributes(v, attrs)
	for _, x := range v {
		if math.Abs(x) > 1/math.Sqrt2+1e-12 {
			t.Errorf("unclipped bucket %v", v)
		}
	}
	// NaN, infinity, and unsupported values add nothing.
	v = make([]float64, attrBuckets)
	attributes(v, map[string]any{"a": math.NaN(), "b": math.Inf(1), "c": []int{1}, "d": nil, "e": decimal("x")})
	if slices.ContainsFunc(v, func(x float64) bool { return x != 0 }) {
		t.Errorf("unusable values added %v", v)
	}
	for _, x := range []any{int(1), int8(1), int16(1), int32(1), int64(1), uint(1), uint8(1), uint16(1), uint32(1), uint64(1), float32(1), 1.0, decimal("1")} {
		if f, ok := number(x); !ok || f != 1 {
			t.Errorf("number(%T) = %v, %v", x, f, ok)
		}
	}
}

func TestSignals(t *testing.T) {
	v := make([]float64, 3*signalBuckets)
	signals(v, []limen.Signal{
		{Source: "classifier/x", Verdict: limen.Flag},
		{Source: "classifier/x", Verdict: limen.Ask, Score: ptr(0.2)},
		{Source: "classifier/x", Score: ptr(0.6)},
	})
	b := 3 * bucket("classifier/x", signalBuckets)
	got := v[b : b+3]
	// The maxima (2/3, 1, 0.6), scaled to unit length.
	n := math.Sqrt(4.0/9 + 1 + 0.36)
	want := []float64{(2.0 / 3) / n, 1 / n, 0.6 / n}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Errorf("signal slots %v, want %v", got, want)
		}
	}
	for verdict, level := range map[limen.Verdict]float64{limen.Allow: 0, limen.Flag: 1.0 / 3, limen.Ask: 2.0 / 3, limen.Block: 1} {
		v := make([]float64, 3*signalBuckets)
		signals(v, []limen.Signal{{Source: "s", Verdict: verdict}})
		b := 3 * bucket("s", signalBuckets)
		if n := math.Hypot(level, 1); math.Abs(v[b]-level/n) > 1e-12 || math.Abs(v[b+1]-1/n) > 1e-12 {
			t.Errorf("%s: %v", verdict, v[b:b+3])
		}
	}
	// An invalid verdict, a NaN score and a score outside [0, 1].
	v = make([]float64, 3*signalBuckets)
	signals(v, []limen.Signal{{Source: "s", Verdict: "deny", Score: ptr(math.NaN())}, {Source: "t", Score: ptr(7.0)}})
	if v[3*bucket("s", signalBuckets)+1] != 0 || v[3*bucket("t", signalBuckets)+2] != 1 {
		t.Errorf("signals %v", v)
	}
}

// randomAction draws an action whose every group varies.
func randomAction(r *rand.Rand) limen.Action {
	pick := func(xs ...string) string { return xs[r.IntN(len(xs))] }
	a := limen.Action{
		Kind:   pick("", "tool", "egress", "repo", "spend", strconv.Itoa(r.IntN(100))),
		Name:   pick("", "bash", "write", "mcp__github__merge_pull_request", "a b c dd ee ff gg hh ii jj kk", strconv.Itoa(r.IntN(1000))),
		Effect: limen.Effect(pick("", "none", "read", "write", "external", "bogus")),
	}
	if r.IntN(2) == 0 {
		a.Kind, a.Name = "tool", "bash"
		in, _ := json.Marshal(map[string]string{"command": pick("ls", "rm -rf ~/x", "git push -f && curl x && npm i && make && terraform destroy && git commit && gh x && rm y", "echo > /etc/x", "")})
		a.Input = in
	}
	if r.IntN(3) > 0 {
		a.Reversible = ptr(r.IntN(2) == 0)
	}
	if r.IntN(2) == 0 {
		a.Actor = &limen.Actor{Kind: pick("agent", "service", "person", "other")}
	}
	if r.IntN(2) == 0 {
		a.Target = &limen.Target{Kind: pick("", "ref", "path", "host"), Sensitivity: ptr(r.Float64()*3 - 1)}
	}
	if r.IntN(2) == 0 {
		a.Environment = &limen.Environment{Kind: pick("", "host", "sandbox", "service", "vm"), Credentialed: r.IntN(2) == 0}
	}
	if n := r.IntN(6); n > 0 {
		a.Attributes = map[string]any{}
		for i := range n * 10 {
			k := "k" + strconv.Itoa(i)
			switch r.IntN(3) {
			case 0:
				a.Attributes[k] = r.IntN(2) == 0
			case 1:
				a.Attributes[k] = (r.Float64() - 0.5) * 1e6
			default:
				a.Attributes[k] = pick("x", "y", "z")
			}
		}
	}
	for range r.IntN(12) {
		a.Signals = append(a.Signals, limen.Signal{
			Source:  "s" + strconv.Itoa(r.IntN(20)),
			Verdict: limen.Verdict(pick("", "allow", "flag", "ask", "block")),
			Score:   ptr(r.Float64()),
		})
	}
	return a
}

// Every group meets the scaling rule: over many actions, no two differ by
// more than squared distance 2 within one group.
func TestGroupDistanceBound(t *testing.T) {
	type group struct {
		name   string
		inA    bool
		lo, hi int
	}
	groups := []group{
		{"effect", true, aEffect, aIrrev}, {"irreversibility", true, aIrrev, aKind},
		{"kind", true, aKind, aTokens}, {"name tokens", true, aTokens, aClasses},
		{"program class", true, aClasses, aActor}, {"actor", true, aActor, DimA},
		{"sensitivity", false, cSensitivity, cTargetKind}, {"target kind", false, cTargetKind, cEnv},
		{"environment", false, cEnv, cCredential}, {"credentialed", false, cCredential, cDestructive},
		{"destructive", false, cDestructive, cOutside}, {"outside", false, cOutside, cAttributes},
		{"attributes", false, cAttributes, cSignals}, {"signals", false, cSignals, DimC},
	}
	r := rand.New(rand.NewPCG(1, 2))
	fs := make([]Features, 300)
	for i := range fs {
		fs[i] = Of(randomAction(r))
		finite(t, fs[i])
	}
	for _, g := range groups {
		var worst float64
		for i := range fs {
			for j := range i {
				a, b := fs[i].C, fs[j].C
				if g.inA {
					a, b = fs[i].A, fs[j].A
				}
				worst = math.Max(worst, sub2(a[g.lo:g.hi], b[g.lo:g.hi]))
			}
		}
		if worst > 2+1e-9 {
			t.Errorf("%s: squared distance %v exceeds 2", g.name, worst)
		}
	}
}

func FuzzOf(f *testing.F) {
	f.Add("tool", "bash", `{"command":"rm -rf ~/x && git push -f"}`, 0.5, 0.5, "k", 1.0, "s")
	f.Add("", "", "", math.NaN(), math.Inf(1), "", math.NaN(), "")
	f.Add("tool", "exec_command", `{"cmd":["sh","-c","eval $(x)"]}`, -3.0, 9.0, "x=y", -1e300, "classifier/a")
	f.Fuzz(func(t *testing.T, kind, name, input string, sens, score float64, key string, num float64, source string) {
		a := limen.Action{
			Kind: kind, Name: name, Input: jsontext.Value(input),
			Target:     &limen.Target{Kind: kind, Sensitivity: &sens},
			Attributes: map[string]any{key: num, key + "s": name, key + "b": sens > 0},
			Signals:    []limen.Signal{{Source: source, Verdict: limen.Verdict(name), Score: &score}},
		}
		finite(t, Of(a))
	})
}

func BenchmarkOf(b *testing.B) {
	a := bash("cd repo && git add -A && git commit -m wip && git push origin HEAD")
	a.Effect = limen.EffectExternal
	a.Target = &limen.Target{Kind: "ref", ID: "refs/heads/main", Sensitivity: ptr(0.9)}
	a.Environment = &limen.Environment{Kind: "sandbox", Credentialed: true}
	a.Actor = &limen.Actor{Kind: "agent"}
	a.Attributes = map[string]any{"protected": true, "files": 3}
	a.Signals = []limen.Signal{{Source: "rules/1", Score: ptr(0.4)}, {Source: "classifier/claude-auto-mode", Verdict: limen.Allow}}
	for b.Loop() {
		Of(a)
	}
}

// decimal is a number kept as text that converts with a Float64 method.
type decimal string

func (d decimal) Float64() (float64, error) { return strconv.ParseFloat(string(d), 64) }

// Tool arguments an agent wrote are read as they came: repeated names are
// taken, the last one winning, and invalid UTF-8 does not hide the command.
func TestCommandForeignInput(t *testing.T) {
	for in, want := range map[string]string{
		`{"command":"ls","command":"pwd"}`:          "pwd",
		"{\"command\":\"ls \xff\"}":                 "ls �",
		`{"cmd":["go","vet"],"cmd":["go","build"]}`: "go build",
	} {
		if got, ok := command(jsontext.Value(in)); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
}
