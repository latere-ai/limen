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

	"latere.ai/x/cribrum"
	"latere.ai/x/cribrum/client"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

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

// start runs cribrumd until stop is called, and returns its address.
func start(t *testing.T, ledger string) (addr string, stop func() (int, string)) {
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
		code <- run(nil, &out, &errOut, env(map[string]string{"CRIBRUM_TOKEN": "s3cret", "CRIBRUM_DATA_DIR": ledger, "CRIBRUM_SUBJECT": "me"}))
	}()
	select {
	case a := <-got:
		addr = a.String()
	case c := <-code:
		t.Fatalf("cribrumd exited %d: %s%s", c, out.String(), errOut.String())
	case <-time.After(10 * time.Second):
		t.Fatal("cribrumd never listened")
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
	addr, stop := start(t, ledger)
	c, err := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	a := cribrum.Action{Org: "local", Subject: "me", ActionID: "s1/t1", Kind: "tool", Name: "bash", Effect: cribrum.EffectWrite, Input: in}
	p, err := c.Predict(ctx, a)
	if err != nil || p.Verdict != cribrum.Ask {
		t.Fatalf("predict: %+v, %v", p, err)
	}
	if err := c.Record(ctx, cribrum.Decision{Key: a.Key, Verdict: cribrum.Ask, ReviewProbability: 1, Source: "topos/confirm"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Answer(ctx, cribrum.Answer{Key: a.Key, Approve: true}); err != nil {
		t.Fatal(err)
	}
	bad, _ := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "wrong"})
	if _, err := bad.Predict(ctx, a); cribrum.CodeOf(err) != cribrum.CodeUnauthenticated {
		t.Errorf("a wrong token: %v", err)
	}
	if code, log := stop(); code != exitOK || !strings.Contains(log, "Cribrum stopped.") {
		t.Fatalf("stop: exit %d: %s", code, log)
	}

	addr, stop = start(t, ledger)
	c, _ = client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
	page, err := c.Cases(ctx, cribrum.CaseQuery{Org: "local", Subject: "me"})
	if err != nil || len(page.Entries) != 1 || len(page.Entries[0].Answers) != 1 {
		t.Errorf("after a restart: %+v, %v", page, err)
	}
	if code, _ := stop(); code != exitOK {
		t.Errorf("second stop: exit %d", code)
	}
}

func TestConfiguration(t *testing.T) {
	cases := []struct {
		args []string
		env  map[string]string
		code int
		want string
	}{
		{nil, nil, exitConfig, "set CRIBRUM_TOKEN"},
		{[]string{"extra"}, map[string]string{"CRIBRUM_TOKEN": "x"}, exitConfig, `unexpected argument "extra"`},
		{[]string{"-bogus"}, nil, exitConfig, "flag provided but not defined"},
		{[]string{"-h"}, nil, exitOK, "Usage: cribrumd"},
		{[]string{"-version"}, nil, exitOK, "cribrumd dev"},
		{nil, map[string]string{"CRIBRUM_TOKEN": "x"}, exitConfig, "no directory for the ledger"},
		{nil, map[string]string{"CRIBRUM_TOKEN": "x", "CRIBRUM_DATA_DIR": "/dev/null/x"}, exitFail, "could not open the ledger"},
		{nil, map[string]string{"CRIBRUM_TOKEN": "x", "CRIBRUM_DATA_DIR": "DIR", "CRIBRUM_ADDR": "256.0.0.1:1"}, exitFail, "could not listen"},
	}
	for _, c := range cases {
		e := map[string]string{}
		for k, v := range c.env {
			if v == "DIR" {
				v = t.TempDir()
			}
			e[k] = v
		}
		var out, errOut bytes.Buffer
		code := run(c.args, &out, &errOut, env(e))
		if code != c.code || !strings.Contains(out.String()+errOut.String(), c.want) {
			t.Errorf("%v %v: exit %d, want %d with %q:\n%s%s", c.args, c.env, code, c.code, c.want, out.String(), errOut.String())
		}
	}
}

func TestDataDir(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"CRIBRUM_DATA_DIR": "/a"}, "/a"},
		{map[string]string{"XDG_STATE_HOME": "/s"}, filepath.Join("/s", "cribrum")},
		{map[string]string{"HOME": "/h"}, filepath.Join("/h", ".local", "state", "cribrum")},
		{map[string]string{}, ""},
	}
	for _, c := range cases {
		if got := dataDir(env(c.env)); got != c.want {
			t.Errorf("%v: %q, want %q", c.env, got, c.want)
		}
	}
}

// A listener that fails ends the server with an error rather than hanging.
func TestServerStops(t *testing.T) {
	listen = func(string) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			ln.Close()
		}
		return ln, err
	}
	t.Cleanup(func() { listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) } })
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut, env(map[string]string{"CRIBRUM_TOKEN": "x", "CRIBRUM_DATA_DIR": t.TempDir()}))
	if code != exitFail || !strings.Contains(errOut.String(), "the server stopped") {
		t.Errorf("exit %d: %s", code, errOut.String())
	}
}
