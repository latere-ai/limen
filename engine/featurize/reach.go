// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"cmp"
	"slices"
	"strings"

	"latere.ai/x/limen"
)

// The reach of an action, its scope (spec 011): Limen's reading of the
// input and the fields of the action, the holds of the environment that
// bound that reading, and the claims of the decision point merged with it.
// Only words of the vocabularies go into a scope: the refspec, the URL and
// each path are dropped with the input, and no hash of them is kept.

var (
	undos    = limen.Undos()
	classes  = limen.TargetClasses()
	contacts = limen.Contacts()
	bounds   = limen.Bounds()
)

// The words of a boundary.
const (
	no  = "no"
	yes = "yes"
)

// targetKinds is the table of the kinds of the target of a shell action,
// in the order of its rows: the first row a segment matches names it.
var targetKinds = [...]string{"ref", "repository", "package", "host", "path"}

const (
	kindRef = iota
	kindRepository
	kindPackage
	kindHost
	kindPath
)

// segmentReach is one segment's row of the shell table.
type segmentReach struct {
	undo                           string
	machine, remote, network, publ string
	class                          string
	push                           bool
	pushClass                      string
	kind                           int
}

// unnamed is the row of a segment that no table names: each dimension
// unknown.
func unnamed() segmentReach {
	return segmentReach{undo: limen.Unknown, machine: limen.Unknown, remote: limen.Unknown, network: limen.Unknown,
		publ: limen.Unknown, kind: len(targetKinds)}
}

// named is a row of the table: the boundaries it leaves blank are no.
func named(undo string) segmentReach {
	return segmentReach{undo: undo, machine: no, remote: no, network: no, publ: no, kind: len(targetKinds)}
}

var (
	networkHosts = set("ssh", "scp", "rsync", "nc", "ncat", "telnet", "ftp", "sftp")
	publishers   = map[string]string{"npm": "publish", "pnpm": "publish", "yarn": "publish", "cargo": "publish", "gem": "push", "twine": "upload"}
)

// reach joins the row of one segment into the reading.
func (r *reading) reach(prog string, args []string, cls [numClasses]bool, destructive bool) {
	row := rowOf(prog, args, cls, destructive)
	if destructive {
		row.undo = undos.Join(row.undo, limen.UndoIrreversible)
	}
	r.undo = undos.Join(r.undo, row.undo)
	r.machine = bounds.Join(r.machine, row.machine)
	r.remote = bounds.Join(r.remote, row.remote)
	r.network = bounds.Join(r.network, row.network)
	r.public = bounds.Join(r.public, row.publ)
	r.class = classes.Join(r.class, row.class)
	if row.push {
		r.pushes = true
		r.pushClass = classes.Join(r.pushClass, row.pushClass)
	}
	r.kind = min(r.kind, row.kind)
}

// rowOf is the row of the shell table of spec 011 for one segment. The
// specific rows come before the rows of a class, and a segment of more
// than one class gets the greatest row of its classes.
func rowOf(prog string, args []string, cls [numClasses]bool, destructive bool) segmentReach {
	s := sub(prog, args)
	switch {
	case prog == "git":
		return gitRow(s, args, destructive)
	case prog == "gh":
		return ghRow(args)
	case prog == "curl" || prog == "wget":
		row := named(limen.UndoNone)
		if sendsData(prog, args) {
			row.undo = limen.UndoIrreversible
		}
		row.remote, row.network, row.kind = yes, limen.Unknown, kindHost
		return row
	case networkHosts[prog]:
		row := named(limen.UndoIrreversible)
		row.remote, row.network, row.kind = yes, limen.Unknown, kindHost
		return row
	case cls[clsNetwork]:
		row := named(limen.Unknown)
		row.remote, row.network, row.kind = yes, limen.Unknown, kindHost
		return row
	case publishers[prog] != "" && s == publishers[prog]:
		row := named(limen.UndoIrreversible)
		row.remote, row.publ, row.kind = yes, yes, kindPackage
		return row
	case (prog == "docker" || prog == "podman") && s == "push":
		row := named(limen.UndoIrreversible)
		row.remote, row.publ, row.kind = yes, limen.Unknown, kindPackage
		return row
	case cls[clsInfra]:
		if destructive {
			row := named(limen.UndoIrreversible)
			row.remote = yes
			return row
		}
		row := named(limen.Unknown)
		row.remote = limen.Unknown
		return row
	}
	row, any := named(""), false
	for c, on := range cls {
		if !on {
			continue
		}
		any = true
		switch c {
		case clsDelete:
			row.undo = undos.Join(row.undo, limen.UndoIrreversible)
			row.kind = min(row.kind, kindPath)
		case clsPackage:
			row.undo = undos.Join(row.undo, limen.UndoRecoverable)
			row.remote = yes
		case clsRead:
			row.undo = undos.Join(row.undo, limen.UndoNone)
		case clsBuildTest, clsVCSLocal:
			row.undo = undos.Join(row.undo, limen.UndoReversible)
		}
	}
	if !any {
		return unnamed()
	}
	return row
}

