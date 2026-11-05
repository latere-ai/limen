// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package smoke

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// limend serves the route under a base path and asks every request for a
// token. The stub answers that way under /v1/oversight and nothing else.
func limend(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/oversight/cases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func script(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Join(filepath.Dir(file), "release.sh")
}

// run executes the script and returns its combined output and whether it
// exited zero. It skips when the tools the script forks are not on PATH,
// which is the case in the gate's hermetic run.
func run(t *testing.T, env ...string) (string, bool) {
	t.Helper()
	for _, cmd := range []string{"bash", "curl", "grep", "mktemp"} {
		if _, err := exec.LookPath(cmd); err != nil {
			t.Skipf("%s is not on PATH, so release.sh cannot run here", cmd)
		}
	}
	cmd := exec.CommandContext(context.Background(), "bash", script(t))
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func TestSmokePassesAndWritesEvidence(t *testing.T) {
	srv := limend(t)
	evidence := filepath.Join(t.TempDir(), "smoke.md")
	out, ok := run(t, "BASE_URL="+srv.URL+"/v1/oversight", "TAG=v1.2.3", "COMMIT=abc1234", "OUTPUT_MD="+evidence)
	if !ok {
		t.Fatalf("the smoke failed against a healthy route:\n%s", out)
	}
	md, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Release evidence", "- Tag: `v1.2.3`", "- Commit: `abc1234`", "answered 401"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("the evidence lacks %q:\n%s", want, md)
		}
	}
}

// TestSmokeFailsOnARouteNoIngressClaims is the rollout that lands the image
// without the rule that claims the prefix: the origin answers 404.
func TestSmokeFailsOnARouteNoIngressClaims(t *testing.T) {
	srv := limend(t)
	out, ok := run(t, "BASE_URL="+srv.URL+"/v1/wrong", "TAG=v1.2.3")
	if ok {
		t.Fatalf("the smoke passed against an unrouted prefix:\n%s", out)
	}
	if !strings.Contains(out, "expected 401") {
		t.Errorf("the failure does not name the status:\n%s", out)
	}
}

// TestSmokeFailsOnADownService holds the failure of a Deployment that is
// not answering at all.
func TestSmokeFailsOnADownService(t *testing.T) {
	srv := limend(t)
	url := srv.URL
	srv.Close()
	out, ok := run(t, "BASE_URL="+url+"/v1/oversight", "TAG=v1.2.3")
	if ok {
		t.Fatalf("the smoke passed against a service that is down:\n%s", out)
	}
	if !strings.Contains(out, "curl failed") {
		t.Errorf("the failure does not say the service did not answer:\n%s", out)
	}
}

func TestSmokeFailsOnAnErrorThatIsNotLimens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("nginx"))
	}))
	t.Cleanup(srv.Close)
	out, ok := run(t, "BASE_URL="+srv.URL)
	if ok {
		t.Fatalf("the smoke passed against a 401 that is not limend's:\n%s", out)
	}
}

func TestSmokeRefusesWithNoTarget(t *testing.T) {
	out, ok := run(t, "BASE_URL=")
	if ok || !strings.Contains(out, "BASE_URL is required") {
		t.Fatalf("the smoke did not refuse with no target (ok=%v):\n%s", ok, out)
	}
}
