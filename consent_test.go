// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

// values are the words of an order and unknown, the values a dimension can
// take.
func values(o Order) []string { return append(slices.Clone(o), Unknown) }

// The order of each dimension is a partial order over its values and
// unknown: reflexive, antisymmetric and transitive, with unknown above the
// least value, below the greatest, and not comparable with a middle value
// (spec 011, criterion 5). Join is the least upper bound where one exists,
// and the greatest value otherwise.
func TestOrderIsAPartialOrder(t *testing.T) {
	for _, o := range []Order{Undos(), TargetClasses(), Contacts(), Bounds()} {
		vs := values(o)
		for _, a := range vs {
			if !o.Within(a, a) {
				t.Errorf("%v: %s is not within itself", o, a)
			}
			for _, b := range vs {
				if a != b && o.Within(a, b) && o.Within(b, a) {
					t.Errorf("%v: %s and %s are within each other", o, a, b)
				}
				j := o.Join(a, b)
				if !o.Within(a, j) || !o.Within(b, j) {
					t.Errorf("%v: join %s of %s and %s is not above both", o, j, a, b)
				}
				for _, c := range vs {
					if o.Within(a, b) && o.Within(b, c) && !o.Within(a, c) {
						t.Errorf("%v: %s within %s within %s, but not %s within %s", o, a, b, c, a, c)
					}
					// No value above both is below the join, unless the
					// join is the only one.
					if o.Within(a, c) && o.Within(b, c) && !o.Within(j, c) {
						t.Errorf("%v: %s is above %s and %s but not above their join %s", o, c, a, b, j)
					}
				}
			}
		}
		least, greatest := o[0], o[len(o)-1]
		if !o.Within(least, Unknown) || !o.Within(Unknown, greatest) || o.Within(Unknown, least) || o.Within(greatest, Unknown) {
			t.Errorf("%v: unknown is not between %s and %s", o, least, greatest)
		}
		for _, mid := range o[1 : len(o)-1] {
			if o.Within(mid, Unknown) || o.Within(Unknown, mid) {
				t.Errorf("%v: the middle value %s is comparable with unknown", o, mid)
			}
			if j := o.Join(mid, Unknown); j != greatest {
				t.Errorf("%v: join of %s and unknown is %s, want %s", o, mid, j, greatest)
			}
		}
	}
	u := Undos()
	for _, c := range []struct{ a, b, want string }{
		{"", "", ""}, {"", UndoReversible, UndoReversible}, {UndoNone, "", UndoNone},
		{"later", UndoNone, Unknown}, {UndoRecoverable, "nonsense", UndoIrreversible},
	} {
		if got := u.Join(c.a, c.b); got != c.want {
			t.Errorf("join of %q and %q is %q, want %q", c.a, c.b, got, c.want)
		}
	}
	if u.Word("") != "" || u.Word("partial") != Unknown || u.Word(UndoNone) != UndoNone || u.Word(Unknown) != Unknown {
		t.Error("a word outside the vocabulary is not unknown")
	}
	if !u.Within("", UndoIrreversible) || u.Within("", UndoRecoverable) || !u.Within("x", "") {
		t.Error("an empty word and a word outside the vocabulary are not unknown")
	}
}

// A scope is within another when each dimension of the order is, and
// contact is not one of them.
func TestReachWithin(t *testing.T) {
	update := Reach{Undo: UndoRecoverable, Crosses: Crosses{Remote: BoundYes, Public: BoundNo}, TargetClass: ClassOwn, Contact: ContactRepeat}
	public := update
	public.Undo, public.Crosses.Public, public.Contact = UndoIrreversible, BoundYes, ContactFirst
	if !update.Within(update) || !update.Within(public) || public.Within(update) {
		t.Errorf("update within public: %v, public within update: %v", update.Within(public), public.Within(update))
	}
	if n := public.Exceeds(update); n != 2 {
		t.Errorf("public exceeds update in %d dimensions, want 2 (undo and crosses.public)", n)
	}
	first := update
	first.Contact = ContactFirst
	if !first.Within(update) {
		t.Error("contact is in the order")
	}
	all := Reach{Undo: Unknown, TargetClass: Unknown, Contact: Unknown}
	if !all.Within(all) || update.Within(all) || !(Reach{Undo: UndoNone, TargetClass: ClassScratch, Crosses: Crosses{BoundNo, BoundNo, BoundNo, BoundNo, BoundNo}}).Within(all) {
		t.Error("unknown covers more than the least value")
	}
	top := Reach{Undo: UndoIrreversible, TargetClass: ClassPrimary, Crosses: Crosses{BoundYes, BoundYes, BoundYes, BoundYes, BoundYes}}
	if !all.Within(top) || !update.Within(top) || top.Within(all) {
		t.Error("the greatest scope does not cover each scope")
	}
}

