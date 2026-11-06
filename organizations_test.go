// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json/v2"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

const person = "https://issuer.example|usr_42"

// saved is a profile as PutProfile saves it: thresholds derived from the
// costs and a zero claim filled.
func saved(t *testing.T, s Scope, c Costs, audit float64, certify Certify) Profile {
	t.Helper()
	th, err := c.Thresholds()
	if err != nil {
		t.Fatal(err)
	}
	return Profile{Scope: s, Costs: c, AuditRate: audit, Certify: certifyOr(certify), Thresholds: th}
}

// The three profiles a hosted console names, each stricter than the one
// before it.
var (
	balanced = Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}
	careful  = Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}
	strict   = Costs{FalseAllow: 20, FalseBlock: 4, Ask: 1}
)

func TestScopeValidate(t *testing.T) {
	cases := []struct {
		s      Scope
		fields []string
	}{
		{Scope{Org: "o"}, nil},
		{Scope{Org: "o", Kind: "tool", Agent: "x"}, nil},
		{Scope{Org: "o", Subject: person}, nil},
		{Scope{Org: person, Subject: person}, nil},
		{Scope{}, []string{"org"}},
		{Scope{Subject: person}, []string{"org"}},
		{Scope{Org: "o", Kind: "tool", Subject: person}, []string{"kind"}},
		{Scope{Org: "o", Kind: "tool", Agent: "x", Subject: person}, []string{"kind", "agent"}},
		{Scope{Org: person, Subject: "https://issuer.example|usr_other"}, []string{"subject"}},
	}
	for _, c := range cases {
		err := c.s.Validate()
		if c.fields == nil {
			if err != nil {
				t.Errorf("%+v: %v", c.s, err)
			}
			continue
		}
		if got := fieldsOf(t, err, CodeInvalidQuery); !slices.Equal(got, c.fields) {
			t.Errorf("%+v: fields %v, want %v", c.s, got, c.fields)
		}
		p := Profile{Scope: c.s, Costs: DefaultCosts, AuditRate: 0.05}
		if got := fieldsOf(t, p.Validate(), CodeInvalidProfile); !slices.Equal(got, c.fields) {
			t.Errorf("profile %+v: fields %v, want %v", c.s, got, c.fields)
		}
	}
	got := Scope{Org: "o", Kind: "tool", Agent: "x", Subject: person}.Candidates()
	want := []Scope{{Org: "o", Kind: "tool", Agent: "x"}, {Org: "o", Agent: "x"}, {Org: "o", Kind: "tool"}, {Org: "o"}}
	if !slices.Equal(got, want) {
		t.Errorf("a subject reached the candidates: %+v", got)
	}
}

// The meet of the named profiles is always the stricter of the two, with
// the person's subject only when the person's profile changed a number
// (spec 010, criterion 2).
func TestStricterNamedProfiles(t *testing.T) {
	org := Scope{Org: "o"}
	own := Scope{Org: "o", Subject: person}
	named := []Costs{balanced, careful, strict}
	for i, oc := range named {
		for j, pc := range named {
			o := saved(t, org, oc, 0.05, Certify{})
			p := saved(t, own, pc, 0.05, Certify{})
			m := Stricter(o, p)
			want := o
			if j > i {
				want = p
			}
			if !near(m.Thresholds.AllowAbove, want.Thresholds.AllowAbove) || !near(m.Thresholds.BlockBelow, want.Thresholds.BlockBelow) {
				t.Errorf("org %d, person %d: thresholds %+v, want %+v", i, j, m.Thresholds, want.Thresholds)
			}
			if (m.Subject == person) != (j > i) || m.Kind != "" || m.Agent != "" {
				t.Errorf("org %d, person %d: scope %+v", i, j, m.Scope)
			}
			if j <= i && m != o {
				t.Errorf("org %d, person %d: a person's looser profile changed the organization's: %+v", i, j, m)
			}
		}
	}
	// Each of the other numbers on its own.
	o := saved(t, org, careful, 0.05, Certify{Alpha: 0.02, Delta: 0.05})
	for _, c := range []struct {
		name   string
		person Profile
		check  func(Profile) bool
	}{
		{"audit rate", saved(t, own, careful, 0.2, Certify{Alpha: 0.02, Delta: 0.05}), func(m Profile) bool { return m.AuditRate == 0.2 }},
		{"alpha", saved(t, own, careful, 0, Certify{Alpha: 0.01, Delta: 0.5}), func(m Profile) bool { return m.Certify == Certify{Alpha: 0.01, Delta: 0.05} }},
		{"delta", saved(t, own, careful, 0, Certify{Alpha: 0.5, Delta: 0.01}), func(m Profile) bool { return m.Certify == Certify{Alpha: 0.02, Delta: 0.01} }},
	} {
		m := Stricter(o, c.person)
		if !c.check(m) || m.Subject != person || m.Thresholds != o.Thresholds || m.Default {
			t.Errorf("%s: meet %+v", c.name, m)
		}
	}
	// The default stays the default only when nothing changed.
	d := DefaultProfile(org)
	if m := Stricter(d, saved(t, own, balanced, 0, Certify{Alpha: 1, Delta: 0.9})); !m.Default || m != d {
		t.Errorf("a looser person's profile changed the default: %+v", m)
	}
	if m := Stricter(d, saved(t, own, strict, 0, Certify{})); m.Default || m.Subject != person {
		t.Errorf("a stricter person's profile kept the default: %+v", m)
	}
	// A zero claim is the default claim, as a saved profile has it.
	zero := o
	zero.Certify = Certify{}
	if m := Stricter(zero, saved(t, own, careful, 0, Certify{Alpha: 0.5, Delta: 0.5})); m.Certify != (Certify{}) {
		t.Errorf("a looser claim changed a zero claim: %+v", m.Certify)
	}
	if m := Stricter(zero, saved(t, own, careful, 0, Certify{Alpha: 0.001, Delta: 0.5})); m.Certify != (Certify{Alpha: 0.001, Delta: DefaultCertify.Delta}) {
		t.Errorf("a stricter alpha against a zero claim: %+v", m.Certify)
	}
}

