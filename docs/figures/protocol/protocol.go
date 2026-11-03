// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package protocol runs Limen's model against one simulated person, in the
// prequential protocol of the paper's experiments (github.com/changkun/
// trustcalib, experiment/gateway.py run_gateway with query_strategy
// "escalate"): each action is predicted and decided before its answer
// exists, an ask is shown to the person, an allow or a block is shown only
// when the review draw selects it, and each shown action's answer joins
// the training set.
//
// The model is Limen's, not the research's: engine/gp with
// kernel.DefaultAdditive over the person's feature points, refitted after
// every answer with gp.Options{} (so on the most recent
// gp.DefaultMaxLabels answers); the verdict is Limen's
// limen.Thresholds.Verdict, and the review draw is the decision point's
// verdict.Decide, as a decision point that uses Limen applies them.
package protocol

import (
	"fmt"
	"math/rand/v2"

	"latere.ai/x/limen"
	"latere.ai/x/limen/docs/figures/person"
	"latere.ai/x/limen/engine/gp"
	"latere.ai/x/limen/engine/kernel"
	"latere.ai/x/pkg/verdict"
)

// Options set one run. The zero value of a field is not a default; use
// [Paper].
type Options struct {
	Decisions  int              // the length of the person's history
	Kernel     kernel.Kernel    // the model's covariance
	Thresholds limen.Thresholds // the profile's verdict thresholds
	AuditRate  float64          // the share of automatic verdicts shown at random
	Oracle     person.Oracle    // the person
	// StreamSeed is the seed of person s's actions and Seed the seed of
	// the review draws and the person's answers, as experiment/run.py
	// seeds make_stream with 1000 + s and the gateway with s.
	StreamSeed uint64
	Seed       uint64
}

// Paper is the setup of the figures for person s: 1500 decisions, the
// paper's oracle, Limen's additive kernel, and Limen's built-in Careful
// profile (limen.DefaultCosts: allow above 0.90, block below 0.25, a
// twentieth of automatic verdicts reviewed), which is the paper's SAFETY
// operating point (experiment/gateway.py SAFETY, c_FA 10, c_FB 4).
func Paper(s int) Options {
	th, err := limen.DefaultCosts.Thresholds()
	if err != nil {
		panic(err)
	}
	return Options{
		Decisions: 1500, Kernel: kernel.DefaultAdditive(), Thresholds: th,
		AuditRate: limen.DefaultAuditRate, Oracle: person.Paper,
		StreamSeed: 1000 + uint64(s), Seed: uint64(s),
	}
}

// Record is one decision of the run.
type Record struct {
	Decision person.Decision
	// PHat is Limen's approval probability before the answer, PStar the
	// person's true one, Phi(f*).
	PHat, PStar float64
	// Verdict is the verdict the thresholds give PHat, and Applied the one
	// verdict.Decide applies: an audited allow becomes a flag and an
	// audited block an ask.
	Verdict, Applied verdict.Verdict
	// Shown is true when the person saw the action and answered it; Approve
	// is the answer then.
	Shown, Approve bool
	// Vetoed is the oracle's safety conjunction at the decision.
	Vetoed bool
	// Answers is the number of answers the model was fitted on when it
	// predicted PHat.
	Answers int
}

// Run returns the records of one person's history, in time order.
func Run(o Options) ([]Record, error) {
	rng := rand.New(rand.NewPCG(o.Seed, 1))
	stream := person.Stream(o.Decisions, o.StreamSeed)
	post := gp.Prior(o.Kernel)
	var (
		points  []kernel.Point
		answers []bool
		out     = make([]Record, 0, len(stream))
	)
	for _, d := range stream {
		pt := d.Point()
		pHat := post.Predict(pt).Approve
		suggested := o.Thresholds.Verdict(pHat)
		// One uniform draw per action, never redrawn (verdict.Decide).
		applied, _ := verdict.Decide(suggested, verdict.Allow, o.AuditRate, rng.Float64())
		pStar := o.Oracle.Approve(d)
		r := Record{Decision: d, PHat: pHat, PStar: pStar, Verdict: suggested, Applied: applied,
			Vetoed: o.Oracle.Vetoed(d), Answers: post.Len()}
		if applied.Shown() {
			// The person answers y ~ Bernoulli(Phi(f*)),
			// experiment/oracle.py sample_label.
			r.Shown, r.Approve = true, rng.Float64() < pStar
			points, answers = append(points, pt), append(answers, r.Approve)
			var err error
			if post, err = gp.Fit(o.Kernel, points, answers, gp.Options{}); err != nil {
				return nil, fmt.Errorf("protocol: fit at t=%d: %w", d.T, err)
			}
		}
		out = append(out, r)
	}
	return out, nil
}