// A bound is true, false or absent on the wire, and any other value reads
// as unknown, not as an error (spec 011, criterion 4).
func TestBoundJSON(t *testing.T) {
	c := Crosses{Machine: BoundNo, Remote: BoundYes, Network: "maybe"}
	b, err := json.Marshal(c)
	if err != nil || string(b) != `{"machine":false,"remote":true}` {
		t.Fatalf("%s, %v", b, err)
	}
	var back Crosses
	for _, doc := range []string{`{"machine":false,"remote":true,"network":"yes","public":1,"organization":null,"later":true}`} {
		if err := json.Unmarshal([]byte(doc), &back); err != nil {
			t.Fatal(err)
		}
	}
	if back != (Crosses{Machine: BoundNo, Remote: BoundYes}) {
		t.Errorf("read back %+v", back)
	}
	for _, x := range []Bound{BoundUnknown, BoundNo, BoundYes, "other"} {
		raw, _ := x.MarshalJSON()
		var y Bound
		if err := y.UnmarshalJSON(raw); err != nil || (y != x && x != "other") || (x == "other" && y != BoundUnknown) {
			t.Errorf("%q marshals to %s and reads back %q", x, raw, y)
		}
	}
}

// A claim with a word outside the vocabulary is valid: a newer decision
// point works with an older Limen.
func TestActionWithClaimsIsValid(t *testing.T) {
	a := validAction()
	a.Scope = &ReachClaim{Undo: "partially", Contact: "often", Crosses: Crosses{Public: BoundYes}}
	a.Target.Class = "galaxy"
	a.Environment.Holds = []string{HoldEgress, "moat"}
	a.Environment.Repositories = "secret"
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if !a.Environment.Held(HoldEgress) || a.Environment.Held(HoldCheckpoints) || (*Environment)(nil).Held(HoldEgress) {
		t.Error("Held reads the holds wrong")
	}
}

func TestHowAnswered(t *testing.T) {
	good := Answer{Key: key, Approve: true, AnswerMS: new(int64(MaxAnswerMS)), WaitedMS: new(int64(0)),
		DetailsOpened: new(false), SuggestionShown: new(true), Surface: SurfaceQueue, Remembered: true}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.AnswerMS, bad.WaitedMS = new(int64(MaxAnswerMS+1)), new(int64(-1))
	if fields := fieldsOf(t, bad.Validate(), CodeInvalidAnswer); !slices.Equal(fields, []string{"answer_ms", "waited_ms"}) {
		t.Errorf("fields %v", fields)
	}
	bad.AnswerMS = new(int64(-1))
	if fields := fieldsOf(t, bad.Validate(), CodeInvalidAnswer); !slices.Contains(fields, "answer_ms") {
		t.Errorf("a negative answer_ms: %v", fields)
	}
	other := good
	other.Surface = "watch"
	if err := other.Validate(); err != nil {
		t.Errorf("a surface outside the list is an error: %v", err)
	}
	if k := other.Kept(); k.Surface != "" || k.AnswerMS != other.AnswerMS {
		t.Errorf("kept %+v", k)
	}
	if k := good.Kept(); k != good.HowAnswered {
		t.Errorf("a known surface is not kept: %+v", k)
	}
	for _, s := range Surfaces() {
		if (HowAnswered{Surface: s}).Kept().Surface != s {
			t.Errorf("surface %s is dropped", s)
		}
	}
	b, err := json.Marshal(good)
	if err != nil || !strings.Contains(string(b), `"answer_ms":86400000,"waited_ms":0,"details_opened":false,"suggestion_shown":true,"surface":"queue","remembered":true`) {
		t.Errorf("%s, %v", b, err)
	}
}

func TestReplayRecordAnswer(t *testing.T) {
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	r := ReplayRecord{At: at, Action: validAction(), Verdict: Ask, ReviewProbability: 1, Source: "rules/1", Approve: new(true),
		AnsweredAt: at.Add(3 * time.Second), DetailsOpened: new(true)}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.AnsweredAt, r.WaitedMS = at.Add(-time.Second), new(int64(-5))
	fields := fieldsOf(t, r.Validate(), CodeInvalidReplay)
	if !slices.Contains(fields, "answered_at") || !slices.Contains(fields, "waited_ms") {
		t.Errorf("fields %v", fields)
	}
}

// A case with a session counts under it, and a case without one under the
// UTC day it was received.
func TestSessionOf(t *testing.T) {
	late := time.Date(2026, 11, 2, 23, 30, 0, 0, time.FixedZone("UTC-2", -2*3600))
	a := validAction()
	if s := SessionOf(a, late); s != "session ses_9f" {
		t.Errorf("with a session: %q", s)
	}
	a.Actor.Session = ""
	if s := SessionOf(a, late); s != "day 2026-11-03" {
		t.Errorf("without a session: %q, want the UTC day", s)
	}
	a.Actor = nil
	if SessionOf(a, late) != SessionOf(a, late.Add(10*time.Minute)) || SessionOf(a, late) == SessionOf(a, late.Add(24*time.Hour)) {
		t.Error("the cases of one UTC day are not one session")
	}
}

