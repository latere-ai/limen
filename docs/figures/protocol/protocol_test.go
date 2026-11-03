// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package protocol

import (
	"reflect"
	"testing"

	"latere.ai/x/pkg/verdict"
)

// A run follows the protocol: the first action meets the prior and is
// asked, an ask is always shown, an automatic verdict is shown only as
// its audited form, each prediction is made on the answers before it, and
// a seed gives the same run.
func TestRun(t *testing.T) {
	o := Paper(3)
	o.Decisions = 120
	recs, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != o.Decisions {
		t.Fatalf("%d records, want %d", len(recs), o.Decisions)
	}
	if r := recs[0]; r.PHat != 0.5 || r.Verdict != verdict.Ask || !r.Shown || r.Answers != 0 {
		t.Errorf("first decision %+v, want the prior's ask, shown", r)
	}
	answered := 0
	for i, r := range recs {
		if r.Decision.T != i {
			t.Fatalf("record %d at time %d", i, r.Decision.T)
		}
		if r.Answers != min(answered, 512) {
			t.Fatalf("record %d predicted on %d answers, want %d", i, r.Answers, answered)
		}
		if r.Verdict != o.Thresholds.Verdict(r.PHat) {
			t.Fatalf("record %d: verdict %s for %v", i, r.Verdict, r.PHat)
		}
		switch {
		case r.Verdict == verdict.Ask && !r.Shown:
			t.Fatalf("record %d: an ask was not shown", i)
		case r.Verdict == verdict.Allow && r.Shown != (r.Applied == verdict.Flag):
			t.Fatalf("record %d: an allow applied as %s, shown %v", i, r.Applied, r.Shown)
		case r.Verdict == verdict.Block && r.Shown != (r.Applied == verdict.Ask):
			t.Fatalf("record %d: a block applied as %s, shown %v", i, r.Applied, r.Shown)
		}
		if r.Shown {
			answered++
		}
	}
	again, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recs, again) {
		t.Error("one seed gives two runs")
	}
}
