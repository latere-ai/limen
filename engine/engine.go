// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package engine is Limen's service: the predictor, the ledger and the
// administration of the contract in latere.ai/x/limen, over a [store.Store].
//
// A prediction featurizes the action (engine/featurize), reads the
// subject's fitted model, and suggests the verdict the effective cost
// profile's thresholds give the approval probability. It never fits: a model
// is an immutable [gp.Posterior] read through an atomic pointer, refitted off
// the hot path when answers arrive, either by background fitters after a
// debounce (the default) or, for a replay, inline once enough answers are
// waiting. Fitted states are kept in the store, so a restarted engine loads
// a model rather than refitting it.
//
// The time a model sees is the case's ordinal in its subject's history, not
// the wall clock: the research's forgetting scale counts decisions
// (spec 004). Wall-clock receive times serve report windows only.
package engine

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/featurize"
	"latere.ai/x/limen/engine/gp"
	"latere.ai/x/limen/engine/kernel"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/estimate"
)

// Version names the model version this engine serves: features/1, the
// additive kernel with the research's hyperparameters, and the label cap.
// A prediction's source is "learned/" + Version, and a model never mixes
// versions (spec 004).
const Version = "1"

// Source is the source of every prediction this engine makes.
const Source = "learned/" + Version

// Options configure an [Engine]. The zero value is a working engine over a
// fresh in-memory store with background fitting.
type Options struct {
	// Store keeps the ledger, the profiles and the fitted models; nil is a
	// fresh [store.Memory].
	Store store.Store
	// Clock stamps receive and answer times; nil is time.Now.
	Clock func() time.Time
	// Inline, when positive, fits a model in the caller's goroutine once it
	// has that many answers it was not fitted on, before predicting, and
	// starts no background fitter. A replay uses it, so each prediction sees
	// the answers before it, as the research's simulation refitted every
	// eight. Zero fits in the background.
	Inline int
	// Workers is the number of background fitters; zero is half the
	// processors, at least one.
	Workers int
	// Debounce gathers answers arriving together into one fit; zero is one
	// second.
	Debounce time.Duration
	// MaxLabels caps a model's training set at the most recent answered
	// cases; zero is the spec's 512.
	MaxLabels int
	// CacheBytes bounds the fitted models held in memory; zero is 1 GiB. An
	// evicted model is loaded from the store on its next prediction.
	CacheBytes int64
}

type subjectKey struct{ org, subject string }

// Engine implements [limen.Service].
type Engine struct {
	st      store.Store
	clock   func() time.Time
	kern    kernel.Additive
	opts    gp.Options
	inline  int
	debounc time.Duration

	locksMu sync.Mutex
	locks   map[subjectKey]*sync.Mutex

	models *cache

	work    chan subjectKey
	stop    chan struct{}
	wg      sync.WaitGroup
	closeMu sync.Once
}

var _ limen.Service = (*Engine)(nil)

// New returns an engine. Call [Engine.Close] to stop its fitters.
func New(o Options) *Engine {
	e := build(o)
	if e.inline <= 0 {
		workers := cmp.Or(o.Workers, max(1, runtime.GOMAXPROCS(0)/2))
		e.work = make(chan subjectKey, 1024)
		for range workers {
			e.wg.Add(1)
			go e.fitter()
		}
	}
	return e
}

// build is an engine with no fitter running: an inline engine is complete,
// and New starts the fitters of a background one.
func build(o Options) *Engine {
	e := &Engine{
		st:      o.Store,
		clock:   o.Clock,
		kern:    kernel.DefaultAdditive(),
		opts:    gp.Options{MaxLabels: cmp.Or(o.MaxLabels, gp.DefaultMaxLabels)},
		inline:  o.Inline,
		debounc: cmp.Or(o.Debounce, time.Second),
		locks:   map[subjectKey]*sync.Mutex{},
		models:  newCache(cmp.Or(o.CacheBytes, 1<<30)),
		stop:    make(chan struct{}),
	}
	if e.st == nil {
		e.st = store.NewMemory()
	}
	if e.clock == nil {
		e.clock = time.Now
	}
	return e
}

// Close stops the background fitters and waits for a fit in flight. It does
// not close the store, which the caller opened.
func (e *Engine) Close() error {
	e.closeMu.Do(func() { close(e.stop) })
	e.wg.Wait()
	return nil
}

func (e *Engine) lock(org, subject string) func() {
	k := subjectKey{org, subject}
	e.locksMu.Lock()
	m, ok := e.locks[k]
	if !ok {
		m = &sync.Mutex{}
		e.locks[k] = m
	}
	e.locksMu.Unlock()
	m.Lock()
	return m.Unlock
}

// digest names an action by every field but its input: the input is read
// at prediction time and never kept, so it cannot tell two actions apart
// later.
func digest(a limen.Action) (string, error) {
	a.Input = nil
	b, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("engine: digest of action %s: %w", a.ActionID, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func conflict(k limen.Key) error {
	return &limen.Error{Code: limen.CodeActionConflict, Fields: []string{"action_id"},
		Detail: fmt.Sprintf("action %s for %s in %s was first recorded as a different action", k.ActionID, k.Subject, k.Org)}
}

// newCase assigns the case its ordinal and receive time and featurizes the
// action, the one moment its input is read.
func (e *Engine) newCase(ctx context.Context, a limen.Action, dg string, at time.Time) (store.Case, error) {
	t, err := e.st.NextT(ctx, a.Org, a.Subject)
	if err != nil {
		return store.Case{}, err
	}
	f := featurize.Of(a)
	label := featurize.Label(a)
	a.Input = nil
	return store.Case{T: t, ReceivedAt: at, Digest: dg, Action: a, Featurizer: featurize.Version, A: f.A, C: f.C,
		Label: label, Labels: featurize.LabelsVersion}, nil
}

// Predict implements [limen.Predictor].
func (e *Engine) Predict(ctx context.Context, a limen.Action) (limen.Prediction, error) {
	return e.predictAt(ctx, a, e.clock())
}

func (e *Engine) predictAt(ctx context.Context, a limen.Action, at time.Time) (limen.Prediction, error) {
	if err := ctx.Err(); err != nil {
		return limen.Prediction{}, err
	}
	if err := a.Validate(); err != nil {
		return limen.Prediction{}, err
	}
	dg, err := digest(a)
	if err != nil {
		return limen.Prediction{}, err
	}
	defer e.lock(a.Org, a.Subject)()
	c, ok, err := e.st.Case(ctx, a.Key)
	if err != nil {
		return limen.Prediction{}, err
	}
	switch {
	case ok && c.Digest != dg:
		return limen.Prediction{}, conflict(a.Key)
	case ok && c.Prediction != nil:
		return *c.Prediction, nil
	case !ok:
		if c, err = e.newCase(ctx, a, dg, at); err != nil {
			return limen.Prediction{}, err
		}
	}
	prof, err := e.profile(ctx, limen.ScopeOf(a))
	if err != nil {
		return limen.Prediction{}, err
	}
	m, err := e.model(ctx, subjectKey{a.Org, a.Subject})
	if err != nil {
		return limen.Prediction{}, err
	}
	post := m.post.Load()
	pr := post.Predict(kernel.Point{Tool: c.A, Ctx: c.C, T: float64(c.T)})
	v := prof.Thresholds.Verdict(pr.Approve)
	er := evidenceRatio(pr.Var, post.Kernel().Var())
	p := limen.Prediction{
		Key:           a.Key,
		Verdict:       v,
		Approve:       pr.Approve,
		Uncertainty:   math.Sqrt(pr.Var),
		EvidenceRatio: er,
		AskKind:       askKind(v, er),
		Thresholds:    prof.Thresholds,
		Profile:       prof.Scope,
		AuditRate:     prof.AuditRate,
		Source:        Source,
		Reason:        reason(pr.Approve, prof.Thresholds, post.Len()),
	}
	c.Prediction = &p
	by := post.answers
	c.PredictedBy = &by
	if err := e.st.Put(ctx, c); err != nil {
		return limen.Prediction{}, err
	}
	return p, nil
}

func reason(approve float64, t limen.Thresholds, answers int) string {
	if answers == 0 {
		return "Limen has no answers from this person yet. It suggests ask."
	}
	d := digits(approve, t.AllowAbove, t.BlockBelow)
	switch t.Verdict(approve) {
	case limen.Allow:
		return fmt.Sprintf("The approval probability is %.*f, above the allow threshold (%.*f). Limen suggests allow.",
			d, approve, d, t.AllowAbove)
	case limen.Block:
		return fmt.Sprintf("The approval probability is %.*f, below the block threshold (%.*f). Limen suggests block.",
			d, approve, d, t.BlockBelow)
	default:
		return fmt.Sprintf("The approval probability is %.*f, between the block threshold (%.*f) and the allow threshold (%.*f). Limen suggests ask.",
			d, approve, d, t.BlockBelow, d, t.AllowAbove)
	}
}

// digits is the number of decimals, two or more, at which p and every
// threshold print in the order they have, so a reason never reads "0.90,
// above the allow threshold (0.90)".
func digits(p float64, thresholds ...float64) int {
	const most = 6
	for d := 2; d < most; d++ {
		rp, kept := rounded(p, d), true
		for _, t := range thresholds {
			kept = kept && cmp.Compare(p, t) == cmp.Compare(rp, rounded(t, d))
		}
		if kept {
			return d
		}
	}
	return most
}

func rounded(x float64, d int) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', d, 64), 64)
	return r
}

// Record implements [limen.Ledger].
func (e *Engine) Record(ctx context.Context, d limen.Decision) error {
	return e.recordAt(ctx, d, e.clock())
}

func (e *Engine) recordAt(ctx context.Context, d limen.Decision, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}
	defer e.lock(d.Org, d.Subject)()
	c, ok, err := e.st.Case(ctx, d.Key)
	if err != nil {
		return err
	}
	switch {
	case ok && c.Decision != nil:
		return &limen.Error{Code: limen.CodeAlreadyDecided, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("action %s for %s in %s is already decided %s", d.ActionID, d.Subject, d.Org, c.Decision.Verdict)}
	case !ok && d.Action == nil:
		return &limen.Error{Code: limen.CodeInvalidDecision, Fields: []string{"action"},
			Detail: fmt.Sprintf("action %s for %s in %s has no prediction, so the decision must carry the action", d.ActionID, d.Subject, d.Org)}
	case !ok:
		dg, err := digest(*d.Action)
		if err != nil {
			return err
		}
		if c, err = e.newCase(ctx, *d.Action, dg, at); err != nil {
			return err
		}
	case d.Action != nil:
		dg, err := digest(*d.Action)
		if err != nil {
			return err
		}
		if dg != c.Digest {
			return conflict(d.Key)
		}
	}
	d.Action = nil
	c.Decision = &d
	c.DecidedAt = at
	return e.st.Put(ctx, c)
}

