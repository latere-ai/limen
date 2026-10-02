// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package featurize

import (
	"slices"
	"testing"
)

func TestShellClasses(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"ls -la", []string{"read"}},
		{"cat a | grep x | wc -l", []string{"read"}},
		{"git status", []string{"read"}},
		{"git log --oneline", []string{"read"}},
		{"git -C repo diff", []string{"read"}},
		{"git -c core.pager=cat show HEAD", []string{"read"}},
		{"git add . && git commit -m x", []string{"vcs_local"}},
		{"git cherry-pick abc", []string{"vcs_local"}},
		{"git push origin main", []string{"vcs_remote"}},
		{"git clone https://x", []string{"vcs_remote"}},
		{"gh pr create", []string{"vcs_remote"}},
		{"curl -s https://example.com", []string{"network"}},
		{"ssh host uptime", []string{"network"}},
		{"rm file", []string{"delete"}},
		{"git clean -n", []string{"delete"}},
		{"find . -name '*.o' -delete", []string{"read", "delete"}},
		{"npm install left-pad", []string{"package"}},
		{"pnpm add x", []string{"package"}},
		{"bun i", []string{"package"}},
		{"apt-get install -y curl", []string{"package"}},
		{"go install golang.org/x/tools/cmd/stringer@latest", []string{"package"}},
		{"go get x", []string{"package"}},
		{"cargo add serde", []string{"package"}},
		{"gem install rails", []string{"package"}},
		{"make check", []string{"build_test"}},
		{"go test ./...", []string{"build_test"}},
		{"cargo build", []string{"build_test"}},
		{"npm test", []string{"build_test"}},
		{"npm run lint", []string{"build_test"}},
		{"pytest -q", []string{"build_test"}},
		{"terraform plan", []string{"infra"}},
		{"kubectl get pods", []string{"infra"}},
		{"psql -c 'select 1'", []string{"infra"}},
		{"redis-cli ping", []string{"infra"}},
		{"git mystery", nil},
		{"npm ls", nil},
		{"go version", nil},
		{"cargo fmt", nil},
		{"gem list", nil},
		{"frobnicate --all", nil},
		{"", nil},
		{"ls; curl x; rm y; make", []string{"read", "network", "delete", "build_test"}},
	}
	for _, c := range cases {
		if got := ReadShell(c.cmd).Classes; !slices.Equal(got, c.want) {
			t.Errorf("%q: classes %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestShellPrograms(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"/usr/bin/rm -rf x", []string{"rm"}},
		{"FOO=1 BAR=2 make", []string{"make"}},
		{"sudo -E env FOO=1 rm -rf x", []string{"rm"}},
		{"sudo -u root ls", []string{"ls"}},
		{"timeout 5s curl x", []string{"curl"}},
		{"nice -n 10 make", []string{"make"}},
		{"xargs -I{} rm {}", []string{"rm"}},
		{"echo $(whoami)", []string{"echo", "whoami"}},
		{"echo `date`", []string{"echo", "date"}},
		{"sleep 1 & curl x", []string{"sleep", "curl"}},
		{"make 2>&1 | tee log", []string{"make", "tee"}},
		{"cmd &> out", []string{"cmd"}},
		{"a || b |& c", []string{"a", "b", "c"}},
		{"a\nb", []string{"a", "b"}},
		{"sudo", nil},
		{"FOO=1", nil},
		{"> out", nil},
		{`bash -lc "rm -rf /tmp/x"`, []string{"bash", "rm"}},
		{`sh -c 'curl x'`, []string{"sh", "curl"}},
		{"bash script.sh", []string{"bash"}},
		{"bash -c", []string{"bash"}},
		{"eval rm -rf x", []string{"eval", "rm"}},
		{"eval", []string{"eval"}},
		{`find . -exec rm -rf {} \;`, []string{"find", "rm"}},
		{"find . -exec", []string{"find"}},
		{"find . -exec rm {} + -print", []string{"find", "rm"}},
	}
	for _, c := range cases {
		if got := ReadShell(c.cmd).Programs; !slices.Equal(got, c.want) {
			t.Errorf("%q: programs %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestShellDestructive(t *testing.T) {
	yes := []string{
		"rm -rf x", "rm -r x", "rm -R x", "rm -f x", "rm --recursive x", "rm --force x", "rm -vf x",
		"git push --force", "git push -f origin main", "git push --force-with-lease", "git push --force-with-lease=main",
		"git push --force-if-includes", "git push --delete origin b", "git push -d origin b",
		"git push origin :branch", "git push origin +main",
		"git reset --hard HEAD~1", "git clean -fd", "git clean --force",
		"git branch -D old", "git branch -d -f old", "git branch --delete --force old",
		"git commit --no-verify -m x", "make --no-verify",
		"psql -c 'DROP TABLE users'", "mysql -e 'drop database prod'", "echo truncate", "sqlite3 db 'DELETE FROM t'",
		"mkfs.ext4 /dev/sdb1", "mkfs /dev/sdb", "dd if=x of=/dev/sda",
		"chmod -R 777 .", "chown -R me .", "chmod --recursive a .",
		"cat x > /dev/sda", "echo x >/dev/nvme0n1", "echo x > /dev/disk2",
		"kubectl delete pod x", "kubectl -n prod delete deploy x",
		"terraform destroy", "terraform apply -destroy",
		"docker rm -f c", "docker rmi --force i", "podman rm -f c", "docker system prune -a",
		"cd x && rm -rf /", "ls; rm -rf x",
		"sudo -E env FOO=1 rm -rf x",
		`bash -lc "rm -rf /tmp/x"`, "eval rm -rf x", `find . -exec rm -rf {} \;`,
		"echo $(rm -rf x)",
	}
	for _, c := range yes {
		if !ReadShell(c).Destructive {
			t.Errorf("%q is not destructive", c)
		}
	}
	no := []string{
		"rm file", "rm -i file", "rm -v file", "git push", "git push origin main", "git push -u origin main",
		"git push origin main:main", "git reset --soft HEAD~1", "git clean -n", "git branch -d old",
		"git branch feature", "psql -c 'select 1'", "dd if=x of=out.img", "chmod 644 a", "chown me a",
		"echo x > /dev/null", "echo x 2>/dev/null", "kubectl get pods", "terraform plan",
		"docker rm c", "docker system df", "docker run --rm img", "ls -la",
	}
	for _, c := range no {
		if ReadShell(c).Destructive {
			t.Errorf("%q is destructive", c)
		}
	}
}

func TestShellOutsideWorkspace(t *testing.T) {
	yes := []string{
		"rm /etc/passwd", "cp a ~/x", "mv a $HOME/x", "mv a ${HOME}/x", "cp ../a b", "ln -s a /usr/local/bin/a",
		"tee -a /etc/hosts", "chmod 600 /var/lib/x", "chown root /boot/x", "sed -i s/a/b/ /etc/x",
		"sed --in-place=.bak s/a/b/ /etc/x", "sed -i.bak s/a/b/ ~/x",
		"echo hi > /etc/hosts", "echo hi >> /usr/share/x", "echo hi >/sbin/x", "cat a 2> /var/log/x",
		"cp a /System/x", "cp a /Library/x", "cp a /lib64/x", "cp a /private/etc/x", "rm /etc",
	}
	for _, c := range yes {
		if !ReadShell(c).OutsideWorkspace {
			t.Errorf("%q is not outside the workspace", c)
		}
	}
	no := []string{
		"rm a/b", "cp a b", "cat /etc/hosts", "sed s/a/b/ /etc/x", "echo hi > out.txt", "cp a /tmp/x",
		"cp a /etcetera/x", "rm -rf ./build", "ls ~", "grep x ../y", "echo x 2>&1",
	}
	for _, c := range no {
		if ReadShell(c).OutsideWorkspace {
			t.Errorf("%q is outside the workspace", c)
		}
	}
}

func TestRedirection(t *testing.T) {
	cases := []struct {
		w            string
		write        bool
		target       string
		isRedir, dup bool
	}{
		{">", true, "", true, false},
		{">>out", true, "out", true, false},
		{"2>err", true, "err", true, false},
		{"&>all", true, "all", true, false},
		{"2>&1", true, "", true, true},
		{"<in", false, "in", true, false},
		{"<<EOF", false, "EOF", true, false},
		{"word", false, "", false, false},
		{"=>", false, "", false, false},
	}
	for _, c := range cases {
		write, target, isRedir, dup := redirection(c.w)
		if write != c.write || target != c.target || isRedir != c.isRedir || dup != c.dup {
			t.Errorf("redirection(%q) = %v %q %v %v", c.w, write, target, isRedir, dup)
		}
	}
}

// A command nested deeper than maxDepth is not read further, so a
// pathological input cannot recurse without end.
func TestShellDepthBound(t *testing.T) {
	cmd := "rm -rf x"
	for range maxDepth + 2 {
		cmd = "eval " + cmd
	}
	if ReadShell(cmd).Destructive {
		t.Error("a command nested past the bound was read")
	}
}

func FuzzReadShell(f *testing.F) {
	for _, s := range []string{"rm -rf /", "a && b || c; d | e", "bash -c 'eval $(x)'", "sudo env A=1 timeout", `find . -exec \;`, "2>&1 >"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r := ReadShell(s)
		for _, c := range r.Classes {
			if !slices.Contains(classNames[:], c) {
				t.Fatalf("unknown class %q", c)
			}
		}
	})
}
