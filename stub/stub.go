// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package stub is an in-memory [cribrum.Service] for the test tiers of other
// repositories: a harness exercises its decision point, its ledger calls
// and its review queue against the stub without a model, a store or a
// network.
//
// The stub keeps every rule of the contract that does not depend on
// learning: idempotent predictions keyed by the action's key, one decision
// per action, answers whose role follows from the decision, profiles with
// their precedence and the built-in default, reliability reports computed
// with latere.ai/x/cribrum/estimate, replays and forgetting. Its predictions
// come from [Options.Predict], which by default knows nothing, so every
// action is an ask. It passes latere.ai/x/cribrum/conformance.
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

	"latere.ai/x/cribrum"
	"latere.ai/x/cribrum/estimate"
)

// Source is the source of the stub's predictions.
const Source = "stub/1"

// Options configure a stub.
type Options struct {
	// Predict returns the approval probability and its uncertainty for an
	// action. Nil predicts 0.5 with uncertainty 1, which every profile Cribrum
	// accepts turns into an ask.
	Predict func(cribrum.Action) (approve, uncertainty float64)
	// Clock stamps every record. Nil is time.Now.
	Clock func() time.Time
}

// Service is the in-memory service. The zero value is not usable; call
// [New].
type Service struct {
	predict func(cribrum.Action) (float64, float64)
	clock   func() time.Time

	mu       sync.Mutex
	seq      int
	cases    map[cribrum.Key]*entry
	profiles map[cribrum.Scope]cribrum.Profile
}

// entry is one action's record. The action is kept without its input.
type entry struct {
	seq        int
	at         time.Time
	action     cribrum.Action
	digest     string
	prediction *cribrum.Prediction
	decision   *cribrum.Decision
	answers    []cribrum.RecordedAnswer
}

var _ cribrum.Service = (*Service)(nil)

