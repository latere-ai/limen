// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"context"
	"iter"
	"time"
)

// Gate is the hot path: what a harness calls for every proposed call and
// every answer. The engine implements it in process and the HTTP client
// over the network; a harness codes against the interface and chooses at
// start-up.
type Gate interface {
	// Assess returns the assessment of one call. It is idempotent on the
	// action's Subject and CallID: a second call returns the first
	// assessment, audit sample included.
	Assess(ctx context.Context, a Action) (Assessment, error)
	// Answer records the person's decision on an assessed call. A call
	// takes one decision; a later dispute is an [AnswerContest].
	Answer(ctx context.Context, assessment string, ans Answer) error
	// Reliability reports the error rates of Limen, or of one signal
	// source, for one person over one window.
	Reliability(ctx context.Context, q ReliabilityQuery) (Reliability, error)
}

// Record is an assessment with the action it answers and the answer it
// received, if any.
type Record struct {
	Assessment Assessment `json:"assessment"`
	Action     Action     `json:"action"`
	Answer     *Answer    `json:"answer,omitempty"`
	// AnsweredBy is the verified subject of the credential that sent the
	// answer, set by Limen. It differs from the action's subject when
	// someone the authorizer allows answers on the person's behalf.
	AnsweredBy string `json:"answered_by,omitempty"`
}

// ListQuery selects one subject's records, oldest first, one page at a
// time.
type ListQuery struct {
	Subject string `json:"subject"`
	// Pending narrows the list to calls that still need an answer: asks
	// nobody has answered yet, and flagged or audited calls awaiting
	// review.
	Pending bool `json:"pending,omitempty"`
	// Cursor is the NextCursor of the previous page; empty for the first.
	Cursor string `json:"cursor,omitempty"`
	// Limit bounds the page, from 1 to [MaxListLimit]; zero is
	// [DefaultListLimit].
	Limit int `json:"limit,omitempty"`
}

// The page bounds of [Reviews.List].
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// Validate reports every field of q that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil.
func (q ListQuery) Validate() error {
	var p problems
	if q.Subject == "" {
		p.add("subject", "is empty")
	}
	if q.Limit < 0 || q.Limit > MaxListLimit {
		p.add("limit", "is outside [0, 500]")
	}
	return p.err(CodeInvalidQuery)
}

// Page is one page of records. NextCursor is empty on the last page.
type Page struct {
	Entries    []Record `json:"entries"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// Reviews is what a review surface reads: one record, and a subject's
// records, all of them or only the ones waiting for an answer.
type Reviews interface {
	Record(ctx context.Context, assessment string) (Record, error)
	List(ctx context.Context, q ListQuery) (Page, error)
}

// Admin is what an organization's operator uses: cost profiles, replays of
// recorded decisions, and forgetting a person.
type Admin interface {
	Profile(ctx context.Context, id string) (Profile, error)
	// PutProfile validates the profile, derives its thresholds, and stores
	// it. It returns the stored profile.
	PutProfile(ctx context.Context, p Profile) (Profile, error)
	// Replay assesses recorded calls in time order, each before its own
	// answer is learned, and reports what Limen would have done.
	Replay(ctx context.Context, records iter.Seq2[ReplayRecord, error]) (ReplayReport, error)
	// Forget deletes everything Limen holds about a subject: answers,
	// assessments, and the model learned from them. It is idempotent.
	Forget(ctx context.Context, subject string) error
}

// ReplayRecord is one call as a harness recorded it: the action, the
// verdict the harness applied, and the person's answer if there was one.
type ReplayRecord struct {
	Action  Action  `json:"action"`
	Verdict Verdict `json:"verdict"`
	// Audit is set when the recording harness sampled the call for review;
	// it fixes the probability with which the call was going to be
	// answered. Without it, an ask, and a flag the deterministic layers
	// forced, were going to be answered with probability one, and an allow
	// or a block with probability zero.
	Audit  *Audit  `json:"audit,omitempty"`
	Answer *Answer `json:"answer,omitempty"`
}

// ReplayReport is what Limen would have done on a recorded history.
type ReplayReport struct {
	Calls    int `json:"calls"`
	Answered int `json:"answered"`
	// Would counts Limen's verdicts on the replayed calls.
	Would map[Verdict]int `json:"would"`
	// AllowedDenied counts answered calls Limen would have allowed and the
	// person denied; BlockedApproved those it would have blocked and the
	// person approved.
	AllowedDenied   int `json:"allowed_denied"`
	BlockedApproved int `json:"blocked_approved"`
	// FalseAllow estimates the false-allow rate of Limen's would-be allows
	// from the answers the record holds with a known probability. Covered
	// is the share of those allows that had a positive probability of an
	// answer; the estimate speaks for that share only.
	FalseAllow Estimate `json:"false_allow"`
	Covered    float64  `json:"covered"`
}

// FailClosed wraps g so that assessing never fails open.
//
// When Assess returns an error, or does not return within timeout, the
// wrapper returns an [Assessment] whose verdict is Least(Ask, a.Ceiling)
// and whose Source is [SourceUnavailable], with a nil error, and hands the
// error to report when report is not nil. A verdict g does return is
// narrowed to the ceiling, so a faulty gate cannot exceed it either; when
// narrowing changes the verdict, the audit is dropped too, because the
// ceiling, not the sample, now decides whether the person sees the call.
// Answer and Reliability run under the same timeout and return their
// errors unchanged. A timeout of zero or less sets no bound.
//
// An assessment made this way has no ID, because no gate recorded the
// call, so the person's answer to it cannot be sent and is not learned. A
// harness that keeps its own decision record can include the call in a
// later replay.
func FailClosed(g Gate, timeout time.Duration, report func(error)) Gate {
	return failClosed{g: g, timeout: timeout, report: report}
}

type failClosed struct {
	g       Gate
	timeout time.Duration
	report  func(error)
}

func (f failClosed) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if f.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, f.timeout)
}

func (f failClosed) Assess(ctx context.Context, a Action) (Assessment, error) {
	ctx, cancel := f.bound(ctx)
	defer cancel()
	type result struct {
		as  Assessment
		err error
	}
	done := make(chan result, 1)
	go func() {
		as, err := f.g.Assess(ctx, a)
		done <- result{as, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		r.err = ctx.Err()
	}
	if r.err != nil {
		if f.report != nil {
			f.report(r.err)
		}
		return unavailable(a), nil
	}
	if v := Least(r.as.Verdict, a.Ceiling); v != r.as.Verdict {
		r.as.Verdict = v
		r.as.Audit = nil
	}
	return r.as, nil
}

// unavailable is the assessment of a call no gate could assess.
func unavailable(a Action) Assessment {
	v := Least(Ask, a.Ceiling)
	reason := "Limen could not assess this call in time, so it waits for a person."
	if v == Block {
		reason = "The deterministic rules block this call. Limen could not assess it."
	}
	return Assessment{CallID: a.CallID, Verdict: v, Source: SourceUnavailable, Reason: reason}
}

func (f failClosed) Answer(ctx context.Context, assessment string, ans Answer) error {
	ctx, cancel := f.bound(ctx)
	defer cancel()
	return f.g.Answer(ctx, assessment, ans)
}

func (f failClosed) Reliability(ctx context.Context, q ReliabilityQuery) (Reliability, error) {
	ctx, cancel := f.bound(ctx)
	defer cancel()
	return f.g.Reliability(ctx, q)
}
