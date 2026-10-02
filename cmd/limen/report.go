// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"slices"
	"text/tabwriter"

	"latere.ai/x/pkg/errwriter"

	"latere.ai/x/limen"
)

// minAnswers is the number of answers below which the report warns that
// Limen has too little to learn from.
const minAnswers = 30

// writeReport prints a replay's report for the person who ran it.
func writeReport(w *errwriter.Writer, rep limen.ReplayReport, skipped map[string]int) {
	w.Printf("Replayed %d %s; %d of them have your answer.\n", rep.Records, plural(rep.Records, "action", "actions"), rep.Answered)
	if n := total(skipped); n > 0 {
		w.Printf("Skipped %d %s:\n", n, plural(n, "action", "actions"))
		reasons := make([]string, 0, len(skipped))
		for r := range skipped {
			reasons = append(reasons, r)
		}
		slices.Sort(reasons)
		for _, r := range reasons {
			w.Printf("  %d: %s\n", skipped[r], r)
		}
	}
	if rep.Records == 0 {
		w.Println("\nThere is nothing to report yet.")
		return
	}

	w.Println("\nWhat Limen would have suggested:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, v := range []limen.Verdict{limen.Allow, limen.Ask, limen.Block} {
		twPrintf(tw, "  %s\t%d\t%s\n", v, rep.Would[v], share(rep.Would[v], rep.Records))
	}
	_ = tw.Flush() // tw writes through w, which latches a failed write

	w.Println("\nWhere Limen and your answers disagree:")
	w.Printf("  would have allowed, and you denied:   %d\n", rep.AllowedDenied)
	w.Printf("  would have blocked, and you approved: %d\n", rep.BlockedApproved)

	w.Println("\nHow often Limen's allows would be wrong:")
	fa := rep.FalseAllow
	if fa.Value == nil {
		w.Println("  Not measurable from this history: none of the actions Limen would have allowed was shown to you.")
	} else {
		lo, hi := 0.0, 1.0
		if fa.Interval != nil {
			lo, hi = fa.Interval[0], fa.Interval[1]
		}
		w.Printf("  %s of its allows (95%% interval %s to %s), from %d of your answers.\n",
			pct(*fa.Value), pct(lo), pct(hi), fa.Reviews)
		w.Printf("  This speaks for the %s of its allows that you could have seen.\n", pct(fa.Covered))
	}

	asks := float64(rep.Would[limen.Ask]) / float64(rep.Records)
	auto := float64(rep.Would[limen.Allow]+rep.Would[limen.Block]) / float64(rep.Records)
	w.Println("\nHow often you would be asked:")
	w.Printf("  under Limen: %s of actions (%s asks, and a %s review sample of the rest)\n",
		pct(asks+limen.DefaultAuditRate*auto), pct(asks), pct(limen.DefaultAuditRate))
	w.Printf("  in this history: %s of actions had your answer\n", share(rep.Answered, rep.Records))

	if rep.Answered < minAnswers {
		w.Printf("\nOnly %d %s: Limen needs a few dozen before it suggests anything but ask. "+
			"Use Topos in manual mode for a while, then replay again.\n", rep.Answered, plural(rep.Answered, "answer", "answers"))
	}
	if rep.Kept {
		w.Println("\nThe history is now part of Limen's ledger, and its models have learned from it.")
	}
}

// twPrintf writes a table row; the table's error surfaces at Flush, which
// writes through the report's writer.
func twPrintf(tw *tabwriter.Writer, format string, a ...any) { _, _ = fmt.Fprintf(tw, format, a...) }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func share(n, of int) string {
	if of == 0 {
		return pct(0)
	}
	return pct(float64(n) / float64(of))
}

func pct(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }
