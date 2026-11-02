// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"latere.ai/x/limen"
)

var t0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// decidedAnswer is a record decided at t0 and answered after the delay, as a
// review, with how.
func decidedAnswer(t int64, approve bool, delay time.Duration, how limen.HowAnswered) Record {
	return Record{T: t, ActionID: "a" + strconv.FormatInt(t, 10), Session: "session s", Shown: true, DecidedAt: t0,
		Answers: []limen.RecordedAnswer{{Approve: approve, At: t0.Add(delay), Review: true, HowAnswered: how}}}
}

// Each rule of weights/1 gives weight 0 to an approval, a denial has
// weight 1 except from a rule, and the time is the smallest of the three
// times (spec 011, criterion 13).
func TestWeights(t *testing.T) {
	yes, no := new(true), new(false)
	ms := func(n int64) *int64 { return &n }
	lean, leanFor := 0.8, 0.3
	cases := []struct {
		name      string
		r         Record
		suggested *float64
		want      float64
	}{
		{"a slow approval", decidedAnswer(1, true, 3*time.Second, limen.HowAnswered{}), nil, 1},
		{"a quick approval", decidedAnswer(1, true, time.Second, limen.HowAnswered{}), nil, 0},
		{"a quick approval with details", decidedAnswer(1, true, time.Second, limen.HowAnswered{DetailsOpened: yes}), nil, 1},
		{"a quick approval, details offered and closed", decidedAnswer(1, true, time.Second, limen.HowAnswered{DetailsOpened: no}), nil, 0},
		{"the client measured it quick", decidedAnswer(1, true, time.Minute, limen.HowAnswered{AnswerMS: ms(900)}), nil, 0},
		{"the decision point measured it quick", decidedAnswer(1, true, time.Minute, limen.HowAnswered{WaitedMS: ms(1500)}), nil, 0},
		{"a client cannot make it slower", decidedAnswer(1, true, time.Second, limen.HowAnswered{AnswerMS: ms(60000), WaitedMS: ms(60000)}), nil, 0},
		{"at the floor", decidedAnswer(1, true, limen.AnswerFloor, limen.HowAnswered{}), nil, 1},
		{"a quick denial", decidedAnswer(1, false, 0, limen.HowAnswered{}), nil, 1},
		{"a denial by a rule", decidedAnswer(1, false, time.Minute, limen.HowAnswered{Surface: limen.SurfaceRule}), nil, 0},
		{"an approval by a rule", decidedAnswer(1, true, time.Minute, limen.HowAnswered{Surface: limen.SurfaceRule, DetailsOpened: yes}), nil, 0},
		{"one gesture for many", decidedAnswer(1, true, time.Minute, limen.HowAnswered{Surface: limen.SurfaceBulk}), nil, 0},
		{"one gesture with details", decidedAnswer(1, true, time.Minute, limen.HowAnswered{Surface: limen.SurfaceBulk, DetailsOpened: yes}), nil, 1},
		{"it followed the lean", decidedAnswer(1, true, time.Minute, limen.HowAnswered{SuggestionShown: yes}), &lean, 0},
		{"it went against the lean", decidedAnswer(1, true, time.Minute, limen.HowAnswered{SuggestionShown: yes}), &leanFor, 1},
		{"no lean was shown", decidedAnswer(1, true, time.Minute, limen.HowAnswered{SuggestionShown: no}), &lean, 1},
		{"a lean with no prediction", decidedAnswer(1, true, time.Minute, limen.HowAnswered{SuggestionShown: yes}), nil, 1},
		{"the lean with details", decidedAnswer(1, true, time.Minute, limen.HowAnswered{SuggestionShown: yes, DetailsOpened: yes}), &lean, 1},
		{"other surfaces", decidedAnswer(1, true, time.Minute, limen.HowAnswered{Surface: limen.SurfaceNotification}), nil, 1},
	}
	for _, c := range cases {
		c.r.Suggested = c.suggested
		if w := DefaultPolicy().Weigh([]Record{c.r}); w[0].Latest != c.want || w[0].Review != c.want {
			t.Errorf("%s: weights %+v, want %v", c.name, w[0], c.want)
		}
	}
	// No time is known: the rule "too quick" does not apply.
	r := decidedAnswer(1, true, 0, limen.HowAnswered{})
	r.DecidedAt = time.Time{}
	if w := DefaultPolicy().Weigh([]Record{r}); w[0].Latest != 1 {
		t.Errorf("no time known: %+v", w[0])
	}
	// A floor of zero turns the rule off.
	if w := (Policy{Fatigue: 50}).Weigh([]Record{decidedAnswer(1, true, 0, limen.HowAnswered{})}); w[0].Latest != 1 {
		t.Errorf("floor 0: %+v", w[0])
	}
	// A record with no answer has no weight.
	if w := DefaultPolicy().Weigh([]Record{{T: 1}}); w[0] != (Weights{}) {
		t.Errorf("no answer: %+v", w[0])
	}
}

