// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package estimate

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"time"

	"latere.ai/x/limen"
)

// The cover rule and the weight of an answer (spec 011) are shared
// arithmetic, like the estimates: the engine and the stub both build a
// [Record] for each case of a person and read the cover here, so the two
// give the same verdicts and covers for the same ledger. None of it reads
// a review probability, and no estimate reads a weight.

// Record is one case of a person as the cover rule and the weights read
// it.
type Record struct {
	// T orders the person's cases.
	T        int64
	ActionID string
	Label    string
	Families []string
	Scope    limen.Reach
	// Session is the case's [limen.SessionOf].
	Session string
	// Shown is true when the decision showed the action to a person.
	Shown bool
	// DecidedAt is when Limen recorded the decision, the start of the time
	// it measures to an answer; zero when it has none.
	DecidedAt time.Time
	// Suggested is the approval probability of the recorded prediction,
	// the lean a client could show; nil without a prediction.
	Suggested *float64
	Answers   []limen.RecordedAnswer
}

// latest is the record's latest answer, the one the model learns.
func (r Record) latest() (limen.RecordedAnswer, bool) {
	if len(r.Answers) == 0 {
		return limen.RecordedAnswer{}, false
	}
	return r.Answers[len(r.Answers)-1], true
}

// review is the record's review, the first answer to a shown action.
func (r Record) review() (limen.RecordedAnswer, bool) {
	for _, a := range r.Answers {
		if a.Review {
			return a, true
		}
	}
	return limen.RecordedAnswer{}, false
}

// Policy is the setting of weights/1: the floor of the time of an
// approval, and the fatigue count of the reviews before it in its session.
// A zero setting turns its rule off.
type Policy struct {
	Floor   time.Duration
	Fatigue int
}

// DefaultPolicy is weights/1 with the settings of spec 011.
func DefaultPolicy() Policy { return Policy{Floor: limen.AnswerFloor, Fatigue: limen.AnswerFatigue} }

// Weights are the weights of a record's latest answer and of its review,
// 0 or 1; zero for an answer the record does not have.
type Weights struct {
	Latest, Review float64
}

// Weigh gives the weights of each record's answers under p. A record's
// position counts the reviews of shown cases in its session before an
// answer, so Weigh reads every record of the person.
func (p Policy) Weigh(recs []Record) []Weights { return p.weigh(recs, p.weight) }

// weigh gives the weights of each record's answers under the policy
// weight, which reads the record, the answer and its position.
func (p Policy) weigh(recs []Record, weight func(Record, limen.RecordedAnswer, int) float64) []Weights {
	type mark struct {
		at time.Time
		t  int64
	}
	reviews := map[string][]mark{}
	for _, r := range recs {
		if rv, ok := r.review(); ok && r.Shown {
			reviews[r.Session] = append(reviews[r.Session], mark{rv.At, r.T})
		}
	}
	before := func(a, b mark) int { return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.t, b.t)) }
	for _, ms := range reviews {
		slices.SortFunc(ms, before)
	}
	position := func(r Record, a limen.RecordedAnswer) int {
		at := mark{a.At, r.T}
		n, _ := slices.BinarySearchFunc(reviews[r.Session], at, before)
		// The case's own review is not counted.
		if rv, ok := r.review(); ok && r.Shown && before(mark{rv.At, r.T}, at) < 0 {
			n--
		}
		return n
	}
	out := make([]Weights, len(recs))
	for i, r := range recs {
		if a, ok := r.latest(); ok {
			out[i].Latest = weight(r, a, position(r, a))
		}
		if a, ok := r.review(); ok {
			out[i].Review = weight(r, a, position(r, a))
		}
	}
	return out
}

// weight is the weight of one answer of r under weights/1 at its
// position: 0 if a rule applies, and 1 if none does.
func (p Policy) weight(r Record, a limen.RecordedAnswer, position int) float64 {
	if p.rules(r, a, position) != (rules{}) {
		return 0
	}
	return 1
}

// rules are the rules of the weight of an answer that apply to it.
type rules struct {
	// standing: a standing rule gave the answer. bulk: one gesture
	// answered more than one action. quick: the answer was faster than the
	// floor. lean: it followed a lean that the client showed. late: it came
	// at the fatigue count or later in its session.
	standing, bulk, quick, lean, late bool
}

// rules finds the rules that apply to one answer of r at its position. A
// denial has only the rule of a standing rule, because a habit gives
// approvals, not denials: a quick denial is evidence. An approval whose
// details were opened has no rule but that one.
func (p Policy) rules(r Record, a limen.RecordedAnswer, position int) rules {
	var f rules
	f.standing = a.Surface == limen.SurfaceRule
	if !a.Approve || (a.DetailsOpened != nil && *a.DetailsOpened) {
		return f
	}
	f.bulk = a.Surface == limen.SurfaceBulk
	if t, ok := answerTime(r, a); ok && p.Floor > 0 && t < p.Floor {
		f.quick = true
	}
	f.lean = a.SuggestionShown != nil && *a.SuggestionShown && r.Suggested != nil && *r.Suggested > 0.5
	f.late = p.Fatigue > 0 && position >= p.Fatigue
	return f
}

