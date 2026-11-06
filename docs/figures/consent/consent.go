// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package consent runs Limen itself, the engine with its featurizer and
// its cover rule (latere.ai/x/limen/engine, spec 011), against a simulated
// person whose actions differ in scope: shell commands and publishes of an
// agent, across sessions, answered with and without the details opened.
// Every decision is one engine.Predict call for a new action, recorded
// and, when shown, answered, as a decision point that uses Limen does it.
//
// The person is not the paper's: the paper's oracle has no scope. Each
// kind of action has a fixed approval probability, stated in Kinds.
package consent

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"latere.ai/x/pkg/verdict"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
)

// Kind is one kind of action the agent proposes, with how often it comes
// and how likely the person approves it.
type Kind struct {
	Name    string
	Weight  float64 // relative frequency in the stream
	Approve float64 // the person's approval probability
	action  func(id string) limen.Action
}

// The claims a hosted decision point sends with a publish, as Limen's
// engine tests write them (engine/consent_test.go): an update can be undone
// from a copy and stays private; access for anyone cannot be undone and is
// public.
var (
	updateClaim = limen.ReachClaim{Undo: limen.UndoRecoverable, Contact: limen.ContactRepeat,
		Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundNo}}
	publicClaim = limen.ReachClaim{Undo: limen.UndoIrreversible, Contact: limen.ContactRepeat,
		Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundYes}}
)

func bash(command string) func(string) limen.Action {
	in, _ := json.Marshal(map[string]string{"command": command})
	return func(id string) limen.Action {
		return limen.Action{ActionID: id, Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in,
			Environment: &limen.Environment{Kind: "sandbox"}}
	}
}

func publish(claim limen.ReachClaim) func(string) limen.Action {
	return func(id string) limen.Action {
		c := claim
		return limen.Action{ActionID: id, Kind: "tool", Name: "publish", Effect: limen.EffectExternal,
			Environment: &limen.Environment{Kind: "host", Credentialed: true},
			Target:      &limen.Target{Kind: "app", Class: limen.ClassOwn}, Scope: &c}
	}
}

// Kinds are the person's actions: the routine ones of narrow scope that a
// person approves nearly always, and wider ones of the same families that
// the person approves less often.
var Kinds = []Kind{
	{"go test", 6, 0.99, bash("go test ./...")},
	{"git push to a feature branch", 3, 0.96, bash("git push origin feature/figures")},
	{"git push to main", 1.5, 0.75, bash("git push origin main")},
	{"git push --force to main", 0.5, 0.1, bash("git push --force origin main")},
	{"publish: update", 3, 0.97, publish(updateClaim)},
	{"publish: make public", 1, 0.45, publish(publicClaim)},
}

// Options set one person's run.
type Options struct {
	Decisions int
	// Session is the number of decisions in one session of the agent.
	Session int
	// Opened is the probability that the person opens the details before
	// an answer.
	Opened    float64
	Costs     limen.Costs
	AuditRate float64
	Seed      uint64
}

// Default is the setup of the cover figure for person s.
func Default(s int) Options {
	return Options{Decisions: 600, Session: 40, Opened: 0.5, Costs: limen.DefaultCosts,
		AuditRate: limen.DefaultAuditRate, Seed: uint64(s)}
}

// Record is one decision.
type Record struct {
	T               int
	Kind            string
	Session         int
	Approve         float64 // Limen's approval probability
	Truth           float64 // the person's
	Suggested       verdict.Verdict
	AskKind         string
	Weight          float64 // the cover's weight
	Sessions        int     // the cover's sessions
	Covered         bool
	Applied         verdict.Verdict
	Shown, Approved bool
	Opened          bool
	AnswerMS        int64
}

// Run returns one person's history.
func Run(o Options) ([]Record, error) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(o.Seed, 2))
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	e := engine.New(engine.Options{Inline: 1, Clock: func() time.Time { return now }})
	defer e.Close()
	const org, subject = "simulation", "person"
	if _, err := e.PutProfile(ctx, limen.Profile{Scope: limen.Scope{Org: org}, Costs: o.Costs, AuditRate: o.AuditRate}); err != nil {
		return nil, err
	}
	total := 0.0
	for _, k := range Kinds {
		total += k.Weight
	}
	out := make([]Record, 0, o.Decisions)
	for t := range o.Decisions {
		u := rng.Float64() * total
		k := Kinds[len(Kinds)-1]
		for _, c := range Kinds {
			if u < c.Weight {
				k = c
				break
			}
			u -= c.Weight
		}
		session := t / o.Session
		a := k.action(fmt.Sprintf("a-%d", t))
		a.Org, a.Subject = org, subject
		a.Actor = &limen.Actor{Kind: "agent", ID: "agent", Session: fmt.Sprintf("s-%d", session)}
		now = now.Add(time.Minute)
		p, err := e.Predict(ctx, a)
		if err != nil {
			return nil, err
		}
		applied, review := verdict.Decide(p.Verdict, verdict.Allow, o.AuditRate, rng.Float64())
		if err := e.Record(ctx, limen.Decision{Key: a.Key, Verdict: applied, ReviewProbability: review, Source: "simulation"}); err != nil {
			return nil, err
		}
		r := Record{T: t, Kind: k.Name, Session: session, Approve: p.Approve, Truth: k.Approve, Suggested: p.Verdict,
			AskKind: p.AskKind, Weight: p.Cover.Weight, Sessions: p.Cover.Sessions, Covered: p.Cover.Covered, Applied: applied}
		if applied.Shown() {
			r.Shown, r.Approved = true, rng.Float64() < k.Approve
			r.Opened = rng.Float64() < o.Opened
			// Answer times are log-normal around 3 s: some under the
			// 2 s floor, which with the details unopened weigh nothing.
			r.AnswerMS = int64(math.Round(3000 * math.Exp(0.6*rng.NormFloat64())))
			now = now.Add(time.Duration(r.AnswerMS) * time.Millisecond)
			how := limen.HowAnswered{DetailsOpened: &r.Opened, AnswerMS: &r.AnswerMS}
			if err := e.Answer(ctx, limen.Answer{Key: a.Key, Approve: r.Approved, HowAnswered: how}); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	return out, nil
}
