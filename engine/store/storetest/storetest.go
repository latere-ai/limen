// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package storetest is the contract every [store.Store] passes: what each
// method keeps, returns and refuses, written once and run against the
// memory store, the file store, and any store added later.
//
// [Run] covers one open store. [RunReopen] covers what a persistent store
// keeps across a close and an open of the same directory. Across a reopen
// a store promises less about [store.Store.NextT] than within one process:
// an ordinal handed out but never stored may be handed out again, because no
// case holds it, while an ordinal a stored case holds never is.
package storetest

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
)

var ctx = context.Background()

// Case returns a case of subject in org with action id, ordinal t, and
// every field set, so a round trip that drops one is seen.
func Case(org, subject, id string, t int64) store.Case {
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC).Add(time.Duration(t) * time.Second)
	key := limen.Key{Org: org, Subject: subject, ActionID: id}
	reversible := false
	return store.Case{
		T:          t,
		ReceivedAt: at,
		Digest:     fmt.Sprintf("digest-%s-%d", id, t),
		Action: limen.Action{
			Key:         key,
			Actor:       &limen.Actor{Kind: "agent", ID: "agt", Session: "ses"},
			Kind:        "tool",
			Name:        "bash",
			Effect:      limen.EffectWrite,
			Reversible:  &reversible,
			Target:      &limen.Target{Kind: "path", ID: "build/"},
			Environment: &limen.Environment{Kind: "sandbox", Credentialed: true},
			Attributes:  map[string]any{"files": 3.0, "protected": true, "remote": "origin"},
			Signals:     []limen.Signal{{Source: "classifier/x", Verdict: limen.Allow}},
		},
		Featurizer: "features/1",
		A:          []float64{0.25, 0.5, float64(t)},
		C:          []float64{1, 0, -0.5},
		Prediction: &limen.Prediction{Key: key, Verdict: limen.Ask, Approve: 0.5, Uncertainty: 1.2,
			Thresholds: limen.Thresholds{AllowAbove: 0.9, BlockBelow: 0.25}, Profile: limen.Scope{Org: org},
			AuditRate: 0.05, Source: "learned/1", Reason: "no answers yet"},
		Decision:  &limen.Decision{Key: key, Verdict: limen.Ask, ReviewProbability: 1, Source: "manual"},
		DecidedAt: at.Add(time.Second),
		Answers:   []limen.RecordedAnswer{{Approve: true, By: subject, At: at.Add(2 * time.Second), Review: true}},
	}
}

// Run runs the contract against stores open returns, one fresh empty store
// per subtest.
func Run(t *testing.T, open func(t *testing.T) store.Store) {
	t.Helper()
	tests := []struct {
		name string
		run  func(*testing.T, store.Store)
	}{
		{"CaseMissing", caseMissing},
		{"PutAndCase", putAndCase},
		{"PutReplaces", putReplaces},
		{"CasesOrderedAndScoped", casesOrderedAndScoped},
		{"InputNeverKept", inputNeverKept},
		{"NextT", nextT},
		{"NextTConcurrent", nextTConcurrent},
		{"Profiles", profiles},
		{"PersonProfiles", personProfiles},
		{"DeleteProfile", deleteProfile},
		{"Subjects", subjects},
		{"WeekReports", weekReports},
		{"Snapshots", snapshots},
		{"Forget", forget},
		{"Canceled", canceled},
		{"Copies", copies},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			t.Cleanup(func() { _ = s.Close() })
			tc.run(t, s)
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// expect fails the test with the message when ok is false. Every check goes
// through it, so the suite's own failure branches are one line.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

func get(t *testing.T, s store.Store, c store.Case) (store.Case, bool) {
	t.Helper()
	got, ok, err := s.Case(ctx, c.Action.Key)
	must(t, err)
	return got, ok
}

func list(t *testing.T, s store.Store, org, subject string) []store.Case {
	t.Helper()
	cs, err := s.Cases(ctx, org, subject)
	must(t, err)
	return cs
}

func ids(cs []store.Case) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Action.ActionID
	}
	return out
}

