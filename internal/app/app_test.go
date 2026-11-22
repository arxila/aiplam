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

package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/gitinfo"
)

type env struct {
	t    *testing.T
	root string
	home string
	app  *App
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := fsutil.Canonical(t.TempDir())
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, root: root, home: home, app: &App{Home: home, Cwd: root, Git: gitinfo.NewReader()}}
}

// run runs aiplam with args and returns the exit code, stdout and stderr.
func (e *env) run(args ...string) (int, string, string) {
	e.t.Helper()
	var out, errOut bytes.Buffer
	e.app.Stdout, e.app.Stderr = &out, &errOut
	e.app.gitWarned = false
	code := e.app.Run(args)
	return code, out.String(), errOut.String()
}

// mustRun runs aiplam and fails the test unless it exits with want.
func (e *env) mustRun(want int, args ...string) (string, string) {
	e.t.Helper()
	code, out, errOut := e.run(args...)
	if code != want {
		e.t.Fatalf("aiplam %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, want, out,
			errOut)
	}
	return out, errOut
}

// skill creates a skill folder (with a SKILL.md) under dir.
func (e *env) skill(dir, name string) string {
	e.t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// playbook creates a playbook folder under root with the given skills in its skills/ subfolder.
func (e *env) playbook(rel string, skills ...string) string {
	e.t.Helper()
	dir := filepath.Join(e.root, rel)
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	for _, s := range skills {
		e.skill(filepath.Join(dir, "skills"), s)
	}
	return dir
}

func (e *env) agentsSkills() string { return filepath.Join(e.home, ".agents", "skills") }

// assertLink checks that path is a link to target.
func (e *env) assertLink(path, target string) {
	e.t.Helper()
	state, current, err := fsutil.Inspect(path)
	if err != nil || state != fsutil.Link || !fsutil.SamePath(current, target) {
		e.t.Fatalf("%s: want a link to %s, got state %v -> %q (err %v)", path, target, state, current, err)
	}
}

func (e *env) assertMissing(path string) {
	e.t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		e.t.Fatalf("%s should not exist", path)
	}
}

// config returns the first enabled set.
func (e *env) config() *config.Set {
	e.t.Helper()
	cfg, err := config.LoadSet(e.home, e.install().Enabled[0])
	if err != nil {
		e.t.Fatal(err)
	}
	return cfg
}

func (e *env) install() *config.Install {
	e.t.Helper()
	inst, err := config.LoadInstall(e.home)
	if err != nil {
		e.t.Fatal(err)
	}
	return inst
}

// writeSet replaces the content of a set's file.
func (e *env) writeSet(set, content string) {
	e.t.Helper()
	if err := os.WriteFile(config.SetFile(e.home, set), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestCommandsNeedInit(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"list"}, {"check"}, {"update", "--all"}, {"add", "x", "."}, {"remove", "x"}} {
		_, errOut := e.mustRun(ExitFailure, args...)
		if !strings.Contains(errOut, "aiplam init") {
			t.Errorf("%v: want a hint to run aiplam init, got %q", args, errOut)
		}
	}
}

func TestInit(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitUsage, "init")                  // a folder (or --global) is required
	e.mustRun(ExitUsage, "init", ".", "-g")       // not both
	e.mustRun(ExitUsage, "init", "-b", e.home)    // the folder is an argument
	e.mustRun(ExitUsage, "init", "-g", "-t", "x") // unknown type
	e.mustRun(ExitFailure, "init", filepath.Join(e.root, "missing"))
	e.mustRun(ExitOK, "init", "-g") // agents by default
	if !fsutil.IsDir(filepath.Join(e.home, ".agents")) {
		t.Fatal("~/.agents not created")
	}
	if inst := e.install(); strings.Join(inst.Types, ",") != "agents" || strings.Join(inst.Enabled, ",") != "default" {
		t.Fatalf("install = %+v", inst)
	}
	if n := len(e.config().Playbooks); n != 0 {
		t.Fatalf("%d playbooks", n)
	}
	_, errOut := e.mustRun(ExitFailure, "init", "~", "--type", "claude")
	if !strings.Contains(errOut, "already initialized") || !strings.Contains(errOut, "aiplam type") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	e2 := newEnv(t)
	e2.mustRun(ExitOK, "init", "-g", "--type", "agents,claude", "-s", "work")
	if inst := e2.install(); strings.Join(inst.Types, ",") != "agents,claude" ||
		strings.Join(inst.Enabled, ",") != "work" {
		t.Fatalf("install = %+v", inst)
	}
	if config.SetExists(e2.home, "default") {
		t.Fatal("the default set should not be created")
	}
	if !fsutil.IsDir(filepath.Join(e2.home, ".claude")) {
		t.Fatal("~/.claude not created")
	}
	e2.mustRun(ExitUsage, "init", "-g", "-t", "agents", "-s", config.InstallName)
}