// answerTime is the time of an answer: the smallest of the time the
// client measured, the time the decision point measured, and the time
// Limen measured from the decision. ok is false when none is known.
func answerTime(r Record, a limen.RecordedAnswer) (time.Duration, bool) {
	var ts []time.Duration
	if a.AnswerMS != nil {
		ts = append(ts, time.Duration(*a.AnswerMS)*time.Millisecond)
	}
	if a.WaitedMS != nil {
		ts = append(ts, time.Duration(*a.WaitedMS)*time.Millisecond)
	}
	if !r.DecidedAt.IsZero() {
		ts = append(ts, max(a.At.Sub(r.DecidedAt), 0))
	}
	if len(ts) == 0 {
		return 0, false
	}
	return slices.Min(ts), true
}

// Settings are the settings of the cover rule that apply to an action:
// the weight and the sessions its covering approvals need, and the window,
// the number of the person's most recent answered cases the rule reads.
// A window of zero or less reads every answered case.
type Settings struct {
	MinWeight, MinSessions, Window int
}

// window is the indexes of the answered records in the window, the most
// recent last.
func window(recs []Record, n int) []int {
	var idx []int
	for i, r := range recs {
		if len(r.Answers) > 0 {
			idx = append(idx, i)
		}
	}
	slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(recs[a].T, recs[b].T) })
	if n > 0 && len(idx) > n {
		idx = idx[len(idx)-n:]
	}
	return idx
}

