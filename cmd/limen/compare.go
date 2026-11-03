// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"text/tabwriter"

	"latere.ai/x/pkg/errwriter"

	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/engine/store/file"
)

// compareOptions are the flags every comparison takes.
type compareOptions struct {
	store string
	json  bool
}

func (o *compareOptions) register(fs *flag.FlagSet) {
	fs.StringVar(&o.store, "store", "", "the directory where Limen keeps its ledger, its cost profiles and its models")
	fs.BoolVar(&o.json, "json", false, "print the comparison as JSON")
}

// compare replays a history through learned/1 and learned/2 and reports
// where their suggestions differ (spec 011): the check before an operator
// sets LIMEN_MODEL to learned/2.
func compare(args []string, stdin io.Reader, out, errOut *errwriter.Writer, getenv func(string) string) int {
	const cmd = "limen compare"
	if len(args) == 0 {
		errOut.Print(cmd + ": give the source to compare: topos, file or ledger.\n\n" + usage)
		return exitUsage
	}
	var (
		o       compareOptions
		src     *source
		orgs    []string
		subject string
	)
	switch args[0] {
	case "topos", "file":
		s, closeSrc, code, done := readSource(cmd, args, stdin, errOut, getenv, o.register)
		defer closeSrc()
		if done {
			return code
		}
		src = s
	case "ledger":
		fs := flag.NewFlagSet(cmd+" ledger", flag.ContinueOnError)
		fs.SetOutput(errOut)
		org := fs.String("org", "", "the organization to compare; default each organization of the ledger")
		fs.StringVar(&subject, "subject", "", "the person to compare; default each person")
		o.register(fs)
		if code, done := parse(fs, args[1:]); done {
			return code
		}
		if fs.NArg() > 0 {
			errOut.Printf("%s ledger: unexpected argument %q.\n", cmd, fs.Arg(0))
			return exitUsage
		}
		if o.store == "" {
			errOut.Printf("%s ledger: give the ledger directory with --store. Use a copy of the directory, or stop limend first.\n", cmd)
			return exitUsage
		}
		if *org != "" {
			orgs = []string{*org}
		}
	default:
		errOut.Printf("%s: %q is not a known source. Use topos, file or ledger.\n", cmd, args[0])
		return exitUsage
	}

	st := store.Store(store.NewMemory())
	if o.store != "" {
		f, err := file.Open(o.store)
		if err != nil {
			errOut.Printf("%s: %v\n", cmd, err)
			return exitFail
		}
		st = f
		if src == nil && orgs == nil {
			if orgs, err = f.Orgs(context.Background()); err != nil {
				_ = f.Close()
				errOut.Printf("%s: %v\n", cmd, err)
				return exitFail
			}
		}
	}
	// The comparison reads the ledger and keeps nothing, so a failed close
	// loses nothing.
	defer func() { _ = st.Close() }()
	e := engine.New(engine.Options{Store: st, Inline: 1})
	defer func() { _ = e.Close() }()

	var (
		c   engine.Comparison
		err error
	)
	if src != nil {
		c, err = e.Compare(context.Background(), src.records, engine.CompareOptions{})
	} else {
		c, err = e.CompareLedger(context.Background(), orgs, engine.CompareOptions{Subject: subject})
	}
	if err != nil {
		printError(errOut, cmd, src, err)
		return exitFail
	}
	skipped := map[string]int{}
	if src != nil {
		skipped = src.skipped
	}
	if o.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Comparison engine.Comparison `json:"comparison"`
			Skipped    map[string]int    `json:"skipped"`
		}{c, skipped}); err != nil {
			errOut.Printf("%s: %v\n", cmd, err)
			return exitFail
		}
		return exitOK
	}
	writeComparison(out, c, skipped)
	return exitOK
}