func TestAddLinksPrefixedSkillsOnly(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "--type", "agents", "--type", "claude")
	pb := e.playbook("ws/arxila-ai-playbook", "arxila-one", "arxila-two", "other-skill")
	if err := os.MkdirAll(filepath.Join(pb, "skills", "arxila-noskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = pb
	e.mustRun(ExitOK, "add", "arxila", ".")

	for _, target := range []string{".agents", ".claude"} {
		dir := filepath.Join(e.home, target, "skills")
		e.assertLink(filepath.Join(dir, "arxila-one"), filepath.Join(pb, "skills", "arxila-one"))
		e.assertLink(filepath.Join(dir, "arxila-two"), filepath.Join(pb, "skills", "arxila-two"))
		e.assertMissing(filepath.Join(dir, "other-skill"))
		e.assertMissing(filepath.Join(dir, "arxila-noskill"))
	}
	cfg := e.config()
	if len(cfg.Playbooks) != 1 || cfg.Playbooks[0].Name != "arxila" || cfg.Playbooks[0].Dir(e.home) != pb ||
		cfg.Playbooks[0].Artifacts.Skills.Path != "skills" {
		t.Fatalf("unexpected config: %+v", cfg.Playbooks)
	}

	_, errOut := e.mustRun(ExitFailure, "add", "arxila", pb)
	if !strings.Contains(errOut, "already registered") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestAddSkillsPathRoot(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	repo := filepath.Join(e.root, "repo")
	e.skill(repo, "x-a")
	e.skill(repo, "y-b")
	e.mustRun(ExitOK, "add", "x", repo, "--skills-path", ".")
	e.mustRun(ExitOK, "add", "y", repo, "--skills-path=.")
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(repo, "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-b"), filepath.Join(repo, "y-b"))

	_, errOut := e.mustRun(ExitFailure, "add", "z", repo)
	if !strings.Contains(errOut, "--skills-path") {
		t.Fatalf("want a hint about --skills-path: %s", errOut)
	}
}

func TestAddConflictCancelsEverything(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a", "x-b")
	// A real folder (an old copy) where x-b must be linked.
	e.skill(e.agentsSkills(), "x-b")
	_, errOut := e.mustRun(ExitFailure, "add", "x", pb)
	if !strings.Contains(errOut, "not a link") || !strings.Contains(errOut, "was not added") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	if len(e.config().Playbooks) != 0 {
		t.Fatal("playbook registered despite the conflict")
	}
}

func TestSameNameInSeveralFolders(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	a := e.playbook("one/pb-a", "x-a")
	b := e.playbook("two/pb-b", "x-b")
	collide := e.playbook("three/pb-a", "x-c")
	e.mustRun(ExitOK, "add", "x", a)
	out, _ := e.mustRun(ExitOK, "add", "x", b)
	if !strings.Contains(out, "x is now identified as x@pb-a") {
		t.Fatalf("missing id change notice: %s", out)
	}
	_, errOut := e.mustRun(ExitFailure, "add", "x", collide)
	if !strings.Contains(errOut, "ids would collide") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	out, _ = e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "x@pb-a\n") || !strings.Contains(out, "x@pb-b\n") {
		t.Fatalf("unexpected list: %s", out)
	}
	_, errOut = e.mustRun(ExitFailure, "update", "x")
	if !strings.Contains(errOut, "several playbooks match 'x' (x@pb-a, x@pb-b)") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "update", "x", b)
	e.mustRun(ExitOK, "update", "x@pb-a")

	// The same skill name in both folders: the second playbook cannot be added.
	e.skill(filepath.Join(a, "skills"), "x-dup")
	e.skill(filepath.Join(b, "skills"), "x-dup")
	_, errOut = e.mustRun(ExitFailure, "update", "x@pb-a")
	if !strings.Contains(errOut, "also provided by playbook x@pb-b") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	out, _ = e.mustRun(ExitOK, "remove", "x@pb-b")
	if !strings.Contains(out, "x@pb-a is now identified as x") {
		t.Fatalf("missing id change notice: %s", out)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-b"))
	e.mustRun(ExitOK, "update", "x")
	e.assertLink(filepath.Join(e.agentsSkills(), "x-dup"), filepath.Join(a, "skills", "x-dup"))
}

func TestAddFailsWhenSkillProvidedByAnotherFolder(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	a := e.playbook("pb-a", "x-same")
	b := e.playbook("pb-b", "x-same", "x-other")
	e.mustRun(ExitOK, "add", "x", a)
	_, errOut := e.mustRun(ExitFailure, "add", "x", b)
	if !strings.Contains(errOut, "was not added") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-other"))
	if n := len(e.config().Playbooks); n != 1 {
		t.Fatalf("%d playbooks registered", n)
	}
}

