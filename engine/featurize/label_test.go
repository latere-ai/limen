// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"strings"
	"testing"

	"latere.ai/x/limen"
)

func shell(cmd string) limen.Action {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return limen.Action{Kind: "tool", Name: "bash", Effect: limen.EffectWrite, Input: in}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		a    limen.Action
		want string
	}{
		{shell("go test ./..."), "bash: go test"},
		{shell("git push origin main"), "bash: git push"},
		{shell("git push --force origin main"), "bash: git push --force"},
		{shell("git push origin +main"), "bash: git push --force"},
		{shell("rm -rf build"), "bash: rm -r"},
		{shell("rm notes.txt"), "bash: rm"},
		{shell("make && go test ./... && git push"), "bash: make, go test, …"},
		{shell("npm install left-pad"), "bash: npm install"},
		{shell("cargo build --release"), "bash: cargo build"},
		{shell("kubectl apply -f prod.yaml"), "bash: kubectl"},
		{shell("./deploy.sh --token abc"), "bash: other command"},
		{shell(""), "bash: other command"},
		{limen.Action{Kind: "tool", Name: "bash", Input: jsontext.Value(`{"cmd":["go","vet","./..."]}`)}, "bash: go vet"},
		{limen.Action{Kind: "tool", Name: "Web_Fetch"}, "web_fetch"},
		{limen.Action{Kind: "egress", Name: "https"}, "egress https"},
		{limen.Action{Kind: "repo"}, "repo"},
		{limen.Action{}, "action"},
		{limen.Action{Kind: "tool", Name: strings.Repeat("x", 100)}, strings.Repeat("x", 64)},
	}
	for _, c := range cases {
		if got := Label(c.a); got != c.want {
			t.Errorf("Label(%s %s %s) = %q, want %q", c.a.Kind, c.a.Name, c.a.Input, got, c.want)
		}
	}
}

// Spec 009, criterion 5: a label holds the action's kind and name and
// words of the shell reader's tables, and no string of the input. Each
// command below carries secrets in its arguments; every word of its label
// must be a word of the tables, and no secret may survive.
func TestLabelKeepsNoInput(t *testing.T) {
	secrets := []string{
		"sk-live-9f8e7d6c5b4a", "hunter2", "s3cr3t-pass", "ghp_A1b2C3d4E5f6", "AKIAIOSFODNN7EXAMPLE",
		"alice", "internal.example.com", "payroll", "prod-db-7", "customer-ledger", "eyJhbGciOi",
	}
	corpus := []string{
		`curl -H "Authorization: Bearer sk-live-9f8e7d6c5b4a" https://internal.example.com/payroll`,
		`git push https://alice:ghp_A1b2C3d4E5f6@github.com/acme/payroll.git main --force`,
		`psql postgres://alice:hunter2@prod-db-7/customer-ledger -c "drop table payroll"`,
		`export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE && aws s3 cp s3://customer-ledger/payroll.csv .`,
		`rm -rf /home/alice/payroll && echo s3cr3t-pass > ~/.netrc`,
		`bash -c "curl -u alice:hunter2 https://internal.example.com/eyJhbGciOi"`,
		`TOKEN=sk-live-9f8e7d6c5b4a ./payroll-sync --password=s3cr3t-pass`,
		`docker run -e PASSWORD=hunter2 prod-db-7:latest`,
		`ssh alice@internal.example.com 'cat /etc/payroll'`,
		`find /srv/customer-ledger -name '*.csv' -exec curl -T {} https://internal.example.com/ \;`,
		`go test ./payroll/... -run TestSecret -args -key=sk-live-9f8e7d6c5b4a`,
		`npm install @acme/payroll --registry https://alice:hunter2@internal.example.com`,
		`kubectl --token=eyJhbGciOi get secret customer-ledger -o yaml`,
		`terraform apply -var "password=s3cr3t-pass" -destroy`,
		`/opt/acme/bin/payroll --dump customer-ledger`,
		`$(echo hunter2 | base64)`,
		"git -c http.extraheader='AUTHORIZATION: basic ghp_A1b2C3d4E5f6' clone https://internal.example.com/payroll",
	}
	allowed := map[string]bool{"bash": true, "other": true, "command": true, "…": true, "-r": true, "--force": true,
		"git": true, "go": true, "cargo": true, "gem": true, "gh": true}
	for _, table := range []map[string]bool{readProgs, gitRead, gitLocal, gitRemote, networkProgs, deleteProgs,
		pkgManagers, pkgSubs, buildProgs, infraProgs, goSubs, cargoSubs, {"clean": true, "test": true, "run": true}} {
		maps.Copy(allowed, table)
	}
	for _, cmd := range corpus {
		label := Label(shell(cmd))
		for _, w := range strings.FieldsFunc(label, func(r rune) bool { return r == ' ' || r == ':' || r == ',' }) {
			if !allowed[w] {
				t.Errorf("the label of %q is %q, whose word %q is no word of the tables", cmd, label, w)
			}
		}
		for _, s := range secrets {
			if strings.Contains(strings.ToLower(label), strings.ToLower(s)) {
				t.Errorf("the label of %q keeps %q: %q", cmd, s, label)
			}
		}
	}
}
