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
	"runtime/debug"
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

// skillItems returns the skills of a listed playbook.
func skillItems(p listPlaybook) []listItem {
	for _, k := range p.Artifacts {
		if k.Kind == "skills" {
			return k.Items
		}
	}
	return nil
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

// config returns the first enabled profile.
func (e *env) config() *config.Profile {
	e.t.Helper()
	prof, err := config.LoadProfile(e.home, e.baseConfig().Enabled[0])
	if err != nil {
		e.t.Fatal(err)
	}
	return prof
}

func (e *env) baseConfig() *config.BaseConfig {
	e.t.Helper()
	conf, err := config.LoadBaseConfig(e.home)
	if err != nil {
		e.t.Fatal(err)
	}
	return conf
}

// writeProfile replaces the content of a profile's file.
func (e *env) writeProfile(profile, content string) {
	e.t.Helper()
	if err := os.WriteFile(config.ProfileFile(e.home, profile), []byte(content), 0o644); err != nil {
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

func TestOneCommandAtATime(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g")
	dir := config.Dir(e.home)
	if ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err != nil ||
		!strings.Contains(string(ignore), "/.lock\n") {
		t.Fatalf(".aiplam/.gitignore: %q, %v", ignore, err)
	}

	// While another command holds the lock, commands that change the base folder fail; list and check still run.
	unlock, err := fsutil.Lock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	pb := e.playbook("p", "x-a")
	for _, args := range [][]string{{"add", "x", pb}, {"update", "--all"}, {"profile", "add", "other"}} {
		if _, errOut := e.mustRun(ExitFailure, args...); !strings.Contains(errOut, "another aiplam command") {
			t.Errorf("%v: %q", args, errOut)
		}
	}
	e.mustRun(ExitOK, "list")
	e.mustRun(ExitOK, "check")
	unlock()
	e.mustRun(ExitOK, "add", "x", pb)
}

func TestInit(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitUsage, "init")                  // a folder (or --global) is required
	e.mustRun(ExitUsage, "init", ".", "-g")       // not both
	e.mustRun(ExitUsage, "init", "-b", e.home)    // the folder is an argument
	e.mustRun(ExitUsage, "init", "-g", "-a", "x") // unknown agent
	e.mustRun(ExitFailure, "init", filepath.Join(e.root, "missing"))
	e.mustRun(ExitOK, "init", "-g") // standard by default
	if fsutil.IsDir(filepath.Join(e.home, ".agents")) {
		t.Fatal("~/.agents created before anything is linked")
	}
	if conf := e.baseConfig(); strings.Join(conf.Enabled, ",") != "default" {
		t.Fatalf("base config = %+v", conf)
	}
	if prof := e.config(); len(prof.Playbooks) != 0 || prof.Agent != "standard" {
		t.Fatalf("profile = %+v", prof)
	}
	_, errOut := e.mustRun(ExitFailure, "init", "~", "--agent", "claude")
	if !strings.Contains(errOut, "is already set up as a base folder") || !strings.Contains(errOut, "aiplam profile") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	e2 := newEnv(t)
	out, _ := e2.mustRun(ExitOK, "init", "-g", "--agent", "claude", "-p", "work")
	if conf := e2.baseConfig(); strings.Join(conf.Enabled, ",") != "work" {
		t.Fatalf("base config = %+v", conf)
	}
	if !strings.Contains(out, "agent claude") || !strings.Contains(out, "~/.claude/skills") {
		t.Fatalf("unexpected output: %s", out)
	}
	if prof, err := config.LoadProfile(e2.home, "work"); err != nil || prof.Agent != "claude" {
		t.Fatalf("profile = %+v (%v)", prof, err)
	}
	if config.ProfileExists(e2.home, "default") {
		t.Fatal("the default profile should not be created")
	}
	e2.mustRun(ExitUsage, "init", "-g", "-a", "standard", "-p", config.BaseConfigName)
}

func TestAddLinksPrefixedSkillsOnly(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g")
	e.mustRun(ExitOK, "profile", "add", "claude", "--agent", "claude")
	e.mustRun(ExitOK, "profile", "enable", "claude")
	pb := e.playbook("ws/arxila-ai-playbook", "arxila-one", "arxila-two", "other-skill")
	if err := os.MkdirAll(filepath.Join(pb, "skills", "arxila-noskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = pb
	e.mustRun(ExitOK, "add", "arxila", ".", "-p", "default")
	e.mustRun(ExitOK, "add", "arxila", ".", "-p", "claude")

	for _, folder := range []string{".agents", ".claude"} {
		dir := filepath.Join(e.home, folder, "skills")
		e.assertLink(filepath.Join(dir, "arxila-one"), filepath.Join(pb, "skills", "arxila-one"))
		e.assertLink(filepath.Join(dir, "arxila-two"), filepath.Join(pb, "skills", "arxila-two"))
		e.assertMissing(filepath.Join(dir, "other-skill"))
		e.assertMissing(filepath.Join(dir, "arxila-noskill"))
	}
	prof := e.config()
	if len(prof.Playbooks) != 1 || prof.Playbooks[0].Name != "arxila" || prof.Playbooks[0].Dir(e.home) != pb ||
		prof.Playbooks[0].Artifacts.Skills.Path != "" { // the default is not stored
		t.Fatalf("unexpected config: %+v", prof.Playbooks)
	}

	_, errOut := e.mustRun(ExitFailure, "add", "arxila", pb, "-p", "default")
	if !strings.Contains(errOut, "already registered") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestAddSkillsPathRoot(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	for _, p := range list.Profiles[0].Playbooks {
		for _, s := range skillItems(p) {
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	copyDir := e.skill(e.agentsSkills(), "x-copy")
	e.mustRun(ExitOK, "remove", "x")
	if !fsutil.IsDir(copyDir) {
		t.Fatal("a real folder was removed")
	}
	_, errOut := e.mustRun(ExitFailure, "remove", "x")
	if !strings.Contains(errOut, "no playbook of profile 'default' is registered") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestCheck(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	for _, content := range []string{
		"playbooks: []\nunknown: 1\n",
		"targets: [~/.agents]\n",
		"playbooks:\n  - name: x\n    folder: /absolute\n    artifacts:\n      skills:\n        path: skills\n",
		"playbooks:\n  - name: x\n    folder: ~/p\n    artifacts:\n      skills:\n        path: skills\n",
		"playbooks:\n  - name: 'x@y'\n    folder: relative\n    artifacts:\n      skills:\n        path: skills\n",
		"playbooks: [\n",
	} {
		e.writeProfile("default", content)
		e.mustRun(ExitFailure, "check")
		_, errOut := e.mustRun(ExitFailure, "list")
		if !strings.Contains(errOut, "default.yaml") {
			t.Fatalf("list should point to the profile's file for %q: %s", content, errOut)
		}
	}
	e.writeProfile("default", "")
	e.mustRun(ExitOK, "check") // an empty file is an empty profile (for the standard agent)
	e.writeProfile("default", "agent: nope\nplaybooks: []\n")
	_, errOut := e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "unknown agent 'nope'") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.writeProfile("default", "")

	file := config.BaseConfigFile(e.home)
	for _, content := range []string{
		"types: [agents]\nenabled: [default]\n",
		"targets: [~/.agents]\nenabled: [default]\n",
		"enabled: [default]\nplaybooks: []\n",
		"enabled: ['../x']\n",
		"enabled: [default, Default]\n",
		"enabled: default\n",
		"",
	} {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		e.mustRun(ExitFailure, "check")
		_, errOut := e.mustRun(ExitFailure, "list")
		if !strings.Contains(errOut, config.BaseConfigName+".yaml") {
			t.Fatalf("list should point to the base config file for %q: %s", content, errOut)
		}
	}
}

func TestListJSONAndVersions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	if len(list.Profiles) != 1 || len(list.Profiles[0].Playbooks) != 1 || !list.Profiles[0].Enabled {
		t.Fatalf("unexpected list: %+v", list)
	}
	p := list.Profiles[0].Playbooks[0]
	if p.ID != "x" || p.Name != "x" || p.Folder != pb || p.Label != "branch: main" || !p.Version.Known ||
		len(skillItems(p)) != 1 || !skillItems(p)[0].Linked {
		t.Fatalf("unexpected playbook: %+v", p)
	}

	git("tag", "v1.0.0")
	git("tag", "x-1.0.0")
	git("checkout", "-q", "--detach", "v1.0.0")
	if got := readList().Profiles[0].Playbooks[0].Label; got != "tags: v1.0.0, x-1.0.0" {
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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

func TestProfiles(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	pbA := e.playbook("pa", "x-a")
	pbB := e.playbook("pb", "y-b")
	pbC := e.playbook("pc", "z-c")
	e.mustRun(ExitOK, "add", "x", pbA)
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pbA, "skills", "x-a"))

	// --profile needs an existing profile.
	_, errOut := e.mustRun(ExitFailure, "list", "--profile", "work")
	if !strings.Contains(errOut, "profile 'work' does not exist") || !strings.Contains(errOut, "aiplam profile add work") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "profile", "add", "work")
	e.mustRun(ExitFailure, "profile", "add", "work")
	e.mustRun(ExitUsage, "profile", "add", config.BaseConfigName)

	// A profile that is not enabled is only recorded: nothing is linked or unlinked.
	out, _ := e.mustRun(ExitOK, "add", "y", pbB, "-p", "work")
	if !strings.Contains(out, "not enabled") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-b"))
	out, _ = e.mustRun(ExitOK, "update", "--all", "-p", "work")
	if !strings.Contains(out, "Nothing to update") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.mustRun(ExitOK, "check", "-p", "work")
	var list listOutput
	out, _ = e.mustRun(ExitOK, "list", "--json", "--profile=work")
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Profiles) != 1 ||
		list.Profiles[0].Name != "work" ||
		list.Profiles[0].Enabled || skillItems(list.Profiles[0].Playbooks[0])[0].Linked {
		t.Fatalf("unexpected list: %v %s", err, out)
	}

	// Enabling a profile adds it to the enabled ones.
	e.mustRun(ExitOK, "profile", "enable", "work")
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pbA, "skills", "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-b"), filepath.Join(pbB, "skills", "y-b"))
	if got := strings.Join(e.baseConfig().Enabled, ","); got != "default,work" {
		t.Fatalf("enabled = %s", got)
	}
	out, _ = e.mustRun(ExitOK, "profile", "list")
	if !strings.Contains(out, "* default") || !strings.Contains(out, "* work") {
		t.Fatalf("unexpected profiles: %s", out)
	}
	out, _ = e.mustRun(ExitOK, "profile", "enable", "work")
	if !strings.Contains(out, "already enabled") {
		t.Fatalf("unexpected output: %s", out)
	}

	// With several profiles enabled: list and check cover them all, add needs --profile, and selectors look in all of
	// them.
	out, _ = e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "Profile default") || !strings.Contains(out, "Profile work") {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitOK, "check")
	_, errOut = e.mustRun(ExitFailure, "add", "z", pbC)
	if !strings.Contains(errOut, "--profile") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "update", "y")
	e.mustRun(ExitOK, "update", "--all")

	// Profiles enabled together cannot provide the same skill.
	_, errOut = e.mustRun(ExitFailure, "add", "y", pbB, "-p", "default")
	if !strings.Contains(errOut, "also provided by playbook y of profile work") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	// Disabling a profile unlinks its playbooks only.
	e.mustRun(ExitOK, "profile", "disable", "default")
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-b"), filepath.Join(pbB, "skills", "y-b"))
	e.mustRun(ExitFailure, "profile", "disable", "default") // not enabled
	e.mustRun(ExitOK, "remove", "y")                        // the only enabled profile
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-b"))
	e.mustRun(ExitOK, "add", "y", pbB)

	// The enabled profiles cannot be removed; others can, unlinking nothing.
	_, errOut = e.mustRun(ExitFailure, "profile", "remove", "work")
	if !strings.Contains(errOut, "aiplam profile disable work") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "profile", "remove", "default")
	e.mustRun(ExitFailure, "list", "-p", "default")
	e.mustRun(ExitFailure, "profile", "enable", "default")

	// No profile enabled.
	e.mustRun(ExitOK, "profile", "disable", "work")
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-b"))
	out, _ = e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "No profile is enabled") {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitOK, "check")
	_, errOut = e.mustRun(ExitFailure, "add", "z", pbC)
	if !strings.Contains(errOut, "no profile is enabled") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	e.mustRun(ExitUsage, "profile")
	e.mustRun(ExitUsage, "profile", "frob")
	e.mustRun(ExitUsage, "profile", "enable")
	e.mustRun(ExitUsage, "profile", "enable", "a,,b")
	e.mustRun(ExitUsage, "profile", "list", "-p", "work")
	e.mustRun(ExitUsage, "list", "--profile")
	e.mustRun(ExitUsage, "list", "--profile", "../x")
	e.mustRun(ExitUsage, "list", "-p", "a.b")
}