func TestUpdate(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a", "x-b")
	e.mustRun(ExitOK, "add", "x", pb)
	unrelated := filepath.Join(e.agentsSkills(), "personal")
	if err := fsutil.CreateLink(unrelated, e.root); err != nil {
		t.Fatal(err)
	}

	e.skill(filepath.Join(pb, "skills"), "x-c")
	if err := os.RemoveAll(filepath.Join(pb, "skills", "x-a")); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = filepath.Join(pb) // update with no arguments: the playbook of the current folder
	out, _ := e.mustRun(ExitOK, "update")
	if !strings.Contains(out, "+ ") || !strings.Contains(out, "- ") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "x-c"), filepath.Join(pb, "skills", "x-c"))
	e.assertLink(unrelated, e.root)

	out, _ = e.mustRun(ExitOK, "update", "--all")
	if !strings.Contains(out, "up to date") {
		t.Fatalf("unexpected output: %s", out)
	}

	// A conflict cancels the whole update.
	e.skill(filepath.Join(pb, "skills"), "x-d")
	e.skill(filepath.Join(pb, "skills"), "x-e")
	if err := fsutil.CreateLink(filepath.Join(e.agentsSkills(), "x-e"), e.root); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "update", "x")
	if !strings.Contains(errOut, "cancelled") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-d"))

	e.app.Cwd = e.root
	_, errOut = e.mustRun(ExitFailure, "update")
	if !strings.Contains(errOut, "is registered for the current folder") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestLongestPrefixWins(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a", "x-y-b")
	skills := filepath.Join(pb, "skills")
	e.mustRun(ExitOK, "add", "x", pb)
	e.assertLink(filepath.Join(e.agentsSkills(), "x-y-b"), filepath.Join(skills, "x-y-b"))

	// x-y takes over x-y-b, whose link stays in place.
	e.mustRun(ExitOK, "add", "x-y", pb)
	out, _ := e.mustRun(ExitOK, "list", "--json")
	var list listOutput
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	names := map[string][]string{}
	for _, p := range list.Sets[0].Playbooks {
		for _, s := range p.Artifacts.Skills.Items {
			names[p.ID] = append(names[p.ID], s.Name)
		}
	}
	if strings.Join(names["x"], ",") != "x-a" || strings.Join(names["x-y"], ",") != "x-y-b" {
		t.Fatalf("unexpected ownership: %v", names)
	}
	e.mustRun(ExitOK, "check")

	// Removing x leaves x-y's links alone.
	e.mustRun(ExitOK, "remove", "x")
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "x-y-b"), filepath.Join(skills, "x-y-b"))

	// Removing x-y while x is registered leaves the link to x, which owns it again.
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "remove", "x-y")
	e.assertLink(filepath.Join(e.agentsSkills(), "x-y-b"), filepath.Join(skills, "x-y-b"))
	e.mustRun(ExitOK, "check")

	e.mustRun(ExitOK, "remove", pb)
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-y-b"))
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	if n := len(e.config().Playbooks); n != 0 {
		t.Fatalf("%d playbooks still registered", n)
	}
	if !fsutil.IsDir(filepath.Join(skills, "x-a")) {
		t.Fatal("remove deleted playbook content")
	}
}

func TestRemoveKeepsRealFolders(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	copyDir := e.skill(e.agentsSkills(), "x-copy")
	e.mustRun(ExitOK, "remove", "x")
	if !fsutil.IsDir(copyDir) {
		t.Fatal("a real folder was removed")
	}
	_, errOut := e.mustRun(ExitFailure, "remove", "x")
	if !strings.Contains(errOut, "no playbook of set 'default' is registered") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestCheck(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	out, _ := e.mustRun(ExitOK, "check")
	if !strings.Contains(out, "Everything is up to date") {
		t.Fatalf("unexpected output: %s", out)
	}

	e.skill(filepath.Join(pb, "skills"), "x-new")
	if err := fsutil.CreateLink(filepath.Join(e.agentsSkills(), "dangling"), filepath.Join(e.root, "gone")); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "would link") || !strings.Contains(errOut, "broken link") {
		t.Fatalf("unexpected warnings: %s", errOut)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-new")) // check changes nothing

	// A broken link into the playbook is removed by update.
	if err := os.RemoveAll(filepath.Join(pb, "skills", "x-a")); err != nil {
		t.Fatal(err)
	}
	_, errOut = e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "would remove") {
		t.Fatalf("unexpected warnings: %s", errOut)
	}
	e.mustRun(ExitOK, "update", "x")
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
}

func TestCheckInvalidConfig(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	for _, content := range []string{
		"playbooks: []\nunknown: 1\n",
		"targets: [~/.agents]\n",
		"playbooks:\n  - name: x\n    folder: /absolute\n    artifacts:\n      skills:\n        path: skills\n",
		"playbooks:\n  - name: x\n    folder: ~/p\n    artifacts:\n      skills:\n        path: skills\n",
		"playbooks:\n  - name: 'x@y'\n    folder: relative\n    artifacts:\n      skills:\n        path: skills\n",
		"playbooks: [\n",
	} {
		e.writeSet("default", content)
		e.mustRun(ExitFailure, "check")
		_, errOut := e.mustRun(ExitFailure, "list")
		if !strings.Contains(errOut, "default.yaml") {
			t.Fatalf("list should point to the set's file for %q: %s", content, errOut)
		}
	}
	e.writeSet("default", "")
	e.mustRun(ExitOK, "check") // an empty file is an empty set

	file := config.InstallFile(e.home)
	for _, content := range []string{
		"enabled: [default]\n",
		"types: []\nenabled: [default]\n",
		"types: [agents, other]\nenabled: [default]\n",
		"types: [agents, agents]\nenabled: [default]\n",
		"targets: [~/.agents]\nenabled: [default]\n",
		"types: [agents]\nenabled: [default]\nplaybooks: []\n",
		"types: [agents]\nenabled: ['../x']\n",
		"types: [agents]\nenabled: [default, Default]\n",
		"types: [agents]\nenabled: default\n",
		"",
	} {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		e.mustRun(ExitFailure, "check")
		_, errOut := e.mustRun(ExitFailure, "list")
		if !strings.Contains(errOut, config.InstallName+".yaml") {
			t.Fatalf("list should point to the installation file for %q: %s", content, errOut)
		}
	}
}

