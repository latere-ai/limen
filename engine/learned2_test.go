// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
)

// The approval probabilities of a publish under Careful after 0 to 7
// considered approvals, the measurement of spec 011's Overview: the owner
// saw 0.805 after two allows, and the seventh crosses 0.90.
var carefulSteps = []string{"0.500", "0.729", "0.805", "0.845", "0.870", "0.887", "0.899", "0.908"}

// steps answers seven publishes in two sessions with how, and returns the
// approval probability before each answer and after the last.
func steps(t *testing.T, e *engine.Engine, how limen.HowAnswered) []string {
	t.Helper()
	var out []string
	for i := range 7 {
		p := answerHow(t, e, publish(fmt.Sprintf("p%d", i), fmt.Sprintf("ses-%d", i%2)), true, how)
		out = append(out, fmt.Sprintf("%.3f", p.Approve))
	}
	return append(out, fmt.Sprintf("%.3f", predicted(t, e, publish("probe", "ses-0")).Approve))
}

// With considered answers, each of weight 1, learned/2 is learned/1 on
// actions of one scope: the same point under any featurizer, and the same
// likelihood. With quick approvals, weights/2 gives each 0.2, so learned/2
// moves less on each: the history of the owner never reaches 0.90 with
// seven of them, where learned/1 does.
func TestQuickApprovalsMoveLearned2Less(t *testing.T) {
	for _, m := range engine.Models() {
		e := coverEngine(t, careful, engine.Options{Model: m})
		if got := steps(t, e, considered); strings.Join(got, " ") != strings.Join(carefulSteps, " ") {
			t.Errorf("%s, considered approvals: %v, want %v", m, got, carefulSteps)
		}
	}
	one := steps(t, coverEngine(t, careful, engine.Options{Model: engine.Learned1}), limen.HowAnswered{})
	if strings.Join(one, " ") != strings.Join(carefulSteps, " ") {
		t.Errorf("learned/1, quick approvals: %v, want %v", one, carefulSteps)
	}
	two := steps(t, coverEngine(t, careful, engine.Options{Model: engine.Learned2}), limen.HowAnswered{})
	for i := 1; i < len(two); i++ {
		if two[i] <= two[i-1] || two[i] >= carefulSteps[i] {
			t.Errorf("learned/2, quick approvals: %v, each step must rise and stay below %v", two, carefulSteps)
			break
		}
	}
	t.Logf("learned/2 after quick approvals: %v", two)
}

