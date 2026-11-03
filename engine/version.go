// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"strings"

	"latere.ai/x/limen/engine/featurize"
	"latere.ai/x/limen/engine/kernel"
	"latere.ai/x/limen/engine/store"
)

// Model names a version of the model an engine serves (specs 004 and
// 011): its featurizer, its kernel and hyperparameters, its likelihood and
// its label cap. It is the source of each prediction the engine makes. A
// model is keyed by organization, person and version, so the snapshots of
// two versions never mix, and an engine reads and writes the snapshots of
// its own version only.
type Model string

const (
	// Learned1 is version 1, which an engine serves by default:
	// features/1, the additive kernel of the research, the probit, and
	// the 512 most recent answers. Each answer trains it with its full
	// weight; the weight of an answer applies to the cover rule only.
	Learned1 Model = "learned/1"
	// Learned2 is version 2: features/2, which holds the scope of an
	// action, the same kernel and hyperparameters, the probit with the
	// weight of each answer under weights/2, and the 512 most recent
	// answers (spec 011).
	Learned2 Model = "learned/2"
)

// Version is the version of [Learned1], the model an engine serves by
// default, and the name of its snapshots in a store.
const Version = "1"

// Source is the source of a prediction of [Learned1].
const Source = string(Learned1)

// Models lists the model versions an engine can serve, oldest first.
func Models() []Model { return []Model{Learned1, Learned2} }

// ParseModel reads the name of a model version: learned/1 or learned/2,
// in any case and with surrounding space. The empty string is
// [Learned1].
func ParseModel(s string) (Model, error) {
	m := Model(strings.ToLower(strings.TrimSpace(s)))
	if m == "" {
		return Learned1, nil
	}
	if _, ok := versions[m]; !ok {
		return "", fmt.Errorf("%q is not a model version of Limen: use learned/1 or learned/2", s)
	}
	return m, nil
}

// version is what a model version fixes in an engine.
type version struct {
	model Model
	// snapshot names the snapshots of the version in a store.
	snapshot string
	// dimA and dimC are the widths of the vectors of its featurizer.
	dimA, dimC int
	// weighted fits each answer with its weight under weights/2.
	weighted bool
}

var versions = map[Model]version{
	Learned1: {model: Learned1, snapshot: Version, dimA: featurize.DimA, dimC: featurize.DimC},
	Learned2: {model: Learned2, snapshot: "2", dimA: featurize.DimA2, dimC: featurize.DimC2, weighted: true},
}

// versionOf is the version of the model m, the zero model being
// [Learned1]. An engine of a model this package does not know is a
// mistake of its caller, which [ParseModel] prevents.
func versionOf(m Model) version {
	if m == "" {
		m = Learned1
	}
	v, ok := versions[m]
	if !ok {
		panic(fmt.Sprintf("engine: %q is not a model version; read it with ParseModel", string(m)))
	}
	return v
}

// point is the case as the model of v sees it at time t. A case keeps its
// features/1 vectors and its scope, so the vectors of each version come
// from them: features/1 as kept, or featurized again from the stored
// action when the case has another featurizer, and features/2 from those
// vectors and the scope, its own or the one its records give (spec 011,
// migration).
func (v version) point(c store.Case, t int64) kernel.Point {
	f := featurize.Features{A: c.A, C: c.C}
	if c.Featurizer != featurize.Version {
		f = featurize.Of(c.Action)
	}
	if v.model == Learned2 {
		s, _ := reach(c)
		f = featurize.Of2(c.Action, f, s)
	}
	return kernel.Point{Tool: f.A, Ctx: f.C, T: float64(t)}
}

// fits reports whether a point has the widths of the version's vectors,
// so a snapshot of another version fails to load rather than predicting.
func (v version) fits(p kernel.Point) bool { return len(p.Tool) == v.dimA && len(p.Ctx) == v.dimC }