func TestListJSONAndVersions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", pb, "-c", "user.name=t", "-c", "user.email=t@example.com",
			"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "first")
	e.mustRun(ExitOK, "add", "x", pb)

	readList := func() listOutput {
		out, _ := e.mustRun(ExitOK, "list", "--json")
		var list listOutput
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		return list
	}
	list := readList()
	if len(list.Sets) != 1 || len(list.Sets[0].Playbooks) != 1 || !list.Sets[0].Enabled {
		t.Fatalf("unexpected list: %+v", list)
	}
	p := list.Sets[0].Playbooks[0]
	if p.ID != "x" || p.Name != "x" || p.Folder != pb || p.Label != "branch: main" || !p.Version.Known ||
		len(p.Artifacts.Skills.Items) != 1 || !p.Artifacts.Skills.Items[0].Linked {
		t.Fatalf("unexpected playbook: %+v", p)
	}

	git("tag", "v1.0.0")
	git("tag", "x-1.0.0")
	git("checkout", "-q", "--detach", "v1.0.0")
	if got := readList().Sets[0].Playbooks[0].Label; got != "tags: v1.0.0, x-1.0.0" {
		t.Fatalf("label = %q", got)
	}
	out, _ := e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "version: tags: v1.0.0, x-1.0.0") {
		t.Fatalf("unexpected list: %s", out)
	}

	e.app.Git = &gitinfo.Reader{}
	code, out, errOut := e.run("list")
	if code != ExitOK || !strings.Contains(out, "version: unknown") || !strings.Contains(errOut, "git was not found") {
		t.Fatalf("unexpected output without git: %s / %s", out, errOut)
	}
}

func TestUsageErrors(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitUsage)
	e.mustRun(ExitUsage, "frobnicate")
	e.mustRun(ExitUsage, "init", "--bogus")
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	e.mustRun(ExitUsage, "add", "x")
	e.mustRun(ExitUsage, "add", "x@y", ".")
	e.mustRun(ExitUsage, "update", "--all", "x")
	e.mustRun(ExitUsage, "remove")
	out, _ := e.mustRun(ExitOK, "add", "--help")
	if !strings.Contains(out, "Usage: aiplam add") {
		t.Fatalf("unexpected help: %s", out)
	}
	out, _ = e.mustRun(ExitOK, "--version")
	if !strings.HasPrefix(out, "aiplam ") {
		t.Fatalf("unexpected version: %s", out)
	}
}

