// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package topos reads the sessions a Topos harness kept in a data
// directory and turns every recorded tool call into a limen.ReplayRecord,
// so Limen can replay a person's real history of decisions (spec 007, the
// replay gate).
//
// The sessions are read through Topos's own session store,
// latere.ai/x/topos/session/dir, never by parsing its files here, so the
// importer follows the log format Topos defines. Each agent.tool_use event
// becomes one record:
//
//   - the key is the importer's organization, the session's initiator,
//     and "<session id>/<tool_use_id>", unique across sessions;
//   - the action is a "tool" action named after the tool, with the tool's
//     declared effect for Topos's built-in tools and "write" for any other
//     tool, whose effect a Topos log does not record; the session's machine
//     gives the environment, a host counting as credentialed because it
//     holds the person's own credentials, a Cella sandbox as not; the
//     recorded risk score is a signal from its source, such as rules/1;
//     and the attribute "attended" says whether a person began the call's
//     turn, as Topos v0.56.0 and later record it and send it with the
//     action, so a replay featurizes the action as the live session did;
//   - the verdict is the one Topos recorded, with the review probability
//     Topos recorded for the call: in auto mode a share of automatic
//     verdicts is drawn for review,
//     and the probability is that share. A log written before Topos
//     recorded it (before v0.11.0) carries none, and a call there was shown
//     to a person exactly when its verdict was ask or flag, so the
//     probability is one for those and zero otherwise;
//   - the source of the verdict is "topos/<mode>/<layer>": the mode, and
//     the layer of Topos's rules that decided the call (its decided_by),
//     for example "topos/confirm/always_allow" or "topos/progressive/score",
//     so the ledger reports how often each layer is wrong. A log written
//     before Topos v0.65.0 records no layer, and a layer the pinned Topos
//     version does not name is left out; the source is then "topos/<mode>".
//     The rule within the layer (decided_by_rule) is not part of the
//     source: it is free text, such as a pattern, and a source for each
//     pattern divides the answers too finely for an estimate. Topos
//     v0.68.0 and later send a live decision under the same source, and
//     earlier versions under "topos/<mode>" alone, so the sources of a
//     kept replay can be finer than those of a live session; "applied"
//     reports both;
//   - the answer comes from the person's answers to the call, in the order
//     Topos sends them to a decision service: each user.tool_confirmation
//     of the call and, as Topos v0.66.0 and later send them, each
//     confirmation of a network approval that asked about a connection the
//     call made. A record holds one answer. For a call Topos showed (ask
//     or flag) it is the first, which is the review Limen counts in its
//     estimates; for a call that ran unseen it is the last, which is the
//     label Limen learns. A later answer to a shown call that differs from
//     the first changes the label in a live session and not in a replay.
//     A person's message in place of an answer, and an approval that
//     nothing waited for (a block), give no answer, as in Topos.
//
// A call is skipped, with the reason reported through [Options.Skipped],
// when its event is redacted, carries no verdict (a log written before
// verdicts were recorded), names an unknown verdict, does not make a
// valid record, or repeats the tool use ID of an earlier call in its
// session. A key names one action, so the first call keeps it.
package topos

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"time"

	"latere.ai/x/topos/harness/tools"
	"latere.ai/x/topos/session"
	"latere.ai/x/topos/session/dir"

	"latere.ai/x/limen"
)

// DefaultOrg is the organization of the records when neither the options
// nor the session name one. A local Topos log records no organization.
const DefaultOrg = "local"

// DefaultSubject is the subject of a session whose initiator has none.
const DefaultSubject = "local"

// AttributeAttended is the attribute of an action that says whether a
// person began the call's turn: false on a turn a trigger's message began,
// which nobody watches (Topos spec 087). Topos sends it under this name.
const AttributeAttended = "attended"

// ErrNoSessions reports a data directory with no Topos sessions in it.
var ErrNoSessions = errors.New("no Topos sessions")

// Options control an import.
type Options struct {
	// Org is the organization of every record; empty takes the session's
	// "org" metadata, and then [DefaultOrg].
	Org string
	// Subject, when set, is the subject of every record instead of each
	// session's initiator.
	Subject string
	// Skipped, when not nil, is called for every tool call that does not
	// become a record.
	Skipped func(Skip)
}

// Skip is a tool call the importer did not turn into a record.
type Skip struct {
	SessionID string
	ToolUseID string
	Reason    string
}