func TestProfileEnableCollisions(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	pb := e.playbook("p", "x-a", "x-b")
	other := e.playbook("q", "y-a")
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "profile", "add", "work")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "work")
	e.mustRun(ExitOK, "profile", "add", "more")
	e.mustRun(ExitOK, "add", "y", other, "-p", "more")

	// Enabling a profile whose links already exist (from an enabled profile) fails, changing nothing.
	_, errOut := e.mustRun(ExitFailure, "profile", "enable", "work,more")
	if !strings.Contains(errOut, "collide") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(e.agentsSkills(), "y-a"))
	if got := strings.Join(e.baseConfig().Enabled, ","); got != "default" {
		t.Fatalf("enabled = %s", got)
	}

	// Also when the link exists for any other reason, even if it is right.
	e.mustRun(ExitOK, "profile", "disable", "default")
	if err := os.MkdirAll(e.agentsSkills(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.CreateLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a")); err != nil {
		t.Fatal(err)
	}
	_, errOut = e.mustRun(ExitFailure, "profile", "enable", "work")
	if !strings.Contains(errOut, "already exists") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	if err := fsutil.RemoveLink(filepath.Join(e.agentsSkills(), "x-a")); err != nil {
		t.Fatal(err)
	}

	// Two profiles being enabled together cannot collide either.
	_, errOut = e.mustRun(ExitFailure, "profile", "enable", "default", "work")
	if !strings.Contains(errOut, "collide") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "profile", "enable", "work,more")
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "x-b"), filepath.Join(pb, "skills", "x-b"))
}

