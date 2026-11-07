// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
)

// lockCounts reads the sizes of the two lock maps.
func lockCounts(e *Engine) (subjects, reports int) {
	e.locksMu.Lock()
	defer e.locksMu.Unlock()
	return len(e.locks), len(e.reports)
}

// Reads of people and organizations Limen holds nothing about leave no
// lock behind, so a stream of unknown names does not grow memory.
func TestReadsLeaveNoLocks(t *testing.T) {
	e := New(Options{Inline: 1})
	defer e.Close()
	ctx := context.Background()
	to := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	for i := range 200 {
		org, subject := fmt.Sprintf("org_%d", i), fmt.Sprintf("usr_%d", i)
		if _, err := e.Cases(ctx, limen.CaseQuery{Org: org, Subject: subject}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Learned(ctx, limen.LearnedQuery{Org: org, Subject: subject}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Report(ctx, limen.ReportQuery{Org: org, From: to.AddDate(0, 0, -14), To: to}); err != nil {
			t.Fatal(err)
		}
	}
	if s, r := lockCounts(e); s != 0 || r != 0 {
		t.Errorf("%d subject locks and %d report locks left after the reads", s, r)
	}
}

// A lock in use keeps its entry, goroutines on one key exclude each other,
// and the entry is gone when the last holder lets go.
func TestLockExcludesAndShrinks(t *testing.T) {
	e := New(Options{Inline: 1})
	defer e.Close()
	const n = 2000
	for name, lock := range map[string]func() func(){
		"subject": func() func() { return e.lock("o", "s") },
		"report":  func() func() { return e.reportLock("o") },
	} {
		t.Run(name, func(t *testing.T) {
			var counter int
			var wg sync.WaitGroup
			for range 2 {
				wg.Go(func() {
					for range n {
						unlock := lock()
						counter++
						unlock()
					}
				})
			}
			wg.Wait()
			if counter != 2*n {
				t.Errorf("counter %d, want %d", counter, 2*n)
			}
			if s, r := lockCounts(e); s != 0 || r != 0 {
				t.Errorf("%d subject locks and %d report locks left", s, r)
			}
			// A waiter keeps the entry alive while the holder lets go.
			first := lock()
			done := make(chan struct{})
			go func() { lock()(); close(done) }()
			time.Sleep(10 * time.Millisecond)
			first()
			<-done
			if s, r := lockCounts(e); s != 0 || r != 0 {
				t.Errorf("%d subject locks and %d report locks left after the waiter", s, r)
			}
		})
	}
}
