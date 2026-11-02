// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/stub"
)

// setClock is a clock a test sets.
type setClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *setClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *setClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

var (
	balanced = limen.Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}
	strict   = limen.Costs{FalseAllow: 50, FalseBlock: 4, Ask: 1}
)

func put(t *testing.T, s limen.Service, sc limen.Scope, c limen.Costs) {
	t.Helper()
	if _, err := s.PutProfile(ctx, limen.Profile{Scope: sc, Costs: c, AuditRate: 0.05}); err != nil {
		t.Fatal(err)
	}
}

// A replay predicts each record under the meet of the organization's
// profile and the person's own, as a live prediction does (spec 010,
// criterion 4): on a history of approved test runs, a person whose own
// profile is Strict gets fewer allows than under the organization's
// Balanced.
func TestReplayUsesThePersonsProfile(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var rs []limen.ReplayRecord
	for i := range 40 {
		rs = append(rs, limen.ReplayRecord{At: start.Add(time.Duration(i) * time.Minute), Action: across(bash("s", fmt.Sprintf("r-%d", i), "go test ./..."), i),
			Verdict: limen.Ask, ReviewProbability: 1, Source: "manual", Approve: new(true)})
	}
	allows := func(own bool) int {
		e := engine.New(engine.Options{Inline: 1})
		defer e.Close()
		put(t, e, limen.Scope{Org: "o"}, balanced)
		if own {
			put(t, e, limen.Scope{Org: "o", Subject: "s"}, strict)
		}
		rep, err := e.Replay(ctx, recs(rs), limen.ReplayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Would[limen.Allow]
	}
	org, own := allows(false), allows(true)
	if org == 0 || own >= org {
		t.Errorf("a replay allows %d under the organization's profile and %d under the person's stricter one", org, own)
	}
}

// Forget removes the person's own profile with their cases, and a failure
// to remove it reaches the caller before any case goes.
func TestForgetRemovesTheOwnProfile(t *testing.T) {
	f := newFaulty("DeleteProfile", 0)
	e := engine.New(engine.Options{Store: f, Inline: 1})
	defer e.Close()
	if _, err := e.Predict(ctx, bash("s", "a", "ls")); err != nil {
		t.Fatal(err)
	}
	if err := e.Forget(ctx, "o", "s"); !errors.Is(err, errStore) {
		t.Errorf("Forget with a store that cannot remove a profile: %v", err)
	}
	if cs, _ := f.Memory.Cases(ctx, "o", "s"); len(cs) != 1 {
		t.Errorf("a failed Forget removed %d cases", 1-len(cs))
	}
	st := store.NewMemory()
	e2 := engine.New(engine.Options{Store: st, Inline: 1})
	defer e2.Close()
	put(t, e2, limen.Scope{Org: "o", Subject: "s"}, strict)
	put(t, e2, limen.Scope{Org: "o", Subject: "t"}, strict)
	if err := e2.Forget(ctx, "o", "s"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Profile(ctx, limen.Scope{Org: "o", Subject: "s"}); ok {
		t.Error("Forget kept the person's own profile")
	}
	if _, ok, _ := st.Profile(ctx, limen.Scope{Org: "o", Subject: "t"}); !ok {
		t.Error("Forget removed another person's profile")
	}
}

// A store that fails while a report reads it fails the report, and keeps
// no week.
func TestReportStoreFailures(t *testing.T) {
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	q := limen.ReportQuery{Org: "o", From: monday, To: monday.Add(limen.ReportWeek)}
	for _, method := range []string{"Subjects", "Cases", "WeekReport", "PutWeekReport"} {
		f := newFaulty(method, 0)
		c := storeCase(monday)
		if err := f.Memory.Put(ctx, c); err != nil {
			t.Fatal(err)
		}
		e := engine.New(engine.Options{Store: f, Inline: 1})
		_, err := e.Report(ctx, q)
		e.Close()
		if !errors.Is(err, errStore) {
			t.Errorf("%s failing: %v", method, err)
		}
		if _, ok, _ := f.Memory.WeekReport(ctx, "o", monday); ok && method != "PutWeekReport" {
			t.Errorf("%s failing: a week was kept", method)
		}
	}
	// A Profile failure in the person's own profile, after the
	// organization's four scopes, fails the prediction.
	f := newFaulty("Profile", 4)
	e := engine.New(engine.Options{Store: f, Inline: 1})
	defer e.Close()
	a := bash("s", "a", "ls")
	if _, err := e.Predict(ctx, a); !errors.Is(err, errStore) || f.called("Profile") != 5 {
		t.Errorf("predict with the fifth profile read failing: %v after %d reads", err, f.called("Profile"))
	}
}

// storeCase is a decided case received an hour into the week of monday.
func storeCase(monday time.Time) store.Case {
	a := bash("s", "a", "ls")
	a.Input = nil
	return store.Case{T: 0, ReceivedAt: monday.Add(time.Hour), Action: a,
		Decision: &limen.Decision{Key: a.Key, Verdict: limen.Allow, ReviewProbability: 0, Source: "m"}}
}

// Two first reads of a closed week at once keep one entry and return the
// same report.
func TestConcurrentFirstReads(t *testing.T) {
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	c := &setClock{now: monday.Add(time.Hour)}
	e := engine.New(engine.Options{Inline: 1, Clock: c.Now})
	defer e.Close()
	for i := range 6 {
		a := bash(fmt.Sprintf("s%d", i), "a", "ls")
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Allow, ReviewProbability: 0.05, Source: "m", Action: &a}); err != nil {
			t.Fatal(err)
		}
	}
	c.Set(monday.Add(limen.ReportWeek + limen.ReportClosesAfter))
	q := limen.ReportQuery{Org: "o", From: monday, To: monday.Add(limen.ReportWeek)}
	var wg sync.WaitGroup
	out := make([]limen.Report, 8)
	for i := range out {
		wg.Go(func() {
			r, err := e.Report(ctx, q)
			if err != nil {
				t.Error(err)
			}
			out[i] = r
		})
	}
	wg.Wait()
	for i := range out {
		if !reflect.DeepEqual(out[i], out[0]) || out[i].Weeks[0].State != limen.WeekReady {
			t.Errorf("read %d: %+v, read 0: %+v", i, out[i].Weeks[0], out[0].Weeks[0])
		}
	}
}

