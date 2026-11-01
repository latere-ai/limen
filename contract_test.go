// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"latere.ai/x/pkg/verdict"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// The two profiles of the research, the symmetric band (0.35, 0.65) and
// the safety-weighted costs 10, 4, 1 with band (0.25, 0.90), and a scaled
// copy of the second: only the ratios matter.
func TestCostsThresholds(t *testing.T) {
	cases := []struct {
		name              string
		c                 Costs
		allowAbove, block float64
	}{
		{"symmetric", Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}, 0.65, 0.35},
		{"safety", Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}, 0.90, 0.25},
		{"scale free", Costs{FalseAllow: 100, FalseBlock: 40, Ask: 10}, 0.90, 0.25},
	}
	for _, c := range cases {
		th, err := c.c.Thresholds()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !near(th.AllowAbove, c.allowAbove) || !near(th.BlockBelow, c.block) {
			t.Errorf("%s: %+v, want allow above %v, block below %v", c.name, th, c.allowAbove, c.block)
		}
		if th.Verdict(0.5) != Ask {
			t.Errorf("%s: an action Limen knows nothing about is not an ask", c.name)
		}
	}
	if d := DefaultProfile(Scope{Org: "o"}); !d.Default || !near(d.Thresholds.AllowAbove, 0.9) || d.Certify != DefaultCertify {
		t.Errorf("default profile %+v", d)
	}
}

// Costs that leave one half outside the ask band would decide an action on
// no evidence; costs that leave no band at all are a special case of that.
func TestCostsRefusesDecidingOnNoEvidence(t *testing.T) {
	cases := []struct {
		c      Costs
		fields []string
	}{
		{Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1}, []string{"costs.false_allow"}},
		{Costs{FalseAllow: 100, FalseBlock: 1.5, Ask: 1}, []string{"costs.false_block"}},
		{Costs{FalseAllow: 2, FalseBlock: 2, Ask: 1}, []string{"costs.false_allow", "costs.false_block"}},
	}
	for _, c := range cases {
		_, err := c.c.Thresholds()
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeAskTooCostly || !slices.Equal(e.Fields, c.fields) {
			t.Errorf("%+v: %v, want %s on %v", c.c, err, CodeAskTooCostly, c.fields)
		}
	}
	for _, c := range []Costs{
		{FalseAllow: 0, FalseBlock: 4, Ask: 1},
		{FalseAllow: 10, FalseBlock: -1, Ask: 1},
		{FalseAllow: 10, FalseBlock: 4, Ask: math.NaN()},
		{FalseAllow: math.Inf(1), FalseBlock: 4, Ask: 1},
	} {
		if _, err := c.Thresholds(); CodeOf(err) != CodeInvalidProfile {
			t.Errorf("%+v: code %q, want %q", c, CodeOf(err), CodeInvalidProfile)
		}
	}
}

func TestThresholdsVerdict(t *testing.T) {
	th := Thresholds{AllowAbove: 0.90, BlockBelow: 0.25}
	cases := []struct {
		approve float64
		want    Verdict
	}{
		{1, Allow}, {0.91, Allow}, {0.90, Ask}, {0.5, Ask}, {0.25, Ask},
		{0.24, Block}, {0, Block}, {-0.1, Ask}, {1.1, Ask}, {math.NaN(), Ask},
	}
	for _, c := range cases {
		if got := th.Verdict(c.approve); got != c.want {
			t.Errorf("Verdict(%v) = %q, want %q", c.approve, got, c.want)
		}
	}
}

// The rule agrees with the minimum expected cost it is derived from, on a
// grid of approval probabilities.
func TestThresholdsVerdictIsBayesOptimal(t *testing.T) {
	c := Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}
	th, err := c.Thresholds()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 1000; i++ {
		p := float64(i) / 1000
		cost := map[Verdict]float64{Allow: (1 - p) * c.FalseAllow, Block: p * c.FalseBlock, Ask: c.Ask}
		best := Allow
		for _, v := range []Verdict{Ask, Block} {
			if cost[v] < cost[best] {
				best = v
			}
		}
		if got := th.Verdict(p); cost[got] > cost[best]+1e-9 {
			t.Errorf("p=%v: %q costs %v, %q costs %v", p, got, cost[got], best, cost[best])
		}
	}
}

func TestCertificationSize(t *testing.T) {
	for _, c := range []struct {
		alpha, delta float64
		want         int
	}{{0.01, 0.05, 300}, {0.25, 0.05, 12}, {1, 0.5, 1}} {
		n, err := CertificationSize(c.alpha, c.delta)
		if err != nil || n != c.want {
			t.Errorf("CertificationSize(%v, %v) = %d, %v; want %d", c.alpha, c.delta, n, err, c.want)
		}
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

func f(x float64) *float64 { return &x }
func b(x bool) *bool       { return &x }

var key = Key{Org: "org_7", Subject: "https://issuer.example|usr_42", ActionID: "ses_9f/toolu_01"}

func validAction() Action {
	return Action{
		Key:         key,
		Actor:       &Actor{Kind: "agent", ID: "agt_ci", Session: "ses_9f"},
		Kind:        "tool",
		Name:        "bash",
		Effect:      EffectExternal,
		Reversible:  b(false),
		Input:       json.RawMessage(`{"command":"git push origin main"}`),
		Target:      &Target{Kind: "ref", ID: "refs/heads/main", Sensitivity: f(0.9)},
		Environment: &Environment{Kind: "sandbox", Credentialed: true},
		Attributes:  map[string]any{"protected": true, "files": 3, "remote": "origin", "ratio": 0.5},
		Signals:     []Signal{{Source: "rules/1", Score: f(0.4)}, {Source: "classifier/claude-auto-mode", Verdict: Allow}},
	}
}

func fieldsOf(t *testing.T, err error, code Code) []string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("%v, want %s", err, code)
	}
	return e.Fields
}

func TestActionValidate(t *testing.T) {
	if err := validAction().Validate(); err != nil {
		t.Fatalf("valid action: %v", err)
	}
	minimal := Action{Key: key, Kind: "egress", Name: "https", Effect: EffectExternal}
	if err := minimal.Validate(); err != nil {
		t.Fatalf("minimal action: %v", err)
	}
	cases := []struct {
		field string
		edit  func(*Action)
	}{
		{"org", func(a *Action) { a.Org = "" }},
		{"subject", func(a *Action) { a.Subject = "" }},
		{"action_id", func(a *Action) { a.ActionID = "" }},
		{"actor.kind", func(a *Action) { a.Actor.Kind = "" }},
		{"kind", func(a *Action) { a.Kind = "" }},
		{"name", func(a *Action) { a.Name = "" }},
		{"effect", func(a *Action) { a.Effect = "delete" }},
		{"input", func(a *Action) { a.Input = json.RawMessage(`{`) }},
		{"target.kind", func(a *Action) { a.Target.Kind = "" }},
		{"target.sensitivity", func(a *Action) { a.Target.Sensitivity = f(1.5) }},
		{"environment.kind", func(a *Action) { a.Environment.Kind = "" }},
		{"attributes", func(a *Action) { a.Attributes[""] = 1 }},
		{"attributes.nested", func(a *Action) { a.Attributes["nested"] = map[string]any{} }},
		{"attributes.nan", func(a *Action) { a.Attributes["nan"] = math.NaN() }},
		{"signals[0].source", func(a *Action) { a.Signals[0].Source = "" }},
		{"signals[1].verdict", func(a *Action) { a.Signals[1].Verdict = "deny" }},
		{"signals[0].score", func(a *Action) { a.Signals[0].Score = f(-1) }},
		{"signals[1]", func(a *Action) { a.Signals[1].Verdict = "" }},
	}
	for _, c := range cases {
		a := validAction()
		a.Attributes = map[string]any{"protected": true}
		c.edit(&a)
		if fields := fieldsOf(t, a.Validate(), CodeInvalidAction); !slices.Contains(fields, c.field) {
			t.Errorf("%s: fields %v", c.field, fields)
		}
	}
	many := validAction()
	many.Attributes = map[string]any{}
	for i := range MaxAttributes + 1 {
		many.Attributes["a"+strconv.Itoa(i)] = i
	}
	if fields := fieldsOf(t, many.Validate(), CodeInvalidAction); !slices.Contains(fields, "attributes") {
		t.Errorf("too many attributes: %v", fields)
	}
	// Every problem is reported in one pass.
	if fields := fieldsOf(t, (Action{}).Validate(), CodeInvalidAction); len(fields) < 6 {
		t.Errorf("empty action: %v", fields)
	}
}

func TestScalar(t *testing.T) {
	for _, v := range []any{"s", true, 1, int8(1), int16(1), int32(1), int64(1), uint(1), uint8(1), uint16(1), uint32(1), uint64(1), float32(1), 1.5, json.Number("2.5")} {
		if !scalar(v) {
			t.Errorf("%T %v is not a scalar", v, v)
		}
	}
	for _, v := range []any{nil, []int{1}, map[string]any{}, float32(math.Inf(1)), math.Inf(-1), json.Number("x")} {
		if scalar(v) {
			t.Errorf("%T %v is a scalar", v, v)
		}
	}
}

