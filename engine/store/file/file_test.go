// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package file_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/conformance"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/engine/store/file"
	"latere.ai/x/limen/engine/store/storetest"
)

var ctx = context.Background()

func open(t *testing.T, dir string) *file.Store {
	t.Helper()
	s, err := file.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return open(t, t.TempDir()) })
}

func TestContractNoSync(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := file.OpenWith(t.TempDir(), file.Options{NoSync: true})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestReopen(t *testing.T) {
	storetest.RunReopen(t, func(t *testing.T, dir string) store.Store { return open(t, dir) })
}

// The engine over a file store passes the service contract.
func TestEngineConformance(t *testing.T) {
	over := func(t *testing.T, now func() time.Time) limen.Service {
		s, err := file.OpenWith(t.TempDir(), file.Options{NoSync: true})
		if err != nil {
			t.Fatal(err)
		}
		e := engine.New(engine.Options{Store: s, Inline: 1, Clock: now})
		t.Cleanup(func() {
			_ = e.Close()
			_ = s.Close()
		})
		return e
	}
	conformance.Run(t, conformance.Target{
		New:     func(t *testing.T) limen.Service { return over(t, nil) },
		Clocked: over,
	})
}

// No file of a ledger holds an action's input after the prediction returns
// (spec 004, criterion 9). Inputs with a marker reach the engine through a
// prediction, a decision that carries its action, a kept replay and an
// explanation, and the models are fitted and the store closed; then no byte
// of any file under the directory holds the marker.
func TestNoFileHoldsTheInput(t *testing.T) {
	const marker = "MARKER-7f3a9c"
	dir := t.TempDir()
	s := open(t, dir)
	e := engine.New(engine.Options{Store: s, Inline: 1})
	input := func(i int) []byte {
		return fmt.Appendf(nil, `{"command":"curl -H 'Authorization: %s-%d' https://example.com && rm -rf /tmp/%s"}`, marker, i, marker)
	}
	action := func(id string, i int) limen.Action {
		return limen.Action{Org: "o", Subject: "s", ActionID: id, Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: input(i),
			Attributes: map[string]any{"branch": "main"}}
	}
	for i := range 12 {
		a := action(fmt.Sprintf("p-%d", i), i)
		if _, err := e.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1"}); err != nil {
			t.Fatal(err)
		}
		if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: i%3 != 0}); err != nil {
			t.Fatal(err)
		}
	}
	unpredicted := action("d-1", 100)
	if err := e.Record(ctx, limen.Decision{Key: unpredicted.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "rules/1", Action: &unpredicted}); err != nil {
		t.Fatal(err)
	}
	approve := true
	replayed := limen.ReplayRecord{At: time.Now(), Action: action("r-1", 200), Verdict: limen.Ask, ReviewProbability: 1, Source: "topos/confirm", Approve: &approve}
	if _, err := e.Replay(ctx, func(yield func(limen.ReplayRecord, error) bool) { yield(replayed, nil) }, limen.ReplayOptions{Keep: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Explain(ctx, unpredicted.Key); err != nil {
		t.Fatal(err)
	}
	if err := e.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	files := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		if bytes.Contains(b, []byte(marker)) {
			t.Errorf("%s holds the input", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 3 {
		t.Errorf("the ledger has %d files, want its log, its snapshot and its profiles at least", files)
	}
}

// logPath finds the subject's case log by its meta.json, without knowing
// the encoding.
func logPath(t *testing.T, dir, org, subject string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.Name() != "meta.json" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(`"org":"`+org+`"`)) && bytes.Contains(b, []byte(`"subject":"`+subject+`"`)) {
			found = filepath.Join(filepath.Dir(path), "cases.ndjson")
		}
		return nil
	})
	if err != nil || found == "" {
		t.Fatalf("no log for %s in %s: %v", subject, org, err)
	}
	return found
}

func lines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Count(b, []byte("\n"))
}

