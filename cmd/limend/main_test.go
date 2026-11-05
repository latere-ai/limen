// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
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
	"latere.ai/x/limen/internal/version"
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
	if code, log := stop(); code != exitOK || !strings.Contains(log, "Limen stopped.") || !strings.Contains(log, "The model is learned/1.") {
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

// The build limend was built as is public under LIMEN_BASE_PATH, with no
// token: the route a release check reads from outside.
func TestVersionUnderTheBasePath(t *testing.T) {
	oldV, oldC, oldD := version.Version, version.Commit, version.Date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldV, oldC, oldD })
	version.Version, version.Commit, version.Date = "v1.2.3", "abc1234", "2026-11-05T00:00:00Z"
	addr, stop := startWith(t, map[string]string{"LIMEN_TOKEN": "s3cret", "LIMEN_DATA_DIR": t.TempDir(), "LIMEN_BASE_PATH": "/v1/oversight"})
	const want = `{"version":"v1.2.3","commit":"abc1234","build_time":"2026-11-05T00:00:00Z"}`
	resp, err := http.Get("http://" + addr + "/v1/oversight/version")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != want {
		t.Errorf("GET /v1/oversight/version with no token: %d %s", resp.StatusCode, body)
	}
	if code, log := stop(); code != exitOK {
		t.Fatalf("stop: exit %d: %s", code, log)
	}
}

// The cover rule has the contract's settings by default, and
// LIMEN_MIN_ALLOWS, LIMEN_MIN_SESSIONS, LIMEN_ANSWER_FLOOR_MS and
// LIMEN_ANSWER_FATIGUE set them: one approval of a publish under an allow
// threshold of 0.65 is a few ask by default, held by the settings set, and
// an allow with both minimums at 0, which limend warns about. A quick
// approval counts only with the floor at 0, which limend warns about too.
func TestCoverSettings(t *testing.T) {
	for _, c := range []struct {
		vars    map[string]string
		details bool
		want    limen.Cover
		warn    string
	}{
		{nil, true, limen.Cover{Weight: 1, Sessions: 1, MinWeight: limen.MinWeight, MinSessions: limen.MinSessions}, ""},
		{map[string]string{"LIMEN_MIN_ALLOWS": "5", "LIMEN_MIN_SESSIONS": " 1 "}, true, limen.Cover{Weight: 1, Sessions: 1, MinWeight: 5, MinSessions: 1}, ""},
		{map[string]string{"LIMEN_MIN_SESSIONS": "0"}, true, limen.Cover{Weight: 1, Sessions: 1, MinWeight: limen.MinWeight}, "LIMEN_MIN_ALLOWS or LIMEN_MIN_SESSIONS is 0."},
		{map[string]string{"LIMEN_MIN_ALLOWS": "0", "LIMEN_MIN_SESSIONS": "0"}, true, limen.Cover{Covered: true, Weight: 1, Sessions: 1}, "LIMEN_MIN_ALLOWS or LIMEN_MIN_SESSIONS is 0."},
		{nil, false, limen.Cover{MinWeight: limen.MinWeight, MinSessions: limen.MinSessions}, ""},
		{map[string]string{"LIMEN_ANSWER_FLOOR_MS": "0"}, false, limen.Cover{Weight: 1, Sessions: 1, MinWeight: limen.MinWeight, MinSessions: limen.MinSessions}, "LIMEN_ANSWER_FLOOR_MS or LIMEN_ANSWER_FATIGUE is 0."},
		{map[string]string{"LIMEN_ANSWER_FLOOR_MS": "1", "LIMEN_ANSWER_FATIGUE": "0"}, true, limen.Cover{Weight: 1, Sessions: 1, MinWeight: limen.MinWeight, MinSessions: limen.MinSessions}, "LIMEN_ANSWER_FLOOR_MS or LIMEN_ANSWER_FATIGUE is 0."},
	} {
		vars := map[string]string{"LIMEN_TOKEN": "s3cret", "LIMEN_DATA_DIR": t.TempDir(), "LIMEN_SUBJECT": "me"}
		maps.Copy(vars, c.vars)
		addr, stop := startWith(t, vars)
		c2, err := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if _, err := c2.PutProfile(ctx, limen.Profile{Org: "local", Costs: limen.Costs{FalseAllow: 1 / 0.35, FalseBlock: 1 / 0.35, Ask: 1}, AuditRate: 0.05}); err != nil {
			t.Fatal(err)
		}
		publish := func(id string) limen.Action {
			return limen.Action{Org: "local", Subject: "me", ActionID: "ses/" + id, Actor: &limen.Actor{Kind: "agent", Session: "ses"},
				Kind: "tool", Name: "publish", Effect: limen.EffectExternal}
		}
		a := publish("a")
		if err := c2.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "topos/confirm", Action: &a}); err != nil {
			t.Fatal(err)
		}
		how := limen.HowAnswered{}
		if c.details {
			how.DetailsOpened = new(true)
		}
		if err := c2.Answer(ctx, limen.Answer{Key: a.Key, Approve: true, HowAnswered: how}); err != nil {
			t.Fatal(err)
		}
		// The model fits in the background after the answer. Wait for the fit
		// through the explanation of the answered action, which adds no
		// action: each new action advances the person's time, and a probe
		// many decisions after the answer reads a tolerance that has drifted
		// back toward the prior.
		for range 500 {
			x, err := c2.Explain(ctx, a.Key)
			if err != nil {
				t.Fatal(err)
			}
			if x.Mean != 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		p, err := c2.Predict(ctx, publish("probe"))
		if err != nil {
			t.Fatal(err)
		}
		code, log := stop()
		if code != exitOK || p.Cover != c.want || (p.Verdict == limen.Allow) != c.want.Covered {
			t.Errorf("%v: %+v, want %+v: exit %d", c.vars, p, c.want, code)
		}
		for _, w := range []string{"LIMEN_MIN_ALLOWS or LIMEN_MIN_SESSIONS is 0.", "LIMEN_ANSWER_FLOOR_MS or LIMEN_ANSWER_FATIGUE is 0."} {
			if strings.Contains(log, "Warning: "+w) != (c.warn == w) {
				t.Errorf("%v: the log %q, want the warning %q", c.vars, log, c.warn)
			}
		}
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
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_MIN_ALLOWS": "three"}, exitConfig, `LIMEN_MIN_ALLOWS is not correct: "three"`},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_MIN_SESSIONS": "-1"}, exitConfig, `LIMEN_MIN_SESSIONS is not correct: "-1"`},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_ANSWER_FLOOR_MS": "2s"}, exitConfig, `LIMEN_ANSWER_FLOOR_MS is not correct: "2s"`},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_ANSWER_FATIGUE": "-3"}, exitConfig, `LIMEN_ANSWER_FATIGUE is not correct: "-3"`},
		{nil, map[string]string{"LIMEN_TOKEN": "x", "LIMEN_DATA_DIR": "DIR", "LIMEN_MODEL": "learned/3"}, exitConfig, `LIMEN_MODEL is not correct: "learned/3" is not a model version`},
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
// A case that needs a closed week gives limend its clock.
func TestConformance(t *testing.T) {
	running := func(t *testing.T, now func() time.Time) limen.Service {
		was := clock
		t.Cleanup(func() { clock = was })
		clock = now
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
	}
	conformance.Run(t, conformance.Target{
		New:     func(t *testing.T) limen.Service { return running(t, nil) },
		Clocked: running,
	})
}

