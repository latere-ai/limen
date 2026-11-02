// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
)

// minimumOf is the minimum an option sets: zero is the contract's value
// def, and a negative value no minimum.
func minimumOf(option, def int) int {
	switch {
	case option == 0:
		return def
	case option < 0:
		return 0
	}
	return option
}

// optionOf is the option that sets the minimum n, the inverse of
// minimumOf: no minimum is a negative option.
func optionOf(n int) int {
	if n == 0 {
		return -1
	}
	return n
}

// holds reports whether the answer minimum can change v: v is an allow and
// the engine applies a minimum.
func (e *Engine) holds(v limen.Verdict) bool {
	return v == limen.Allow && (e.minAllows > 0 || e.minSessions > 0)
}

// tally counts the approvals among cases, a subject's cases, of actions
// with the label l, against the engine's answer minimum.
func (e *Engine) tally(cases []store.Case, l string) limen.Minimum {
	var t limen.Tally
	for _, c := range cases {
		if label(c) == l {
			t.Add(c.Action, c.Answers)
		}
	}
	return t.Minimum(e.minAllows, e.minSessions)
}
