// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package topostest writes Topos session directories for tests, through
// Topos's own session store, so a test reads exactly the format a Topos
// harness writes.
package topostest

import (
	"cmp"
	"context"
	"encoding/json"
	"testing"
	"time"

	"latere.ai/x/pkg/llmdialect/ir"
	"latere.ai/x/pkg/llmdialect/lux"

	"latere.ai/x/topos/session"
	"latere.ai/x/topos/session/dir"
)

// Call is one tool call of a session and what happened to it.
type Call struct {
	ToolUseID string
	Name      string
	// Input is the call's input as JSON; empty is {}.
	Input string
	// Verdict is the recorded verdict; empty writes a call from a log that
	// predates recorded verdicts.
	Verdict string
	Mode    string
	// DecidedBy and DecidedByRule are the layer of the rules that decided
	// the call and the rule within it, as Topos v0.65.0 and later record
	// them; empty writes a call from an older log.
	DecidedBy     string
	DecidedByRule string
	// Attended is whether a person began the call's turn, as Topos v0.56.0
	// and later record it; nil writes a call from an older log.
	Attended *bool
	Risk     *session.Risk
	// Answer is the person's confirmation, "allow" or "deny"; empty writes
	// none.
	Answer   string
	Remember string
	// ReviewProbability is the probability the call recorded, as Topos
	// v0.11.0 and later record it; nil writes a call from an older log.
	ReviewProbability *float64
	// Redact redacts the call's event after it is written.
	Redact bool
	// Approvals are the network approvals the call's connections raised,
	// written after the call and its answer, as Topos v0.62.0 and later
	// write them.
	Approvals []Approval
}

// Approval is a connection of a call that the egress gateway refused, and
// what settled it (Topos spec 052).
type Approval struct {
	// Host is where the connection was going.
	Host string
	// Verdict is "ask" when a person decides and "block" when nothing
	// waits; empty is ask.
	Verdict string
	// Answer is the person's confirmation, "allow" or "deny". Message,
	// when set, is a person's message in place of a confirmation, which
	// denies the approval. With neither, the approval is not settled.
	Answer  string
	Message string
	// Unsettled leaves out the approval.decided Topos appends when it
	// settles the approval, as in a log that ends before Topos goes on.
	Unsettled bool
}

// Raw is an event written as given, for unknown or malformed events.
type Raw struct {
	Type    session.Type
	Payload string
}

// Session is one session to write.
type Session struct {
	// Initiator is the subject of the person who started the session.
	Initiator string
	// Machine is "host" or "cella"; empty is host.
	Machine string
	AgentID string
	// Org, when set, is written as the session's "org" metadata.
	Org   string
	Calls []Call
	// Raw events are written before the calls.
	Raw []Raw
}

// Start is the time of the first event a written session holds; each
// later event is one second after the one before.
var Start = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

// Write writes the sessions into dataDir and returns their ids in order.
func Write(t testing.TB, dataDir string, sessions ...Session) []string {
	t.Helper()
	ctx := context.Background()
	st, err := dir.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	at := Start
	tick := func() time.Time {
		at = at.Add(time.Second)
		return at
	}
	var ids []string
	for _, s := range sessions {
		id := session.NewID(session.PrefixSession)
		ids = append(ids, id)
		hdr := session.Session{
			Schema:    session.SchemaVersion,
			ID:        id,
			Agent:     session.AgentRef{ID: s.AgentID, Name: "coder", Version: 1},
			Initiator: session.Sender{Subject: s.Initiator, Kind: session.SenderPerson},
			Runner:    session.RunnerExternal,
			Machine:   session.Machine{Kind: s.Machine},
			CreatedAt: at,
			UpdatedAt: at,
		}
		if hdr.Machine.Kind == "" {
			hdr.Machine.Kind = session.MachineHost
		}
		if s.Org != "" {
			hdr.Metadata = map[string]string{"org": s.Org}
		}
		if err := st.Create(ctx, hdr, nil); err != nil {
			t.Fatal(err)
		}
		var events []session.Event
		add := func(typ session.Type, payload json.RawMessage) string {
			e := session.Event{ID: session.NewID(session.PrefixEvent), Type: typ, Time: tick(), Payload: payload}
			events = append(events, e)
			return e.ID
		}
		mustJSON := func(v any) json.RawMessage {
			b, err := session.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		for _, r := range s.Raw {
			add(r.Type, json.RawMessage(r.Payload))
		}
		var redact []string
		person := session.Sender{Subject: s.Initiator, Kind: session.SenderPerson}
		for _, c := range s.Calls {
			in := c.Input
			if in == "" {
				in = "{}"
			}
			eid := add(session.TypeAgentToolUse, mustJSON(session.AgentToolUse{
				ToolUseID: c.ToolUseID, Name: c.Name, Input: json.RawMessage(in),
				Risk: c.Risk, Verdict: c.Verdict, Mode: c.Mode, ReviewProbability: c.ReviewProbability,
				DecidedBy: c.DecidedBy, DecidedByRule: c.DecidedByRule, Attended: c.Attended,
			}))
			if c.Redact {
				redact = append(redact, eid)
			}
			if c.Answer != "" {
				add(session.TypeUserToolConfirmation, mustJSON(session.UserToolConfirmation{
					Sender:    person,
					ToolUseID: c.ToolUseID, Decision: c.Answer, Remember: c.Remember,
				}))
			}
			approvals := make([]string, len(c.Approvals))
			for i, a := range c.Approvals {
				approvals[i] = session.NewID(session.PrefixApproval)
				add(session.TypeApprovalRequested, mustJSON(session.ApprovalRequested{
					ApprovalID: approvals[i], ToolUseID: c.ToolUseID, Source: session.ApprovalFromEgress,
					Destination: session.Destination{Host: a.Host, Port: 443}, Reason: session.ReasonConnectionOutside,
					Verdict: cmp.Or(a.Verdict, "ask"),
				}))
			}
			for i, a := range c.Approvals {
				decided := session.ApprovalDecided{ApprovalID: approvals[i], Decision: a.Answer, By: person}
				switch {
				case a.Answer != "":
					add(session.TypeUserToolConfirmation, mustJSON(session.UserToolConfirmation{Sender: person, ApprovalID: approvals[i], Decision: a.Answer}))
				case a.Message != "":
					add(session.TypeUserMessage, mustJSON(session.UserMessage{Sender: person, Content: []lux.Block{{Type: ir.BlockText, Text: a.Message}}}))
					decided.Decision = session.DecisionDeny
				default:
					continue
				}
				if !a.Unsettled {
					add(session.TypeApprovalDecided, mustJSON(decided))
				}
			}
		}
		if len(events) > 0 {
			session.Stamp(id, 0, events)
			if _, err := st.Append(ctx, id, 0, events); err != nil {
				t.Fatal(err)
			}
		}
		for _, eid := range redact {
			if err := st.Redact(ctx, id, eid, person, "test"); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ids
}