func TestProfileSwitch(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	pb := e.playbook("p", "x-a", "x-b")
	other := e.playbook("q", "y-a")
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "profile", "add", "work")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "work")
	e.mustRun(ExitOK, "profile", "add", "more")
	e.mustRun(ExitOK, "add", "y", other, "-p", "more")

	// Switching replaces the enabled profiles; links wanted before and after are left alone.
	out, _ := e.mustRun(ExitOK, "profile", "switch", "work,more")
	if strings.Contains(out, "x-a") {
		t.Fatalf("links wanted by both profiles should be left alone: %s", out)
	}
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	if got := strings.Join(e.baseConfig().Enabled, ","); got != "work,more" {
		t.Fatalf("enabled = %s", got)
	}
	e.mustRun(ExitOK, "profile", "switch", "more")
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	out, _ = e.mustRun(ExitOK, "profile", "switch", "more")
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.mustRun(ExitFailure, "profile", "switch", "default,work") // they collide
	e.assertLink(filepath.Join(e.agentsSkills(), "y-a"), filepath.Join(other, "skills", "y-a"))
	e.mustRun(ExitFailure, "profile", "switch", "nope")
}

func TestProfileAgent(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	out, _ := e.mustRun(ExitOK, "profile", "list")
	if !strings.Contains(out, "* default") || !strings.Contains(out, "standard") {
		t.Fatalf("unexpected list: %q", out)
	}

	// Changing the agent moves the links, and removes the folders left empty.
	claude := filepath.Join(e.home, ".claude", "skills")
	out, _ = e.mustRun(ExitOK, "profile", "agent", "default", "claude")
	if !strings.Contains(out, "x-a (agent changed)") || !strings.Contains(out, "empty folder removed") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertLink(filepath.Join(claude, "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(e.home, ".agents"))
	if prof := e.config(); prof.Agent != "claude" {
		t.Fatalf("agent = %s", prof.Agent)
	}
	out, _ = e.mustRun(ExitOK, "profile", "agent", "default", "claude")
	if !strings.Contains(out, "already has agent claude") {
		t.Fatalf("unexpected output: %s", out)
	}

	// Folders holding anything else are kept.
	e.skill(claude, "mine")
	e.mustRun(ExitOK, "profile", "agent", "default", "standard")
	e.assertMissing(filepath.Join(claude, "x-a"))
	if !fsutil.IsDir(filepath.Join(claude, "mine")) {
		t.Fatal("an artifact added by hand was removed")
	}

	// codex reads skills from the same folder as standard: the links stay.
	out, _ = e.mustRun(ExitOK, "profile", "agent", "default", "codex")
	if strings.Contains(out, "x-a") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))

	// A conflict in a new folder cancels everything.
	if err := os.MkdirAll(filepath.Join(claude, "x-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "profile", "agent", "default", "claude")
	if !strings.Contains(errOut, "not changed") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))

	// A profile that is not enabled only changes its file.
	e.mustRun(ExitOK, "profile", "add", "other")
	out, _ = e.mustRun(ExitOK, "profile", "agent", "other", "junie")
	if !strings.Contains(out, "not enabled") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.mustRun(ExitUsage, "profile", "agent", "default")
	e.mustRun(ExitUsage, "profile", "agent", "default", "nope")
	e.mustRun(ExitUsage, "profile", "enable", "other", "--agent", "claude")
	e.mustRun(ExitUsage, "profile", "add", "third", "--agent", "nope")
	e.mustRun(ExitFailure, "profile", "agent", "missing", "claude")
}