// same compares two cases through their JSON encoding, the form every
// store keeps them in, so a time zone or an empty slice that a store
// normalizes does not count as a difference.
func same(a, b store.Case) bool { return encode(a) == encode(b) }

func encode(c store.Case) string {
	b, err := json.Marshal(c, json.Deterministic(true))
	if err != nil {
		return "unencodable: " + err.Error()
	}
	return string(b)
}

func caseMissing(t *testing.T, s store.Store) {
	_, ok := get(t, s, Case("o", "s", "nope", 0))
	expect(t, !ok, "an empty store has a case")
	cs := list(t, s, "o", "s")
	expect(t, len(cs) == 0, "an empty store lists %v", ids(cs))
}

func putAndCase(t *testing.T, s store.Store) {
	c := Case("o", "https://issuer.example|usr_42", "ses_9f/toolu_01", 4)
	must(t, s.Put(ctx, c))
	got, ok := get(t, s, c)
	expect(t, ok && same(got, c), "round trip, found %v:\n got %s\nwant %s", ok, encode(got), encode(c))
}

func putReplaces(t *testing.T, s store.Store) {
	x := Case("o", "s", "x", 5)
	y := Case("o", "s", "y", 5)
	must(t, s.Put(ctx, x))
	must(t, s.Put(ctx, y))
	x.Answers = append(x.Answers, limen.RecordedAnswer{Approve: false, By: "s", At: x.ReceivedAt, Review: false})
	x.Digest = "replaced"
	must(t, s.Put(ctx, x))
	got, _ := get(t, s, x)
	expect(t, got.Digest == "replaced" && len(got.Answers) == 2, "Put did not replace: %s", encode(got))
	// Two cases with one ordinal keep the order they were first stored in.
	cs := list(t, s, "o", "s")
	expect(t, slices.Equal(ids(cs), []string{"x", "y"}), "after a replace the subject lists %v, want [x y]", ids(cs))
}

func casesOrderedAndScoped(t *testing.T, s store.Store) {
	for _, c := range []store.Case{
		Case("o", "s", "c", 2), Case("o", "s", "a", 0), Case("o", "s", "b", 1),
		Case("o", "other", "z", 0), Case("p", "s", "w", 0),
	} {
		must(t, s.Put(ctx, c))
	}
	cs := list(t, s, "o", "s")
	expect(t, slices.Equal(ids(cs), []string{"a", "b", "c"}), "Cases = %v, want [a b c] in the order of T", ids(cs))
	cs = list(t, s, "o", "other")
	expect(t, slices.Equal(ids(cs), []string{"z"}), "another subject lists %v", ids(cs))
	cs = list(t, s, "p", "s")
	expect(t, slices.Equal(ids(cs), []string{"w"}), "another organization lists %v", ids(cs))
}

func inputNeverKept(t *testing.T, s store.Store) {
	c := Case("o", "s", "in", 0)
	c.Action.Input = []byte(`{"command":"rm -rf /"}`)
	must(t, s.Put(ctx, c))
	got, _ := get(t, s, c)
	expect(t, got.Action.Input == nil, "Case kept the input %s", got.Action.Input)
	cs := list(t, s, "o", "s")
	expect(t, len(cs) == 1 && cs[0].Action.Input == nil, "Cases kept the input")
	expect(t, c.Action.Input != nil, "Put cleared the caller's input")
}

func next(t *testing.T, s store.Store, org, subject string) int64 {
	t.Helper()
	n, err := s.NextT(ctx, org, subject)
	must(t, err)
	return n
}

func nextT(t *testing.T, s store.Store) {
	for want := range int64(5) {
		got := next(t, s, "o", "s")
		expect(t, got == want, "NextT = %d, want %d", got, want)
	}
	got := next(t, s, "o", "other")
	expect(t, got == 0, "another subject starts at %d", got)
	got = next(t, s, "p", "s")
	expect(t, got == 0, "the same subject in another organization starts at %d", got)
	got = next(t, s, "o", "s")
	expect(t, got == 5, "NextT after another subject's = %d, want 5", got)
}

