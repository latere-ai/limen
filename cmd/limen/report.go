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
	w.Printf("Limen replayed %d %s. You answered %d of them.\n", rep.Records, plural(rep.Records, "action", "actions"), rep.Answered)
	if n := total(skipped); n > 0 {
		w.Printf("Limen skipped %d %s for these reasons:\n", n, plural(n, "action", "actions"))
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

	w.Println("\nThe verdicts that Limen would suggest:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, v := range []limen.Verdict{limen.Allow, limen.Ask, limen.Block} {
		twPrintf(tw, "  %s\t%d\t%s\n", v, rep.Would[v], share(rep.Would[v], rep.Records))
	}
	_ = tw.Flush() // tw writes through w, which latches a failed write

	w.Println("\nWhere Limen and your answers do not agree:")
	w.Printf("  Limen would allow, and you denied:   %d\n", rep.AllowedDenied)
	w.Printf("  Limen would block, and you approved: %d\n", rep.BlockedApproved)

	w.Println("\nThe false allow rate of Limen:")
	fa := rep.FalseAllow
	if fa.Value == nil {
		w.Println("  Limen cannot measure it from this history. You did not see any of the actions that Limen would allow.")
	} else {
		lo, hi := 0.0, 1.0
		if fa.Interval != nil {
			lo, hi = fa.Interval[0], fa.Interval[1]
		}
		w.Printf("  %s of its allows (95%% interval: %s to %s), from %d of your answers.\n",
			pct(*fa.Value), pct(lo), pct(hi), fa.Reviews)
		w.Printf("  This rate applies only to the %s of its allows that you could see.\n", pct(fa.Covered))
	}

	asks := float64(rep.Would[limen.Ask]) / float64(rep.Records)
	auto := float64(rep.Would[limen.Allow]+rep.Would[limen.Block]) / float64(rep.Records)
	w.Println("\nHow often Limen would ask you:")
	w.Printf("  With Limen: %s of the actions (%s asks, and audits of %s of the other actions)\n",
		pct(asks+limen.DefaultAuditRate*auto), pct(asks), pct(limen.DefaultAuditRate))
	w.Printf("  In this history: %s of the actions have your answer\n", share(rep.Answered, rep.Records))

	if rep.Answered < minAnswers {
		w.Printf("\nLimen has only %d %s from you. It usually needs %d or more answers before it suggests a verdict other than ask. "+
			"Use Topos in manual mode for some time, then replay again.\n", rep.Answered, plural(rep.Answered, "answer", "answers"), minAnswers)
	}
	if rep.Kept {
		w.Println("\nLimen added this history to its ledger. Its models learned from it.")
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