func TestArtifactKindsPerAgent(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	files := map[string]string{
		"agents/x-rev.md": "", "agents/x-rev.agent.md": "", "agents/x-rev.toml": "", "agents/other.md": "",
		"rules/x-style.md": "", "rules/x-style.mdc": "", "commands/x-go.md": "", "commands/x-go.toml": "",
		"prompts/x-ask.prompt.md": "", "instructions/x-java.instructions.md": "",
	}
	for name := range files {
		path := filepath.Join(pb, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := e.mustRun(ExitOK, "add", "x", pb)
	if !strings.Contains(out, "1 skill(s), 1 subagent(s), 1 command(s), 1 rule(s)") {
		t.Fatalf("unexpected output: %s", out)
	}
	claude := filepath.Join(e.home, ".claude")
	e.assertLink(filepath.Join(claude, "agents", "x-rev.md"), filepath.Join(pb, "agents", "x-rev.md"))
	e.assertLink(filepath.Join(claude, "rules", "x-style.md"), filepath.Join(pb, "rules", "x-style.md"))
	e.assertLink(filepath.Join(claude, "commands", "x-go.md"), filepath.Join(pb, "commands", "x-go.md"))
	e.assertMissing(filepath.Join(claude, "agents", "x-rev.agent.md"))
	e.assertMissing(filepath.Join(claude, "agents", "other.md"))
	e.assertMissing(filepath.Join(claude, "prompts"))

	// Each agent links the files in its own format, from its own folders (in the home folder here).
	e.mustRun(ExitOK, "profile", "agent", "default", "copilot")
	e.assertMissing(claude)
	copilot := filepath.Join(e.home, ".copilot")
	e.assertLink(filepath.Join(copilot, "agents", "x-rev.agent.md"), filepath.Join(pb, "agents", "x-rev.agent.md"))
	e.assertLink(filepath.Join(copilot, "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(copilot, "prompts")) // project-only for Copilot
	e.mustRun(ExitOK, "profile", "agent", "default", "codex")
	e.assertLink(filepath.Join(e.home, ".codex", "agents", "x-rev.toml"), filepath.Join(pb, "agents", "x-rev.toml"))
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "check")

	// In a project, Copilot reads .github, where prompts and instructions are linked too.
	project := filepath.Join(e.root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = project
	e.mustRun(ExitOK, "init", ".", "-a", "copilot")
	e.mustRun(ExitOK, "add", "x", pb)
	github := filepath.Join(project, ".github")
	e.assertLink(filepath.Join(github, "prompts", "x-ask.prompt.md"), filepath.Join(pb, "prompts", "x-ask.prompt.md"))
	e.assertLink(filepath.Join(github, "instructions", "x-java.instructions.md"),
		filepath.Join(pb, "instructions", "x-java.instructions.md"))
	e.mustRun(ExitOK, "remove", "x")
	e.assertMissing(github)
}

func TestDefaultFoldersFollowTheAgent(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	write := func(rel string) {
		path := filepath.Join(pb, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("subagents/x-rev.md")

	// Claude Code reads subagents from .claude/agents, so by default they are looked for in the playbook's agents/.
	out, _ := e.mustRun(ExitOK, "add", "x", pb)
	if strings.Contains(out, "subagent") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertMissing(filepath.Join(e.home, ".claude", "agents"))
	e.mustRun(ExitOK, "remove", "x")

	// With --subagents-path, its subagents/ folder is used, and kept in the profile.
	out, _ = e.mustRun(ExitOK, "add", "x", pb, "-sap", "subagents")
	if !strings.Contains(out, "1 subagent(s)") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertLink(filepath.Join(e.home, ".claude", "agents", "x-rev.md"), filepath.Join(pb, "subagents", "x-rev.md"))
	if prof := e.config(); prof.Playbooks[0].Artifacts.Subagents.Path != "subagents" {
		t.Fatalf("unexpected config: %+v", prof.Playbooks)
	}

	// A playbook without any folder its profile's agent reads cannot be added.
	rulesOnly := filepath.Join(e.root, "rules-only")
	if err := os.MkdirAll(filepath.Join(rulesOnly, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.mustRun(ExitOK, "profile", "add", "std")
	_, errOut := e.mustRun(ExitFailure, "add", "y", rulesOnly, "-p", "std")
	if !strings.Contains(errOut, "agent standard reads (skills/)") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "add", "y", rulesOnly) // Claude Code reads rules
	e.mustRun(ExitFailure, "add", "z", pb, "-p", "std", "-cp", "missing")
}

func TestDoubleLoadWarning(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "profile", "add", "claude", "-a", "claude")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "claude")
	_, errOut := e.mustRun(ExitOK, "profile", "enable", "claude")
	if strings.Contains(errOut, "loaded twice") {
		t.Fatalf("claude does not read .agents: %s", errOut)
	}
	_, errOut = e.mustRun(ExitOK, "profile", "agent", "claude", "cursor")
	if !strings.Contains(errOut, "skill x-a is linked for agent cursor (profile claude)") ||
		!strings.Contains(errOut, "loaded twice") {
		t.Fatalf("want a warning: %s", errOut)
	}
	_, errOut = e.mustRun(ExitOK, "check")
	if !strings.Contains(errOut, "loaded twice") {
		t.Fatalf("want a warning: %s", errOut)
	}
	// Profiles whose agents share a folder collide instead.
	e.mustRun(ExitOK, "profile", "disable", "claude")
	e.mustRun(ExitOK, "profile", "agent", "claude", "codex")
	e.mustRun(ExitFailure, "profile", "enable", "claude")
}

func TestShortOptions(t *testing.T) {
	e := newEnv(t)
	claude := filepath.Join(e.home, ".claude")
	e.mustRun(ExitOK, "init", "-g", "-a", "standard", "-a=claude", "-p", "main")
	if conf := e.baseConfig(); strings.Join(conf.Enabled, ",") != "main" {
		t.Fatalf("base config = %+v", conf)
	}
	e.mustRun(ExitOK, "profile", "add", "work", "-a", "claude")

	pb := filepath.Join(e.root, "flat")
	e.skill(pb, "x-a")
	e.mustRun(ExitOK, "add", "x", pb, "-sp", ".")
	e.assertLink(filepath.Join(claude, "skills", "x-a"), filepath.Join(pb, "x-a"))
	e.mustRun(ExitOK, "add", "x", pb, "-sp=.", "-p", "work")
	e.mustRun(ExitOK, "update", "--all", "-p", "work")
	e.mustRun(ExitOK, "list", "-p", "work")
	e.mustRun(ExitOK, "check", "-p", "work")
	e.mustRun(ExitOK, "remove", "x", "-p", "work")

	_, errOut := e.mustRun(ExitUsage, "add", "y", pb, "-x")
	if !strings.Contains(errOut, "unknown option: -x") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitUsage, "list", "-a", "standard") // -a is an option of init and profile add only
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

func TestResolveVersion(t *testing.T) {
	info := func(version string, ok bool) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			if !ok {
				return nil, false
			}
			return &debug.BuildInfo{Main: debug.Module{Version: version}}, true
		}
	}
	for _, c := range []struct {
		set  string
		info func() (*debug.BuildInfo, bool)
		want string
	}{
		{"9.9.9", info("v1.2.3", true), "9.9.9"},   // set with -ldflags -X
		{"", info("v1.2.3", true), "1.2.3"},        // go install ...@v1.2.3
		{"", info("(devel)", true), sourceVersion}, // go build without version control information
		{"", info("", true), sourceVersion},        // go test
		{"", info("", false), sourceVersion},       // no build information
	} {
		if got := resolveVersion(c.set, c.info); got != c.want {
			t.Errorf("resolveVersion(%q, ...) = %q, want %q", c.set, got, c.want)
		}
	}
}

func TestPlaybookBehindSymlink(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	inside := filepath.Join(e.home, "playbooks", "p")
	e.skill(filepath.Join(inside, "skills"), "x-a")
	outside := e.playbook("q", "y-a")
	e.mustRun(ExitOK, "add", "x", inside)
	e.mustRun(ExitOK, "add", "y", outside)
	prof := e.config()
	if prof.Playbooks[0].Folder != "playbooks/p" || prof.Playbooks[1].Folder != "../q" {
		t.Fatalf("folders = %q, %q", prof.Playbooks[0].Folder, prof.Playbooks[1].Folder)
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	pb := e.playbook("p", "x-a")
	other := e.playbook("other", "x-b")
	for _, sp := range []string{"..", "../other/skills", "skills/../..", filepath.Join(e.root, "other")} {
		e.mustRun(ExitUsage, "add", "x", pb, "-sp", sp)
	}

	// An artifact folder (skills), or a skill folder, that leads outside the playbook through a link is not used.
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
	e.writeProfile("default", "playbooks:\n  - name: x\n    folder: "+pb+"\n    artifacts:\n"+
		"      skills:\n        path: ../other/skills\n")
	_, errOut = e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "inside the playbook folder") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestProfileNameCase(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard", "-p", "work")
	if !config.ProfileExists(e.home, "Work") {
		t.Skip("the file system distinguishes case: Work and work are different profiles")
	}
	pb := e.playbook("p", "x-a")
	out, _ := e.mustRun(ExitOK, "add", "x", pb, "-p", "Work")
	if strings.Contains(out, "not enabled") {
		t.Fatalf("Work is the enabled profile work: %s", out)
	}
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "check", "-p", "Work")
	e.mustRun(ExitFailure, "profile", "remove", "Work")
}

func TestFailedAddIsUndone(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the current user cannot write to")
	}
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	if err := os.MkdirAll(filepath.Join(pb, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pb, "agents", "x-r.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(e.home, ".claude", "agents")
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
	e.assertMissing(filepath.Join(e.home, ".claude", "skills", "x-a"))
	if n := len(e.config().Playbooks); n != 0 {
		t.Fatalf("%d playbooks registered", n)
	}
}

func TestMessagesUseProfileAndHome(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
	pb := e.playbook("p", "x-a")
	if err := os.MkdirAll(filepath.Join(e.agentsSkills(), "x-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "add", "x", pb)
	if !strings.Contains(errOut, "'~"+string(filepath.Separator)+".agents") {
		t.Fatalf("conflict should show ~ paths: %s", errOut)
	}

	e.mustRun(ExitOK, "profile", "add", "work")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "work")
	_, errOut = e.mustRun(ExitFailure, "add", "x", pb, "-p", "work")
	if !strings.Contains(errOut, "'aiplam update x --profile work'") {
		t.Fatalf("hint should name the profile: %s", errOut)
	}
	_, errOut = e.mustRun(ExitFailure, "add", "x", pb, "-p", "default")
	if strings.Contains(errOut, "--profile") {
		t.Fatalf("hint should not name the enabled profile: %s", errOut)
	}
}

func TestBaseFolders(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g")
	e.mustRun(ExitOK, "profile", "add", "claude", "-a", "claude")
	e.mustRun(ExitOK, "profile", "enable", "claude")
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
		t.Fatalf("unexpected notes: %s", out) // not inside a repository; outside home, so home is a higher-level base folder
	}
	if fsutil.IsDir(filepath.Join(project, ".agents")) || fsutil.IsDir(filepath.Join(project, ".claude")) {
		t.Fatal("init should not create agents configuration folders")
	}
	out, _ = e.mustRun(ExitOK, "add", "x", pb)
	if !strings.HasPrefix(out, "Base folder: "+project+"\n") {
		t.Fatalf("unexpected output: %s", out)
	}
	projectSkills := filepath.Join(project, ".agents", "skills")
	e.assertLink(filepath.Join(projectSkills, "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(e.agentsSkills(), "x-a"))
	if f, err := config.LoadProfile(project, "default"); err != nil || f.Playbooks[0].Folder != "../p" {
		t.Fatalf("profile = %+v (%v)", f, err)
	}

	// --global and --base choose the configuration explicitly.
	e.mustRun(ExitOK, "add", "z", other, "-g", "-p", "default")
	e.mustRun(ExitOK, "add", "z", other, "-g", "-p", "claude")
	e.assertLink(filepath.Join(e.agentsSkills(), "z-a"), filepath.Join(other, "skills", "z-a"))
	e.app.Cwd = e.root
	e.mustRun(ExitOK, "add", "y", pb, "--base", project)
	e.assertLink(filepath.Join(projectSkills, "y-a"), filepath.Join(pb, "skills", "y-a"))
	e.mustRun(ExitUsage, "list", "-b", project, "-g")
	_, errOut := e.mustRun(ExitFailure, "list", "-b", module)
	if !strings.Contains(errOut, "aiplam init") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	// The home folder is a higher-level base folder for a project outside it: a skill linked from either cannot be
	// linked from the other.
	e.app.Cwd = module
	_, errOut = e.mustRun(ExitFailure, "add", "z", other)
	if !strings.Contains(errOut, "higher-level base folder") || !strings.Contains(errOut, "--base <basefolder>") ||
		strings.Contains(errOut, "what is in the way") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.assertMissing(filepath.Join(projectSkills, "z-a"))
	// Only checked upwards: the project's links are not seen from home.
	e.mustRun(ExitOK, "add", "x", pb, "-g", "-p", "default")
	_, errOut = e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "agents reading both would see it twice") {
		t.Fatalf("check should report the duplicate: %s", errOut)
	}
	e.mustRun(ExitOK, "remove", "x", "-g", "-p", "default")
	e.mustRun(ExitOK, "check")

	// list --all lists every base folder, nearest first.
	out, _ = e.mustRun(ExitOK, "list", "--all", "--json")
	var levels []listOutput
	if err := json.Unmarshal([]byte(out), &levels); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(levels) != 2 || levels[0].Base != project || levels[1].Base != e.home ||
		len(levels[1].Profiles) != 2 || levels[1].Profiles[1].Agent != "claude" {
		t.Fatalf("unexpected levels: %+v", levels)
	}
	out, _ = e.mustRun(ExitOK, "list", "--all")
	if strings.Count(out, "Base folder: ") != 2 {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitUsage, "list", "--all", "-p", "default")

	// A nested configuration inside a git repository.
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _ = e.mustRun(ExitOK, "init", ".", "-a", "claude")
	if !strings.Contains(out, "git repository") ||
		!strings.Contains(out, "higher-level base folders exist: the home folder") ||
		!strings.Contains(out, "whose agents configuration folders some agents read too") ||
		!strings.Contains(out, "/.claude/skills/<name>-*") ||
		!strings.Contains(out, "'"+filepath.Join(module, ".gitignore")+"'") {
		t.Fatalf("unexpected notes: %s", out)
	}
	// Only artifacts linked for the same agent collide: z is in ~/.claude, x only in the project's .agents.
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
	e.mustRun(ExitOK, "init", "-g", "-a", "standard")
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
	if !strings.Contains(errOut, "'aiplam profile --base "+project+"'") {
		t.Fatalf("hint should name the base folder: %s", errOut)
	}
	out, _ = e.mustRun(ExitOK, "profile", "add", "work", "-b", project)
	if !strings.Contains(out, "'aiplam profile enable work --base "+project+"'") {
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

	// list --all reports a higher-level base folder it cannot read and lists the others.
	nested := filepath.Join(project, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = nested
	e.mustRun(ExitOK, "init", ".")
	if err := os.WriteFile(config.BaseConfigFile(project), []byte("types: [nope]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut = e.mustRun(ExitFailure, "list", "--all")
	if !strings.Contains(out, "Base folder: "+nested) || !strings.Contains(out, "Base folder: ~") ||
		!strings.Contains(errOut, config.BaseConfigName+".yaml") {
		t.Fatalf("unexpected list: %s / %s", out, errOut)
	}
}

// file creates a file (and its folders) at a path relative to dir.
func (e *env) file(dir, rel string) string {
	e.t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func TestUpdateFileArtifacts(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	sub := e.file(pb, "agents/x-r.md")
	e.mustRun(ExitOK, "add", "x", pb)
	claude := filepath.Join(e.home, ".claude")
	e.assertLink(filepath.Join(claude, "agents", "x-r.md"), sub)

	// Another version of the playbook: the subagent is gone, a command is new.
	if err := os.Remove(sub); err != nil {
		t.Fatal(err)
	}
	cmd := e.file(pb, "commands/x-go.md")
	_, errOut := e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "would remove") || !strings.Contains(errOut, "would link") {
		t.Fatalf("check should report the changes: %s", errOut)
	}
	out, _ := e.mustRun(ExitOK, "update", "x")
	if !strings.Contains(out, "x-r.md (broken link: its artifact no longer exists)") ||
		!strings.Contains(out, "agents (empty folder removed)") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertMissing(filepath.Join(claude, "agents"))
	e.assertLink(filepath.Join(claude, "commands", "x-go.md"), cmd)
	e.mustRun(ExitOK, "check")
	out, _ = e.mustRun(ExitOK, "update", "--all")
	if !strings.Contains(out, "up to date") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCheckReportsBrokenFileLinks(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	stray := filepath.Join(e.home, ".claude", "skills", "old-skill")
	if err := fsutil.CreateLink(stray, filepath.Join(e.root, "gone")); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "check")
	if !strings.Contains(errOut, "broken link") || !strings.Contains(errOut, "old-skill") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestHomeAndProjectFoldersDiffer(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "copilot")
	pb := e.playbook("p", "x-a")
	e.file(pb, "prompts/x-ask.prompt.md")
	out, _ := e.mustRun(ExitOK, "add", "x", pb)
	if strings.Contains(out, "prompt") {
		t.Fatalf("Copilot reads prompts in projects only: %s", out)
	}
	e.assertLink(filepath.Join(e.home, ".copilot", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(e.home, ".github"))

	// In a project, Copilot reads .github: the skill linked from ~/.copilot is refused there.
	project := filepath.Join(e.home, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = project
	e.mustRun(ExitOK, "init", ".", "-a", "copilot")
	_, errOut := e.mustRun(ExitFailure, "add", "x", pb)
	if !strings.Contains(errOut, "'~"+string(filepath.Separator)+filepath.Join(".copilot", "skills", "x-a")+"'") ||
		!strings.Contains(errOut, "agent copilot") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "remove", "x", "-g")
	out, _ = e.mustRun(ExitOK, "add", "x", pb)
	if !strings.Contains(out, "1 skill(s), 1 prompt(s)") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertLink(filepath.Join(project, ".github", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(e.home, ".copilot")) // removed once empty
}

func TestHomeConfigFolders(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "opencode")
	pb := e.playbook("p", "x-a")
	e.file(pb, "rules/x-style.md")
	e.mustRun(ExitOK, "add", "x", pb)
	e.assertLink(filepath.Join(e.home, ".config", "opencode", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))

	// Devin keeps rules in ~/.devin but everything else in ~/.config/devin.
	e.mustRun(ExitOK, "profile", "agent", "default", "devin")
	e.assertMissing(filepath.Join(e.home, ".config", "opencode"))
	if !fsutil.IsDir(filepath.Join(e.home, ".config")) {
		t.Fatal("~/.config is not an agents configuration folder and must stay")
	}
	e.assertLink(filepath.Join(e.home, ".config", "devin", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertLink(filepath.Join(e.home, ".devin", "rules", "x-style.md"), filepath.Join(pb, "rules", "x-style.md"))
	e.mustRun(ExitOK, "profile", "disable", "default")
	e.assertMissing(filepath.Join(e.home, ".devin"))
	e.assertMissing(filepath.Join(e.home, ".config", "devin"))
}

func TestExplicitPathSurvivesAgentChange(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	sub := e.file(pb, "ai/subagents/x-r.md")
	e.file(pb, "agents/x-default.md")
	e.mustRun(ExitOK, "add", "x", pb, "--subagents-path", "ai/subagents")
	e.assertLink(filepath.Join(e.home, ".claude", "agents", "x-r.md"), sub)
	e.assertMissing(filepath.Join(e.home, ".claude", "agents", "x-default.md"))
	e.mustRun(ExitOK, "profile", "agent", "default", "cursor")
	e.assertLink(filepath.Join(e.home, ".cursor", "agents", "x-r.md"), sub)
	if p := e.config().Playbooks[0]; p.Artifacts.Subagents.Path != "ai/subagents" || p.Artifacts.Skills.Path != "" {
		t.Fatalf("unexpected config: %+v", p)
	}
	_, errOut := e.mustRun(ExitUsage, "add", "y", pb, "-sap", "../outside")
	if !strings.Contains(errOut, "--subagents-path must be a path inside the playbook folder") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestListJSONKinds(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	e.file(pb, "agents/x-r.md")
	e.mustRun(ExitOK, "add", "x", pb)
	if err := os.Remove(filepath.Join(e.home, ".claude", "agents", "x-r.md")); err != nil {
		t.Fatal(err)
	}
	out, _ := e.mustRun(ExitOK, "list", "--json")
	var list listOutput
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	p := list.Profiles[0]
	if p.Agent != "claude" || len(p.Playbooks[0].Artifacts) != 4 {
		t.Fatalf("unexpected list: %+v", p)
	}
	sub := p.Playbooks[0].Artifacts[1]
	if sub.Kind != "subagents" || sub.Path != "agents" || sub.LinkFolder != filepath.Join(e.home, ".claude", "agents") ||
		len(sub.Items) != 1 || sub.Items[0].Linked {
		t.Fatalf("unexpected subagents: %+v", sub)
	}
	out, _ = e.mustRun(ExitOK, "list")
	notLinked := "x-r.md  (not linked from ~" + string(filepath.Separator) + filepath.Join(".claude", "agents")
	if !strings.Contains(out, notLinked) ||
		!strings.Contains(out, "agent claude (Claude Code)") || strings.Contains(out, "commands (") {
		t.Fatalf("unexpected list: %s", out)
	}
}

func TestProfileSwitchBetweenAgents(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g")
	pb := e.playbook("p", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "profile", "add", "claude", "-a", "claude")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "claude")
	out, _ := e.mustRun(ExitOK, "profile", "switch", "claude")
	if !strings.Contains(out, "Disabling default; enabling claude") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.assertMissing(filepath.Join(e.home, ".agents"))
	e.assertLink(filepath.Join(e.home, ".claude", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "profile", "switch", "default,claude") // different folders: no collision
	e.assertLink(filepath.Join(e.agentsSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	out, _ = e.mustRun(ExitOK, "profile", "list")
	if !strings.Contains(out, "* claude               claude") ||
		!strings.Contains(out, "* default              standard") {
		t.Fatalf("unexpected list: %s", out)
	}
	_, errOut := e.mustRun(ExitFailure, "add", "y", pb)
	if !strings.Contains(errOut, "2 profiles are enabled") {
		t.Fatalf("unexpected message: %s", errOut)
	}
}

func TestRemoveKeepsHandAddedFiles(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-g", "-a", "claude")
	pb := e.playbook("p", "x-a")
	e.file(pb, "agents/x-r.md")
	e.mustRun(ExitOK, "add", "x", pb)
	mine := e.file(e.home, ".claude/agents/mine.md")
	settings := e.file(e.home, ".claude/settings.json")
	out, _ := e.mustRun(ExitOK, "remove", "x")
	if !strings.Contains(out, "skills (empty folder removed)") || strings.Contains(out, "agents (empty") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !fsutil.IsFile(mine) || !fsutil.IsFile(settings) {
		t.Fatal("files added by hand were removed")
	}
	e.assertMissing(filepath.Join(e.home, ".claude", "agents", "x-r.md"))
	e.assertMissing(filepath.Join(e.home, ".claude", "skills"))
}

func TestAgentsHelp(t *testing.T) {
	e := newEnv(t)
	out, _ := e.mustRun(ExitOK, "profile", "--help")
	for _, want := range []string{"copilot   GitHub Copilot", ".github/agents, ~/.copilot/agents (.agent.md files)",
		".github/prompts, not in the home folder", "custom agent definitions"} {
		if !strings.Contains(out, want) {
			t.Errorf("profile help lacks %q", want)
		}
	}
	out, _ = e.mustRun(ExitOK, "--help")
	if !strings.Contains(out, "Agents: "+strings.Join(config.AgentNames(), ", ")) {
		t.Errorf("main help lacks the agents: %s", out)
	}
	for _, args := range [][]string{{}, {"init"}, {"add"}, {"update"}, {"remove"}, {"list"}, {"check"}, {"profile"}} {
		out, _ := e.mustRun(ExitOK, append(args, "--help")...)
		for i, line := range strings.Split(out, "\n") {
			if n := len([]rune(line)); n > 120 {
				t.Errorf("%v help, line %d: %d characters", args, i+1, n)
			}
		}
	}
}