func nextTConcurrent(t *testing.T, s store.Store) {
	const n = 64
	var (
		mu   sync.Mutex
		seen = map[int64]bool{}
		wg   sync.WaitGroup
	)
	for range n {
		wg.Go(func() {
			v, err := s.NextT(ctx, "o", "s")
			expect(t, err == nil, "NextT: %v", err)
			mu.Lock()
			defer mu.Unlock()
			expect(t, err != nil || !seen[v], "NextT handed out %d twice", v)
			seen[v] = err == nil
		})
	}
	wg.Wait()
	for v := range int64(n) {
		expect(t, seen[v], "NextT skipped %d", v)
	}
}

func profile(s limen.Scope, ask float64) limen.Profile {
	return limen.Profile{Scope: s, Costs: limen.Costs{FalseAllow: 10, FalseBlock: 4, Ask: ask}, AuditRate: 0.05,
		Certify: limen.DefaultCertify, Thresholds: limen.Thresholds{AllowAbove: 1 - ask/10, BlockBelow: ask / 4}}
}

func profiles(t *testing.T, s store.Store) {
	org := limen.Scope{Org: "o"}
	kind := limen.Scope{Org: "o", Kind: "tool"}
	agent := limen.Scope{Org: "o", Kind: "tool", Agent: "agt"}
	_, ok, err := s.Profile(ctx, org)
	must(t, err)
	expect(t, !ok, "an empty store has a profile")
	must(t, s.PutProfile(ctx, profile(org, 1)))
	must(t, s.PutProfile(ctx, profile(kind, 1.5)))
	for _, c := range []struct {
		s    limen.Scope
		ok   bool
		want float64
	}{{org, true, 1}, {kind, true, 1.5}, {agent, false, 0}, {limen.Scope{Org: "p"}, false, 0}} {
		p, ok, err := s.Profile(ctx, c.s)
		must(t, err)
		expect(t, ok == c.ok && (!ok || (p.Costs.Ask == c.want && p.Scope == c.s)), "Profile(%+v) = %+v, %v", c.s, p, ok)
	}
	must(t, s.PutProfile(ctx, profile(org, 2)))
	p, _, err := s.Profile(ctx, org)
	must(t, err)
	expect(t, p.Costs.Ask == 2, "PutProfile did not replace: %+v", p)
	p, _, err = s.Profile(ctx, kind)
	must(t, err)
	expect(t, p.Costs.Ask == 1.5, "replacing one scope changed another: %+v", p)
}

// person is the subject of a person's own profile and cases, with a pipe
// and a slash, as a rendered subject has.
const person = "https://issuer.example|usr_42"

func personProfiles(t *testing.T, s store.Store) {
	org := limen.Scope{Org: "o"}
	mine := limen.Scope{Org: "o", Subject: person}
	theirs := limen.Scope{Org: "o", Subject: "s"}
	must(t, s.PutProfile(ctx, profile(org, 1)))
	must(t, s.PutProfile(ctx, profile(mine, 1.5)))
	_, ok, err := s.Profile(ctx, theirs)
	must(t, err)
	expect(t, !ok, "another person's profile is found")
	for _, c := range []struct {
		s    limen.Scope
		want float64
	}{{org, 1}, {mine, 1.5}} {
		p, ok, err := s.Profile(ctx, c.s)
		must(t, err)
		expect(t, ok && p.Costs.Ask == c.want && p.Scope == c.s, "Profile(%+v) = %+v, %v", c.s, p, ok)
	}
}