func TestDecisionValidate(t *testing.T) {
	a := validAction()
	good := []Decision{
		{Key: key, Verdict: Allow, ReviewProbability: 0.05, Source: "learned/3"},
		{Key: key, Verdict: Flag, ReviewProbability: 0.05, Source: "learned/3"},
		{Key: key, Verdict: Flag, ReviewProbability: 1, Source: "topos/progressive"},
		{Key: key, Verdict: Ask, ReviewProbability: 1, Source: "rules/1", Action: &a},
		{Key: key, Verdict: Block, ReviewProbability: 0, Source: "rules/1"},
	}
	for _, d := range good {
		if err := d.Validate(); err != nil {
			t.Errorf("%+v: %v", d, err)
		}
	}
	other := validAction()
	other.ActionID = "other"
	bad := []struct {
		d     Decision
		field string
	}{
		{Decision{Verdict: Allow, Source: "s"}, "org"},
		{Decision{Key: key, Verdict: "deny", Source: "s"}, "verdict"},
		{Decision{Key: key, Verdict: Allow, ReviewProbability: 1.5, Source: "s"}, "review_probability"},
		{Decision{Key: key, Verdict: Allow, ReviewProbability: math.NaN(), Source: "s"}, "review_probability"},
		{Decision{Key: key, Verdict: Ask, ReviewProbability: 0, Source: "s"}, "review_probability"},
		{Decision{Key: key, Verdict: Block, ReviewProbability: 1, Source: "s"}, "review_probability"},
		{Decision{Key: key, Verdict: Allow}, "source"},
		{Decision{Key: key, Verdict: Allow, Source: "s", Action: &other}, "action"},
		{Decision{Key: key, Verdict: Allow, Source: "s", Action: &Action{Key: key}}, "action.kind"},
	}
	for _, c := range bad {
		if fields := fieldsOf(t, c.d.Validate(), CodeInvalidDecision); !slices.Contains(fields, c.field) {
			t.Errorf("%+v: fields %v, want %s", c.d, fields, c.field)
		}
	}
}

// What verdict.Decide returns is always a valid decision.
func TestDecideProducesValidDecisions(t *testing.T) {
	for _, s := range []Verdict{Allow, Ask, Block, ""} {
		for _, c := range []Verdict{Allow, Flag, Ask, Block} {
			for _, u := range []float64{0.01, 0.5} {
				v, p := verdict.Decide(s, c, 0.05, u)
				d := Decision{Key: key, Verdict: v, ReviewProbability: p, Source: "x"}
				if err := d.Validate(); err != nil {
					t.Errorf("Decide(%q, %q, 0.05, %v) = %q, %v: %v", s, c, u, v, p, err)
				}
			}
		}
	}
}

func TestAnswerAndQueries(t *testing.T) {
	if err := (Answer{Key: key, Approve: true}).Validate(); err != nil {
		t.Error(err)
	}
	if fields := fieldsOf(t, (Answer{}).Validate(), CodeInvalidAnswer); len(fields) != 3 {
		t.Errorf("empty answer: %v", fields)
	}
	for _, q := range []CaseQuery{{Org: "o", Subject: "s"}, {Org: "o", Subject: "s", Pending: true, Limit: MaxPageLimit}} {
		if err := q.Validate(); err != nil {
			t.Errorf("%+v: %v", q, err)
		}
	}
	for _, q := range []CaseQuery{{Subject: "s"}, {Org: "o"}, {Org: "o", Subject: "s", Limit: -1}, {Org: "o", Subject: "s", Limit: MaxPageLimit + 1}} {
		if CodeOf(q.Validate()) != CodeInvalidQuery {
			t.Errorf("%+v: %v", q, q.Validate())
		}
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	good := ReliabilityQuery{Org: "o", Subject: "s", Of: OfApplied, From: from, To: from.Add(time.Hour)}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		edit  func(*ReliabilityQuery)
		field string
	}{
		{func(q *ReliabilityQuery) { q.Org = "" }, "org"},
		{func(q *ReliabilityQuery) { q.Subject = "" }, "subject"},
		{func(q *ReliabilityQuery) { q.Of = "" }, "of"},
		{func(q *ReliabilityQuery) { q.From = time.Time{} }, "from"},
		{func(q *ReliabilityQuery) { q.To = time.Time{} }, "to"},
		{func(q *ReliabilityQuery) { q.To = q.From }, "to"},
	} {
		q := good
		c.edit(&q)
		if fields := fieldsOf(t, q.Validate(), CodeInvalidQuery); !slices.Contains(fields, c.field) {
			t.Errorf("%s: %v", c.field, fields)
		}
	}
	// A window with neither bound names both.
	if fields := fieldsOf(t, ReliabilityQuery{Org: "o", Subject: "s", Of: OfApplied}.Validate(), CodeInvalidQuery); !slices.Equal(fields, []string{"from", "to"}) {
		t.Errorf("no window: %v", fields)
	}
}

