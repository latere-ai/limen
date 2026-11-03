// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package stub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/stub"
)

// The calibration report holds the predictions first recorded in its
// window, and an explanation reads the last 90 days at the stub's clock.
func TestCalibrationWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s := stub.New(stub.Options{Clock: func() time.Time { return now }, Predict: func(limen.Action) (float64, float64) { return 0.93, 0.1 }})
	for i := range 20 {
		a := act("w" + string(rune('a'+i)))
		if _, err := s.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := s.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "m"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Answer(ctx, limen.Answer{Key: a.Key, Approve: true}); err != nil {
			t.Fatal(err)
		}
	}
	in := limen.CalibrationQuery{Org: "o", Subject: "s", From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	c, err := s.Calibration(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	// 20 approvals of 0.93: 1 in [0.83, 1], calibrated.
	if b := c.Bins[9]; b.Cases != 20 || b.Observed == nil || *b.Observed != 1 || b.State != limen.Calibrated {
		t.Errorf("inside the window: %+v", b)
	}
	out := in
	out.From = now.Add(time.Second)
	if c, _ := s.Calibration(ctx, out); c.Bins[9].Cases != 0 || c.Estimable != 0 {
		t.Errorf("outside the window: %+v", c.Bins[9])
	}
	x, err := s.Explain(ctx, act("wa").Key)
	if err != nil || x.Calibration != limen.Calibrated {
		t.Fatalf("an explanation: %q, %v", x.Calibration, err)
	}
	now = now.Add(limen.CalibrationWindow + time.Second)
	if x, _ := s.Explain(ctx, act("wa").Key); x.Calibration != limen.NotEstimable {
		t.Errorf("an explanation after 90 days: %q", x.Calibration)
	}
}

func TestCalibrationErrors(t *testing.T) {
	s := stub.New(stub.Options{})
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Calibration(c, limen.CalibrationQuery{}); !errors.Is(err, context.Canceled) {
		t.Errorf("under a canceled context: %v", err)
	}
	_, err := s.Calibration(ctx, limen.CalibrationQuery{Org: "o", Subject: "s"})
	codeOf(t, err, limen.CodeInvalidQuery)
}
