// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/engine/store/file"
)

func inOrg(a limen.Action, org string) limen.Action {
	a.Org = org
	return a
}

func predictOK(t *testing.T, e *engine.Engine, a limen.Action) {
	t.Helper()
	if _, err := e.Predict(ctx, a); err != nil {
		t.Fatalf("%s/%s/%s: %v", a.Org, a.Subject, a.ActionID, err)
	}
}

func wantQuota(t *testing.T, err error, what string) {
	t.Helper()
	if limen.CodeOf(err) != limen.CodeQuotaExceeded {
		t.Fatalf("%s: %v, want %s", what, err, limen.CodeQuotaExceeded)
	}
}

func TestMaxSubjectsPerOrgDefault(t *testing.T) {
	if engine.MaxSubjectsPerOrg != 10000 {
		t.Fatalf("MaxSubjectsPerOrg = %d, want 10000", engine.MaxSubjectsPerOrg)
	}
}

// With a cap of two, a third new person in one organization is refused
// while the first two keep predicting and another organization is
// unaffected; Forget makes room again.
func TestPersonCap(t *testing.T) {
	e := engine.New(engine.Options{Inline: 1, MaxSubjects: 2})
	defer e.Close()
	predictOK(t, e, bash("s1", "a1", "ls"))
	predictOK(t, e, bash("s2", "a1", "ls"))
	_, err := e.Predict(ctx, bash("s3", "a1", "ls"))
	wantQuota(t, err, "third person's prediction")
	// A refused person left nothing behind.
	if got, err := e.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s3"}); err != nil || len(got.Entries) != 0 {
		t.Fatalf("the refused person has cases %v, %v", got.Entries, err)
	}
	predictOK(t, e, bash("s1", "a2", "ls -l"))
	predictOK(t, e, bash("s2", "a2", "ls -l"))
	// A repeat of a prediction is idempotent, not a new person.
	predictOK(t, e, bash("s1", "a1", "ls"))
	predictOK(t, e, inOrg(bash("s3", "a1", "ls"), "other"))

	// A decision that carries its action also adds a person.
	a := bash("s4", "d1", "ls")
	err = e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "t", Action: &a})
	wantQuota(t, err, "a decision for a new person")
	// A person's own profile adds a person too.
	_, err = e.PutProfile(ctx, limen.Profile{Org: "o", Subject: "s5", Costs: limen.DefaultCosts, AuditRate: 0.05})
	wantQuota(t, err, "a new person's own profile")
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Subject: "s1", Costs: limen.DefaultCosts, AuditRate: 0.05}); err != nil {
		t.Fatalf("a person already in the organization saves a profile: %v", err)
	}
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Costs: limen.DefaultCosts, AuditRate: 0.05}); err != nil {
		t.Fatalf("the organization's profile: %v", err)
	}

	// Forgetting a person that is not there frees nothing.
	if err := e.Forget(ctx, "o", "nobody"); err != nil {
		t.Fatal(err)
	}
	_, err = e.Predict(ctx, bash("s3", "a1", "ls"))
	wantQuota(t, err, "after a Forget of nobody")

	if err := e.Forget(ctx, "o", "s1"); err != nil {
		t.Fatal(err)
	}
	predictOK(t, e, bash("s3", "a1", "ls"))
	_, err = e.Predict(ctx, bash("s1", "a9", "ls"))
	wantQuota(t, err, "the forgotten person coming back to a full organization")
}

// A replay that keeps its records refuses before it writes anything when
// they would add more people than the organization has room for; one that
// keeps nothing runs in a scratch store and is not held to the cap.
func TestPersonCapReplay(t *testing.T) {
	e := engine.New(engine.Options{Inline: 1, MaxSubjects: 2})
	defer e.Close()
	predictOK(t, e, bash("s1", "a1", "ls"))
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	records := func() iter.Seq2[limen.ReplayRecord, error] {
		var recs []limen.ReplayRecord
		for i, s := range []string{"r1", "r2", "r3"} {
			recs = append(recs, limen.ReplayRecord{At: at.Add(time.Duration(i) * time.Minute), Action: bash(s, "x", "ls"),
				Verdict: limen.Ask, ReviewProbability: 1, Source: "t"})
		}
		return func(yield func(limen.ReplayRecord, error) bool) {
			for _, r := range recs {
				if !yield(r, nil) {
					return
				}
			}
		}
	}
	rep, err := e.Replay(ctx, records(), limen.ReplayOptions{})
	if err != nil || rep.Records != 3 {
		t.Fatalf("a replay that keeps nothing: %+v, %v", rep, err)
	}
	_, err = e.Replay(ctx, records(), limen.ReplayOptions{Keep: true})
	wantQuota(t, err, "a replay that keeps three new people with room for one")
	for _, s := range []string{"r1", "r2", "r3"} {
		if got, err := e.Cases(ctx, limen.CaseQuery{Org: "o", Subject: s}); err != nil || len(got.Entries) != 0 {
			t.Errorf("the refused replay left cases of %s: %v, %v", s, got.Entries, err)
		}
	}
	// One new person fits, and the same replay then keeps.
	if err := e.Forget(ctx, "o", "s1"); err != nil {
		t.Fatal(err)
	}
	two := func(yield func(limen.ReplayRecord, error) bool) {
		for i, s := range []string{"r1", "r2"} {
			if !yield(limen.ReplayRecord{At: at.Add(time.Duration(i) * time.Minute), Action: bash(s, "x", "ls"),
				Verdict: limen.Ask, ReviewProbability: 1, Source: "t"}, nil) {
				return
			}
		}
	}
	if _, err := e.Replay(ctx, two, limen.ReplayOptions{Keep: true}); err != nil {
		t.Fatalf("a replay that fits: %v", err)
	}
}