func TestSets(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pbA := e.playbook("pa", "x-a")
	pbB := e.playbook("pb", "y-b")
	pbC := e.playbook("pc", "z-c")
	e.mustRun(ExitOK, "add", "x", pbA)
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pbA, "skills", "x-a"))

	// --set needs an existing set.
	_, errOut := e.mustRun(ExitFailure, "list", "--set", "work")
	if !strings.Contains(errOut, "set 'work' does not exist") || !strings.Contains(errOut, "aiplam set add work") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "set", "add", "work")
	e.mustRun(ExitFailure, "set", "add", "work")
	e.mustRun(ExitUsage, "set", "add", config.InstallName)

	// A set that is not enabled is only recorded: nothing is linked or unlinked.
	out, _ := e.mustRun(ExitOK, "add", "y", pbB, "-s", "work")
	if !strings.Contains(out, "not enabled") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-b"))
	out, _ = e.mustRun(ExitOK, "update", "--all", "-s", "work")
	if !strings.Contains(out, "Nothing to update") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.mustRun(ExitOK, "check", "-s", "work")
	var list listOutput
	out, _ = e.mustRun(ExitOK, "list", "--json", "--set=work")
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Sets) != 1 || list.Sets[0].Name != "work" ||
		list.Sets[0].Enabled || list.Sets[0].Playbooks[0].Artifacts.Skills.Items[0].Linked {
		t.Fatalf("unexpected list: %v %s", err, out)
	}

	// Enabling a set adds it to the enabled ones.
	e.mustRun(ExitOK, "set", "enable", "work")
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pbA, "skills", "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-b"), filepath.Join(pbB, "skills", "y-b"))
	if got := strings.Join(e.install().Enabled, ","); got != "default,work" {
		t.Fatalf("enabled = %s", got)
	}
	out, _ = e.mustRun(ExitOK, "set", "list")
	if !strings.Contains(out, "* default") || !strings.Contains(out, "* work") {
		t.Fatalf("unexpected sets: %s", out)
	}
	out, _ = e.mustRun(ExitOK, "set", "enable", "work")
	if !strings.Contains(out, "already enabled") {
		t.Fatalf("unexpected output: %s", out)
	}

	// With several sets enabled: list and check cover them all, add needs --set, and selectors look in all of them.
	out, _ = e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "Set default") || !strings.Contains(out, "Set work") {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitOK, "check")
	_, errOut = e.mustRun(ExitFailure, "add", "z", pbC)
	if !strings.Contains(errOut, "--set") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "update", "y")
	e.mustRun(ExitOK, "update", "--all")

	// Sets enabled together cannot provide the same skill.
	_, errOut = e.mustRun(ExitFailure, "add", "y", pbB, "-s", "default")
	if !strings.Contains(errOut, "also provided by playbook y of set work") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	// Disabling a set unlinks its playbooks only.
	e.mustRun(ExitOK, "set", "disable", "default")
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-b"), filepath.Join(pbB, "skills", "y-b"))
	e.mustRun(ExitFailure, "set", "disable", "default") // not enabled
	e.mustRun(ExitOK, "remove", "y")                    // the only enabled set
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-b"))
	e.mustRun(ExitOK, "add", "y", pbB)

	// The enabled sets cannot be removed; others can, unlinking nothing.
	_, errOut = e.mustRun(ExitFailure, "set", "remove", "work")
	if !strings.Contains(errOut, "aiplam set disable work") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "set", "remove", "default")
	e.mustRun(ExitFailure, "list", "-s", "default")
	e.mustRun(ExitFailure, "set", "enable", "default")

	// No set enabled.
	e.mustRun(ExitOK, "set", "disable", "work")
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-b"))
	out, _ = e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "No set is enabled") {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitOK, "check")
	_, errOut = e.mustRun(ExitFailure, "add", "z", pbC)
	if !strings.Contains(errOut, "no set is enabled") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	e.mustRun(ExitUsage, "set")
	e.mustRun(ExitUsage, "set", "frob")
	e.mustRun(ExitUsage, "set", "enable")
	e.mustRun(ExitUsage, "set", "enable", "a,,b")
	e.mustRun(ExitUsage, "set", "list", "-s", "work")
	e.mustRun(ExitUsage, "list", "--set")
	e.mustRun(ExitUsage, "list", "--set", "../x")
	e.mustRun(ExitUsage, "list", "-s", "a.b")
}

func TestSetEnableCollisions(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a", "x-b")
	other := e.playbook("q", "y-a")
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "set", "add", "work")
	e.mustRun(ExitOK, "add", "x", pb, "-s", "work")
	e.mustRun(ExitOK, "set", "add", "more")
	e.mustRun(ExitOK, "add", "y", other, "-s", "more")

	// Enabling a set whose links already exist (from an enabled set) fails, changing nothing.
	_, errOut := e.mustRun(ExitFailure, "set", "enable", "work,more")
	if !strings.Contains(errOut, "collide") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-a"))
	if got := strings.Join(e.install().Enabled, ","); got != "default" {
		t.Fatalf("enabled = %s", got)
	}

	// Also when the link exists for any other reason, even if it is right.
	e.mustRun(ExitOK, "set", "disable", "default")
	if err := fsutil.CreateLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a")); err != nil {
		t.Fatal(err)
	}
	_, errOut = e.mustRun(ExitFailure, "set", "enable", "work")
	if !strings.Contains(errOut, "already exists") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	if err := fsutil.RemoveLink(filepath.Join(e.agentsSkills(), "x-a")); err != nil {
		t.Fatal(err)
	}

	// Two sets being enabled together cannot collide either.
	_, errOut = e.mustRun(ExitFailure, "set", "enable", "default", "work")
	if !strings.Contains(errOut, "collide") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "set", "enable", "work,more")
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "x-b"), filepath.Join(pb, "skills", "x-b"))
}

func TestSetSwitch(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a", "x-b")
	other := e.playbook("q", "y-a")
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "set", "add", "work")
	e.mustRun(ExitOK, "add", "x", pb, "-s", "work")
	e.mustRun(ExitOK, "set", "add", "more")
	e.mustRun(ExitOK, "add", "y", other, "-s", "more")

	// Switching replaces the enabled sets; links wanted before and after are left alone.
	out, _ := e.mustRun(ExitOK, "set", "switch", "work,more")
	if strings.Contains(out, "x-a") {
		t.Fatalf("links wanted by both sets should be left alone: %s", out)
	}
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	if got := strings.Join(e.install().Enabled, ","); got != "work,more" {
		t.Fatalf("enabled = %s", got)
	}
	e.mustRun(ExitOK, "set", "switch", "more")
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	out, _ = e.mustRun(ExitOK, "set", "switch", "more")
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.mustRun(ExitFailure, "set", "switch", "default,work") // they collide
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	e.mustRun(ExitFailure, "set", "switch", "nope")
}

