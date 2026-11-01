// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command limend serves Limen's HTTP API over a ledger kept in a
// directory, for one person: decision points such as Topos send their
// actions, decisions and answers to it, and it learns from them.
//
// It is configured by the environment:
//
//	LIMEN_TOKEN     the bearer every request carries; required
//	LIMEN_ADDR      the address to listen on; default 127.0.0.1:7720
//	LIMEN_DATA_DIR  the ledger's directory; default $XDG_STATE_HOME/limen,
//	                  or ~/.local/state/limen
//	LIMEN_SUBJECT   the person the server answers to, who may act for every
//	                  subject in it; default local
//	LIMEN_BASE_PATH a second prefix for the API in place of /v1, for an
//	                  ingress that forwards a public path unchanged, for
//	                  example /v1/oversight; default none
//
// docs/running.md has the variables of a hosted server.
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

	eng := engine.New(engine.Options{Store: st})
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
