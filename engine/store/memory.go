// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"latere.ai/x/cribrum"
)

type subjectKey struct{ org, subject string }

type snapshotKey struct{ org, subject, version string }

// Memory is a [Store] in memory, for tests and for a replay that keeps
// nothing. It copies every value in and out, so a caller's later writes
// never reach it.
type Memory struct {
	mu        sync.Mutex
	cases     map[cribrum.Key][]byte
	order     map[subjectKey][]cribrum.Key
	next      map[subjectKey]int64
	profiles  map[cribrum.Scope]cribrum.Profile
	snapshots map[snapshotKey][]byte
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		cases:     map[cribrum.Key][]byte{},
		order:     map[subjectKey][]cribrum.Key{},
		next:      map[subjectKey]int64{},
		profiles:  map[cribrum.Scope]cribrum.Profile{},
		snapshots: map[snapshotKey][]byte{},
	}
}

var _ Store = (*Memory)(nil)

// Case implements [Store].
func (m *Memory) Case(ctx context.Context, k cribrum.Key) (Case, bool, error) {
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
	b, err := json.Marshal(c)
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
func (m *Memory) Profile(ctx context.Context, s cribrum.Scope) (cribrum.Profile, bool, error) {
	if err := ctx.Err(); err != nil {
		return cribrum.Profile{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[s]
	return p, ok, nil
}

// PutProfile implements [Store].
func (m *Memory) PutProfile(ctx context.Context, p cribrum.Profile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles[p.Scope] = p
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
