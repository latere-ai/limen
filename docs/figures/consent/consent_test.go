// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package consent

import (
	"reflect"
	"testing"

	"latere.ai/x/limen"
)

// A run follows the cover rule: every allow is covered and above the
// allow threshold, a few or wider ask is an allow the cover turned, and a
// seed gives the same run.
func TestRun(t *testing.T) {
	o := Default(1)
	o.Decisions = 200
	recs, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	th, _ := o.Costs.Thresholds()
	kinds := map[string]int{}
	for _, r := range recs {
		kinds[string(r.Suggested)+r.AskKind]++
		switch {
		case r.Suggested == limen.Allow && (!r.Covered || r.Approve <= th.AllowAbove):
			t.Fatalf("decision %d: an allow at %v with cover %v", r.T, r.Approve, r.Covered)
		case (r.AskKind == limen.AskFew || r.AskKind == limen.AskWider) && (r.Covered || r.Approve <= th.AllowAbove):
			t.Fatalf("decision %d: a %s ask at %v with cover %v", r.T, r.AskKind, r.Approve, r.Covered)
		}
	}
	if kinds["allow"] == 0 || kinds["askfew"] == 0 {
		t.Errorf("the run reaches too little of the rule: %v", kinds)
	}
	again, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recs, again) {
		t.Error("one seed gives two runs")
	}
}
