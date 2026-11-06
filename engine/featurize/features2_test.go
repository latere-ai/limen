// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"encoding/json/jsontext"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"latere.ai/x/limen"
)

// of2 is features/2 of an action as Limen makes it for a new case.
func of2(a limen.Action) Features { return Of2(a, Of(a), Read(a, "").Scope) }

func finite2(t *testing.T, f Features) {
	t.Helper()
	if len(f.A) != DimA2 || len(f.C) != DimC2 {
		t.Fatalf("dimensions %d, %d, want %d, %d", len(f.A), len(f.C), DimA2, DimC2)
	}
	for _, v := range append(slices.Clone(f.A), f.C...) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("non-finite feature in %v %v", f.A, f.C)
		}
	}
}

// The groups of features/2 tile its 63 and 61 dimensions in the order of
// the table of spec 011 (criterion 18).
func TestGroupLayout2(t *testing.T) {
	if a2Undo != a2Effect+4 || a2Kind != a2Undo+3 || a2Tokens != a2Kind+kindBuckets || a2Classes != a2Tokens+tokenBuckets ||
		a2Actor != a2Classes+numClasses || a2Crosses != a2Actor+3 || a2Crosses+5 != DimA2 {
		t.Error("the groups of A do not tile its 63 dimensions")
	}
	if c2TargetKind != c2Sensitivity+1 || c2Class != c2TargetKind+kindBuckets || c2Env != c2Class+3 || c2Credential != c2Env+4 ||
		c2Holds != c2Credential+1 || c2Contact != c2Holds+3 || c2Attributes != c2Contact+1 || c2Signals != c2Attributes+attrBuckets ||
		c2Signals+3*signalBuckets != DimC2 {
		t.Error("the groups of C do not tile its 61 dimensions")
	}
	if Version2 != "features/2" {
		t.Error(Version2)
	}
}

// The groups that features/1 and features/2 share have the same values,
// and the scope and the holds have the encodings of the table.
func TestEncodings2(t *testing.T) {
	a := shell("git push --force origin main")
	a.Effect = limen.EffectExternal
	a.Target = &limen.Target{Kind: "ref", Sensitivity: ptr(0.7)}
	a.Environment = &limen.Environment{Kind: "sandbox", Credentialed: true, Holds: []string{limen.HoldEgress, limen.HoldCheckpoints},
		Repositories: limen.RepositoriesPrivate}
	a.Actor = &limen.Actor{Kind: "agent"}
	a.Attributes = map[string]any{"files": 3}
	a.Signals = []limen.Signal{{Source: "rules/1", Score: ptr(0.4)}}
	f1, s := Of(a), Read(a, limen.ContactFirst).Scope
	f := Of2(a, f1, s)
	finite2(t, f)
	if s.Undo != limen.UndoIrreversible || s.TargetClass != limen.ClassPrimary || s.Crosses.Remote != limen.BoundYes ||
		s.Crosses.Public != limen.BoundNo || s.Crosses.Machine != limen.BoundNo || s.Crosses.Network != limen.BoundNo {
		t.Fatalf("scope %+v", s)
	}
	same := func(name string, got, want []float64) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	same("effect", f.A[a2Effect:a2Undo], f1.A[aEffect:aIrrev])
	same("kind to actor", f.A[a2Kind:a2Crosses], f1.A[aKind:DimA])
	same("sensitivity and target kind", f.C[c2Sensitivity:c2Class], f1.C[cSensitivity:cEnv])
	same("environment and credentialed", f.C[c2Env:c2Holds], f1.C[cEnv:cDestructive])
	same("attributes and signals", f.C[c2Attributes:DimC2], f1.C[cAttributes:DimC])
	same("undo", f.A[a2Undo:a2Kind], []float64{step, step, step})
	// machine no, remote yes, network no (egress), organization unknown,
	// public no (private repositories).
	same("crosses", f.A[a2Crosses:DimA2], []float64{0, math.Sqrt2, 0, half, 0})
	same("target class", f.C[c2Class:c2Env], []float64{step, step, step})
	same("holds", f.C[c2Holds:c2Contact], []float64{step, 0, step})
	same("contact", f.C[c2Contact:c2Attributes], []float64{math.Sqrt2})

	for _, c := range []struct {
		o    limen.Order
		w    string
		want []float64
	}{
		{limen.Undos(), limen.UndoNone, []float64{0, 0, 0}},
		{limen.Undos(), limen.UndoReversible, []float64{step, 0, 0}},
		{limen.Undos(), limen.UndoRecoverable, []float64{step, step, 0}},
		{limen.Undos(), limen.Unknown, []float64{step / 2, step / 2, step / 2}},
		{limen.Undos(), "", []float64{step / 2, step / 2, step / 2}},
		{limen.Undos(), "often", []float64{step / 2, step / 2, step / 2}},
		{limen.TargetClasses(), limen.ClassScratch, []float64{0, 0, 0}},
		{limen.TargetClasses(), limen.ClassOwn, []float64{step, 0, 0}},
		{limen.TargetClasses(), limen.ClassShared, []float64{step, step, 0}},
	} {
		same(c.w, thermometer(nil, c.o, c.w), c.want)
	}
	for b, want := range map[limen.Bound]float64{limen.BoundNo: 0, limen.BoundYes: math.Sqrt2, limen.BoundUnknown: half, "maybe": half} {
		if got := boundary(b); got != want {
			t.Errorf("boundary %q: %v, want %v", b, got, want)
		}
	}
	for c, want := range map[string]float64{limen.ContactRepeat: 0, limen.ContactFirst: math.Sqrt2, limen.Unknown: half, "": half, "twice": half} {
		s := limen.Reach{Contact: c}
		if got := Of2(limen.Action{}, Of(limen.Action{}), s).C[c2Contact]; got != want {
			t.Errorf("contact %q: %v, want %v", c, got, want)
		}
	}
}