// The position of an answer counts the reviews of shown cases in its
// session before it, and the fatigue rule gives weight 0 from the fatigue
// count on, unless the details were opened.
func TestFatigue(t *testing.T) {
	var recs []Record
	for i := range 6 {
		r := decidedAnswer(int64(i), true, time.Duration(i+3)*time.Second, limen.HowAnswered{})
		recs = append(recs, r)
	}
	// Another session, a case nobody was shown, and an equal time ordered
	// by the case.
	other := decidedAnswer(10, true, 4*time.Second, limen.HowAnswered{})
	other.Session = "session other"
	hidden := decidedAnswer(11, true, 4*time.Second, limen.HowAnswered{})
	hidden.Shown = false
	recs = append(recs, other, hidden)
	w := Policy{Floor: limen.AnswerFloor, Fatigue: 3}.Weigh(recs)
	want := []float64{1, 1, 1, 0, 0, 0, 1, 1}
	for i, x := range w {
		if x.Latest != want[i] {
			t.Errorf("record %d: weight %v, want %v", i, x.Latest, want[i])
		}
	}
	// The case's own review is not counted for a later answer.
	again := decidedAnswer(0, true, 3*time.Second, limen.HowAnswered{})
	again.Answers = append(again.Answers, limen.RecordedAnswer{Approve: true, At: t0.Add(time.Hour)})
	if w := (Policy{Fatigue: 1}).Weigh([]Record{again}); w[0].Latest != 1 || w[0].Review != 1 {
		t.Errorf("own review counted: %+v", w[0])
	}
	recs[5].Answers[0].DetailsOpened = new(true)
	if w := (Policy{Fatigue: 3}).Weigh(recs); w[5].Latest != 1 {
		t.Errorf("details opened late in a session: %+v", w[5])
	}
	if w := (Policy{}).Weigh(recs); w[4].Latest != 1 {
		t.Errorf("fatigue 0 turns the rule off: %+v", w[4])
	}
}

var (
	update = limen.Reach{Undo: limen.UndoRecoverable, Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundNo}, TargetClass: limen.ClassOwn, Contact: limen.ContactRepeat}
	public = limen.Reach{Undo: limen.UndoIrreversible, Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundYes}, TargetClass: limen.ClassOwn, Contact: limen.ContactRepeat}
	set    = Settings{MinWeight: 3, MinSessions: 2, Window: 512}
)

// approval is a considered approval of the family in the session with
// the scope.
func approval(t int64, family, session string, s limen.Reach) Record {
	r := decidedAnswer(t, true, time.Minute, limen.HowAnswered{})
	r.Families, r.Session, r.Scope, r.Label = []string{family}, session, s, family
	return r
}

func cover(fams []string, s limen.Reach, recs []Record) (limen.Cover, bool) {
	return Cover(fams, s, recs, DefaultPolicy().Weigh(recs), set)
}

