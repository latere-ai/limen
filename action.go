// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json"
	"math"
	"strconv"
	"time"
)

// Effect is what a call can do to the world, as the harness classifies
// its tools: nothing, read, write inside the harness's boundary, or reach
// outside it.
type Effect string

const (
	EffectNone     Effect = "none"
	EffectRead     Effect = "read"
	EffectWrite    Effect = "write"
	EffectExternal Effect = "external"
)

// Valid reports whether e is one of the four effects.
func (e Effect) Valid() bool {
	switch e {
	case EffectNone, EffectRead, EffectWrite, EffectExternal:
		return true
	}
	return false
}

// MachineKind is where a call runs.
type MachineKind string

const (
	// MachineHost is the person's own machine.
	MachineHost MachineKind = "host"
	// MachineSandbox is an isolated environment whose files are disposable.
	MachineSandbox MachineKind = "sandbox"
)

// Action is one proposed call, described by what a harness can observe
// before it runs. Limen learns from these fields and nothing else: the
// person's tolerance is a function of them and of time.
type Action struct {
	// Subject is the person whose approvals Limen learns from and predicts,
	// rendered as the issuer and the token's sub joined by a pipe. It is
	// the person the agent acts for, not the agent.
	Subject string `json:"subject"`
	// Org is the organization whose cost profile applies.
	Org string `json:"org"`
	// Agent and Session identify who proposed the call, for the record and
	// for a cost profile scoped to one agent.
	Agent   string `json:"agent,omitempty"`
	Session string `json:"session,omitempty"`
	// CallID is the harness's own id for the call. Assessing the same
	// Subject and CallID again returns the first assessment unchanged, so
	// a caller cannot draw a second audit sample by asking twice.
	CallID string `json:"call_id"`
	// Tool is the tool's name as the harness knows it: "bash", "write",
	// "mcp__github__merge_pull_request".
	Tool   string `json:"tool"`
	Effect Effect `json:"effect"`
	// Input is the tool's input. Only deterministic feature extractors read
	// it, for example to recognize a recursive delete in a shell command;
	// no language model ever sees it, so text an agent writes cannot
	// instruct the gate.
	Input   json.RawMessage `json:"input,omitempty"`
	Target  *Target         `json:"target,omitempty"`
	Machine Machine         `json:"machine"`
	// Signals are other opinions about the call: a rule score, an LLM
	// judge's verdict. Limen treats them as evidence about the person's
	// answer and never as a verdict of their own.
	Signals []Signal `json:"signals,omitempty"`
	// Ceiling is the most permissive verdict the deterministic layers leave
	// open: [Allow] when they impose nothing, [Ask] for an action they
	// always confirm, [Block] for one they deny. Limen's verdict is never
	// more permissive, which is how a deterministic deny stays final.
	Ceiling Verdict `json:"ceiling"`
	// At is when the call was proposed. Tolerance drifts, so time is an
	// input like any other.
	At time.Time `json:"at"`
}

// Target is what the call acts on.
type Target struct {
	// Kind is the harness's word for the target: "path", "ref", "host",
	// "resource".
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Sensitivity runs from 0, a scratch directory, to 1, production. It is
	// optional; without it the target's kind and id are the only evidence.
	Sensitivity *float64 `json:"sensitivity,omitempty"`
}

// Machine is where the call runs.
type Machine struct {
	Kind MachineKind `json:"kind"`
	// Credentialed is true when the machine holds a credential that reaches
	// outside it, so a command run there can act beyond its boundary.
	Credentialed bool `json:"credentialed"`
}

// Signal is one other opinion about a call. Source names who gave it, in
// the form "<kind>/<name>": "rules/1", "judge/claude-auto-mode". A judge's
// error rate for a person is reported per Source ([Gate.Reliability]).
type Signal struct {
	Source string `json:"source"`
	// Verdict is set by a judge that returns allow or block.
	Verdict Verdict `json:"verdict,omitempty"`
	// Score is set by a source that returns a number in [0, 1], higher
	// meaning riskier.
	Score *float64 `json:"score,omitempty"`
}

// Validate reports every field of a that breaks the contract, as one
// [*Error] with [CodeInvalidAction], or nil.
func (a Action) Validate() error {
	var p problems
	if a.Subject == "" {
		p.add("subject", "is empty")
	}
	if a.Org == "" {
		p.add("org", "is empty")
	}
	if a.CallID == "" {
		p.add("call_id", "is empty")
	}
	if a.Tool == "" {
		p.add("tool", "is empty")
	}
	if !a.Effect.Valid() {
		p.add("effect", "is not one of none, read, write, external")
	}
	if len(a.Input) > 0 && !json.Valid(a.Input) {
		p.add("input", "is not valid JSON")
	}
	if a.Target != nil {
		if a.Target.Kind == "" {
			p.add("target.kind", "is empty")
		}
		if s := a.Target.Sensitivity; s != nil && !unit(*s) {
			p.add("target.sensitivity", "is outside [0, 1]")
		}
	}
	if a.Machine.Kind != MachineHost && a.Machine.Kind != MachineSandbox {
		p.add("machine.kind", "is not one of host, sandbox")
	}
	for i, s := range a.Signals {
		at := "signals[" + strconv.Itoa(i) + "]"
		if s.Source == "" {
			p.add(at+".source", "is empty")
		}
		if s.Verdict != "" && s.Verdict != Allow && s.Verdict != Block {
			p.add(at+".verdict", "is not one of allow, block")
		}
		if s.Score != nil && !unit(*s.Score) {
			p.add(at+".score", "is outside [0, 1]")
		}
		if s.Verdict == "" && s.Score == nil {
			p.add(at, "carries neither a verdict nor a score")
		}
	}
	if !a.Ceiling.Valid() {
		p.add("ceiling", "is not one of allow, flag, ask, block")
	}
	if a.At.IsZero() {
		p.add("at", "is not set")
	}
	return p.err(CodeInvalidAction)
}

func unit(x float64) bool { return !math.IsNaN(x) && x >= 0 && x <= 1 }
