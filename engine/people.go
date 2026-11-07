// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"fmt"
	"sync"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
)

// people counts the people of each organization against a cap. A person is
// a subject that has a case or an own profile in the organization. The set
// of an organization is read from the store once, when the engine first
// needs it, and kept in memory from then on: a new person is added when a
// write admits them and a person is removed when they are forgotten, so a
// request never lists a directory.
type people struct {
	st  store.Store
	max int // 0 means no limit

	mu   sync.Mutex
	orgs map[string]*roster
}

// roster is the people of one organization. Its mutex orders every
// admission, so two new people at the cap cannot both get in.
type roster struct {
	mu     sync.Mutex
	loaded bool
	set    map[string]struct{}
}

func newPeople(st store.Store, max int) *people {
	return &people{st: st, max: max, orgs: map[string]*roster{}}
}

func (p *people) roster(org string) *roster {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.orgs[org]
	if !ok {
		r = &roster{}
		p.orgs[org] = r
	}
	return r
}

// load reads the organization's people from the store, once. The caller
// holds r.mu.
func (p *people) load(ctx context.Context, r *roster, org string) error {
	if r.loaded {
		return nil
	}
	withCases, err := p.st.Subjects(ctx, org)
	if err != nil {
		return err
	}
	withProfile, err := p.st.ProfileSubjects(ctx, org)
	if err != nil {
		return err
	}
	r.set = make(map[string]struct{}, len(withCases)+len(withProfile))
	for _, s := range withCases {
		r.set[s] = struct{}{}
	}
	for _, s := range withProfile {
		r.set[s] = struct{}{}
	}
	r.loaded = true
	return nil
}

func quotaError(org string, max int) error {
	return &limen.Error{Code: limen.CodeQuotaExceeded, Fields: []string{"subject"},
		Detail: fmt.Sprintf("organization %s already has %d people, the most it may have", org, max)}
}

// admit adds the person to the organization unless it is full, and reports
// whether they were new. A person already there is always admitted. A
// caller whose write then fails calls [people.release] with added, so the
// failure does not use up a place.
func (p *people) admit(ctx context.Context, org, subject string) (added bool, err error) {
	if p.max == 0 {
		return false, nil
	}
	r := p.roster(org)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := p.load(ctx, r, org); err != nil {
		return false, err
	}
	if _, ok := r.set[subject]; ok {
		return false, nil
	}
	if len(r.set) >= p.max {
		return false, quotaError(org, p.max)
	}
	r.set[subject] = struct{}{}
	return true, nil
}

// release takes back a place admit gave to a person whose write failed.
func (p *people) release(org, subject string, added bool) {
	if added {
		p.remove(org, subject)
	}
}

// remove drops a person from the organization's count, after Forget. An
// organization not loaded yet has nothing to drop: its first count reads
// the store, where the person is gone.
func (p *people) remove(org, subject string) {
	if p.max == 0 {
		return
	}
	r := p.roster(org)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.set, subject)
}

// fits reports, without admitting anyone, whether the people in subjects
// that are new to org would fit in it, and fails with
// [limen.CodeQuotaExceeded] when they would not. A replay asks it before it
// writes a record, so a refusal leaves nothing half kept.
func (p *people) fits(ctx context.Context, org string, subjects []string) error {
	if p.max == 0 {
		return nil
	}
	r := p.roster(org)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := p.load(ctx, r, org); err != nil {
		return err
	}
	fresh := 0
	for _, s := range subjects {
		if _, ok := r.set[s]; !ok {
			fresh++
		}
	}
	if len(r.set)+fresh > p.max {
		return quotaError(org, p.max)
	}
	return nil
}
