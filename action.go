// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json"
	"math"
	"strconv"

	"latere.ai/x/pkg/verdict"
)

// Verdict is the shared decision vocabulary, latere.ai/x/pkg/verdict.
type Verdict = verdict.Verdict

// The four verdicts, from most to least permissive.
const (
	Allow = verdict.Allow
	Flag  = verdict.Flag
	Ask   = verdict.Ask
	Block = verdict.Block
)

// Key names one proposed action in every record Limen keeps. The decision
// point chooses ActionID and keeps it unique within the organization and
// subject; Topos, for example, uses the session id and the tool call's id.
type Key struct {
	// Org is the organization whose cost profile applies and whose records
	// these are.
	Org string `json:"org"`
	// Subject is the person on whose behalf the action runs and whose
	// approvals Limen learns, rendered as the issuer and the token's sub
	// joined by a pipe. It may name a group whose members answer for it.
	Subject  string `json:"subject"`
	ActionID string `json:"action_id"`
}

func (k Key) check(p *problems, at string) {
	if k.Org == "" {
		p.add(at+"org", "is empty")
	}
	if k.Subject == "" {
		p.add(at+"subject", "is empty")
	}
	if k.ActionID == "" {
		p.add(at+"action_id", "is empty")
	}
}

// Effect is what an action can do to the world: nothing, read, write
// inside the boundary of the system that runs it, or reach outside it (a
// network call, a message sent, money moved).
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

// MaxAttributes bounds an action's attributes.
const MaxAttributes = 64

// Action is one proposed action, described by what the decision point can
// observe before it runs. Limen learns from these fields and from time,
// which it keeps itself; nothing else about the action reaches the model.
type Action struct {
	Key
	// Actor is who proposed the action, when the decision point knows.
	Actor *Actor `json:"actor,omitempty"`
	// Kind is the domain of the action and Name the operation within it:
	// "tool" and "bash", "tool" and "mcp__github__merge_pull_request",
	// "egress" and "https", "repo" and "ref.update", "spend" and "charge".
	// A cost profile may be scoped to a kind.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Effect is the widest effect the action can have.
	Effect Effect `json:"effect"`
	// Reversible says whether the action can be undone, when known.
	Reversible *bool `json:"reversible,omitempty"`
	// Input is the action's input, such as a tool call's arguments. Only
	// deterministic feature extractors read it, so text an agent writes
	// never instructs a model inside Limen, and Limen never stores it.
	Input       json.RawMessage `json:"input,omitempty"`
	Target      *Target         `json:"target,omitempty"`
	Environment *Environment    `json:"environment,omitempty"`
	// Attributes are further observable facts the decision point's domain
	// has, such as an amount or whether a branch is protected. Values are
	// strings, numbers or booleans.
	Attributes map[string]any `json:"attributes,omitempty"`
	// Signals are other opinions about the action: a rule's score, an LLM
	// judge's verdict. Limen treats them as evidence about the person's
	// answer, and reports the reliability of each source.
	Signals []Signal `json:"signals,omitempty"`
}

// Actor is who proposed an action.
type Actor struct {
	// Kind is "agent", "service" or "person".
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	// Session is the run the action belongs to, when there is one.
	Session string `json:"session,omitempty"`
}

// Target is what an action acts on.
type Target struct {
	// Kind is the domain's word for the target: "path", "ref", "host",
	// "account".
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Sensitivity runs from 0, a scratch directory, to 1, production.
	Sensitivity *float64 `json:"sensitivity,omitempty"`
}

// Environment is where an action runs.
type Environment struct {
	// Kind is "host" (a person's own machine), "sandbox" (an isolated
	// environment whose state is disposable) or "service".
	Kind string `json:"kind"`
	// Credentialed is true when the environment holds a credential that
	// reaches outside it, so an action there can act beyond its boundary.
	Credentialed bool `json:"credentialed"`
}

// Signal is one other opinion about an action. Source names who gave it,
// in the form "<kind>/<name>": "rules/1", "classifier/claude-auto-mode".
type Signal struct {
	Source  string   `json:"source"`
	Verdict Verdict  `json:"verdict,omitempty"`
	Score   *float64 `json:"score,omitempty"`
}

// Validate reports every field of a that breaks the contract, as one
// [*Error] with [CodeInvalidAction], or nil.
func (a Action) Validate() error {
	var p problems
	a.check(&p, "")
	return p.err(CodeInvalidAction)
}

func (a Action) check(p *problems, at string) {
	a.Key.check(p, at)
	if a.Actor != nil && a.Actor.Kind == "" {
		p.add(at+"actor.kind", "is empty")
	}
	if a.Kind == "" {
		p.add(at+"kind", "is empty")
	}
	if a.Name == "" {
		p.add(at+"name", "is empty")
	}
	if !a.Effect.Valid() {
		p.add(at+"effect", "is not one of none, read, write, external")
	}
	if len(a.Input) > 0 && !json.Valid(a.Input) {
		p.add(at+"input", "is not valid JSON")
	}
	if a.Target != nil {
		if a.Target.Kind == "" {
			p.add(at+"target.kind", "is empty")
		}
		if s := a.Target.Sensitivity; s != nil && !unit(*s) {
			p.add(at+"target.sensitivity", "is outside [0, 1]")
		}
	}
	if a.Environment != nil && a.Environment.Kind == "" {
		p.add(at+"environment.kind", "is empty")
	}
	if len(a.Attributes) > MaxAttributes {
		p.add(at+"attributes", "has more than "+strconv.Itoa(MaxAttributes)+" entries")
	}
	for k, v := range a.Attributes {
		if k == "" {
			p.add(at+"attributes", "has an empty key")
		}
		if !scalar(v) {
			p.add(at+"attributes."+k, "is not a string, a finite number, or a boolean")
		}
	}
	for i, s := range a.Signals {
		si := at + "signals[" + strconv.Itoa(i) + "]"
		if s.Source == "" {
			p.add(si+".source", "is empty")
		}
		if s.Verdict != "" && !s.Verdict.Valid() {
			p.add(si+".verdict", "is not one of allow, flag, ask, block")
		}
		if s.Score != nil && !unit(*s.Score) {
			p.add(si+".score", "is outside [0, 1]")
		}
		if s.Verdict == "" && s.Score == nil {
			p.add(si, "carries neither a verdict nor a score")
		}
	}
}

func unit(x float64) bool { return !math.IsNaN(x) && x >= 0 && x <= 1 }

// scalar reports whether v is an attribute value: a string, a boolean, or
// a finite number of any Go numeric type.
func scalar(v any) bool {
	switch x := v.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0)
	case float64:
		return !math.IsNaN(x) && !math.IsInf(x, 0)
	case json.Number:
		_, err := x.Float64()
		return err == nil
	}
	return false
}
