// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"sync"
	"time"

	"latere.ai/x/limen"
)

// storedJSON writes what the store keeps: map keys in order, so the same
// value is the same bytes, and invalid UTF-8 in a string replaced.
var storedJSON = json.JoinOptions(
	json.Deterministic(true),
	jsontext.AllowInvalidUTF8(true),
)

type subjectKey struct{ org, subject string }

type snapshotKey struct{ org, subject, version string }

type weekKey struct {
	org  string
	from int64
}

// Memory is a [Store] in memory, for tests and for a replay that keeps
// nothing. It copies every value in and out, so a caller's later writes
// never reach it.
type Memory struct {
	mu        sync.Mutex
	cases     map[limen.Key][]byte
	order     map[subjectKey][]limen.Key
	next      map[subjectKey]int64
	profiles  map[limen.Scope]limen.Profile
	snapshots map[snapshotKey][]byte
	weeks     map[weekKey][]byte
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		cases:     map[limen.Key][]byte{},
		order:     map[subjectKey][]limen.Key{},
		next:      map[subjectKey]int64{},
		profiles:  map[limen.Scope]limen.Profile{},
		snapshots: map[snapshotKey][]byte{},
		weeks:     map[weekKey][]byte{},
	}
}

var _ Store = (*Memory)(nil)

// Case implements [Store].
func (m *Memory) Case(ctx context.Context, k limen.Key) (Case, bool, error) {
	if err := ctx.Err(); err != nil {
		return Case{}, false, err
	}
	m.mu.Lock()
	b, ok := m.cases[k]
	m.mu.Unlock()
	if !ok {
		return Case{}, false, nil
	}
	var c Case
	return c, true, json.Unmarshal(b, &c)
}

// Put implements [Store].
func (m *Memory) Put(ctx context.Context, c Case) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Action.Input = nil
	b, err := json.Marshal(c, storedJSON)
	if err != nil {
		return err
	}
	k := c.Action.Key
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cases[k]; !ok {
		sk := subjectKey{k.Org, k.Subject}
		m.order[sk] = append(m.order[sk], k)
	}
	m.cases[k] = b
	return nil
}

// NextT implements [Store].
func (m *Memory) NextT(ctx context.Context, org, subject string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sk := subjectKey{org, subject}
	t := m.next[sk]
	m.next[sk] = t + 1
	return t, nil
}

// Cases implements [Store].
func (m *Memory) Cases(ctx context.Context, org, subject string) ([]Case, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	keys := slices.Clone(m.order[subjectKey{org, subject}])
	blobs := make([][]byte, len(keys))
	for i, k := range keys {
		blobs[i] = m.cases[k]
	}
	m.mu.Unlock()
	out := make([]Case, len(blobs))
	for i, b := range blobs {
		if err := json.Unmarshal(b, &out[i]); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(out, func(a, b Case) int { return int(a.T - b.T) })
	return out, nil
}

// Profile implements [Store].
func (m *Memory) Profile(ctx context.Context, s limen.Scope) (limen.Profile, bool, error) {
	if err := ctx.Err(); err != nil {
		return limen.Profile{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[s]
	return p, ok, nil
}

// PutProfile implements [Store].
func (m *Memory) PutProfile(ctx context.Context, p limen.Profile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles[p.Scope] = p
	return nil
}

// DeleteProfile implements [Store].
func (m *Memory) DeleteProfile(ctx context.Context, s limen.Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.profiles, s)
	return nil
}

// Subjects implements [Store].
func (m *Memory) Subjects(ctx context.Context, org string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for k := range m.order {
		if k.org == org {
			out = append(out, k.subject)
		}
	}
	slices.Sort(out)
	return out, nil
}

// WeekReport implements [Store].
func (m *Memory) WeekReport(ctx context.Context, org string, from time.Time) (limen.WeekReport, bool, error) {
	if err := ctx.Err(); err != nil {
		return limen.WeekReport{}, false, err
	}
	m.mu.Lock()
	b, ok := m.weeks[weekKey{org, from.UnixNano()}]
	m.mu.Unlock()
	if !ok {
		return limen.WeekReport{}, false, nil
	}
	var w limen.WeekReport
	return w, true, json.Unmarshal(b, &w)
}

// PutWeekReport implements [Store].
func (m *Memory) PutWeekReport(ctx context.Context, org string, w limen.WeekReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := json.Marshal(w, storedJSON)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.weeks[weekKey{org, w.From.UnixNano()}] = b
	return nil
}

// Snapshot implements [Store].
func (m *Memory) Snapshot(ctx context.Context, org, subject, version string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.snapshots[snapshotKey{org, subject, version}]
	return slices.Clone(b), ok, nil
}

// PutSnapshot implements [Store].
func (m *Memory) PutSnapshot(ctx context.Context, org, subject, version string, state []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots[snapshotKey{org, subject, version}] = slices.Clone(state)
	return nil
}

// Forget implements [Store].
func (m *Memory) Forget(ctx context.Context, org, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sk := subjectKey{org, subject}
	for _, k := range m.order[sk] {
		delete(m.cases, k)
	}
	delete(m.order, sk)
	delete(m.next, sk)
	for k := range m.snapshots {
		if k.org == org && k.subject == subject {
			delete(m.snapshots, k)
		}
	}
	return nil
}

// Close implements [Store].
func (m *Memory) Close() error { return nil }
