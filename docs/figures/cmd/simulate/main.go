// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command simulate runs Limen's model against the paper's simulated
// person and writes the data render.py draws the figures from, into one
// directory:
//
//	decisions.csv.gz          every decision, additive kernel, Careful profile
//	decisions-product.csv.gz  the same persons with the product kernel
//	audits.csv                the false-allow estimate of each person under
//	                          Balanced, at four audit rates, several draws each
//	calibration.csv           the calibration report of each person of
//	                          decisions.csv.gz, by bin
//	thresholds.csv.gz         limen.Costs.Thresholds over a grid of costs
//	profiles.csv              the named profiles and their thresholds
//	cover.csv.gz              Limen's engine with its cover rule against a
//	                          person whose actions differ in scope
//	meta.json                 the settings of the run
//
// Usage:
//
//	go run ./cmd/simulate -persons 20 -out data
package main

import (
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/docs/figures/consent"
	"latere.ai/x/limen/docs/figures/protocol"
	"latere.ai/x/limen/engine/gp"
	"latere.ai/x/limen/engine/kernel"
	"latere.ai/x/limen/estimate"
)

// AuditRates are the audit rates of audits.csv, and AuditProfile the
// profile its runs use: Balanced allows enough actions the person would
// deny for there to be a false-allow rate to estimate.
var (
	AuditRates   = []float64{0.02, 0.05, 0.10, 0.20}
	AuditProfile = "Balanced"
)

// run is one person's history under one setting.
type run struct {
	set  string // "main", "product" or "audits"
	s    int    // the person
	draw int    // the draw of reviews and answers, for "audits"
	rate float64
	o    protocol.Options
	recs []protocol.Record
	err  error
}

func main() {
	persons := flag.Int("persons", 20, "number of simulated persons, seeds 0 to persons-1")
	decisions := flag.Int("decisions", 1500, "decisions per person")
	draws := flag.Int("draws", 5, "draws of reviews and answers per person and audit rate")
	out := flag.String("out", "data", "directory of the data files")
	workers := flag.Int("workers", runtime.NumCPU(), "histories simulated at once")
	flag.Parse()
	log.SetFlags(0)
	start := time.Now()

	var runs []*run
	for s := range *persons {
		o := protocol.Paper(s)
		o.Decisions = *decisions
		runs = append(runs, &run{set: "main", s: s, o: o})
		p := o
		p.Kernel = kernel.DefaultProduct()
		runs = append(runs, &run{set: "product", s: s, o: p})
		for _, rate := range AuditRates {
			for d := range *draws {
				a := o
				a.Thresholds = protocol.Named(AuditProfile).Thresholds()
				a.AuditRate = rate
				// Draw 0 shares the main run's seed; draw d the seed
				// s + 1000 d. The stream, the person's actions, is the
				// same for every draw.
				a.Seed = uint64(s + 1000*d)
				runs = append(runs, &run{set: "audits", s: s, draw: d, rate: rate, o: a})
			}
		}
	}
	// The slow histories first, so the workers finish together: the
	// product kernel asks about nearly every action and refits the most.
	order := make([]*run, 0, len(runs))
	for _, set := range []string{"product", "main", "audits"} {
		for _, r := range runs {
			if r.set == set {
				order = append(order, r)
			}
		}
	}
	jobs := make(chan *run)
	var wg sync.WaitGroup
	for range max(*workers, 1) {
		wg.Go(func() {
			for r := range jobs {
				r.recs, r.err = protocol.Run(r.o)
			}
		})
	}
	for _, r := range order {
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	for _, r := range runs {
		if r.err != nil {
			log.Fatalf("simulate: %s person %d: %v", r.set, r.s, r.err)
		}
	}
	log.Printf("simulate: %d histories of %d decisions in %s", len(runs), *decisions, time.Since(start).Round(time.Second))

	// The engine with its cover rule, one person per seed.
	covers := make([][]consent.Record, *persons)
	cerrs := make([]error, *persons)
	sem := make(chan struct{}, max(*workers, 1))
	for s := range *persons {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			covers[s], cerrs[s] = consent.Run(consent.Default(s))
		})
	}
	wg.Wait()
	for s, err := range cerrs {
		if err != nil {
			log.Fatalf("simulate: cover person %d: %v", s, err)
		}
	}
	log.Printf("simulate: %d cover histories in %s", *persons, time.Since(start).Round(time.Second))

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	pick := func(set string) []*run {
		var rs []*run
		for _, r := range runs {
			if r.set == set {
				rs = append(rs, r)
			}
		}
		return rs
	}
	for _, w := range []struct {
		name string
		fn   func(io.Writer) error
	}{
		{"decisions.csv.gz", func(w io.Writer) error { return writeDecisions(w, pick("main")) }},
		{"decisions-product.csv.gz", func(w io.Writer) error { return writeDecisions(w, pick("product")) }},
		{"audits.csv", func(w io.Writer) error { return writeAudits(w, pick("audits")) }},
		{"calibration.csv", func(w io.Writer) error { return writeCalibration(w, pick("main")) }},
		{"thresholds.csv.gz", writeThresholds},
		{"profiles.csv", writeProfiles},
		{"cover.csv.gz", func(w io.Writer) error { return writeCover(w, covers) }},
		{"meta.json", func(w io.Writer) error { return writeMeta(w, *persons, *decisions, *draws) }},
	} {
		if err := create(filepath.Join(*out, w.name), w.fn); err != nil {
			log.Fatalf("simulate: %s: %v", w.name, err)
		}
	}
}

