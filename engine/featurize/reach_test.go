// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/limen"
)

// The bounds of a scope written short: machine, remote, network,
// organization, public, each n (no), y (yes) or ? (unknown).
func crosses(s string) limen.Crosses {
	b := func(c byte) limen.Bound {
		switch c {
		case 'n':
			return limen.BoundNo
		case 'y':
			return limen.BoundYes
		}
		return limen.BoundUnknown
	}
	return limen.Crosses{Machine: b(s[0]), Remote: b(s[1]), Network: b(s[2]), Organization: b(s[3]), Public: b(s[4])}
}

const (
	u    = limen.Unknown
	none = limen.UndoNone
	rev  = limen.UndoReversible
	rec  = limen.UndoRecoverable
	irr  = limen.UndoIrreversible
	prim = limen.ClassPrimary
)

// One command for each row of the shell table of spec 011 (criterion 1).
func TestShellTable(t *testing.T) {
	cases := []struct {
		cmd           string
		undo, crosses string
		class, kind   string
	}{
		{"cat README.md", none, "nnnnn", u, ""},
		{"go build ./...", rev, "nnnnn", u, ""},
		{"git commit -m 'fix'", rev, "nnnnn", u, ""},
		{"git restore engine.go", irr, "nnnnn", u, ""},
		{"git checkout -- engine.go", irr, "nnnnn", u, ""},
		{"git checkout feature", rev, "nnnnn", u, ""},
		{"git stash drop", irr, "nnnnn", u, ""},
		{"git stash clear", irr, "nnnnn", u, ""},
		{"git pull --rebase", rev, "nyn?n", u, ""},
		{"git fetch origin", rev, "nyn?n", u, ""},
		{"git clone https://example.com/r.git", rev, "nyn?n", u, ""},
		{"git push origin feature", rec, "nyn??", u, "ref"},
		{"git push --force origin feature", irr, "nyn??", u, "ref"},
		{"git push origin :old", irr, "nyn??", u, "ref"},
		{"git branch -D old", irr, "nnnnn", u, "ref"},
		{"gh pr merge 12 --squash", rec, "nyn?n", prim, "ref"},
		{"gh repo create tool --public", irr, "nyn?y", u, "repository"},
		{"gh repo edit --visibility public", irr, "nyn?y", u, "repository"},
		{"gh repo edit --visibility=public", irr, "nyn?y", u, "repository"},
		{"gh repo delete tool --yes", irr, "nyn?n", u, "repository"},
		{"gh release delete v1", irr, "nyn?n", u, ""},
		{"gh repo view", u, "nyn?n", u, "repository"},
		{"gh -R owner/repo issue list", u, "nyn?n", u, ""},
		{"curl https://example.com", none, "ny??n", u, "host"},
		{"curl -sS -X GET https://example.com", none, "ny??n", u, "host"},
		{"curl -d @payload.json https://example.com", irr, "ny??n", u, "host"},
		{"curl --data-binary @f https://example.com", irr, "ny??n", u, "host"},
		{"curl -F file=@a https://example.com", irr, "ny??n", u, "host"},
		{"curl -T a.tar https://example.com", irr, "ny??n", u, "host"},
		{"curl --upload-file a https://example.com", irr, "ny??n", u, "host"},
		{"curl -X POST https://example.com", irr, "ny??n", u, "host"},
		{"curl -XDELETE https://example.com", irr, "ny??n", u, "host"},
		{"curl --request=PUT https://example.com", irr, "ny??n", u, "host"},
		{"curl --json '{}' https://example.com", irr, "ny??n", u, "host"},
		{"curl -sd x https://example.com", irr, "ny??n", u, "host"},
		{"wget https://example.com/a.tar", none, "ny??n", u, "host"},
		{"wget --post-data=x https://example.com", irr, "ny??n", u, "host"},
		{"wget --method DELETE https://example.com", irr, "ny??n", u, "host"},
		{"ssh build.example.com uptime", irr, "ny??n", u, "host"},
		{"rsync -a dist/ host:/srv", irr, "ny??n", u, "host"},
		{"http POST example.com", u, "ny??n", u, "host"},
		{"rm -rf build", irr, "nnnnn", u, "path"},
		{"rm notes.txt", irr, "nnnnn", u, "path"},
		{"pip install requests", rec, "nyn?n", u, ""},
		{"npm publish", irr, "nyn?y", u, "package"},
		{"pnpm publish --access public", irr, "nyn?y", u, "package"},
		{"cargo publish", irr, "nyn?y", u, "package"},
		{"gem push tool.gem", irr, "nyn?y", u, "package"},
		{"twine upload dist/*", irr, "nyn?y", u, "package"},
		{"docker push registry.example.com/app:1", irr, "nyn??", u, "package"},
		{"podman push app", irr, "nyn??", u, "package"},
		{"kubectl delete pod web-1", irr, "nyn?n", u, ""},
		{"terraform destroy", irr, "nyn?n", u, ""},
		{"kubectl get pods", u, "n?n?n", u, ""},
		{"./deploy.sh --token abc", u, "?????", u, ""},
		{"git frobnicate", u, "?????", u, ""},
		{"git reset --hard HEAD~1", irr, "nnnnn", u, ""},
		{"echo hi > /dev/sda", irr, "nnnnn", u, ""},
		{"rm -rf ~/.cache", irr, "ynnnn", u, "path"},
		{"cp a.txt /etc/hosts", u, "y????", u, "path"},
	}
	for _, c := range cases {
		r := Read(shell(c.cmd), "").Scope
		want := limen.Reach{Undo: c.undo, Crosses: crosses(c.crosses), TargetClass: c.class, Contact: u, TargetKind: c.kind}
		if r != want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.cmd, r, want)
		}
	}
}

