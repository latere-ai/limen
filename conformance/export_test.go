// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package conformance

// RuleNames returns the names of the rules of the suite, in order, for
// the test that holds each rule to a defect it catches.
func RuleNames() []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.name
	}
	return out
}
