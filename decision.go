// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import "math"

// Decision is what a decision point did with one action, recorded once,
// before the action runs or a person sees it.
//
// ReviewProbability is the probability, fixed before the action ran, that
// a person sees it: one for an [Ask], and for a [Flag] the decision point's
// rules force; the audit rate for an automatic verdict under random review,
// whether or not the draw picked it; zero when nothing samples it.
// latere.ai/x/pkg/verdict.Decide returns exactly this pair. An answer to an
// action seen with probability p counts in an error estimate with weight
// 1/p, which is why the probability comes from the decision point that
// drew it and from nobody else.
type Decision struct {
	Key
	// Verdict is the verdict applied.
	Verdict           Verdict `json:"verdict"`
	ReviewProbability float64 `json:"review_probability"`
	// Source names the policy that decided: "topos/progressive",
	// "rules/1", or a prediction's source when the decision point applied
	// Limen's suggestion unchanged. Reliability can be reported per source.
	Source string `json:"source"`
	// Action is required when Limen holds no prediction for the action,
	// for example after [FailClosed] stood in for an unreachable predictor,
	// so the record and the person's answer still teach the model. When a
	// prediction exists, an action sent here must be the same one.
	Action *Action `json:"action,omitzero"`
}

// Validate reports every field of d that breaks the contract, as one
// [*Error] with [CodeInvalidDecision], or nil.
func (d Decision) Validate() error {
	var p problems
	d.check(&p, "")
	if !d.Verdict.Valid() {
		p.add("verdict", "is not one of allow, flag, ask, block")
	}
	checkReview(&p, "", d.Verdict, d.ReviewProbability)
	if d.Source == "" {
		p.add("source", "is empty")
	}
	if d.Action != nil {
		d.Action.check(&p, "action.")
		if d.Action.Key != d.Key {
			p.add("action", "names a different org, subject or action_id")
		}
	}
	return p.err(CodeInvalidDecision)
}

// checkReview holds a verdict and its review probability to each other: a
// person sees a [Flag] or an [Ask], so its probability is positive; nobody
// sees an [Allow] or a [Block], so its probability is below one.
func checkReview(p *problems, at string, v Verdict, prob float64) {
	switch {
	case math.IsNaN(prob) || prob < 0 || prob > 1:
		p.add(at+"review_probability", "is outside [0, 1]")
	case v.Valid() && v.Shown() && prob == 0:
		p.add(at+"review_probability", "is zero for a verdict a person sees")
	case v.Valid() && !v.Shown() && prob == 1:
		p.add(at+"review_probability", "is one for a verdict nobody sees")
	}
}
