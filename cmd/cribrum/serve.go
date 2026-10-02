// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
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
	"latere.ai/x/cribrum/server"
)

const serveUsage = `Usage: cribrum serve [--addr ADDR] [--store DIR] [--subject SUBJECT]

Serves Cribrum's HTTP API for one person: decision points such as Topos send
their actions, decisions and answers to it, and it learns from them.

Set CRIBRUM_TOKEN to a secret first; every request must carry it as a bearer
token.

Options:
`

// defaultAddr is loopback, because the server is one person's.
const defaultAddr = "127.0.0.1:7720"

// signalContext ends on an interrupt or a termination signal; a test
// replaces it with a context it cancels.
var signalContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// listen opens the server's listener; a test replaces it to learn the port.
var listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

func serve(args []string, out, errOut *errwriter.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("cribrum serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		errOut.Print(serveUsage)
		fs.PrintDefaults()
	}
	addr := fs.String("addr", defaultAddr, "the address to listen on")
	dir := fs.String("store", "", "the directory of the ledger; default $CRIBRUM_DATA_DIR, $XDG_STATE_HOME/cribrum, or ~/.local/state/cribrum")
	subject := fs.String("subject", "local", "the person this server answers to, who may act for every subject in it")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		errOut.Printf("cribrum serve: unexpected argument %q.\n", fs.Arg(0))
		return exitUsage
	}
	token := strings.TrimSpace(getenv("CRIBRUM_TOKEN"))
	if token == "" {
		errOut.Println("cribrum serve: set CRIBRUM_TOKEN to a secret first. Decision points send it as their bearer token.")
		return exitUsage
	}
	if *dir == "" {
		*dir = dataDir(getenv)
	}
	if *dir == "" {
		errOut.Println("cribrum serve: there is no directory for the ledger. Pass --store or set CRIBRUM_DATA_DIR.")
		return exitUsage
	}
	st, err := openStore(*dir)
	if err != nil {
		errOut.Printf("cribrum serve: could not open the ledger in %s: %v\n", *dir, err)
		return exitFail
	}
	defer func() {
		if err := st.Close(); err != nil {
			errOut.Printf("cribrum serve: closing the ledger: %v\n", err)
		}
	}()
	ln, err := listen(*addr)
	if err != nil {
		errOut.Printf("cribrum serve: could not listen on %s: %v\n", *addr, err)
		return exitFail
	}
	ctx, stop := signalContext()
	defer stop()

	eng := engine.New(engine.Options{Store: st})
	// Close stops the fitters and cannot fail.
	defer func() { _ = eng.Close() }()
	o := server.Local(token, *subject)
	o.Logger = slog.New(slog.NewTextHandler(errOut, nil))
	srv := &http.Server{
		Handler:           server.New(eng, o),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	out.Printf("Cribrum is serving http://%s with its ledger in %s. Stop it with Ctrl-C.\n", ln.Addr(), *dir)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		errOut.Printf("cribrum serve: the server stopped: %v\n", err)
		return exitFail
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		errOut.Printf("cribrum serve: stopping: %v\n", err)
		return exitFail
	}
	// Fit what arrived since the last fit, so a restart predicts from it.
	if err := eng.Flush(shutdown); err != nil {
		errOut.Printf("cribrum serve: fitting before stopping: %v\n", err)
		return exitFail
	}
	out.Println("Cribrum stopped.")
	return exitOK
}

// dataDir is where a ledger lives when --store names none.
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
