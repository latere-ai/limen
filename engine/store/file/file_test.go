// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package file_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/cribrum"
	"latere.ai/x/cribrum/conformance"
	"latere.ai/x/cribrum/engine"
	"latere.ai/x/cribrum/engine/store"
	"latere.ai/x/cribrum/engine/store/file"
	"latere.ai/x/cribrum/engine/store/storetest"
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
	conformance.Run(t, conformance.Target{
		New: func(t *testing.T) cribrum.Service {
			s, err := file.OpenWith(t.TempDir(), file.Options{NoSync: true})
			if err != nil {
				t.Fatal(err)
			}
			e := engine.New(engine.Options{Store: s, Inline: 1})
			t.Cleanup(func() {
				_ = e.Close()
				_ = s.Close()
			})
			return e
		},
	})
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
	k := cribrum.Key{Org: "o", Subject: "s", ActionID: "a"}
	errs := []error{
		func() error { _, _, err := s.Case(ctx, k); return err }(),
		s.Put(ctx, storetest.Case("o", "s", "a", 0)),
		func() error { _, err := s.NextT(ctx, "o", "s"); return err }(),
		func() error { _, err := s.Cases(ctx, "o", "s"); return err }(),
		func() error { _, _, err := s.Profile(ctx, cribrum.Scope{Org: "o"}); return err }(),
		s.PutProfile(ctx, cribrum.Profile{Org: "o"}),
		func() error { _, _, err := s.Snapshot(ctx, "o", "s", "1"); return err }(),
		s.PutSnapshot(ctx, "o", "s", "1", nil),
		s.Forget(ctx, "o", "s"),
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
		if err := s.PutProfile(ctx, cribrum.Profile{Org: n, AuditRate: float64(i) / 100}); err != nil {
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
		if p, ok, _ := s.Profile(ctx, cribrum.Scope{Org: n}); !ok || p.AuditRate != float64(i)/100 {
			t.Errorf("profile of %q is %+v", n, p)
		}
	}
}

// A profiles file that does not parse fails loudly.
func TestDamagedProfiles(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.PutProfile(ctx, cribrum.Profile{Org: "o"}); err != nil {
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
	if _, _, err := s.Profile(ctx, cribrum.Scope{Org: "o"}); err == nil {
		t.Error("a damaged profiles file loaded")
	}
	if err := s.PutProfile(ctx, cribrum.Profile{Org: "o"}); err == nil {
		t.Error("PutProfile over a damaged profiles file succeeded")
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
