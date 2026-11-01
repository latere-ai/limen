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
	"latere.ai/x/limen/conformance"
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

// restore puts back the signal context and the listener a test replaces,
// so the next test runs limend's own.
func restore(t *testing.T) {
	t.Helper()
	sc, l := signalContext, listen
	t.Cleanup(func() { signalContext, listen = sc, l })
}

// start runs limend for one person until stop is called, and returns its
// address.
func start(t *testing.T, ledger string) (addr string, stop func() (int, string)) {
	t.Helper()
	return startWith(t, map[string]string{"LIMEN_TOKEN": "s3cret", "LIMEN_DATA_DIR": ledger, "LIMEN_SUBJECT": "me"})
}

// startWith runs limend with the environment vars until stop is called.
func startWith(t *testing.T, vars map[string]string) (addr string, stop func() (int, string)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan net.Addr, 1)
	restore(t)
	signalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	listen = func(string) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			got <- ln.Addr()
		}
		return ln, err
	}
	var out, errOut syncBuffer
	code := make(chan int, 1)
	go func() {
		code <- run(nil, &out, &errOut, env(vars))
	}()
	select {
	case a := <-got:
		addr = a.String()
	case c := <-code:
		t.Fatalf("limend exited %d: %s%s", c, out.String(), errOut.String())
	case <-time.After(10 * time.Second):
		t.Fatal("limend never listened")
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

	addr, stop = start(t, ledger)
	c, _ = client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
	page, err := c.Cases(ctx, limen.CaseQuery{Org: "local", Subject: "me"})
	if err != nil || len(page.Entries) != 1 || len(page.Entries[0].Answers) != 1 {
		t.Errorf("after a restart: %+v, %v", page, err)
	}
	if code, _ := stop(); code != exitOK {
		t.Errorf("second stop: exit %d", code)
	}
}

// With LIMEN_BASE_PATH the API answers under it as well as under /v1.
func TestBasePath(t *testing.T) {
	addr, stop := startWith(t, map[string]string{"LIMEN_TOKEN": "s3cret", "LIMEN_DATA_DIR": t.TempDir(), "LIMEN_BASE_PATH": "/v1/oversight"})
	ctx := context.Background()
	for _, base := range []string{"", "/v1/oversight"} {
		c, err := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret", BasePath: base})
		if err != nil {
			t.Fatal(err)
		}
		l, err := c.Learned(ctx, limen.LearnedQuery{Org: "local", Subject: "local"})
		if err != nil || l.Subject != "local" {
			t.Errorf("learned under %q: %+v, %v", base, l, err)
		}
	}
	if code, log := stop(); code != exitOK || !strings.Contains(log, "The API is also available under /v1/oversight.") {
		t.Fatalf("stop: exit %d: %s", code, log)
	}
}

func TestConfiguration(t *testing.T) {
	cases := []struct {
		args []string
		env  map[string]string
		code int
		want string
	}{
		{nil, nil, exitConfig, "set LIMEN_TOKEN"},
		{[]string{"extra"}, map[string]string{"LIMEN_TOKEN": "x"}, exitConfig, `unexpected argument "extra"`},
		{[]string{"-bogus"}, nil, exitConfig, "flag provided but not defined"},
		{[]string{"-h"}, nil, exitOK, "Usage: limend"},
		{[]string{"-version"}, nil, exitOK, "limend dev"},
		{nil, map[string]string{"LIMEN_TOKEN": "x"}, exitConfig, "no directory for the ledger"},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_BASE_PATH": "/v1/oversight/"}, exitConfig, "LIMEN_BASE_PATH is not correct"},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_BASE_PATH": "/v1"}, exitConfig, "LIMEN_BASE_PATH is not correct"},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "/dev/null/x"}, exitFail, "cannot open the ledger"},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_ADDR": "256.0.0.1:1"}, exitFail, "cannot listen"},
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
		{map[string]string{"LIMEN_DATA_DIR": "/a"}, "/a"},
		{map[string]string{"XDG_STATE_HOME": "/s"}, filepath.Join("/s", "limen")},
		{map[string]string{"HOME": "/h"}, filepath.Join("/h", ".local", "state", "limen")},
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
	restore(t)
	listen = func(string) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			ln.Close()
		}
		return ln, err
	}
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut, env(map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": t.TempDir()}))
	if code != exitFail || !strings.Contains(errOut.String(), "the server stopped") {
		t.Errorf("exit %d: %s", code, errOut.String())
	}
}

// The client against a running limend passes the conformance suite
// (spec 001, criterion 5; spec 003, criterion 7): each case starts limend
// on a fresh ledger directory, as a person runs it, and stops it after.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Target{
		New: func(t *testing.T) limen.Service {
			addr, stop := start(t, t.TempDir())
			t.Cleanup(func() {
				if code, log := stop(); code != exitOK {
					t.Errorf("stop: exit %d: %s", code, log)
				}
			})
			c, err := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
			if err != nil {
				t.Fatal(err)
			}
			return c
		},
	})
}
