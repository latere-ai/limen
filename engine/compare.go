// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"slices"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/estimate"
)

// The replay comparison of two model versions (spec 011, criterion 21):
// before an operator switches an installation from learned/1 to learned/2,
// a history is replayed through both, each version in its own scratch
// engine, and the would-be suggestions are put side by side, with the
// calibration report of each version over its replayed predictions. The
// cover rule reads weights/1 and the same cases in both, so a verdict that
// differs comes from the model: its approval probability.

// CompareOptions control a comparison.
type CompareOptions struct {
	// Refit is the number of new answers after which each scratch engine
	// refits, before its next prediction; zero is 1, so each prediction
	// learns every answer that came before it, as a live engine does about
	// a second after an answer.
	Refit int
	// Subject, when set, compares the cases of this person only.
	Subject string
}

// Comparison is what two model versions would have suggested on one
// recorded history.
type Comparison struct {
	// Base is the version the installation serves, learned/1, and Next
	// the version it can switch to, learned/2.
	Base Model `json:"base"`
	Next Model `json:"next"`
	// Overall is the comparison over every person, and Persons the one of
	// each person, in the order of the organization and the subject.
	Overall Tally   `json:"overall"`
	Persons []Tally `json:"persons"`
	// Changed lists each record whose suggestion differs between the
	// versions, in the order of time.
	Changed []Change `json:"changed"`
}

// Tally compares the suggestions of the two versions for one person, or
// for every person.
type Tally struct {
	Org     string `json:"org,omitempty"`
	Subject string `json:"subject,omitempty"`
	// Records are the actions replayed, and Answered those with an answer.
	Records  int `json:"records"`
	Answered int `json:"answered"`
	// Base and Next count the suggestions of each version by [Kind]: allow,
	// block, or ask with its kind.
	Base map[string]int `json:"base"`
	Next map[string]int `json:"next"`
	// Changes counts the records whose suggestion differs, by the kind of
	// Base and the kind of Next, as "<base> -> <next>".
	Changes map[string]int `json:"changes"`
	// Withheld counts the records Base allows and Next does not, and
	// Granted the records Next allows and Base does not.
	Withheld int `json:"withheld"`
	Granted  int `json:"granted"`
	// Looser counts the records where Next is more permissive than Base,
	// and Outside those of them that the cover rule does not hold: all but
	// an allow of Next on an action that the person's considered approvals
	// cover. The cover rule holds no block, so an ask of Next where Base
	// blocks is outside it.
	Looser  int `json:"looser"`
	Outside int `json:"outside"`
	// CoverDiffers counts the records whose cover differs between the two
	// versions. The cover rule reads weights/1 in both, so it differs only
	// where a client showed the suggestion before the answer, and the two
	// versions suggested on different sides of one half.
	CoverDiffers int `json:"cover_differs"`
	// BrierBase and BrierNext are the Brier scores of the two versions
	// over the answered records: the mean of (approve - y)^2, where y is 1
	// for an approval and 0 for a denial. Lower is better. They are null
	// without an answer. Only the actions a person saw have an answer, so
	// the two scores compare the versions on the same records and do not
	// estimate the error over all actions.
	BrierBase *float64 `json:"brier_base"`
	BrierNext *float64 `json:"brier_next"`
	// CalibrationBase and CalibrationNext are the calibration report of
	// each version (GET /v1/calibration) over the replayed predictions of
	// the version and the reviews of the history: its ten bins, and its
	// error, the mean distance between the stated approval probability and
	// the approval rate that the reviews measured over the bins that are
	// estimable, null where none is.
	CalibrationBase limen.Calibration `json:"calibration_base"`
	CalibrationNext limen.Calibration `json:"calibration_next"`
	// Recorded counts the verdicts of the predictions a ledger recorded,
	// which a replay of decision point logs does not have, and Agree the
	// records where Base suggests the recorded verdict again.
	Recorded map[string]int `json:"recorded,omitempty"`
	Agree    int            `json:"agree,omitzero"`

	brier [2]float64
}