// The engine and the stub report alike on one scripted history of many
// persons, agents, verdicts and late answers (spec 010, criterion 15),
// and the history reaches a ready week divided by agent, a too_few week,
// and a withheld field.
func TestEngineAndStubReportOrganizationsAlike(t *testing.T) {
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	now := monday
	clock := func() time.Time { return now }
	e := engine.New(engine.Options{Inline: 1, Clock: clock})
	defer e.Close()
	s := stub.New(stub.Options{Clock: clock})
	services := []limen.Service{e, s}
	decisions := []struct {
		verdict limen.Verdict
		prob    float64
	}{
		{limen.Allow, 0}, {limen.Allow, 0.2}, {limen.Flag, 0.2}, {limen.Flag, 1},
		{limen.Ask, 1}, {limen.Ask, 0.2}, {limen.Block, 0}, {limen.Block, 0.2},
	}
	agents := []string{"agt_a", "agt_b", "agt_c", ""}
	rng := rand.New(rand.NewPCG(7, 10))
	type pending struct {
		key     limen.Key
		approve bool
	}
	var late []pending
	for w := range 3 {
		// The second week has four persons; the others have nine.
		persons := 9
		if w == 1 {
			persons = 4
		}
		for i := range 120 {
			now = monday.Add(time.Duration(w)*limen.ReportWeek + time.Duration(i)*time.Hour)
			p := rng.IntN(persons)
			if rng.IntN(3) == 0 {
				p = 0
			}
			a := bash(fmt.Sprintf("s%d", p), fmt.Sprintf("w%d-%d", w, i), "go test ./...")
			a.Actor = nil
			if agent := agents[rng.IntN(len(agents))]; agent != "" {
				a.Actor = &limen.Actor{Kind: "agent", ID: agent}
			}
			d := decisions[rng.IntN(len(decisions))]
			for _, svc := range services {
				if err := svc.Record(ctx, limen.Decision{Key: a.Key, Verdict: d.verdict, ReviewProbability: d.prob, Source: "rules/1", Action: &a}); err != nil {
					t.Fatal(err)
				}
			}
			switch rng.IntN(4) {
			case 0:
				late = append(late, pending{a.Key, rng.IntN(4) > 0})
			case 1, 2:
				approve := rng.IntN(4) > 0
				for _, svc := range services {
					if err := svc.Answer(ctx, limen.Answer{Key: a.Key, Approve: approve}); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	now = monday.Add(5 * limen.ReportWeek)
	for _, l := range late {
		for _, svc := range services {
			if err := svc.Answer(ctx, limen.Answer{Key: l.key, Approve: l.approve}); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := limen.ReportQuery{Org: "o", From: monday, To: monday.Add(4 * limen.ReportWeek)}
	want, err := s.Report(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Report(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Errorf("the engine reports %s\nthe stub %s", g, w)
	}
	states := []string{}
	for _, w := range want.Weeks {
		states = append(states, w.State)
	}
	if !reflect.DeepEqual(states, []string{limen.WeekReady, limen.WeekTooFew, limen.WeekReady, limen.WeekTooFew}) {
		t.Fatalf("states %v", states)
	}
	w := want.Weeks[0]
	if len(w.Agents) == 0 || w.Other == nil || len(w.Agents[0].Withheld) == 0 || w.Total.Applied.FalseAllow == nil || w.Total.Applied.FalseAllow.Unanswered == 0 {
		t.Errorf("the script does not reach every part of a report: %+v", w)
	}
}
