// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// The program classes of features/1, in the order of their dimensions.
var classNames = [...]string{"read", "vcs_local", "vcs_remote", "network", "delete", "package", "build_test", "infra"}

const (
	clsRead = iota
	clsVCSLocal
	clsVCSRemote
	clsNetwork
	clsDelete
	clsPackage
	clsBuildTest
	clsInfra
	numClasses
)

// Shell is the shell extractor's reading of a command: the base name of
// each segment's program, in order, the program classes found (in the
// order of features/1's dimensions, each once), whether any segment is
// destructive or writes outside the workspace, and the labels of the
// segments whose program the tables below name, in order, each once.
// Programs is the command's own text and is never kept; Labels holds words
// of the tables alone (spec 009).
type Shell struct {
	Programs         []string
	Classes          []string
	Destructive      bool
	OutsideWorkspace bool
	Labels           []string
}

// ReadShell reads a command statically, before it runs. It is a worst-case
// reading, not a parser: a chain is as dangerous as its most dangerous
// segment, and a program it does not recognize contributes its name and no
// class.
func ReadShell(command string) Shell {
	var r reading
	r.read(command, 0)
	out := Shell{Programs: r.programs, Destructive: r.destructive, OutsideWorkspace: r.outside, Labels: r.labels}
	for i, on := range r.classes {
		if on {
			out.Classes = append(out.Classes, classNames[i])
		}
	}
	return out
}

// maxDepth bounds how deep a command nested in an interpreter's -c, eval,
// or find -exec is read, so a pathological input cannot recurse without
// end.
const maxDepth = 3

type reading struct {
	programs    []string
	classes     [numClasses]bool
	destructive bool
	outside     bool
	labels      []string
}

func (r *reading) read(command string, depth int) {
	for _, seg := range segments(command) {
		r.segment(seg, depth)
	}
}