// families is fams with each family once, in order.
func families(fams []string) []string {
	var out []string
	for _, f := range fams {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// Cover is the cover rule for an action with the families fams and the
// scope s, over the person's records and their weights. An approval that
// covers the action is a record in the window whose latest answer
// approves, that shares a family with the action, and whose scope the
// action is within; its weight counts when it is above zero. The action
// is covered when, in each family, the covering approvals with weight have
// MinWeight or more in total from MinSessions or more sessions. wider is
// true when, in some family, no approval of any weight covers the action.
func Cover(fams []string, s limen.Reach, recs []Record, w []Weights, set Settings) (c limen.Cover, wider bool) {
	type family struct {
		weight   float64
		sessions int
		met      bool
	}
	in := window(recs, set.Window)
	var fs []family
	for _, f := range families(fams) {
		var fam family
		seen := map[string]bool{}
		covered := false
		for _, i := range in {
			r := recs[i]
			a, _ := r.latest()
			if !a.Approve || !slices.Contains(r.Families, f) || !s.Within(r.Scope) {
				continue
			}
			covered = true
			if w[i].Latest > 0 {
				fam.weight += w[i].Latest
				seen[r.Session] = true
			}
		}
		fam.sessions = len(seen)
		fam.met = fam.weight >= float64(set.MinWeight) && fam.sessions >= set.MinSessions
		wider = wider || !covered
		fs = append(fs, fam)
	}
	c = limen.Cover{Covered: len(fs) > 0, MinWeight: set.MinWeight, MinSessions: set.MinSessions}
	if len(fs) == 0 {
		// No family, so no approval can cover the action.
		return c, true
	}
	missed := slices.ContainsFunc(fs, func(f family) bool { return !f.met })
	first := true
	for _, f := range fs {
		c.Covered = c.Covered && f.met
		if missed && f.met {
			continue
		}
		if first || f.weight < c.Weight || (f.weight == c.Weight && f.sessions < c.Sessions) {
			c.Weight, c.Sessions, first = f.weight, f.sessions, false
		}
	}
	return c, wider && !c.Covered
}

// Nearest is up to n approved cases of a family of the action, in the
// window, whose scope the action with the scope s exceeds: closest first,
// the fewest dimensions in which the action exceeds the case, and then the
// most recent.
func Nearest(fams []string, s limen.Reach, recs []Record, set Settings, n int) []limen.Approval {
	type near struct {
		r       Record
		at      time.Time
		exceeds int
	}
	var ns []near
	fams = families(fams)
	for _, i := range window(recs, set.Window) {
		r := recs[i]
		a, _ := r.latest()
		if !a.Approve || !slices.ContainsFunc(fams, func(f string) bool { return slices.Contains(r.Families, f) }) {
			continue
		}
		if e := s.Exceeds(r.Scope); e > 0 {
			ns = append(ns, near{r, a.At, e})
		}
	}
	slices.SortStableFunc(ns, func(a, b near) int {
		return cmp.Or(cmp.Compare(a.exceeds, b.exceeds), b.at.Compare(a.at), cmp.Compare(b.r.T, a.r.T))
	})
	out := make([]limen.Approval, 0, min(n, len(ns)))
	for _, x := range ns[:min(n, len(ns))] {
		out = append(out, limen.Approval{ActionID: x.r.ActionID, Label: x.r.Label, At: x.at})
	}
	return out
}

// Disagree reports whether, in some family of fams, the latest answers of
// the answered records in the window hold an approval and a denial: an ask
// in the ask band on such evidence is [limen.AskSplit], and on agreeing
// evidence [limen.AskShort]. The weight of an answer does not count.
func Disagree(fams []string, recs []Record, set Settings) bool {
	in := window(recs, set.Window)
	for _, f := range families(fams) {
		var approved, denied bool
		for _, i := range in {
			if !slices.Contains(recs[i].Families, f) {
				continue
			}
			a, _ := recs[i].latest()
			approved = approved || a.Approve
			denied = denied || !a.Approve
		}
		if approved && denied {
			return true
		}
	}
	return false
}

// AskReason is the reason of an ask of the kind, for the person who reads
// the record: it names the evidence and the reach, and states no
// probability and no direction (spec 011). label is the label of the
// action, and c its cover. Every implementation words an ask with it.
func AskReason(kind, label string, c limen.Cover) string {
	switch kind {
	case limen.AskSplit:
		return "The answers of this person about actions like this do not agree. Limen suggests ask."
	case limen.AskShort:
		return "The answers of this person about actions like this agree, but they are not enough yet to settle this action. Limen suggests ask."
	case limen.AskWider:
		return fmt.Sprintf("This person never allowed an action of this reach: %q. Limen suggests ask.", label)
	case limen.AskFew:
		var need string
		switch {
		case c.MinSessions <= 0:
			need = plural(c.MinWeight, "considered approval")
		case c.MinWeight <= 0:
			need = "approvals from " + plural(c.MinSessions, "session")
		default:
			need = plural(c.MinWeight, "considered approval") + " from " + plural(c.MinSessions, "session")
		}
		return "This person allowed actions of this reach. Limen needs " + need + " before it suggests allow. Limen suggests ask."
	}
	return "Limen has few answers from this person about actions like this. Limen suggests ask."
}

// plural is n and the word, with an s for any n but one.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// Previewed is the verdict a preview gives a recorded prediction under the
// thresholds th: theirs, except that an allow is an ask where the
// prediction recorded a cover that does not cover it. A preview applies
// the cover rule as it was recorded and does not read it again. A
// prediction from before the cover rule has no cover, and is not held.
func Previewed(th limen.Thresholds, p limen.Prediction) limen.Verdict {
	v := th.Verdict(p.Approve)
	if v == limen.Allow && p.Cover != (limen.Cover{}) && !p.Cover.Covered {
		return limen.Ask
	}
	return v
}

// EvidenceCut is the evidence ratio below which an ask in the ask band is
// [limen.AskUnsure], and at or above which it is [limen.AskSplit] or
// [limen.AskShort] (specs 009 and 011).
const EvidenceCut = 0.5

// DefaultWindow is the window of the cover rule: the 512 most recent
// answered cases, the training set of the model (spec 004).
const DefaultWindow = 512

// Judged is the suggestion for an action: the verdict, the kind of an ask,
// and the cover.
type Judged struct {
	Verdict limen.Verdict
	Kind    string
	Cover   limen.Cover
}

// Judge is the suggestion for an action with the families fams and the
// scope s, whose model gives approve and the evidence ratio er, under the
// thresholds th, over the person's records and their weights. The
// thresholds give the verdict. An allow the approvals do not cover is an
// ask of kind wider or few, and an ask in the band is unsure, split or
// short. Every implementation judges with it, so two of them suggest
// alike for the same ledger and the same model.
func Judge(approve, er float64, th limen.Thresholds, fams []string, s limen.Reach, recs []Record, w []Weights, set Settings) Judged {
	j := Judged{Verdict: th.Verdict(approve)}
	var wider bool
	j.Cover, wider = Cover(fams, s, recs, w, set)
	switch {
	case j.Verdict == limen.Allow && !j.Cover.Covered:
		j.Verdict, j.Kind = limen.Ask, limen.AskFew
		if wider {
			j.Kind = limen.AskWider
		}
	case j.Verdict != limen.Ask:
	case er < EvidenceCut:
		j.Kind = limen.AskUnsure
	case Disagree(fams, recs, set):
		j.Kind = limen.AskSplit
	default:
		j.Kind = limen.AskShort
	}
	return j
}
