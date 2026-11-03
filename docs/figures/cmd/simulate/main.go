// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command simulate runs Limen's model against the paper's simulated
// person, one history per seed, and writes every decision as gzipped CSV
// with the settings beside it as JSON, the data render.py draws the
// figures from.
//
//	go run ./cmd/simulate -persons 20 -out data
package main

import (
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"latere.ai/x/limen/docs/figures/protocol"
	"latere.ai/x/limen/engine/gp"
	"latere.ai/x/limen/engine/kernel"
)

func main() {
	persons := flag.Int("persons", 20, "number of simulated persons, seeds 0 to persons-1")
	decisions := flag.Int("decisions", 1500, "decisions per person")
	out := flag.String("out", "data", "directory of decisions.csv.gz and meta.json")
	workers := flag.Int("workers", runtime.NumCPU(), "persons simulated at once")
	flag.Parse()
	log.SetFlags(0)

	start := time.Now()
	runs := make([][]protocol.Record, *persons)
	errs := make([]error, *persons)
	sem := make(chan struct{}, max(*workers, 1))
	var wg sync.WaitGroup
	for s := range *persons {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			o := protocol.Paper(s)
			o.Decisions = *decisions
			runs[s], errs[s] = protocol.Run(o)
		})
	}
	wg.Wait()
	for s, err := range errs {
		if err != nil {
			log.Fatalf("simulate: person %d: %v", s, err)
		}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := writeCSV(filepath.Join(*out, "decisions.csv.gz"), runs); err != nil {
		log.Fatal(err)
	}
	if err := writeMeta(filepath.Join(*out, "meta.json"), *persons, *decisions); err != nil {
		log.Fatal(err)
	}
	log.Printf("simulate: %d persons x %d decisions in %s", *persons, *decisions, time.Since(start).Round(time.Second))
}

var header = []string{"seed", "t", "p_hat", "p_star", "verdict", "applied", "shown", "answer", "action", "task", "arg_risk", "vetoed", "answers"}

// writeCSV writes one row per decision, ordered by seed and time. The
// gzip header carries no time, so the same records give the same bytes.
func writeCSV(path string, runs [][]protocol.Record) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	w := csv.NewWriter(zw)
	_ = w.Write(header)
	p := func(x float64) string { return strconv.FormatFloat(x, 'f', 6, 64) }
	b := func(x bool) string {
		if x {
			return "1"
		}
		return "0"
	}
	for s, run := range runs {
		for _, r := range run {
			answer := ""
			if r.Shown {
				answer = b(r.Approve)
			}
			_ = w.Write([]string{strconv.Itoa(s), strconv.Itoa(r.Decision.T), p(r.PHat), p(r.PStar),
				string(r.Verdict), string(r.Applied), b(r.Shown), answer, r.Decision.Name(), r.Decision.Task,
				b(r.Decision.ArgRisk), b(r.Vetoed), strconv.Itoa(r.Answers)})
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

// writeMeta records the settings the renderer states in a caption and
// draws as thresholds, so no number in a figure is typed by hand.
func writeMeta(path string, persons, decisions int) error {
	o := protocol.Paper(0)
	k, ok := o.Kernel.(kernel.Additive)
	if !ok {
		return fmt.Errorf("simulate: kernel %T is not the additive kernel", o.Kernel)
	}
	meta := map[string]any{
		"persons":      persons,
		"decisions":    decisions,
		"seeds":        fmt.Sprintf("0-%d", persons-1),
		"stream_seeds": fmt.Sprintf("%d-%d", o.StreamSeed, o.StreamSeed+uint64(persons)-1),
		"model":        "engine/gp, kernel.DefaultAdditive",
		"kernel":       k,
		"max_labels":   gp.DefaultMaxLabels,
		"refit":        "after every answer",
		"allow_above":  o.Thresholds.AllowAbove,
		"block_below":  o.Thresholds.BlockBelow,
		"audit_rate":   o.AuditRate,
		"changepoint":  o.Oracle.Changepoint,
		"oracle":       o.Oracle,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