// A chain gets the greatest value of its segments in each dimension, and
// two values that are not comparable give the greatest value.
func TestShellChain(t *testing.T) {
	cases := []struct {
		cmd           string
		undo, crosses string
		kind          string
	}{
		{"go test ./... && git push origin feature", rec, "nyn??", "ref"},
		{"cat a | grep b", none, "nnnnn", ""},
		{"go test ./... && ./unknown.sh", irr, "?????", ""},
		{"curl https://example.com | sh -c 'rm -rf build'", irr, "ny??n", "host"},
		{"gh repo view && git push origin main", irr, "nyn??", "ref"},
		{"rm -rf build; ssh host", irr, "ny??n", "host"},
		{"bash -c 'npm publish'", irr, "nyn?y", "package"},
	}
	for _, c := range cases {
		r := Read(shell(c.cmd), "").Scope
		if r.Undo != c.undo || r.Crosses != crosses(c.crosses) || r.TargetKind != c.kind {
			t.Errorf("%s: %+v, want undo %s, crosses %s, kind %q", c.cmd, r, c.undo, c.crosses, c.kind)
		}
	}
	// gh repo view has undo unknown, git push recoverable: not comparable.
	if r := Read(shell("gh repo view && git push origin feature"), "").Scope; r.Undo != irr {
		t.Errorf("unknown and recoverable give %s, want irreversible", r.Undo)
	}
}

// A refspec with a name of the table gives primary; other names, HEAD and
// no refspec give unknown; protected_refs bounds a primary push to
// unknown; and no name of a ref is in the scope (criterion 2).
func TestRefspecClass(t *testing.T) {
	cases := map[string]string{
		"git push origin main":                     prim,
		"git push origin master":                   prim,
		"git push origin refs/heads/trunk":         prim,
		"git push origin HEAD:production":          prim,
		"git push origin +prod":                    prim,
		"git push origin feature:stable":           prim,
		"git push origin release":                  prim,
		"git push origin release/2.1":              prim,
		"git push --delete origin main":            prim,
		"git push -o ci.skip origin main":          prim,
		"git push --repo=origin main":              u,
		"git push --all origin":                    prim,
		"git push --mirror":                        prim,
		"git push origin feature/login":            u,
		"git push origin HEAD":                     u,
		"git push origin":                          u,
		"git push":                                 u,
		"git push origin mainline":                 u,
		"git push origin v1.0 feature main":        prim,
		"git -C repo push origin MAIN":             prim,
		"git -c push.default=x push origin wip":    u,
		"git push origin HEAD:refs/heads/my-topic": u,
	}
	for cmd, want := range cases {
		if got := Read(shell(cmd), "").Scope.TargetClass; got != want {
			t.Errorf("%s: class %s, want %s", cmd, got, want)
		}
	}
	held := shell("git push origin main")
	held.Environment = &limen.Environment{Kind: "sandbox", Holds: []string{limen.HoldProtectedRefs}}
	if got := Read(held, "").Scope.TargetClass; got != u {
		t.Errorf("protected_refs: class %s, want unknown", got)
	}
	held.Target = &limen.Target{Kind: "ref", Class: prim}
	if got := Read(held, "").Scope.TargetClass; got != prim {
		t.Errorf("a claim of primary under protected_refs: %s", got)
	}
	merge := shell("gh pr merge 3")
	merge.Environment = held.Environment
	if got := Read(merge, "").Scope.TargetClass; got != prim {
		t.Errorf("protected_refs bounds gh pr merge: %s", got)
	}
	for _, cmd := range []string{"git push origin feature/secret-plan", "git push origin release/acme-merger:main"} {
		b, _ := json.Marshal(Read(shell(cmd), ""))
		for _, name := range []string{"secret", "plan", "acme", "merger", "release/", "main\""} {
			if strings.Contains(string(b), name) {
				t.Errorf("%s: the reading %s holds %q", cmd, b, name)
			}
		}
	}
}

// The visibility of a git push is that of the repositories.
func TestPushVisibility(t *testing.T) {
	for vis, want := range map[string]limen.Bound{
		limen.RepositoriesPublic: limen.BoundYes, limen.RepositoriesPrivate: limen.BoundNo,
		limen.RepositoriesMixed: limen.BoundUnknown, "": limen.BoundUnknown, "weird": limen.BoundUnknown,
	} {
		a := shell("git push origin feature")
		a.Environment = &limen.Environment{Kind: "host", Repositories: vis}
		if got := Read(a, "").Scope.Crosses.Public; got != want {
			t.Errorf("repositories %q: public %q, want %q", vis, got, want)
		}
	}
	a := shell("git push origin feature")
	a.Environment = &limen.Environment{Kind: "host", Repositories: limen.RepositoriesPublic}
	if l := Read(a, "").Label; l != "bash: git push, public" {
		t.Errorf("label %q", l)
	}
}

// The reading of an action that is not a shell action comes from its
// effect and reversible.
func TestFieldReading(t *testing.T) {
	cases := []struct {
		effect     limen.Effect
		reversible *bool
		undo       string
		remote     limen.Bound
	}{
		{limen.EffectNone, nil, none, ""},
		{limen.EffectRead, nil, none, ""},
		{limen.EffectWrite, nil, u, limen.BoundNo},
		{limen.EffectWrite, new(true), rev, limen.BoundNo},
		{limen.EffectExternal, new(false), irr, limen.BoundYes},
		{limen.EffectExternal, nil, u, limen.BoundYes},
		{limen.EffectRead, new(false), irr, ""},
	}
	for _, c := range cases {
		r := Read(limen.Action{Kind: "tool", Name: "publish", Effect: c.effect, Reversible: c.reversible}, "").Scope
		if r.Undo != c.undo || r.Crosses.Remote != c.remote {
			t.Errorf("effect %s, reversible %v: %+v", c.effect, c.reversible, r)
		}
		if c.remote == limen.BoundNo && (r.Crosses.Network != limen.BoundNo || r.Crosses.Public != limen.BoundNo || r.Crosses.Organization != limen.BoundNo) {
			t.Errorf("remote no leaves %+v", r.Crosses)
		}
	}
}

