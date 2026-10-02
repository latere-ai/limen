// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command limen is Limen's command line. It replays recorded decisions
// through Limen, from a Topos data directory or from a file of records,
// and reports what Limen would have suggested and how often it would have
// been wrong. The server is limend:
//
//	limen replay topos [--data DIR] [--org ORG] [--subject SUBJECT] [--keep --store DIR] [--json]
//	limen replay file  [--keep --store DIR] [--json] RECORDS.ndjson
//	limen version
//
// It exits 0 on success, 1 when the replay fails, and 2 on a usage error.
package main

import (
	"io"
	"os"

	"latere.ai/x/pkg/errwriter"

	"latere.ai/x/limen/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// The exit codes.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

const usage = `Limen learns when an automated action needs a person.

Usage:
  limen replay topos [--data DIR] [--org ORG] [--subject SUBJECT] [--keep --store DIR] [--json]
  limen replay file  [--keep --store DIR] [--json] RECORDS.ndjson
  limen version

Run "limen replay topos -h" for the options of a command.
`

// run is the command line, with its environment passed in so a test can
// run it without touching the process's. Output that could not be written
// fails the command, so a closed pipe never reports success.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	out, errOut := errwriter.New(stdout), errwriter.New(stderr)
	code := dispatch(args, stdin, out, errOut, getenv)
	if out.Err() != nil && code == exitOK {
		return exitFail
	}
	return code
}

func dispatch(args []string, stdin io.Reader, out, errOut *errwriter.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		errOut.Print(usage)
		return exitUsage
	}
	switch args[0] {
	case "replay":
		return replay(args[1:], stdin, out, errOut, getenv)
	case "version":
		out.Println(version.String("limen"))
		return exitOK
	case "help", "-h", "--help":
		out.Print(usage)
		return exitOK
	}
	errOut.Printf("limen: unknown command %q.\n\n%s", args[0], usage)
	return exitUsage
}
