// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command cribrumd serves Cribrum's HTTP API over a ledger kept in a
// directory, for one person: decision points such as Topos send their
// actions, decisions and answers to it, and it learns from them.
//
// It is configured by the environment:
//
//	CRIBRUM_TOKEN     the bearer every request carries; required
//	CRIBRUM_ADDR      the address to listen on; default 127.0.0.1:7720
//	CRIBRUM_DATA_DIR  the ledger's directory; default $XDG_STATE_HOME/cribrum,
//	                  or ~/.local/state/cribrum
//	CRIBRUM_SUBJECT   the person the server answers to, who may act for every
//	                  subject in it; default local
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

	"latere.ai/x/cribrum/engine"
	"latere.ai/x/cribrum/engine/store/file"
	"latere.ai/x/cribrum/internal/version"
	"latere.ai/x/cribrum/server"
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

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	out, errOut := errwriter.New(stdout), errwriter.New(stderr)
	fs := flag.NewFlagSet("cribrumd", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		errOut.Print("Usage: cribrumd [-version]\n\nConfigured by CRIBRUM_TOKEN, CRIBRUM_ADDR, CRIBRUM_DATA_DIR and CRIBRUM_SUBJECT.\n")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitConfig
	}
	if *showVersion {
		out.Println(version.String("cribrumd"))
		return exitOK
	}
	if fs.NArg() > 0 {
		errOut.Printf("cribrumd: unexpected argument %q.\n", fs.Arg(0))
		return exitConfig
	}
	token := strings.TrimSpace(getenv("CRIBRUM_TOKEN"))
	if token == "" {
		errOut.Println("cribrumd: set CRIBRUM_TOKEN to a secret first. Decision points send it as their bearer token.")
		return exitConfig
	}
	addr := cmp.Or(strings.TrimSpace(getenv("CRIBRUM_ADDR")), defaultAddr)
	subject := cmp.Or(strings.TrimSpace(getenv("CRIBRUM_SUBJECT")), "local")
	dir := dataDir(getenv)
	if dir == "" {
		errOut.Println("cribrumd: there is no directory for the ledger. Set CRIBRUM_DATA_DIR.")
		return exitConfig
	}
	st, err := file.Open(dir)
	if err != nil {
		errOut.Printf("cribrumd: could not open the ledger in %s: %v\n", dir, err)
		return exitFail
	}
	defer func() {
		if err := st.Close(); err != nil {
			errOut.Printf("cribrumd: closing the ledger: %v\n", err)
		}
	}()
	ln, err := listen(addr)
	if err != nil {
		errOut.Printf("cribrumd: could not listen on %s: %v\n", addr, err)
		return exitFail
	}
	ctx, stop := signalContext()
	defer stop()

	eng := engine.New(engine.Options{Store: st})
	// Close stops the fitters and cannot fail.
	defer func() { _ = eng.Close() }()
	o := server.Local(token, subject)
	o.Logger = slog.New(slog.NewTextHandler(errOut, nil))
	srv := &http.Server{
		Handler:           server.New(eng, o),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	out.Printf("Cribrum is serving http://%s with its ledger in %s. Stop it with Ctrl-C.\n", ln.Addr(), dir)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		errOut.Printf("cribrumd: the server stopped: %v\n", err)
		return exitFail
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		errOut.Printf("cribrumd: stopping: %v\n", err)
		return exitFail
	}
	// Fit what arrived since the last fit, so a restart predicts from it.
	if err := eng.Flush(shutdown); err != nil {
		errOut.Printf("cribrumd: fitting before stopping: %v\n", err)
		return exitFail
	}
	out.Println("Cribrum stopped.")
	return exitOK
}

// dataDir is where the ledger lives: CRIBRUM_DATA_DIR, or cribrum under the
// XDG state directory or ~/.local/state.
func dataDir(getenv func(string) string) string {
	if d := strings.TrimSpace(getenv("CRIBRUM_DATA_DIR")); d != "" {
		return d
	}
	if d := strings.TrimSpace(getenv("XDG_STATE_HOME")); d != "" {
		return filepath.Join(d, "cribrum")
	}
	if h := strings.TrimSpace(getenv("HOME")); h != "" {
		return filepath.Join(h, ".local", "state", "cribrum")
	}
	return ""
}
