// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"math"
	"strconv"
	"testing"
	"time"

	"latere.ai/x/limen"
)

// The cases of the calibration report (spec 011). The report puts the
// approval probabilities a service stated beside the approval rates the
// reviews measured, in ten bins, and an explanation carries the state of
// the bin of its prediction.

// calibrationOf is the subject's calibration report over [from, to).
func calibrationOf(t *testing.T, s limen.Service, from, to time.Time) limen.Calibration {
	t.Helper()
	c, err := s.Calibration(ctx, limen.CalibrationQuery{Org: org, Subject: subject, From: from, To: to})
	must(t, err)
	return c
}

// lastDays is the subject's report over the window an explanation reads,
// the last limen.CalibrationWindow, with an hour after now for a clock
// that moves while the case runs.
func lastDays(t *testing.T, s limen.Service) limen.Calibration {
	t.Helper()
	now := time.Now()
	return calibrationOf(t, s, now.Add(-limen.CalibrationWindow), now.Add(time.Hour))
}

// wilson is the 95% Wilson score interval of a proportion p over n
// trials, clamped to [0, 1].
func wilson(p, n float64) [2]float64 {
	const z = 1.959963984540054
	d := 1 + z*z/n
	c := (p + z*z/(2*n)) / d
	h := z / d * math.Sqrt(p*(1-p)/n+z*z/(4*n*n))
	return [2]float64{math.Max(0, c-h), math.Min(1, c+h)}
}

// near reports whether a and b differ by 1e-9 or less.
func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9 }

// orNull is a number that can be null, as a message shows it.
func orNull[T any](p *T) any {
	if p == nil {
		return "null"
	}
	return *p
}

// asked is one prediction of a calibration case: decided as an ask, which
// a person sees with probability 1, or not decided, and answered or not.
type asked struct {
	approve                     float64
	decided, answered, approved bool
}

// holdsAsked holds a report to the definition of spec 011 over predictions
// that were asks or not decided. Each bin has its edges, its predictions,
// the share decided, the mean approval probability of those, and its
// reviews. Every weight is 1, so the effective size of a bin is its count
// of reviews: below 20 the bin is not_estimable with a null rate, and
// otherwise its rate is its share of approvals among its reviews, with the
// Wilson interval at that count, and its state follows. The error and the
// share estimable read the estimable bins.
func holdsAsked(t *testing.T, what string, c limen.Calibration, cs []asked) {
	t.Helper()
	require(t, len(c.Bins) == limen.CalibrationBins, "%s: %d bins, want %d", what, len(c.Bins), limen.CalibrationBins)
	var total, estimable int
	var distance float64
	for i, b := range c.Bins {
		from, to := limen.CalibrationEdges(i)
		expect(t, b.From == from && b.To == to, "%s: bin %d has the edges %v and %v", what, i, b.From, b.To)
		var n, covered, reviews, approvals int
		var sum float64
		for _, x := range cs {
			if limen.CalibrationBinOf(x.approve) != i {
				continue
			}
			n++
			if !x.decided {
				continue
			}
			covered++
			sum += x.approve
			if x.answered {
				reviews++
				if x.approved {
					approvals++
				}
			}
		}
		total += n
		expect(t, b.Cases == n && b.Reviews == reviews, "%s: bin %d has %d cases and %d reviews, want %d and %d", what, i, b.Cases, b.Reviews, n, reviews)
		if covered == 0 {
			expect(t, b.Covered == 0 && b.Stated == nil, "%s: bin %d with nothing covered: covered %v, stated %v", what, i, b.Covered, orNull(b.Stated))
		} else {
			stated := sum / float64(covered)
			expect(t, near(b.Covered, float64(covered)/float64(n)) && b.Stated != nil && near(*b.Stated, stated),
				"%s: bin %d: covered %v and stated %v, want %v and %v", what, i, b.Covered, orNull(b.Stated), float64(covered)/float64(n), stated)
		}
		if reviews < limen.CalibrationMinEffective {
			expect(t, b.Observed == nil && b.Interval == nil && b.State == limen.NotEstimable,
				"%s: bin %d with an effective size of %d: observed %v, state %q, want null and not_estimable", what, i, reviews, orNull(b.Observed), b.State)
			continue
		}
		observed := float64(approvals) / float64(reviews)
		iv := wilson(observed, float64(reviews))
		if !(b.Observed != nil && near(*b.Observed, observed) && b.Interval != nil && near(b.Interval[0], iv[0]) && near(b.Interval[1], iv[1])) {
			t.Errorf("%s: bin %d: observed %v in %v, want %v in %v", what, i, orNull(b.Observed), orNull(b.Interval), observed, iv)
			continue
		}
		want := limen.CalibrationState(sum/float64(covered), &observed, &iv)
		expect(t, b.State == want, "%s: bin %d is %q, want %q", what, i, b.State, want)
		estimable += n
		distance += float64(n) * math.Abs(sum/float64(covered)-observed)
	}
	if estimable == 0 {
		expect(t, c.Error == nil, "%s: error %v with no estimable bin, want null", what, orNull(c.Error))
	} else {
		expect(t, c.Error != nil && near(*c.Error, distance/float64(estimable)), "%s: error %v, want %v", what, orNull(c.Error), distance/float64(estimable))
	}
	share := 0.0
	if total > 0 {
		share = float64(estimable) / float64(total)
	}
	expect(t, near(c.Estimable, share), "%s: estimable %v, want %v", what, c.Estimable, share)
}