// gitRow is the row of a git segment with the subcommand s.
func gitRow(s string, args []string, destructive bool) segmentReach {
	switch {
	case s == "restore" || (s == "checkout" && contains(args, "--")) || (s == "stash" && (contains(args, "drop") || contains(args, "clear"))):
		return named(limen.UndoIrreversible)
	case s == "pull" || s == "fetch" || s == "clone":
		row := named(limen.UndoReversible)
		row.remote = yes
		return row
	case s == "push":
		row := named(limen.UndoRecoverable)
		if destructive {
			row.undo = limen.UndoIrreversible
		}
		row.remote, row.push, row.pushClass, row.kind = yes, true, refspecClass(args), kindRef
		// The visibility of a push is that of the repositories, which
		// the environment names.
		row.publ = ""
		return row
	case s == "branch":
		row := named(limen.UndoReversible)
		row.kind = kindRef
		return row
	case gitRead[s]:
		return named(limen.UndoNone)
	case gitLocal[s]:
		return named(limen.UndoReversible)
	case s == "clean":
		row := named(limen.UndoIrreversible)
		row.kind = kindPath
		return row
	}
	return unnamed()
}

// ghRow is the row of a gh segment: its first two words name the
// command.
func ghRow(args []string) segmentReach {
	w := ghWords(args)
	at := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	cmd := at(0) + " " + at(1)
	row := named(limen.Unknown)
	row.remote = yes
	switch {
	case cmd == "pr merge":
		row.undo, row.class, row.kind = limen.UndoRecoverable, limen.ClassPrimary, kindRef
	case cmd == "repo create" && anyArg(args, func(a string) bool { return a == "--public" }),
		cmd == "repo edit" && visibilityPublic(args):
		row.undo, row.publ, row.kind = limen.UndoIrreversible, yes, kindRepository
	case cmd == "repo delete", cmd == "release delete":
		row.undo = limen.UndoIrreversible
		if at(0) == "repo" {
			row.kind = kindRepository
		}
	case at(0) == "repo":
		row.kind = kindRepository
	}
	return row
}

// ghWords are the words of a gh command that are not options, with the
// value of its global option -R or --repo skipped.
func ghWords(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			out = append(out, strings.ToLower(a))
		}
	}
	return out
}

// visibilityPublic reports a gh repo edit that makes the repository
// public.
func visibilityPublic(args []string) bool {
	for i, a := range args {
		if a == "--visibility=public" || (a == "--visibility" && i+1 < len(args) && strings.EqualFold(args[i+1], "public")) {
			return true
		}
	}
	return false
}

// sendsData reports a curl or wget that sends data: -d, --data and its
// forms, -F, --form, -T, --upload-file, --json, or a method other than GET
// or HEAD; for wget also --post-data, --post-file, --body-data,
// --body-file and --method.
func sendsData(prog string, args []string) bool {
	for i, a := range args {
		switch {
		case a == "-d" || strings.HasPrefix(a, "--data") || a == "-F" || strings.HasPrefix(a, "--form") ||
			a == "-T" || a == "--upload-file" || a == "--json" || strings.HasPrefix(a, "--json="):
			return true
		case prog == "wget" && (strings.HasPrefix(a, "--post-") || strings.HasPrefix(a, "--body-")):
			return true
		case a == "-X" || a == "--request" || (prog == "wget" && a == "--method"):
			if i+1 < len(args) && !safeMethod(args[i+1]) {
				return true
			}
		case strings.HasPrefix(a, "--request=") || strings.HasPrefix(a, "--method="):
			_, m, _ := strings.Cut(a, "=")
			if !safeMethod(m) {
				return true
			}
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			if !safeMethod(a[2:]) {
				return true
			}
		case shortFlag(a, "dFT"):
			return true
		}
	}
	return false
}

