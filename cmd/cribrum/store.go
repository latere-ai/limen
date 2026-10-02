// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"latere.ai/x/cribrum/engine/store"
	"latere.ai/x/cribrum/engine/store/file"
)

// openStore opens the ledger kept in dir, creating it when it is new.
func openStore(dir string) (store.Store, error) { return file.Open(dir) }
