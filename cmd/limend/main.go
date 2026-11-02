// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command limend serves Limen's HTTP API over a ledger kept in a
// directory: decision points such as Topos send their actions, decisions
// and answers to it, and it learns from them. It serves one person, with
// one bearer token, or a hosted installation, with the tokens of the
// operator's issuers (LIMEN_OIDC_ISSUERS); the two modes exclude each
// other.
//
// It is configured by the environment:
//
//	LIMEN_TOKEN     the bearer every request carries, for one person
//	LIMEN_ADDR      the address to listen on; default 127.0.0.1:7720
//	LIMEN_DATA_DIR  the ledger's directory; default $XDG_STATE_HOME/limen,
//	                  or ~/.local/state/limen
//	LIMEN_SUBJECT   the person the server answers to, who may act for every
//	                  subject in it; default local
//	LIMEN_BASE_PATH a second prefix for the API in place of /v1, for an
//	                  ingress that forwards a public path unchanged, for
//	                  example /v1/oversight; default none
//	LIMEN_MIN_ALLOWS, LIMEN_MIN_SESSIONS
//	                the answer minimum: the approvals of actions with one
//	                  label, and the sessions they come from, that an allow
//	                  needs; default 3 and 2 (limen.MinAllows,
//	                  limen.MinSessions), and 0 is no minimum, for tests
//
// A hosted server sets LIMEN_OIDC_ISSUERS and LIMEN_OIDC_AUDIENCE in place
// of LIMEN_TOKEN and LIMEN_SUBJECT, and LIMEN_AUTHORIZER_URL or LIMEN_ADMINS
// for who may do what; docs/running.md has every variable. It keeps one
// ledger directory, so one process serves it.
//
// It stops on an interrupt or a termination signal, after fitting what
// arrived since the last fit. It exits 0 when stopped, 1 when it fails, and
// 2 on a configuration error.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"latere.ai/x/pkg/errwriter"
	"latere.ai/x/pkg/otel"

	"latere.ai/x/limen/engine"
	"latere.ai/x/limen/engine/store/file"
	"latere.ai/x/limen/internal/version"
	"latere.ai/x/limen/server"
)

// The exit codes.
const (
	exitOK     = 0
	exitFail   = 1
	exitConfig = 2
)

// defaultAddr is loopback, because the server is one person's.
const defaultAddr = "127.0.0.1:7720"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// signalContext ends on an interrupt or a termination signal; a test
// replaces it with a context it cancels.
var signalContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// listen opens the server's listener; a test replaces it to learn the port.
var listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

// httpClient reads the issuers' key sets and asks the authorization
// endpoint, instrumented as every outbound call of the family is.
var httpClient = otel.HTTPClient