// Change is one record whose suggestion differs between the versions.
type Change struct {
	Org      string     `json:"org"`
	Subject  string     `json:"subject"`
	ActionID string     `json:"action_id"`
	Label    string     `json:"label"`
	At       time.Time  `json:"at"`
	Base     Suggestion `json:"base"`
	Next     Suggestion `json:"next"`
	// Looser is true when Next suggests a verdict that is more permissive
	// than the one of Base: allow where Base asks or blocks, or ask where
	// Base blocks. Outside is true when the cover rule does not hold the
	// looser verdict, as [Tally.Outside] counts it.
	Looser  bool `json:"looser"`
	Outside bool `json:"outside"`
}

// looser reports whether next is more permissive than base, and whether
// it is so outside the cover rule: not an allow of a covered action.
func looser(base, next limen.Prediction) (bool, bool) {
	l := rank(next.Verdict) > rank(base.Verdict)
	return l, l && (next.Verdict != limen.Allow || !next.Cover.Covered)
}

// Suggestion is what one version suggested for a record.
type Suggestion struct {
	Kind    string      `json:"kind"`
	Approve float64     `json:"approve"`
	Cover   limen.Cover `json:"cover"`
}

// Kind names a suggestion: "allow", "block", or "ask (<kind>)".
func Kind(p limen.Prediction) string {
	if p.Verdict == limen.Ask && p.AskKind != "" {
		return "ask (" + p.AskKind + ")"
	}
	return string(p.Verdict)
}

// rank orders verdicts from the least permissive: block, ask, allow.
func rank(v limen.Verdict) int {
	switch v {
	case limen.Allow:
		return 2
	case limen.Block:
		return 0
	}
	return 1
}

// event is one thing that happened to a recorded action: its prediction,
// its decision, or an answer.
type event struct {
	at   time.Time
	step int
	what int // 0 predict, 1 decide, 2 answer
	ans  int
}

// step is one recorded action of a history.
type step struct {
	at     time.Time
	action limen.Action
	// stored is the case of a ledger, whose vectors and scope a scratch
	// engine reads in place of the input, which a ledger does not keep.
	stored    *store.Case
	decision  *limen.Decision
	decidedAt time.Time
	answers   []limen.Answer
	answerAt  []time.Time
	recorded  *limen.Prediction
}

// Compare replays records, as [Engine.Replay] reads them, through
// [Learned1] and [Learned2] and compares the would-be suggestions. Each
// version runs in a scratch engine over a memory store with this engine's
// profiles, cover rule and weights, so nothing is kept. Each action is
// predicted at its time, its decision recorded then, and its answer learned
// at its answer time, so a prediction sees every answer that came before
// it.
func (e *Engine) Compare(ctx context.Context, records iter.Seq2[limen.ReplayRecord, error], o CompareOptions) (Comparison, error) {
	recs, err := collect(records)
	if err != nil {
		return Comparison{}, err
	}
	var steps []step
	for _, r := range recs {
		if o.Subject != "" && r.Action.Subject != o.Subject {
			continue
		}
		d := decisionOf(r, false)
		s := step{at: r.At, action: r.Action, decision: &d, decidedAt: r.At}
		if r.Approve != nil {
			s.answers, s.answerAt = []limen.Answer{answerOf(r)}, []time.Time{answeredAt(r)}
		}
		steps = append(steps, s)
	}
	return e.compare(ctx, steps, o)
}