// The reasons a call is skipped.
const (
	ReasonRedacted       = "Topos removed the content of the call from the log"
	ReasonNoVerdict      = "the log comes from a Topos version that did not record verdicts"
	ReasonUnknownVerdict = "Limen does not know the recorded verdict"
	ReasonUndecodable    = "Limen cannot read the event of the call"
	ReasonInvalid        = "the call does not give a valid record"
	ReasonRepeated       = "the session repeats the ID of an earlier call"
)

// effects are the declared effects of Topos's built-in tools.
var effects = func() map[string]limen.Effect {
	out := map[string]limen.Effect{}
	for _, t := range tools.Builtins() {
		out[t.Definition().Name] = limen.Effect(t.Properties().Effect)
	}
	return out
}()

// EffectOf is the effect of a Topos tool: the declared effect of a
// built-in, and "write" for any other tool, whose effect a log does not
// record.
func EffectOf(name string) limen.Effect {
	if e, ok := effects[name]; ok && e.Valid() {
		return e
	}
	return limen.EffectWrite
}

// Records yields a record for every decided tool call of every session in
// the data directory, session by session in the store's order. It never
// creates the directory: a directory with no sessions yields
// [ErrNoSessions].
func Records(dataDir string, o Options) iter.Seq2[limen.ReplayRecord, error] {
	return func(yield func(limen.ReplayRecord, error) bool) {
		root := filepath.Join(dataDir, "sessions")
		if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
			yield(limen.ReplayRecord{}, fmt.Errorf("%w in %s", ErrNoSessions, dataDir))
			return
		} else if err != nil {
			yield(limen.ReplayRecord{}, err)
			return
		}
		ctx := context.Background()
		st, err := dir.Open(ctx, dataDir)
		if err != nil {
			yield(limen.ReplayRecord{}, err)
			return
		}
		cursor := ""
		for {
			page, next, err := st.List(ctx, session.ListOptions{Cursor: cursor})
			if err != nil {
				yield(limen.ReplayRecord{}, err)
				return
			}
			for _, sess := range page {
				events, err := st.Events(ctx, sess.ID, 0, 0)
				if err != nil {
					yield(limen.ReplayRecord{}, err)
					return
				}
				for _, r := range sessionRecords(sess, events, o) {
					if !yield(r, nil) {
						return
					}
				}
			}
			if next == "" {
				return
			}
			cursor = next
		}
	}
}

// sessionRecords maps one session's events to records.
func sessionRecords(sess session.Session, events []session.Event, o Options) []limen.ReplayRecord {
	skip := func(toolUseID, reason string) {
		if o.Skipped != nil {
			o.Skipped(Skip{SessionID: sess.ID, ToolUseID: toolUseID, Reason: reason})
		}
	}
	answers := answersOf(events)
	key := limen.Key{Org: o.Org, Subject: o.Subject}
	if key.Org == "" {
		key.Org = sess.Metadata["org"]
	}
	if key.Org == "" {
		key.Org = DefaultOrg
	}
	if key.Subject == "" {
		key.Subject = sess.Initiator.Subject
	}
	if key.Subject == "" {
		key.Subject = DefaultSubject
	}
	env := &limen.Environment{Kind: "host", Credentialed: true}
	if sess.Machine.Kind == session.MachineCella {
		env = &limen.Environment{Kind: "sandbox"}
	}
	actor := &limen.Actor{Kind: "agent", ID: sess.Agent.ID, Session: sess.ID}
	if actor.ID == "" {
		actor.ID = sess.Agent.Name
	}

	var out []limen.ReplayRecord
	kept := map[string]struct{}{}
	for _, e := range events {
		if e.Type != session.TypeAgentToolUse {
			continue
		}
		if e.Redacted() {
			skip("", ReasonRedacted)
			continue
		}
		var u session.AgentToolUse
		if e.Decode(&u) != nil {
			skip("", ReasonUndecodable)
			continue
		}
		if u.Verdict == "" {
			skip(u.ToolUseID, ReasonNoVerdict)
			continue
		}
		v := limen.Verdict(u.Verdict)
		if !v.Valid() {
			skip(u.ToolUseID, ReasonUnknownVerdict)
			continue
		}
		k := key
		k.ActionID = sess.ID + "/" + u.ToolUseID
		a := limen.Action{Key: k, Actor: actor, Kind: "tool", Name: u.Name, Effect: EffectOf(u.Name), Input: u.Input, Environment: env}
		if u.Attended != nil {
			a.Attributes = map[string]any{AttributeAttended: *u.Attended}
		}
		if u.Risk != nil && u.Risk.Source != "" && u.Risk.Score >= 0 && u.Risk.Score <= 1 {
			score := u.Risk.Score
			a.Signals = []limen.Signal{{Source: u.Risk.Source, Score: &score}}
		}
		source := "topos"
		if u.Mode != "" {
			source += "/" + u.Mode
		}
		if slices.Contains(session.Decided, u.DecidedBy) {
			source += "/" + u.DecidedBy
		}
		r := limen.ReplayRecord{At: e.Time, Action: a, Verdict: v, Source: source}
		switch {
		case u.ReviewProbability != nil:
			r.ReviewProbability = *u.ReviewProbability
		case v.Shown():
			r.ReviewProbability = 1
		}
		if as := answers[u.ToolUseID]; len(as) > 0 {
			ans := as[len(as)-1]
			if v.Shown() {
				ans = as[0]
			}
			r.Approve = &ans.approve
			// The time of the answer, so the time Limen measures for it is
			// the time the call waited (spec 011).
			if !ans.at.IsZero() && !ans.at.Before(r.At) {
				r.AnsweredAt = ans.at
			}
		}
		if r.Validate() != nil {
			skip(u.ToolUseID, ReasonInvalid)
			continue
		}
		if _, seen := kept[u.ToolUseID]; seen {
			skip(u.ToolUseID, ReasonRepeated)
			continue
		}
		kept[u.ToolUseID] = struct{}{}
		out = append(out, r)
	}
	return out
}

