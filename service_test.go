// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fake is a Predictor whose answers the test sets.
type fake struct {
	p     Prediction
	err   error
	delay time.Duration
}

func (f fake) Predict(ctx context.Context, a Action) (Prediction, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.p, f.err
}

func TestFailClosedOnError(t *testing.T) {
	var reported error
	p := FailClosed(fake{err: errors.New("connection refused")}, time.Second, func(err error) { reported = err })
	got, err := p.Predict(context.Background(), validAction())
	if err != nil {
		t.Fatalf("FailClosed returned an error: %v", err)
	}
	if got.Verdict != Ask || got.Source != SourceUnavailable || got.AskKind != AskUnsure || got.Key != key || got.Reason == "" {
		t.Errorf("fallback %+v", got)
	}
	if reported == nil {
		t.Error("the error was not reported")
	}
}

func TestFailClosedOnTimeout(t *testing.T) {
	p := FailClosed(fake{p: Prediction{Verdict: Allow}, delay: 200 * time.Millisecond}, 10*time.Millisecond, nil)
	got, err := p.Predict(context.Background(), validAction())
	if err != nil || got.Verdict != Ask || got.Source != SourceUnavailable {
		t.Errorf("timeout: %+v, %v", got, err)
	}
}

func TestFailClosedPassesAPrediction(t *testing.T) {
	want := Prediction{Key: key, Verdict: Allow, Approve: 0.97, Source: "learned/3"}
	got, err := FailClosed(fake{p: want}, 0, nil).Predict(context.Background(), validAction())
	if err != nil || got != want {
		t.Errorf("%+v, %v", got, err)
	}
}
