// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/gp"
	"latere.ai/x/limen/engine/kernel"
	"latere.ai/x/limen/estimate"
)

// model is one subject's fitted posterior. Predictions read post without
// a lock; a fit swaps in a new posterior and never mutates the old one.
type model struct {
	key  subjectKey
	post atomic.Pointer[fitted]
	// answers counts the answers the subject has given, and fitted the
	// count the current posterior was fitted through. A model is due when
	// they differ.
	answers atomic.Int64
	fitted  atomic.Int64
	// fitMu admits one fit at a time; scheduled marks a fit as pending, so
	// answers arriving together are fitted once.
	fitMu     sync.Mutex
	scheduled atomic.Bool
	bytes     atomic.Int64
	elem      *list.Element
	// ledger is the subject's cases as the cover rule reads them, built
	// when the model is loaded and kept with each write, so a prediction
	// reads no case from the store. The subject's lock guards it.
	ledger *ledger
}

func (m *model) due() int64 { return m.answers.Load() - m.fitted.Load() }

// fitted is a posterior with the number of the subject's answers it was
// fitted through, read together so a prediction names the model that made
// it (spec 009).
type fitted struct {
	*gp.Posterior
	answers int64
	model   Model
}

// ref names the posterior as the contract does.
func (f *fitted) ref() limen.ModelRef {
	return limen.ModelRef{Source: string(f.model), Answers: f.answers}
}

// cache holds models under a byte budget, least recently used first out.
type cache struct {
	mu     sync.Mutex
	budget int64
	used   int64
	byKey  map[subjectKey]*model
	lru    *list.List
}

func newCache(budget int64) *cache {
	return &cache{budget: budget, byKey: map[subjectKey]*model{}, lru: list.New()}
}

// get returns the cached model and marks it used, or nil.
func (c *cache) get(k subjectKey) *model {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.byKey[k]
	if m != nil {
		c.lru.MoveToFront(m.elem)
	}
	return m
}

// add caches m unless another model for the key arrived first, which it
// returns instead.
func (c *cache) add(m *model) *model {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.byKey[m.key]; old != nil {
		c.lru.MoveToFront(old.elem)
		return old
	}
	m.elem = c.lru.PushFront(m)
	c.byKey[m.key] = m
	c.used += m.bytes.Load()
	c.evictLocked()
	return m
}

// resize records a model's new size after a fit.
func (c *cache) resize(m *model, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := m.bytes.Swap(bytes)
	if c.byKey[m.key] == m {
		c.used += bytes - old
		c.evictLocked()
	}
}

func (c *cache) evictLocked() {
	for c.used > c.budget && c.lru.Len() > 1 {
		back := c.lru.Back()
		c.lru.Remove(back)
		m, ok := back.Value.(*model)
		if !ok {
			continue
		}
		delete(c.byKey, m.key)
		c.used -= m.bytes.Load()
	}
}

func (c *cache) drop(k subjectKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.byKey[k]; m != nil {
		c.lru.Remove(m.elem)
		delete(c.byKey, k)
		c.used -= m.bytes.Load()
	}
}

func (c *cache) all() []*model {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*model, 0, len(c.byKey))
	for _, m := range c.byKey {
		out = append(out, m)
	}
	return out
}

// sizeOf approximates a posterior's memory under the version v: the factor
// and the points.
func (v version) sizeOf(p *gp.Posterior) int64 {
	n := int64(p.Len())
	return 8*n*n + 8*n*int64(v.dimA+v.dimC+8) + 256
}