// CompareLedger replays the cases of this engine's ledger in the
// organizations orgs through [Learned1] and [Learned2], as [Engine.Compare]
// does with records. A ledger keeps no input, so each scratch engine reads
// the vectors and the scope that the case kept. Each person's tally also
// counts the verdicts the ledger recorded.
func (e *Engine) CompareLedger(ctx context.Context, orgs []string, o CompareOptions) (Comparison, error) {
	var steps []step
	for _, org := range orgs {
		subjects, err := e.st.Subjects(ctx, org)
		if err != nil {
			return Comparison{}, err
		}
		for _, subject := range subjects {
			if o.Subject != "" && subject != o.Subject {
				continue
			}
			unlock := e.lock(org, subject)
			cases, err := e.st.Cases(ctx, org, subject)
			unlock()
			if err != nil {
				return Comparison{}, err
			}
			for _, c := range cases {
				steps = append(steps, stepOf(c))
			}
		}
	}
	return e.compare(ctx, steps, o)
}

// stepOf is the step of a stored case.
func stepOf(c store.Case) step {
	s := step{at: c.ReceivedAt, action: c.Action, recorded: c.Prediction}
	kept := store.Case{ReceivedAt: c.ReceivedAt, Action: c.Action, Featurizer: c.Featurizer, A: c.A, C: c.C,
		Label: c.Label, Labels: c.Labels, Scope: c.Scope, Families: c.Families}
	s.stored = &kept
	if c.Decision != nil {
		d := *c.Decision
		d.Action = nil
		s.decision = &d
		s.decidedAt = c.DecidedAt
		if s.decidedAt.Before(s.at) {
			s.decidedAt = s.at
		}
	}
	for _, a := range c.Answers {
		if s.decision == nil {
			break
		}
		s.answers = append(s.answers, limen.Answer{Key: c.Action.Key, Approve: a.Approve, By: a.By, HowAnswered: a.HowAnswered})
		s.answerAt = append(s.answerAt, latestOf(a.At, s.decidedAt))
	}
	return s
}

func latestOf(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}

// collect reads and checks the records of a replay, as Replay does, and
// orders them by their times.
func collect(records iter.Seq2[limen.ReplayRecord, error]) ([]limen.ReplayRecord, error) {
	var recs []limen.ReplayRecord
	for r, err := range records {
		if err != nil {
			return nil, err
		}
		if len(recs) == limen.MaxReplayRecords {
			return nil, &limen.Error{Code: limen.CodeBodyTooLarge,
				Detail: fmt.Sprintf("more than %d records", limen.MaxReplayRecords)}
		}
		if err := r.Validate(); err != nil {
			return nil, prefixed(err, len(recs))
		}
		recs = append(recs, r)
	}
	slices.SortStableFunc(recs, func(a, b limen.ReplayRecord) int { return a.At.Compare(b.At) })
	return recs, nil
}

