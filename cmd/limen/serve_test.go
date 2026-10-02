// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/limen"
	"latere.ai/x/limen/client"
)

// syncBuffer is a buffer two goroutines may write and read.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startServe runs `limen serve` until stop is called, and returns the
// address it listens on.
func startServe(t *testing.T, ledger string) (addr string, stop func() (int, string)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan net.Addr, 1)
	signalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	listen = func(string) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			got <- ln.Addr()
		}
		return ln, err
	}
	t.Cleanup(func() {
		signalContext = func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }
		listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
	})
	var out, errOut syncBuffer
	code := make(chan int, 1)
	go func() {
		code <- run([]string{"serve", "--store", ledger, "--subject", "me"}, strings.NewReader(""), &out, &errOut, env(map[string]string{"LIMEN_TOKEN": "s3cret"}))
	}()
	select {
	case a := <-got:
		addr = a.String()
	case c := <-code:
		t.Fatalf("serve exited %d: %s%s", c, out.String(), errOut.String())
	case <-time.After(10 * time.Second):
		t.Fatal("serve never listened")
	}
	return addr, func() (int, string) {
		cancel()
		return <-code, out.String() + errOut.String()
	}
}

// A decision point predicts, records and answers through the served API,
// and the ledger survives a restart.
func TestServe(t *testing.T) {
	ledger := t.TempDir()
	addr, stop := startServe(t, ledger)
	c, err := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	a := limen.Action{Org: "local", Subject: "me", ActionID: "s1/t1", Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in}
	p, err := c.Predict(ctx, a)
	if err != nil || p.Verdict != limen.Ask {
		t.Fatalf("predict: %+v, %v", p, err)
	}
	if err := c.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "topos/confirm"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Answer(ctx, limen.Answer{Key: a.Key, Approve: true}); err != nil {
		t.Fatal(err)
	}
	bad, _ := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "wrong"})
	if _, err := bad.Predict(ctx, a); limen.CodeOf(err) != limen.CodeUnauthenticated {
		t.Errorf("a wrong token: %v", err)
	}
	if code, log := stop(); code != exitOK || !strings.Contains(log, "Limen stopped.") {
		t.Fatalf("stop: exit %d: %s", code, log)
	}

	addr, stop = startServe(t, ledger)
	c, _ = client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
	page, err := c.Cases(ctx, limen.CaseQuery{Org: "local", Subject: "me"})
	if err != nil || len(page.Entries) != 1 || len(page.Entries[0].Answers) != 1 {
		t.Errorf("after a restart: %+v, %v", page, err)
	}
	if code, _ := stop(); code != exitOK {
		t.Errorf("second stop: exit %d", code)
	}
}

func TestServeUsage(t *testing.T) {
	cases := []struct {
		args []string
		env  map[string]string
		code int
		want string
	}{
		{[]string{"serve"}, nil, exitUsage, "set LIMEN_TOKEN"},
		{[]string{"serve", "extra"}, map[string]string{"LIMEN_TOKEN": "x"}, exitUsage, `unexpected argument "extra"`},
		{[]string{"serve", "--bogus"}, nil, exitUsage, "flag provided but not defined"},
		{[]string{"serve", "-h"}, nil, exitOK, "Usage: limen serve"},
		{[]string{"serve"}, map[string]string{"LIMEN_TOKEN": "x", "HOME": ""}, exitUsage, "no directory for the ledger"},
		{[]string{"serve", "--store", "/dev/null/x"}, map[string]string{"LIMEN_TOKEN": "x"}, exitFail, "could not open the ledger"},
		{[]string{"serve", "--store", "STORE", "--addr", "256.0.0.1:1"}, map[string]string{"LIMEN_TOKEN": "x"}, exitFail, "could not listen"},
	}
	for _, c := range cases {
		args := append([]string(nil), c.args...)
		for i, a := range args {
			if a == "STORE" {
				args[i] = t.TempDir()
			}
		}
		code, out, errOut := runCLI(t, "", env(c.env), args...)
		if code != c.code || !strings.Contains(out+errOut, c.want) {
			t.Errorf("%v: exit %d, want %d with %q:\n%s%s", c.args, code, c.code, c.want, out, errOut)
		}
	}
}

func TestDataDir(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LIMEN_DATA_DIR": "/a"}, "/a"},
		{map[string]string{"XDG_STATE_HOME": "/s"}, filepath.Join("/s", "limen")},
		{map[string]string{"HOME": "/h"}, filepath.Join("/h", ".local", "state", "limen")},
		{map[string]string{}, ""},
	}
	for _, c := range cases {
		if got := dataDir(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%v: %q, want %q", c.env, got, c.want)
		}
	}
}