func safeMethod(m string) bool { return strings.EqualFold(m, "GET") || strings.EqualFold(m, "HEAD") }

func contains(args []string, w string) bool {
	return anyArg(args, func(a string) bool { return a == w })
}

// pushValueOptions are the options of git push that take the next word as
// their value.
var pushValueOptions = set("--repo", "-o", "--push-option", "--receive-pack", "--exec")

// primaryRefs are the names of destinations that are the default branch,
// production or a release.
var primaryRefs = set("main", "master", "trunk", "production", "prod", "stable", "release")

// refspecClass is the class of the destinations of a git push: primary if
// one destination is named in primaryRefs or starts with release/, or the
// push sends each branch (--all, --mirror, --branches); unknown otherwise,
// with no refspec, or with HEAD. The first word that is not an option is
// the repository.
func refspecClass(args []string) string {
	var words []string
	seenPush := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !seenPush:
			if strings.EqualFold(a, "push") {
				seenPush = true
			}
			if a == "-C" || a == "-c" {
				i++
			}
		case a == "--all" || a == "--mirror" || a == "--branches":
			return limen.ClassPrimary
		case pushValueOptions[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			words = append(words, a)
		}
	}
	class := limen.Unknown
	if len(words) < 2 {
		return class
	}
	for _, spec := range words[1:] {
		if primaryRef(spec) {
			class = limen.ClassPrimary
		}
	}
	return class
}

// primaryRef reports whether the destination of one refspec is primary.
func primaryRef(spec string) bool {
	spec = strings.TrimPrefix(spec, "+")
	dst := spec
	if _, after, ok := strings.Cut(spec, ":"); ok {
		dst = after
	}
	dst = strings.ToLower(strings.TrimPrefix(dst, "refs/heads/"))
	return primaryRefs[dst] || strings.HasPrefix(dst, "release/")
}

// Reading is what the featurizer reads from an action for consent
// (spec 011): its scope, its families, and its label of labels/2.
type Reading struct {
	Scope    limen.Reach
	Families []string
	Label    string
}

// OtherCommand is the family of a shell segment that no table names.
const OtherCommand = "other command"

// Read reads the scope, the families and the label of an action. contact
// is Limen's own reading of contact from the person's approved cases
// ([Contact]): limen.ContactFirst, limen.ContactRepeat, or empty where it
// cannot read it. Read never fails: each action, an empty one included,
// has a defined reading.
func Read(a limen.Action, contact string) Reading {
	// The reading of Limen, in words; an empty word is a dimension it does
	// not read.
	var undo, machine, remote, network, organization, public, class, kind string
	shell := isShell(a)
	var sh Shell
	if shell {
		cmd, ok := command(a.Input)
		if ok {
			sh = ReadShell(cmd)
		}
		if len(sh.Programs) == 0 {
			// No segment: what a segment no table names reads.
			sh.Undo, sh.Machine, sh.Remote, sh.Network, sh.Public, sh.Unlabeled = limen.Unknown, limen.Unknown, limen.Unknown, limen.Unknown, limen.Unknown, true
		}
		undo, machine, remote, network, public, class, kind = sh.Undo, sh.Machine, sh.Remote, sh.Network, sh.Public, sh.TargetClass, sh.TargetKind
		if sh.Pushes {
			public = bounds.Join(public, pushVisibility(a.Environment))
			pushClass := sh.PushClass
			if pushClass == limen.ClassPrimary && a.Environment.Held(limen.HoldProtectedRefs) {
				pushClass = limen.Unknown
			}
			class = classes.Join(class, pushClass)
		}
	} else {
		switch a.Effect {
		case limen.EffectNone, limen.EffectRead:
			undo = limen.UndoNone
		case limen.EffectExternal:
			remote = yes
		case limen.EffectWrite:
			remote = no
		}
		if a.Reversible != nil {
			undo = undos.Join(undo, map[bool]string{true: limen.UndoReversible, false: limen.UndoIrreversible}[*a.Reversible])
		}
	}
	if network != "" && a.Environment.Held(limen.HoldEgress) {
		network = no
	}

	// The claims, merged with the bounded reading.
	var claim limen.ReachClaim
	if a.Scope != nil {
		claim = *a.Scope
	}
	machine = bounds.Join(machine, claimed(claim.Crosses.Machine))
	remote = bounds.Join(remote, claimed(claim.Crosses.Remote))
	network = bounds.Join(network, claimed(claim.Crosses.Network))
	organization = bounds.Join(organization, claimed(claim.Crosses.Organization))
	public = bounds.Join(public, claimed(claim.Crosses.Public))
	// Network, organization and public each reach another system, so one
	// of them makes remote yes, and remote no makes each of them no.
	if network == yes || organization == yes || public == yes {
		remote = yes
	}
	if remote == no {
		network, organization, public = no, no, no
	}
	if undo != "" && a.Environment.Held(limen.HoldCheckpoints) &&
		machine == no && remote == no && !undos.Within(undo, limen.UndoRecoverable) {
		undo = limen.UndoRecoverable
	}
	// For a shell action, the table reads the command, and reversible is
	// a claim of the decision point, as scope.undo is.
	claimUndo := claim.Undo
	if shell && a.Reversible != nil {
		claimUndo = undos.Join(claimUndo, map[bool]string{true: limen.UndoReversible, false: limen.UndoIrreversible}[*a.Reversible])
	}
	undo = undos.Join(undo, claimUndo)
	if a.Target != nil {
		class = classes.Join(class, a.Target.Class)
	}
	contact = contacts.Join(contact, claim.Contact)
	if a.Target != nil && strings.TrimSpace(a.Target.Kind) != "" {
		kind = cut(strings.ToLower(strings.TrimSpace(a.Target.Kind)), maxLabelName)
	}

	r := limen.Reach{
		Undo: word(undo),
		Crosses: limen.Crosses{Machine: bound(machine), Remote: bound(remote), Network: bound(network),
			Organization: bound(organization), Public: bound(public)},
		TargetClass: word(class),
		Contact:     word(contact),
		TargetKind:  kind,
	}
	base := Label(a)
	out := Reading{Scope: r, Label: phrased(base, shell, r)}
	if shell {
		out.Families = append(out.Families, sh.Labels...)
		if sh.Unlabeled {
			out.Families = append(out.Families, OtherCommand)
		}
	} else {
		out.Families = []string{base}
	}
	return out
}

