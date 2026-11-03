// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"
)

// Bin i holds [i/10, (i+1)/10), the last bin also 1, and a probability
// outside [0, 1] is in no bin.
func TestCalibrationBinOf(t *testing.T) {
	for i := range CalibrationBins {
		from, to := CalibrationEdges(i)
		if from != float64(i)/10 || to != float64(i+1)/10 {
			t.Errorf("bin %d: edges %v, %v", i, from, to)
		}
		if got := CalibrationBinOf(from); got != i {
			t.Errorf("the lower edge %v is in bin %d, want %d", from, got, i)
		}
		if got := CalibrationBinOf(math.Nextafter(to, 0)); got != i {
			t.Errorf("just below %v is in bin %d, want %d", to, got, i)
		}
	}
	for p, want := range map[float64]int{1: 9, 0.3: 3, 0.7: 7, 0.6: 6, 0.29999999999999993: 2, -0.1: -1, 1.1: -1} {
		if got := CalibrationBinOf(p); got != want {
			t.Errorf("CalibrationBinOf(%v) = %d, want %d", p, got, want)
		}
	}
	if got := CalibrationBinOf(math.NaN()); got != -1 {
		t.Errorf("NaN is in bin %d", got)
	}
	// Every probability is in the bin whose edges hold it.
	for k := range 100001 {
		p := float64(k) / 100000
		i := CalibrationBinOf(p)
		from, to := CalibrationEdges(i)
		if p < from || (p >= to && i < CalibrationBins-1) {
			t.Fatalf("%v is in bin %d, [%v, %v)", p, i, from, to)
		}
	}
}

// Each state of a bin by its definition (spec 011, criterion 16).
func TestCalibrationState(t *testing.T) {
	iv := func(lo, hi float64) *[2]float64 { return &[2]float64{lo, hi} }
	cases := []struct {
		name     string
		stated   float64
		observed *float64
		interval *[2]float64
		want     string
	}{
		{"no observed rate", 0.95, nil, nil, NotEstimable},
		{"no interval", 0.95, new(0.9), nil, NotEstimable},
		{"inside", 0.95, new(0.93), iv(0.9, 0.97), Calibrated},
		{"on the lower edge", 0.9, new(0.93), iv(0.9, 0.97), Calibrated},
		{"on the upper edge", 0.97, new(0.93), iv(0.9, 0.97), Calibrated},
		{"surer above 1/2", 0.95, new(0.8), iv(0.7, 0.88), Overconfident},
		{"surer below 1/2", 0.05, new(0.2), iv(0.12, 0.3), Overconfident},
		{"less sure above 1/2", 0.7, new(0.95), iv(0.9, 0.98), Underconfident},
		{"less sure below 1/2", 0.3, new(0.05), iv(0.01, 0.1), Underconfident},
		{"as far from 1/2 on the other side", 0.25, new(0.75), iv(0.625, 0.875), Underconfident},
	}
	for _, c := range cases {
		if got := CalibrationState(c.stated, c.observed, c.interval); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if !slices.Equal(CalibrationStates(), []string{"calibrated", "overconfident", "underconfident", "not_estimable"}) {
		t.Errorf("states %v", CalibrationStates())
	}
}

func TestCalibrationQueryValidate(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	good := CalibrationQuery{Org: "o", Subject: "s", From: from, To: from.Add(time.Hour)}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		edit  func(*CalibrationQuery)
		field string
	}{
		{func(q *CalibrationQuery) { q.Org = "" }, "org"},
		{func(q *CalibrationQuery) { q.Subject = "" }, "subject"},
		{func(q *CalibrationQuery) { q.From = time.Time{} }, "from"},
		{func(q *CalibrationQuery) { q.To = time.Time{} }, "to"},
		{func(q *CalibrationQuery) { q.To = q.From }, "to"},
	} {
		q := good
		c.edit(&q)
		if fields := fieldsOf(t, q.Validate(), CodeInvalidQuery); !slices.Contains(fields, c.field) {
			t.Errorf("%s: %v", c.field, fields)
		}
	}
	if fields := fieldsOf(t, CalibrationQuery{}.Validate(), CodeInvalidQuery); !slices.Equal(fields, []string{"org", "subject", "from", "to"}) {
		t.Errorf("an empty query: %v", fields)
	}
}

// A report on the wire: a null rate is null, not zero, and an interval goes
// with a rate.
func TestCalibrationJSON(t *testing.T) {
	from := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	c := Calibration{Org: "o", Subject: "s", From: from, To: from.Add(90 * 24 * time.Hour)}
	c.Bins = []CalibrationBin{
		{From: 0.8, To: 0.9, Cases: 40, Covered: 1, Stated: new(0.85), Reviews: 12, Considered: 9, State: NotEstimable},
		{From: 0.9, To: 1, Cases: 400, Covered: 1, Stated: new(0.95), Observed: new(0.96), Interval: &[2]float64{0.91, 0.98}, Reviews: 60, Considered: 41, State: Calibrated},
	}
	c.Error, c.Estimable = new(0.01), 400.0/440
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"org":"o","subject":"s","from":"2026-08-05T00:00:00Z","to":"2026-11-03T00:00:00Z","bins":[` +
		`{"from":0.8,"to":0.9,"cases":40,"covered":1,"stated":0.85,"observed":null,"reviews":12,"considered":9,"state":"not_estimable"},` +
		`{"from":0.9,"to":1,"cases":400,"covered":1,"stated":0.95,"observed":0.96,"interval":[0.91,0.98],"reviews":60,"considered":41,"state":"calibrated"}],` +
		`"error":0.01,"estimable":0.9090909090909091}`
	if string(b) != want {
		t.Errorf("\n got %s\nwant %s", b, want)
	}
	if got, ok := c.Bin(0.97); !ok || got.State != Calibrated {
		t.Errorf("Bin(0.97) = %+v, %v", got, ok)
	}
	if _, ok := c.Bin(0.1); ok {
		t.Error("a report of two bins has a bin for 0.1")
	}
	if _, ok := c.Bin(2); ok {
		t.Error("2 is in a bin")
	}
	x, err := json.Marshal(Explanation{Calibration: Overconfident})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(x, &m); err != nil || m["calibration"] != Overconfident {
		t.Errorf("an explanation: %s", x)
	}
	x, _ = json.Marshal(Explanation{})
	m = nil
	if err := json.Unmarshal(x, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["calibration"]; ok {
		t.Errorf("an explanation with no calibration: %s", x)
	}
}