// A claim makes a dimension wider and never narrower; two values that are
// not comparable give the greatest value; and each hold bounds the reading
// as its table says (criterion 3).
func TestClaims(t *testing.T) {
	claim := func(a limen.Action, c limen.ReachClaim) limen.Action { a.Scope = &c; return a }
	pub := limen.Action{Kind: "tool", Name: "publish", Effect: limen.EffectExternal}
	cases := []struct {
		name string
		a    limen.Action
		want limen.Reach
	}{
		{"a claim gives what Limen does not read", claim(pub, limen.ReachClaim{Undo: rec, Contact: limen.ContactRepeat, Crosses: crosses("nnnnn")}),
			limen.Reach{Undo: rec, Crosses: crosses("nynnn"), TargetClass: u, Contact: limen.ContactRepeat}},
		{"a claim cannot narrow the reading", claim(shell("git push --force origin main"), limen.ReachClaim{Undo: none, Crosses: crosses("nnnnn")}),
			limen.Reach{Undo: irr, Crosses: crosses("nynn?"), TargetClass: prim, Contact: u, TargetKind: "ref"}},
		{"a claim widens the reading", claim(shell("go test ./..."), limen.ReachClaim{Crosses: crosses("nnnny")}),
			limen.Reach{Undo: rev, Crosses: crosses("nynny"), TargetClass: u, Contact: u}},
		{"not comparable gives the greatest", claim(shell("go test ./..."), limen.ReachClaim{Undo: u}),
			limen.Reach{Undo: irr, Crosses: crosses("nnnnn"), TargetClass: u, Contact: u}},
		{"a word outside the vocabulary is a claim of unknown", claim(shell("go test ./..."), limen.ReachClaim{Undo: "mostly", Contact: "sometimes"}),
			limen.Reach{Undo: irr, Crosses: crosses("nnnnn"), TargetClass: u, Contact: u}},
		{"an unknown reading and a claim give the greatest", claim(shell("./x.sh"), limen.ReachClaim{Undo: rev}),
			limen.Reach{Undo: irr, Crosses: crosses("?????"), TargetClass: u, Contact: u}},
		{"an organization claim makes remote yes", claim(limen.Action{Kind: "tool", Name: "share", Effect: limen.EffectWrite}, limen.ReachClaim{Crosses: crosses("???y?")}),
			limen.Reach{Undo: u, Crosses: crosses("?y?y?"), TargetClass: u, Contact: u}},
	}
	for _, c := range cases {
		if got := Read(c.a, "").Scope; got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	// For a shell action, reversible is a claim: it widens and never
	// narrows the reading of the table.
	push := shell("git push origin feature")
	push.Reversible = ptr(false)
	if r := Read(push, "").Scope; r.Undo != irr {
		t.Errorf("a push the decision point calls irreversible: %s", r.Undo)
	}
	push.Reversible = ptr(true)
	if r := Read(push, "").Scope; r.Undo != rec {
		t.Errorf("a push the decision point calls reversible: %s", r.Undo)
	}
	// The class claim, and a class outside the vocabulary.
	a := pub
	a.Target = &limen.Target{Kind: "app", Class: limen.ClassOwn}
	if r := Read(a, ""); r.Scope.TargetClass != limen.ClassOwn || r.Scope.TargetKind != "app" {
		t.Errorf("target class %+v", r.Scope)
	}
	a.Target.Class = "cosmic"
	if r := Read(a, ""); r.Scope.TargetClass != u {
		t.Errorf("a class outside the vocabulary: %s", r.Scope.TargetClass)
	}
	// Limen's contact and the claim: the greater.
	a.Scope = &limen.ReachClaim{Contact: limen.ContactRepeat}
	if r := Read(a, limen.ContactFirst); r.Scope.Contact != limen.ContactFirst {
		t.Errorf("contact %s", r.Scope.Contact)
	}
}

func TestHolds(t *testing.T) {
	env := func(holds ...string) *limen.Environment { return &limen.Environment{Kind: "sandbox", Holds: holds} }
	curl := shell("curl https://example.com")
	curl.Environment = env(limen.HoldEgress)
	if n := Read(curl, "").Scope.Crosses.Network; n != limen.BoundNo {
		t.Errorf("egress: network %q", n)
	}
	// A hold bounds the reading, not a claim.
	curl.Scope = &limen.ReachClaim{Crosses: limen.Crosses{Network: limen.BoundYes}}
	if n := Read(curl, "").Scope.Crosses.Network; n != limen.BoundYes {
		t.Errorf("egress bounds a claim: network %q", n)
	}
	rm := shell("rm -rf build")
	if r := Read(rm, "").Scope; r.Undo != irr {
		t.Fatalf("rm %+v", r)
	}
	rm.Environment = env(limen.HoldCheckpoints)
	if r := Read(rm, "").Scope; r.Undo != rec {
		t.Errorf("checkpoints: undo %s", r.Undo)
	}
	unknown := shell("make clean-all && go test")
	unknown.Environment = env(limen.HoldCheckpoints)
	if r := Read(unknown, "").Scope; r.Undo != rev {
		t.Errorf("checkpoints leave %s", r.Undo)
	}
	for _, cmd := range []string{"rm -rf ~/x", "git push --force origin main", "./x.sh"} {
		a := shell(cmd)
		a.Environment = env(limen.HoldCheckpoints)
		if r := Read(a, "").Scope; r.Undo == rec {
			t.Errorf("%s: checkpoints bound an action that crosses a boundary: %+v", cmd, r)
		}
	}
	// checkpoints bound an unknown undo where no boundary is crossed.
	w := limen.Action{Kind: "tool", Name: "write", Effect: limen.EffectWrite, Reversible: new(false), Environment: env(limen.HoldCheckpoints),
		Scope: &limen.ReachClaim{Crosses: crosses("nnnnn")}}
	if r := Read(w, "").Scope; r.Undo != rec {
		t.Errorf("write under checkpoints: %+v", r)
	}
	// A claim of irreversible stays.
	w.Scope.Undo = irr
	if r := Read(w, "").Scope; r.Undo != irr {
		t.Errorf("checkpoints bound a claim: %+v", r)
	}
}

// A value outside the vocabulary of a scope field is unknown, and it is
// not an error: each reading holds words of the vocabularies (criterion
// 4).
func FuzzScopeFields(f *testing.F) {
	f.Add("partial", "often", "galaxy", "moat", "secret", "kind", "ls", true, false)
	f.Add(limen.UndoNone, limen.ContactFirst, limen.ClassOwn, limen.HoldEgress, limen.RepositoriesPublic, "app", "git push origin main", false, true)
	f.Fuzz(func(t *testing.T, undo, contact, class, hold, repos, kind, cmd string, pub, shellAction bool) {
		a := limen.Action{Kind: "tool", Name: "publish", Effect: limen.EffectExternal}
		if shellAction {
			a = shell(cmd)
		}
		a.Scope = &limen.ReachClaim{Undo: undo, Contact: contact, Crosses: limen.Crosses{Public: map[bool]limen.Bound{true: limen.BoundYes, false: limen.Bound(cmd)}[pub]}}
		a.Target = &limen.Target{Kind: kind, Class: class}
		a.Environment = &limen.Environment{Kind: "host", Holds: []string{hold}, Repositories: repos}
		if err := a.Validate(); err != nil && a.Org != "" {
			t.Fatal(err)
		}
		r := Read(a, "").Scope
		for v, o := range map[string]limen.Order{r.Undo: limen.Undos(), r.TargetClass: limen.TargetClasses(), r.Contact: limen.Contacts()} {
			if v != limen.Unknown && !slices.Contains(o, v) {
				t.Fatalf("word %q outside %v", v, o)
			}
		}
		for _, b := range []limen.Bound{r.Crosses.Machine, r.Crosses.Remote, r.Crosses.Network, r.Crosses.Organization, r.Crosses.Public} {
			if b != limen.BoundNo && b != limen.BoundYes && b != limen.BoundUnknown {
				t.Fatalf("bound %q", b)
			}
		}
	})
}

// labels/2 gives each label of the example table of spec 011, and two
// actions that differ only in scope have different labels (criterion 7).
func TestLabels2(t *testing.T) {
	pub := func(c limen.ReachClaim, target *limen.Target) limen.Action {
		return limen.Action{Kind: "tool", Name: "publish", Effect: limen.EffectExternal, Scope: &c, Target: target}
	}
	app := func(class string) *limen.Target { return &limen.Target{Kind: "app", ID: "app_1", Class: class} }
	cases := []struct {
		a    limen.Action
		want string
	}{
		{pub(limen.ReachClaim{Undo: irr, Crosses: crosses("?y??y"), Contact: limen.ContactRepeat}, app(limen.ClassOwn)), "publish: make public"},
		{pub(limen.ReachClaim{Undo: rec, Crosses: crosses("?y??n"), Contact: limen.ContactRepeat}, app(limen.ClassOwn)), "publish: update"},
		{pub(limen.ReachClaim{Undo: rec, Crosses: crosses("?y??n"), Contact: limen.ContactRepeat}, app(limen.ClassPrimary)), "publish: production"},
		{pub(limen.ReachClaim{Undo: rec, Crosses: crosses("?y??n"), Contact: limen.ContactFirst}, app(limen.ClassOwn)), "publish: first time"},
		{pub(limen.ReachClaim{Undo: rec, Crosses: crosses("?y??y")}, nil), "publish: public"},
		{pub(limen.ReachClaim{Crosses: crosses("?yy??")}, nil), "publish: outside the session network"},
		{pub(limen.ReachClaim{Crosses: crosses("?y?y?")}, nil), "publish: outside the organization"},
		{pub(limen.ReachClaim{Undo: irr}, &limen.Target{Kind: "account", Class: prim}), "publish: protected target, cannot undo"},
		{pub(limen.ReachClaim{Undo: irr, Crosses: crosses("?yyyy")}, app(prim)), "publish: make public, outside the organization"},
		{limen.Action{Kind: "tool", Name: "web_fetch", Effect: limen.EffectRead}, "web_fetch: read"},
		{limen.Action{Kind: "egress", Name: "https", Effect: limen.EffectExternal}, "egress https: update"},
		{shell("git push --force origin main"), "bash: git push --force, default branch, cannot undo"},
		{shell("rm -rf ~/.cache"), "bash: rm -r, outside the workspace, cannot undo"},
		{shell("go test ./..."), "bash: go test"},
		{shell("npm publish"), "bash: npm, make public"},
		{shell("./x.sh"), "bash: other command"},
	}
	for _, c := range cases {
		if got := Read(c.a, "").Label; got != c.want {
			t.Errorf("%s %s: label %q, want %q", c.a.Name, c.a.Input, got, c.want)
		}
	}
	a, b := cases[0].a, cases[1].a
	if Read(a, "").Label == Read(b, "").Label {
		t.Error("two actions that differ in scope have one label")
	}
	if got := Read(shell("go test ./..."), "").Label; got != Label(shell("go test ./...")) {
		t.Errorf("a shell label with no phrase is its labels/1 label: %q", got)
	}
}

func TestFamilies(t *testing.T) {
	cases := []struct {
		a    limen.Action
		want []string
	}{
		{shell("go test ./... && git push origin main"), []string{"go test", "git push"}},
		{shell("make && go test && git push && ./x.sh"), []string{"make", "go test", "git push", OtherCommand}},
		{shell("git push --force"), []string{"git push --force"}},
		{shell(""), []string{OtherCommand}},
		{limen.Action{Kind: "tool", Name: "bash"}, []string{OtherCommand}},
		{limen.Action{Kind: "tool", Name: "Publish"}, []string{"publish"}},
		{limen.Action{Kind: "egress", Name: "https"}, []string{"egress https"}},
	}
	for _, c := range cases {
		if got := Read(c.a, "").Families; !slices.Equal(got, c.want) {
			t.Errorf("%s %s: families %v, want %v", c.a.Name, c.a.Input, got, c.want)
		}
	}
}

func TestContact(t *testing.T) {
	app := &limen.Target{Kind: "app", ID: "app_1"}
	if c := Contact(app, nil); c != limen.ContactFirst {
		t.Errorf("no approval: %s", c)
	}
	if c := Contact(app, []*limen.Target{nil, {Kind: "App", ID: "app_1"}}); c != limen.ContactRepeat {
		t.Errorf("an approval of the target: %s", c)
	}
	if c := Contact(app, []*limen.Target{{Kind: "host", ID: "app_1"}, {Kind: "app", ID: "app_2"}}); c != limen.ContactFirst {
		t.Errorf("other targets: %s", c)
	}
	if Contact(nil, nil) != "" || Contact(&limen.Target{Kind: "host"}, nil) != "" {
		t.Error("a target with no id has a contact")
	}
}

// An old case gets the scope of the migration table, and its families from
// its label (criterion 20).
func TestMigrated(t *testing.T) {
	old := func(cmd string) (limen.Reach, []string) {
		a := shell(cmd)
		f := Of(a)
		label := Label(a)
		a.Input = nil
		return Migrated(a, Version, f.A, f.C, label)
	}
	cases := []struct {
		cmd           string
		undo, crosses string
		fams          []string
	}{
		{"go test ./...", rev, "nnnnn", []string{"go test"}},
		{"cat a.txt", none, "nnnnn", []string{"cat"}},
		{"git push --force origin main", irr, "ny???", []string{"git push --force"}},
		{"git push origin main", u, "ny???", []string{"git push"}},
		{"rm -rf ~/.cache", irr, "ynnnn", []string{"rm -r"}},
		{"rm notes", irr, "nnnnn", []string{"rm"}},
		{"pip install x", rec, "ny???", []string{"pip install"}},
		{"git restore a.go", irr, "nnnnn", []string{"git restore"}},
		{"git checkout a.go", u, "nnnnn", []string{"git checkout"}},
		{"./x.sh", u, "?????", []string{OtherCommand}},
		{"make && go test && go vet", u, "?????", []string{"make", "go test"}},
		{"git reset --hard", irr, "nnnnn", []string{"git reset"}},
	}
	for _, c := range cases {
		r, fams := old(c.cmd)
		want := limen.Reach{Undo: c.undo, Crosses: crosses(c.crosses), TargetClass: u, Contact: u}
		if r != want || !slices.Equal(fams, c.fams) {
			t.Errorf("%s:\n got %+v %v\nwant %+v %v", c.cmd, r, fams, want, c.fams)
		}
	}
	pub := limen.Action{Kind: "tool", Name: "publish", Effect: limen.EffectExternal, Reversible: new(true)}
	if r, fams := Migrated(pub, Version, nil, nil, "publish"); r != (limen.Reach{Undo: rev, Crosses: crosses("?y???"), TargetClass: u, Contact: u}) || !slices.Equal(fams, []string{"publish"}) {
		t.Errorf("publish: %+v %v", r, fams)
	}
	// Vectors of another featurizer tell nothing.
	if r, fams := Migrated(shell("go test"), "features/0", nil, nil, "bash: go test"); r.Undo != u || !slices.Equal(fams, []string{"go test"}) {
		t.Errorf("another featurizer: %+v %v", r, fams)
	}
	if _, fams := Migrated(shell(""), Version, nil, nil, "bash"); !slices.Equal(fams, []string{OtherCommand}) {
		t.Errorf("a label with no segment: %v", fams)
	}
	// An old approval of unknown reach never covers a new action of known
	// greatest reach.
	unknownOld, _ := old("./x.sh")
	top := Read(shell("git push --force origin main"), "").Scope
	if top.Within(unknownOld) {
		t.Error("an unknown old approval covers a force push to the default branch")
	}
}

// Read never fails and gives each reading a word in each dimension, for
// random actions.
func TestReadRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 11))
	for range 2000 {
		a := randomAction(rng)
		r := Read(a, "")
		if r.Label == "" || len(r.Families) == 0 || r.Scope.Undo == "" || r.Scope.TargetClass == "" || r.Scope.Contact == "" {
			t.Fatalf("%+v: %+v", a, r)
		}
	}
}