// compare runs the steps through a scratch engine of each version and
// tallies the suggestions.
func (e *Engine) compare(ctx context.Context, steps []step, o CompareOptions) (Comparison, error) {
	var actions []limen.Action
	for _, s := range steps {
		actions = append(actions, s.action)
	}
	versions := [2]Model{Learned1, Learned2}
	var scratch [2]*Engine
	for i, m := range versions {
		mem := store.NewMemory()
		if err := copyProfilesOf(ctx, e, mem, actions); err != nil {
			return Comparison{}, err
		}
		scratch[i] = build(Options{Store: mem, Model: m, Inline: cmp.Or(o.Refit, 1), MaxLabels: e.opts.MaxLabels,
			MinWeight: optionOf(e.minWeight), MinSessions: optionOf(e.minSessions),
			AnswerFloor: optionOf(e.policy.Floor), AnswerFatigue: optionOf(e.policy.Fatigue),
			MaxSubjects: -1})
	}

	// The steps in the order of time, and of the person at one time, so a
	// history gives one comparison whatever order it came in.
	slices.SortStableFunc(steps, func(a, b step) int {
		return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.action.Org, b.action.Org), cmp.Compare(a.action.Subject, b.action.Subject))
	})
	var events []event
	for i, s := range steps {
		events = append(events, event{at: s.at, step: i})
		if s.decision != nil {
			events = append(events, event{at: s.decidedAt, step: i, what: 1})
		}
		for j, at := range s.answerAt {
			events = append(events, event{at: at, step: i, what: 2, ans: j})
		}
	}
	slices.SortStableFunc(events, func(a, b event) int {
		return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.step, b.step), cmp.Compare(a.what, b.what), cmp.Compare(a.ans, b.ans))
	})

	preds := make([][2]limen.Prediction, len(steps))
	labels := make([]string, len(steps))
	for _, ev := range events {
		if err := ctx.Err(); err != nil {
			return Comparison{}, err
		}
		s := steps[ev.step]
		for i, x := range scratch {
			var err error
			switch ev.what {
			case 0:
				preds[ev.step][i], labels[ev.step], err = x.predictStep(ctx, s)
			case 1:
				err = x.recordAt(ctx, *s.decision, s.decidedAt)
			default:
				err = x.answerAt(ctx, s.answers[ev.ans], s.answerAt[ev.ans])
			}
			if err != nil {
				return Comparison{}, fmt.Errorf("engine: comparison under %s, action %s of %s in %s: %w",
					versions[i], s.action.ActionID, s.action.Subject, s.action.Org, err)
			}
		}
	}

	out := Comparison{Base: versions[0], Next: versions[1], Overall: newTally("", ""), Persons: []Tally{}, Changed: []Change{}}
	persons := map[subjectKey]*Tally{}
	for i, s := range steps {
		k := subjectKey{s.action.Org, s.action.Subject}
		t := persons[k]
		if t == nil {
			nt := newTally(k.org, k.subject)
			t = &nt
			persons[k] = t
		}
		base, next := preds[i][0], preds[i][1]
		var y *bool
		if n := len(s.answers); n > 0 {
			y = &s.answers[n-1].Approve
		}
		t.add(base, next, y, s.recorded)
		out.Overall.add(base, next, y, s.recorded)
		if Kind(base) != Kind(next) {
			out.Changed = append(out.Changed, Change{Org: k.org, Subject: k.subject, ActionID: s.action.ActionID,
				Label: labels[i], At: s.at,
				Base: Suggestion{Kind: Kind(base), Approve: base.Approve, Cover: base.Cover},
				Next: Suggestion{Kind: Kind(next), Approve: next.Approve, Cover: next.Cover}})
			c := &out.Changed[len(out.Changed)-1]
			c.Looser, c.Outside = looser(base, next)
		}
	}
	// Each report reads the window of its history: the time of the first
	// action, and of the last one included.
	windowOf := func(keep func(step) bool) limen.CalibrationQuery {
		var q limen.CalibrationQuery
		for _, st := range steps {
			if !keep(st) {
				continue
			}
			if q.From.IsZero() {
				q.From = st.at
			}
			q.To = st.at.Add(time.Nanosecond)
		}
		return q
	}
	window := windowOf(func(step) bool { return true })
	var all [2][]estimate.Row
	for k, t := range persons {
		q := windowOf(func(st step) bool { return st.action.Org == k.org && st.action.Subject == k.subject })
		q.Org, q.Subject = k.org, k.subject
		for i, x := range scratch {
			rows, err := x.replayRows(ctx, k)
			if err != nil {
				return Comparison{}, err
			}
			all[i] = append(all[i], rows...)
			c := estimate.Calibrate(q, rows)
			if i == 0 {
				t.CalibrationBase = c
			} else {
				t.CalibrationNext = c
			}
		}
	}
	out.Overall.CalibrationBase = estimate.Calibrate(window, all[0])
	out.Overall.CalibrationNext = estimate.Calibrate(window, all[1])
	out.Overall.finish()
	for _, t := range persons {
		t.finish()
		out.Persons = append(out.Persons, *t)
	}
	slices.SortFunc(out.Persons, func(a, b Tally) int { return cmp.Or(cmp.Compare(a.Org, b.Org), cmp.Compare(a.Subject, b.Subject)) })
	return out, nil
}

