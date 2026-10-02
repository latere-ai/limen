// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"latere.ai/x/pkg/errwriter"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store"
	"latere.ai/x/limen/importer/topos"
)

// options are the flags every replay takes.
type options struct {
	keep  bool
	store string
	json  bool
}

func (o *options) register(fs *flag.FlagSet) {
	fs.BoolVar(&o.keep, "keep", false, "learn the history into the models kept in --store")
	fs.StringVar(&o.store, "store", "", "the directory where Limen keeps its ledger and models")
	fs.BoolVar(&o.json, "json", false, "print the report as JSON")
}

// source is where a replay reads its records, and what it found on the way.
type source struct {
	records  iter.Seq2[limen.ReplayRecord, error]
	skipped  map[string]int
	describe string
}

func replay(args []string, stdin io.Reader, out, errOut *errwriter.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		errOut.Print("limen replay: name what to replay: topos or file.\n\n" + usage)
		return exitUsage
	}
	var (
		o   options
		src *source
	)
	switch args[0] {
	case "topos":
		fs := flag.NewFlagSet("limen replay topos", flag.ContinueOnError)
		fs.SetOutput(errOut)
		data := fs.String("data", "", "the Topos data directory; default TOPOS_DATA_DIR, then $XDG_STATE_HOME/topos, then ~/.local/state/topos")
		org := fs.String("org", "", "the organization of the replayed actions; default the session's, then local")
		subject := fs.String("subject", "", "the person whose decisions these are; default each session's initiator")
		o.register(fs)
		if code, done := parse(fs, args[1:]); done {
			return code
		}
		if fs.NArg() > 0 {
			errOut.Printf("limen replay topos: unexpected argument %q.\n", fs.Arg(0))
			return exitUsage
		}
		dir := *data
		if dir == "" {
			dir = toposDataDir(getenv)
		}
		if dir == "" {
			errOut.Println("limen replay topos: no Topos data directory. Pass --data, or set TOPOS_DATA_DIR or HOME.")
			return exitUsage
		}
		skipped := map[string]int{}
		src = &source{
			records: topos.Records(dir, topos.Options{Org: *org, Subject: *subject, Skipped: func(s topos.Skip) { skipped[s.Reason]++ }}),
			skipped: skipped, describe: dir,
		}
	case "file":
		fs := flag.NewFlagSet("limen replay file", flag.ContinueOnError)
		fs.SetOutput(errOut)
		o.register(fs)
		if code, done := parse(fs, args[1:]); done {
			return code
		}
		if fs.NArg() != 1 {
			errOut.Println("limen replay file: name one file of records, or - for standard input.")
			return exitUsage
		}
		path := fs.Arg(0)
		r := stdin
		if path != "-" {
			f, err := os.Open(path)
			if err != nil {
				errOut.Printf("limen replay file: cannot open %s: %v\n", path, err)
				return exitFail
			}
			defer func() { _ = f.Close() }() // a read-only file: closing it loses nothing
			r = f
		}
		src = &source{records: ndjson(r), skipped: map[string]int{}, describe: path}
	default:
		errOut.Printf("limen replay: unknown source %q; use topos or file.\n", args[0])
		return exitUsage
	}
	if o.keep && o.store == "" {
		errOut.Println("limen replay: --keep needs --store, the directory where the learned models are kept.")
		return exitUsage
	}

	st := store.Store(store.NewMemory())
	if o.store != "" {
		var err error
		if st, err = openStore(o.store); err != nil {
			errOut.Printf("limen replay: %v\n", err)
			return exitFail
		}
	}
	e := engine.New(engine.Options{Store: st})
	defer func() {
		// The engine stops its fitters; the store flushes what a kept replay
		// wrote, which the person must hear about if it fails.
		_ = e.Close()
		if err := st.Close(); err != nil {
			errOut.Printf("limen replay: closing the ledger: %v\n", err)
		}
	}()

	rep, err := e.Replay(context.Background(), src.records, limen.ReplayOptions{Keep: o.keep})
	if err != nil {
		printError(errOut, src, err)
		return exitFail
	}
	if o.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Report  limen.ReplayReport `json:"report"`
			Skipped map[string]int     `json:"skipped"`
		}{rep, src.skipped}); err != nil {
			errOut.Printf("limen replay: %v\n", err)
			return exitFail
		}
		return exitOK
	}
	writeReport(out, rep, src.skipped)
	return exitOK
}

// parse parses the flags, and reports done with the exit code when the
// command should stop: after help, or on a usage error.
func parse(fs *flag.FlagSet, args []string) (int, bool) {
	err := fs.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitOK, true
	case err != nil:
		return exitUsage, true
	}
	return 0, false
}

// toposDataDir resolves Topos's data directory the way Topos does:
// TOPOS_DATA_DIR, then $XDG_STATE_HOME/topos, then .local/state/topos under
// the home directory.
func toposDataDir(getenv func(string) string) string {
	if d := strings.TrimSpace(getenv("TOPOS_DATA_DIR")); d != "" {
		return d
	}
	if d := strings.TrimSpace(getenv("XDG_STATE_HOME")); d != "" {
		return filepath.Join(d, "topos")
	}
	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		home = strings.TrimSpace(getenv("USERPROFILE"))
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "topos")
}

// ndjson reads one record per line; blank lines are skipped.
func ndjson(r io.Reader) iter.Seq2[limen.ReplayRecord, error] {
	return func(yield func(limen.ReplayRecord, error) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		line := 0
		for sc.Scan() {
			line++
			text := strings.TrimSpace(sc.Text())
			if text == "" {
				continue
			}
			var rec limen.ReplayRecord
			if err := json.Unmarshal([]byte(text), &rec); err != nil {
				yield(limen.ReplayRecord{}, fmt.Errorf("line %d is not a record: %w", line, err))
				return
			}
			if !yield(rec, nil) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			yield(limen.ReplayRecord{}, err)
		}
	}
}

// printError explains a failed replay to the person who ran it.
func printError(w *errwriter.Writer, src *source, err error) {
	if errors.Is(err, topos.ErrNoSessions) {
		w.Printf("limen replay: there are no Topos sessions in %s. Use Topos for a while, or pass --data.\n", src.describe)
		return
	}
	var le *limen.Error
	if errors.As(err, &le) {
		w.Printf("limen replay: %s\n  %s\n", le.Code.Message(), le.Error())
		return
	}
	w.Printf("limen replay: %v\n", err)
}
