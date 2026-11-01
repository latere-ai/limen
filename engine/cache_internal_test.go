// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import "testing"

// The models in memory stay within the byte budget (spec 004, criterion
// 8): adding or growing a model evicts the least recently used ones. One
// model larger than the whole budget stays alone, because a prediction
// needs the model it reads.
func TestCacheStaysWithinTheBudget(t *testing.T) {
	c := newCache(1000)
	sized := func(subject string, bytes int64) *model {
		m := &model{key: subjectKey{"o", subject}}
		m.bytes.Store(bytes)
		return m
	}
	check := func(want ...string) {
		t.Helper()
		if c.used > c.budget && c.lru.Len() > 1 {
			t.Errorf("%d bytes in %d models over a budget of %d", c.used, c.lru.Len(), c.budget)
		}
		var used int64
		for _, m := range c.all() {
			used += m.bytes.Load()
		}
		if used != c.used || len(c.byKey) != len(want) {
			t.Errorf("%d models of %d bytes, counted %d; want %v", len(c.byKey), used, c.used, want)
		}
		for _, s := range want {
			if c.byKey[subjectKey{"o", s}] == nil {
				t.Errorf("%s was evicted; want %v", s, want)
			}
		}
	}
	c.add(sized("a", 400))
	c.add(sized("b", 400))
	check("a", "b")
	c.get(subjectKey{"o", "a"})
	c.add(sized("c", 400))
	check("a", "c")
	c.resize(c.byKey[subjectKey{"o", "c"}], 700)
	check("c")
	c.add(sized("big", 5000))
	check("big")
	c.drop(subjectKey{"o", "big"})
	check()
}
