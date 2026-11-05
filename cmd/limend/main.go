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
//	                the cover rule: the total weight of the considered
//	                  approvals that cover an action, and the sessions they
//	                  come from, that an allow needs in each family of the
//	                  action; default 3 and 2 (limen.MinWeight,
//	                  limen.MinSessions), and 0 turns a setting off, for
//	                  tests; a cost profile can make them greater
//	LIMEN_ANSWER_FLOOR_MS, LIMEN_ANSWER_FATIGUE
//	                weights/1: an approval faster than the floor, or with
//	                  the fatigue count of reviews or more before it in its
//	                  session, has weight 0 if its details were not opened;
//	                  default 2000 and 50 (limen.AnswerFloor,
//	                  limen.AnswerFatigue), and 0 turns a rule off
//	LIMEN_MODEL     the model version the server serves: learned/1, the
//	                  default, or learned/2, which reads the scope of an
//	                  action and the weight of each answer under weights/2
//	                  (spec 011); compare the two with limen compare first
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
	var settings [4]int
	for i, name := range []string{"LIMEN_MIN_ALLOWS", "LIMEN_MIN_SESSIONS", "LIMEN_ANSWER_FLOOR_MS", "LIMEN_ANSWER_FATIGUE"} {
		if settings[i], err = setting(getenv, name); err != nil {
			errOut.Printf("limend: %v\n", err)
			return exitConfig
		}
	}
	minWeight, minSessions, floorMS, fatigue := settings[0], settings[1], settings[2], settings[3]
	model, err := engine.ParseModel(getenv("LIMEN_MODEL"))
	if err != nil {
		errOut.Printf("limend: LIMEN_MODEL is not correct: %v.\n", err)
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

	floor := time.Duration(floorMS) * time.Millisecond
	if floorMS < 0 {
		floor = -1
	}
	eng := engine.New(engine.Options{Store: st, Clock: clock, Model: model, MinWeight: minWeight, MinSessions: minSessions,
		AnswerFloor: floor, AnswerFatigue: fatigue})
	// Close stops the fitters and cannot fail.
	defer func() { _ = eng.Close() }()
	o := acc.options
	o.Logger = slog.New(slog.NewTextHandler(errOut, nil))
	o.BasePath = base
	o.Version, o.Commit, o.BuildTime = version.Version, version.Commit, version.Date
	srv := &http.Server{
		Handler:           server.New(eng, o),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	out.Printf("Limen is available at http://%s. The ledger is in %s. Callers: %s. Use Ctrl-C to stop Limen.\n", ln.Addr(), dir, acc.describe)
	if base != "" {
		out.Printf("The API is also available under %s.\n", base)
	}
	out.Printf("The model is %s.\n", model)
	if minWeight < 0 || minSessions < 0 {
		out.Println("Warning: LIMEN_MIN_ALLOWS or LIMEN_MIN_SESSIONS is 0. Limen can suggest allow for an action after fewer approvals. Use 0 only for tests.")
	}
	if floorMS < 0 || fatigue < 0 {
		out.Println("Warning: LIMEN_ANSWER_FLOOR_MS or LIMEN_ANSWER_FATIGUE is 0. Quick approvals, or approvals late in a long session, can let Limen suggest allow. Use 0 only for tests.")
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

// setting reads one setting of the cover rule or of weights/1 from the
// variable name, as the engine's option: an empty variable is 0, the
// engine's default, a value of 0 is -1, which turns the setting off, and a
// positive whole number is itself.
func setting(getenv func(string) string, name string) (int, error) {
	v := strings.TrimSpace(getenv(name))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	switch {
	case err != nil || n < 0:
		return 0, errors.New(name + " is not correct: " + strconv.Quote(v) + ". Use a whole number, 0 or more. 0 turns this setting off.")
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
