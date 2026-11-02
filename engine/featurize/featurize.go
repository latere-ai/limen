// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package featurize turns an action into the two feature vectors Limen's
// kernel compares: A, what the action is, and C, where and how it runs.
// This is features/1 of specs/004-engine.md, and its tables are the
// contract: the groups, their widths and offsets, and their encodings.
//
// Nothing in an action is an error for the featurizer. An unknown kind or
// tool, an empty name, malformed input, or a missing target, environment,
// or actor has a defined encoding, so a decision point never sees a
// prediction fail because of what it described.
//
// Strings are hashed with FNV-1a 32 bit over their lowercase UTF-8 bytes,
// into the group's number of buckets. Every group is scaled so that two
// actions that differ only in that group are at squared distance at most
// 2, the distance of two distinct one-hot vectors, so that no group
// dominates the kernel by its width.
//
// The input of a shell action is read by a static extractor ([ReadShell])
// and dropped; only the vectors are kept.
//
// Where the spec leaves a choice open, this package takes the reading that
// finds more danger or keeps the distance bound, and says so:
//
//   - The program-class group is multi-hot, and two multi-hot vectors can
//     be further apart than 2. It is scaled to unit length, so it meets the
//     bound as the name tokens do.
//   - The attribute group can hold negative entries. It is scaled to length
//     at most 1/sqrt(2), the radius at which two such vectors are at
//     squared distance at most 2. The signal group, nonnegative, is scaled
//     to length at most 1.
//   - An empty kind or target kind is absent, encoded as zero, like a
//     missing actor or environment. A target sensitivity outside [0, 1] is
//     clamped, and NaN is absent.
//   - Kind "tool" is compared case-insensitively, as the tool name is.
//   - The shell extractor also splits on command substitution and a
//     background &, reads the script of sh -c (and bash, zsh, dash, ksh), of
//     eval and of find -exec, treats doas, exec, nice, stdbuf, timeout and
//     xargs as wrappers beside the spec's five, skips git's global options
//     before its subcommand, counts git push with -d, a +ref force push, and
//     git branch -d -f as destructive, extends /dev/sd to the other block
//     device names, adds terraform apply -destroy and podman beside docker,
//     and counts /lib32, /lib64, /libx32, /private/etc and /private/var as
//     outside the workspace.
//   - An attribute number that is NaN or infinite adds nothing.
//
// [Read] reads the scope of an action for consent (spec 011): the shell
// table of features/2's scope part over each segment, the class of the
// destinations of a git push, the outside-workspace rule, the publishing
// commands, and the effect and reversible of any other action; the holds
// of the environment bound that reading, and the claims of the decision
// point are merged with it. It also gives the families of the action and
// its label of labels/2. Where the spec leaves a choice open, it takes the
// reading that finds more danger: a destructive pattern of features/1 is
// irreversible in any row, a segment of class network with no row of its
// own (http) reaches the remote and maybe the network, a git push with
// --all, --mirror or --branches is primary, and an interpreter's -c or an
// eval has the reach of its script, or of a segment no table names at the
// depth bound. Remote no makes network, organization and public no,
// because each of them reaches another system. [Migrated] reads the scope
// of a case from before labels/2 from its stored vectors and label, with
// unknown in place of a middle value the input would decide.
package featurize

import (
	"cmp"
	"encoding/json"
	"hash/fnv"
	"math"
	"slices"
	"sort"
	"strings"

	"latere.ai/x/limen"
)

// Version names this featurizer; a model keeps the version it was fitted
// with and never mixes two.
const Version = "features/1"

// The widths of the two blocks.
const (
	DimA = 56
	DimC = 56
)

// The offsets and widths of the groups of A.
const (
	aEffect  = 0  // 4: none, read, write, external
	aIrrev   = 4  // 1
	aKind    = 5  // 8
	aTokens  = 13 // 32
	aClasses = 45 // 8
	aActor   = 53 // 3: agent, service, person

	kindBuckets  = 8
	tokenBuckets = 32
)

// The offsets and widths of the groups of C.
const (
	cSensitivity = 0  // 1
	cTargetKind  = 1  // 8
	cEnv         = 9  // 4: host, sandbox, service, other
	cCredential  = 13 // 1
	cDestructive = 14 // 1
	cOutside     = 15 // 1
	cAttributes  = 16 // 16
	cSignals     = 32 // 24: 8 buckets of verdict level, verdict present, score

	attrBuckets   = 16
	signalBuckets = 8
)

// Features is an action's two blocks: A what the action is, C where and
// how it runs.
type Features struct{ A, C []float64 }