// Answer implements [limen.Ledger]. The first answer to an action its
// decision showed a person is the review; every other answer trains only.
func (e *Engine) Answer(ctx context.Context, a limen.Answer) error {
	return e.answerAt(ctx, a, e.clock())
}

func (e *Engine) answerAt(ctx context.Context, a limen.Answer, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	k := subjectKey{a.Org, a.Subject}
	unlock := e.lock(a.Org, a.Subject)
	c, ok, err := e.st.Case(ctx, a.Key)
	if err == nil && (!ok || c.Decision == nil) {
		err = &limen.Error{Code: limen.CodeNoDecision, Fields: []string{"action_id"},
			Detail: fmt.Sprintf("action %s for %s in %s has no recorded decision", a.ActionID, a.Subject, a.Org)}
	}
	var m *model
	if err == nil {
		review := estimate.Pending(c.Contract())
		c.Answers = append(c.Answers, limen.RecordedAnswer{Approve: a.Approve, By: cmp.Or(a.By, a.Subject), At: at, Review: review})
		err = e.st.Put(ctx, c)
	}
	if err == nil {
		// Counted under the subject's lock, so a fit, which reads the
		// subject's answers under the same lock, sees either both the
		// answer and the count or neither.
		if m = e.models.get(k); m != nil {
			m.answers.Add(1)
		}
	}
	unlock()
	if err != nil {
		return err
	}
	if m != nil && e.inline <= 0 {
		e.schedule(m)
	}
	return nil
}