func deleteProfile(t *testing.T, s store.Store) {
	org := limen.Scope{Org: "o"}
	agent := limen.Scope{Org: "o", Agent: "agt"}
	mine := limen.Scope{Org: "o", Subject: person}
	for _, sc := range []limen.Scope{org, agent, mine} {
		must(t, s.PutProfile(ctx, profile(sc, 1)))
	}
	must(t, s.DeleteProfile(ctx, agent))
	must(t, s.DeleteProfile(ctx, agent))
	must(t, s.DeleteProfile(ctx, limen.Scope{Org: "p"}))
	for _, c := range []struct {
		s  limen.Scope
		ok bool
	}{{org, true}, {agent, false}, {mine, true}} {
		_, ok, err := s.Profile(ctx, c.s)
		must(t, err)
		expect(t, ok == c.ok, "after removing the agent's profile, Profile(%+v) is there: %v", c.s, ok)
	}
	must(t, s.DeleteProfile(ctx, mine))
	must(t, s.DeleteProfile(ctx, org))
	for _, sc := range []limen.Scope{org, mine} {
		_, ok, err := s.Profile(ctx, sc)
		must(t, err)
		expect(t, !ok, "a removed profile %+v is there", sc)
	}
	must(t, s.PutProfile(ctx, profile(org, 2)))
	p, ok, err := s.Profile(ctx, org)
	must(t, err)
	expect(t, ok && p.Costs.Ask == 2, "a profile saved after a removal: %+v, %v", p, ok)
}

func subjects(t *testing.T, s store.Store) {
	got, err := s.Subjects(ctx, "o")
	must(t, err)
	expect(t, len(got) == 0 && got != nil, "an empty store lists %v", got)
	for _, c := range []store.Case{Case("o", person, "a", 0), Case("o", person, "b", 1), Case("o", "s", "c", 0), Case("p", "x", "d", 0)} {
		must(t, s.Put(ctx, c))
	}
	got, err = s.Subjects(ctx, "o")
	must(t, err)
	expect(t, slices.Equal(got, []string{person, "s"}), "Subjects(o) = %v", got)
	must(t, s.Forget(ctx, "o", person))
	got, err = s.Subjects(ctx, "o")
	must(t, err)
	expect(t, slices.Equal(got, []string{"s"}), "after a Forget, Subjects(o) = %v", got)
	got, err = s.Subjects(ctx, "p")
	must(t, err)
	expect(t, slices.Equal(got, []string{"x"}), "Subjects(p) = %v", got)
}

// Monday is the start of a week of a report.
var Monday = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

// Week returns a kept week that starts at from, with every field set.
func Week(from time.Time) limen.WeekReport {
	denied, rate := 2, 0.25
	cell := limen.ReportCell{Subjects: 6, Decisions: limen.VerdictCounts{Allow: 10, Flag: 3, Ask: 2, Block: 1},
		FlagReviews: limen.FlagReviews{Reviews: 3, Denied: &denied},
		Applied:     limen.SourceRates{FalseAllow: &limen.Estimate{Value: &rate, Interval: &[2]float64{0.1, 0.5}, Covered: 1, Reviews: 8, Audits: 5, Unanswered: 1}},
		Learned:     limen.SourceRates{FalseBlock: &limen.Estimate{Covered: 0.5}},
		Withheld:    []string{"applied.false_block", "learned.false_allow"}}
	return limen.WeekReport{From: from, To: from.Add(limen.ReportWeek), State: limen.WeekReady, Total: &cell,
		Agents: []limen.AgentCell{{Agent: "agt", ReportCell: cell}}, Other: &cell}
}

func sameWeek(a, b limen.WeekReport) bool {
	x, err1 := json.Marshal(a, json.Deterministic(true))
	y, err2 := json.Marshal(b, json.Deterministic(true))
	return err1 == nil && err2 == nil && string(x) == string(y)
}

func weekReports(t *testing.T, s store.Store) {
	_, ok, err := s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, !ok, "an empty store keeps a week")
	w := Week(Monday)
	must(t, s.PutWeekReport(ctx, "o", w))
	must(t, s.PutWeekReport(ctx, person, Week(Monday.Add(limen.ReportWeek))))
	got, ok, err := s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, ok && sameWeek(got, w), "round trip, found %v: %+v", ok, got)
	for _, c := range []struct {
		org  string
		from time.Time
		ok   bool
	}{{"o", Monday.Add(limen.ReportWeek), false}, {"p", Monday, false}, {person, Monday.Add(limen.ReportWeek), true}} {
		_, ok, err := s.WeekReport(ctx, c.org, c.from)
		must(t, err)
		expect(t, ok == c.ok, "WeekReport(%s, %v) found %v", c.org, c.from, ok)
	}
	// A kept week holds no person's record, so Forget leaves it.
	must(t, s.Put(ctx, Case("o", "s", "a", 0)))
	must(t, s.Forget(ctx, "o", "s"))
	_, ok, err = s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, ok, "Forget removed a kept week")
}