// create writes a file, gzipped when its name ends in .gz. The gzip
// header carries no time, so the same data give the same bytes.
func create(path string, fn func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	var w io.Writer = f
	var zw *gzip.Writer
	if filepath.Ext(path) == ".gz" {
		zw, _ = gzip.NewWriterLevel(f, gzip.BestCompression)
		w = zw
	}
	if err := fn(w); err != nil {
		f.Close()
		return err
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}

func num(x float64) string { return strconv.FormatFloat(x, 'f', 6, 64) }

func flag01(x bool) string {
	if x {
		return "1"
	}
	return "0"
}

// table writes a header and rows as CSV.
func table(w io.Writer, header []string, rows func(add func(...string))) error {
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	rows(func(cells ...string) { _ = cw.Write(cells) })
	cw.Flush()
	return cw.Error()
}

// writeDecisions writes one row per decision, ordered by person and time.
func writeDecisions(w io.Writer, runs []*run) error {
	header := []string{"seed", "t", "p_hat", "p_star", "verdict", "applied", "review", "shown", "answer",
		"action", "task", "arg_risk", "vetoed", "answers"}
	return table(w, header, func(add func(...string)) {
		for _, r := range runs {
			for _, d := range r.recs {
				answer := ""
				if d.Shown {
					answer = flag01(d.Approve)
				}
				add(strconv.Itoa(r.s), strconv.Itoa(d.Decision.T), num(d.PHat), num(d.PStar), string(d.Verdict),
					string(d.Applied), num(d.Review), flag01(d.Shown), answer, d.Decision.Name(), d.Decision.Task,
					flag01(d.Decision.ArgRisk), flag01(d.Vetoed), strconv.Itoa(d.Answers))
			}
		}
	})
}

// cases are a history as the ledger keeps it: the prediction of Limen's
// model, the decision with its review probability, and the review.
func cases(recs []protocol.Record) []limen.Case {
	out := make([]limen.Case, len(recs))
	for i, d := range recs {
		c := limen.Case{
			Prediction: &limen.Prediction{Verdict: d.Verdict, Approve: d.PHat},
			Decision:   &limen.Decision{Verdict: d.Applied, ReviewProbability: d.Review},
		}
		if d.Shown {
			c.Answers = []limen.RecordedAnswer{{Approve: d.Approve, Review: true}}
		}
		out[i] = c
	}
	return out
}

// rows are the cases' rows for the learned source, estimate.RowOf.
func rows(cs []limen.Case) []estimate.Row {
	var out []estimate.Row
	for _, c := range cs {
		if r, ok := estimate.RowOf(c, limen.OfLearned); ok {
			out = append(out, r)
		}
	}
	return out
}

// writeAudits writes, for each history, the true false-allow rate of
// Limen's allows, the mean of 1 - p* over them, beside the estimate the
// ledger gives from the audited allows, estimate.Rate.
func writeAudits(w io.Writer, runs []*run) error {
	header := []string{"profile", "rate", "seed", "draw", "allows", "true_rate", "estimate", "lo", "hi", "reviews", "audits", "covered"}
	return table(w, header, func(add func(...string)) {
		for _, r := range runs {
			allows, deny := 0, 0.0
			for _, d := range r.recs {
				if d.Verdict == limen.Allow {
					allows++
					deny += 1 - d.PStar
				}
			}
			e := estimate.Rate(rows(cases(r.recs)), estimate.FalseAllow)
			value, lo, hi := "", "", ""
			if e.Value != nil {
				value, lo, hi = num(*e.Value), num(e.Interval[0]), num(e.Interval[1])
			}
			truth := ""
			if allows > 0 {
				truth = num(deny / float64(allows))
			}
			add(AuditProfile, num(r.rate), strconv.Itoa(r.s), strconv.Itoa(r.draw), strconv.Itoa(allows), truth,
				value, lo, hi, strconv.Itoa(e.Reviews), strconv.Itoa(e.Audits), num(e.Covered))
		}
	})
}

// writeCalibration writes each person's calibration report over the whole
// history, estimate.Calibrate, one row per bin.
func writeCalibration(w io.Writer, runs []*run) error {
	header := []string{"seed", "bin", "from", "to", "cases", "reviews", "covered", "stated", "observed", "lo", "hi", "state", "error"}
	return table(w, header, func(add func(...string)) {
		for _, r := range runs {
			q := limen.CalibrationQuery{Org: "simulation", Subject: strconv.Itoa(r.s)}
			c := estimate.Calibrate(q, rows(cases(r.recs)))
			opt := func(x *float64) string {
				if x == nil {
					return ""
				}
				return num(*x)
			}
			for i, b := range c.Bins {
				lo, hi := "", ""
				if b.Interval != nil {
					lo, hi = num(b.Interval[0]), num(b.Interval[1])
				}
				add(strconv.Itoa(r.s), strconv.Itoa(i), num(b.From), num(b.To), strconv.Itoa(b.Cases), strconv.Itoa(b.Reviews),
					num(b.Covered), opt(b.Stated), opt(b.Observed), lo, hi, b.State, opt(c.Error))
			}
		}
	})
}

// Grid is the plane of thresholds.csv.gz: the cost of a false allow and
// of a false block, each over the cost of an ask, log-spaced.
const (
	gridMin, gridMax = 1.25, 100.0
	gridN            = 161
)

// writeThresholds evaluates limen.Costs.Thresholds over the grid. A cost
// pair the function refuses has ok 0 and no thresholds.
func writeThresholds(w io.Writer) error {
	header := []string{"false_allow", "false_block", "ok", "allow_above", "block_below"}
	at := func(i int) float64 {
		return gridMin * math.Pow(gridMax/gridMin, float64(i)/float64(gridN-1))
	}
	return table(w, header, func(add func(...string)) {
		for i := range gridN {
			for j := range gridN {
				x, y := at(i), at(j)
				th, err := limen.Costs{FalseAllow: x, FalseBlock: y, Ask: 1}.Thresholds()
				if err != nil {
					add(num(x), num(y), "0", "", "")
					continue
				}
				add(num(x), num(y), "1", num(th.AllowAbove), num(th.BlockBelow))
			}
		}
	})
}

func writeProfiles(w io.Writer) error {
	header := []string{"name", "false_allow", "false_block", "ask", "allow_above", "block_below"}
	return table(w, header, func(add func(...string)) {
		for _, p := range protocol.Profiles {
			th := p.Thresholds()
			add(p.Name, num(p.Costs.FalseAllow), num(p.Costs.FalseBlock), num(p.Costs.Ask), num(th.AllowAbove), num(th.BlockBelow))
		}
	})
}

// writeCover writes one row per decision of the engine runs.
func writeCover(w io.Writer, runs [][]consent.Record) error {
	header := []string{"seed", "t", "kind", "session", "approve", "truth", "verdict", "ask_kind", "weight", "sessions",
		"covered", "applied", "shown", "answer", "opened", "answer_ms"}
	return table(w, header, func(add func(...string)) {
		for s, run := range runs {
			for _, r := range run {
				answer, opened, ms := "", "", ""
				if r.Shown {
					answer, opened, ms = flag01(r.Approved), flag01(r.Opened), strconv.FormatInt(r.AnswerMS, 10)
				}
				add(strconv.Itoa(s), strconv.Itoa(r.T), r.Kind, strconv.Itoa(r.Session), num(r.Approve), num(r.Truth),
					string(r.Suggested), r.AskKind, num(r.Weight), strconv.Itoa(r.Sessions), flag01(r.Covered),
					string(r.Applied), flag01(r.Shown), answer, opened, ms)
			}
		}
	})
}

// writeMeta records the settings the renderer states in a caption and
// draws as thresholds, so no number in a figure is typed by hand.
func writeMeta(w io.Writer, persons, decisions, draws int) error {
	o := protocol.Paper(0)
	k, ok := o.Kernel.(kernel.Additive)
	if !ok {
		return fmt.Errorf("kernel %T is not the additive kernel", o.Kernel)
	}
	meta := map[string]any{
		"persons":      persons,
		"decisions":    decisions,
		"seeds":        fmt.Sprintf("0-%d", persons-1),
		"stream_seeds": fmt.Sprintf("%d-%d", o.StreamSeed, o.StreamSeed+uint64(persons)-1),
		"model":        "engine/gp",
		"kernel":       k,
		"product":      kernel.DefaultProduct(),
		"max_labels":   gp.DefaultMaxLabels,
		"refit":        "after every answer",
		"allow_above":  o.Thresholds.AllowAbove,
		"block_below":  o.Thresholds.BlockBelow,
		"audit_rate":   o.AuditRate,
		"changepoint":  o.Oracle.Changepoint,
		"oracle":       o.Oracle,
		"audits": map[string]any{
			"profile": AuditProfile, "rates": AuditRates, "draws": draws,
			"allow_above": protocol.Named(AuditProfile).Thresholds().AllowAbove,
			"block_below": protocol.Named(AuditProfile).Thresholds().BlockBelow,
		},
		"calibration": map[string]any{
			"bins": limen.CalibrationBins, "min_effective": limen.CalibrationMinEffective,
		},
		"cover": coverMeta(),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func coverMeta() map[string]any {
	o := consent.Default(0)
	th, _ := o.Costs.Thresholds()
	var kinds []map[string]any
	for _, k := range consent.Kinds {
		kinds = append(kinds, map[string]any{"name": k.Name, "weight": k.Weight, "approve": k.Approve})
	}
	return map[string]any{
		"decisions": o.Decisions, "session": o.Session, "opened": o.Opened, "audit_rate": o.AuditRate,
		"allow_above": th.AllowAbove, "block_below": th.BlockBelow,
		"min_weight": limen.MinWeight, "min_sessions": limen.MinSessions,
		"answer_floor_ms": limen.AnswerFloor.Milliseconds(), "kinds": kinds,
	}
}