func TestProfileValidate(t *testing.T) {
	good := Profile{Org: "org_7", Kind: "tool", Costs: DefaultCosts, AuditRate: 0.05}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid profile: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Profile)
		code Code
	}{
		{"org", func(p *Profile) { p.Org = "" }, CodeInvalidProfile},
		{"audit rate", func(p *Profile) { p.AuditRate = 1.2 }, CodeInvalidProfile},
		{"certify", func(p *Profile) { p.Certify = Certify{Alpha: 0.01, Delta: 1} }, CodeInvalidProfile},
		{"costs", func(p *Profile) { p.Costs.Ask = 0 }, CodeInvalidProfile},
		{"no evidence", func(p *Profile) { p.Costs = Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1} }, CodeAskTooCostly},
	}
	for _, c := range cases {
		p := good
		c.edit(&p)
		if got := CodeOf(p.Validate()); got != c.code {
			t.Errorf("%s: code %q, want %q", c.name, got, c.code)
		}
	}
}

func TestReplayRecordValidate(t *testing.T) {
	at := time.Date(2026, 9, 2, 9, 12, 3, 0, time.UTC)
	good := ReplayRecord{At: at, Action: validAction(), Verdict: Ask, ReviewProbability: 1, Source: "rules/1", Approve: b(true)}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.At, bad.Verdict, bad.ReviewProbability, bad.Source = time.Time{}, "x", 2, ""
	bad.Action.Kind = ""
	fields := fieldsOf(t, bad.Validate(), CodeInvalidReplay)
	for _, want := range []string{"at", "action.kind", "verdict", "review_probability", "source"} {
		if !slices.Contains(fields, want) {
			t.Errorf("missing %s in %v", want, fields)
		}
	}
}

// The request types of explanations (spec 009) report every field at
// fault in one error, as the others do, and nothing for a valid value.
func TestExplanationQueries(t *testing.T) {
	if err := key.Validate(); err != nil {
		t.Error(err)
	}
	if fields := fieldsOf(t, Key{}.Validate(), CodeInvalidQuery); !slices.Equal(fields, []string{"org", "subject", "action_id"}) {
		t.Errorf("empty key: %v", fields)
	}
	if err := (LearnedQuery{Org: "o", Subject: "s", Kind: "tool"}).Validate(); err != nil {
		t.Error(err)
	}
	if fields := fieldsOf(t, LearnedQuery{Agent: "agt_1"}.Validate(), CodeInvalidQuery); !slices.Equal(fields, []string{"org", "subject"}) {
		t.Errorf("empty learned query: %v", fields)
	}

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	good := PreviewRequest{Org: "o", Subject: "s", From: from, To: from.Add(time.Hour), Costs: DefaultCosts, AuditRate: 0.05}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		edit   func(*PreviewRequest)
		code   Code
		fields []string
	}{
		{func(r *PreviewRequest) { r.Org, r.Subject = "", "" }, CodeInvalidQuery, []string{"org", "subject"}},
		{func(r *PreviewRequest) { r.From = time.Time{} }, CodeInvalidQuery, []string{"from"}},
		{func(r *PreviewRequest) { r.To = time.Time{} }, CodeInvalidQuery, []string{"to"}},
		{func(r *PreviewRequest) { r.To = r.From }, CodeInvalidQuery, []string{"to"}},
		{func(r *PreviewRequest) { r.From, r.To = time.Time{}, time.Time{} }, CodeInvalidQuery, []string{"from", "to"}},
		{func(r *PreviewRequest) { r.AuditRate = 1.5 }, CodeInvalidQuery, []string{"audit_rate"}},
		{func(r *PreviewRequest) { r.AuditRate = math.NaN() }, CodeInvalidQuery, []string{"audit_rate"}},
		// Every field at fault in one error, the costs checked only after.
		{func(r *PreviewRequest) { r.Subject, r.AuditRate, r.Costs.Ask = "", -1, 0 }, CodeInvalidQuery, []string{"subject", "audit_rate"}},
		{func(r *PreviewRequest) { r.Costs.Ask = 0 }, CodeInvalidProfile, []string{"costs.ask"}},
		{func(r *PreviewRequest) { r.Costs = Costs{FalseAllow: 1.5, FalseBlock: 100, Ask: 1} }, CodeAskTooCostly, []string{"costs.false_allow"}},
	} {
		r := good
		c.edit(&r)
		if fields := fieldsOf(t, r.Validate(), c.code); !slices.Equal(fields, c.fields) {
			t.Errorf("%+v: fields %v, want %v", r, fields, c.fields)
		}
	}
}

