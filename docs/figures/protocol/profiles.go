// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package protocol

import "latere.ai/x/limen"

// Profile is a named cost profile.
type Profile struct {
	Name  string
	Costs limen.Costs
}

// Profiles are the three cost profiles a hosted console names, from the
// least strict: Balanced, Careful and Strict (specs/010-organizations.md,
// the table of named profiles). The limen package exports Careful alone,
// as limen.DefaultCosts; Balanced and Strict are the costs its tests use
// for those names (organizations_test.go). Each one's thresholds come
// from limen.Costs.Thresholds.
var Profiles = []Profile{
	{"Balanced", limen.Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}},
	{"Careful", limen.DefaultCosts},
	{"Strict", limen.Costs{FalseAllow: 20, FalseBlock: 4, Ask: 1}},
}

// Named returns the profile called name.
func Named(name string) Profile {
	for _, p := range Profiles {
		if p.Name == name {
			return p
		}
	}
	panic("protocol: no profile " + name)
}

// Thresholds are the profile's verdict thresholds.
func (p Profile) Thresholds() limen.Thresholds {
	th, err := p.Costs.Thresholds()
	if err != nil {
		panic(err)
	}
	return th
}
