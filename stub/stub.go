// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package stub is an in-memory [limen.Service] for the test tiers of other
// repositories: a harness exercises its decision point, its ledger calls
// and its review queue against the stub without a model, a store or a
// network.
//
// The stub keeps every rule of the contract that does not depend on
// learning: idempotent predictions keyed by the action's key, one decision
// per action, answers whose role follows from the decision, profiles with
// their precedence, the built-in default and a person's own profile met
// with the organization's, the scope of each action and the cover rule with
// the weight of each answer (spec 011), reliability reports and the reports
// of organizations computed with latere.ai/x/limen/estimate, kept weeks,
// replays and forgetting. Its predictions come from [Options.Predict],
// which by default knows nothing, so every action is an ask. It knows no
// variance of a model, so an ask in the band is unsure. It passes
// latere.ai/x/limen/conformance.
package stub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/featurize"
	"latere.ai/x/limen/estimate"
)

// Source is the source of the stub's predictions.
const Source = "stub/1"

// Options configure a stub.
type Options struct {
	// Predict returns the approval probability and its uncertainty for an
	// action. Nil predicts 0.5 with uncertainty 1, which every profile Limen
	// accepts turns into an ask.
	Predict func(limen.Action) (approve, uncertainty float64)
	// Clock stamps every record. Nil is time.Now.
	Clock func() time.Time
	// MinWeight, MinSessions, AnswerFloor and AnswerFatigue are the
	// settings of the cover rule and of weights/1, as the engine's options
	// of the same names: zero is the contract's value, and a negative value
	// turns a setting off.
	MinWeight     int
	MinSessions   int
	AnswerFloor   time.Duration
	AnswerFatigue int
}

// Service is the in-memory service. The zero value is not usable; call
// [New].
type Service struct {
	predict func(limen.Action) (float64, float64)
	clock   func() time.Time
	// minWeight and minSessions are the settings of the cover rule, zero
	// for a setting that is off, and policy is weights/1.
	minWeight, minSessions int
	policy                 estimate.Policy

	mu       sync.Mutex
	seq      int
	cases    map[limen.Key]*entry
	profiles map[limen.Scope]limen.Profile
	// weeks are the kept weeks of the reports, as JSON, so no caller
	// reaches a kept week through a pointer it was given.
	weeks map[weekKey][]byte
}

type weekKey struct {
	org  string
	from int64
}

// entry is one action's record. The action is kept without its input, and
// label, scope and families are read from the input before it is dropped
// (specs 009 and 011).
type entry struct {
	seq        int
	at         time.Time
	action     limen.Action
	label      string
	scope      limen.Reach
	families   []string
	digest     string
	decidedAt  time.Time
	prediction *limen.Prediction
	// predictedBy counts the subject's answers when the prediction was made.
	predictedBy *int64
	decision    *limen.Decision
	answers     []limen.RecordedAnswer
}

var _ limen.Service = (*Service)(nil)