func TestType(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	out, _ := e.mustRun(ExitOK, "type")
	if !strings.Contains(out, "agents   ~"+string(filepath.Separator)+".agents") {
		t.Fatalf("unexpected types: %q", out)
	}

	// Add a type: the enabled set is linked into its folder.
	claude := filepath.Join(e.home, ".claude", "skills")
	e.mustRun(ExitOK, "type", "agents", "claude")
	e.assertLink(filepath.Join(claude, "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	out, _ = e.mustRun(ExitOK, "type", "claude,agents")
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("unexpected output: %s", out)
	}

	// Replace the types: the old folder is unlinked.
	out, _ = e.mustRun(ExitOK, "type", "claude")
	if !strings.Contains(out, "x-a (type removed)") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	e.assertLink(filepath.Join(claude, "x-a"), filepath.Join(pb, "skills", "x-a"))
	if got := e.install().Types; strings.Join(got, ",") != "claude" {
		t.Fatalf("types = %v", got)
	}

	// A conflict in a new folder cancels everything.
	if err := os.MkdirAll(filepath.Join(e.agentsSkills(), "x-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "type", "agents")
	if !strings.Contains(errOut, "not changed") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertLink(filepath.Join(claude, "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitUsage, "type", "agents", "-s", "default")
	e.mustRun(ExitUsage, "type", "other")
	e.mustRun(ExitUsage, "type", "agents,agents") // listed twice
	e.mustRun(ExitUsage, "init", "-g", "-t", "agents", "-t", "agents")
}

func TestShortOptions(t *testing.T) {
	e := newEnv(t)
	claude := filepath.Join(e.home, ".claude")
	e.mustRun(ExitOK, "init", "-g", "-t", "agents", "-t=claude", "-s", "main")
	if inst := e.install(); strings.Join(inst.Types, ",") != "agents,claude" ||
		strings.Join(inst.Enabled, ",") != "main" {
		t.Fatalf("install = %+v", inst)
	}
	e.mustRun(ExitOK, "set", "add", "work")

	pb := filepath.Join(e.root, "flat")
	e.skill(pb, "x-a")
	e.mustRun(ExitOK, "add", "x", pb, "-sp", ".")
	e.assertLink(filepath.Join(claude, "skills", "x-a"), filepath.Join(pb, "x-a"))
	e.mustRun(ExitOK, "add", "x", pb, "-sp=.", "-s", "work")
	e.mustRun(ExitOK, "update", "--all", "-s", "work")
	e.mustRun(ExitOK, "list", "-s", "work")
	e.mustRun(ExitOK, "check", "-s", "work")
	e.mustRun(ExitOK, "remove", "x", "-s", "work")

	_, errOut := e.mustRun(ExitUsage, "add", "y", pb, "-x")
	if !strings.Contains(errOut, "unknown option: -x") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitUsage, "list", "-t", "agents") // -t is an option of init only
	e.mustRun(ExitUsage, "add", "y", pb, "-sp")
}

func TestVersion(t *testing.T) {
	e := newEnv(t)
	for _, arg := range []string{"-v", "--version", "version"} {
		out, _ := e.mustRun(ExitOK, arg)
		if out != "aiplam "+Version+"\n" {
			t.Fatalf("%s: unexpected output %q", arg, out)
		}
	}
	_, errOut := e.mustRun(ExitUsage)
	if !strings.Contains(errOut, "aiplam "+Version+" - AI Playbook Manager\n"+Copyright) {
		t.Fatalf("unexpected help: %s", errOut)
	}
}

func TestPlaybookBehindSymlink(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)

	// Move the playbook and leave a link at its old path: its links still lead to its skills.
	moved := filepath.Join(e.root, "moved")
	if err := os.Rename(pb, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, pb); err != nil {
		t.Skipf("cannot create symbolic links: %v", err)
	}
	out, _ := e.mustRun(ExitOK, "update", "x")
	if !strings.Contains(out, "up to date") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.mustRun(ExitOK, "check")
	e.app.Cwd = moved // the real folder selects the playbook registered through the link
	e.mustRun(ExitOK, "update")
	e.mustRun(ExitOK, "remove", "x")
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
}

func TestFoldersAreRelativeToBase(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	inside := filepath.Join(e.home, "playbooks", "p")
	e.skill(filepath.Join(inside, "skills"), "x-a")
	outside := e.playbook("q", "y-a")
	e.mustRun(ExitOK, "add", "x", inside)
	e.mustRun(ExitOK, "add", "y", outside)
	cfg := e.config()
	if cfg.Playbooks[0].Folder != "playbooks/p" || cfg.Playbooks[1].Folder != "../q" {
		t.Fatalf("folders = %q, %q", cfg.Playbooks[0].Folder, cfg.Playbooks[1].Folder)
	}
	// Links are relative too (junctions on Windows are always absolute).
	link := filepath.Join(e.agentsSkills(), "x-a")
	if target, err := os.Readlink(link); runtime.GOOS != "windows" && (err != nil || filepath.IsAbs(target)) {
		t.Fatalf("link target = %q (%v), want a relative path", target, err)
	}
	e.assertLink(link, filepath.Join(inside, "skills", "x-a"))
	e.mustRun(ExitOK, "check")
	e.mustRun(ExitOK, "remove", inside)
	e.assertMissing(link)
}

func TestArtifactsStayInsidePlaybook(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a")
	other := e.playbook("other", "x-b")
	for _, sp := range []string{"..", "../other/skills", "skills/../..", filepath.Join(e.root, "other")} {
		e.mustRun(ExitUsage, "add", "x", pb, "-sp", sp)
	}

	// A skills folder, or a skill folder, that leads outside the playbook through a link is not used.
	if err := os.Symlink(filepath.Join(other, "skills"), filepath.Join(pb, "linked")); err != nil {
		t.Skipf("cannot create symbolic links: %v", err)
	}
	_, errOut := e.mustRun(ExitFailure, "add", "x", pb, "-sp", "linked")
	if !strings.Contains(errOut, "outside the playbook folder") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	if err := os.Symlink(filepath.Join(other, "skills", "x-b"), filepath.Join(pb, "skills", "x-b")); err != nil {
		t.Fatal(err)
	}
	e.mustRun(ExitOK, "add", "x", pb)
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-b"))

	// A hand-edited skills path leading outside is a configuration error.
	e.writeSet("default", "playbooks:\n  - name: x\n    folder: "+pb+"\n    artifacts:\n"+
		"      skills:\n        path: ../other/skills\n")
	_, errOut = e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "inside the playbook folder") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestSetNameCase(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents", "-s", "work")
	if !config.SetExists(e.home, "Work") {
		t.Skip("the file system distinguishes case: Work and work are different sets")
	}
	pb := e.playbook("p", "x-a")
	out, _ := e.mustRun(ExitOK, "add", "x", pb, "-s", "Work")
	if strings.Contains(out, "not enabled") {
		t.Fatalf("Work is the enabled set work: %s", out)
	}
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "check", "-s", "Work")
	e.mustRun(ExitFailure, "set", "remove", "Work")
}

func TestFailedAddIsUndone(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the current user cannot write to")
	}
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents,claude")
	pb := e.playbook("p", "x-a")
	locked := filepath.Join(e.home, ".claude", "skills")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)

	_, errOut := e.mustRun(ExitFailure, "add", "x", pb)
	if !strings.Contains(errOut, "undone") || !strings.Contains(errOut, "not registered") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	if n := len(e.config().Playbooks); n != 0 {
		t.Fatalf("%d playbooks registered", n)
	}
}

