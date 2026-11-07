// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json/jsontext"
	"math"
	"slices"
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
	p.long(at+"org", k.Org)
	p.long(at+"subject", k.Subject)
	p.long(at+"action_id", k.ActionID)
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

// MaxSignals bounds an action's signals.
const MaxSignals = 64

// MaxHolds bounds the holds of an action's environment.
const MaxHolds = 64

// MaxStringBytes bounds every string a client sends, in bytes: an
// identifier, a name, a kind, an attribute key or value. Each is kept in a
// record and some are map keys or path elements, so one unbounded string
// costs memory and disk for as long as the record lives. An action's input
// is no string of this kind: it is never kept.
const MaxStringBytes = 256

// Action is one proposed action, described by what the decision point can
// observe before it runs. Limen learns from these fields and from time,
// which it keeps itself; nothing else about the action reaches the model.
type Action struct {
	Key
	// Actor is who proposed the action, when the decision point knows.
	Actor *Actor `json:"actor,omitzero"`
	// Kind is the domain of the action and Name the operation within it:
	// "tool" and "bash", "tool" and "mcp__github__merge_pull_request",
	// "egress" and "https", "repo" and "ref.update", "spend" and "charge".
	// A cost profile may be scoped to a kind.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Effect is the widest effect the action can have.
	Effect Effect `json:"effect"`
	// Reversible says whether the action can be undone, when known.
	Reversible *bool `json:"reversible,omitzero"`
	// Input is the action's input, such as a tool call's arguments. Only
	// deterministic feature extractors read it, so text an agent writes
	// never instructs a model inside Limen, and Limen never stores it.
	Input       jsontext.Value `json:"input,omitzero"`
	Target      *Target        `json:"target,omitzero"`
	Environment *Environment   `json:"environment,omitzero"`
	// Scope is what the decision point knows about the reach of the
	// action, in the words of the vocabularies (spec 011). Limen reads the
	// reach from the input and these fields as well, and the prediction
	// gives the scope it used.
	Scope *ReachClaim `json:"scope,omitzero"`
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
	// Session is the run the action belongs to, when there is one, such
	// as an agent's session. The cover rule counts the sessions of a
	// person's approvals by it, and the weight of an answer counts the
	// answers before it in its session ([SessionOf]).
	Session string `json:"session,omitempty"`
}

// Target is what an action acts on.
type Target struct {
	// Kind is the domain's word for the target: "path", "ref", "host",
	// "account".
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Sensitivity runs from 0, a scratch directory, to 1, production.
	Sensitivity *float64 `json:"sensitivity,omitzero"`
	// Class is the class of the target, a value of [TargetClasses], when
	// the decision point knows it: a claim of the scope (spec 011).
	Class string `json:"class,omitempty"`
}

// Environment is where an action runs.
type Environment struct {
	// Kind is "host" (a person's own machine), "sandbox" (an isolated
	// environment whose state is disposable) or "service".
	Kind string `json:"kind"`
	// Credentialed is true when the environment holds a credential that
	// reaches outside it, so an action there can act beyond its boundary.
	Credentialed bool `json:"credentialed"`
	// Holds are the limits the environment enforces, from [Holds]. A hold
	// outside the vocabulary is no hold.
	Holds []string `json:"holds,omitempty"`
	// Repositories is the visibility of the repositories the session
	// pushes to, from [Repositories], when the decision point knows it.
	Repositories string `json:"repositories,omitempty"`
}

// Held reports whether the environment names the hold h.
func (e *Environment) Held(h string) bool { return e != nil && slices.Contains(e.Holds, h) }

// Signal is one other opinion about an action. Source names who gave it,
// in the form "<kind>/<name>": "rules/1", "classifier/claude-auto-mode".
type Signal struct {
	Source  string   `json:"source"`
	Verdict Verdict  `json:"verdict,omitempty"`
	Score   *float64 `json:"score,omitzero"`
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
	if a.Actor != nil {
		if a.Actor.Kind == "" {
			p.add(at+"actor.kind", "is empty")
		}
		p.long(at+"actor.kind", a.Actor.Kind)
		p.long(at+"actor.id", a.Actor.ID)
		p.long(at+"actor.session", a.Actor.Session)
	}
	if a.Kind == "" {
		p.add(at+"kind", "is empty")
	}
	if a.Name == "" {
		p.add(at+"name", "is empty")
	}
	p.long(at+"kind", a.Kind)
	p.long(at+"name", a.Name)
	if !a.Effect.Valid() {
		p.add(at+"effect", "is not one of none, read, write, external")
	}
	// The input is a tool call's arguments, written by an agent, so
	// repeated names and invalid UTF-8 do not make it invalid.
	if len(a.Input) > 0 && !a.Input.IsValid(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true)) {
		p.add(at+"input", "is not valid JSON")
	}
	if a.Target != nil {
		if a.Target.Kind == "" {
			p.add(at+"target.kind", "is empty")
		}
		if s := a.Target.Sensitivity; s != nil && !unit(*s) {
			p.add(at+"target.sensitivity", "is outside [0, 1]")
		}
		p.long(at+"target.kind", a.Target.Kind)
		p.long(at+"target.id", a.Target.ID)
		p.long(at+"target.class", a.Target.Class)
	}
	if e := a.Environment; e != nil {
		if e.Kind == "" {
			p.add(at+"environment.kind", "is empty")
		}
		p.long(at+"environment.kind", e.Kind)
		p.long(at+"environment.repositories", e.Repositories)
		if len(e.Holds) > MaxHolds {
			p.add(at+"environment.holds", "has more than "+strconv.Itoa(MaxHolds)+" entries")
		}
		// Past the bound the count is the problem; the entries beyond it
		// are not read.
		for i, h := range e.Holds[:min(len(e.Holds), MaxHolds)] {
			p.long(at+"environment.holds["+strconv.Itoa(i)+"]", h)
		}
	}
	if c := a.Scope; c != nil {
		p.long(at+"scope.undo", c.Undo)
		p.long(at+"scope.contact", c.Contact)
	}
	if len(a.Attributes) > MaxAttributes {
		p.add(at+"attributes", "has more than "+strconv.Itoa(MaxAttributes)+" entries")
	}
	for k, v := range a.Attributes {
		if k == "" {
			p.add(at+"attributes", "has an empty key")
		}
		// A key past the bound is named by the map alone, so the path of
		// the field never carries it.
		path := at + "attributes." + k
		if len(k) > MaxStringBytes {
			p.add(at+"attributes", "has a key longer than "+strconv.Itoa(MaxStringBytes)+" bytes")
			path = at + "attributes"
		}
		if s, ok := v.(string); ok {
			p.long(path, s)
		} else if !scalar(v) {
			p.add(path, "is not a string, a finite number, or a boolean")
		}
	}
	if len(a.Signals) > MaxSignals {
		p.add(at+"signals", "has more than "+strconv.Itoa(MaxSignals)+" entries")
	}
	for i, s := range a.Signals[:min(len(a.Signals), MaxSignals)] {
		si := at + "signals[" + strconv.Itoa(i) + "]"
		if s.Source == "" {
			p.add(si+".source", "is empty")
		}
		p.long(si+".source", s.Source)
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
	case interface{ Float64() (float64, error) }:
		_, err := x.Float64()
		return err == nil
	}
	return false
}