// The count is read from the store when the engine first needs it: a
// reopened file store holds the people the earlier engine admitted, those
// with cases and those with only an own profile, and a Forget is not
// undone by the reopen.
func TestPersonCapSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	open := func() (*engine.Engine, *file.Store) {
		st, err := file.OpenWith(dir, file.Options{NoSync: true})
		if err != nil {
			t.Fatal(err)
		}
		return engine.New(engine.Options{Store: st, Inline: 1, MaxSubjects: 3}), st
	}
	e, st := open()
	predictOK(t, e, bash("s1", "a1", "ls"))
	predictOK(t, e, bash("s2", "a1", "ls"))
	predictOK(t, e, bash("gone", "a1", "ls"))
	if err := e.Forget(ctx, "o", "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PutProfile(ctx, limen.Profile{Org: "o", Subject: "p1", Costs: limen.DefaultCosts, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	e, st = open()
	defer func() { e.Close(); st.Close() }()
	_, err := e.Predict(ctx, bash("s3", "a1", "ls"))
	wantQuota(t, err, "a new person after a reopen with three people in the directory")
	predictOK(t, e, bash("s1", "a2", "ls"))
	predictOK(t, e, bash("p1", "a1", "ls"))
	if err := e.Forget(ctx, "o", "s2"); err != nil {
		t.Fatal(err)
	}
	predictOK(t, e, bash("s3", "a1", "ls"))
}

// Two new people at the cap never both get in: of many concurrent new
// people, exactly the cap is admitted, and the store holds that many.
func TestPersonCapConcurrent(t *testing.T) {
	const cap, workers = 5, 64
	st := store.NewMemory()
	e := engine.New(engine.Options{Store: st, Inline: 1, MaxSubjects: cap})
	defer e.Close()
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := range workers {
		wg.Go(func() {
			_, errs[i] = e.Predict(ctx, bash(fmt.Sprintf("s%d", i), "a1", "ls"))
		})
	}
	wg.Wait()
	admitted := 0
	for i, err := range errs {
		switch {
		case err == nil:
			admitted++
		case limen.CodeOf(err) != limen.CodeQuotaExceeded:
			t.Errorf("worker %d: %v", i, err)
		}
	}
	if admitted != cap {
		t.Errorf("%d people admitted, want %d", admitted, cap)
	}
	if subs, err := st.Subjects(ctx, "o"); err != nil || len(subs) != cap {
		t.Errorf("the store holds %d people, %v; want %d", len(subs), err, cap)
	}
}

// A request that fails after it was admitted gives its place back, so
// failures cannot use up an organization's room.
func TestPersonCapFailureKeepsNoPlace(t *testing.T) {
	st := newFaulty("Put", 0)
	e := engine.New(engine.Options{Store: st, Inline: 1, MaxSubjects: 1})
	defer e.Close()
	if _, err := e.Predict(ctx, bash("s1", "a1", "ls")); err == nil {
		t.Fatal("the failing store did not fail the prediction")
	}
	st.fail = ""
	predictOK(t, e, bash("s2", "a1", "ls"))
	_, err := e.Predict(ctx, bash("s3", "a1", "ls"))
	wantQuota(t, err, "a second person with a cap of one")
}

// Negative turns the cap off.
func TestPersonCapOff(t *testing.T) {
	e := engine.New(engine.Options{Inline: 1, MaxSubjects: -1})
	defer e.Close()
	for i := range 20 {
		predictOK(t, e, bash(fmt.Sprintf("s%d", i), "a1", "ls"))
	}
}

// Forget refuses a value past the string bound without reaching the store.
func TestForgetLengthCap(t *testing.T) {
	e := engine.New(engine.Options{Inline: 1})
	defer e.Close()
	long := make([]byte, limen.MaxStringBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := e.Forget(ctx, string(long), "s"); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("Forget of a long org: %v", err)
	}
	if err := e.Forget(ctx, "o", string(long)); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("Forget of a long subject: %v", err)
	}
}

// The engine refuses the report's 1,000,000-byte action id on every route
// that takes one, and keeps nothing.
func TestEngineRefusesLongStrings(t *testing.T) {
	st := store.NewMemory()
	e := engine.New(engine.Options{Store: st, Inline: 1})
	defer e.Close()
	long := string(make([]byte, 1_000_000))
	a := bash("s", long, "ls")
	if _, err := e.Predict(ctx, a); limen.CodeOf(err) != limen.CodeInvalidAction {
		t.Errorf("Predict: %v", err)
	}
	d := limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "t", Action: &a}
	if err := e.Record(ctx, d); limen.CodeOf(err) != limen.CodeInvalidDecision {
		t.Errorf("Record: %v", err)
	}
	if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: true}); limen.CodeOf(err) != limen.CodeInvalidAnswer {
		t.Errorf("Answer: %v", err)
	}
	if _, err := e.Cases(ctx, limen.CaseQuery{Org: "o", Subject: "s", ActionID: long}); limen.CodeOf(err) != limen.CodeInvalidQuery {
		t.Errorf("Cases: %v", err)
	}
	if subs, err := st.Subjects(ctx, "o"); err != nil || len(subs) != 0 {
		t.Errorf("the store holds %v, %v", subs, err)
	}
}