func TestMessagesUseSetAndHome(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	pb := e.playbook("p", "x-a")
	if err := os.MkdirAll(filepath.Join(e.agentsSkills(), "x-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "add", "x", pb)
	if !strings.Contains(errOut, "'~"+string(filepath.Separator)+".agents") {
		t.Fatalf("conflict should show ~ paths: %s", errOut)
	}

	e.mustRun(ExitOK, "set", "add", "work")
	e.mustRun(ExitOK, "add", "x", pb, "-s", "work")
	_, errOut = e.mustRun(ExitFailure, "add", "x", pb, "-s", "work")
	if !strings.Contains(errOut, "'aiplam update x --set work'") {
		t.Fatalf("hint should name the set: %s", errOut)
	}
	_, errOut = e.mustRun(ExitFailure, "add", "x", pb, "-s", "default")
	if strings.Contains(errOut, "--set") {
		t.Fatalf("hint should not name the enabled set: %s", errOut)
	}
}

func TestBaseFolders(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents,claude")
	project := filepath.Join(e.root, "project")
	module := filepath.Join(project, "module")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	pb := e.playbook("p", "x-a", "y-a")
	other := e.playbook("o", "z-a")

	// Outside any configuration, commands use the home folder's.
	e.app.Cwd = module
	out, _ := e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "Base folder: ~\n") {
		t.Fatalf("unexpected list: %s", out)
	}

	// A project configuration, found from its subfolders.
	out, _ = e.mustRun(ExitOK, "init", project)
	if strings.Contains(out, "git repository") ||
		!strings.Contains(out, "the home folder is also being used as a base folder for playbook management, and "+
			"agents always read") {
		t.Fatalf("unexpected notes: %s", out) // not inside a repository; outside home, so home is a higher level
	}
	if !fsutil.IsDir(filepath.Join(project, ".agents")) || fsutil.IsDir(filepath.Join(project, ".claude")) {
		t.Fatal("init should create the .agents folder only")
	}
	out, _ = e.mustRun(ExitOK, "add", "x", pb)
	if !strings.HasPrefix(out, "Base folder: "+project+"\n") {
		t.Fatalf("unexpected output: %s", out)
	}
	projectSkills := filepath.Join(project, ".agents", "skills")
	e.assertLink(filepath.Join(projectSkills, "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	if f, err := config.LoadSet(project, "default"); err != nil || f.Playbooks[0].Folder != "../p" {
		t.Fatalf("set = %+v (%v)", f, err)
	}

	// --global and --base choose the configuration explicitly.
	e.mustRun(ExitOK, "add", "z", other, "-g")
	e.assertLink(filepath.Join(e.agentsSkills(), "z-a"), filepath.Join(other, "skills", "z-a"))
	e.app.Cwd = e.root
	e.mustRun(ExitOK, "add", "y", pb, "--base", project)
	e.assertLink(filepath.Join(projectSkills, "y-a"), filepath.Join(pb, "skills", "y-a"))
	e.mustRun(ExitUsage, "list", "-b", project, "-g")
	_, errOut := e.mustRun(ExitFailure, "list", "-b", module)
	if !strings.Contains(errOut, "aiplam init") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	// The home folder is a higher level for a project outside it: a skill installed in either cannot be installed in
	// the other.
	e.app.Cwd = module
	_, errOut = e.mustRun(ExitFailure, "add", "z", other)
	if !strings.Contains(errOut, "higher-level base folder") || !strings.Contains(errOut, "--base <basefolder>") ||
		strings.Contains(errOut, "what is in the way") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(projectSkills, "z-a"))
	e.mustRun(ExitOK, "add", "x", pb, "-g") // only checked upwards: the project's links are not seen from home
	_, errOut = e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "agents reading both would see it twice") {
		t.Fatalf("check should report the duplicate: %s", errOut)
	}
	e.mustRun(ExitOK, "remove", "x", "-g")
	e.mustRun(ExitOK, "check")

	// list --all shows every level, nearest first.
	out, _ = e.mustRun(ExitOK, "list", "--all", "--json")
	var levels []listOutput
	if err := json.Unmarshal([]byte(out), &levels); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(levels) != 2 || levels[0].Base != project || levels[1].Base != e.home ||
		strings.Join(levels[1].Types, ",") != "agents,claude" {
		t.Fatalf("unexpected levels: %+v", levels)
	}
	out, _ = e.mustRun(ExitOK, "list", "--all")
	if strings.Count(out, "Base folder: ") != 2 {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitUsage, "list", "--all", "-s", "default")

	// A nested configuration inside a git repository.
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _ = e.mustRun(ExitOK, "init", ".", "-t", "claude")
	if !strings.Contains(out, "git repository") ||
		!strings.Contains(out, "higher-level base folders exist: the home folder") ||
		!strings.Contains(out, "whose agents configuration folders some agents read too") ||
		!strings.Contains(out, "/.claude/skills/<name>-*") ||
		!strings.Contains(out, "'"+filepath.Join(module, ".gitignore")+"'") {
		t.Fatalf("unexpected notes: %s", out)
	}
	// Only agents configuration folders of the same type collide: z is in ~/.claude, x only in the project's .agents.
	_, errOut = e.mustRun(ExitFailure, "add", "z", other)
	if !strings.Contains(errOut, "higher-level base folder") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "add", "x", pb)
	e.assertLink(filepath.Join(module, ".claude", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "check")
}

