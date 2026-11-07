// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package file

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func cached(s *Store) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subjects)
}

// Reads of people the store holds nothing about answer as an empty person
// and keep no entry, so they do not grow the store's memory.
func TestReadsOfUnknownPeopleKeepNoEntry(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for i := range 300 {
		org, name := fmt.Sprintf("org_%d", i), fmt.Sprintf("usr_%d", i)
		if _, ok, err := s.Case(ctx, limen.Key{Org: org, Subject: name, ActionID: "a"}); err != nil || ok {
			t.Fatalf("Case: %v, %v", ok, err)
		}
		if cs, err := s.Cases(ctx, org, name); err != nil || len(cs) != 0 {
			t.Fatalf("Cases: %v, %v", cs, err)
		}
		if b, ok, err := s.Snapshot(ctx, org, name, "v"); err != nil || ok || b != nil {
			t.Fatalf("Snapshot: %v, %v, %v", b, ok, err)
		}
		if err := s.Forget(ctx, org, name); err != nil {
			t.Fatalf("Forget: %v", err)
		}
	}
	if n := cached(s); n != 0 {
		t.Errorf("%d entries after the reads", n)
	}
}

// A person a write created is read back, by the entry the write made and,
// after a reopen, by the entry a read of an existing directory makes.
func TestReadAfterWriteSeesTheWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	k := limen.Key{Org: "o", Subject: "s", ActionID: "a1"}
	// A read first, then the write, then the read again.
	if cs, err := s.Cases(ctx, "o", "s"); err != nil || len(cs) != 0 {
		t.Fatalf("before: %v, %v", cs, err)
	}
	if err := s.Put(ctx, store.Case{T: 0, Action: limen.Action{Key: k, Kind: "tool", Name: "bash"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSnapshot(ctx, "o", "s", "v", []byte("state")); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Case(ctx, k); err != nil || !ok {
		t.Fatalf("Case after Put: %v, %v", ok, err)
	}
	if b, ok, err := s.Snapshot(ctx, "o", "s", "v"); err != nil || !ok || string(b) != "state" {
		t.Fatalf("Snapshot after PutSnapshot: %q, %v, %v", b, ok, err)
	}
	if n := cached(s); n != 1 {
		t.Errorf("%d entries, want 1", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	if cs, err := s.Cases(ctx, "o", "s"); err != nil || len(cs) != 1 {
		t.Fatalf("after reopen: %v, %v", cs, err)
	}
}

// Readers racing a first write each see either no case or the case, never
// an error, and one read after the write sees it.
func TestReadsRaceTheFirstWrite(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	k := limen.Key{Org: "o", Subject: "s", ActionID: "a1"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				if _, _, err := s.Case(ctx, k); err != nil {
					t.Errorf("Case: %v", err)
					return
				}
				if _, err := s.Cases(ctx, "o", "s"); err != nil {
					t.Errorf("Cases: %v", err)
					return
				}
			}
		})
	}
	wg.Go(func() {
		if err := s.Put(ctx, store.Case{T: 0, Action: limen.Action{Key: k, Kind: "tool", Name: "bash"}}); err != nil {
			t.Errorf("Put: %v", err)
		}
	})
	wg.Wait()
	if _, ok, err := s.Case(ctx, k); err != nil || !ok {
		t.Fatalf("after the race: %v, %v", ok, err)
	}
}