// New returns an empty stub.
func New(o Options) *Service {
	s := &Service{
		predict:  o.Predict,
		clock:    o.Clock,
		cases:    map[cribrum.Key]*entry{},
		profiles: map[cribrum.Scope]cribrum.Profile{},
	}
	if s.predict == nil {
		s.predict = func(cribrum.Action) (float64, float64) { return 0.5, 1 }
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	return s
}

// digest names an action by every field but its input: the input is read
// at prediction time and never kept, so it cannot tell two actions apart
// afterwards.
func digest(a cribrum.Action) (string, error) {
	a.Input = nil
	b, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("stub: digest of action %s: %w", a.ActionID, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func conflict(k cribrum.Key) error {
	return &cribrum.Error{Code: cribrum.CodeActionConflict, Fields: []string{"action_id"},
		Detail: fmt.Sprintf("action %s for %s in %s was recorded with a different action", k.ActionID, k.Subject, k.Org)}
}

// Predict implements [cribrum.Predictor].
func (s *Service) Predict(ctx context.Context, a cribrum.Action) (cribrum.Prediction, error) {
	if err := ctx.Err(); err != nil {
		return cribrum.Prediction{}, err
	}
	if err := a.Validate(); err != nil {
		return cribrum.Prediction{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.predictLocked(a, s.clock())
}

func (s *Service) predictLocked(a cribrum.Action, at time.Time) (cribrum.Prediction, error) {
	d, err := digest(a)
	if err != nil {
		return cribrum.Prediction{}, err
	}
	e := s.cases[a.Key]
	if e != nil {
		if e.digest != d {
			return cribrum.Prediction{}, conflict(a.Key)
		}
		if e.prediction != nil {
			return *e.prediction, nil
		}
	} else {
		e = s.newEntry(a, d, at)
	}
	p := s.profileLocked(cribrum.ScopeOf(a))
	approve, uncertainty := s.predict(a)
	v := p.Thresholds.Verdict(approve)
	pr := cribrum.Prediction{
		Key: a.Key, Verdict: v, Approve: approve, Uncertainty: uncertainty,
		Thresholds: p.Thresholds, Profile: p.Scope, AuditRate: p.AuditRate, Source: Source,
		Reason: fmt.Sprintf("approval %.2f against the ask band %.2f to %.2f", approve, p.Thresholds.BlockBelow, p.Thresholds.AllowAbove),
	}
	e.prediction = &pr
	return pr, nil
}

func (s *Service) newEntry(a cribrum.Action, d string, at time.Time) *entry {
	a.Input = nil
	s.seq++
	e := &entry{seq: s.seq, at: at, action: a, digest: d}
	s.cases[a.Key] = e
	return e
}

// Record implements [cribrum.Ledger].
func (s *Service) Record(ctx context.Context, d cribrum.Decision) error {
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

func (s *Service) recordLocked(d cribrum.Decision, at time.Time) error {
	e := s.cases[d.Key]
	switch {
	case e != nil && e.decision != nil:
		return &cribrum.Error{Code: cribrum.CodeAlreadyDecided, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("action %s for %s in %s is already decided %s", d.ActionID, d.Subject, d.Org, e.decision.Verdict)}
	case e == nil && d.Action == nil:
		return &cribrum.Error{Code: cribrum.CodeInvalidDecision, Fields: []string{"action"},
			Detail: "action is required when Cribrum holds no prediction for the action"}
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
	return nil
}

// Answer implements [cribrum.Ledger]. Without an authorizer, the stub takes
// By as given and stands in the subject for an empty one.
func (s *Service) Answer(ctx context.Context, a cribrum.Answer) error {
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

func (s *Service) answerLocked(a cribrum.Answer, at time.Time) error {
	e := s.cases[a.Key]
	if e == nil || e.decision == nil {
		return &cribrum.Error{Code: cribrum.CodeNoDecision, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("action %s for %s in %s has no recorded decision", a.ActionID, a.Subject, a.Org)}
	}
	by := a.By
	if by == "" {
		by = a.Subject
	}
	review := estimate.Pending(caseOf(e))
	e.answers = append(e.answers, cribrum.RecordedAnswer{Approve: a.Approve, By: by, At: at, Review: review})
	return nil
}

func pending(e *entry) bool { return estimate.Pending(caseOf(e)) }

// Cases implements [cribrum.Ledger]. The cursor is the position of the next
// case in the subject's list.
func (s *Service) Cases(ctx context.Context, q cribrum.CaseQuery) (cribrum.Page, error) {
	if err := ctx.Err(); err != nil {
		return cribrum.Page{}, err
	}
	if err := q.Validate(); err != nil {
		return cribrum.Page{}, err
	}
	start := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 {
			return cribrum.Page{}, &cribrum.Error{Code: cribrum.CodeInvalidQuery, Fields: []string{"cursor"}, Detail: "cursor " + strconv.Quote(q.Cursor) + " is not one this service issued"}
		}
		start = n
	}
	limit := q.Limit
	if limit == 0 {
		limit = cribrum.DefaultPageLimit
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
	page := cribrum.Page{Entries: []cribrum.Case{}}
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

func caseOf(e *entry) cribrum.Case {
	c := cribrum.Case{Action: e.action, Answers: slices.Clone(e.answers)}
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

// Reliability implements [cribrum.Ledger] with latere.ai/x/cribrum/estimate.
// The window holds the actions first recorded in [From, To).
func (s *Service) Reliability(ctx context.Context, q cribrum.ReliabilityQuery) (cribrum.Reliability, error) {
	if err := ctx.Err(); err != nil {
		return cribrum.Reliability{}, err
	}
	if err := q.Validate(); err != nil {
		return cribrum.Reliability{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []estimate.Row
	for _, e := range s.sortedLocked() {
		if e.action.Org != q.Org || e.action.Subject != q.Subject || e.at.Before(q.From) || !e.at.Before(q.To) {
			continue
		}
		if r, ok := estimate.RowOf(caseOf(e), q.Of); ok {
			rows = append(rows, r)
		}
	}
	return cribrum.Reliability{
		ReliabilityQuery: q,
		FalseAllow:       estimate.Rate(rows, estimate.FalseAllow),
		FalseBlock:       estimate.Rate(rows, estimate.FalseBlock),
		Certificate:      estimate.Certificate(rows, s.profileLocked(cribrum.Scope{Org: q.Org}).Certify),
	}, nil
}

// Profile implements [cribrum.Admin].
func (s *Service) Profile(ctx context.Context, sc cribrum.Scope) (cribrum.Profile, error) {
	if err := ctx.Err(); err != nil {
		return cribrum.Profile{}, err
	}
	if sc.Org == "" {
		return cribrum.Profile{}, &cribrum.Error{Code: cribrum.CodeInvalidQuery, Fields: []string{"org"}, Detail: "org is empty"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.profileLocked(sc), nil
}

// profileLocked is the profile that applies to sc: the most specific saved
// one in the order kind and agent, agent, kind, the organization, or the
// default.
func (s *Service) profileLocked(sc cribrum.Scope) cribrum.Profile {
	for _, c := range sc.Candidates() {
		if p, ok := s.profiles[c]; ok {
			return p
		}
	}
	return cribrum.DefaultProfile(cribrum.Scope{Org: sc.Org})
}

// PutProfile implements [cribrum.Admin].
func (s *Service) PutProfile(ctx context.Context, p cribrum.Profile) (cribrum.Profile, error) {
	if err := ctx.Err(); err != nil {
		return cribrum.Profile{}, err
	}
	if err := p.Validate(); err != nil {
		return cribrum.Profile{}, err
	}
	p.Thresholds, _ = p.Costs.Thresholds() // Validate checked the costs
	if p.Certify == (cribrum.Certify{}) {
		p.Certify = cribrum.DefaultCertify
	}
	p.Default = false
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[p.Scope] = p
	return p, nil
}

// Replay implements [cribrum.Admin]. Without Keep it replays into a scratch
// stub that shares this one's predictor and profiles, and keeps nothing.
func (s *Service) Replay(ctx context.Context, records iter.Seq2[cribrum.ReplayRecord, error], o cribrum.ReplayOptions) (cribrum.ReplayReport, error) {
	if err := ctx.Err(); err != nil {
		return cribrum.ReplayReport{}, err
	}
	var recs []cribrum.ReplayRecord
	for r, err := range records {
		if err != nil {
			return cribrum.ReplayReport{}, err
		}
		if len(recs) == cribrum.MaxReplayRecords {
			return cribrum.ReplayReport{}, &cribrum.Error{Code: cribrum.CodeBodyTooLarge,
				Detail: fmt.Sprintf("more than %d records", cribrum.MaxReplayRecords)}
		}
		if err := r.Validate(); err != nil {
			var e *cribrum.Error
			if !errors.As(err, &e) {
				return cribrum.ReplayReport{}, err
			}
			prefix := "records[" + strconv.Itoa(len(recs)) + "]."
			for i := range e.Fields {
				e.Fields[i] = prefix + e.Fields[i]
			}
			return cribrum.ReplayReport{}, e
		}
		recs = append(recs, r)
	}
	slices.SortStableFunc(recs, func(a, b cribrum.ReplayRecord) int { return a.At.Compare(b.At) })

	target := s
	if !o.Keep {
		s.mu.Lock()
		target = New(Options{Predict: s.predict, Clock: s.clock})
		maps.Copy(target.profiles, s.profiles)
		s.mu.Unlock()
	}
	report := cribrum.ReplayReport{Would: map[cribrum.Verdict]int{}, Kept: o.Keep}
	var rows []estimate.Row
	target.mu.Lock()
	defer target.mu.Unlock()
	for _, r := range recs {
		pr, err := target.predictLocked(r.Action, r.At)
		if err != nil {
			return cribrum.ReplayReport{}, err
		}
		if err := target.recordLocked(cribrum.Decision{Key: r.Action.Key, Verdict: r.Verdict,
			ReviewProbability: r.ReviewProbability, Source: r.Source}, r.At); err != nil {
			return cribrum.ReplayReport{}, err
		}
		report.Records++
		report.Would[pr.Verdict]++
		row := estimate.Row{Verdict: pr.Verdict, Review: r.ReviewProbability, Shown: r.Verdict.Shown()}
		if r.Approve != nil {
			report.Answered++
			if err := target.answerLocked(cribrum.Answer{Key: r.Action.Key, Approve: *r.Approve}, r.At); err != nil {
				return cribrum.ReplayReport{}, err
			}
			row.Reviewed, row.Approve = row.Shown, *r.Approve
			switch {
			case pr.Verdict == cribrum.Allow && !*r.Approve:
				report.AllowedDenied++
			case pr.Verdict == cribrum.Block && *r.Approve:
				report.BlockedApproved++
			}
		}
		rows = append(rows, row)
	}
	report.FalseAllow = estimate.Rate(rows, estimate.FalseAllow)
	return report, nil
}

// Forget implements [cribrum.Admin].
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
		return &cribrum.Error{Code: cribrum.CodeInvalidQuery, Fields: fields, Detail: "org and subject must be set"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.cases {
		if k.Org == org && k.Subject == subject {
			delete(s.cases, k)
		}
	}
	return nil
}