// Every code has a status and a fixed sentence.
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
	e := &Error{Code: CodeInvalidAction, Fields: []string{"org", "kind"}, Detail: "org is empty; kind is empty"}
	if got, want := e.Error(), "invalid_action [org, kind]: org is empty; kind is empty"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := (&Error{Code: CodeInternal}).Error(); got != "internal" {
		t.Errorf("bare Error() = %q", got)
	}
	if CodeOf(errors.New("plain")) != CodeInternal {
		t.Error("a plain error is not internal")
	}
	wrapped := fmt.Errorf("decision point: %w", fmt.Errorf("record: %w", e))
	if CodeOf(wrapped) != CodeInvalidAction {
		t.Errorf("CodeOf a wrapped error: %s", CodeOf(wrapped))
	}
}

// The documented examples round-trip through the types unchanged, so the
// JSON in the documentation is JSON the contract produces.
func TestJSONRoundTrip(t *testing.T) {
	docs := []struct {
		v   any
		doc string
	}{
		{&Prediction{}, `{"org":"org_7","subject":"https://issuer.example|usr_42","action_id":"ses_9f/toolu_01","verdict":"ask","approve":0.58,"uncertainty":0.71,"evidence_ratio":0.62,"ask_kind":"split","thresholds":{"allow_above":0.9,"block_below":0.25},"profile":{"org":"org_7"},"audit_rate":0.05,"source":"learned/3","reason":"the chance this person approves (0.58) is inside the ask band (0.25 to 0.90)"}`},
		{&Decision{}, `{"org":"org_7","subject":"https://issuer.example|usr_42","action_id":"ses_9f/toolu_01","verdict":"flag","review_probability":0.05,"source":"topos/progressive"}`},
		{&Answer{}, `{"org":"org_7","subject":"https://issuer.example|usr_42","action_id":"ses_9f/toolu_01","approve":true}`},
	}
	for _, d := range docs {
		if err := json.Unmarshal([]byte(d.doc), d.v); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(d.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != d.doc {
			t.Errorf("round trip:\n got %s\nwant %s", out, d.doc)
		}
	}
}

// The root package is imported into every decision point's hot path, so
// it reaches the standard library and the shared verdict vocabulary only.
func TestRootImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(path, "/")
			if strings.Contains(first, ".") && path != "latere.ai/x/pkg/verdict" {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}

func TestScopeOfAndCandidates(t *testing.T) {
	a := validAction()
	if got := ScopeOf(a); got != (Scope{Org: "org_7", Kind: "tool", Agent: "agt_ci"}) {
		t.Errorf("ScopeOf = %+v", got)
	}
	a.Actor = &Actor{Kind: "person", ID: "usr_1"}
	if got := ScopeOf(a); got.Agent != "" {
		t.Errorf("a person is not an agent scope: %+v", got)
	}
	full := Scope{Org: "o", Kind: "tool", Agent: "x"}.Candidates()
	want := []Scope{{Org: "o", Kind: "tool", Agent: "x"}, {Org: "o", Agent: "x"}, {Org: "o", Kind: "tool"}, {Org: "o"}}
	if !slices.Equal(full, want) {
		t.Errorf("Candidates = %+v", full)
	}
	if got := (Scope{Org: "o"}).Candidates(); !slices.Equal(got, []Scope{{Org: "o"}}) {
		t.Errorf("org only = %+v", got)
	}
	if got := (Scope{Org: "o", Kind: "tool"}).Candidates(); !slices.Equal(got, []Scope{{Org: "o", Kind: "tool"}, {Org: "o"}}) {
		t.Errorf("kind only = %+v", got)
	}
}
