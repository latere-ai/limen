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
//   - the verdict is the one Topos recorded, from the source
//     "topos/<mode>", with the review probability Topos recorded for the
//     call: in auto mode a share of automatic verdicts is drawn for review,
//     and the probability is that share. A log written before Topos
//     recorded it (before v0.11.0) carries none, and a call there was shown
//     to a person exactly when its verdict was ask or flag, so the
//     probability is one for those and zero otherwise;
//   - the answer is the first user.tool_confirmation for the call.
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
	answers := map[string]bool{}
	for _, e := range events {
		if e.Type != session.TypeUserToolConfirmation || e.Redacted() {
			continue
		}
		var c session.UserToolConfirmation
		// An answer to a network approval names an approval, not a call.
		if e.Decode(&c) != nil || c.ToolUseID == "" {
			continue
		}
		if _, seen := answers[c.ToolUseID]; seen {
			continue
		}
		switch c.Decision {
		case session.DecisionAllow:
			answers[c.ToolUseID] = true
		case session.DecisionDeny:
			answers[c.ToolUseID] = false
		}
	}
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
		if u.Risk != nil && u.Risk.Source != "" && u.Risk.Score >= 0 && u.Risk.Score <= 1 {
			score := u.Risk.Score
			a.Signals = []limen.Signal{{Source: u.Risk.Source, Score: &score}}
		}
		source := "topos"
		if u.Mode != "" {
			source += "/" + u.Mode
		}
		r := limen.ReplayRecord{At: e.Time, Action: a, Verdict: v, Source: source}
		switch {
		case u.ReviewProbability != nil:
			r.ReviewProbability = *u.ReviewProbability
		case v.Shown():
			r.ReviewProbability = 1
		}
		if approve, ok := answers[u.ToolUseID]; ok {
			r.Approve = &approve
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