// pushVisibility is the visibility of a git push: yes for public
// repositories, no for private ones, unknown otherwise.
func pushVisibility(e *limen.Environment) string {
	if e != nil {
		switch e.Repositories {
		case limen.RepositoriesPublic:
			return yes
		case limen.RepositoriesPrivate:
			return no
		}
	}
	return limen.Unknown
}

// claimed is the word of a claimed bound: empty for no claim.
func claimed(b limen.Bound) string {
	switch b {
	case limen.BoundYes:
		return yes
	case limen.BoundNo:
		return no
	}
	return ""
}

// word is w, or unknown for a dimension nobody gave.
func word(w string) string { return cmp.Or(w, limen.Unknown) }

// bound is the bound of a word: unknown unless yes or no.
func bound(w string) limen.Bound {
	switch w {
	case yes:
		return limen.BoundYes
	case no:
		return limen.BoundNo
	}
	return limen.BoundUnknown
}

// Contact is Limen's reading of contact for an action with the target t:
// limen.ContactFirst if no target of approved, the targets of the
// person's cases whose latest answer approves, has the kind and the id of
// t, limen.ContactRepeat if one has, and empty if t has no id.
func Contact(t *limen.Target, approved []*limen.Target) string {
	if t == nil || t.ID == "" {
		return ""
	}
	for _, o := range approved {
		if o != nil && o.ID == t.ID && strings.EqualFold(o.Kind, t.Kind) {
			return limen.ContactRepeat
		}
	}
	return limen.ContactFirst
}

// phrased is the label of labels/2: the label of labels/1 and a phrase
// from the scope that tells the person the reach of the action (spec 011).
// A shell label adds the phrase only from the rows of the table, after its
// segments.
func phrased(base string, shell bool, r limen.Reach) string {
	var ps []string
	add := func(p string) {
		if len(ps) < 2 {
			ps = append(ps, p)
		}
	}
	makePublic := r.Crosses.Public == limen.BoundYes && r.Undo == limen.UndoIrreversible
	if makePublic {
		add("make public")
	} else if r.Crosses.Public == limen.BoundYes {
		add("public")
	}
	if r.Crosses.Organization == limen.BoundYes {
		add("outside the organization")
	}
	if r.Crosses.Network == limen.BoundYes {
		add("outside the session network")
	}
	if r.TargetClass == limen.ClassPrimary {
		switch r.TargetKind {
		case "ref":
			add("default branch")
		case "app", "deploy", "service":
			add("production")
		default:
			add("protected target")
		}
	}
	if r.Crosses.Machine == limen.BoundYes {
		add("outside the workspace")
	}
	if r.Undo == limen.UndoIrreversible && !makePublic {
		add("cannot undo")
	}
	if r.Contact == limen.ContactFirst {
		add("first time")
	}
	switch {
	case shell && len(ps) == 0:
		return base
	case shell:
		return base + ", " + strings.Join(ps, ", ")
	case len(ps) == 0 && r.Undo == limen.UndoNone:
		return base + ": read"
	case len(ps) == 0:
		return base + ": update"
	}
	return base + ": " + strings.Join(ps, ", ")
}