// New returns an empty stub.
func New(o Options) *Service {
	s := &Service{
		predict:  o.Predict,
		clock:    o.Clock,
		cases:    map[limen.Key]*entry{},
		profiles: map[limen.Scope]limen.Profile{},
		weeks:    map[weekKey][]byte{},

		minWeight:   settingOf(o.MinWeight, limen.MinWeight),
		minSessions: settingOf(o.MinSessions, limen.MinSessions),
		policy: estimate.Policy{
			Floor:   settingOf(o.AnswerFloor, limen.AnswerFloor),
			Fatigue: settingOf(o.AnswerFatigue, limen.AnswerFatigue),
		},
	}
	if s.predict == nil {
		s.predict = func(limen.Action) (float64, float64) { return 0.5, 1 }
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	return s
}

// digest names an action by every field but its input: the input is read
// at prediction time and never kept, so it cannot tell two actions apart
// afterwards.
func digest(a limen.Action) (string, error) {
	a.Input = nil
	b, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("stub: digest of action %s: %w", a.ActionID, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func conflict(k limen.Key) error {
	return &limen.Error{Code: limen.CodeActionConflict, Fields: []string{"action_id"},
		Detail: fmt.Sprintf("action %s for %s in %s was recorded with a different action", k.ActionID, k.Subject, k.Org)}
}

// Predict implements [limen.Predictor].
func (s *Service) Predict(ctx context.Context, a limen.Action) (limen.Prediction, error) {
	if err := ctx.Err(); err != nil {
		return limen.Prediction{}, err
	}
	if err := a.Validate(); err != nil {
		return limen.Prediction{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.predictLocked(a, s.clock())
}

func (s *Service) predictLocked(a limen.Action, at time.Time) (limen.Prediction, error) {
	d, err := digest(a)
	if err != nil {
		return limen.Prediction{}, err
	}
	e := s.cases[a.Key]
	if e != nil {
		if e.digest != d {
			return limen.Prediction{}, conflict(a.Key)
		}
		if e.prediction != nil {
			return *e.prediction, nil
		}
	} else {
		e = s.newEntry(a, d, at)
	}
	p := s.effectiveLocked(limen.ScopeOf(a), a.Subject)
	approve, uncertainty := s.predict(a)
	// The stub's predictor knows no answers' variance: its evidence ratio
	// is zero, so every ask in the band is unsure, as its explanations say.
	j := s.judgeLocked(e, approve, p)
	pr := limen.Prediction{
		Key: a.Key, Verdict: j.Verdict, Approve: approve, Uncertainty: uncertainty,
		AskKind:    j.Kind,
		Thresholds: p.Thresholds, Profile: p.Scope, AuditRate: p.AuditRate, Source: Source,
		Reason: reasonOf(approve, p.Thresholds, j, e.label), Scope: e.scope, Cover: j.Cover,
	}
	e.prediction = &pr
	n := s.answersLocked(a.Org, a.Subject)
	e.predictedBy = &n
	return pr, nil
}

func (s *Service) newEntry(a limen.Action, d string, at time.Time) *entry {
	var approved []*limen.Target
	for _, o := range s.subjectLocked(a.Org, a.Subject) {
		if n := len(o.answers); n > 0 && o.answers[n-1].Approve {
			approved = append(approved, o.action.Target)
		}
	}
	rd := featurize.Read(a, featurize.Contact(a.Target, approved))
	a.Input = nil
	s.seq++
	e := &entry{seq: s.seq, at: at, action: a, label: rd.Label, scope: rd.Scope, families: rd.Families, digest: d}
	s.cases[a.Key] = e
	return e
}

// Record implements [limen.Ledger].
func (s *Service) Record(ctx context.Context, d limen.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordLocked(d, s.clock())
}

func (s *Service) recordLocked(d limen.Decision, at time.Time) error {
	e := s.cases[d.Key]
	switch {
	case e != nil && e.decision != nil:
		return &limen.Error{Code: limen.CodeAlreadyDecided, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("action %s for %s in %s is already decided %s", d.ActionID, d.Subject, d.Org, e.decision.Verdict)}
	case e == nil && d.Action == nil:
		return &limen.Error{Code: limen.CodeInvalidDecision, Fields: []string{"action"},
			Detail: "action is required when Limen holds no prediction for the action"}
	case e == nil:
		dg, err := digest(*d.Action)
		if err != nil {
			return err
		}
		e = s.newEntry(*d.Action, dg, at)
	case d.Action != nil:
		dg, err := digest(*d.Action)
		if err != nil {
			return err
		}
		if dg != e.digest {
			return conflict(d.Key)
		}
	}
	d.Action = nil
	e.decision = &d
	e.decidedAt = at
	return nil
}

// Answer implements [limen.Ledger]. Without an authorizer, the stub takes
// By as given and stands in the subject for an empty one.
func (s *Service) Answer(ctx context.Context, a limen.Answer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.answerLocked(a, s.clock())
}

func (s *Service) answerLocked(a limen.Answer, at time.Time) error {
	e := s.cases[a.Key]
	if e == nil || e.decision == nil {
		return &limen.Error{Code: limen.CodeNoDecision, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("action %s for %s in %s has no recorded decision", a.ActionID, a.Subject, a.Org)}
	}
	by := a.By
	if by == "" {
		by = a.Subject
	}
	review := estimate.Pending(caseOf(e))
	e.answers = append(e.answers, limen.RecordedAnswer{Approve: a.Approve, By: by, At: at, Review: review, HowAnswered: a.Kept()})
	return nil
}

func pending(e *entry) bool { return estimate.Pending(caseOf(e)) }

// Cases implements [limen.Ledger]. The cursor is the position of the next
// case in the subject's list.
func (s *Service) Cases(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
	if err := ctx.Err(); err != nil {
		return limen.Page{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Page{}, err
	}
	start := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 {
			return limen.Page{}, &limen.Error{Code: limen.CodeInvalidQuery, Fields: []string{"cursor"}, Detail: "cursor " + strconv.Quote(q.Cursor) + " is not one this service issued"}
		}
		start = n
	}
	limit := q.Limit
	if limit == 0 {
		limit = limen.DefaultPageLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []*entry
	for _, e := range s.sortedLocked() {
		if e.action.Org != q.Org || e.action.Subject != q.Subject {
			continue
		}
		if q.ActionID != "" && e.action.ActionID != q.ActionID {
			continue
		}
		if q.Pending && !pending(e) {
			continue
		}
		all = append(all, e)
	}
	page := limen.Page{Entries: []limen.Case{}}
	for i := start; i < len(all) && i < start+limit; i++ {
		page.Entries = append(page.Entries, caseOf(all[i]))
	}
	if start+limit < len(all) {
		page.NextCursor = strconv.Itoa(start + limit)
	}
	return page, nil
}

// sortedLocked is every entry, oldest first.
func (s *Service) sortedLocked() []*entry {
	out := make([]*entry, 0, len(s.cases))
	for _, e := range s.cases {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b *entry) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return a.seq - b.seq
	})
	return out
}

func caseOf(e *entry) limen.Case {
	c := limen.Case{Action: e.action, Answers: slices.Clone(e.answers)}
	if e.prediction != nil {
		p := *e.prediction
		c.Prediction = &p
	}
	if e.decision != nil {
		d := *e.decision
		c.Decision = &d
	}
	return c
}

// Reliability implements [limen.Ledger] with latere.ai/x/limen/estimate.
// The window holds the actions first recorded in [From, To).
func (s *Service) Reliability(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
	if err := ctx.Err(); err != nil {
		return limen.Reliability{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Reliability{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.subjectLocked(q.Org, q.Subject)
	w := s.policy.Weigh(records(entries))
	var rows []estimate.Row
	for i, e := range entries {
		if e.at.Before(q.From) || !e.at.Before(q.To) {
			continue
		}
		if r, ok := estimate.RowOf(caseOf(e), q.Of); ok {
			r.Considered = w[i].Review == 1
			rows = append(rows, r)
		}
	}
	return limen.Reliability{
		ReliabilityQuery: q,
		FalseAllow:       estimate.Rate(rows, estimate.FalseAllow),
		FalseBlock:       estimate.Rate(rows, estimate.FalseBlock),
		Certificate:      estimate.Certificate(rows, s.effectiveLocked(limen.Scope{Org: q.Org}, q.Subject).Certify),
	}, nil
}

// Profile implements [limen.Admin].
func (s *Service) Profile(ctx context.Context, sc limen.Scope) (limen.Profile, error) {
	if err := ctx.Err(); err != nil {
		return limen.Profile{}, err
	}
	if err := sc.Validate(); err != nil {
		return limen.Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc.Subject != "" {
		if p, ok := s.profiles[sc]; ok {
			return p, nil
		}
		sc = limen.Scope{Org: sc.Org}
	}
	return s.profileLocked(sc), nil
}

// DeleteProfile implements [limen.Admin].
func (s *Service) DeleteProfile(ctx context.Context, sc limen.Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sc.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.profiles, sc)
	return nil
}

// effectiveLocked is the profile that applies to an action of subject
// within sc: the organization's, made stricter by the person's own.
func (s *Service) effectiveLocked(sc limen.Scope, subject string) limen.Profile {
	org := s.profileLocked(sc)
	if own, ok := s.profiles[limen.Scope{Org: sc.Org, Subject: subject}]; ok {
		return limen.Stricter(org, own)
	}
	return org
}

// profileLocked is the profile that applies to sc: the most specific saved
// one in the order kind and agent, agent, kind, the organization, or the
// default.
func (s *Service) profileLocked(sc limen.Scope) limen.Profile {
	for _, c := range sc.Candidates() {
		if p, ok := s.profiles[c]; ok {
			return p
		}
	}
	return limen.DefaultProfile(limen.Scope{Org: sc.Org})
}

// PutProfile implements [limen.Admin].
func (s *Service) PutProfile(ctx context.Context, p limen.Profile) (limen.Profile, error) {
	if err := ctx.Err(); err != nil {
		return limen.Profile{}, err
	}
	if err := p.Validate(); err != nil {
		return limen.Profile{}, err
	}
	p.Thresholds, _ = p.Costs.Thresholds() // Validate checked the costs
	if p.Certify == (limen.Certify{}) {
		p.Certify = limen.DefaultCertify
	}
	p.Default = false
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[p.Scope] = p
	return p, nil
}

// Replay implements [limen.Admin]. Without Keep it replays into a scratch
// stub that shares this one's predictor and profiles, and keeps nothing.
func (s *Service) Replay(ctx context.Context, records iter.Seq2[limen.ReplayRecord, error], o limen.ReplayOptions) (limen.ReplayReport, error) {
	if err := ctx.Err(); err != nil {
		return limen.ReplayReport{}, err
	}
	var recs []limen.ReplayRecord
	for r, err := range records {
		if err != nil {
			return limen.ReplayReport{}, err
		}
		if len(recs) == limen.MaxReplayRecords {
			return limen.ReplayReport{}, &limen.Error{Code: limen.CodeBodyTooLarge,
				Detail: fmt.Sprintf("more than %d records", limen.MaxReplayRecords)}
		}
		if err := r.Validate(); err != nil {
			var e *limen.Error
			if !errors.As(err, &e) {
				return limen.ReplayReport{}, err
			}
			prefix := "records[" + strconv.Itoa(len(recs)) + "]."
			for i := range e.Fields {
				e.Fields[i] = prefix + e.Fields[i]
			}
			return limen.ReplayReport{}, e
		}
		recs = append(recs, r)
	}
	slices.SortStableFunc(recs, func(a, b limen.ReplayRecord) int { return a.At.Compare(b.At) })

	target := s
	if !o.Keep {
		s.mu.Lock()
		target = New(Options{Predict: s.predict, Clock: s.clock, MinWeight: optionOf(s.minWeight), MinSessions: optionOf(s.minSessions),
			AnswerFloor: optionOf(s.policy.Floor), AnswerFatigue: optionOf(s.policy.Fatigue)})
		maps.Copy(target.profiles, s.profiles)
		s.mu.Unlock()
	}
	report := limen.ReplayReport{Would: map[limen.Verdict]int{}, Kept: o.Keep}
	var rows []estimate.Row
	target.mu.Lock()
	defer target.mu.Unlock()
	for _, r := range recs {
		pr, err := target.predictLocked(r.Action, r.At)
		if err != nil {
			return limen.ReplayReport{}, err
		}
		if err := target.recordLocked(limen.Decision{Key: r.Action.Key, Verdict: r.Verdict,
			ReviewProbability: r.ReviewProbability, Source: r.Source}, r.At); err != nil {
			return limen.ReplayReport{}, err
		}
		report.Records++
		report.Would[pr.Verdict]++
		row := estimate.Row{Verdict: pr.Verdict, Review: r.ReviewProbability, Shown: r.Verdict.Shown()}
		if r.Approve != nil {
			report.Answered++
			at := r.AnsweredAt
			if at.IsZero() {
				at = r.At
			}
			if err := target.answerLocked(limen.Answer{Key: r.Action.Key, Approve: *r.Approve, HowAnswered: r.HowAnswered}, at); err != nil {
				return limen.ReplayReport{}, err
			}
			row.Reviewed, row.Approve = row.Shown, *r.Approve
			switch {
			case pr.Verdict == limen.Allow && !*r.Approve:
				report.AllowedDenied++
			case pr.Verdict == limen.Block && *r.Approve:
				report.BlockedApproved++
			}
		}
		rows = append(rows, row)
	}
	report.FalseAllow = estimate.Rate(rows, estimate.FalseAllow)
	return report, nil
}

// Forget implements [limen.Admin].
func (s *Service) Forget(ctx context.Context, org, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var fields []string
	if org == "" {
		fields = append(fields, "org")
	}
	if subject == "" {
		fields = append(fields, "subject")
	}
	if len(fields) > 0 {
		return &limen.Error{Code: limen.CodeInvalidQuery, Fields: fields, Detail: "org and subject must be set"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.cases {
		if k.Org == org && k.Subject == subject {
			delete(s.cases, k)
		}
	}
	delete(s.profiles, limen.Scope{Org: org, Subject: subject})
	return nil
}

var _ limen.Reporter = (*Service)(nil)

// Report implements [limen.Reporter] with latere.ai/x/limen/estimate, as
// the engine does: a closed week is built once from the cases of every
// person of the organization and kept, and every later read returns it.
func (s *Service) Report(ctx context.Context, q limen.ReportQuery) (limen.Report, error) {
	if err := ctx.Err(); err != nil {
		return limen.Report{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	out := limen.Report{Org: q.Org, From: q.From.UTC(), To: q.To.UTC(), MinSubjects: limen.ReportMinSubjects, Weeks: []limen.WeekReport{}}
	var cases []estimate.ReportCase
	for _, e := range s.sortedLocked() {
		if e.action.Org == q.Org && e.decision != nil {
			cases = append(cases, estimate.ReportCase{Case: caseOf(e), ReceivedAt: e.at})
		}
	}
	for _, from := range estimate.Weeks(q) {
		if !limen.WeekClosed(from, now) {
			out.Weeks = append(out.Weeks, estimate.OpenWeek(from))
			continue
		}
		k := weekKey{q.Org, from.UnixNano()}
		if b, ok := s.weeks[k]; ok {
			var w limen.WeekReport
			if err := json.Unmarshal(b, &w); err != nil {
				return limen.Report{}, err
			}
			out.Weeks = append(out.Weeks, w)
			continue
		}
		w := estimate.Week(from, cases)
		b, err := json.Marshal(w)
		if err != nil {
			return limen.Report{}, err
		}
		s.weeks[k] = b
		out.Weeks = append(out.Weeks, w)
	}
	return out, nil
}