// Of featurizes one action. It never fails: every input, including an
// empty action, an unknown kind or tool, malformed input JSON, a missing
// target or environment, has a defined encoding.
func Of(a limen.Action) Features {
	A := make([]float64, DimA)
	C := make([]float64, DimC)

	var sh Shell
	if isShell(a) {
		if cmd, ok := command(a.Input); ok {
			sh = ReadShell(cmd)
		}
	}

	// A: effect, one-hot.
	if i := slices.Index([]limen.Effect{limen.EffectNone, limen.EffectRead, limen.EffectWrite, limen.EffectExternal}, a.Effect); i >= 0 {
		A[aEffect+i] = 1
	}
	// A: irreversibility in [0, 1].
	A[aIrrev] = 0.5
	if a.Reversible != nil {
		A[aIrrev] = 0
		if !*a.Reversible {
			A[aIrrev] = 1
		}
	}
	// A: kind, one-hot of its hash.
	if a.Kind != "" {
		A[aKind+bucket(a.Kind, kindBuckets)] = 1
	}
	// A: name tokens and the programs of a shell action, unit length.
	toks := tokens(a.Name)
	for _, p := range sh.Programs {
		toks = append(toks, tokens(p)...)
	}
	for _, t := range toks {
		A[aTokens+bucket(t, tokenBuckets)]++
	}
	unit(A[aTokens : aTokens+tokenBuckets])
	// A: program classes, multi-hot scaled to unit length.
	for _, c := range sh.Classes {
		A[aClasses+slices.Index(classNames[:], c)] = 1
	}
	unit(A[aClasses : aClasses+numClasses])
	// A: actor kind, one-hot.
	if a.Actor != nil {
		if i := slices.Index([]string{"agent", "service", "person"}, strings.ToLower(a.Actor.Kind)); i >= 0 {
			A[aActor+i] = 1
		}
	}

	// C: target sensitivity and kind.
	C[cSensitivity] = 0.5
	if a.Target != nil {
		if s := a.Target.Sensitivity; s != nil && !math.IsNaN(*s) {
			C[cSensitivity] = math.Min(1, math.Max(0, *s))
		}
		if a.Target.Kind != "" {
			C[cTargetKind+bucket(a.Target.Kind, kindBuckets)] = 1
		}
	}
	// C: environment kind, one-hot, and whether it holds a credential.
	C[cCredential] = 0.5
	if e := a.Environment; e != nil {
		switch k := strings.ToLower(e.Kind); k {
		case "host":
			C[cEnv] = 1
		case "sandbox":
			C[cEnv+1] = 1
		case "service":
			C[cEnv+2] = 1
		case "":
		default:
			C[cEnv+3] = 1
		}
		C[cCredential] = 0
		if e.Credentialed {
			C[cCredential] = 1
		}
	}
	// C: what the shell extractor found.
	if sh.Destructive {
		C[cDestructive] = 1
	}
	if sh.OutsideWorkspace {
		C[cOutside] = 1
	}
	// C: attributes and signals.
	attributes(C[cAttributes:cAttributes+attrBuckets], a.Attributes)
	signals(C[cSignals:cSignals+3*signalBuckets], a.Signals)
	return Features{A: A, C: C}
}

// LabelsVersion names the vocabulary of the labels of new cases, labels/2
// ([Read]): the label of [Label] and a phrase of the scope. A case keeps
// the version its label was written with (spec 009), and a case of
// [Labels1] keeps its label (spec 011).
const LabelsVersion = "labels/2"

// Labels1 names the vocabulary of [Label], the labels of cases from
// before labels/2.
const Labels1 = "labels/1"

// maxLabelName bounds the part of a label that is the action's own name.
const maxLabelName = 64

// Label names an action for the person it runs for, in labels/1, in words
// a person can read, from the action's kind and name and, for a shell
// action, the segments the shell reader's tables name (spec 009): "bash: git push,
// go test", "bash: other command", "web_fetch", "egress https". It never
// holds text of the input, so keeping it keeps the input unkept. The kind
// and the name are the decision point's own fields, already kept with the
// action.
func Label(a limen.Action) string {
	name := cut(strings.ToLower(strings.TrimSpace(a.Name)), maxLabelName)
	if isShell(a) {
		var ls []string
		if cmd, ok := command(a.Input); ok {
			ls = ReadShell(cmd).Labels
		}
		switch {
		case len(ls) == 0:
			return name + ": other command"
		case len(ls) > 2:
			return name + ": " + strings.Join(ls[:2], ", ") + ", …"
		}
		return name + ": " + strings.Join(ls, ", ")
	}
	kind := cut(strings.ToLower(strings.TrimSpace(a.Kind)), maxLabelName)
	switch {
	case kind == "tool" && name != "":
		return name
	case name == "":
		return cmp.Or(kind, "action")
	}
	return kind + " " + name
}