func TestBaseDiscoveryAndHints(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-t", "agents")
	project := filepath.Join(e.root, "project")
	elsewhere := filepath.Join(e.root, "elsewhere")
	for _, dir := range []string{project, elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pb := e.playbook("p", "x-a")

	// init from another folder: suggested commands name the new base folder.
	e.app.Cwd = elsewhere
	out, _ := e.mustRun(ExitOK, "init", project)
	if !strings.Contains(out, "'aiplam add <name> <playbookfolder> --base "+project+"'") {
		t.Fatalf("hint should name the base folder: %s", out)
	}
	_, errOut := e.mustRun(ExitFailure, "init", project)
	if !strings.Contains(errOut, "'aiplam type --base "+project+"'") {
		t.Fatalf("hint should name the base folder: %s", errOut)
	}
	out, _ = e.mustRun(ExitOK, "set", "add", "work", "-b", project)
	if !strings.Contains(out, "'aiplam set enable work --base "+project+"'") {
		t.Fatalf("hint should name the base folder: %s", out)
	}

	// From inside a linked skill, the base folder is the one above the link, not the playbook's.
	e.app.Cwd = project
	e.mustRun(ExitOK, "add", "x", pb)
	e.app.Cwd = filepath.Join(project, ".agents", "skills", "x-a")
	out, _ = e.mustRun(ExitOK, "list")
	if !strings.HasPrefix(out, "Base folder: "+project+"\n") {
		t.Fatalf("unexpected list: %s", out)
	}

	// A .aiplam folder makes a base folder, even without its configuration.
	empty := filepath.Join(e.root, "empty")
	if err := os.MkdirAll(filepath.Join(empty, ".aiplam"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = empty
	_, errOut = e.mustRun(ExitFailure, "list")
	if !strings.Contains(errOut, "aiplam init") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "init", ".")

	// list --all reports a higher level it cannot read and lists the others.
	nested := filepath.Join(project, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = nested
	e.mustRun(ExitOK, "init", ".")
	if err := os.WriteFile(config.InstallFile(project), []byte("types: [nope]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut = e.mustRun(ExitFailure, "list", "--all")
	if !strings.Contains(out, "Base folder: "+nested) || !strings.Contains(out, "Base folder: ~") ||
		!strings.Contains(errOut, config.InstallName+".yaml") {
		t.Fatalf("unexpected list: %s / %s", out, errOut)
	}
}
