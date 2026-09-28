// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

// Verdict is what a harness does with one call. The four values are
// ordered from most to least permissive, and composing verdicts from
// several layers keeps the least permissive one ([Least]).
type Verdict string

const (
	// Allow runs the call.
	Allow Verdict = "allow"
	// Flag runs the call and asks the person about it afterwards. Limen
	// returns it only for an allow sampled as an audit.
	Flag Verdict = "flag"
	// Ask holds the call until the person answers.
	Ask Verdict = "ask"
	// Block refuses the call. The agent receives the refusal as the call's
	// result.
	Block Verdict = "block"
)

// rank orders the verdicts from most to least permissive. A value outside
// the four has no rank.
var rank = map[Verdict]int{Allow: 0, Flag: 1, Ask: 2, Block: 3}

// Valid reports whether v is one of the four verdicts.
func (v Verdict) Valid() bool {
	_, ok := rank[v]
	return ok
}

// Least returns the least permissive of the verdicts. An invalid verdict
// counts as [Block], so a malformed input can only narrow the result, and
// Least with no arguments is [Block] for the same reason.
func Least(vs ...Verdict) Verdict {
	if len(vs) == 0 {
		return Block
	}
	out := Allow
	for _, v := range vs {
		if !v.Valid() {
			return Block
		}
		if rank[v] > rank[out] {
			out = v
		}
	}
	return out
}