// cut shortens s to at most n runes.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// shellNames are the tool names whose input is a shell command.
var shellNames = set("bash", "shell", "sh", "exec_command", "run_shell_command", "local_shell")

func isShell(a limen.Action) bool {
	return strings.EqualFold(a.Kind, "tool") && shellNames[strings.ToLower(a.Name)]
}

// command reads a shell action's command from its input: the field
// command, or else cmd, each a string or an array of strings joined by
// spaces. Anything else is no command.
func command(input json.RawMessage) (string, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return "", false
	}
	for _, key := range []string{"command", "cmd"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s, true
		}
		var parts []string
		if json.Unmarshal(raw, &parts) == nil {
			return strings.Join(parts, " "), true
		}
	}
	return "", false
}

// bucket hashes s with FNV-1a 32 bit over its lowercase UTF-8 bytes.
func bucket(s string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(s)))
	return int(h.Sum32() % uint32(n))
}

// tokens are the maximal runs of [a-z0-9] of length two or more in s,
// lowercased.
func tokens(s string) []string {
	var out []string
	start := -1
	s = strings.ToLower(s)
	for i := 0; i <= len(s); i++ {
		alnum := i < len(s) && (s[i] >= 'a' && s[i] <= 'z' || s[i] >= '0' && s[i] <= '9')
		switch {
		case alnum && start < 0:
			start = i
		case !alnum && start >= 0:
			if i-start >= 2 {
				out = append(out, s[start:i])
			}
			start = -1
		}
	}
	return out
}

// unit scales v to unit length; a zero vector stays zero.
func unit(v []float64) { capNorm(v, 1) }

// capNorm scales v down to length r when it is longer.
func capNorm(v []float64, r float64) {
	var n float64
	for _, x := range v {
		n += x * x
	}
	n = math.Sqrt(n)
	if n > r {
		for i := range v {
			v[i] *= r / n
		}
	}
}

// attributes encodes an action's attributes: per key, bucket hash(key): a
// boolean adds 1 or -1, a number x adds x/(1+|x|), and a string adds 1 to
// bucket hash(key=value) instead. Each bucket is clipped to [-1, 1] and the
// group to length 1/sqrt(2). Keys are read in sorted order so the sums are
// bitwise the same on every run.
func attributes(v []float64, attrs map[string]any) {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch x := attrs[k].(type) {
		case bool:
			v[bucket(k, attrBuckets)] += map[bool]float64{true: 1, false: -1}[x]
		case string:
			v[bucket(k+"="+x, attrBuckets)]++
		default:
			if f, ok := number(x); ok {
				v[bucket(k, attrBuckets)] += f / (1 + math.Abs(f))
			}
		}
	}
	for i := range v {
		v[i] = math.Max(-1, math.Min(1, v[i]))
	}
	capNorm(v, 1/math.Sqrt2)
}

// number reads a finite number of any Go numeric type or a json.Number.
func number(x any) (float64, bool) {
	var f float64
	switch n := x.(type) {
	case int:
		f = float64(n)
	case int8:
		f = float64(n)
	case int16:
		f = float64(n)
	case int32:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint:
		f = float64(n)
	case uint8:
		f = float64(n)
	case uint16:
		f = float64(n)
	case uint32:
		f = float64(n)
	case uint64:
		f = float64(n)
	case float32:
		f = float64(n)
	case float64:
		f = n
	case json.Number:
		var err error
		if f, err = n.Float64(); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

// levels place a signal's verdict on [0, 1].
var levels = map[limen.Verdict]float64{limen.Allow: 0, limen.Flag: 1.0 / 3, limen.Ask: 2.0 / 3, limen.Block: 1}

// signals encodes the signals in eight buckets by hash(source), three
// slots each: the verdict's level, 1 when a verdict is present, and the
// score; within a bucket each slot keeps its maximum. The group is scaled
// to length at most 1.
func signals(v []float64, sigs []limen.Signal) {
	for _, s := range sigs {
		b := 3 * bucket(s.Source, signalBuckets)
		if lv, ok := levels[s.Verdict]; ok {
			v[b] = math.Max(v[b], lv)
			v[b+1] = 1
		}
		if s.Score != nil && !math.IsNaN(*s.Score) {
			v[b+2] = math.Max(v[b+2], math.Min(1, math.Max(0, *s.Score)))
		}
	}
	capNorm(v, 1)
}