// calibration (spec 011, criterion 16): on any service, the calibration
// report of a person holds the predictions first received in its window,
// in the bins of their approval probability, and each bin holds to its
// definition as the reviews arrive: null below an effective size of 20,
// which is not zero, and then the share of approvals with its interval and
// its state.
func calibration(t *testing.T, target Target) {
	s := target.New(t)
	from, to := window()
	_, err := s.Calibration(ctx, limen.CalibrationQuery{Org: org, Subject: subject, From: to, To: from})
	code(t, err, limen.CodeInvalidQuery, "to")
	_, err = s.Calibration(ctx, limen.CalibrationQuery{Org: org, From: from, To: to})
	code(t, err, limen.CodeInvalidQuery, "subject")
	holdsAsked(t, "no predictions", calibrationOf(t, s, from, to), nil)

	// Every prediction is made before any answer; two are never decided.
	cs := make([]asked, 24)
	acts := make([]limen.Action, len(cs))
	for i := range cs {
		acts[i] = action("cal-" + strconv.Itoa(i))
		cs[i].approve = predict(t, s, acts[i]).Approve
	}
	for i := range 22 {
		decide(t, s, acts[i], limen.Ask, 1, "test/1")
		cs[i].decided = true
	}
	holdsAsked(t, "no reviews", calibrationOf(t, s, from, to), cs)
	for i := range 20 {
		cs[i].answered, cs[i].approved = true, i%4 != 0
		answer(t, s, acts[i].Key, cs[i].approved)
		if i == 18 || i == 19 {
			holdsAsked(t, strconv.Itoa(i+1)+" reviews", calibrationOf(t, s, from, to), cs)
		}
	}
	// A window that ends before the cases has none of them.
	early := calibrationOf(t, s, from.Add(-time.Hour), from)
	holdsAsked(t, "a window before the cases", early, nil)
}