// clock stamps the records the engine keeps; nil is the wall clock. A test
// sets it to run the conformance cases that need a closed week.
var clock func() time.Time

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	out, errOut := errwriter.New(stdout), errwriter.New(stderr)
	fs := flag.NewFlagSet("limend", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		errOut.Print("Usage: limend [-version]\n\nThe environment variables LIMEN_TOKEN, LIMEN_ADDR, LIMEN_DATA_DIR, and LIMEN_SUBJECT configure limend.\n")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitConfig
	}
	if *showVersion {
		out.Println(version.String("limend"))
		return exitOK
	}
	if fs.NArg() > 0 {
		errOut.Printf("limend: unexpected argument %q.\n", fs.Arg(0))
		return exitConfig
	}
	acc, err := readAccess(getenv, httpClient())
	if err != nil {
		errOut.Printf("limend: %v\n", err)
		return exitConfig
	}
	base := strings.TrimSpace(getenv("LIMEN_BASE_PATH"))
	if err := server.CheckBasePath(base); err != nil {
		errOut.Printf("limend: LIMEN_BASE_PATH is not correct: %v. Use an absolute path with no slash at the end, for example /v1/oversight.\n", err)
		return exitConfig
	}
	minAllows, err := minimum(getenv, "LIMEN_MIN_ALLOWS")
	if err != nil {
		errOut.Printf("limend: %v\n", err)
		return exitConfig
	}
	minSessions, err := minimum(getenv, "LIMEN_MIN_SESSIONS")
	if err != nil {
		errOut.Printf("limend: %v\n", err)
		return exitConfig
	}
	addr := cmp.Or(strings.TrimSpace(getenv("LIMEN_ADDR")), defaultAddr)
	dir := dataDir(getenv)
	if dir == "" {
		errOut.Println("limend: there is no directory for the ledger. Set LIMEN_DATA_DIR.")
		return exitConfig
	}
	st, err := file.Open(dir)
	if err != nil {
		errOut.Printf("limend: cannot open the ledger in %s: %v\n", dir, err)
		return exitFail
	}
	defer func() {
		if err := st.Close(); err != nil {
			errOut.Printf("limend: cannot close the ledger: %v\n", err)
		}
	}()
	ln, err := listen(addr)
	if err != nil {
		errOut.Printf("limend: cannot listen on %s: %v\n", addr, err)
		return exitFail
	}
	ctx, stop := signalContext()
	defer stop()
	if acc.warm != nil {
		if err := acc.warm(ctx); err != nil {
			errOut.Printf("limend: cannot read the key sets of LIMEN_OIDC_ISSUERS: %v\n", err)
			return exitFail
		}
	}

	eng := engine.New(engine.Options{Store: st, Clock: clock, MinAllows: minAllows, MinSessions: minSessions})
	// Close stops the fitters and cannot fail.
	defer func() { _ = eng.Close() }()
	o := acc.options
	o.Logger = slog.New(slog.NewTextHandler(errOut, nil))
	o.BasePath = base
	srv := &http.Server{
		Handler:           server.New(eng, o),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	out.Printf("Limen is available at http://%s. The ledger is in %s. Callers: %s. Use Ctrl-C to stop Limen.\n", ln.Addr(), dir, acc.describe)
	if base != "" {
		out.Printf("The API is also available under %s.\n", base)
	}
	if minAllows < 0 || minSessions < 0 {
		out.Println("Warning: LIMEN_MIN_ALLOWS or LIMEN_MIN_SESSIONS is 0. Limen can suggest allow for an action after fewer approvals. Use 0 only for tests.")
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		errOut.Printf("limend: the server stopped: %v\n", err)
		return exitFail
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		errOut.Printf("limend: cannot stop the server correctly: %v\n", err)
		return exitFail
	}
	// Fit what arrived since the last fit, so a restart predicts from it.
	if err := eng.Flush(shutdown); err != nil {
		errOut.Printf("limend: cannot update the models before it stops: %v\n", err)
		return exitFail
	}
	out.Println("Limen stopped.")
	return exitOK
}

// minimum reads one count of the answer minimum from the variable name, as
// the engine's option: an empty variable is 0, the engine's default, a
// value of 0 is -1, no minimum, and a positive count is itself.
func minimum(getenv func(string) string, name string) (int, error) {
	v := strings.TrimSpace(getenv(name))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	switch {
	case err != nil || n < 0:
		return 0, errors.New(name + " is not correct: " + strconv.Quote(v) + ". Use a whole number, 0 or more. 0 turns this part of the answer minimum off.")
	case n == 0:
		return -1, nil
	}
	return n, nil
}

// dataDir is where the ledger lives: LIMEN_DATA_DIR, or limen under the
// XDG state directory or ~/.local/state.
func dataDir(getenv func(string) string) string {
	if d := strings.TrimSpace(getenv("LIMEN_DATA_DIR")); d != "" {
		return d
	}
	if d := strings.TrimSpace(getenv("XDG_STATE_HOME")); d != "" {
		return filepath.Join(d, "limen")
	}
	if h := strings.TrimSpace(getenv("HOME")); h != "" {
		return filepath.Join(h, ".local", "state", "limen")
	}
	return ""
}
