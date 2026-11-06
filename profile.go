// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// Scope is what a cost profile applies to: an organization, optionally
// narrowed to one kind of action, one agent, or both. For an action, the
// most specific saved profile applies, in the order kind and agent, agent,
// kind, the organization, and then [DefaultProfile].
//
// A scope with Subject is one person's own profile in the organization
// (spec 010). It has no kind and no agent, and it applies to every action
// of that person there, only ever to make the organization's profile
// stricter: the profile that applies to an action of the person is
// [Stricter] of the organization's and the person's.
type Scope struct {
	Org   string `json:"org"`
	Kind  string `json:"kind,omitempty"`
	Agent string `json:"agent,omitempty"`
	// Subject names the person whose own profile this is. In the context
	// of a person, an org that holds a pipe, it is that org.
	Subject string `json:"subject,omitempty"`
}

// Validate reports every field of s that breaks the contract, as one
// [*Error] with [CodeInvalidQuery], or nil: a scope named in a query, to
// read or remove the profile saved for it.
func (s Scope) Validate() error {
	var p problems
	s.check(&p)
	return p.err(CodeInvalidQuery)
}

// check holds a scope to its rules: an org, and for a person's own
// profile no kind, no agent, and in the context of a person that person.
func (s Scope) check(p *problems) {
	if s.Org == "" {
		p.add("org", "is empty")
	}
	if s.Subject == "" {
		return
	}
	if s.Kind != "" {
		p.add("kind", "is set beside subject; a person has one profile in an organization")
	}
	if s.Agent != "" {
		p.add("agent", "is set beside subject; a person has one profile in an organization")
	}
	if strings.Contains(s.Org, "|") && s.Subject != s.Org {
		p.add("subject", "is not org in the context of a person")
	}
}

// ScopeOf is the most specific scope an action can match: its
// organization, its kind, and its actor's id when the actor is an agent.
func ScopeOf(a Action) Scope {
	s := Scope{Org: a.Org, Kind: a.Kind}
	if a.Actor != nil && a.Actor.Kind == "agent" {
		s.Agent = a.Actor.ID
	}
	return s
}