// calibrationWeights (spec 011, criterion 16): each review counts with
// weight one over its review probability, as in a reliability report, and
// with no weight of the answer. A bin below 1/2 counts approvals, and a bin
// of 1/2 or more counts denials and observes one minus their rate, which
// for the bins above the allow threshold is one minus the false-allow rate
// of their allows. The numbers below are calculated by hand.
func calibrationWeights(t *testing.T, target Target) {
	if target.Steered == nil {
		t.Skip("the target learns its predictions; Target.Steered is nil")
	}
	s := target.Steered(t, func(a limen.Action) (float64, float64) {
		if a.ActionID[:2] == "lo" {
			return 0.15, 0.1
		}
		return 0.95, 0.1
	})
	id := func(prefix string, i int) limen.Action { return action(prefix + "-" + strconv.Itoa(i)) }
	// [0.9, 1]: 30 asks, the first denied, 10 approvals considered; 20
	// automatic allows under audits at 0.25, 4 drawn and approved; 5
	// predictions never decided.
	for i := range 30 {
		a := id("hi", i)
		predict(t, s, a)
		decide(t, s, a, limen.Ask, 1, "test/1")
		how := limen.HowAnswered{}
		if i > 0 && i <= 10 {
			how = considered
		}
		must(t, s.Answer(ctx, limen.Answer{Key: a.Key, Approve: i > 0, HowAnswered: how}))
	}
	for i := range 20 {
		a := id("hia", i)
		predict(t, s, a)
		if i < 4 {
			decide(t, s, a, limen.Flag, 0.25, "test/1")
			answer(t, s, a.Key, true)
		} else {
			decide(t, s, a, limen.Allow, 0.25, "test/1")
		}
	}
	for i := range 5 {
		predict(t, s, id("hin", i))
	}
	// [0.1, 0.2): 25 asks, 20 approved.
	for i := range 25 {
		a := id("lo", i)
		predict(t, s, a)
		decide(t, s, a, limen.Ask, 1, "test/1")
		answer(t, s, a.Key, i < 20)
	}

	c := lastDays(t, s)
	require(t, len(c.Bins) == limen.CalibrationBins, "%d bins", len(c.Bins))
	hi, lo := c.Bins[9], c.Bins[1]
	// The one denial at weight 1, over the 50 covered cases, is 0.02; the
	// Kish size of 30 reviews at weight 1 and 4 at weight 4 is 46^2 / 94.
	d := wilson(0.02, 46.0*46/94)
	require(t, hi.Observed != nil && hi.Interval != nil && hi.Stated != nil, "[0.9, 1]: %+v", hi)
	expect(t, hi.Cases == 55 && near(hi.Covered, 50.0/55) && near(*hi.Stated, 0.95) && hi.Reviews == 34,
		"[0.9, 1]: %d cases, covered %v, stated %v, %d reviews; want 55, 50/55, 0.95, 34", hi.Cases, hi.Covered, *hi.Stated, hi.Reviews)
	expect(t, near(*hi.Observed, 0.98) && near(hi.Interval[0], 1-d[1]) && near(hi.Interval[1], 1-d[0]) && hi.State == limen.Calibrated,
		"[0.9, 1]: observed %v in %v, %q; want 0.98 in [%v, %v], calibrated", *hi.Observed, *hi.Interval, hi.State, 1-d[1], 1-d[0])
	// The denial and the 10 approvals with their details opened are
	// considered; the quick approvals are not, and they count as much.
	expect(t, hi.Considered == 11, "[0.9, 1]: %d considered reviews, want 11", hi.Considered)
	require(t, lo.Observed != nil && lo.Stated != nil, "[0.1, 0.2): %+v", lo)
	wl := wilson(0.8, 25)
	expect(t, lo.Cases == 25 && near(*lo.Observed, 0.8) && lo.Interval != nil && near(lo.Interval[0], wl[0]) && near(lo.Interval[1], wl[1]) && lo.State == limen.Overconfident,
		"[0.1, 0.2): %d cases, observed %v in %v, %q; want 25, 0.8 in %v, overconfident", lo.Cases, *lo.Observed, orNull(lo.Interval), lo.State, wl)
	wantErr := (55*math.Abs(*hi.Stated-0.98) + 25*math.Abs(*lo.Stated-0.8)) / 80
	expect(t, c.Error != nil && near(*c.Error, wantErr) && c.Estimable == 1, "error %v and estimable %v, want %v and 1", orNull(c.Error), c.Estimable, wantErr)

	// An explanation carries the state of the bin of its prediction.
	for k, want := range map[string]string{"hi-3": limen.Calibrated, "hia-7": limen.Calibrated, "hin-0": limen.Calibrated, "lo-1": limen.Overconfident} {
		x, err := s.Explain(ctx, key(k))
		must(t, err)
		expect(t, x.Calibration == want, "the explanation of %s has the calibration %q, want %q", k, x.Calibration, want)
	}
}

// explanationCalibration (spec 011): on any service, an explanation
// carries the state of the bin of its recorded prediction in the person's
// calibration report for the last 90 days, so a client need not ask for
// the report, and an action with no prediction has none.
func explanationCalibration(t *testing.T, target Target) {
	s := target.New(t)
	acts := make([]limen.Action, 22)
	preds := make([]limen.Prediction, len(acts))
	for i := range acts {
		acts[i] = action("xc-" + strconv.Itoa(i))
		preds[i] = predict(t, s, acts[i])
		decide(t, s, acts[i], limen.Ask, 1, "test/1")
	}
	explained := func(what string) {
		t.Helper()
		c := lastDays(t, s)
		for i, a := range acts {
			b, ok := c.Bin(preds[i].Approve)
			require(t, ok, "%s: no bin holds the prediction %v", what, preds[i].Approve)
			x, err := s.Explain(ctx, a.Key)
			must(t, err)
			expect(t, x.Calibration == b.State, "%s: the explanation of %s has the calibration %q, its bin %q", what, a.ActionID, x.Calibration, b.State)
		}
	}
	explained("no reviews")
	for i := range 21 {
		answer(t, s, acts[i].Key, i%3 != 0)
	}
	explained("21 reviews")

	unpredicted := action("xc-none")
	decide(t, s, unpredicted, limen.Ask, 1, "test/1")
	x, err := s.Explain(ctx, unpredicted.Key)
	must(t, err)
	expect(t, x.Calibration == "", "an action with no prediction has the calibration %q", x.Calibration)
}
