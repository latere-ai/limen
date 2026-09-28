// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

func TestLeast(t *testing.T) {
	cases := []struct {
		in   []Verdict
		want Verdict
	}{
		{nil, Block},
		{[]Verdict{Allow}, Allow},
		{[]Verdict{Allow, Flag}, Flag},
		{[]Verdict{Ask, Allow}, Ask},
		{[]Verdict{Allow, Block, Ask}, Block},
		{[]Verdict{Allow, "maybe"}, Block},
		{[]Verdict{""}, Block},
	}
	for _, c := range cases {
		if got := Least(c.in...); got != c.want {
			t.Errorf("Least(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVerdictValid(t *testing.T) {
	for _, v := range []Verdict{Allow, Flag, Ask, Block} {
		if !v.Valid() {
			t.Errorf("%q is not valid", v)
		}
	}
	for _, v := range []Verdict{"", "deny", "ALLOW"} {
		if v.Valid() {
			t.Errorf("%q is valid", v)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// The two profiles of the research: the symmetric band (0.35, 0.65), and
// the safety-weighted costs 10, 4, 1 with band (0.25, 0.90).
func TestCostsThresholds(t *testing.T) {
	cases := []struct {
		name             string
		c                Costs
		askAt, blockAt   float64
		bandLow, bandHig float64
	}{
		{"symmetric", Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}, 0.35, 0.65, 0.35, 0.65},
		{"safety", Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}, 0.10, 0.75, 0.25, 0.90},
		{"scale free", Costs{FalseAllow: 100, FalseBlock: 40, Ask: 10}, 0.10, 0.75, 0.25, 0.90},
	}
	for _, c := range cases {
		th, err := c.c.Thresholds()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !near(th.AskAt, c.askAt) || !near(th.BlockAt, c.blockAt) {
			t.Errorf("%s: thresholds %+v, want ask_at %v block_at %v", c.name, th, c.askAt, c.blockAt)
		}
		lo, hi := th.Band()
		if !near(lo, c.bandLow) || !near(hi, c.bandHig) {
			t.Errorf("%s: band (%v, %v), want (%v, %v)", c.name, lo, hi, c.bandLow, c.bandHig)
		}
	}
}

func TestCostsThresholdsRefuses(t *testing.T) {
	// Ask (FA + FB) >= FA FB: 1 * (2 + 2) >= 4, the band is a single point.
	_, err := Costs{FalseAllow: 2, FalseBlock: 2, Ask: 1}.Thresholds()
	if CodeOf(err) != CodeEmptyAskBand {
		t.Errorf("empty band: code %q (%v), want %q", CodeOf(err), err, CodeEmptyAskBand)
	}
	for _, c := range []Costs{
		{FalseAllow: 0, FalseBlock: 4, Ask: 1},
		{FalseAllow: 10, FalseBlock: -1, Ask: 1},
		{FalseAllow: 10, FalseBlock: 4, Ask: math.NaN()},
		{FalseAllow: math.Inf(1), FalseBlock: 4, Ask: 1},
	} {
		_, err := c.Thresholds()
		if CodeOf(err) != CodeInvalidProfile {
			t.Errorf("%+v: code %q, want %q", c, CodeOf(err), CodeInvalidProfile)
		}
	}
}

// Thresholds.Verdict is Chow's rule: allow strictly below ask_at, block
// strictly above block_at, ask on both thresholds and between.
func TestThresholdsVerdict(t *testing.T) {
	th := Thresholds{AskAt: 0.10, BlockAt: 0.75}
	cases := []struct {
		score float64
		want  Verdict
	}{
		{0, Allow}, {0.09, Allow}, {0.10, Ask}, {0.42, Ask}, {0.75, Ask},
		{0.76, Block}, {1, Block}, {-0.1, Ask}, {1.1, Ask}, {math.NaN(), Ask},
	}
	for _, c := range cases {
		if got := th.Verdict(c.score); got != c.want {
			t.Errorf("Verdict(%v) = %q, want %q", c.score, got, c.want)
		}
	}
}

// The rule agrees with the minimum expected cost it is derived from, on a
// grid of approval probabilities away from the two thresholds.
func TestThresholdsVerdictIsBayesOptimal(t *testing.T) {
	c := Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}
	th, err := c.Thresholds()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 1000; i++ {
		p := float64(i) / 1000
		costs := map[Verdict]float64{Allow: (1 - p) * c.FalseAllow, Block: p * c.FalseBlock, Ask: c.Ask}
		best := Allow
		for _, v := range []Verdict{Ask, Block} {
			if costs[v] < costs[best] {
				best = v
			}
		}
		got := th.Verdict(1 - p)
		if costs[got] > costs[best]+1e-9 {
			t.Errorf("p=%v: verdict %q costs %v, %q costs %v", p, got, costs[got], best, costs[best])
		}
	}
}

func TestCertificationSize(t *testing.T) {
	cases := []struct {
		alpha, delta float64
		want         int
	}{
		{0.01, 0.05, 300}, {0.25, 0.05, 12}, {1, 0.5, 1},
	}
	for _, c := range cases {
		n, err := CertificationSize(c.alpha, c.delta)
		if err != nil || n != c.want {
			t.Errorf("CertificationSize(%v, %v) = %d, %v; want %d", c.alpha, c.delta, n, err, c.want)
		}
		// n clean audits certify: a rate of alpha would leave them all clean
		// with probability at most delta.
		if p := math.Pow(1-c.alpha, float64(n)); p > c.delta {
			t.Errorf("(1-%v)^%d = %v > delta %v", c.alpha, n, p, c.delta)
		}
	}
	for _, bad := range [][2]float64{{0, 0.05}, {1.5, 0.05}, {0.01, 0}, {0.01, 1}, {math.NaN(), 0.05}} {
		if _, err := CertificationSize(bad[0], bad[1]); CodeOf(err) != CodeInvalidProfile {
			t.Errorf("CertificationSize(%v, %v): code %q", bad[0], bad[1], CodeOf(err))
		}
	}
}

func sens(x float64) *float64 { return &x }

func validAction() Action {
	return Action{
		Subject: "https://issuer.example|usr_42", Org: "org_7", Agent: "agt_ci", Session: "ses_9f",
		CallID: "toolu_01", Tool: "bash", Effect: EffectExternal,
		Input:   json.RawMessage(`{"command":"git push origin main"}`),
		Target:  &Target{Kind: "ref", ID: "refs/heads/main", Sensitivity: sens(0.9)},
		Machine: Machine{Kind: MachineSandbox, Credentialed: true},
		Signals: []Signal{{Source: "rules/1", Score: sens(0.4)}, {Source: "judge/claude-auto-mode", Verdict: Allow}},
		Ceiling: Allow,
		At:      time.Date(2026, 9, 28, 10, 2, 11, 0, time.UTC),
	}
}

func TestActionValidate(t *testing.T) {
	if err := validAction().Validate(); err != nil {
		t.Fatalf("valid action: %v", err)
	}
	cases := []struct {
		field string
		edit  func(*Action)
	}{
		{"subject", func(a *Action) { a.Subject = "" }},
		{"org", func(a *Action) { a.Org = "" }},
		{"call_id", func(a *Action) { a.CallID = "" }},
		{"tool", func(a *Action) { a.Tool = "" }},
		{"effect", func(a *Action) { a.Effect = "delete" }},
		{"input", func(a *Action) { a.Input = json.RawMessage(`{`) }},
		{"target.kind", func(a *Action) { a.Target.Kind = "" }},
		{"target.sensitivity", func(a *Action) { a.Target.Sensitivity = sens(1.5) }},
		{"machine.kind", func(a *Action) { a.Machine.Kind = "vm" }},
		{"signals[0].source", func(a *Action) { a.Signals[0].Source = "" }},
		{"signals[1].verdict", func(a *Action) { a.Signals[1].Verdict = Ask }},
		{"signals[0].score", func(a *Action) { a.Signals[0].Score = sens(-1) }},
		{"signals[1]", func(a *Action) { a.Signals[1].Verdict = "" }},
		{"ceiling", func(a *Action) { a.Ceiling = "" }},
		{"at", func(a *Action) { a.At = time.Time{} }},
	}
	for _, c := range cases {
		a := validAction()
		c.edit(&a)
		err := a.Validate()
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeInvalidAction {
			t.Errorf("%s: %v, want %s", c.field, err, CodeInvalidAction)
			continue
		}
		if !slices.Contains(e.Fields, c.field) {
			t.Errorf("%s: fields %v", c.field, e.Fields)
		}
	}
	// Every problem is reported in one pass.
	err := (Action{}).Validate()
	var e *Error
	if !errors.As(err, &e) || len(e.Fields) < 7 {
		t.Errorf("empty action: %v", err)
	}
}

func TestAnswerValidate(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 3, 0, 0, time.UTC)
	for _, a := range []Answer{{Approve: true, At: at}, {Approve: false, Kind: AnswerContest, At: at}} {
		if err := a.Validate(); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	for _, a := range []Answer{{Approve: true}, {Kind: "appeal", At: at}} {
		if CodeOf(a.Validate()) != CodeInvalidAnswer {
			t.Errorf("%+v: %v", a, a.Validate())
		}
	}
}

func TestProfileValidate(t *testing.T) {
	good := Profile{ID: "org_7", Costs: Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}, AuditRate: 0.05, Certify: Certify{Alpha: 0.01, Delta: 0.05}}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid profile: %v", err)
	}
	unstated := good
	unstated.Certify = Certify{}
	if err := unstated.Validate(); err != nil {
		t.Errorf("a profile that states no certify: %v", err)
	}
	if n, err := CertificationSize(DefaultCertify.Alpha, DefaultCertify.Delta); err != nil || n != 300 {
		t.Errorf("the default claim takes %d audits, %v", n, err)
	}
	cases := []struct {
		name string
		edit func(*Profile)
		code Code
	}{
		{"id", func(p *Profile) { p.ID = "" }, CodeInvalidProfile},
		{"audit rate", func(p *Profile) { p.AuditRate = 1.2 }, CodeInvalidProfile},
		{"certify", func(p *Profile) { p.Certify.Delta = 1 }, CodeInvalidProfile},
		{"costs", func(p *Profile) { p.Costs.Ask = 0 }, CodeInvalidProfile},
		{"band", func(p *Profile) { p.Costs = Costs{FalseAllow: 2, FalseBlock: 2, Ask: 1} }, CodeEmptyAskBand},
	}
	for _, c := range cases {
		p := good
		c.edit(&p)
		if got := CodeOf(p.Validate()); got != c.code {
			t.Errorf("%s: code %q, want %q", c.name, got, c.code)
		}
	}
}

func TestReliabilityQueryValidate(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	good := ReliabilityQuery{Subject: "s", Of: OfLearned, From: from, To: from.Add(time.Hour)}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q     ReliabilityQuery
		field string
	}{
		{ReliabilityQuery{Of: OfLearned, From: from, To: from.Add(time.Hour)}, "subject"},
		{ReliabilityQuery{Subject: "s", From: from, To: from.Add(time.Hour)}, "of"},
		{ReliabilityQuery{Subject: "s", Of: OfLearned, To: from}, "from"},
		{ReliabilityQuery{Subject: "s", Of: OfLearned, From: from}, "to"},
		{ReliabilityQuery{Subject: "s", Of: OfLearned, From: from, To: from}, "to"},
	} {
		var e *Error
		if err := c.q.Validate(); !errors.As(err, &e) || e.Code != CodeInvalidQuery || !slices.Contains(e.Fields, c.field) {
			t.Errorf("%+v: %v, want %s on %s", c.q, err, CodeInvalidQuery, c.field)
		}
	}
}

