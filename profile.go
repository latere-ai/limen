// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"errors"
	"math"
)

// Profile is what an organization states about errors and interruptions.
// The thresholds follow from it; nobody tunes them.
//
// A profile's id is the organization's id, optionally followed by
// "/agents/" and an agent id for a profile that applies to one agent:
// "org_7" and "org_7/agents/agt_ci". The most specific profile wins.
type Profile struct {
	ID    string `json:"id"`
	Costs Costs  `json:"costs"`
	// AuditRate is the share of automatic verdicts sampled for review. A
	// rate of zero leaves the false-allow rate unknowable, which a
	// reliability report says rather than reporting zero.
	AuditRate float64 `json:"audit_rate"`
	// Certify is the claim reliability reports test. A zero value takes
	// [DefaultCertify] when the profile is saved.
	Certify Certify `json:"certify,omitzero"`
	// Thresholds is derived from Costs when the profile is saved; a value
	// sent by a caller is ignored.
	Thresholds Thresholds `json:"thresholds"`
}

// Costs are the three prices of the decision, in any one unit. Only their
// ratios matter.
type Costs struct {
	// FalseAllow is the cost of running a call the person would deny.
	FalseAllow float64 `json:"false_allow"`
	// FalseBlock is the cost of refusing a call the person would approve.
	FalseBlock float64 `json:"false_block"`
	// Ask is the cost of one interruption.
	Ask float64 `json:"ask"`
}

// Certify is the claim a reliability report tests: a false-allow rate
// below Alpha, at confidence 1 - Delta.
type Certify struct {
	Alpha float64 `json:"alpha"`
	Delta float64 `json:"delta"`
}

// DefaultCertify is the claim a profile that states none is tested
// against: a false-allow rate below 1% at 95% confidence, which takes 300
// consecutive clean audits.
var DefaultCertify = Certify{Alpha: 0.01, Delta: 0.05}

// errEmptyBand is wrapped into the [CodeEmptyAskBand] error so a test can
// match the reason without parsing text.
var errEmptyBand = errors.New("c_ask(c_FA + c_FB) >= c_FA c_FB")

// Thresholds derives the verdict thresholds from the costs. With p the
// probability of approval, allowing costs (1-p) FalseAllow, blocking costs
// p FalseBlock, and asking costs Ask, so the expected cost is least for
// allow when p > 1 - Ask/FalseAllow and for block when p < Ask/FalseBlock
// (Chow's reject option). In score units, score = 1 - p:
//
//	AskAt   = Ask / FalseAllow
//	BlockAt = 1 - Ask / FalseBlock
//
// The ask band is non-empty exactly when Ask (FalseAllow + FalseBlock) <
// FalseAllow FalseBlock. Otherwise an automatic verdict is never worse than
// asking, and Thresholds refuses the costs with [CodeEmptyAskBand].
func (c Costs) Thresholds() (Thresholds, error) {
	var p problems
	for _, f := range []struct {
		name string
		v    float64
	}{{"costs.false_allow", c.FalseAllow}, {"costs.false_block", c.FalseBlock}, {"costs.ask", c.Ask}} {
		if !(f.v > 0) || math.IsInf(f.v, 0) {
			p.add(f.name, "is not a positive finite number")
		}
	}
	if err := p.err(CodeInvalidProfile); err != nil {
		return Thresholds{}, err
	}
	if c.Ask*(c.FalseAllow+c.FalseBlock) >= c.FalseAllow*c.FalseBlock {
		return Thresholds{}, &Error{Code: CodeEmptyAskBand, Fields: []string{"costs"}, Detail: errEmptyBand.Error()}
	}
	return Thresholds{AskAt: c.Ask / c.FalseAllow, BlockAt: 1 - c.Ask/c.FalseBlock}, nil
}

// Validate reports every field of p that breaks the contract, or nil. A
// profile whose costs leave no ask band fails with [CodeEmptyAskBand];
// every other problem is [CodeInvalidProfile].
func (p Profile) Validate() error {
	var q problems
	if p.ID == "" {
		q.add("id", "is empty")
	}
	if !unit(p.AuditRate) {
		q.add("audit_rate", "is outside [0, 1]")
	}
	if p.Certify != (Certify{}) {
		if _, err := CertificationSize(p.Certify.Alpha, p.Certify.Delta); err != nil {
			q.add("certify", "needs 0 < alpha <= 1 and 0 < delta < 1")
		}
	}
	if err := q.err(CodeInvalidProfile); err != nil {
		return err
	}
	_, err := p.Costs.Thresholds()
	return err
}

// CertificationSize is the number of consecutive clean audits that
// certify a false-allow rate below alpha at confidence 1 - delta: if the
// rate were at least alpha, n independent audits would all be clean with
// probability at most (1-alpha)^n <= exp(-alpha n), which is at most delta
// once n >= ln(1/delta)/alpha. For alpha = 0.01 and delta = 0.05 it is 300.
func CertificationSize(alpha, delta float64) (int, error) {
	if !(alpha > 0 && alpha <= 1 && delta > 0 && delta < 1) {
		return 0, &Error{Code: CodeInvalidProfile, Fields: []string{"certify"}, Detail: "need 0 < alpha <= 1 and 0 < delta < 1"}
	}
	return int(math.Ceil(math.Log(1/delta) / alpha)), nil
}