func snapshot(t *testing.T, s store.Store, org, subject, version string) ([]byte, bool) {
	t.Helper()
	b, ok, err := s.Snapshot(ctx, org, subject, version)
	must(t, err)
	return b, ok
}

func snapshots(t *testing.T, s store.Store) {
	_, ok := snapshot(t, s, "o", "s", "1")
	expect(t, !ok, "an empty store has a snapshot")
	must(t, s.PutSnapshot(ctx, "o", "s", "1", []byte("one")))
	must(t, s.PutSnapshot(ctx, "o", "s", "2", []byte("two")))
	must(t, s.PutSnapshot(ctx, "o", "other", "1", []byte("other")))
	for _, c := range []struct{ subject, version, want string }{{"s", "1", "one"}, {"s", "2", "two"}, {"other", "1", "other"}} {
		b, ok := snapshot(t, s, "o", c.subject, c.version)
		expect(t, ok && string(b) == c.want, "Snapshot(%s, %s) = %q, %v; want %q", c.subject, c.version, b, ok, c.want)
	}
	must(t, s.PutSnapshot(ctx, "o", "s", "1", []byte("one again")))
	b, _ := snapshot(t, s, "o", "s", "1")
	expect(t, string(b) == "one again", "PutSnapshot did not replace: %q", b)
	_, ok = snapshot(t, s, "p", "s", "1")
	expect(t, !ok, "a snapshot leaked to another organization")
}

func forget(t *testing.T, s store.Store) {
	for _, c := range []store.Case{Case("o", "s", "a", 0), Case("o", "s", "b", 1), Case("o", "other", "c", 0), Case("p", "s", "d", 0)} {
		must(t, s.Put(ctx, c))
	}
	next(t, s, "o", "s")
	next(t, s, "o", "s")
	must(t, s.PutSnapshot(ctx, "o", "s", "1", []byte("x")))
	must(t, s.PutSnapshot(ctx, "o", "other", "1", []byte("y")))
	must(t, s.PutProfile(ctx, profile(limen.Scope{Org: "o"}, 1)))
	must(t, s.PutProfile(ctx, profile(limen.Scope{Org: "o", Subject: "s"}, 1)))

	must(t, s.Forget(ctx, "o", "s"))
	cs := list(t, s, "o", "s")
	expect(t, len(cs) == 0, "a forgotten subject lists %v", ids(cs))
	_, ok := get(t, s, Case("o", "s", "a", 0))
	expect(t, !ok, "a forgotten case is still there")
	_, ok = snapshot(t, s, "o", "s", "1")
	expect(t, !ok, "a forgotten subject's snapshot is still there")
	got := next(t, s, "o", "s")
	expect(t, got == 0, "a forgotten subject's counter starts at %d", got)
	cs = list(t, s, "o", "other")
	expect(t, len(cs) == 1, "Forget reached another subject: %v", ids(cs))
	cs = list(t, s, "p", "s")
	expect(t, len(cs) == 1, "Forget reached another organization: %v", ids(cs))
	_, ok = snapshot(t, s, "o", "other", "1")
	expect(t, ok, "Forget removed another subject's snapshot")
	_, ok, err := s.Profile(ctx, limen.Scope{Org: "o"})
	must(t, err)
	expect(t, ok, "Forget removed the organization's profile")
	_, ok, err = s.Profile(ctx, limen.Scope{Org: "o", Subject: "s"})
	must(t, err)
	expect(t, ok, "the store's Forget removed the person's profile, which the engine removes")
	must(t, s.Forget(ctx, "o", "s"))
	must(t, s.Forget(ctx, "o", "nobody"))
}

