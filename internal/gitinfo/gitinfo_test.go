// =========================================================================
//
//   Copyright (c) 2026 Arxila OSS (https://arxila.io)
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
//   implied. See the License for the specific language governing
//   permissions and limitations under the License.
//
// =========================================================================

package gitinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionString(t *testing.T) {
	for _, c := range []struct {
		v    Version
		want string
	}{
		{Version{}, "unknown"},
		{Version{Known: true}, "unknown"},
		{Version{Known: true, Tags: []string{"v1.0.0"}, Branch: "main", Commit: "abc1234"}, "tag: v1.0.0"},
		{Version{Known: true, Tags: []string{"v1.0.0", "stable"}, Commit: "abc1234"}, "tags: v1.0.0, stable"},
		{Version{Known: true, Tags: []string{}, Branch: "main", Commit: "abc1234"}, "branch: main"},
		{Version{Known: true, Commit: "abc1234"}, "commit: abc1234 (detached)"},
		{Version{Known: false, Tags: []string{"v1"}}, "unknown"},
	} {
		if got := c.v.String(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.v, got, c.want)
		}
	}
}

func TestUnavailable(t *testing.T) {
	var nilReader *Reader
	for _, r := range []*Reader{nilReader, {}} {
		if r.Available() {
			t.Fatal("a reader without git is available")
		}
		if v := r.Read(t.TempDir()); v.Known || v.String() != "unknown" {
			t.Fatalf("version = %+v", v)
		}
	}
	t.Setenv("PATH", t.TempDir()) // no git there
	if NewReader().Available() {
		t.Fatal("git was found in an empty PATH")
	}
}

// repo creates a git repository with one commit on branch main, isolated from the user's git configuration.
func repo(t *testing.T) (*Reader, string, func(args ...string)) {
	t.Helper()
	r := NewReader()
	if !r.Available() {
		t.Skip("git is not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command(r.git, append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com",
			"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "first")
	return r, dir, git
}

func TestRead(t *testing.T) {
	r, dir, git := repo(t)

	v := r.Read(dir)
	if !v.Known || v.Branch != "main" || v.Commit == "" || len(v.Tags) != 0 || v.String() != "branch: main" {
		t.Fatalf("on a branch: %+v", v)
	}
	commit := v.Commit

	// Tags of the checked-out commit win over the branch, in git's order.
	git("tag", "v1.0.0")
	git("tag", "-a", "-m", "annotated", "stable")
	v = r.Read(dir)
	if strings.Join(v.Tags, ",") != "stable,v1.0.0" || v.Branch != "main" || v.String() != "tags: stable, v1.0.0" {
		t.Fatalf("tagged: %+v", v)
	}

	// A new commit: the tags no longer point at it.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-a", "-m", "second")
	if v = r.Read(dir); len(v.Tags) != 0 || v.Commit == commit {
		t.Fatalf("after a commit: %+v", v)
	}

	// Detached at a tag, and at a commit without tags.
	git("switch", "-q", "--detach", "v1.0.0")
	if v = r.Read(dir); v.Branch != "" || v.Commit != commit || v.String() != "tags: stable, v1.0.0" {
		t.Fatalf("detached at a tag: %+v", v)
	}
	git("switch", "-q", "main")
	git("switch", "-q", "--detach", "HEAD")
	if v = r.Read(dir); v.Branch != "" || !strings.HasSuffix(v.String(), "(detached)") {
		t.Fatalf("detached: %+v", v)
	}

	// A subfolder of the checkout has the checkout's version.
	sub := filepath.Join(dir, "skills")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := r.Read(sub); got.Commit != v.Commit {
		t.Fatalf("subfolder: %+v", got)
	}
}

func TestReadOutsideRepositories(t *testing.T) {
	r, dir, _ := repo(t)

	// A folder that is not a git checkout (with no repository above it), a missing folder, and a repository without
	// commits have no known version.
	plain := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	empty := t.TempDir()
	if out, err := exec.Command(r.git, "-C", empty, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, folder := range []string{plain, filepath.Join(plain, "missing"), empty} {
		if v := r.Read(folder); v.Known {
			t.Errorf("%s: %+v", folder, v)
		}
	}

	// Reading never changes the repository.
	before, err := os.ReadDir(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	r.Read(dir)
	after, err := os.ReadDir(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf(".git changed: %d entries, then %d", len(before), len(after))
	}
}