// segments splits a command on the spec's separators (&&, ||, ;, |, a
// newline) and, reading conservatively, on command substitution ($( and a
// backtick) and on a background &, so a command hidden in any of them is
// read as a segment of its own. An & inside a redirection (2>&1, &>) does
// not split.
func segments(s string) []string {
	var out []string
	start := 0
	flush := func(end int) {
		if seg := strings.TrimSpace(s[start:end]); seg != "" {
			out = append(out, seg)
		}
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\n' || c == ';' || c == '`':
			flush(i)
			start = i + 1
		case c == '|':
			flush(i)
			if i+1 < len(s) && (s[i+1] == '|' || s[i+1] == '&') {
				i++
			}
			start = i + 1
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				flush(i)
				i++
				start = i + 1
				continue
			}
			if (i > 0 && (s[i-1] == '>' || s[i-1] == '<')) || (i+1 < len(s) && s[i+1] == '>') {
				continue
			}
			flush(i)
			start = i + 1
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			flush(i)
			i++
			start = i + 1
		}
	}
	flush(len(s))
	return out
}

// unquote strips the quotes, parentheses and braces a word carries at its
// ends, which word splitting without a parser leaves behind. An option
// keeps its braces, so xargs -I{} keeps its replacement string attached.
func unquote(w string) string {
	if strings.HasPrefix(strings.TrimLeft(w, "\"'"), "-") {
		return strings.Trim(w, "\"'")
	}
	return strings.Trim(w, "\"'(){}")
}

// redirection reports whether a word is a redirection operator, with or
// without its target attached: write is true for > and >>, false for <;
// target is what follows the operator in the same word, empty when the
// target is the next word. A duplication such as 2>&1 has no file target.
func redirection(w string) (write bool, target string, isRedir, dup bool) {
	rest := strings.TrimLeft(w, "0123456789")
	rest = strings.TrimPrefix(rest, "&")
	switch {
	case strings.HasPrefix(rest, ">"):
		rest = strings.TrimLeft(rest, ">")
		write = true
	case strings.HasPrefix(rest, "<"):
		rest = strings.TrimLeft(rest, "<")
	default:
		return false, "", false, false
	}
	if strings.HasPrefix(rest, "&") {
		return write, "", true, true
	}
	return write, unquote(rest), true, false
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// wrappers run the command that follows their own options. The spec names
// sudo, env, time, nohup and command; doas, exec, nice, stdbuf, timeout and
// xargs are read the same way, conservatively, because each runs its
// arguments as a command. The value lists the options that take the next
// word as their value.
var wrappers = map[string][]string{
	"sudo":    {"-u", "-g", "-h", "-p", "-C", "-D", "-r", "-t", "-U", "-T"},
	"env":     {"-u", "-C", "-S"},
	"time":    {"-f", "-o"},
	"nohup":   nil,
	"command": nil,
	"doas":    {"-u", "-C"},
	"exec":    {"-a"},
	"nice":    {"-n"},
	"stdbuf":  {"-i", "-o", "-e"},
	"timeout": {"-s", "-k"},
	"xargs":   {"-I", "-n", "-P", "-L", "-d", "-E", "-s", "-a"},
}

// interpreters run the script their -c option names.
var interpreters = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

func (r *reading) segment(seg string, depth int) {
	var args, writes []string
	words := strings.Fields(seg)
	for i := 0; i < len(words); i++ {
		write, target, isRedir, dup := redirection(words[i])
		if !isRedir {
			args = append(args, unquote(words[i]))
			continue
		}
		if dup {
			continue
		}
		if target == "" && i+1 < len(words) {
			i++
			target = unquote(words[i])
		}
		if write && target != "" {
			writes = append(writes, target)
		}
	}
	for _, t := range writes {
		if blockDevice(t) {
			r.destructive = true
		}
		if outsideWorkspace(t) {
			r.outside = true
		}
	}
	args = skipWrappers(args)
	if len(args) == 0 {
		return
	}
	prog := strings.ToLower(path.Base(args[0]))
	rest := args[1:]
	r.programs = append(r.programs, prog)
	r.classify(prog, rest)
	if l := labelOf(prog, rest); l != "" && !slices.Contains(r.labels, l) {
		r.labels = append(r.labels, l)
	}
	if r.isDestructive(seg, prog, rest) {
		r.destructive = true
	}
	if writesOutside(prog, rest) {
		r.outside = true
	}
	if depth < maxDepth {
		if script, ok := nested(prog, rest); ok {
			r.read(script, depth+1)
		}
	}
}

// skipWrappers drops the leading NAME=value assignments and wrappers, with
// their options, and returns the program and its arguments.
func skipWrappers(args []string) []string {
	i := 0
	for i < len(args) {
		w := args[i]
		if assignment.MatchString(w) {
			i++
			continue
		}
		name := strings.ToLower(path.Base(w))
		valueOpts, ok := wrappers[name]
		if !ok {
			break
		}
		i++
	options:
		for i < len(args) {
			a := args[i]
			switch {
			case assignment.MatchString(a):
				i++
			case strings.HasPrefix(a, "-") && len(a) > 1:
				i++
				if slices.Contains(valueOpts, a) {
					i++
				}
			default:
				break options
			}
		}
		if name == "timeout" && i < len(args) {
			i++ // the duration
		}
	}
	return args[min(i, len(args)):]
}

// nested returns the command an interpreter's -c, an eval, or a find
// -exec runs, which the extractor reads as well.
func nested(prog string, args []string) (string, bool) {
	switch {
	case interpreters[prog]:
		for i, a := range args {
			if a == "-c" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c")) {
				return strings.Join(args[i+1:], " "), i+1 < len(args)
			}
		}
	case prog == "eval":
		return strings.Join(args, " "), len(args) > 0
	case prog == "find":
		for i, a := range args {
			if a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir" {
				var cmd []string
				for _, b := range args[i+1:] {
					if b == ";" || b == `\;` || b == "+" {
						break
					}
					cmd = append(cmd, b)
				}
				return strings.Join(cmd, " "), len(cmd) > 0
			}
		}
	}
	return "", false
}

// sub is a program's first argument, its subcommand. For git, the global
// options before the subcommand (-C dir, -c key=value, --git-dir=...) are
// skipped.
func sub(prog string, args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if prog == "git" && strings.HasPrefix(a, "-") {
			if a == "-C" || a == "-c" {
				i++
			}
			continue
		}
		return strings.ToLower(a)
	}
	return ""
}

var (
	readProgs    = set("ls", "cat", "head", "tail", "less", "more", "grep", "rg", "find", "fd", "stat", "wc", "pwd", "echo", "printf", "which", "file", "tree")
	gitRead      = set("status", "log", "diff", "show", "blame")
	gitLocal     = set("add", "commit", "checkout", "switch", "restore", "stash", "merge", "rebase", "reset", "tag", "cherry-pick", "branch")
	gitRemote    = set("push", "pull", "fetch", "clone")
	networkProgs = set("curl", "wget", "ssh", "scp", "rsync", "nc", "ncat", "telnet", "ftp", "sftp", "http", "https")
	deleteProgs  = set("rm", "rmdir", "unlink", "shred", "truncate")
	pkgManagers  = set("apt", "apt-get", "yum", "dnf", "apk", "brew", "pip", "pip3", "pipx", "npm", "pnpm", "yarn", "bun")
	pkgSubs      = set("install", "add", "i")
	buildProgs   = set("make", "pytest", "jest", "tox", "mvn", "gradle")
	infraProgs   = set("terraform", "kubectl", "helm", "docker", "podman", "aws", "gcloud", "az", "psql", "mysql", "mongo", "mongosh", "sqlite3", "redis-cli")
)

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func (r *reading) classify(prog string, args []string) {
	s := sub(prog, args)
	on := func(c int) { r.classes[c] = true }
	switch {
	case readProgs[prog]:
		on(clsRead)
		if prog == "find" && slices.Contains(args, "-delete") {
			on(clsDelete)
		}
	case prog == "git":
		switch {
		case gitRead[s]:
			on(clsRead)
		case gitLocal[s]:
			on(clsVCSLocal)
		case gitRemote[s]:
			on(clsVCSRemote)
		case s == "clean":
			on(clsDelete)
		}
	case prog == "gh":
		on(clsVCSRemote)
	case networkProgs[prog]:
		on(clsNetwork)
	case deleteProgs[prog]:
		on(clsDelete)
	case pkgManagers[prog]:
		if pkgSubs[s] {
			on(clsPackage)
		}
		if prog == "npm" && (s == "test" || s == "run") {
			on(clsBuildTest)
		}
	case prog == "go":
		switch s {
		case "install", "get":
			on(clsPackage)
		case "build", "test", "vet", "run":
			on(clsBuildTest)
		}
	case prog == "cargo":
		switch s {
		case "install", "add":
			on(clsPackage)
		case "build", "test", "run":
			on(clsBuildTest)
		}
	case prog == "gem":
		if s == "install" {
			on(clsPackage)
		}
	case buildProgs[prog]:
		on(clsBuildTest)
	case infraProgs[prog]:
		on(clsInfra)
	}
}

// labelOf names a segment in words of the tables above and nothing else, so
// a label never carries text of the command: the program when a table names
// it, its subcommand when a table names that, and a fixed flag word for a
// recursive delete or a forced push. A program no table names has no label.
func labelOf(prog string, args []string) string {
	s := sub(prog, args)
	switch {
	case prog == "git":
		switch {
		case s == "push" && anyArg(args, forced):
			return "git push --force"
		case gitRead[s], gitLocal[s], gitRemote[s], s == "clean":
			return "git " + s
		}
		return "git"
	case prog == "rm" && anyArg(args, func(a string) bool { return a == "--recursive" || shortFlag(a, "rR") }):
		return "rm -r"
	case pkgManagers[prog]:
		if pkgSubs[s] || (prog == "npm" && (s == "test" || s == "run")) {
			return prog + " " + s
		}
		return prog
	case prog == "go":
		if goSubs[s] {
			return "go " + s
		}
		return prog
	case prog == "cargo":
		if cargoSubs[s] {
			return "cargo " + s
		}
		return prog
	case prog == "gem":
		if s == "install" {
			return "gem install"
		}
		return prog
	case readProgs[prog], networkProgs[prog], deleteProgs[prog], buildProgs[prog], infraProgs[prog], prog == "gh":
		return prog
	}
	return ""
}

var (
	goSubs    = set("install", "get", "build", "test", "vet", "run")
	cargoSubs = set("install", "add", "build", "test", "run")
)

// forced reports an argument of git push that forces or deletes a ref.
func forced(a string) bool {
	return a == "--force" || strings.HasPrefix(a, "--force-with-lease") || strings.HasPrefix(a, "--force-if-includes") ||
		shortFlag(a, "f") || (len(a) > 1 && a[0] == '+')
}

// shortFlag reports whether a is a cluster of short options (-rf) holding
// any of the letters.
func shortFlag(a, letters string) bool {
	return len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters)
}

func anyArg(args []string, pred func(string) bool) bool { return slices.ContainsFunc(args, pred) }

var sqlDestructive = regexp.MustCompile(`(?i)\bdrop\s+(table|database)\b|\btruncate\b|\bdelete\s+from\b`)

// isDestructive applies features/1's destructive patterns to one segment.
func (r *reading) isDestructive(seg, prog string, args []string) bool {
	if sqlDestructive.MatchString(seg) || slices.Contains(args, "--no-verify") || strings.HasPrefix(prog, "mkfs") {
		return true
	}
	s := sub(prog, args)
	switch prog {
	case "rm":
		return anyArg(args, func(a string) bool {
			return a == "--recursive" || a == "--force" || shortFlag(a, "rRf")
		})
	case "git":
		switch s {
		case "push":
			return anyArg(args, func(a string) bool {
				return a == "--force" || a == "--delete" || strings.HasPrefix(a, "--force-with-lease") ||
					strings.HasPrefix(a, "--force-if-includes") || shortFlag(a, "fd") ||
					(len(a) > 1 && (a[0] == ':' || a[0] == '+'))
			})
		case "reset":
			return slices.Contains(args, "--hard")
		case "clean":
			return anyArg(args, func(a string) bool { return a == "--force" || shortFlag(a, "f") })
		case "branch":
			del := anyArg(args, func(a string) bool { return a == "--delete" || shortFlag(a, "d") })
			force := anyArg(args, func(a string) bool { return a == "--force" || shortFlag(a, "f") })
			return anyArg(args, func(a string) bool { return shortFlag(a, "D") }) || (del && force)
		}
	case "dd":
		return anyArg(args, func(a string) bool { return strings.HasPrefix(a, "of=/dev/") })
	case "chmod", "chown":
		return anyArg(args, func(a string) bool { return a == "--recursive" || shortFlag(a, "R") })
	case "kubectl":
		return slices.Contains(args, "delete")
	case "terraform":
		return slices.Contains(args, "destroy") || slices.Contains(args, "-destroy")
	case "docker", "podman":
		if slices.Contains(args, "system") && slices.Contains(args, "prune") {
			return true
		}
		rm := slices.Contains(args, "rm") || slices.Contains(args, "rmi")
		return rm && anyArg(args, func(a string) bool { return a == "--force" || shortFlag(a, "f") })
	}
	return false
}

// blockDevices are the device prefixes a redirection to which overwrites a
// disk: the spec's /dev/sd and, conservatively, the other common block
// device names.
var blockDevices = []string{"/dev/sd", "/dev/hd", "/dev/nvme", "/dev/disk", "/dev/mmcblk", "/dev/xvd", "/dev/vd"}

func blockDevice(p string) bool {
	return slices.ContainsFunc(blockDevices, func(d string) bool { return strings.HasPrefix(p, d) })
}

// systemDirs are the absolute prefixes outside any workspace: the spec's
// list and, conservatively, /lib's 32 and 64 bit variants and the /private
// paths macOS resolves /etc and /var to.
var systemDirs = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/boot", "/var", "/System", "/Library", "/private/etc", "/private/var"}

// outsideWorkspace reports whether a path argument names a system or home
// location, or climbs out of the working directory.
func outsideWorkspace(p string) bool {
	switch {
	case strings.HasPrefix(p, "~"), strings.HasPrefix(p, "$HOME"), strings.HasPrefix(p, "${HOME}"), strings.Contains(p, ".."):
		return true
	}
	return slices.ContainsFunc(systemDirs, func(d string) bool { return p == d || strings.HasPrefix(p, d+"/") })
}

var pathWriters = set("rm", "mv", "cp", "tee", "chmod", "chown", "ln")

// writesOutside applies the outside-the-workspace rule to a program's
// arguments: rm, mv, cp, tee, chmod, chown, ln, and sed with -i.
func writesOutside(prog string, args []string) bool {
	if !pathWriters[prog] {
		if prog != "sed" || !anyArg(args, func(a string) bool {
			return a == "-i" || a == "--in-place" || strings.HasPrefix(a, "-i") || strings.HasPrefix(a, "--in-place=")
		}) {
			return false
		}
	}
	return anyArg(args, func(a string) bool { return !strings.HasPrefix(a, "-") && outsideWorkspace(a) })
}