func canceled(t *testing.T, s store.Store) {
	c, cancel := context.WithCancel(ctx)
	cancel()
	cs := Case("o", "s", "a", 0)
	errs := map[string]error{
		"Case":          func() error { _, _, err := s.Case(c, cs.Action.Key); return err }(),
		"Put":           s.Put(c, cs),
		"NextT":         func() error { _, err := s.NextT(c, "o", "s"); return err }(),
		"Cases":         func() error { _, err := s.Cases(c, "o", "s"); return err }(),
		"Profile":       func() error { _, _, err := s.Profile(c, limen.Scope{Org: "o"}); return err }(),
		"PutProfile":    s.PutProfile(c, profile(limen.Scope{Org: "o"}, 1)),
		"Snapshot":      func() error { _, _, err := s.Snapshot(c, "o", "s", "1"); return err }(),
		"PutSnapshot":   s.PutSnapshot(c, "o", "s", "1", []byte("x")),
		"Forget":        s.Forget(c, "o", "s"),
		"DeleteProfile": s.DeleteProfile(c, limen.Scope{Org: "o"}),
		"Subjects":      func() error { _, err := s.Subjects(c, "o"); return err }(),
		"WeekReport":    func() error { _, _, err := s.WeekReport(c, "o", Monday); return err }(),
		"PutWeekReport": s.PutWeekReport(c, "o", Week(Monday)),
	}
	for name, err := range errs {
		expect(t, errors.Is(err, context.Canceled), "%s under a canceled context: %v", name, err)
	}
	_, ok := get(t, s, cs)
	expect(t, !ok, "a Put under a canceled context stored the case")
	_, ok, err := s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, !ok, "a PutWeekReport under a canceled context kept the week")
}

func copies(t *testing.T, s store.Store) {
	c := Case("o", "s", "a", 0)
	must(t, s.Put(ctx, c))
	c.A[0] = 99
	c.Answers[0].Approve = false
	c.Action.Attributes["files"] = 99.0
	got, _ := get(t, s, c)
	expect(t, got.A[0] != 99 && got.Answers[0].Approve && got.Action.Attributes["files"] != 99.0,
		"a write to the caller's case after Put reached the store")
	got.A[0] = 42
	got.Answers[0].By = "mallory"
	again, _ := get(t, s, c)
	expect(t, again.A[0] != 42 && again.Answers[0].By != "mallory", "a write to a returned case reached the store")
	cs := list(t, s, "o", "s")
	cs[0].C[0] = 42
	cs = list(t, s, "o", "s")
	expect(t, cs[0].C[0] != 42, "a write to a listed case reached the store")

	snap := []byte("state")
	must(t, s.PutSnapshot(ctx, "o", "s", "1", snap))
	snap[0] = 'X'
	b, _ := snapshot(t, s, "o", "s", "1")
	expect(t, string(b) == "state", "a write to the caller's snapshot reached the store: %q", b)
	b[0] = 'Y'
	b, _ = snapshot(t, s, "o", "s", "1")
	expect(t, string(b) == "state", "a write to a returned snapshot reached the store: %q", b)

	w := Week(Monday)
	must(t, s.PutWeekReport(ctx, "o", w))
	*w.Total.FlagReviews.Denied = 99
	w.Agents[0].Agent = "mallory"
	kept, _, err := s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, *kept.Total.FlagReviews.Denied == 2 && kept.Agents[0].Agent == "agt", "a write to the caller's week reached the store")
	kept.Agents[0].Agent = "mallory"
	kept.Total.Withheld[0] = "x"
	kept, _, err = s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, kept.Agents[0].Agent == "agt" && kept.Total.Withheld[0] == "applied.false_block", "a write to a returned week reached the store")
}

// RunReopen runs the persistence contract against a store that open
// returns over dir: what one store keeps is what a store opened later over
// the same directory finds. Each subtest gets its own directory, and open
// is called twice for it, with a Close in between.
func RunReopen(t *testing.T, open func(t *testing.T, dir string) store.Store) {
	t.Helper()
	t.Run("KeepsEverything", func(t *testing.T) { keepsEverything(t, open) })
	t.Run("KeepsForget", func(t *testing.T) { keepsForget(t, open) })
}