// Cases implements [limen.Ledger]. The cursor is the ordinal of the last
// case of the previous page.
func (e *Engine) Cases(ctx context.Context, q limen.CaseQuery) (limen.Page, error) {
	if err := ctx.Err(); err != nil {
		return limen.Page{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Page{}, err
	}
	after := int64(-1)
	if q.Cursor != "" {
		n, err := strconv.ParseInt(q.Cursor, 10, 64)
		if err != nil || n < 0 {
			return limen.Page{}, &limen.Error{Code: limen.CodeInvalidQuery, Fields: []string{"cursor"},
				Detail: fmt.Sprintf("cursor %q is not a cursor this engine returned", q.Cursor)}
		}
		after = n
	}
	limit := cmp.Or(q.Limit, limen.DefaultPageLimit)
	cases, err := e.st.Cases(ctx, q.Org, q.Subject)
	if err != nil {
		return limen.Page{}, err
	}
	page := limen.Page{Entries: []limen.Case{}}
	for _, c := range cases {
		if c.T <= after || (q.ActionID != "" && c.Action.ActionID != q.ActionID) {
			continue
		}
		lc := c.Contract()
		if q.Pending && !estimate.Pending(lc) {
			continue
		}
		if len(page.Entries) == limit {
			page.NextCursor = strconv.FormatInt(after, 10)
			break
		}
		page.Entries = append(page.Entries, lc)
		after = c.T
	}
	return page, nil
}

// Reliability implements [limen.Ledger] with latere.ai/x/limen/estimate. The
// window holds the cases first received in [From, To).
func (e *Engine) Reliability(ctx context.Context, q limen.ReliabilityQuery) (limen.Reliability, error) {
	if err := ctx.Err(); err != nil {
		return limen.Reliability{}, err
	}
	if err := q.Validate(); err != nil {
		return limen.Reliability{}, err
	}
	cases, err := e.st.Cases(ctx, q.Org, q.Subject)
	if err != nil {
		return limen.Reliability{}, err
	}
	var rows []estimate.Row
	for _, c := range cases {
		if c.ReceivedAt.Before(q.From) || !c.ReceivedAt.Before(q.To) {
			continue
		}
		if r, ok := estimate.RowOf(c.Contract(), q.Of); ok {
			rows = append(rows, r)
		}
	}
	prof, err := e.profile(ctx, limen.Scope{Org: q.Org})
	if err != nil {
		return limen.Reliability{}, err
	}
	return limen.Reliability{
		ReliabilityQuery: q,
		FalseAllow:       estimate.Rate(rows, estimate.FalseAllow),
		FalseBlock:       estimate.Rate(rows, estimate.FalseBlock),
		Certificate:      estimate.Certificate(rows, prof.Certify),
	}, nil
}

// profile is the effective profile for a scope: the most specific saved
// one, or the default.
func (e *Engine) profile(ctx context.Context, s limen.Scope) (limen.Profile, error) {
	for _, c := range s.Candidates() {
		p, ok, err := e.st.Profile(ctx, c)
		if err != nil {
			return limen.Profile{}, err
		}
		if ok {
			return p, nil
		}
	}
	return limen.DefaultProfile(limen.Scope{Org: s.Org}), nil
}

// Profile implements [limen.Admin].
func (e *Engine) Profile(ctx context.Context, s limen.Scope) (limen.Profile, error) {
	if err := ctx.Err(); err != nil {
		return limen.Profile{}, err
	}
	if s.Org == "" {
		return limen.Profile{}, &limen.Error{Code: limen.CodeInvalidQuery, Fields: []string{"org"}, Detail: "org is empty"}
	}
	return e.profile(ctx, s)
}

// PutProfile implements [limen.Admin].
func (e *Engine) PutProfile(ctx context.Context, p limen.Profile) (limen.Profile, error) {
	if err := ctx.Err(); err != nil {
		return limen.Profile{}, err
	}
	if err := p.Validate(); err != nil {
		return limen.Profile{}, err
	}
	t, err := p.Costs.Thresholds()
	if err != nil {
		return limen.Profile{}, err
	}
	p.Thresholds, p.Default = t, false
	if p.Certify == (limen.Certify{}) {
		p.Certify = limen.DefaultCertify
	}
	if err := e.st.PutProfile(ctx, p); err != nil {
		return limen.Profile{}, err
	}
	return p, nil
}

// Forget implements [limen.Admin].
func (e *Engine) Forget(ctx context.Context, org, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var p []string
	if org == "" {
		p = append(p, "org")
	}
	if subject == "" {
		p = append(p, "subject")
	}
	if len(p) > 0 {
		return &limen.Error{Code: limen.CodeInvalidQuery, Fields: p, Detail: "forget needs an org and a subject"}
	}
	defer e.lock(org, subject)()
	e.models.drop(subjectKey{org, subject})
	return e.st.Forget(ctx, org, subject)
}

// Replay implements [limen.Admin]. Records are predicted in the order of
// their times, each before its own answer is learned, by a scratch engine
// over a memory store that copies this engine's profiles and refits inline
// every eight answers. With Keep, the records' decisions and answers are
// then recorded in this engine, as if they had arrived live, and its models
// are fitted on them.
func (e *Engine) Replay(ctx context.Context, records iter.Seq2[limen.ReplayRecord, error], o limen.ReplayOptions) (limen.ReplayReport, error) {
	var recs []limen.ReplayRecord
	for r, err := range records {
		if err != nil {
			return limen.ReplayReport{}, err
		}
		if len(recs) == limen.MaxReplayRecords {
			return limen.ReplayReport{}, &limen.Error{Code: limen.CodeBodyTooLarge,
				Detail: fmt.Sprintf("more than %d records", limen.MaxReplayRecords)}
		}
		if err := r.Validate(); err != nil {
			var le *limen.Error
			if !errors.As(err, &le) {
				return limen.ReplayReport{}, err
			}
			prefix := "records[" + strconv.Itoa(len(recs)) + "]."
			fields := make([]string, len(le.Fields))
			for i, f := range le.Fields {
				fields[i] = prefix + f
			}
			return limen.ReplayReport{}, &limen.Error{Code: limen.CodeInvalidReplay, Fields: fields, Detail: prefix + " " + le.Detail}
		}
		recs = append(recs, r)
	}
	slices.SortStableFunc(recs, func(a, b limen.ReplayRecord) int { return a.At.Compare(b.At) })

	// The report comes from a scratch engine, so a replay's suggestions are
	// never mistaken for live ones; keep then adds the history itself to
	// this engine's ledger and models.
	mem := store.NewMemory()
	orgs := map[string]bool{}
	for _, r := range recs {
		if !orgs[r.Action.Org] {
			orgs[r.Action.Org] = true
			if err := copyProfiles(ctx, e, mem, r.Action.Org, recs); err != nil {
				return limen.ReplayReport{}, err
			}
		}
	}
	// Inline, so the scratch engine runs no fitter and needs no Close.
	scratch := build(Options{Store: mem, Inline: 8, MaxLabels: e.opts.MaxLabels})

	report := limen.ReplayReport{Records: len(recs), Would: map[limen.Verdict]int{}, Kept: o.Keep}
	var rows []estimate.Row
	for _, r := range recs {
		if err := ctx.Err(); err != nil {
			return limen.ReplayReport{}, err
		}
		p, err := scratch.predictAt(ctx, r.Action, r.At)
		if err != nil {
			return limen.ReplayReport{}, err
		}
		report.Would[p.Verdict]++
		if err := scratch.recordAt(ctx, decisionOf(r, false), r.At); err != nil {
			return limen.ReplayReport{}, err
		}
		row := estimate.Row{Verdict: p.Verdict, Review: r.ReviewProbability, Shown: r.Verdict.Shown()}
		if r.Approve != nil {
			report.Answered++
			if err := scratch.answerAt(ctx, limen.Answer{Key: r.Action.Key, Approve: *r.Approve}, r.At); err != nil {
				return limen.ReplayReport{}, err
			}
			row.Reviewed, row.Approve = row.Shown, *r.Approve
			switch {
			case p.Verdict == limen.Allow && !*r.Approve:
				report.AllowedDenied++
			case p.Verdict == limen.Block && *r.Approve:
				report.BlockedApproved++
			}
		}
		rows = append(rows, row)
	}
	report.FalseAllow = estimate.Rate(rows, estimate.FalseAllow)
	if !o.Keep {
		return report, nil
	}
	touched := map[subjectKey]bool{}
	for _, r := range recs {
		if err := e.recordAt(ctx, decisionOf(r, true), r.At); err != nil {
			return limen.ReplayReport{}, err
		}
		if r.Approve != nil {
			if err := e.answerAt(ctx, limen.Answer{Key: r.Action.Key, Approve: *r.Approve}, r.At); err != nil {
				return limen.ReplayReport{}, err
			}
		}
		touched[subjectKey{r.Action.Org, r.Action.Subject}] = true
	}
	// Fit what the replay taught now, rather than after the debounce, for
	// every subject it touched, cached or not.
	for k := range touched {
		if err := e.fitSubject(ctx, k); err != nil {
			return limen.ReplayReport{}, err
		}
	}
	return report, nil
}

// decisionOf is a replayed record's decision, carrying its action when the
// target holds no prediction for it.
func decisionOf(r limen.ReplayRecord, withAction bool) limen.Decision {
	d := limen.Decision{Key: r.Action.Key, Verdict: r.Verdict, ReviewProbability: r.ReviewProbability, Source: r.Source}
	if withAction {
		a := r.Action
		d.Action = &a
	}
	return d
}

// copyProfiles copies into dst every profile of the org that a record of
// the replay can resolve to.
func copyProfiles(ctx context.Context, e *Engine, dst store.Store, org string, recs []limen.ReplayRecord) error {
	seen := map[limen.Scope]bool{}
	for _, r := range recs {
		if r.Action.Org != org {
			continue
		}
		for _, c := range limen.ScopeOf(r.Action).Candidates() {
			if seen[c] {
				continue
			}
			seen[c] = true
			p, ok, err := e.st.Profile(ctx, c)
			if err != nil {
				return err
			}
			if ok {
				if err := dst.PutProfile(ctx, p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// snapshot is what the store keeps of a fitted model: its state and the
// number of answers it was fitted through, so a loaded model knows whether
// answers have arrived since.
type snapshot struct {
	State   gp.State
	Answers int64
}

func encodeSnapshot(s snapshot) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeSnapshot(b []byte) (snapshot, error) {
	var s snapshot
	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&s)
	return s, err
}