func TestListQueryValidate(t *testing.T) {
	for _, q := range []ListQuery{{Subject: "s"}, {Subject: "s", Pending: true, Limit: MaxListLimit}} {
		if err := q.Validate(); err != nil {
			t.Errorf("%+v: %v", q, err)
		}
	}
	for _, q := range []ListQuery{{}, {Subject: "s", Limit: -1}, {Subject: "s", Limit: MaxListLimit + 1}} {
		if CodeOf(q.Validate()) != CodeInvalidQuery {
			t.Errorf("%+v: %v", q, q.Validate())
		}
	}
}

// Every code has a status and a fixed sentence, and the sentence names no
// field path or value, which are the detail's.
func TestErrorTable(t *testing.T) {
	codes := Codes()
	if len(codes) != len(table) {
		t.Fatalf("Codes() has %d codes, the table %d", len(codes), len(table))
	}
	for _, c := range codes {
		if c.Message() == "" {
			t.Errorf("%s has no sentence", c)
		}
		if s := c.Status(); s < 400 || s > 599 {
			t.Errorf("%s has status %d", c, s)
		}
	}
	if Code("nope").Status() != 500 || Code("nope").Message() != "" {
		t.Error("an unknown code is not a 500 without a sentence")
	}
	e := &Error{Code: CodeInvalidAction, Fields: []string{"org", "at"}, Detail: "org is empty; at is not set"}
	if got, want := e.Error(), "invalid_action [org, at]: org is empty; at is not set"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := (&Error{Code: CodeInternal}).Error(); got != "internal" {
		t.Errorf("bare Error() = %q", got)
	}
	if CodeOf(errors.New("plain")) != CodeInternal {
		t.Error("a plain error is not internal")
	}
}

// The documented example round-trips through the types unchanged, so the
// JSON in the documentation is JSON the contract accepts.
func TestAssessmentJSON(t *testing.T) {
	const doc = `{"id":"asm_01J8","call_id":"toolu_01","verdict":"flag","approve":0.97,"uncertainty":0.2,"score":0.03,"thresholds":{"ask_at":0.1,"block_at":0.75},"profile":"org_7","audit":{"of":"allow","probability":0.05},"source":"learned/3","reason":"approved often before; sampled for review"}`
	var as Assessment
	if err := json.Unmarshal([]byte(doc), &as); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(as)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != doc {
		t.Errorf("round trip:\n got %s\nwant %s", out, doc)
	}
}