// A prediction of learned/2 names its model, an explanation sums to its
// mean with the weighted gradient, and a learned entry and a replay come
// from the same model.
func TestLearned2NamesItsModel(t *testing.T) {
	e := coverEngine(t, careful, engine.Options{Model: engine.Learned2})
	if e.Model() != engine.Learned2 {
		t.Fatalf("model %s", e.Model())
	}
	for i := range 4 {
		answerHow(t, e, publish(fmt.Sprintf("p%d", i), "ses-1"), true, limen.HowAnswered{})
	}
	answer(t, e, makePublic("m", "ses-1"), false)
	p := predicted(t, e, publish("probe", "ses-2"))
	if p.Source != "learned/2" {
		t.Fatalf("source %q", p.Source)
	}
	x, err := e.Explain(ctx, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for _, part := range []float64{x.Parts.Action, x.Parts.Recent, x.Parts.Interaction} {
		sum += part
	}
	if x.Model.Source != "learned/2" || x.PredictedBy.Source != "learned/2" || math.Abs(sum-x.Mean) > 1e-9 || x.Approve != p.Approve {
		t.Errorf("explanation %+v", x)
	}
	l, err := e.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil || l.Model.Source != "learned/2" || len(l.Entries) != 2 {
		t.Errorf("learned %+v, %v", l, err)
	}
	rep, err := e.Replay(ctx, recs([]limen.ReplayRecord{{At: time.Now(), Action: publish("r", "ses-1"), Verdict: limen.Ask,
		ReviewProbability: 1, Source: "manual", Approve: new(true)}}), limen.ReplayOptions{})
	if err != nil || rep.Records != 1 {
		t.Errorf("replay %+v, %v", rep, err)
	}
}

// A standing rule's approval has weight 0 under weights/2, so learned/2
// leaves it out of the fit and stays at the prior, where learned/1 learns
// from it.
func TestRuleAnswersLeaveLearned2(t *testing.T) {
	rule := limen.HowAnswered{Surface: limen.SurfaceRule}
	for m, want := range map[engine.Model]string{engine.Learned1: "0.729", engine.Learned2: "0.500"} {
		e := coverEngine(t, careful, engine.Options{Model: m})
		answerHow(t, e, publish("a", "ses-1"), true, rule)
		if p := predicted(t, e, publish("b", "ses-1")); fmt.Sprintf("%.3f", p.Approve) != want {
			t.Errorf("%s after a standing rule's approval: %.3f, want %s", m, p.Approve, want)
		}
	}
}

// Two versions over one store (spec 004, criterion 10): each reads and
// writes the snapshots of its own version only. An engine of learned/2
// over a ledger that learned/1 fitted starts from the prior and fits
// version 2 from the stored cases; learned/1 keeps its snapshot, and an
// engine of learned/1 opened afterwards reads it and learns the answers
// that came since from the features/1 vectors the cases keep.
func TestVersionsSideBySide(t *testing.T) {
	st := store.NewMemory()
	one := engine.New(engine.Options{Store: st, Inline: 1})
	train(t, one, "s", 12)
	before := predictOne(t, one, "s", "probe-1")
	one.Close()
	v1, ok, err := st.Snapshot(ctx, "o", "s", engine.Version)
	if err != nil || !ok {
		t.Fatalf("no snapshot of learned/1: %v", err)
	}

	two := engine.New(engine.Options{Store: st, Model: engine.Learned2, Inline: 1})
	defer two.Close()
	p := predictOne(t, two, "s", "probe-2")
	if p.Source != "learned/2" || p.Approve <= 0.6 {
		t.Errorf("learned/2 over the cases of learned/1: %+v", p)
	}
	trainIDs(t, two, "s", "u", 4)
	if b, _, _ := st.Snapshot(ctx, "o", "s", engine.Version); !bytes.Equal(b, v1) {
		t.Error("learned/2 wrote the snapshot of learned/1")
	}
	if _, ok, _ := st.Snapshot(ctx, "o", "s", "2"); !ok {
		t.Error("learned/2 stored no snapshot of its own")
	}

	back := engine.New(engine.Options{Store: st, Inline: 1})
	defer back.Close()
	q := predictOne(t, back, "s", "probe-3")
	if q.Source != engine.Source || q.Approve <= before.Approve {
		t.Errorf("learned/1 after the answers of learned/2: %+v, before %v", q, before.Approve)
	}
	l, err := back.Learned(ctx, limen.LearnedQuery{Org: "o", Subject: "s"})
	if err != nil || l.Model.Answers != 16 {
		t.Errorf("learned/1 fitted through %d answers, want 16: %v", l.Model.Answers, err)
	}
}

// A snapshot of another version under the name of this one fails to load,
// so the prediction fails and a decision point's FailClosed asks; it never
// predicts from vectors of another featurizer.
func TestCrossedSnapshotFails(t *testing.T) {
	st := store.NewMemory()
	one := engine.New(engine.Options{Store: st, Inline: 1})
	train(t, one, "s", 3)
	one.Close()
	v1, _, _ := st.Snapshot(ctx, "o", "s", engine.Version)
	if err := st.PutSnapshot(ctx, "o", "s", "2", v1); err != nil {
		t.Fatal(err)
	}
	two := engine.New(engine.Options{Store: st, Model: engine.Learned2, Inline: 1})
	defer two.Close()
	if _, err := two.Predict(ctx, bash("s", "probe", "go test ./...")); err == nil || !strings.Contains(err.Error(), "learned/2") {
		t.Errorf("a crossed snapshot: %v", err)
	}
}

// A background engine of learned/2 over a ledger with answers predicts
// from the prior until its first fit lands, and never fits in a
// prediction.
func TestSwitchStartsFromThePrior(t *testing.T) {
	st := store.NewMemory()
	one := engine.New(engine.Options{Store: st, Inline: 1})
	train(t, one, "s", 8)
	one.Close()
	two := engine.New(engine.Options{Store: st, Model: engine.Learned2, Debounce: time.Hour})
	defer two.Close()
	p := predictOne(t, two, "s", "probe")
	if p.Approve != 0.5 || p.Verdict != limen.Ask || p.Source != "learned/2" {
		t.Errorf("the first prediction after a switch: %+v", p)
	}
	if err := two.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if q := predictOne(t, two, "s", "probe-2"); q.Approve <= 0.6 {
		t.Errorf("after the fit: %+v", q)
	}
}

func TestParseModel(t *testing.T) {
	for in, want := range map[string]engine.Model{"": engine.Learned1, "learned/1": engine.Learned1, " Learned/2 ": engine.Learned2} {
		if got, err := engine.ParseModel(in); err != nil || got != want {
			t.Errorf("ParseModel(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"learned/3", "1", "learned", "features/2"} {
		if _, err := engine.ParseModel(in); err == nil {
			t.Errorf("ParseModel(%q) accepted", in)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("an engine of an unknown model was built")
		}
	}()
	engine.New(engine.Options{Model: "learned/9"})
}