// Migrated is the scope and the families of a case from before labels/2,
// read from its records (spec 011, migration): its action without the
// input, the featurizer and the vectors of features/1, and its label of
// labels/1. A dimension the records cannot tell is unknown, so an old
// approval covers only new actions of unknown or least reach there.
func Migrated(a limen.Action, featurizer string, A, C []float64, label string) (limen.Reach, []string) {
	r := limen.Reach{Undo: limen.Unknown, TargetClass: limen.Unknown, Contact: limen.Unknown}
	if !isShell(a) {
		rd := Read(a, "")
		r.Undo, r.Crosses.Remote = rd.Scope.Undo, rd.Scope.Crosses.Remote
		return r, []string{label}
	}
	words := segmentWords(label)
	fams := words
	if len(fams) == 0 {
		fams = []string{OtherCommand}
	}
	if featurizer != Version || len(A) != DimA || len(C) != DimC {
		return r, fams
	}
	var on [numClasses]bool
	anyClass := false
	for i := range numClasses {
		on[i] = A[aClasses+i] != 0
		anyClass = anyClass || on[i]
	}
	// The undo the records tell, and whether a segment needs the input to
	// tell it: then a middle value is unknown, because the old case could
	// have been any of the values the input decides.
	undo, needsInput := "", !anyClass || strings.HasSuffix(label, "…") || slices.Contains(words, OtherCommand)
	for c, set := range on {
		if !set {
			continue
		}
		switch c {
		case clsRead:
			undo = undos.Join(undo, limen.UndoNone)
		case clsBuildTest, clsVCSLocal:
			undo = undos.Join(undo, limen.UndoReversible)
		case clsPackage:
			undo = undos.Join(undo, limen.UndoRecoverable)
		case clsDelete:
			undo = undos.Join(undo, limen.UndoIrreversible)
		default:
			needsInput = true
		}
	}
	for _, w := range words {
		switch w {
		case "git push --force", "rm -r", "git restore":
			undo = undos.Join(undo, limen.UndoIrreversible)
		case "git checkout", "git stash":
			needsInput = true
		}
	}
	if C[cDestructive] != 0 {
		undo = undos.Join(undo, limen.UndoIrreversible)
	}
	if needsInput && undo != limen.UndoIrreversible {
		undo = limen.Unknown
	}
	r.Undo = word(undo)
	// named is true when the label names every segment: none is cut off
	// and none is a segment no table names, whose boundaries are unknown.
	named := anyClass && !strings.HasSuffix(label, "…") && !slices.Contains(words, OtherCommand)
	switch {
	case on[clsVCSRemote] || on[clsNetwork] || on[clsInfra] || on[clsPackage]:
		r.Crosses.Remote = limen.BoundYes
	case named:
		// Read, vcs_local, build_test and delete reach no other system, so
		// none of the boundaries beyond it.
		r.Crosses.Remote, r.Crosses.Network, r.Crosses.Organization, r.Crosses.Public = limen.BoundNo, limen.BoundNo, limen.BoundNo, limen.BoundNo
	}
	switch {
	case C[cOutside] != 0:
		r.Crosses.Machine = limen.BoundYes
	case named:
		r.Crosses.Machine = limen.BoundNo
	}
	return r, fams
}

// segmentWords are the words of the segments of a shell label of
// labels/1, "bash: git push, go test", without the ellipsis of a label cut
// at two segments.
func segmentWords(label string) []string {
	_, list, ok := strings.Cut(label, ": ")
	if !ok {
		return nil
	}
	var out []string
	for w := range strings.SplitSeq(list, ", ") {
		if w = strings.TrimSpace(w); w != "" && w != "…" {
			out = append(out, w)
		}
	}
	return out
}