// randomProfile is an accepted profile with random costs, audit rate and
// claim.
func randomProfile(t *testing.T, rng *rand.Rand, s Scope) Profile {
	ask := 0.1 + 10*rng.Float64()
	c := Costs{Ask: ask, FalseAllow: ask * (2 + 1e-9 + 200*rng.Float64()*rng.Float64()), FalseBlock: ask * (2 + 1e-9 + 200*rng.Float64()*rng.Float64())}
	certify := Certify{Alpha: 0.001 + 0.999*rng.Float64(), Delta: 0.001 + 0.998*rng.Float64()}
	if rng.IntN(5) == 0 {
		certify = Certify{}
	}
	return saved(t, s, c, rng.Float64(), certify)
}

// The meet of two accepted profiles is accepted, holds one half in its ask
// band, takes each number from the stricter side exactly, has costs that
// give its thresholds to 1e-12, and never loosens the organization's
// profile (spec 010, criteria 2 and 3).
func TestStricterProperties(t *testing.T) {
	rng := rand.New(rand.NewPCG(10, 2026))
	for i := range 5000 {
		o := randomProfile(t, rng, Scope{Org: "o"})
		p := randomProfile(t, rng, Scope{Org: "o", Subject: person})
		if i%7 == 0 {
			p = o
			p.Subject = person
		}
		m := Stricter(o, p)
		oc, pc := certifyOr(o.Certify), certifyOr(p.Certify)
		if m.Thresholds.AllowAbove != max(o.Thresholds.AllowAbove, p.Thresholds.AllowAbove) ||
			m.Thresholds.BlockBelow != min(o.Thresholds.BlockBelow, p.Thresholds.BlockBelow) ||
			m.AuditRate != max(o.AuditRate, p.AuditRate) {
			t.Fatalf("%d: meet %+v of %+v and %+v", i, m, o, p)
		}
		if mc := certifyOr(m.Certify); mc.Alpha != min(oc.Alpha, pc.Alpha) || mc.Delta != min(oc.Delta, pc.Delta) {
			t.Fatalf("%d: claim %+v of %+v and %+v", i, m.Certify, oc, pc)
		}
		th, err := m.Costs.Thresholds()
		if err != nil || !near(th.AllowAbove, m.Thresholds.AllowAbove) || !near(th.BlockBelow, m.Thresholds.BlockBelow) {
			t.Fatalf("%d: costs %+v give %+v, %v; the meet has %+v", i, m.Costs, th, err, m.Thresholds)
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("%d: the meet %+v is refused: %v", i, m, err)
		}
		if m.Thresholds.Verdict(0.5) != Ask {
			t.Fatalf("%d: the meet %+v does not ask at one half", i, m.Thresholds)
		}
		changed := m != o
		if changed != (m.Subject == person) || (changed && m.Default) {
			t.Fatalf("%d: changed %v, meet %+v", i, changed, m)
		}
		if again := Stricter(m, p); again != m {
			t.Fatalf("%d: the meet met the person again changed: %+v, %+v", i, again, m)
		}
	}
}

