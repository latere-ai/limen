// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storetest_test

import (
	"testing"

	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/engine/store/file"
	"latere.ai/x/limen/engine/store/storetest"
)

// The suite passes against the memory store.
func TestRunAgainstMemory(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return store.NewMemory() })
}

// The persistence suite passes against the file store.
func TestRunReopenAgainstFile(t *testing.T) {
	storetest.RunReopen(t, func(t *testing.T, dir string) store.Store {
		s, err := file.OpenWith(dir, file.Options{NoSync: true})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}
