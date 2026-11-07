// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store/file"
)

// holdSnapshot is a file store whose first PutSnapshot reports that it was
// reached and waits to be released before it writes.
type holdSnapshot struct {
	*file.Store
	reached, release chan struct{}
}

func (h *holdSnapshot) PutSnapshot(ctx context.Context, org, name, version string, state []byte) error {
	select {
	case h.reached <- struct{}{}:
		<-h.release
	default:
	}
	return h.Store.PutSnapshot(ctx, org, name, version, state)
}

// TestAFitInFlightDoesNotReviveAForgottenPerson holds that a fit already past
// reading the answers when Forget runs writes nothing for that person
// afterward: no file is left on disk, and a restarted engine predicts the
// person from the prior.
func TestAFitInFlightDoesNotReviveAForgottenPerson(t *testing.T) {
	dir := t.TempDir()
	fst, err := file.OpenWith(dir, file.Options{NoSync: true})
	if err != nil {
		t.Fatal(err)
	}
	st := &holdSnapshot{Store: fst, reached: make(chan struct{}), release: make(chan struct{})}
	e := engine.New(engine.Options{Store: st, Debounce: time.Millisecond})
	train(t, e, "s", 12)
	select {
	case <-st.reached:
	case <-time.After(30 * time.Second):
		t.Fatal("no fit reached the snapshot write")
	}
	// The held write may hold the subject's lock, so Forget either returns
	// while the write is held, which is the case to catch, or waits behind
	// it. The write is released when Forget returns, or after a grace
	// period in which it did not; a slow machine can only make the unfixed
	// code pass, never fail the fixed one.
	forgot := make(chan error, 1)
	go func() { forgot <- e.Forget(ctx, "o", "s") }()
	var forgetErr error
	select {
	case forgetErr = <-forgot:
		close(st.release)
	case <-time.After(200 * time.Millisecond):
		close(st.release)
		forgetErr = <-forgot
	}
	if forgetErr != nil {
		t.Fatal(forgetErr)
	}
	// Close waits for the fitter, so the held fit has finished.
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	var left []string
	err = filepath.WalkDir(filepath.Join(dir, "subjects"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			left = append(left, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Errorf("files of a forgotten person remain: %v", left)
	}
	if err := fst.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := file.OpenWith(dir, file.Options{NoSync: true})
	if err != nil {
		t.Fatal(err)
	}
	e2 := engine.New(engine.Options{Store: again, Debounce: time.Millisecond})
	defer e2.Close()
	pr, err := e2.Predict(ctx, bash("s", "probe", "go test ./..."))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Approve < 0.45 || pr.Approve > 0.55 {
		t.Errorf("approve after restart = %v, want the 0.5 prior", pr.Approve)
	}
}