// An action is covered if and only if, in each family, its covering
// approvals have the weight and the sessions (spec 011, criterion 8).
func TestCover(t *testing.T) {
	pub := []string{"publish"}
	var recs []Record
	check := func(what string, s limen.Reach, covered, wider bool, weight float64, sessions int) {
		t.Helper()
		c, w := cover(pub, s, recs)
		want := limen.Cover{Covered: covered, Weight: weight, Sessions: sessions, MinWeight: 3, MinSessions: 2}
		if c != want || w != wider {
			t.Errorf("%s: %+v wider %v, want %+v wider %v", what, c, w, want, wider)
		}
	}
	check("no approval", update, false, true, 0, 0)
	recs = append(recs, approval(1, "publish", "session a", update))
	check("one approval", update, false, false, 1, 1)
	check("one approval, a wider action", public, false, true, 0, 0)
	recs = append(recs, approval(2, "publish", "session a", update), approval(3, "publish", "session a", update))
	check("three approvals in one session", update, false, false, 3, 1)
	quick := approval(4, "publish", "session b", update)
	quick.Answers[0].At = t0.Add(time.Second)
	recs = append(recs, quick)
	check("a quick approval in another session", update, false, false, 3, 1)
	denied := approval(5, "publish", "session c", update)
	denied.Answers[0].Approve = false
	other := approval(6, "deploy", "session d", update)
	replaced := approval(7, "publish", "session e", update)
	replaced.Answers = append(replaced.Answers, limen.RecordedAnswer{Approve: false, At: t0.Add(2 * time.Minute)})
	recs = append(recs, denied, other, replaced)
	check("a denial, another family and a replaced approval", update, false, false, 3, 1)
	day := approval(8, "publish", limen.SessionOf(limen.Action{}, t0), update)
	recs = append(recs, day)
	check("an approval without a session, its day", update, true, false, 4, 2)
	// A wider approval covers a narrower action.
	check("a public action", public, false, true, 0, 0)
	recs = append(recs, approval(9, "publish", "session f", public))
	check("one public approval", public, false, false, 1, 1)
	c, _ := cover(pub, update, recs)
	if !c.Covered || c.Weight != 5 || c.Sessions != 3 {
		t.Errorf("a wider approval does not cover: %+v", c)
	}
	// The window keeps the most recent answered cases.
	c, _ = Cover(pub, update, recs, DefaultPolicy().Weigh(recs), Settings{MinWeight: 3, MinSessions: 2, Window: 2})
	if c.Covered || c.Weight != 2 || c.Sessions != 2 {
		t.Errorf("a window of two: %+v", c)
	}
	// Settings of zero: covered with no approval.
	c, w := Cover([]string{"web_fetch"}, update, nil, nil, Settings{})
	if !c.Covered || w {
		t.Errorf("no settings: %+v, wider %v", c, w)
	}
	c, w = Cover(nil, update, recs, DefaultPolicy().Weigh(recs), set)
	if c.Covered || !w {
		t.Errorf("no family: %+v, wider %v", c, w)
	}
}

// A chain needs approvals that cover each of its families, and the cover
// reports the family that misses a setting.
func TestCoverOfAChain(t *testing.T) {
	recs := []Record{
		approval(1, "go test", "session a", update), approval(2, "go test", "session b", update), approval(3, "go test", "session c", update),
		approval(4, "git push", "session a", update), approval(5, "git push", "session a", update), approval(6, "git push", "session a", update),
		approval(7, "git push", "session a", update),
	}
	c, w := cover([]string{"go test", "git push"}, update, recs)
	if c.Covered || w || c.Weight != 4 || c.Sessions != 1 {
		t.Errorf("the family that misses: %+v, wider %v", c, w)
	}
	c, w = cover([]string{"go test", "make"}, update, recs)
	if c.Covered || !w || c.Weight != 0 {
		t.Errorf("a family with no approval: %+v, wider %v", c, w)
	}
	recs = append(recs, approval(8, "git push", "session b", update))
	c, _ = cover([]string{"go test", "git push", "go test"}, update, recs)
	if !c.Covered || c.Weight != 3 || c.Sessions != 3 {
		t.Errorf("both families met: %+v", c)
	}
	// A chain covers its parts.
	chain := approval(9, "go test", "session z", public)
	chain.Families = []string{"go test", "rm -r"}
	recs = []Record{chain}
	if c, w := cover([]string{"rm -r"}, update, recs); c.Weight != 1 || w {
		t.Errorf("a chain does not cover its part: %+v, wider %v", c, w)
	}
}

func TestNearest(t *testing.T) {
	far := public
	far.TargetClass = limen.ClassPrimary
	recs := []Record{
		approval(1, "publish", "s", limen.Reach{Undo: limen.UndoNone, Crosses: limen.Crosses{Remote: limen.BoundNo}, TargetClass: limen.ClassScratch}),
		approval(2, "publish", "s", update),
		approval(3, "publish", "s", update),
		approval(4, "publish", "s", far),
		approval(5, "deploy", "s", update),
	}
	recs[2].Answers[0].At = t0.Add(time.Hour)
	got := Nearest([]string{"publish"}, public, recs, set, 3)
	var ids []string
	for _, a := range got {
		ids = append(ids, a.ActionID)
	}
	if !slices.Equal(ids, []string{"a3", "a2", "a1"}) || got[0].At != t0.Add(time.Hour) || got[0].Label != "publish" {
		t.Errorf("nearest %+v", got)
	}
	if got := Nearest([]string{"publish"}, public, recs, set, 1); len(got) != 1 {
		t.Errorf("a limit of one: %+v", got)
	}
	if got := Nearest([]string{"other"}, public, recs, set, 3); len(got) != 0 {
		t.Errorf("another family: %+v", got)
	}
}