// Of2 reads no input: the features/1 vectors and the scope that a case
// keeps give it the features/2 vectors it got when it was new, and a
// vector that is not of features/1 is made again from the action.
func TestOf2ReadsNoInput(t *testing.T) {
	r := rand.New(rand.NewPCG(18, 2))
	for range 500 {
		a := randomAction(r)
		f1, s := Of(a), Read(a, "").Scope
		fresh := Of2(a, f1, s)
		stored := a
		stored.Input = nil
		kept := Of2(stored, Features{A: slices.Clone(f1.A), C: slices.Clone(f1.C)}, s)
		if !slices.Equal(fresh.A, kept.A) || !slices.Equal(fresh.C, kept.C) {
			t.Fatalf("%+v: a stored case gets other vectors", a)
		}
		again := Of2(a, Features{A: f1.A[:3]}, s)
		want := Of2(a, Of(stored), s)
		if !slices.Equal(again.A, want.A) || !slices.Equal(again.C, want.C) {
			t.Fatalf("%+v: a damaged features/1 vector is not made again from the action", a)
		}
	}
}

// Every group of features/2 meets the scaling rule (criterion 18): over
// many actions, and over each pair of values of each scope group, no two
// differ by more than squared distance 2 within one group.
func TestGroupDistanceBound2(t *testing.T) {
	type group struct {
		name   string
		inA    bool
		lo, hi int
	}
	groups := []group{
		{"effect", true, a2Effect, a2Undo}, {"undo", true, a2Undo, a2Kind},
		{"kind", true, a2Kind, a2Tokens}, {"name tokens", true, a2Tokens, a2Classes},
		{"program class", true, a2Classes, a2Actor}, {"actor", true, a2Actor, a2Crosses},
		{"sensitivity", false, c2Sensitivity, c2TargetKind}, {"target kind", false, c2TargetKind, c2Class},
		{"target class", false, c2Class, c2Env}, {"environment", false, c2Env, c2Credential},
		{"credentialed", false, c2Credential, c2Holds}, {"holds", false, c2Holds, c2Contact},
		{"contact", false, c2Contact, c2Attributes}, {"attributes", false, c2Attributes, c2Signals},
		{"signals", false, c2Signals, DimC2},
	}
	for i := range 5 {
		groups = append(groups, group{"crosses " + []string{"machine", "remote", "network", "organization", "public"}[i], true, a2Crosses + i, a2Crosses + i + 1})
	}
	r := rand.New(rand.NewPCG(1, 2))
	pick := func(xs ...string) string { return xs[r.IntN(len(xs))] }
	bound := func() limen.Bound { return limen.Bound(pick("", "no", "yes")) }
	var fs []Features
	for range 300 {
		a := randomAction(r)
		if r.IntN(2) == 0 {
			a.Environment = &limen.Environment{Holds: []string{pick(limen.Holds()...), pick(limen.Holds()...), pick("", limen.HoldCheckpoints)}}
		}
		s := Read(a, pick("", limen.ContactFirst, limen.ContactRepeat)).Scope
		if r.IntN(2) == 0 {
			s = limen.Reach{Undo: pick(append(limen.Undos(), limen.Unknown)...), TargetClass: pick(append(limen.TargetClasses(), limen.Unknown)...),
				Contact: pick(limen.ContactFirst, limen.ContactRepeat, limen.Unknown),
				Crosses: limen.Crosses{Machine: bound(), Remote: bound(), Network: bound(), Organization: bound(), Public: bound()}}
		}
		f := Of2(a, Of(a), s)
		finite2(t, f)
		fs = append(fs, f)
	}
	// Each pair of values of each dimension of the scope, and of the holds.
	empty := Of(limen.Action{})
	for _, u := range append(limen.Undos(), limen.Unknown) {
		for _, c := range append(limen.TargetClasses(), limen.Unknown) {
			for _, k := range []string{limen.ContactFirst, limen.ContactRepeat, limen.Unknown} {
				for _, b := range []limen.Bound{limen.BoundNo, limen.BoundYes, limen.BoundUnknown} {
					s := limen.Reach{Undo: u, TargetClass: c, Contact: k, Crosses: limen.Crosses{Machine: b, Remote: b, Network: b, Organization: b, Public: b}}
					fs = append(fs, Of2(limen.Action{}, empty, s))
				}
			}
		}
	}
	for mask := range 8 {
		var hs []string
		for i, h := range limen.Holds() {
			if mask&(1<<i) != 0 {
				hs = append(hs, h)
			}
		}
		fs = append(fs, Of2(limen.Action{Environment: &limen.Environment{Holds: hs}}, empty, limen.Reach{}))
	}
	for _, g := range groups {
		var worst float64
		for i := range fs {
			for j := range i {
				a, b := fs[i].C, fs[j].C
				if g.inA {
					a, b = fs[i].A, fs[j].A
				}
				worst = math.Max(worst, sub2(a[g.lo:g.hi], b[g.lo:g.hi]))
			}
		}
		if worst > 2+1e-9 {
			t.Errorf("%s: squared distance %v exceeds 2", g.name, worst)
		}
		if worst < 0.5 {
			t.Errorf("%s: squared distance at most %v, so the test does not vary it", g.name, worst)
		}
	}
}