// model returns the subject's model, loading its stored snapshot or starting
// from the prior when it is not cached, and fitting it inline when the
// engine fits inline and enough answers wait.
func (e *Engine) model(ctx context.Context, k subjectKey) (*model, error) {
	m := e.models.get(k)
	if m == nil {
		loaded, err := e.load(ctx, k)
		if err != nil {
			return nil, err
		}
		m = e.models.add(loaded)
	}
	if e.inline > 0 && m.due() >= int64(e.inline) {
		if err := e.fit(ctx, m, true); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// load builds a model from the store: the stored snapshot of the engine's
// version when there is one, the prior otherwise, with the subject's
// answer count, so a model that missed answers while evicted or stopped,
// or a version that has never fitted the person, is due at once. Its fit
// runs off the hot path, so until it lands, about a second after a
// switch of the version, the person's predictions come from the prior.
func (e *Engine) load(ctx context.Context, k subjectKey) (*model, error) {
	m := &model{key: k}
	cases, err := e.st.Cases(ctx, k.org, k.subject)
	if err != nil {
		return nil, err
	}
	var answers int64
	for _, c := range cases {
		answers += int64(len(c.Answers))
	}
	m.answers.Store(answers)
	m.ledger = newLedger(cases)
	post := gp.Prior(e.kern)
	b, ok, err := e.st.Snapshot(ctx, k.org, k.subject, e.ver.snapshot)
	if err != nil {
		return nil, err
	}
	if ok {
		s, err := decodeSnapshot(b)
		if err != nil {
			return nil, fmt.Errorf("engine: snapshot of %s in %s: %w", k.subject, k.org, err)
		}
		for _, p := range s.State.Train {
			if !e.ver.fits(p) {
				return nil, fmt.Errorf("engine: snapshot %s of %s in %s holds a vector of %d and %d dimensions, not one of %s",
					e.ver.snapshot, k.subject, k.org, len(p.Tool), len(p.Ctx), e.ver.model)
			}
		}
		if post, err = gp.Restore(e.kern, s.State); err != nil {
			return nil, fmt.Errorf("engine: snapshot of %s in %s: %w", k.subject, k.org, err)
		}
		m.fitted.Store(s.Answers)
	}
	m.post.Store(&fitted{Posterior: post, answers: m.fitted.Load(), model: e.ver.model})
	m.bytes.Store(e.ver.sizeOf(post))
	if m.due() > 0 && e.inline <= 0 {
		e.schedule(m)
	}
	return m, nil
}

// training returns the subject's answered cases as points of the engine's
// version and their latest answers, in the order of T, with the weight of
// each latest answer under weights/2 when the version weighs the answers,
// and nil weights when it does not. A case featurized by another
// featurizer version is featurized again from its stored action, whose
// input is gone.
func (e *Engine) training(ctx context.Context, k subjectKey) ([]kernel.Point, []bool, []float64, int64, error) {
	cases, err := e.st.Cases(ctx, k.org, k.subject)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	var (
		pts     []kernel.Point
		approve []bool
		weights []float64
		answers int64
		graded  []estimate.Weights
	)
	if e.ver.weighted {
		graded = e.policy.Graded(records(cases))
	}
	for i, c := range cases {
		answers += int64(len(c.Answers))
		if len(c.Answers) == 0 {
			continue
		}
		pts = append(pts, e.ver.point(c, c.T))
		approve = append(approve, c.Answers[len(c.Answers)-1].Approve)
		if graded != nil {
			weights = append(weights, graded[i].Latest)
		}
	}
	if e.ver.weighted && weights == nil {
		weights = []float64{}
	}
	return pts, approve, weights, answers, nil
}

// fit refits a model on the subject's answers, stores the state, and swaps
// the new posterior in. One fit runs per model at a time. The answers are
// read under the subject's lock, which locked says the caller holds, and the
// factorization runs without it, so predictions never wait for a fit. Locks
// are taken in one order, the subject's and then the model's fit lock.
func (e *Engine) fit(ctx context.Context, m *model, locked bool) error {
	unlock := func() {}
	if !locked {
		unlock = e.lock(m.key.org, m.key.subject)
	}
	m.fitMu.Lock()
	defer m.fitMu.Unlock()
	pts, approve, weights, answers, err := e.training(ctx, m.key)
	unlock()
	if err != nil {
		return err
	}
	if answers == m.fitted.Load() && m.post.Load() != nil {
		return nil
	}
	post, err := gp.FitWeighted(e.kern, pts, approve, weights, e.opts)
	if err != nil {
		return fmt.Errorf("engine: fit of %s in %s: %w", m.key.subject, m.key.org, err)
	}
	b, err := encodeSnapshot(snapshot{State: post.State(), Answers: answers})
	if err != nil {
		return err
	}
	if err := e.st.PutSnapshot(ctx, m.key.org, m.key.subject, e.ver.snapshot, b); err != nil {
		return err
	}
	m.post.Store(&fitted{Posterior: post, answers: answers, model: e.ver.model})
	m.fitted.Store(answers)
	e.models.resize(m, e.ver.sizeOf(post))
	return nil
}

// schedule queues a fit of m after the debounce, unless one is pending.
func (e *Engine) schedule(m *model) {
	if !m.scheduled.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(e.debounc, func() {
		select {
		case e.work <- m.key:
		case <-e.stop:
		}
	})
}

func (e *Engine) fitter() {
	defer e.wg.Done()
	for {
		select {
		case <-e.stop:
			return
		case k := <-e.work:
			m := e.models.get(k)
			if m == nil {
				continue
			}
			m.scheduled.Store(false)
			if err := e.fit(context.Background(), m, false); err != nil {
				// The model keeps serving its last posterior; the next answer
				// schedules another attempt.
				continue
			}
			if m.due() > 0 {
				e.schedule(m)
			}
		}
	}
}

// fitSubject loads the subject's model, fitting it now if answers wait.
func (e *Engine) fitSubject(ctx context.Context, k subjectKey) error {
	unlock := e.lock(k.org, k.subject)
	m := e.models.get(k)
	if m == nil {
		loaded, err := e.load(ctx, k)
		if err != nil {
			unlock()
			return err
		}
		m = e.models.add(loaded)
	}
	unlock()
	if m.due() == 0 {
		return nil
	}
	return e.fit(ctx, m, false)
}

// Flush fits every cached model that has answers it was not fitted on, in
// the caller's goroutine, and returns the first error. A replay that keeps
// its records calls it, and so can a test or an operator before a restart.
func (e *Engine) Flush(ctx context.Context) error {
	for _, m := range e.models.all() {
		if m.due() == 0 {
			continue
		}
		if err := e.fit(ctx, m, false); err != nil {
			return err
		}
	}
	return nil
}