func TestDisagree(t *testing.T) {
	a := approval(1, "publish", "s", update)
	d := approval(2, "publish", "s", update)
	d.Answers[0].Approve = false
	o := approval(3, "deploy", "s", update)
	o.Answers[0].Approve = false
	if Disagree([]string{"publish"}, []Record{a, o}, set) {
		t.Error("agreeing answers of one family disagree")
	}
	if !Disagree([]string{"publish"}, []Record{a, d}, set) || !Disagree([]string{"deploy", "publish"}, []Record{a, d, o}, set) {
		t.Error("an approval and a denial of one family agree")
	}
	if Disagree([]string{"publish"}, nil, set) {
		t.Error("no answers disagree")
	}
	if Disagree([]string{"publish"}, []Record{a, d}, Settings{Window: 1}) {
		t.Error("the window is not read")
	}
}

// The reason of an ask holds no probability and no decimal number
// (spec 011, criterion 14).
func TestAskReason(t *testing.T) {
	decimal := regexp.MustCompile(`\d\.\d|probability`)
	for _, kind := range append(limen.AskKinds(), "") {
		for _, c := range []limen.Cover{{MinWeight: 3, MinSessions: 2}, {MinWeight: 1}, {MinSessions: 1}} {
			r := AskReason(kind, "publish: make public", c)
			if r == "" || decimal.MatchString(r) {
				t.Errorf("%s: reason %q", kind, r)
			}
		}
	}
	if r := AskReason(limen.AskFew, "x", limen.Cover{MinWeight: 3, MinSessions: 2}); r != "This person allowed actions of this reach. Limen needs 3 considered approvals from 2 sessions before it suggests allow. Limen suggests ask." {
		t.Errorf("few: %q", r)
	}
	if r := AskReason(limen.AskFew, "x", limen.Cover{MinWeight: 1}); r != "This person allowed actions of this reach. Limen needs 1 considered approval before it suggests allow. Limen suggests ask." {
		t.Errorf("few without sessions: %q", r)
	}
	if r := AskReason(limen.AskFew, "x", limen.Cover{MinSessions: 1}); r != "This person allowed actions of this reach. Limen needs approvals from 1 session before it suggests allow. Limen suggests ask." {
		t.Errorf("few without weight: %q", r)
	}
	if r := AskReason(limen.AskWider, "publish: make public", limen.Cover{}); r != `This person never allowed an action of this reach: "publish: make public". Limen suggests ask.` {
		t.Errorf("wider: %q", r)
	}
}

// No estimate and no certificate changes when the weights change: only
// the count of considered audits does (spec 011, criterion 15).
func TestWeightsDoNotMoveEstimates(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 11))
	for range 200 {
		var rows []Row
		for range 1 + rng.IntN(40) {
			v := []limen.Verdict{limen.Allow, limen.Block}[rng.IntN(2)]
			review := []float64{0, 0.05, 0.5, 1}[rng.IntN(4)]
			r := Row{Verdict: v, Review: review, Shown: review > 0 && rng.IntN(3) > 0}
			r.Reviewed = r.Shown && rng.IntN(4) > 0
			r.Approve = rng.IntN(5) > 0
			rows = append(rows, r)
		}
		all, none := slices.Clone(rows), slices.Clone(rows)
		for i := range all {
			all[i].Considered, none[i].Considered = true, false
		}
		for _, side := range []Side{FalseAllow, FalseBlock} {
			a, n := Rate(all, side), Rate(none, side)
			if value(a) != value(n) || a.Reviews != n.Reviews || a.Covered != n.Covered {
				t.Fatalf("a rate moved with the weights: %+v, %+v", a, n)
			}
		}
		ca, cn := Certificate(all, limen.Certify{}), Certificate(none, limen.Certify{})
		if ca.Clean != cn.Clean || ca.Holds != cn.Holds || ca.Considered != ca.Clean || cn.Considered != 0 {
			t.Fatalf("the certificate moved with the weights: %+v, %+v", ca, cn)
		}
	}
}

// value is the estimate's value, or -1 for null.
func value(e limen.Estimate) float64 {
	if e.Value == nil {
		return -1
	}
	return *e.Value
}
