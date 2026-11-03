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
	model string
}

func (o *options) register(fs *flag.FlagSet) {
	fs.BoolVar(&o.keep, "keep", false, "learn the history into the models kept in --store")
	fs.StringVar(&o.store, "store", "", "the directory where Limen keeps its ledger and models")
	fs.BoolVar(&o.json, "json", false, "print the report as JSON")
	fs.StringVar(&o.model, "model", "", "the model that replays the history: learned/1 or learned/2; default learned/1")
}

// source is where a replay reads its records, and what it found on the way.
type source struct {
	records  iter.Seq2[limen.ReplayRecord, error]
	skipped  map[string]int
	describe string
}

// readSource parses the flags of a replay or a comparison from a source,
// topos or file, with the flags that register adds, and opens the source.
// It reports done with the exit code when the command should stop. close
// releases the file it opened.
func readSource(cmd string, args []string, stdin io.Reader, errOut *errwriter.Writer, getenv func(string) string,
	register func(*flag.FlagSet)) (src *source, close func(), code int, done bool) {
	close = func() {}
	switch args[0] {
	case "topos":
		fs := flag.NewFlagSet(cmd+" topos", flag.ContinueOnError)
		fs.SetOutput(errOut)
		data := fs.String("data", "", "the Topos data directory; default TOPOS_DATA_DIR, then $XDG_STATE_HOME/topos, then ~/.local/state/topos")
		org := fs.String("org", "", "the organization of the replayed actions; default the session's, then local")
		subject := fs.String("subject", "", "the person whose decisions these are; default each session's initiator")
		register(fs)
		if code, done := parse(fs, args[1:]); done {
			return nil, close, code, true
		}
		if fs.NArg() > 0 {
			errOut.Printf("%s topos: unexpected argument %q.\n", cmd, fs.Arg(0))
			return nil, close, exitUsage, true
		}
		dir := *data
		if dir == "" {
			dir = toposDataDir(getenv)
		}
		if dir == "" {
			errOut.Printf("%s topos: there is no Topos data directory. Use --data, or set TOPOS_DATA_DIR or HOME.\n", cmd)
			return nil, close, exitUsage, true
		}
		skipped := map[string]int{}
		return &source{
			records: topos.Records(dir, topos.Options{Org: *org, Subject: *subject, Skipped: func(s topos.Skip) { skipped[s.Reason]++ }}),
			skipped: skipped, describe: dir,
		}, close, 0, false
	case "file":
		fs := flag.NewFlagSet(cmd+" file", flag.ContinueOnError)
		fs.SetOutput(errOut)
		register(fs)
		if code, done := parse(fs, args[1:]); done {
			return nil, close, code, true
		}
		if fs.NArg() != 1 {
			errOut.Printf("%s file: give one file of records, or - for standard input.\n", cmd)
			return nil, close, exitUsage, true
		}
		path := fs.Arg(0)
		r := stdin
		if path != "-" {
			f, err := os.Open(path)
			if err != nil {
				errOut.Printf("%s file: cannot open %s: %v\n", cmd, path, err)
				return nil, close, exitFail, true
			}
			// A read-only file: closing it loses nothing.
			close = func() { _ = f.Close() }
			r = f
		}
		return &source{records: ndjson(r), skipped: map[string]int{}, describe: path}, close, 0, false
	}
	return nil, close, exitUsage, true
}

func replay(args []string, stdin io.Reader, out, errOut *errwriter.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		errOut.Print("limen replay: give the source to replay: topos or file.\n\n" + usage)
		return exitUsage
	}
	if args[0] != "topos" && args[0] != "file" {
		errOut.Printf("limen replay: %q is not a known source. Use topos or file.\n", args[0])
		return exitUsage
	}
	var o options
	src, closeSrc, code, done := readSource("limen replay", args, stdin, errOut, getenv, o.register)
	defer closeSrc()
	if done {
		return code
	}
	if o.keep && o.store == "" {
		errOut.Println("limen replay: --keep needs --store, the directory that keeps the ledger.")
		return exitUsage
	}
	model, err := engine.ParseModel(o.model)
	if err != nil {
		errOut.Printf("limen replay: --model is not correct: %v.\n", err)
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
	e := engine.New(engine.Options{Store: st, Model: model})
	defer func() {
		// The engine stops its fitters; the store flushes what a kept replay
		// wrote, which the person must hear about if it fails.
		_ = e.Close()
		if err := st.Close(); err != nil {
			errOut.Printf("limen replay: cannot close the ledger: %v\n", err)
		}
	}()

	rep, err := e.Replay(context.Background(), src.records, limen.ReplayOptions{Keep: o.keep})
	if err != nil {
		printError(errOut, "limen replay", src, err)
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

// printError explains a failed replay or comparison to the person who ran
// it.
func printError(w *errwriter.Writer, cmd string, src *source, err error) {
	if errors.Is(err, topos.ErrNoSessions) && src != nil {
		w.Printf("%s: there are no Topos sessions in %s. Use Topos for some time, or use --data to give a different directory.\n", cmd, src.describe)
		return
	}
	var le *limen.Error
	if errors.As(err, &le) {
		w.Printf("%s: %s\n  %s\n", cmd, le.Code.Message(), le.Error())
		return
	}
	w.Printf("%s: %v\n", cmd, err)
}