// Candidates are the scopes whose saved profile applies within s, most
// specific first: kind and agent, agent, kind, then the organization. The
// first one with a saved profile applies, and [DefaultProfile] when none
// has one. A candidate repeated because s leaves a field empty is listed
// once. No candidate has a subject: a person's own profile is read apart
// and met with the organization's ([Stricter]).
func (s Scope) Candidates() []Scope {
	out := make([]Scope, 0, 4)
	for _, c := range []Scope{
		{Org: s.Org, Kind: s.Kind, Agent: s.Agent},
		{Org: s.Org, Agent: s.Agent},
		{Org: s.Org, Kind: s.Kind},
		{Org: s.Org},
	} {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// Profile is what an organization states about errors and interruptions
// for the actions in its scope. The thresholds follow from the costs;
// nobody tunes them.
type Profile struct {
	Scope
	Costs Costs `json:"costs"`
	// AuditRate is the share of automatic verdicts a person reviews at
	// random. At zero, the false-allow rate of automatic verdicts cannot be
	// estimated, and a reliability report says so rather than reporting
	// zero.
	AuditRate float64 `json:"audit_rate"`
	// Certify is the claim reliability reports test. A zero value takes
	// [DefaultCertify] when the profile is saved.
	Certify Certify `json:"certify,omitzero"`
	// Consent sets the cover rule for the actions in the scope. It can
	// only make the settings of the installation greater.
	Consent Consent `json:"consent,omitzero"`
	// Thresholds is derived from Costs when the profile is saved; a value
	// a caller sends is ignored.
	Thresholds Thresholds `json:"thresholds"`
	// Default is true for the built-in profile Limen applies to a scope
	// with no saved profile. A caller cannot set it.
	Default bool `json:"default,omitzero"`
}

// Costs are the three prices of a decision, in any one unit. Only their
// ratios matter.
type Costs struct {
	// FalseAllow is the cost of running an action the person would deny.
	FalseAllow float64 `json:"false_allow"`
	// FalseBlock is the cost of refusing an action the person would approve.
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

// The built-in profile: a wrong allow costs ten interruptions and a wrong
// block four, so Limen suggests an allow only above a 90% chance of
// approval and a block only below 25%; a twentieth of automatic verdicts
// is reviewed; and the claim tested is a false-allow rate below 1% at 95%
// confidence, which takes 300 clean reviews.
var (
	DefaultCosts     = Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}
	DefaultAuditRate = 0.05
	DefaultCertify   = Certify{Alpha: 0.01, Delta: 0.05}
)

// DefaultProfile is the profile Limen applies to a scope with no saved
// profile.
func DefaultProfile(s Scope) Profile {
	t, _ := DefaultCosts.Thresholds()
	return Profile{Scope: s, Costs: DefaultCosts, AuditRate: DefaultAuditRate, Certify: DefaultCertify, Thresholds: t, Default: true}
}

// Thresholds derives the verdict thresholds from the costs. At approval
// probability p, allowing costs (1-p) FalseAllow, blocking costs
// p FalseBlock, and asking costs Ask, so the expected cost is least for
// allow when p > 1 - Ask/FalseAllow and for block when p < Ask/FalseBlock
// (Chow's reject option):
//
//	AllowAbove = 1 - Ask/FalseAllow
//	BlockBelow = Ask/FalseBlock
//
// Limen also requires the ask band to contain one half, the probability it
// assigns to an action it knows nothing about, so that no action is
// allowed or blocked on no evidence. That holds exactly when each error
// costs more than two interruptions: FalseAllow > 2 Ask and
// FalseBlock > 2 Ask. Costs that break it are refused with
// [CodeAskTooCostly]; costs that are not positive and finite with
// [CodeInvalidProfile].
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
	if !(c.FalseAllow > 2*c.Ask) {
		p.add("costs.false_allow", "is not more than twice costs.ask")
	}
	if !(c.FalseBlock > 2*c.Ask) {
		p.add("costs.false_block", "is not more than twice costs.ask")
	}
	if err := p.err(CodeAskTooCostly); err != nil {
		return Thresholds{}, err
	}
	return Thresholds{AllowAbove: 1 - c.Ask/c.FalseAllow, BlockBelow: c.Ask / c.FalseBlock}, nil
}

// Validate reports every field of p that breaks the contract, or nil.
// Costs that would let Limen decide on no evidence fail with
// [CodeAskTooCostly]; every other problem is [CodeInvalidProfile].
func (p Profile) Validate() error {
	var q problems
	p.check(&q)
	if !unit(p.AuditRate) {
		q.add("audit_rate", "is outside [0, 1]")
	}
	if p.Certify != (Certify{}) {
		if _, err := CertificationSize(p.Certify.Alpha, p.Certify.Delta); err != nil {
			q.add("certify", "needs 0 < alpha <= 1 and 0 < delta < 1")
		}
	}
	if p.Consent.MinWeight < 0 {
		q.add("consent.min_weight", "is below 0")
	}
	if p.Consent.MinSessions < 0 {
		q.add("consent.min_sessions", "is below 0")
	}
	if err := q.err(CodeInvalidProfile); err != nil {
		return err
	}
	_, err := p.Costs.Thresholds()
	return err
}

// Stricter is the meet of the organization's profile and a person's own:
// each number from the profile that is stricter for it (spec 010). The
// upper threshold is the higher one, the lower threshold the lower one,
// the audit rate the higher one, the claim the lower alpha and the lower
// delta, and each setting of consent the greater one (spec 011). The costs are restated with an ask of one, each error at
// the higher of the two ratios to the ask: the upper threshold
// 1 - ask/false_allow rises and the lower threshold ask/false_block falls
// with its ratio, so the higher ratios give the stricter thresholds
// (paper, Proposition 2), and the meet's costs give its thresholds to
// rounding. Each accepted profile has both ratios above two, so the meet's
// do too, and its ask band holds one half.
//
// When the person's profile is stricter in no number, Stricter returns
// org unchanged. Otherwise the scope is org's with the person's Subject,
// and the meet is never the default.
func Stricter(org, person Profile) Profile {
	oc, pc := certifyOr(org.Certify), certifyOr(person.Certify)
	out := org
	out.Certify = oc
	fa, fb := org.Costs.FalseAllow/org.Costs.Ask, org.Costs.FalseBlock/org.Costs.Ask
	changed := false
	if person.Thresholds.AllowAbove > org.Thresholds.AllowAbove {
		out.Thresholds.AllowAbove = person.Thresholds.AllowAbove
		fa = person.Costs.FalseAllow / person.Costs.Ask
		changed = true
	}
	if person.Thresholds.BlockBelow < org.Thresholds.BlockBelow {
		out.Thresholds.BlockBelow = person.Thresholds.BlockBelow
		fb = person.Costs.FalseBlock / person.Costs.Ask
		changed = true
	}
	if person.AuditRate > org.AuditRate {
		out.AuditRate = person.AuditRate
		changed = true
	}
	if pc.Alpha < oc.Alpha {
		out.Certify.Alpha = pc.Alpha
		changed = true
	}
	if pc.Delta < oc.Delta {
		out.Certify.Delta = pc.Delta
		changed = true
	}
	if person.Consent.MinWeight > org.Consent.MinWeight {
		out.Consent.MinWeight = person.Consent.MinWeight
		changed = true
	}
	if person.Consent.MinSessions > org.Consent.MinSessions {
		out.Consent.MinSessions = person.Consent.MinSessions
		changed = true
	}
	if !changed {
		return org
	}
	out.Costs = Costs{FalseAllow: fa, FalseBlock: fb, Ask: 1}
	out.Subject = person.Subject
	out.Default = false
	return out
}

// certifyOr is c, or [DefaultCertify] for a zero claim, which a saved
// profile takes.
func certifyOr(c Certify) Certify {
	if c == (Certify{}) {
		return DefaultCertify
	}
	return c
}

// CertificationSize is the number of consecutive clean reviews that
// certify a false-allow rate below alpha at confidence 1 - delta: if the
// rate were at least alpha, n independent reviews would all be clean with
// probability at most (1-alpha)^n <= exp(-alpha n), which is at most delta
// once n >= ln(1/delta)/alpha. For alpha = 0.01 and delta = 0.05 it is 300.
func CertificationSize(alpha, delta float64) (int, error) {
	if !(alpha > 0 && alpha <= 1 && delta > 0 && delta < 1) {
		return 0, &Error{Code: CodeInvalidProfile, Fields: []string{"certify"},
			Detail: "alpha " + strconv.FormatFloat(alpha, 'g', -1, 64) + ", delta " + strconv.FormatFloat(delta, 'g', -1, 64) + ": need 0 < alpha <= 1 and 0 < delta < 1"}
	}
	return int(math.Ceil(math.Log(1/delta) / alpha)), nil
}