// writeComparison prints a comparison for the operator who runs it.
func writeComparison(w *errwriter.Writer, c engine.Comparison, skipped map[string]int) {
	o := c.Overall
	w.Printf("Limen compared %s and %s on %d %s of %d %s. %d of the actions %s an answer.\n",
		c.Base, c.Next, o.Records, plural(o.Records, "action", "actions"), len(c.Persons), plural(len(c.Persons), "person", "persons"),
		o.Answered, plural(o.Answered, "has", "have"))
	if n := total(skipped); n > 0 {
		w.Printf("Limen skipped %d %s. Use limen replay to see the reasons.\n", n, plural(n, "action", "actions"))
	}
	if o.Records == 0 {
		w.Println("\nThere is nothing to compare yet.")
		return
	}

	w.Println("\nThe verdicts that each model would suggest:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	twPrintf(tw, "  \t%s\t%s\n", c.Base, c.Next)
	for _, k := range kinds(o.Base, o.Next) {
		twPrintf(tw, "  %s\t%d\t%d\n", k, o.Base[k], o.Next[k])
	}
	_ = tw.Flush() // tw writes through w, which latches a failed write

	if len(o.Changes) == 0 {
		w.Printf("\nThe two models suggest the same verdict for each action.\n")
	} else {
		w.Printf("\nThe actions where the verdicts are different, from %s to %s:\n", c.Base, c.Next)
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, k := range slices.Sorted(maps.Keys(o.Changes)) {
			twPrintf(tw, "  %s\t%d\n", k, o.Changes[k])
		}
		_ = tw.Flush()
	}

	w.Println()
	w.Printf("Allows of %s that %s does not give: %d\n", c.Base, c.Next, o.Withheld)
	w.Printf("Allows of %s that %s does not give: %d\n", c.Next, c.Base, o.Granted)
	w.Printf("Verdicts of %s that permit more than the verdicts of %s: %d\n", c.Next, c.Base, o.Looser)
	w.Printf("  Of these, verdicts that the cover rule does not hold: %d\n", o.Outside)
	if o.CoverDiffers > 0 {
		w.Printf("Actions whose cover is different between the two models: %d. A client showed the suggestion before the answer.\n", o.CoverDiffers)
	}
	if o.BrierBase != nil {
		w.Printf("\nThe Brier score of each model, from %d %s (lower is better):\n", o.Answered, plural(o.Answered, "answer", "answers"))
		w.Printf("  %s: %.4f\n  %s: %.4f\n", c.Base, *o.BrierBase, c.Next, *o.BrierNext)
		w.Println("  The answers are only those of the actions that a person saw.")
	}
	if o.Recorded != nil {
		w.Printf("\n%s suggests the verdict that the ledger recorded for %d of %d %s.\n", c.Base, o.Agree, total(o.Recorded),
			plural(total(o.Recorded), "prediction", "predictions"))
	}

	var loose []engine.Change
	for _, x := range c.Changed {
		if x.Looser {
			loose = append(loose, x)
		}
	}
	if len(loose) > 0 {
		w.Printf("\nThe verdicts of %s that permit more:\n", c.Next)
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, x := range loose {
			note := "covered"
			if x.Outside {
				note = "not held by the cover rule"
			}
			twPrintf(tw, "  %s/%s\t%s\t%s\t%s %.3f -> %s %.3f\t%s\n", x.Org, x.Subject, x.ActionID, x.Label,
				x.Base.Kind, x.Base.Approve, x.Next.Kind, x.Next.Approve, note)
		}
		_ = tw.Flush()
	}

	w.Println("\nFor each person:")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	twPrintf(tw, "  person\tactions\tanswers\tallows of %s\tallows of %s\tpermit more\tnot held\tBrier %s\tBrier %s\n",
		c.Base, c.Next, c.Base, c.Next)
	for _, p := range c.Persons {
		bb, bn := "-", "-"
		if p.BrierBase != nil {
			bb, bn = fmt.Sprintf("%.4f", *p.BrierBase), fmt.Sprintf("%.4f", *p.BrierNext)
		}
		twPrintf(tw, "  %s/%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n", p.Org, p.Subject, p.Records, p.Answered,
			p.Base["allow"], p.Next["allow"], p.Looser, p.Outside, bb, bn)
	}
	_ = tw.Flush()
}

// kinds are the kinds of suggestion in either count: allow, the kinds of
// ask in order, then block.
func kinds(a, b map[string]int) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	order := func(k string) int {
		switch k {
		case "allow":
			return 0
		case "block":
			return 2
		}
		return 1
	}
	out := slices.Collect(maps.Keys(seen))
	slices.SortFunc(out, func(x, y string) int {
		if d := order(x) - order(y); d != 0 {
			return d
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return 0
	})
	return out
}