// The settings of consent are 0 or more, and the meet takes the greater of
// each (spec 011, criterion 12).
func TestConsentOfAProfile(t *testing.T) {
	good := Profile{Org: "o", Costs: DefaultCosts, AuditRate: 0.05, Consent: Consent{MinWeight: 5, MinSessions: 3}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Consent = Consent{MinWeight: -1, MinSessions: -2}
	if fields := fieldsOf(t, bad.Validate(), CodeInvalidProfile); !slices.Equal(fields, []string{"consent.min_weight", "consent.min_sessions"}) {
		t.Errorf("fields %v", fields)
	}
	org := saved(t, Scope{Org: "o"}, careful, 0.05, Certify{})
	org.Consent = Consent{MinWeight: 4}
	own := saved(t, Scope{Org: "o", Subject: person}, careful, 0.05, Certify{})
	own.Consent = Consent{MinWeight: 2, MinSessions: 3}
	m := Stricter(org, own)
	if m.Consent != (Consent{MinWeight: 4, MinSessions: 3}) || m.Subject != person {
		t.Errorf("meet %+v", m)
	}
	own.Consent = Consent{MinWeight: 1}
	if m := Stricter(org, own); m != org {
		t.Errorf("a smaller setting changed the profile: %+v", m)
	}
}

// The meet of random settings of consent takes the greater of each.
func TestStricterConsentProperties(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 2026))
	for i := range 2000 {
		o := randomProfile(t, rng, Scope{Org: "o"})
		p := randomProfile(t, rng, Scope{Org: "o", Subject: person})
		o.Consent = Consent{MinWeight: rng.IntN(6), MinSessions: rng.IntN(4)}
		p.Consent = Consent{MinWeight: rng.IntN(6), MinSessions: rng.IntN(4)}
		m := Stricter(o, p)
		if m.Consent.MinWeight != max(o.Consent.MinWeight, p.Consent.MinWeight) || m.Consent.MinSessions != max(o.Consent.MinSessions, p.Consent.MinSessions) {
			t.Fatalf("%d: consent %+v of %+v and %+v", i, m.Consent, o.Consent, p.Consent)
		}
	}
}

// The vocabularies are listed in the order of their constants.
func TestVocabularies(t *testing.T) {
	if !slices.Equal(AskKinds(), []string{"unsure", "split", "short", "wider", "few"}) ||
		!slices.Equal(Holds(), []string{"egress", "protected_refs", "checkpoints"}) ||
		!slices.Equal(Repositories(), []string{"private", "public", "mixed"}) ||
		!slices.Equal(Surfaces(), []string{"prompt", "notification", "queue", "bulk", "rule"}) {
		t.Error("a vocabulary is not in the order of its constants")
	}
}

// A prediction and an explanation with a scope and a cover keep them over
// JSON, and a prediction without them, as FailClosed makes, has neither.
func TestCoverJSON(t *testing.T) {
	p := Prediction{Key: key, Verdict: Ask, AskKind: AskWider,
		Scope: Reach{Undo: UndoIrreversible, Crosses: Crosses{Remote: BoundYes}, TargetClass: Unknown, Contact: Unknown, TargetKind: "ref"},
		Cover: Cover{Weight: 1, Sessions: 1, MinWeight: MinWeight, MinSessions: MinSessions}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `"scope":{"undo":"irreversible","crosses":{"remote":true},"target_class":"unknown","contact":"unknown","target_kind":"ref"},"cover":{"covered":false,"weight":1,"sessions":1,"min_weight":3,"min_sessions":2}}`
	if !strings.HasSuffix(string(b), want) {
		t.Errorf("%s\nwant the suffix %s", b, want)
	}
	var back Prediction
	if err := json.Unmarshal(b, &back); err != nil || back != p {
		t.Errorf("read back %+v, %v", back, err)
	}
	b, _ = json.Marshal(Prediction{Key: key, Verdict: Ask})
	if strings.Contains(string(b), "scope") || strings.Contains(string(b), "cover") || strings.Contains(string(b), "minimum") {
		t.Errorf("a prediction without a scope: %s", b)
	}
	x := Explanation{Cover: ExplainedCover{Cover: p.Cover, Nearest: []Approval{{ActionID: "a1", Label: "publish: update", At: time.Unix(0, 0).UTC()}}}}
	b, _ = json.Marshal(x)
	if !strings.Contains(string(b), `"cover":{"covered":false,"weight":1,"sessions":1,"min_weight":3,"min_sessions":2,"nearest":[{"action_id":"a1","label":"publish: update","at":"1970-01-01T00:00:00Z"}]}`) {
		t.Errorf("%s", b)
	}
}