func keepsEverything(t *testing.T, open func(t *testing.T, dir string) store.Store) {
	const subject = "https://issuer.example|usr_42"
	dir := t.TempDir()
	s := open(t, dir)
	cases := []store.Case{Case("o", subject, "x", 2), Case("o", subject, "y", 7), Case("p", "s", "z", 0)}
	for _, c := range cases {
		must(t, s.Put(ctx, c))
	}
	cases[0].Digest = "replaced"
	must(t, s.Put(ctx, cases[0]))
	must(t, s.PutProfile(ctx, profile(limen.Scope{Org: "o", Kind: "tool"}, 1.5)))
	must(t, s.PutProfile(ctx, profile(limen.Scope{Org: "o", Subject: subject}, 2)))
	must(t, s.PutProfile(ctx, profile(limen.Scope{Org: "o", Agent: "agt"}, 1)))
	must(t, s.DeleteProfile(ctx, limen.Scope{Org: "o", Agent: "agt"}))
	must(t, s.PutSnapshot(ctx, "o", subject, "1", []byte("state")))
	must(t, s.PutWeekReport(ctx, "o", Week(Monday)))
	must(t, s.Close())

	s = open(t, dir)
	t.Cleanup(func() { _ = s.Close() })
	for _, c := range cases {
		got, ok := get(t, s, c)
		expect(t, ok && same(got, c), "after a reopen %s is %s, %v", c.Action.ActionID, encode(got), ok)
	}
	cs := list(t, s, "o", subject)
	expect(t, slices.Equal(ids(cs), []string{"x", "y"}), "after a reopen the subject lists %v", ids(cs))
	p, ok, err := s.Profile(ctx, limen.Scope{Org: "o", Kind: "tool"})
	must(t, err)
	expect(t, ok && p.Costs.Ask == 1.5, "after a reopen the profile is %+v, %v", p, ok)
	p, ok, err = s.Profile(ctx, limen.Scope{Org: "o", Subject: subject})
	must(t, err)
	expect(t, ok && p.Costs.Ask == 2, "after a reopen the person's profile is %+v, %v", p, ok)
	_, ok, err = s.Profile(ctx, limen.Scope{Org: "o", Agent: "agt"})
	must(t, err)
	expect(t, !ok, "after a reopen a removed profile is back")
	b, ok := snapshot(t, s, "o", subject, "1")
	expect(t, ok && string(b) == "state", "after a reopen the snapshot is %q, %v", b, ok)
	w, ok, err := s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, ok && sameWeek(w, Week(Monday)), "after a reopen the kept week is %+v, %v", w, ok)
	subs, err := s.Subjects(ctx, "o")
	must(t, err)
	expect(t, slices.Equal(subs, []string{subject}), "after a reopen Subjects(o) = %v", subs)
	got := next(t, s, "o", subject)
	expect(t, got > 7, "after a reopen NextT = %d, an ordinal a stored case holds", got)
}

func keepsForget(t *testing.T, open func(t *testing.T, dir string) store.Store) {
	dir := t.TempDir()
	s := open(t, dir)
	must(t, s.Put(ctx, Case("o", "s", "a", 0)))
	must(t, s.PutSnapshot(ctx, "o", "s", "1", []byte("x")))
	must(t, s.PutWeekReport(ctx, "o", Week(Monday)))
	must(t, s.Forget(ctx, "o", "s"))
	must(t, s.Close())
	s = open(t, dir)
	t.Cleanup(func() { _ = s.Close() })
	cs := list(t, s, "o", "s")
	expect(t, len(cs) == 0, "a forgotten subject came back after a reopen: %v", ids(cs))
	_, ok := snapshot(t, s, "o", "s", "1")
	expect(t, !ok, "a forgotten snapshot came back after a reopen")
	_, ok, err := s.WeekReport(ctx, "o", Monday)
	must(t, err)
	expect(t, ok, "a kept week was lost with a forgotten subject")
	subs, err := s.Subjects(ctx, "o")
	must(t, err)
	expect(t, len(subs) == 0, "a forgotten subject is listed after a reopen: %v", subs)
}