// A crash in the middle of an append leaves a torn last line; reopening
// drops it, truncates the log, and keeps everything before it.
func TestTornLastLine(t *testing.T) {
	for _, tear := range []struct {
		name string
		tail string
	}{
		{"cut mid-record", `{"t":9,"action":{"org":"o","subj`},
		{"garbage line", "not json\n"},
	} {
		t.Run(tear.name, func(t *testing.T) {
			dir := t.TempDir()
			s := open(t, dir)
			for i := range 3 {
				if err := s.Put(ctx, storetest.Case("o", "s", fmt.Sprintf("c%d", i), int64(i))); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			path := logPath(t, dir, "o", "s")
			before, _ := os.ReadFile(path)
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(tear.tail); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()

			s = open(t, dir)
			defer func() { _ = s.Close() }()
			cs, err := s.Cases(ctx, "o", "s")
			if err != nil || len(cs) != 3 {
				t.Fatalf("after a torn append: %d cases, %v", len(cs), err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, before) {
				t.Errorf("the torn end was not truncated:\n%q", after[len(before)-20:])
			}
			// The log takes appends again from a clean end.
			if err := s.Put(ctx, storetest.Case("o", "s", "c3", 3)); err != nil {
				t.Fatal(err)
			}
			if lines(t, path) != 4 {
				t.Errorf("log has %d lines after an append", lines(t, path))
			}
		})
	}
}

// A line that does not parse in the middle of the log is damage, not a
// torn append, and the subject refuses to load.
func TestDamagedLog(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	for i := range 2 {
		if err := s.Put(ctx, storetest.Case("o", "s", fmt.Sprintf("c%d", i), int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	path := logPath(t, dir, "o", "s")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append([]byte("{broken\n"), b...), 0o600); err != nil {
		t.Fatal(err)
	}
	s = open(t, dir)
	defer func() { _ = s.Close() }()
	if _, err := s.Cases(ctx, "o", "s"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("a damaged log loaded: %v", err)
	}
	if _, err := s.NextT(ctx, "o", "s"); err == nil {
		t.Error("NextT on a damaged log succeeded")
	}
	if err := s.PutSnapshot(ctx, "o", "s", "1", []byte("x")); err == nil {
		t.Error("PutSnapshot on a damaged subject succeeded")
	}
}

// A log of many replacements is compacted to one line per case, and the
// compacted log reads back the same.
func TestCompaction(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	c := storetest.Case("o", "s", "a", 0)
	other := storetest.Case("o", "s", "b", 1)
	if err := s.Put(ctx, other); err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		c.Digest = fmt.Sprintf("v%d", i)
		if err := s.Put(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	path := logPath(t, dir, "o", "s")
	if n := lines(t, path); n > 2*2+64 {
		t.Errorf("the log holds %d lines for 2 cases", n)
	}
	_ = s.Close()
	s = open(t, dir)
	defer func() { _ = s.Close() }()
	cs, err := s.Cases(ctx, "o", "s")
	if err != nil || len(cs) != 2 || cs[0].Action.ActionID != "a" || cs[0].Digest != "v199" {
		t.Fatalf("after compaction: %+v, %v", cs, err)
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if _, err := file.Open(dir); !errors.Is(err, file.ErrLocked) {
		t.Errorf("a second open of a locked directory: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
	again := open(t, dir)
	defer func() { _ = again.Close() }()

	// A closed store refuses every call.
	k := limen.Key{Org: "o", Subject: "s", ActionID: "a"}
	errs := []error{
		func() error { _, _, err := s.Case(ctx, k); return err }(),
		s.Put(ctx, storetest.Case("o", "s", "a", 0)),
		func() error { _, err := s.NextT(ctx, "o", "s"); return err }(),
		func() error { _, err := s.Cases(ctx, "o", "s"); return err }(),
		func() error { _, _, err := s.Profile(ctx, limen.Scope{Org: "o"}); return err }(),
		s.PutProfile(ctx, limen.Profile{Org: "o"}),
		func() error { _, _, err := s.Snapshot(ctx, "o", "s", "1"); return err }(),
		s.PutSnapshot(ctx, "o", "s", "1", nil),
		s.Forget(ctx, "o", "s"),
		s.DeleteProfile(ctx, limen.Scope{Org: "o"}),
		func() error { _, err := s.Subjects(ctx, "o"); return err }(),
		func() error { _, err := s.ProfileSubjects(ctx, "o"); return err }(),
		func() error { _, err := s.Orgs(ctx); return err }(),
		func() error { _, _, err := s.WeekReport(ctx, "o", storetest.Monday); return err }(),
		s.PutWeekReport(ctx, "o", storetest.Week(storetest.Monday)),
	}
	for i, err := range errs {
		if !errors.Is(err, file.ErrClosed) {
			t.Errorf("call %d on a closed store: %v", i, err)
		}
	}
}

// Subjects, organizations and versions with every awkward byte, the empty
// string, and a name too long to escape all get their own place on disk.
func TestAwkwardNames(t *testing.T) {
	s := open(t, t.TempDir())
	defer func() { _ = s.Close() }()
	long := strings.Repeat("Ü/|:", 80)
	names := []string{"", "_e", "a", "A", "%61", "..", ".", "https://issuer.example|usr_42", "o/../../etc", long, long + "x", "_h"}
	for i, n := range names {
		c := storetest.Case(n, n, fmt.Sprintf("id-%d", i), 0)
		if err := s.Put(ctx, c); err != nil {
			t.Fatalf("%q: %v", n, err)
		}
		if err := s.PutSnapshot(ctx, n, n, n, []byte(n)); err != nil {
			t.Fatalf("snapshot %q: %v", n, err)
		}
		if err := s.PutProfile(ctx, limen.Profile{Org: n, AuditRate: float64(i) / 100}); err != nil {
			t.Fatalf("profile %q: %v", n, err)
		}
	}
	for i, n := range names {
		cs, err := s.Cases(ctx, n, n)
		if err != nil || len(cs) != 1 || cs[0].Action.ActionID != fmt.Sprintf("id-%d", i) {
			t.Errorf("%q lists %+v, %v", n, cs, err)
		}
		if b, ok, _ := s.Snapshot(ctx, n, n, n); !ok || string(b) != n {
			t.Errorf("snapshot of %q is %q", n, b)
		}
		if p, ok, _ := s.Profile(ctx, limen.Scope{Org: n}); !ok || p.AuditRate != float64(i)/100 {
			t.Errorf("profile of %q is %+v", n, p)
		}
		if subs, err := s.Subjects(ctx, n); err != nil || !slices.Equal(subs, []string{n}) {
			t.Errorf("subjects of %q are %q, %v", n, subs, err)
		}
	}
	orgs, err := s.Orgs(ctx)
	if want := slices.Sorted(slices.Values(names)); err != nil || !slices.Equal(orgs, want) {
		t.Errorf("organizations %q, %v, want %q", orgs, err, want)
	}
	for _, n := range names {
		if err := s.PutWeekReport(ctx, n, storetest.Week(storetest.Monday)); err != nil {
			t.Fatalf("week of %q: %v", n, err)
		}
	}
	for _, n := range names {
		if _, ok, err := s.WeekReport(ctx, n, storetest.Monday); !ok || err != nil {
			t.Errorf("week of %q: %v, %v", n, ok, err)
		}
	}
}

// A meta.json or a kept week that does not parse fails loudly, and a
// subject's directory a crash left before its meta.json is no subject.
func TestDamagedReports(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	defer func() { _ = s.Close() }()
	if orgs, err := s.Orgs(ctx); err != nil || orgs == nil || len(orgs) != 0 {
		t.Errorf("the organizations of a new store: %v, %v", orgs, err)
	}
	for _, c := range []store.Case{storetest.Case("o", "s", "a", 0), storetest.Case("p", "t", "b", 0)} {
		if err := s.Put(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "subjects", "o", "crashed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subjects", "o", "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if subs, err := s.Subjects(ctx, "o"); err != nil || !slices.Equal(subs, []string{"s"}) {
		t.Errorf("subjects %v, %v", subs, err)
	}
	if orgs, err := s.Orgs(ctx); err != nil || !slices.Equal(orgs, []string{"o", "p"}) {
		t.Errorf("organizations %v, %v", orgs, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subjects", "p", "t", "meta.json"), []byte(`{"org":"o","subject":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subjects(ctx, "p"); err == nil {
		t.Error("a meta.json of another organization was listed")
	}
	if _, err := s.Orgs(ctx); err == nil {
		t.Error("a meta.json of another organization named its directory")
	}
	if err := os.Remove(filepath.Join(dir, "subjects", "p", "t", "meta.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subjects", "p", "t", "meta.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subjects(ctx, "p"); err == nil {
		t.Error("a meta.json that is a directory was read")
	}
	if _, err := s.Orgs(ctx); err == nil {
		t.Error("a meta.json that is a directory named an organization")
	}
	if err := os.WriteFile(filepath.Join(dir, "subjects", "q"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subjects(ctx, "q"); err == nil {
		t.Error("an organization's directory that is a file was listed")
	}

	if err := s.PutWeekReport(ctx, "o", storetest.Week(storetest.Monday)); err != nil {
		t.Fatal(err)
	}
	weeks, _ := filepath.Glob(filepath.Join(dir, "reports", "o", "*.json"))
	if len(weeks) != 1 {
		t.Fatalf("weeks: %v", weeks)
	}
	if err := os.WriteFile(weeks[0], []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WeekReport(ctx, "o", storetest.Monday); err == nil {
		t.Error("a damaged week loaded")
	}
	if err := os.Remove(weeks[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(weeks[0], 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WeekReport(ctx, "o", storetest.Monday); err == nil {
		t.Error("a week that is a directory was read")
	}
	if err := s.PutWeekReport(ctx, "o", storetest.Week(storetest.Monday)); err == nil {
		t.Error("a week was written over a directory")
	}
	if err := os.WriteFile(filepath.Join(dir, "reports", "r"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.PutWeekReport(ctx, "r", storetest.Week(storetest.Monday)); err == nil {
		t.Error("a week was written under a file")
	}
	bad := storetest.Week(storetest.Monday)
	nan := math.NaN()
	bad.Total.Applied.FalseAllow.Value = &nan
	if err := s.PutWeekReport(ctx, "o", bad); err == nil {
		t.Error("a week that cannot be encoded was kept")
	}
}

// A profiles file that does not parse fails loudly.
func TestDamagedProfiles(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.PutProfile(ctx, limen.Profile{Org: "o"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	matches, _ := filepath.Glob(filepath.Join(dir, "profiles", "*.json"))
	if len(matches) != 1 {
		t.Fatalf("profiles files: %v", matches)
	}
	if err := os.WriteFile(matches[0], []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = open(t, dir)
	defer func() { _ = s.Close() }()
	if _, _, err := s.Profile(ctx, limen.Scope{Org: "o"}); err == nil {
		t.Error("a damaged profiles file loaded")
	}
	if err := s.PutProfile(ctx, limen.Profile{Org: "o"}); err == nil {
		t.Error("PutProfile over a damaged profiles file succeeded")
	}
	if err := s.DeleteProfile(ctx, limen.Scope{Org: "o"}); err == nil {
		t.Error("DeleteProfile over a damaged profiles file succeeded")
	}
}

// Unreadable or unwritable places surface as errors.
func TestFilesystemErrors(t *testing.T) {
	dir := t.TempDir()
	// The data directory cannot be created under a file.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Open(filepath.Join(blocker, "data")); err == nil {
		t.Error("opened a store under a file")
	}

	s := open(t, filepath.Join(dir, "data"))
	defer func() { _ = s.Close() }()
	if err := s.Put(ctx, storetest.Case("o", "s", "a", 0)); err != nil {
		t.Fatal(err)
	}
	path := logPath(t, filepath.Join(dir, "data"), "o", "s")
	subjectDir := filepath.Dir(path)

	// The log becomes a directory: reads and appends fail.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, storetest.Case("o", "s", "b", 1)); err == nil {
		t.Error("appended to a log that is a directory")
	}
	if err := s.Forget(ctx, "o", "s"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cases(ctx, "o", "s"); err == nil {
		t.Error("read a log that is a directory")
	}
	if err := os.RemoveAll(subjectDir); err != nil {
		t.Fatal(err)
	}

	// The snapshot path becomes a directory: reads fail.
	if err := s.Forget(ctx, "o", "s"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSnapshot(ctx, "o", "s", "1", []byte("x")); err != nil {
		t.Fatal(err)
	}
	snaps, _ := filepath.Glob(filepath.Join(subjectDir, "snapshots", "*"))
	if len(snaps) != 1 {
		t.Fatalf("snapshots: %v", snaps)
	}
	if err := os.Remove(snaps[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(snaps[0], 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Snapshot(ctx, "o", "s", "1"); err == nil {
		t.Error("read a snapshot that is a directory")
	}
	if err := s.PutSnapshot(ctx, "o", "s", "1", []byte("y")); err == nil {
		t.Error("wrote a snapshot over a directory")
	}

	// A case that cannot be encoded is refused.
	bad := storetest.Case("o", "t", "a", 0)
	bad.Action.Attributes = map[string]any{"x": func() {}}
	if err := s.Put(ctx, bad); err == nil {
		t.Error("stored a case that cannot be encoded")
	}
}

// A week a report showed is kept in the directory: an engine that opens
// the directory again returns it unchanged, also after a Forget of a
// person who took part (spec 010, criterion 12).
func TestKeptWeekSurvivesAReopen(t *testing.T) {
	dir := t.TempDir()
	monday := storetest.Monday
	now := monday.Add(time.Hour)
	clock := func() time.Time { return now }
	s := open(t, dir)
	e := engine.New(engine.Options{Store: s, Inline: 1, Clock: clock})
	for i := range 6 {
		a := limen.Action{Org: "o", Subject: fmt.Sprintf("https://issuer.example|usr_%d", i), ActionID: "a", Kind: "tool", Name: "bash",
			Effect: limen.EffectWrite, Actor: &limen.Actor{Kind: "agent", ID: "agt"}}
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Flag, ReviewProbability: 1, Source: "m", Action: &a}); err != nil {
			t.Fatal(err)
		}
		if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: i%2 == 0}); err != nil {
			t.Fatal(err)
		}
	}
	now = monday.Add(limen.ReportWeek + limen.ReportClosesAfter)
	q := limen.ReportQuery{Org: "o", From: monday, To: monday.Add(limen.ReportWeek)}
	first, err := e.Report(ctx, q)
	if err != nil || first.Weeks[0].State != limen.WeekReady {
		t.Fatalf("%+v, %v", first, err)
	}
	_ = e.Close()
	_ = s.Close()

	s = open(t, dir)
	defer func() { _ = s.Close() }()
	e = engine.New(engine.Options{Store: s, Inline: 1, Clock: clock})
	defer func() { _ = e.Close() }()
	if err := e.Forget(ctx, "o", "https://issuer.example|usr_0"); err != nil {
		t.Fatal(err)
	}
	again, err := e.Report(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !sameReport(again, first) {
		t.Errorf("the kept week changed across a reopen and a Forget:\n first %+v\n again %+v", *first.Weeks[0].Total, *again.Weeks[0].Total)
	}
	if subs, _ := s.Subjects(ctx, "o"); len(subs) != 5 {
		t.Errorf("the Forget left %d persons, want 5", len(subs))
	}
}

// sameReport compares two reports by their JSON, the form the store keeps.
func sameReport(a, b limen.Report) bool {
	x, err1 := json.Marshal(a, json.Deterministic(true))
	y, err2 := json.Marshal(b, json.Deterministic(true))
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// A subject's meta.json names its organization and subject in lower case,
// the form a reader of the directory sees.
func TestMetaBytes(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	k := limen.Key{Org: "o", Subject: "s", ActionID: "a"}
	if err := s.Put(context.Background(), store.Case{Action: limen.Action{Key: k, Kind: "tool", Name: "bash", Effect: limen.EffectWrite}}); err != nil {
		t.Fatal(err)
	}
	var found int
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "meta.json" {
			b, rerr := os.ReadFile(p)
			if rerr != nil || string(b) != `{"org":"o","subject":"s"}` {
				t.Errorf("meta.json %q %v", b, rerr)
			}
			found++
		}
		return err
	})
	if err != nil || found != 1 {
		t.Fatalf("%d meta.json files, %v", found, err)
	}
	subs, err := s.Subjects(context.Background(), "o")
	if err != nil || len(subs) != 1 || subs[0] != "s" {
		t.Errorf("subjects %v %v", subs, err)
	}
}