// answer is one answer of the person and the time of the event that gave
// it, zero when the log has none.
type answer struct {
	approve bool
	at      time.Time
}

// answersOf returns the person's answers in a session, by the tool use ID
// of the call each answers, in the order Topos sends them to a decision
// service (Topos spec 037). Topos sends a confirmation of an asked call
// when it settles the call, before the call runs, and a confirmation of a
// network approval when it settles the approval, after its
// approval.decided, as an answer to the call whose connection raised it.
// So the answers follow the log: a call's own confirmations, and the
// approvals' confirmations at their approval.decided. An approval that a
// confirmation settled and that has no approval.decided yet is answered
// when the session goes on, so its answer comes last, in the order of the
// approvals. Only an approval that waited for a person (verdict ask) and
// that a confirmation settled gives an answer: Topos sends none for a
// person's message in place of a confirmation. [session.Approvals] pairs
// each approval with what settled it, so the importer reads the pairing
// Topos itself uses. An answer has the time of its confirmation, or of the
// approval.decided that settled it.
func answersOf(events []session.Event) map[string][]answer {
	out := map[string][]answer{}
	add := func(toolUseID, decision string, at time.Time) {
		switch decision {
		case session.DecisionAllow:
			out[toolUseID] = append(out[toolUseID], answer{true, at})
		case session.DecisionDeny:
			out[toolUseID] = append(out[toolUseID], answer{false, at})
		}
	}
	confirmed := map[string]session.Approval{}
	var order []string
	for _, a := range session.Approvals(events) {
		if a.Request.Verdict != string(limen.Ask) || a.Request.ToolUseID == "" || a.Confirmation == nil {
			continue
		}
		confirmed[a.Request.ApprovalID] = a
		order = append(order, a.Request.ApprovalID)
	}
	settle := func(approvalID string, at time.Time) {
		if a, ok := confirmed[approvalID]; ok {
			add(a.Request.ToolUseID, a.Confirmation.Decision, at)
			delete(confirmed, approvalID)
		}
	}
	for _, e := range events {
		if e.Redacted() {
			continue
		}
		switch e.Type {
		case session.TypeUserToolConfirmation:
			var c session.UserToolConfirmation
			// A confirmation that names no call answers an approval.
			if e.Decode(&c) == nil && c.ToolUseID != "" {
				add(c.ToolUseID, c.Decision, e.Time)
			}
		case session.TypeApprovalDecided:
			var d session.ApprovalDecided
			if e.Decode(&d) == nil {
				settle(d.ApprovalID, e.Time)
			}
		}
	}
	for _, id := range order {
		settle(id, time.Time{})
	}
	return out
}
