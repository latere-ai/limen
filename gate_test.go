// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fake is a Gate whose answers the test sets.
type fake struct {
	as    Assessment
	err   error
	delay time.Duration
	calls int
}

func (f *fake) Assess(ctx context.Context, a Action) (Assessment, error) {
	f.calls++
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.as, f.err
}

func (f *fake) Answer(ctx context.Context, id string, ans Answer) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("no deadline")
	}
	return f.err
}

func (f *fake) Reliability(ctx context.Context, q ReliabilityQuery) (Reliability, error) {
	return Reliability{Subject: q.Subject}, f.err
}

func TestFailClosedOnError(t *testing.T) {
	var reported error
	g := FailClosed(&fake{err: errors.New("connection refused")}, time.Second, func(err error) { reported = err })
	for _, c := range []struct {
		ceiling, want Verdict
	}{{Allow, Ask}, {Flag, Ask}, {Ask, Ask}, {Block, Block}, {"", Block}} {
		a := validAction()
		a.Ceiling = c.ceiling
		as, err := g.Assess(context.Background(), a)
		if err != nil {
			t.Fatalf("FailClosed returned an error: %v", err)
		}
		if as.Verdict != c.want || as.Source != SourceUnavailable || as.CallID != a.CallID || as.Reason == "" {
			t.Errorf("ceiling %q: %+v, want verdict %q from %s", c.ceiling, as, c.want, SourceUnavailable)
		}
	}
	if reported == nil {
		t.Error("the error was not reported")
	}
}

func TestFailClosedOnTimeout(t *testing.T) {
	g := FailClosed(&fake{as: Assessment{Verdict: Allow}, delay: 200 * time.Millisecond}, 10*time.Millisecond, nil)
	as, err := g.Assess(context.Background(), validAction())
	if err != nil || as.Verdict != Ask || as.Source != SourceUnavailable {
		t.Errorf("timeout: %+v, %v", as, err)
	}
}

// A verdict the gate returns is narrowed to the ceiling, so a faulty gate
// cannot exceed it.
func TestFailClosedNarrows(t *testing.T) {
	g := FailClosed(&fake{as: Assessment{Verdict: Allow, Source: "learned/3"}}, 0, nil)
	a := validAction()
	a.Ceiling = Ask
	as, err := g.Assess(context.Background(), a)
	if err != nil || as.Verdict != Ask || as.Source != "learned/3" {
		t.Errorf("narrowing: %+v, %v", as, err)
	}
	a.Ceiling = Allow
	if as, _ := g.Assess(context.Background(), a); as.Verdict != Allow {
		t.Errorf("no narrowing needed: %+v", as)
	}
	// An audited allow narrowed to an ask is no longer a sample: the
	// ceiling makes the person see it.
	audited := FailClosed(&fake{as: Assessment{Verdict: Flag, Audit: &Audit{Of: Allow, Probability: 0.05}}}, 0, nil)
	a.Ceiling = Ask
	if as, _ := audited.Assess(context.Background(), a); as.Verdict != Ask || as.Audit != nil {
		t.Errorf("narrowed audit: %+v", as)
	}
	a.Ceiling = Allow
	if as, _ := audited.Assess(context.Background(), a); as.Verdict != Flag || as.Audit == nil {
		t.Errorf("an audit within the ceiling was dropped: %+v", as)
	}
}

func TestFailClosedPassesThrough(t *testing.T) {
	boom := errors.New("boom")
	g := FailClosed(&fake{err: boom}, time.Second, nil)
	if err := g.Answer(context.Background(), "asm_1", Answer{}); !errors.Is(err, boom) {
		t.Errorf("Answer: %v", err)
	}
	if _, err := g.Reliability(context.Background(), ReliabilityQuery{Subject: "s"}); !errors.Is(err, boom) {
		t.Errorf("Reliability: %v", err)
	}
	ok := FailClosed(&fake{}, time.Second, nil)
	if r, err := ok.Reliability(context.Background(), ReliabilityQuery{Subject: "s"}); err != nil || r.Subject != "s" {
		t.Errorf("Reliability: %+v, %v", r, err)
	}
	if err := ok.Answer(context.Background(), "asm_1", Answer{}); err != nil {
		t.Errorf("Answer under a timeout: %v", err)
	}
}