// The factors of spec 011: one boundary that differs multiplies k_a by
// about 0.44, and publish: update and publish: make public, which differ in
// undo and crosses.public, have k_a of about 0.33.
func TestScopeFactors(t *testing.T) {
	const lTool = 1.1 // the lengthscale of the tool block of learned/2
	ka := func(x, y Features) float64 { return math.Exp(-sub2(x.A, y.A) / (2 * lTool * lTool)) }
	pub := func(c limen.ReachClaim) Features {
		a := limen.Action{Kind: "tool", Name: "publish", Effect: limen.EffectExternal, Target: &limen.Target{Kind: "app", Class: limen.ClassOwn}, Scope: &c}
		return of2(a)
	}
	update := pub(limen.ReachClaim{Undo: limen.UndoRecoverable, Contact: limen.ContactRepeat, Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundNo}})
	public := pub(limen.ReachClaim{Undo: limen.UndoIrreversible, Contact: limen.ContactRepeat, Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundYes}})
	if got := ka(update, public); math.Abs(got-0.33) > 0.005 {
		t.Errorf("k_a of publish: update and publish: make public: %.4f, want about 0.33", got)
	}
	// A boundary from no to yes; from unknown to yes is half the distance.
	inside := pub(limen.ReachClaim{Undo: limen.UndoRecoverable, Contact: limen.ContactRepeat, Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundNo, Machine: limen.BoundNo}})
	outside := pub(limen.ReachClaim{Undo: limen.UndoRecoverable, Contact: limen.ContactRepeat, Crosses: limen.Crosses{Remote: limen.BoundYes, Public: limen.BoundNo, Machine: limen.BoundYes}})
	if got := ka(inside, outside); math.Abs(got-0.44) > 0.005 {
		t.Errorf("k_a after one boundary: %.4f, want about 0.44", got)
	}
	if got := ka(update, outside); math.Abs(got-math.Exp(-0.5/(2*lTool*lTool))) > 1e-12 {
		t.Errorf("k_a from an unknown boundary to yes: %.4f", got)
	}
}

func FuzzOf2(f *testing.F) {
	f.Add("tool", "bash", `{"command":"rm -rf ~/x && git push -f origin main"}`, "irreversible", "primary", "first", "egress", true)
	f.Add("", "", "", "", "", "", "", false)
	f.Add("tool", "publish", `{}`, "sometimes", "galaxy", "twice", "moat", true)
	f.Fuzz(func(t *testing.T, kind, name, input, undo, class, contact, hold string, damaged bool) {
		a := limen.Action{Kind: kind, Name: name, Input: jsontext.Value(input), Effect: limen.EffectExternal,
			Scope:       &limen.ReachClaim{Undo: undo, Contact: contact},
			Target:      &limen.Target{Kind: kind, Class: class},
			Environment: &limen.Environment{Holds: []string{hold}}}
		f1 := Of(a)
		if damaged {
			f1.C = nil
		}
		finite2(t, Of2(a, f1, Read(a, contact).Scope))
		finite2(t, Of2(a, f1, limen.Reach{Undo: undo, TargetClass: class, Contact: contact}))
	})
}