func TestWeeks(t *testing.T) {
	monday := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Time
		want bool
	}{
		{monday, true},
		{monday.In(time.FixedZone("CET", 3600)), true},
		{monday.Add(time.Nanosecond), false},
		{monday.Add(time.Hour), false},
		{monday.Add(24 * time.Hour), false},
		{monday.Add(ReportWeek), true},
		{time.Date(2026, 10, 5, 0, 0, 0, 0, time.FixedZone("CET", 3600)), false},
	} {
		if got := WeekStart(c.at); got != c.want {
			t.Errorf("WeekStart(%v) = %v", c.at, got)
		}
	}
	closes := monday.Add(14 * 24 * time.Hour)
	if WeekClosed(monday, closes.Add(-time.Nanosecond)) || !WeekClosed(monday, closes) {
		t.Error("a week closes 7 days after its end")
	}
}

func TestReportQueryValidate(t *testing.T) {
	monday := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		q      ReportQuery
		fields []string
	}{
		{ReportQuery{Org: "o", From: monday, To: monday.Add(ReportWeek)}, nil},
		{ReportQuery{Org: "o", From: monday, To: monday.Add(ReportMaxWeeks * ReportWeek)}, nil},
		{ReportQuery{}, []string{"org", "from", "to"}},
		{ReportQuery{Org: "o", From: monday.Add(time.Hour), To: monday.Add(ReportWeek)}, []string{"from"}},
		{ReportQuery{Org: "o", From: monday, To: monday.Add(ReportWeek - 24*time.Hour)}, []string{"to"}},
		{ReportQuery{Org: "o", From: monday, To: monday}, []string{"to"}},
		{ReportQuery{Org: "o", From: monday.Add(ReportWeek), To: monday}, []string{"to"}},
		{ReportQuery{Org: "o", From: monday, To: monday.Add((ReportMaxWeeks + 1) * ReportWeek)}, []string{"to"}},
		{ReportQuery{Org: "o", To: monday}, []string{"from"}},
	}
	for _, c := range cases {
		err := c.q.Validate()
		if c.fields == nil {
			if err != nil {
				t.Errorf("%+v: %v", c.q, err)
			}
			continue
		}
		if got := fieldsOf(t, err, CodeInvalidQuery); !slices.Equal(got, c.fields) {
			t.Errorf("%+v: fields %v, want %v", c.q, got, c.fields)
		}
	}
}

// A week as the API shows it round-trips through the types: an open week
// has its bounds alone, and a withheld field is absent.
func TestReportJSON(t *testing.T) {
	doc := `{"org":"org_7","from":"2026-10-05T00:00:00Z","to":"2026-10-19T00:00:00Z","min_subjects":5,"weeks":[` +
		`{"from":"2026-10-05T00:00:00Z","to":"2026-10-12T00:00:00Z","state":"ready",` +
		`"total":{"subjects":12,"decisions":{"allow":1610,"flag":92,"ask":212,"block":9},"flag_reviews":{"reviews":81,"denied":2},` +
		`"applied":{"false_allow":{"value":0.021,"interval":[0.006,0.07],"covered":1,"reviews":81,"audits":81,"unanswered":11}},` +
		`"learned":{"false_allow":{"value":null,"covered":0,"reviews":0,"audits":0,"unanswered":0}},"withheld":["applied.false_block","learned.false_block"]},` +
		`"agents":[{"agent":"agt_release","subjects":9,"decisions":{"allow":1200,"flag":70,"ask":150,"block":6},"flag_reviews":{"reviews":63},"applied":{},"learned":{},` +
		`"withheld":["flag_reviews.denied","applied.false_allow","applied.false_block","learned.false_allow","learned.false_block"]}],` +
		`"other":{"subjects":7,"decisions":{"allow":410,"flag":22,"ask":62,"block":3},"flag_reviews":{"reviews":18},"applied":{},"learned":{},` +
		`"withheld":["flag_reviews.denied","applied.false_allow","applied.false_block","learned.false_allow","learned.false_block"]}},` +
		`{"from":"2026-10-12T00:00:00Z","to":"2026-10-19T00:00:00Z","state":"open"}]}`
	var r Report
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != doc {
		t.Errorf("round trip:\n got %s\nwant %s", out, doc)
	}
	if !slices.Equal(r.Weeks[0].Agents[0].Withheld, AnswerFields()) {
		t.Errorf("withheld %v, want every answer field %v", r.Weeks[0].Agents[0].Withheld, AnswerFields())
	}
	if r.Weeks[0].Agents[0].Agent != "agt_release" || r.Weeks[0].Agents[0].Subjects != 9 || r.Weeks[1].Total != nil {
		t.Errorf("decoded %+v", r)
	}
}