// LIMEN_MODEL switches the server to learned/2 (spec 011). It fits
// version 2 from the cases that learned/1 kept, keeps the snapshot of
// learned/1, and names its model in each prediction; switching back
// serves learned/1 again.
func TestModelSwitch(t *testing.T) {
	ledger := t.TempDir()
	ctx := context.Background()
	in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	act := func(id string) limen.Action {
		return limen.Action{Org: "local", Subject: "me", ActionID: id, Actor: &limen.Actor{Kind: "agent", Session: "ses-" + id},
			Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in}
	}
	serve := func(model string) (*client.Client, func() (int, string)) {
		addr, stop := startWith(t, map[string]string{"LIMEN_TOKEN": "s3cret", "LIMEN_DATA_DIR": ledger, "LIMEN_SUBJECT": "me", "LIMEN_MODEL": model})
		c, err := client.New("http://"+addr, client.Options{HTTP: http.DefaultClient, Token: "s3cret"})
		if err != nil {
			t.Fatal(err)
		}
		return c, stop
	}
	probe := func(c *client.Client, prefix string) limen.Prediction {
		var p limen.Prediction
		var err error
		// The model fits in the background after the answers and the switch.
		for i := 0; i < 500 && (i == 0 || p.Approve == 0.5); i++ {
			if p, err = c.Predict(ctx, act(fmt.Sprintf("%s-%d", prefix, i))); err != nil {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		return p
	}
	c, stop := serve("")
	for i := range 4 {
		a := act(fmt.Sprintf("t%d", i))
		if _, err := c.Predict(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := c.Record(ctx, limen.Decision{Key: a.Key, Verdict: limen.Ask, ReviewProbability: 1, Source: "topos/confirm"}); err != nil {
			t.Fatal(err)
		}
		if err := c.Answer(ctx, limen.Answer{Key: a.Key, Approve: true, DetailsOpened: new(true)}); err != nil {
			t.Fatal(err)
		}
	}
	one := probe(c, "one")
	if code, log := stop(); code != exitOK || one.Source != "learned/1" || one.Approve <= 0.5 || !strings.Contains(log, "The model is learned/1.") {
		t.Fatalf("learned/1: %+v, exit %d: %s", one, code, log)
	}
	c, stop = serve(" learned/2 ")
	two := probe(c, "two")
	if code, log := stop(); code != exitOK || two.Source != "learned/2" || two.Approve <= 0.5 || !strings.Contains(log, "The model is learned/2.") {
		t.Fatalf("learned/2: %+v, exit %d: %s", two, code, log)
	}
	for _, v := range []string{"1", "2"} {
		if m, _ := filepath.Glob(filepath.Join(ledger, "subjects", "*", "*", "snapshots", v+".snap")); len(m) != 1 {
			t.Errorf("snapshots of version %s: %v", v, m)
		}
	}
	c, stop = serve("learned/1")
	back := probe(c, "back")
	if code, _ := stop(); code != exitOK || back.Source != "learned/1" {
		t.Errorf("back to learned/1: %+v, exit %d", back, code)
	}
}