// predictStep predicts the action of a step, and returns the label of its
// case. A step of a ledger puts the case it kept into the scratch store
// first, with a new ordinal, so the prediction reads its vectors and its
// scope and not the input.
func (e *Engine) predictStep(ctx context.Context, s step) (limen.Prediction, string, error) {
	if s.stored != nil {
		t, err := e.st.NextT(ctx, s.action.Org, s.action.Subject)
		if err != nil {
			return limen.Prediction{}, "", err
		}
		c := *s.stored
		c.T = t
		if c.Digest, err = digest(c.Action); err != nil {
			return limen.Prediction{}, "", err
		}
		if err := e.st.Put(ctx, c); err != nil {
			return limen.Prediction{}, "", err
		}
	}
	p, err := e.predictAt(ctx, s.action, s.at)
	if err != nil {
		return limen.Prediction{}, "", err
	}
	c, _, err := e.st.Case(ctx, s.action.Key)
	return p, label(c), err
}

// replayRows are the rows of the learned source over the cases a scratch
// engine replayed for the person k, as its calibration report reads them:
// each recorded prediction with its decision and its review, and whether
// the review is considered.
func (e *Engine) replayRows(ctx context.Context, k subjectKey) ([]estimate.Row, error) {
	cases, err := e.st.Cases(ctx, k.org, k.subject)
	if err != nil {
		return nil, err
	}
	w := e.policy.Weigh(records(cases))
	var rows []estimate.Row
	for i, c := range cases {
		if r, ok := estimate.RowOf(limen.Case{Prediction: c.Prediction, Decision: c.Decision, Answers: c.Answers}, limen.OfLearned); ok {
			r.Considered = w[i].Review == 1
			rows = append(rows, r)
		}
	}
	return rows, nil
}

func newTally(org, subject string) Tally {
	return Tally{Org: org, Subject: subject, Base: map[string]int{}, Next: map[string]int{}, Changes: map[string]int{}}
}

// add counts one record.
func (t *Tally) add(base, next limen.Prediction, y *bool, recorded *limen.Prediction) {
	t.Records++
	kb, kn := Kind(base), Kind(next)
	t.Base[kb]++
	t.Next[kn]++
	if kb != kn {
		t.Changes[kb+" -> "+kn]++
	}
	switch {
	case base.Verdict == limen.Allow && next.Verdict != limen.Allow:
		t.Withheld++
	case next.Verdict == limen.Allow && base.Verdict != limen.Allow:
		t.Granted++
	}
	if l, o := looser(base, next); l {
		t.Looser++
		if o {
			t.Outside++
		}
	}
	if base.Cover != next.Cover {
		t.CoverDiffers++
	}
	if y != nil {
		t.Answered++
		v := 0.0
		if *y {
			v = 1
		}
		t.brier[0] += (base.Approve - v) * (base.Approve - v)
		t.brier[1] += (next.Approve - v) * (next.Approve - v)
	}
	if recorded != nil {
		if t.Recorded == nil {
			t.Recorded = map[string]int{}
		}
		t.Recorded[Kind(*recorded)]++
		if recorded.Verdict == base.Verdict {
			t.Agree++
		}
	}
}

// finish turns the sums of the Brier scores into their means.
func (t *Tally) finish() {
	if t.Answered == 0 {
		return
	}
	b, n := t.brier[0]/float64(t.Answered), t.brier[1]/float64(t.Answered)
	t.BrierBase, t.BrierNext = &b, &n
}

// copyProfilesOf copies into dst every profile of the organizations of the
// actions that one of them can resolve to, the persons' own profiles
// included.
func copyProfilesOf(ctx context.Context, e *Engine, dst store.Store, actions []limen.Action) error {
	seen := map[limen.Scope]bool{}
	for _, a := range actions {
		for _, c := range append(limen.ScopeOf(a).Candidates(), limen.Scope{Org: a.Org, Subject: a.Subject}) {
			if seen[c] {
				continue
			}
			seen[c] = true
			p, ok, err := e.st.Profile(ctx, c)
			if err != nil {
				return err
			}
			if ok {
				if err := dst.PutProfile(ctx, p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
