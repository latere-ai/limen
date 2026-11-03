// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/featurize"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/estimate"
)

// settingOf is the setting an option sets: zero is the contract's value
// def, and a negative value turns the setting off.
func settingOf[T int | time.Duration](option, def T) T {
	switch {
	case option == 0:
		return def
	case option < 0:
		return 0
	}
	return option
}

// optionOf is the option that sets n, the inverse of settingOf: a setting
// that is off is a negative option.
func optionOf[T int | time.Duration](n T) T {
	if n == 0 {
		return -1
	}
	return n
}

// reach is the case's scope and families: those it keeps, or for a case
// from before labels/2 the ones its records give (spec 011, migration).
func reach(c store.Case) (limen.Reach, []string) {
	if c.Scope != nil {
		return *c.Scope, c.Families
	}
	return featurize.Migrated(c.Action, c.Featurizer, c.A, c.C, label(c))
}

// recordOf is a case as the cover rule reads it.
func recordOf(c store.Case) estimate.Record {
	s, fams := reach(c)
	r := estimate.Record{T: c.T, ActionID: c.Action.ActionID, Label: label(c), Families: fams, Scope: s,
		Session: limen.SessionOf(c.Action, c.ReceivedAt), DecidedAt: c.DecidedAt, Answers: c.Answers}
	r.Shown = c.Decision != nil && c.Decision.Verdict.Shown()
	if p := c.Prediction; p != nil && p.Source != limen.SourceUnavailable {
		approve := p.Approve
		r.Suggested = &approve
	}
	return r
}

// records are a subject's cases as the cover rule reads them.
func records(cases []store.Case) []estimate.Record {
	out := make([]estimate.Record, len(cases))
	for i, c := range cases {
		out[i] = recordOf(c)
	}
	return out
}

// ledger is a subject's cases as the cover rule and the reading of
// contact need them, in the order of T: the records, and the target of
// each case whose latest answer approves. The engine keeps it beside the
// cached model and puts each case it writes, so it holds what the store
// holds.
type ledger struct {
	recs    []estimate.Record
	targets []*limen.Target
	at      map[string]int
}

func newLedger(cases []store.Case) *ledger {
	l := &ledger{at: make(map[string]int, len(cases))}
	for _, c := range cases {
		l.put(c)
	}
	return l
}

// put keeps the case as the store now holds it.
func (l *ledger) put(c store.Case) {
	var t *limen.Target
	if a, ok := latest(c); ok && a.Approve {
		t = c.Action.Target
		if t == nil {
			t = &limen.Target{}
		}
	}
	if i, ok := l.at[c.Action.ActionID]; ok {
		l.recs[i], l.targets[i] = recordOf(c), t
		return
	}
	l.at[c.Action.ActionID] = len(l.recs)
	l.recs = append(l.recs, recordOf(c))
	l.targets = append(l.targets, t)
}

// approved is the targets of the cases whose latest answer approves, the
// ones Limen reads contact from.
func (l *ledger) approved() []*limen.Target {
	var out []*limen.Target
	for _, t := range l.targets {
		if t != nil {
			out = append(out, t)
		}
	}
	return out
}

// answeredCases counts the records with an answer.
func answeredCases(recs []estimate.Record) int {
	n := 0
	for _, r := range recs {
		if len(r.Answers) > 0 {
			n++
		}
	}
	return n
}

// settings are the settings of the cover rule under the profile: the
// greater of the engine's and the profile's, in the window of the model.
func (e *Engine) settings(p limen.Profile) estimate.Settings {
	return estimate.Settings{
		MinWeight:   max(e.minWeight, p.Consent.MinWeight),
		MinSessions: max(e.minSessions, p.Consent.MinSessions),
		Window:      e.opts.MaxLabels,
	}
}

// approvedTargets is the targets of the cases whose latest answer
// approves, the ones Limen reads contact from.
func approvedTargets(cases []store.Case) []*limen.Target {
	return newLedger(cases).approved()
}
