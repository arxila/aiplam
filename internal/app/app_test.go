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
	"errors"
	"fmt"
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
	"arxila.io/aiplam/internal/playbook"
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
	return &env{t: t, root: root, home: home,
		app: &App{Home: home, Cwd: root, Git: gitinfo.NewReader(), GOOS: "linux"}}
}

// run runs aiplam with args and returns the exit code, stdout and stderr.
func (e *env) run(args ...string) (int, string, string) {
	e.t.Helper()
	var out, errOut bytes.Buffer
	e.app.Stdout, e.app.Stderr = &out, &errOut
	e.app.gitWarned = false
	e.app.fileLinks = nil
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

// slashed returns s with its backslashes turned into slashes on Windows, where aiplam prints paths with backslashes,
// so that tests can expect paths written with slashes on every system.
func slashed(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(s, `\`, "/")
	}
	return s
}

// contains reports whether out contains want, taking slashes and backslashes as the same on Windows (see slashed).
func contains(out, want string) bool {
	return strings.Contains(slashed(out), slashed(want))
}

// mustFail runs aiplam, expects exit code want and a message containing each of msgs (in stdout or stderr).
func (e *env) mustFail(want int, msgs []string, args ...string) {
	e.t.Helper()
	out, errOut := e.mustRun(want, args...)
	for _, msg := range msgs {
		if !contains(out+errOut, msg) {
			e.t.Fatalf("aiplam %s: missing %q in:\n%s%s", strings.Join(args, " "), msg, out, errOut)
		}
	}
}

// file creates a file (and its folders) at a path relative to the root.
func (e *env) file(rel string) string {
	e.t.Helper()
	path := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
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

func (e *env) mkdir(dir string) string {
	e.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

func (e *env) homeSkills() string { return filepath.Join(e.home, ".agents", "skills") }

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

// profile loads a profile of a managed folder.
func (e *env) profile(managed, name string) *config.Profile {
	e.t.Helper()
	prof, err := config.LoadProfile(managed, name)
	if err != nil {
		e.t.Fatal(err)
	}
	return prof
}

func (e *env) enabled(managed string) string {
	e.t.Helper()
	conf, err := config.LoadConfig(managed)
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.Join(conf.Enabled, ",")
}

// writeProfile replaces the content of a profile's file.
func (e *env) writeProfile(managed, profile, content string) {
	e.t.Helper()
	if err := os.WriteFile(config.ProfileFile(managed, profile), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestCommandsNeedManagedFolder(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"status"}, {"list"}, {"update", "x"}, {"add", "x", "."}, {"remove", "x"},
		{"profile", "list"}, {"list", "--user"}} {
		e.mustFail(ExitFailure, []string{"aiplam init"}, args...)
	}
	// A .aiplam folder without configuration.
	e.mkdir(filepath.Join(e.home, ".aiplam"))
	e.mustFail(ExitFailure, []string{"has a .aiplam folder but no aiplam-config.yaml", "aiplam init --user"},
		"status", "--user")
}

func TestOneCommandAtATime(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	dir := config.Dir(e.home)
	if ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err != nil ||
		!strings.Contains(string(ignore), "/.lock\n") {
		t.Fatalf(".aiplam/.gitignore: %q, %v", ignore, err)
	}
	// While another command holds the lock, commands that change the managed folder fail; status and list still run.
	unlock, err := fsutil.Lock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	pb := e.playbook("p", "x-a")
	for _, args := range [][]string{{"add", "x", pb}, {"update", "x"}, {"profile", "add", "o", "-a", "standard"}} {
		e.mustFail(ExitFailure, []string{"another aiplam command"}, args...)
	}
	e.mustRun(ExitOK, "list", "-u")
	e.mustRun(ExitOK, "status", "-u")
	unlock()
	e.mustRun(ExitOK, "add", "x", pb, "-u")
}

func TestInitUsage(t *testing.T) {
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.root, "project"))
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"init"}, "init needs a folder"},
		{[]string{"init", project}, "init needs the target folders: an agent template with --agent (standard, claude"},
		{[]string{"init", project, "-u", "-a", "claude"}, "give either a folder or --user"},
		{[]string{"init", project, "x", "-a", "claude"}, "unexpected argument: x"},
		{[]string{"init", project, "-a", "claude", "-f", "skills:x"}, "give either --agent or --folder"},
		{[]string{"init", project, "-a", "claude", "-a", "junie"}, "--agent can be given only once"},
		{[]string{"init", project, "-a", "nope"}, "unknown agent template 'nope'"},
		{[]string{"init", project, "-a", "claude:standard"}, "unknown agent template 'claude:standard'"},
		{[]string{"init", project, "-f", "skills"}, "must be <kind>:<targetfolder>"},
		{[]string{"init", project, "-f", "skills:../x"}, "cannot contain '..'"},
		{[]string{"init", project, "-f", "skills:%APPDATA%/x"}, "only exists on Windows"},
		{[]string{"init", "-u", "-f", "skills:%APPDATA%/x"}, "only exists on Windows"},
		{[]string{"init", "-u", "-f", "skills:./%APPDATA%/x"}, "only exists on Windows"},
		{[]string{"init", project, "-f", "skills:a,rules:a"}, "the same target folder"},
		{[]string{"init", project, "-f", "skills:\"a"}, "unterminated quote"},
		{[]string{"init", project, "-a", "claude", "-p", "aiplam-config"}, "invalid profile name"},
		{[]string{"init", project, "--base", "x"}, "unknown option: --base"},
		{[]string{"init", project, "-g"}, "unknown option: -g"},
	} {
		code, _, errOut := e.run(c.args...)
		if code != ExitUsage || !contains(errOut, c.msg) {
			t.Errorf("%v: exit %d, %q; want a usage error with %q", c.args, code, errOut, c.msg)
		}
		// Usage errors show the synopsis only.
		if code == ExitUsage && (!contains(errOut, "--help' for") || contains(errOut, "Examples:")) {
			t.Errorf("%v: unexpected usage text: %s", c.args, errOut)
		}
	}
	e.assertMissing(filepath.Join(project, ".aiplam"))
	e.mustFail(ExitFailure, []string{"does not exist"}, "init", filepath.Join(e.root, "nope"), "-a", "claude")
}

func TestInit(t *testing.T) {
	e := newEnv(t)
	out, _ := e.mustRun(ExitOK, "init", "--user", "--agent", "copilot")
	for _, want := range []string{"Managed folder: ~ (user managed folder)", "Created profile 'default'",
		"skills: .copilot/skills", "instructions: .copilot/instructions"} {
		if !contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	prof := e.profile(e.home, "default")
	if prof.Name != "default" || prof.Agent != "copilot" || len(prof.Targets) != 3 || e.enabled(e.home) != "default" {
		t.Fatalf("profile: %+v", prof)
	}
	e.mustFail(ExitFailure, []string{"is already a managed folder"}, "init", "-u", "-a", "claude")
	// The home folder given as a folder is the user managed folder: its targets are the user ones.
	e2 := newEnv(t)
	e2.mustRun(ExitOK, "init", e2.home, "-a", "opencode")
	if f, _ := e2.profile(e2.home, "default").Targets.Folder("skills"); f != ".config/opencode/skills" {
		t.Fatalf("home given as a folder: %s", f)
	}

	// Several profiles, all enabled, with targets given by hand (quoted, with commas and spaces).
	project := e.mkdir(filepath.Join(e.home, "w", "project"))
	out, _ = e.mustRun(ExitOK, "init", project, "-f", `skills:.agents/skills,rules:"my rules, all"`, "-f",
		"notes:docs/notes", "-p", "a,b", "-p", "c")
	if e.enabled(project) != "a,b,c" {
		t.Fatalf("enabled: %s", e.enabled(project))
	}
	prof = e.profile(project, "b")
	if prof.Agent != config.CustomAgent || len(prof.Targets) != 3 || prof.Targets[1].Folder != "my rules, all" ||
		prof.Targets[2].Kind != "notes" {
		t.Fatalf("profile b: %+v", prof)
	}
	if !contains(out, "is inside managed folder '~'") || contains(out, "git repository") {
		t.Fatalf("unexpected notes:\n%s", out)
	}
	// A leftover profile file without configuration is not overwritten.
	leftover := e.mkdir(filepath.Join(e.root, "leftover"))
	e.mustRun(ExitOK, "init", leftover, "-a", "claude")
	if err := os.Remove(config.ConfigFile(leftover)); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"already exists, from an earlier setup"}, "init", leftover, "-a", "claude")

	// In a git repository, init says that the links must be ignored.
	repo := e.mkdir(filepath.Join(e.root, "repo"))
	e.mkdir(filepath.Join(repo, ".git"))
	out, _ = e.mustRun(ExitOK, "init", repo, "-a", "junie:standard")
	if !contains(out, "links aiplam makes belong to each machine and must be ignored by git") {
		t.Fatalf("unexpected git note:\n%s", out)
	}
}

func TestAddAndMappings(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	pb := e.playbook("pb", "x-a", "x-b", "y-c")
	e.file("pb/skills/.x-hidden/SKILL.md")
	e.file("pb/agents/x-r.md")
	e.file("pb/subagents/x-s.md")
	e.file("pb/rules/x-style.md")
	claude := filepath.Join(e.home, ".claude")

	// Defaults: the subfolder named like the kind, and the format '<name>-*'.
	out, _ := e.mustRun(ExitOK, "add", "x", pb)
	for _, want := range []string{"skills: " + filepath.Join(pb, "skills") + ", format 'x-*': 2 artifact(s)",
		"commands: " + filepath.Join(pb, "commands") + ", format 'x-*': not found", "Added x."} {
		if !contains(out, strings.ReplaceAll(want, e.home, "~")) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	e.assertLink(filepath.Join(claude, "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertLink(filepath.Join(claude, "agents", "x-s.md"), filepath.Join(pb, "subagents", "x-s.md"))
	e.assertLink(filepath.Join(claude, "rules", "x-style.md"), filepath.Join(pb, "rules", "x-style.md"))
	e.assertMissing(filepath.Join(claude, "skills", ".x-hidden"))
	e.assertMissing(filepath.Join(claude, "commands"))
	e.mustFail(ExitFailure, []string{"already registered", "aiplam update x"}, "add", "x", pb)
	e.mustRun(ExitOK, "remove", "x")
	e.assertMissing(claude)

	// Mappings: another subfolder, a format, the playbook root; stored in the profile.
	e.mustRun(ExitOK, "add", "team", pb, "-f", "subagents:agents:*.md", "--folder", "skills:skills:*")
	e.assertLink(filepath.Join(claude, "agents", "x-r.md"), filepath.Join(pb, "agents", "x-r.md"))
	e.assertLink(filepath.Join(claude, "skills", "y-c"), filepath.Join(pb, "skills", "y-c"))
	e.assertMissing(filepath.Join(claude, "skills", ".x-hidden")) // never, whatever the format
	m := e.profile(e.home, "default").Playbooks[0].Mappings
	if m["subagents"].Folder != "agents" || m["subagents"].Format != "*.md" || m["skills"].Format != "*" ||
		len(m) != 2 {
		t.Fatalf("mappings: %+v", m)
	}
	e.mustRun(ExitOK, "remove", "team")

	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"-f", "prompts:p"}, "'prompts' is not a kind of profile 'default' (its kinds: skills, subagents"},
		{[]string{"-f", "skills:../x"}, "cannot contain '..'"},
		{[]string{"-f", "skills:" + pb}, "must be a relative path"},
		{[]string{"-f", "skills"}, "must be <kind>:<subfolder>[:<format>]"},
		{[]string{"-f", "skills::*"}, "the playbook subfolder is empty"},
		{[]string{"-f", "skills:.:a/b"}, "cannot contain path separators"},
		{[]string{"-f", "skills:.:.x*"}, "would only match hidden files and folders"},
		{[]string{"-f", "skills:a,skills:b"}, "kind 'skills' is given more than once"},
		{[]string{"-f", "skills:a:b:c"}, "must be <kind>:<subfolder>[:<format>]"},
	} {
		code, _, errOut := e.run(append([]string{"add", "x", pb}, c.args...)...)
		if code != ExitUsage || !contains(errOut, c.msg) {
			t.Errorf("%v: exit %d, %q; want %q", c.args, code, errOut, c.msg)
		}
	}
	if n := len(e.profile(e.home, "default").Playbooks); n != 0 {
		t.Fatalf("%d playbooks registered", n)
	}
	// Subfolders that do not exist yet are fine: what appears is linked by update.
	empty := e.mkdir(filepath.Join(e.root, "empty"))
	_, errOut := e.mustRun(ExitOK, "add", "z", empty)
	if !contains(errOut, "no artifacts were found") {
		t.Fatalf("want a warning: %s", errOut)
	}
	e.skill(filepath.Join(empty, "skills"), "z-new")
	e.mustRun(ExitOK, "update", "z")
	e.assertLink(filepath.Join(claude, "skills", "z-new"), filepath.Join(empty, "skills", "z-new"))
}

func TestProfilesToActOn(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard", "-p", "a,b")
	e.mustRun(ExitOK, "profile", "add", "off", "-f", "skills:off/skills")
	pb := e.playbook("pb", "x-a", "y-b")

	// With two enabled profiles, --profile is needed.
	e.mustFail(ExitFailure, []string{"2 profiles are enabled (a, b): choose the profile to register the playbook in"},
		"add", "x", pb)
	e.mustRun(ExitOK, "add", "x", pb, "-p", "a")
	// Several profiles at once, enabled or not: registered in each, linked in the enabled ones.
	out, _ := e.mustRun(ExitOK, "add", "y", pb, "-p", "b,off")
	if !contains(out, "Nothing was linked in profile 'off'") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	e.assertLink(filepath.Join(e.homeSkills(), "y-b"), filepath.Join(pb, "skills", "y-b"))
	e.assertMissing(filepath.Join(e.home, "off"))
	if len(e.profile(e.home, "off").Playbooks) != 1 || len(e.profile(e.home, "b").Playbooks) != 1 {
		t.Fatal("not registered in both profiles")
	}
	e.mustFail(ExitFailure, []string{"profile 'nope' does not exist"}, "add", "z", pb, "-p", "nope")
	e.mustFail(ExitFailure, []string{"choose the profile to update the playbook in"}, "update", "x")
	e.mustFail(ExitFailure, []string{"no playbook of profile 'b' is registered with name, id or playbook folder 'x'"},
		"update", "x", "-p", "a,b")
	out, _ = e.mustRun(ExitOK, "update", "y", "-p", "b,off")
	if !contains(out, "Nothing to update for playbook y (profile off)") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	e.mustRun(ExitOK, "remove", "y", "-p", "b,off")
	e.assertMissing(filepath.Join(e.homeSkills(), "y-b"))
	if len(e.profile(e.home, "off").Playbooks) != 0 {
		t.Fatal("not removed from profile off")
	}
	e.mustFail(ExitUsage, []string{"'aiplam profile' does not take --profile"}, "profile", "list", "-p", "a")
	e.mustFail(ExitUsage, []string{"'aiplam status' does not take --profile"}, "status", "-p", "a")
}

func TestCollisions(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	pb := e.playbook("pb", "x-a", "x-data-b")
	other := e.playbook("other", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)

	// Another playbook of the same profile linking the same name: from another folder, or from the same folder with
	// an overlapping format.
	e.mustFail(ExitFailure, []string{"skills x-a of playbook x@other (profile default)", "would also be linked by x@pb",
		"was not added"}, "add", "x", other)
	e.mustFail(ExitFailure, []string{"skills x-data-b of playbook x-data (profile default)", "would also be linked by x"},
		"add", "x-data", pb)
	if n := len(e.profile(e.home, "default").Playbooks); n != 1 {
		t.Fatalf("%d playbooks registered", n)
	}
	// A format that does not overlap is fine.
	e.mustRun(ExitOK, "add", "y", other, "-f", "skills:skills:nothing-*")

	// Something already where a link must go, even a link to the same artifact made by hand.
	third := e.playbook("third", "t-a", "t-b")
	if err := os.Symlink(filepath.Join(third, "skills", "t-a"), filepath.Join(e.homeSkills(), "t-a")); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"'~/.agents/skills/t-a' already exists, linking to the same artifact, but it " +
		"does not belong to this playbook"}, "add", "t", third)
	e.assertMissing(filepath.Join(e.homeSkills(), "t-b"))

	// The same playbook in a second profile: it cannot be enabled besides the first one, but it can replace it.
	e.mustRun(ExitOK, "profile", "add", "second", "-a", "standard")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "second")
	e.mustFail(ExitFailure, []string{"the same playbook (name 'x', playbook folder '" + strings.ReplaceAll(pb, e.home,
		"~") + "') is registered in x (profile default)", "nothing was changed"}, "profile", "enable", "second")
	if e.enabled(e.home) != "default" {
		t.Fatal("the enabled profiles changed")
	}
	out, _ := e.mustRun(ExitOK, "profile", "switch", "second")
	if contains(out, "x-a") { // kept, moved from default to second
		t.Fatalf("unexpected changes:\n%s", out)
	}
	e.assertLink(filepath.Join(e.homeSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "status", "-u")

	// Enabling several profiles at once is all-or-nothing.
	e.mustRun(ExitOK, "profile", "add", "third", "-a", "standard")
	e.mustRun(ExitOK, "add", "z", e.playbook("zpb", "z-a"), "-p", "third")
	e.mustFail(ExitFailure, []string{"nothing was changed"}, "profile", "enable", "third,default")
	e.assertMissing(filepath.Join(e.homeSkills(), "z-a"))
	if e.enabled(e.home) != "second" {
		t.Fatal("the enabled profiles changed")
	}
	e.mustRun(ExitOK, "profile", "enable", "third")
	e.assertLink(filepath.Join(e.homeSkills(), "z-a"), filepath.Join(e.root, "zpb", "skills", "z-a"))
}

func TestUpdateAndRemove(t *testing.T) {
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.home, "project"))
	e.mustRun(ExitOK, "init", project, "-f", "skills:ai/agents/skills,notes:ai/notes")
	e.app.Cwd = project
	pb := e.playbook("home/project/pb", "x-a", "x-b") // inside the project: its managed folder applies there
	e.file("home/project/pb/notes/x-n.txt")
	e.mustRun(ExitOK, "add", "x", pb)
	skills := filepath.Join(project, "ai", "agents", "skills")

	e.skill(filepath.Join(pb, "skills"), "x-c")
	if err := os.RemoveAll(filepath.Join(pb, "skills", "x-b")); err != nil {
		t.Fatal(err)
	}
	e.file("home/project/ai/agents/skills/mine.txt") // added by hand
	e.app.Cwd = pb                                   // the playbook registered for the current folder
	out, _ := e.mustRun(ExitOK, "update")
	for _, want := range []string{"Updating playbook x of profile 'default'", "+ ~/project/ai/agents/skills/x-c",
		"- ~/project/ai/agents/skills/x-b (broken link: its artifact no longer exists)", "Managed folder: ~/project"} {
		if !contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	e.app.Cwd = project
	out, _ = e.mustRun(ExitOK, "update", "x")
	if !contains(out, "up to date") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	// Removing unlinks, and removes the folders left empty up to the managed folder; hand-added files stay.
	out, _ = e.mustRun(ExitOK, "remove", pb)
	if !contains(out, "~/project/ai/notes (empty folder removed)") || !contains(out, "Removed x") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	e.assertMissing(filepath.Join(project, "ai", "notes"))
	if _, err := os.Stat(filepath.Join(skills, "mine.txt")); err != nil {
		t.Fatal("hand-added file removed")
	}
	e.mustFail(ExitUsage, []string{"remove needs a name"}, "remove")
	e.mustFail(ExitFailure, []string{"no playbook of profile 'default' is registered with name, id or playbook folder"},
		"remove", "x")
}

func TestStatus(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	pb := e.playbook("pb", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	out, _ := e.mustRun(ExitOK, "status")
	for _, want := range []string{"Managed folder: ~\n", "user managed folder (the home folder): yes",
		"git repository: none", "default: agent claude (~/.aiplam/default.yaml)", "Everything is up to date."} {
		if !contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// Pending changes, broken links that belong to no playbook, and template mismatches (warned, not counted).
	e.skill(filepath.Join(pb, "skills"), "x-new")
	if err := os.Symlink(filepath.Join(e.root, "gone"), filepath.Join(e.home, ".claude", "skills", "stray")); err != nil {
		t.Fatal(err)
	}
	prof := e.profile(e.home, "default")
	prof.Targets = append(prof.Targets, config.Target{Kind: "extra", Folder: "extra"})
	if err := config.SaveProfile(e.home, prof); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"'aiplam update x' would link ~/.claude/skills/x-new",
		"broken link ~/.claude/skills/stray", "its target folders no longer match those of its agent template 'claude'",
		"2 item(s) need attention"}, "status")
	// A missing playbook folder.
	if err := os.Rename(pb, pb+".moved"); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"playbook folder '" + strings.ReplaceAll(pb, e.home, "~") + "' does not exist"},
		"status")
	// Invalid and missing profiles.
	e.writeProfile(e.home, "default", "name: other\nagent: claude\ntargets: {}\nplaybooks: []\n")
	e.mustFail(ExitFailure, []string{"name 'other' does not match", "nothing else was checked"}, "status")
	e.mustFail(ExitFailure, []string{"name 'other' does not match"}, "list")
	if err := os.Remove(config.ProfileFile(e.home, "default")); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"the enabled profile 'default' does not exist"}, "status")
	_, errOut := e.mustRun(ExitOK, "list")
	if !contains(errOut, "the enabled profile 'default' does not exist") {
		t.Fatalf("want a warning: %s", errOut)
	}
}

func TestList(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	pb := e.playbook("pb", "x-a", "x-b")
	if err := os.WriteFile(filepath.Join(pb, "skills", "x-a", "SKILL.md"),
		[]byte("---\nname: x-a\nmetadata:\n  version: \"2.0\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := e.mustRun(ExitOK, "list")
	if !contains(out, "no playbooks are registered") {
		t.Fatalf("unexpected list:\n%s", out)
	}
	e.mustRun(ExitOK, "add", "x", pb)
	e.file("home/.agents/skills/mine")                                                 // not a link: not listed
	if err := os.Symlink(e.root, filepath.Join(e.homeSkills(), "other")); err != nil { // not a playbook's
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(pb, "skills", "x-b")); err != nil {
		t.Fatal(err)
	}
	out, _ = e.mustRun(ExitOK, "list")
	for _, want := range []string{"Profile default (agent standard)", "skills (~/.agents/skills):",
		"x-a  playbook x (unknown), skill version 2.0", "x-b  playbook x (unknown)  BROKEN LINK"} {
		if !contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if contains(out, "mine") || contains(out, "other") {
		t.Fatalf("unexpected entries:\n%s", out)
	}

	// From a folder given as an argument, and with --user from another managed folder.
	project := e.mkdir(filepath.Join(e.root, "project", "sub"))
	e.mustRun(ExitOK, "init", filepath.Dir(project), "-f", "skills:s")
	e.app.Cwd = e.root
	out, _ = e.mustRun(ExitOK, "list", project)
	if !contains(out, "Managed folder: "+filepath.Dir(project)) || !contains(out, "nothing is linked") &&
		!contains(out, "no playbooks are registered") {
		t.Fatalf("unexpected list:\n%s", out)
	}
	e.app.Cwd = project
	out, _ = e.mustRun(ExitOK, "list", "--user")
	if !contains(out, "Managed folder: ~\n") {
		t.Fatalf("unexpected list:\n%s", out)
	}
	e.mustFail(ExitUsage, []string{"give either a folder or --user"}, "list", project, "--user")
	e.mustFail(ExitFailure, []string{"folder 'nope' does not exist"}, "status", "nope")
	e.mustRun(ExitOK, "profile", "disable", "default", "-u")
	out, _ = e.mustRun(ExitOK, "list", "-u")
	if !contains(out, "No profile is enabled") {
		t.Fatalf("unexpected list:\n%s", out)
	}
}

func TestManagedFolderDiscovery(t *testing.T) {
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.home, "w", "project"))
	deep := e.mkdir(filepath.Join(project, "a", "b"))
	outside := e.mkdir(filepath.Join(e.root, "outside", "x"))
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	e.mustRun(ExitOK, "init", project, "-a", "claude")

	e.app.Cwd = deep
	out, _ := e.mustRun(ExitOK, "status")
	if !contains(out, "Managed folder: ~/w/project\n") {
		t.Fatalf("unexpected status:\n%s", out)
	}
	// Outside home, with nothing managed up to the root: the home folder.
	e.app.Cwd = outside
	out, _ = e.mustRun(ExitOK, "status")
	if !contains(out, "Managed folder: ~\n") {
		t.Fatalf("unexpected status:\n%s", out)
	}
	// A managed folder above the home folder is never used from inside it.
	e.mustRun(ExitOK, "init", e.root, "-a", "claude")
	e.app.Cwd = filepath.Join(e.home, "w")
	out, _ = e.mustRun(ExitOK, "status")
	if !contains(out, "Managed folder: ~\n") {
		t.Fatalf("unexpected status:\n%s", out)
	}
	e.app.Cwd = outside
	out, _ = e.mustRun(ExitOK, "status")
	if !contains(out, "Managed folder: "+e.root+"\n") {
		t.Fatalf("unexpected status:\n%s", out)
	}
	// A git repository whose root is above the managed folder.
	e.mkdir(filepath.Join(e.home, "w", ".git"))
	e.app.Cwd = deep
	out, _ = e.mustRun(ExitOK, "status")
	if !contains(out, "git repository: yes, with its root at ~/w") {
		t.Fatalf("unexpected status:\n%s", out)
	}
}

func TestProfileCommands(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	pb := e.playbook("pb", "x-a", "y-b")
	e.mustRun(ExitOK, "add", "x", pb)

	e.mustFail(ExitUsage, []string{"profile add needs one profile name"}, "profile", "add")
	e.mustFail(ExitUsage, []string{"profile add needs the target folders"}, "profile", "add", "p")
	e.mustFail(ExitUsage, []string{"only 'profile add' takes --agent and --folder"}, "profile", "enable", "x", "-a",
		"claude")
	e.mustFail(ExitUsage, []string{"unknown profile subcommand: agent"}, "profile", "agent", "default", "claude")
	out, _ := e.mustRun(ExitOK, "profile", "add", "work", "-a", "devin")
	if !contains(out, "skills: .config/devin/skills") || !contains(out, "not enabled") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	e.mustFail(ExitFailure, []string{"profile 'work' already exists"}, "profile", "add", "work", "-a", "devin")
	e.mustRun(ExitOK, "add", "y", pb, "-p", "work")
	out, _ = e.mustRun(ExitOK, "profile", "list")
	if !contains(out, "* default") || !contains(out, "  work                 devin") {
		t.Fatalf("unexpected list:\n%s", out)
	}
	e.mustRun(ExitOK, "profile", "enable", "work")
	e.assertLink(filepath.Join(e.home, ".config", "devin", "skills", "y-b"), filepath.Join(pb, "skills", "y-b"))
	out, _ = e.mustRun(ExitOK, "profile", "switch", "default,work")
	if !contains(out, "unchanged") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	e.mustFail(ExitFailure, []string{"not enabled: nope"}, "profile", "disable", "nope")
	e.mustFail(ExitFailure, []string{"profile 'nope' does not exist"}, "profile", "enable", "nope")

	// Removing an enabled profile removes its links first, and the folders left empty, up to the home folder.
	out, _ = e.mustRun(ExitOK, "profile", "remove", "work")
	if !contains(out, "Removed profile 'work' and its 1 playbook registration(s)") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	e.assertMissing(filepath.Join(e.home, ".config"))
	if e.enabled(e.home) != "default" || config.ProfileExists(e.home, "work") {
		t.Fatal("the profile was not removed")
	}
	e.mustRun(ExitOK, "profile", "disable", "default")
	e.assertMissing(filepath.Join(e.home, ".agents"))
	out, _ = e.mustRun(ExitOK, "profile", "list")
	if !contains(out, "No profile is enabled") {
		t.Fatalf("unexpected list:\n%s", out)
	}
}

func TestAppData(t *testing.T) {
	e := newEnv(t)
	e.app.GOOS = "windows"
	appData := e.mkdir(filepath.Join(e.root, "appdata"))
	e.mustRun(ExitOK, "init", "-u", "-a", "devin")
	if f, _ := e.profile(e.home, "default").Targets.Folder("skills"); f != "%APPDATA%/devin/skills" {
		t.Fatalf("skills target: %s", f)
	}
	pb := e.playbook("pb", "x-a")
	e.mustFail(ExitFailure, []string{"needs the APPDATA environment variable"}, "add", "x", pb)
	e.app.AppData = appData
	e.mustRun(ExitOK, "add", "x", pb)
	e.assertLink(filepath.Join(appData, "devin", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "remove", "x")
	e.assertMissing(filepath.Join(appData, "devin"))
	if !fsutil.IsDir(appData) {
		t.Fatal("%APPDATA% itself must never be removed")
	}
	// Only in the user managed folder.
	project := e.mkdir(filepath.Join(e.home, "project"))
	e.mustFail(ExitUsage, []string{"can only be used in the user managed folder"}, "init", project, "-f",
		"skills:%APPDATA%/x")
}

func TestArtifactsStayInsidePlaybook(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	pb := e.playbook("p", "x-a")
	other := e.playbook("other", "x-b")
	// A subfolder that leads outside the playbook through a link is refused; an artifact that does is not linked.
	if err := os.Symlink(filepath.Join(other, "skills"), filepath.Join(pb, "linked")); err != nil {
		t.Skipf("cannot create symbolic links: %v", err)
	}
	e.mustFail(ExitFailure, []string{"outside the playbook folder"}, "add", "x", pb, "-f", "skills:linked")
	if err := os.Symlink(filepath.Join(other, "skills", "x-b"), filepath.Join(pb, "skills", "x-b")); err != nil {
		t.Fatal(err)
	}
	e.mustRun(ExitOK, "add", "x", pb)
	e.assertLink(filepath.Join(e.homeSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(filepath.Join(e.homeSkills(), "x-b"))
}

func TestPlaybookBehindSymlink(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
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
	if !contains(out, "up to date") {
		t.Fatalf("unexpected output: %s", out)
	}
	e.mustRun(ExitOK, "status")
	e.app.Cwd = moved // the real folder selects the playbook registered through the link
	e.mustRun(ExitOK, "update")
	e.mustRun(ExitOK, "remove", "x")
	e.assertMissing(filepath.Join(e.homeSkills(), "x-a"))
}

func TestProfileNameCase(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard", "-p", "work")
	// Never a second profile whose name differs only in case, even where the disk tells them apart.
	e.mustFail(ExitFailure, []string{"already exists"}, "profile", "add", "Work", "-a", "standard", "-u")
	if !config.ProfileExists(e.home, "Work") {
		t.Skip("the file system distinguishes case: Work and work are different profiles")
	}
	pb := e.playbook("p", "x-a")
	out, _ := e.mustRun(ExitOK, "add", "x", pb, "-p", "Work")
	if contains(out, "not enabled") {
		t.Fatalf("Work is the enabled profile work: %s", out)
	}
	e.assertLink(filepath.Join(e.homeSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "status")
}

func TestFailedAddIsUndone(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the current user cannot write to")
	}
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	pb := e.playbook("p", "x-a")
	e.file("p/subagents/x-r.md")
	locked := e.mkdir(filepath.Join(e.home, ".claude", "agents"))
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	e.mustFail(ExitFailure, []string{"undone", "not registered"}, "add", "x", pb)
	e.assertMissing(filepath.Join(e.home, ".claude", "skills", "x-a"))
	if n := len(e.profile(e.home, "default").Playbooks); n != 0 {
		t.Fatalf("%d playbooks registered", n)
	}
}

// TestRestoreProfilesReportsFailures checks that profiles that cannot be put back, after a failed change, are
// reported instead of the change being said undone.
func TestRestoreProfilesReportsFailures(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard", "-p", "a,b")
	e.app.Managed = e.home
	a, b := e.profile(e.home, "a"), e.profile(e.home, "b")
	if got := e.app.restoreProfiles([]*config.Profile{a, b}, "nothing changed"); got != "nothing changed" {
		t.Errorf("all put back: %q", got)
	}
	// A folder where the temporary file of b's save must go makes saving b fail.
	e.mkdir(filepath.Join(fmt.Sprintf("%s.%d.tmp", config.ProfileFile(e.home, "b"), os.Getpid()), "x"))
	got := e.app.restoreProfiles([]*config.Profile{a, b}, "nothing changed")
	if !strings.Contains(got, "putting back profile 'b'") || !strings.Contains(got, "so it keeps the change") ||
		!strings.Contains(got, "see it with 'aiplam profile show b") ||
		strings.Contains(got, "'a'") || strings.Contains(got, "nothing changed") {
		t.Errorf("b not put back: %q", got)
	}
	e.mkdir(filepath.Join(fmt.Sprintf("%s.%d.tmp", config.ProfileFile(e.home, "a"), os.Getpid()), "x"))
	if got := e.app.restoreProfiles([]*config.Profile{a, b}, "nothing changed"); !strings.Contains(got,
		"putting back profiles 'a'") || !strings.Contains(got, "so they keep the change") {
		t.Errorf("a and b not put back: %q", got)
	}
}

// TestUndoReportsEverything checks that, when undoing the link changes fails too, the message still says what
// happened to the profiles, and how to see what is left (once).
func TestUndoReportsEverything(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the current user cannot read")
	}
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	e.app.Managed = e.home
	locked := e.mkdir(filepath.Join(e.root, "locked"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	plan := playbook.Plan{Actions: []playbook.Action{{Kind: playbook.Create, Link: filepath.Join(locked, "x")}}}
	for _, state := range []string{"the playbook was not registered",
		"putting back profile 'b' (failed), so it keeps the change: " + e.app.leftAdvice()} {
		err := e.app.undo(plan, errors.New("saving failed"), state)
		msg := err.Error()
		if !strings.Contains(msg, "undoing the link changes also failed") || !strings.Contains(msg, state) ||
			strings.Count(msg, e.app.leftAdvice()) != 1 {
			t.Errorf("undo: %s", msg)
		}
	}
}

// TestConfigurationFileLinks checks that configuration files that are links are read, but never replaced: not even a
// broken one.
func TestConfigurationFileLinks(t *testing.T) {
	e := newEnv(t)
	isLink := func(path string) bool {
		info, err := os.Lstat(path)
		return err == nil && info.Mode()&os.ModeSymlink != 0
	}

	// A broken link where a profile's file goes: init takes it for an existing profile.
	project := e.mkdir(filepath.Join(e.root, "project"))
	broken := filepath.Join(e.mkdir(filepath.Join(project, config.DirName)), "default.yaml")
	if err := os.Symlink(filepath.Join(e.root, "gone.yaml"), broken); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"already exists"}, "init", project, "-a", "standard")
	if !isLink(broken) {
		t.Fatal("the broken link was replaced")
	}

	// A profile file that is a link to a file: commands that read it work, those that would change it fail.
	other := e.mkdir(filepath.Join(e.root, "other"))
	e.mustRun(ExitOK, "init", other, "-a", "standard")
	file := config.ProfileFile(other, "default")
	shared := filepath.Join(e.mkdir(filepath.Join(other, "shared")), "default.yaml")
	if err := os.Rename(file, shared); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "shared", "default.yaml"), file); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = other
	e.mustRun(ExitOK, "status")
	pb := e.playbook("pb", "x-a")
	e.mustFail(ExitFailure, []string{"is not a regular file"}, "add", "x", pb)
	after, err := os.ReadFile(shared)
	if !isLink(file) || err != nil || string(after) != string(before) {
		t.Fatalf("the profile file or its link changed: link %v, %v", isLink(file), err)
	}
	e.assertMissing(filepath.Join(other, ".agents", "skills", "x-a"))
}

func TestSandbox(t *testing.T) {
	e := newEnv(t)
	outside := e.mkdir(filepath.Join(e.root, "outside"))
	e.mustRun(ExitOK, "init", outside, "-a", "claude") // the real configuration, set up before the sandbox
	e.app.Sandbox = e.home
	for _, args := range [][]string{
		{"init", e.root, "-a", "claude"}, // init outside the sandbox
		{"status"},                       // the managed folder found from a current folder outside the sandbox
		{"list", outside},                // a folder given outside the sandbox
		{"profile", "list"},
	} {
		e.app.Cwd = outside
		e.mustFail(ExitFailure, []string{"is outside the aiplam sandbox ('" + e.home + "')"}, args...)
	}
	if fsutil.IsDir(filepath.Join(e.root, ".aiplam")) {
		t.Fatal("init outside the sandbox should create nothing")
	}
	e.app.Cwd = e.home
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	e.mustRun(ExitOK, "list")
}

func TestHelpAndVersion(t *testing.T) {
	e := newEnv(t)
	for _, arg := range []string{"-v", "--version", "version"} {
		out, _ := e.mustRun(ExitOK, arg)
		if out != "aiplam "+Version+"\n" {
			t.Fatalf("%s: unexpected output %q", arg, out)
		}
	}
	_, errOut := e.mustRun(ExitUsage)
	if !contains(errOut, "aiplam "+Version+" - AI Playbook Manager\n"+Copyright) {
		t.Fatalf("unexpected help: %s", errOut)
	}
	for _, c := range commands {
		out, _ := e.mustRun(ExitOK, c.name, "--help")
		if !strings.HasPrefix(out, "Usage: aiplam "+c.name) {
			t.Errorf("%s --help: %s", c.name, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if len([]rune(line)) > 120 {
				t.Errorf("%s --help: line over 120 characters: %s", c.name, line)
			}
		}
	}
	out, _ := e.mustRun(ExitOK, "--help")
	if !contains(out, "list, show, add, remove, enable, disable, switch") {
		t.Fatalf("unexpected main help:\n%s", out)
	}
	out, _ = e.mustRun(ExitOK, "profile", "--help")
	if !contains(out, "devin (and devin:standard): Devin") ||
		!contains(out, "~/.config/devin/skills (Windows: %APPDATA%/devin/skills)") {
		t.Fatalf("unexpected profile help:\n%s", out)
	}
	e.mustFail(ExitUsage, []string{"unknown command: check"}, "check")
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

func TestSplitQuoted(t *testing.T) {
	items, err := folderItems([]string{`skills:.agents/skills,rules:"a, b:c"`, `x:'y'`})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || strings.Join(items[1], "|") != "rules|a, b:c" || strings.Join(items[2], "|") != "x|y" {
		t.Fatalf("items: %q", items)
	}
	if _, err := folderItems([]string{"a,,b"}); err == nil {
		t.Fatal("want an error for an empty item")
	}
}

func TestStatusReportsLeftoverLinks(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	pb := e.playbook("pb", "x-a")
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustRun(ExitOK, "status")

	// A profile removed from the enabled ones by hand: its links stay, and status reports them.
	if err := config.SaveConfig(e.home, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"~/.agents/skills/x-a -> " + filepath.Join(pb, "skills", "x-a") + " leads into " +
		"a registered playbook folder, but it belongs to no playbook of the enabled profiles", "1 item(s)"}, "status")

	// A playbook's mapping changed by hand (here, its name, so its format): the old links are reported too.
	if err := config.SaveConfig(e.home, &config.Config{Enabled: []string{"default"}}); err != nil {
		t.Fatal(err)
	}
	prof := e.profile(e.home, "default")
	prof.Playbooks[0].Name = "z"
	if err := config.SaveProfile(e.home, prof); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"leads into a registered playbook folder"}, "status")
	prof.Playbooks[0].Name = "x"
	if err := config.SaveProfile(e.home, prof); err != nil {
		t.Fatal(err)
	}
	e.mustRun(ExitOK, "status")

	// Links to elsewhere are not aiplam's: not reported. A profile that is not enabled and not valid is warned about,
	// without counting.
	if err := os.Symlink(e.root, filepath.Join(e.homeSkills(), "mine")); err != nil {
		t.Fatal(err)
	}
	e.writeProfile(e.home, "broken", "name: other\n")
	_, errOut := e.mustRun(ExitOK, "status")
	if !contains(errOut, "the profile cannot be enabled until it is fixed") || contains(errOut, "mine") {
		t.Fatalf("unexpected warnings: %s", errOut)
	}
	out, _ := e.mustRun(ExitOK, "profile", "list")
	if !contains(out, "broken") || !contains(out, "problem(s) to fix by hand") {
		t.Fatalf("unexpected list:\n%s", out)
	}
}

func TestStatusReportsEachConflictOnce(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard", "-p", "a")
	e.mustRun(ExitOK, "profile", "add", "b", "-a", "standard")
	pb := e.playbook("pb", "x-a")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "a,b")
	// Both enabled by hand: the same playbook in two enabled profiles is one problem.
	if err := config.SaveConfig(e.home, &config.Config{Enabled: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	_, errOut := e.mustRun(ExitFailure, "status")
	if n := strings.Count(errOut, "the same playbook cannot be in two enabled profiles"); n != 1 {
		t.Fatalf("reported %d times:\n%s", n, errOut)
	}
}

func TestHintsNameTheManagedFolder(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	project := e.mkdir(filepath.Join(e.home, "project"))
	e.mustRun(ExitOK, "init", project, "-a", "standard")
	// Listing another managed folder: suggested commands are run from inside it.
	e.app.Cwd = e.home
	out, _ := e.mustRun(ExitOK, "list", project)
	want := "'cd ~/project && aiplam add <name> <playbookfolder>'"
	if runtime.GOOS == "windows" { // cmd does not expand "~": the absolute path
		want = "'cd " + project + " && aiplam add <name> <playbookfolder>'"
	}
	if !contains(out, want) {
		t.Fatalf("unexpected hint:\n%s", out)
	}
	// Listing the user managed folder from elsewhere: with --user.
	e.app.Cwd = project
	out, _ = e.mustRun(ExitOK, "list", e.home)
	if !contains(out, "'aiplam add <name> <playbookfolder> --user'") {
		t.Fatalf("unexpected hint:\n%s", out)
	}
	// The managed folder the current folder leads to: plain commands.
	out, _ = e.mustRun(ExitOK, "list", ".")
	if !contains(out, "'aiplam add <name> <playbookfolder>'") {
		t.Fatalf("unexpected hint:\n%s", out)
	}
}

func TestInitNotesManagedFolderAbove(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	outside := e.mkdir(filepath.Join(e.root, "outside"))
	out, _ := e.mustRun(ExitOK, "init", outside, "-a", "standard")
	if contains(out, "is inside managed folder") { // the home folder applies outside it, but does not hold it
		t.Fatalf("unexpected note:\n%s", out)
	}
	inside := e.mkdir(filepath.Join(e.home, "w", "inside"))
	out, _ = e.mustRun(ExitOK, "init", inside, "-a", "standard")
	if !contains(out, "'~/w/inside' is inside managed folder '~'") {
		t.Fatalf("missing note:\n%s", out)
	}
}

func TestBrokenProfileCanBeDisabled(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard", "-p", "a,b")
	pb := e.playbook("pb", "x-a")
	e.mustRun(ExitOK, "add", "x", pb, "-p", "a")
	e.writeProfile(e.home, "b", "name: B\nagent: standard\ntargets:\n  skills: .agents/skills\n")
	e.mustFail(ExitFailure, []string{"it must be 'b'"}, "status")
	// Enabling needs every enabled profile, but disabling and removing work around a broken one.
	e.mustRun(ExitOK, "profile", "add", "c", "-a", "standard")
	e.mustFail(ExitFailure, []string{"it must be 'b'"}, "profile", "enable", "c")
	_, errOut := e.mustRun(ExitOK, "profile", "disable", "b")
	if !contains(errOut, "profile 'b' cannot be loaded") {
		t.Fatalf("want a warning: %s", errOut)
	}
	e.assertLink(filepath.Join(e.homeSkills(), "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.mustRun(ExitOK, "profile", "remove", "b")
	if config.ProfileExists(e.home, "b") || e.enabled(e.home) != "a" {
		t.Fatal("the broken profile was not removed")
	}
	e.mustRun(ExitOK, "profile", "enable", "c")
}

func TestProfileShow(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	pb := e.playbook("pb", "x-a")
	e.file("pb/agents/r.md")
	e.mustRun(ExitOK, "add", "x", pb, "-f", "subagents:agents:*.md")
	out, _ := e.mustRun(ExitOK, "profile", "show", "default")
	for _, want := range []string{"Profile default (~/.aiplam/default.yaml): enabled, agent claude",
		"  skills: .claude/skills\n", "  x: " + pb + " (unknown)\n",
		"    skills: " + filepath.Join(pb, "skills") + ", format 'x-*': 1 artifact(s)",
		"    subagents: " + filepath.Join(pb, "agents") + ", format '*.md': 1 artifact(s)",
		"    rules: " + filepath.Join(pb, "rules") + ", format 'x-*': not found"} {
		if !contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	e.mustRun(ExitOK, "profile", "add", "empty", "-a", "standard")
	out, _ = e.mustRun(ExitOK, "profile", "show", "empty")
	if !contains(out, "not enabled, agent standard") || !contains(out, "No playbooks are registered") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	e.mustFail(ExitFailure, []string{"profile 'nope' does not exist"}, "profile", "show", "nope")
	e.mustFail(ExitUsage, []string{"profile show needs one profile name"}, "profile", "show")
}

func TestFailedInitIsUndone(t *testing.T) {
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.root, "project"))
	// A folder where the second profile's file must go makes saving it fail.
	e.mkdir(filepath.Join(project, ".aiplam", "b.yaml"))
	e.mustRun(ExitFailure, "init", project, "-a", "standard", "-p", "a,b")
	e.assertMissing(config.ProfileFile(project, "a"))
	e.assertMissing(config.ConfigFile(project))
	if err := os.Remove(filepath.Join(project, ".aiplam", "b.yaml")); err != nil {
		t.Fatal(err)
	}
	e.mustRun(ExitOK, "init", project, "-a", "standard", "-p", "a,b")

	// Cleaning up in a folder whose name has characters special in patterns: only its own temporary files go.
	own, kept := e.file("proj[1]/.aiplam/a.yaml.1.tmp"), e.file("proj1/.aiplam/keep.tmp")
	removeNewConfigDir(filepath.Dir(own))
	if fsutil.IsFile(own) || !fsutil.IsFile(kept) {
		t.Fatalf("cleanup: own file left %v, other folder's file kept %v", fsutil.IsFile(own), fsutil.IsFile(kept))
	}

	// A profile file left by an earlier setup, named in another case: init does not make a second one.
	other := e.mkdir(filepath.Join(e.root, "other"))
	e.file("other/.aiplam/Work.yaml")
	e.mustFail(ExitFailure, []string{"already exists, from an earlier setup"}, "init", other, "-a", "standard", "-p",
		"work")
}

func TestFilesWhereFoldersMustBe(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-f", "skills:t/skills")
	pb := e.playbook("pb", "x-a")
	// A target folder's parent that is a file: refused with a clear message, and reported by status.
	e.file("home/t")
	e.mustFail(ExitFailure, []string{"target folder 't/skills' cannot be used: '~/t' exists and is not a folder"},
		"add", "x", pb)
	if err := os.Remove(filepath.Join(e.home, "t")); err != nil {
		t.Fatal(err)
	}
	e.mustRun(ExitOK, "add", "x", pb)
	if err := os.RemoveAll(filepath.Join(e.home, "t")); err != nil {
		t.Fatal(err)
	}
	e.file("home/t/skills")
	e.mustFail(ExitFailure, []string{"'~/t/skills' exists and is not a folder", "1 item(s)"}, "status")
	e.mustFail(ExitFailure, []string{"'~/t/skills' exists and is not a folder"}, "update", "x")
	if err := os.Remove(filepath.Join(e.home, "t", "skills")); err != nil {
		t.Fatal(err)
	}
	e.mustRun(ExitOK, "update", "x")

	// A playbook subfolder that is a file: refused with a clear message, and reported by status, without stopping it.
	other := e.mkdir(filepath.Join(e.root, "other"))
	e.file("other/skills")
	e.mustFail(ExitFailure,
		[]string{"its subfolder for skills, '" + filepath.Join(other, "skills") + "', is not a folder"}, "add", "y", other)
	prof := e.profile(e.home, "default")
	prof.Playbooks = append(prof.Playbooks, config.Playbook{Name: "y", Folder: "../other"})
	if err := config.SaveProfile(e.home, prof); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitFailure, []string{"is not a folder", "1 item(s)"}, "status")
	out, _ := e.mustRun(ExitOK, "profile", "show", "default")
	if !contains(out, "format 'y-*': not a folder") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// TestLinkedFoldersLeadingOutside checks that target folders and the .aiplam folder are refused when a link on their
// way leads outside the managed folder (nothing is written there), and accepted when it leads inside it.
func TestLinkedFoldersLeadingOutside(t *testing.T) {
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.root, "project"))
	outside := e.mkdir(filepath.Join(e.root, "outside"))
	pb := e.playbook("pb", "x-a")
	e.mustRun(ExitOK, "init", project, "-a", "claude")
	e.app.Cwd = project
	link := func(link, target string) {
		t.Helper()
		if err := fsutil.CreateLink(link, target); err != nil {
			t.Fatal(err)
		}
	}
	empty := func(dir string) {
		t.Helper()
		if items, err := os.ReadDir(dir); err != nil || len(items) > 0 {
			t.Fatalf("%s: %v, %d item(s)", dir, err, len(items))
		}
	}

	// A target folder's parent linked outside: refused by every command that uses it, with nothing written there.
	link(filepath.Join(project, ".claude"), outside)
	msg := "target folder '.claude/skills' ('" + filepath.Join(project, ".claude", "skills") +
		"') leads outside managed folder"
	e.mustFail(ExitFailure, []string{msg, "through a link: aiplam only writes links inside it"}, "add", "x", pb)
	e.mustFail(ExitFailure, []string{msg}, "status")
	empty(outside)
	if n := len(e.profile(project, "default").Playbooks); n != 0 {
		t.Fatalf("%d playbooks registered", n)
	}

	// The same link leading inside the managed folder: accepted.
	if err := fsutil.RemoveLink(filepath.Join(project, ".claude")); err != nil {
		t.Fatal(err)
	}
	inside := e.mkdir(filepath.Join(project, "agent-folders"))
	link(filepath.Join(project, ".claude"), inside)
	e.mustRun(ExitOK, "add", "x", pb)
	e.assertLink(filepath.Join(inside, "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))

	// The .aiplam folder linked outside: refused before anything is written in it.
	other := e.mkdir(filepath.Join(e.root, "other"))
	e.mustRun(ExitOK, "init", other, "-a", "claude")
	moved := filepath.Join(outside, "config")
	if err := os.Rename(filepath.Join(other, config.DirName), moved); err != nil {
		t.Fatal(err)
	}
	link(filepath.Join(other, config.DirName), moved)
	before, err := os.ReadFile(filepath.Join(moved, "default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = other
	e.mustFail(ExitFailure, []string{config.DirName + " ('" + filepath.Join(other, config.DirName) +
		"') leads outside managed folder", "aiplam only writes its configuration inside it"}, "add", "x", pb)
	if after, err := os.ReadFile(filepath.Join(moved, "default.yaml")); err != nil || string(after) != string(before) {
		t.Fatalf("the profile was changed: %v\n%s", err, after)
	}
	if fsutil.IsDir(filepath.Join(other, ".claude")) {
		t.Fatal("links were made")
	}

	// The .aiplam folder linked inside the managed folder: accepted.
	if err := fsutil.RemoveLink(filepath.Join(other, config.DirName)); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(other, "kept-config")
	if err := os.Rename(moved, kept); err != nil {
		t.Fatal(err)
	}
	link(filepath.Join(other, config.DirName), kept)
	e.mustRun(ExitOK, "add", "x", pb)

	// Links in the .aiplam folder are never written through, even broken ones: a .gitignore that is a link is left
	// as it is, and a lock file that is a link makes commands fail.
	brokenLink := func(path, target string) {
		t.Helper()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	brokenLink(filepath.Join(kept, ".gitignore"), filepath.Join(outside, "ignore"))
	e.mustRun(ExitOK, "update", "x")
	e.assertMissing(filepath.Join(outside, "ignore"))
	brokenLink(filepath.Join(kept, ".lock"), filepath.Join(outside, "lock"))
	e.mustFail(ExitFailure, []string{"cannot lock managed folder", "not a regular file"}, "update", "x")
	e.assertMissing(filepath.Join(outside, "lock"))
}

func TestSwitchStoresFileNames(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	e.mustRun(ExitOK, "profile", "add", "other", "-a", "standard")
	if !config.ProfileExists(e.home, "Other") {
		t.Skip("the file system distinguishes case: Other is not other")
	}
	e.mustRun(ExitOK, "profile", "switch", "Other")
	if e.enabled(e.home) != "other" {
		t.Fatalf("enabled: %s", e.enabled(e.home))
	}
	e.mustRun(ExitOK, "profile", "enable", "DEFAULT")
	if e.enabled(e.home) != "other,default" {
		t.Fatalf("enabled: %s", e.enabled(e.home))
	}
}

func TestQuotedHintsAndGitNote(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "standard")
	project := e.mkdir(filepath.Join(e.home, "My Projects", "p"))
	e.mustRun(ExitOK, "init", project, "-a", "standard")
	out, _ := e.mustRun(ExitOK, "list", project)
	if !contains(out, `'cd "`+project+`" && aiplam add`) {
		t.Fatalf("unexpected hint:\n%s", out)
	}
	for _, c := range []struct{ goos, pretty, abs, want string }{
		{"linux", "~/p", "/h/p", "~/p"},
		{"linux", "~/a$b", `/h/a$b"c`, `"/h/a\$b\"c"`},
		{"windows", `~\p`, `C:\h\p`, `C:\h\p`}, // backslashes are not escapes, and cmd does not expand "~"
		{"windows", `~\My P`, `C:\h\My P`, `"C:\h\My P"`},
		{"windows", `~\a&b`, `C:\h\a&b`, `"C:\h\a&b"`},
	} {
		if got := shellPath(c.goos, c.pretty, c.abs); got != c.want {
			t.Errorf("shellPath(%s, %q) = %s, want %s", c.goos, c.abs, got, c.want)
		}
	}

	e.mustFail(ExitUsage, []string{"unknown command: nope", "Run 'aiplam --help' for the commands."}, "help", "nope")
	_, errOut := e.mustRun(ExitUsage, "help", "nope")
	if contains(errOut, "Getting started") {
		t.Fatalf("the whole help was printed: %s", errOut)
	}
}

// TestFileLinksNotAllowed checks that, where links to files cannot be created (Windows without developer mode or
// administrator rights), the artifacts that are files are skipped with a warning, and linked once they can be.
func TestFileLinksNotAllowed(t *testing.T) {
	e := newEnv(t)
	allowed := false
	e.app.FileLinks = func() bool { return allowed }
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	pb := e.playbook("pb", "x-a")
	e.file("pb/subagents/x-rev.md")
	e.file("pb/rules/x-style.md")
	agent := filepath.Join(e.home, ".claude", "agents", "x-rev.md")
	rule := filepath.Join(e.home, ".claude", "rules", "x-style.md")

	// add: the skill is linked, the files are skipped with a warning, and the playbook is registered.
	e.mustFail(ExitOK, []string{"2 artifact(s) were not linked, as they are files",
		"subagents x-rev.md of playbook x (profile default) -> " + filepath.Join(pb, "subagents", "x-rev.md"),
		"rules x-style.md of playbook x (profile default)", "Added x."}, "add", "x", pb)
	e.assertLink(filepath.Join(e.home, ".claude", "skills", "x-a"), filepath.Join(pb, "skills", "x-a"))
	e.assertMissing(agent)
	e.assertMissing(rule)
	if len(e.profile(e.home, "default").Playbooks) != 1 {
		t.Fatal("the playbook was not registered")
	}

	// status: reported, not counted; list: mentioned.
	e.mustFail(ExitOK, []string{"2 artifact(s) cannot be linked", "Everything else is up to date."}, "status")
	e.mustFail(ExitOK, []string{"2 artifact(s) are not linked, as they are files"}, "list")

	// update: something in the way of a skipped link is no conflict, as nothing is created there.
	e.file("home/.claude/agents/x-rev.md")
	e.mustFail(ExitOK, []string{"up to date, except the files that cannot be linked", "2 artifact(s) were not linked"},
		"update", "x")
	if info, err := os.Lstat(agent); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the file in the way was touched: %v", err)
	}
	if err := os.Remove(agent); err != nil {
		t.Fatal(err)
	}

	// A conflict between playbooks over a skipped link is still refused: it is in the configuration.
	pb2 := e.mkdir(filepath.Join(e.root, "pb2"))
	e.file("pb2/subagents/x-rev.md")
	e.mustFail(ExitFailure, []string{"would also be linked by x (profile default)"}, "add", "y", pb2,
		"-f", "subagents:subagents:x-*")

	// profile enable: the same.
	e.mustRun(ExitOK, "profile", "add", "other", "-a", "claude")
	e.file("pb3/rules/z-rule.md")
	e.mustRun(ExitOK, "add", "z", filepath.Join(e.root, "pb3"), "-p", "other")
	e.mustFail(ExitOK, []string{"1 artifact(s) were not linked", "rules z-rule.md of playbook z (profile other)"},
		"profile", "enable", "other")
	// list: a profile whose only artifacts are skipped files says so, not that 'update' would link them.
	e.mustFail(ExitOK, []string{"Profile other (agent claude)\n  nothing is linked: its playbooks' artifacts are files"},
		"list")
	// ...but with something 'update' would link, too, it points to it.
	e.skill(filepath.Join(e.root, "pb3", "skills"), "z-new")
	e.mustFail(ExitOK, []string{"Profile other (agent claude)\n  nothing is linked ('aiplam status' shows what"}, "list")
	e.mustRun(ExitOK, "profile", "disable", "other")

	// Once links to files can be created: status shows what to update, and update links them.
	allowed = true
	e.mustFail(ExitFailure, []string{"'aiplam update x' would link ~/.claude/agents/x-rev.md", "2 item(s)"}, "status")
	out, errOut := e.mustRun(ExitOK, "update", "x")
	if contains(out+errOut, "not linked") {
		t.Fatalf("unexpected warning:\n%s%s", out, errOut)
	}
	e.assertLink(agent, filepath.Join(pb, "subagents", "x-rev.md"))
	e.assertLink(rule, filepath.Join(pb, "rules", "x-style.md"))
	e.mustFail(ExitOK, []string{"Everything is up to date."}, "status")
	if out, _ := e.mustRun(ExitOK, "list"); contains(out, "are not linked") {
		t.Fatalf("list mentions skipped artifacts:\n%s", out)
	}

	// Existing links to files are still removed when they cannot be created (removing one needs no rights).
	allowed = false
	e.mustRun(ExitOK, "remove", "x")
	e.assertMissing(agent)
	e.assertMissing(rule)
}

// TestFileLinksCheckedOnlyWhenWanted checks that whether links to files can be created (which creates a test link, on
// Windows) is only found out when a plan wants one.
func TestFileLinksCheckedOnlyWhenWanted(t *testing.T) {
	e := newEnv(t)
	checks := 0
	e.app.FileLinks = func() bool { checks++; return false }
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	pb := e.playbook("pb", "x-a")
	for _, args := range [][]string{{"add", "x", pb}, {"status"}, {"list"}, {"update", "x"}} {
		e.mustRun(ExitOK, args...)
	}
	if checks != 0 {
		t.Fatalf("checked %d time(s) without any link to a file to make", checks)
	}
	e.file("pb/rules/x-style.md")
	e.mustRun(ExitOK, "update", "x")
	if checks != 1 {
		t.Fatalf("checked %d time(s) for one update with a link to a file to make, want 1", checks)
	}
}

// git runs git in dir for a test, failing it on error.
func (e *env) git(dir string, args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestUnignoredLinks checks that, in a git repository, the links that git does not ignore are reported (as warnings,
// with the lines to add to .gitignore, and those already committed), and that .gitignore is never written.
func TestUnignoredLinks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.root, "project"))
	e.git(project, "init", "-q")
	pb := e.playbook("pb", "x-a")
	e.file("pb/subagents/x-rev.md")
	e.mustRun(ExitOK, "init", project, "-a", "claude")
	e.app.Cwd = project
	gitignore := filepath.Join(project, ".gitignore")

	// add: warns, with the lines for this playbook's kinds.
	e.mustFail(ExitOK, []string{"2 links that aiplam made are not ignored by git",
		"(aiplam never changes it):\n  /.claude/skills/x-*\n  /.claude/agents/x-*", "Added x."}, "add", "x", pb)
	e.assertMissing(gitignore)

	// status: the same, without counting it.
	e.mustFail(ExitOK, []string{"2 links that aiplam made are not ignored by git", "Everything else is up to date."},
		"status")

	// update: warns about the links it creates.
	e.skill(filepath.Join(pb, "skills"), "x-c")
	e.mustFail(ExitOK, []string{"1 link that aiplam made is not ignored by git", "\n  /.claude/skills/x-*"},
		"update", "x")

	// Only the lines still missing.
	if err := os.WriteFile(gitignore, []byte("/.claude/skills/x-*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut := e.mustRun(ExitOK, "status")
	if !contains(errOut, "1 link that aiplam made is not ignored by git") ||
		!contains(errOut, "\n  /.claude/agents/x-*") || contains(errOut, "/.claude/skills/x-*") {
		t.Fatalf("unexpected warning:\n%s%s", out, errOut)
	}

	// All ignored: nothing to say, and new links (update) stay quiet.
	if err := os.WriteFile(gitignore, []byte("/.claude/skills/x-*\n/.claude/agents/x-*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitOK, []string{"Everything is up to date."}, "status")
	e.skill(filepath.Join(pb, "skills"), "x-b")
	if out, errOut := e.mustRun(ExitOK, "update", "x"); contains(out+errOut, "not ignored") {
		t.Fatalf("unexpected warning:\n%s%s", out, errOut)
	}

	// A link committed anyway: git never ignores tracked files, so it is reported, as committed.
	e.git(project, "add", "-f", ".claude/skills/x-a")
	e.mustFail(ExitOK, []string{"1 link that aiplam made is not ignored by git",
		"1 link is already committed: stop tracking it with 'git rm -r --cached'",
		"\n  " + filepath.Join(project, ".claude", "skills", "x-a")}, "status")
	e.git(project, "rm", "-q", "-r", "--cached", ".claude/skills/x-a") // -r: on Windows, git takes junctions for folders

	// profile enable: warns about the links it creates, of another playbook.
	e.mustRun(ExitOK, "profile", "add", "other", "-a", "claude")
	e.mustRun(ExitOK, "add", "y", e.playbook("pb2", "y-a"), "-p", "other")
	e.mustFail(ExitOK, []string{"1 link that aiplam made is not ignored by git", "\n  /.claude/skills/y-*"},
		"profile", "enable", "other")
	data, err := os.ReadFile(gitignore)
	if err != nil || string(data) != "/.claude/skills/x-*\n/.claude/agents/x-*\n" {
		t.Fatalf(".gitignore was changed: %q", data)
	}

	// When git cannot check them: status says so (once, not counted); add and update stay quiet about it.
	e.app.Git = &gitinfo.Reader{}
	e.mustFail(ExitOK, []string{"cannot check whether the links that aiplam made are ignored by git (git was not " +
		"found in the PATH)", "Everything else is up to date."}, "status")
	e.skill(filepath.Join(pb, "skills"), "x-d")
	if out, errOut := e.mustRun(ExitOK, "update", "x", "-p", "default"); contains(out+errOut, "cannot check") {
		t.Fatalf("unexpected warning:\n%s%s", out, errOut)
	}
	e.app.Git = gitinfo.NewReader()
	broken := e.mkdir(filepath.Join(e.root, "broken"))
	e.mkdir(filepath.Join(broken, ".git")) // not a repository git can use
	e.mustRun(ExitOK, "init", broken, "-a", "claude")
	e.app.Cwd = broken
	e.mustRun(ExitOK, "add", "x", pb)
	e.mustFail(ExitOK, []string{"cannot check whether the links that aiplam made are ignored by git (fatal: "},
		"status")

	// A home folder in a git repository (dotfiles) on Windows: links under %APPDATA% are outside it, so not reported.
	w := newEnv(t)
	w.app.GOOS, w.app.AppData = "windows", w.mkdir(filepath.Join(w.root, "appdata"))
	w.git(w.home, "init", "-q")
	w.mustRun(ExitOK, "init", "-u", "-a", "devin")
	wpb := w.playbook("pb", "x-a")
	w.file("pb/rules/x-style.md")
	out, errOut = w.mustRun(ExitOK, "add", "x", wpb)
	if !contains(errOut, "1 link that aiplam made is not ignored by git") ||
		!contains(errOut, "\n  /.devin/rules/x-*") || contains(errOut, "APPDATA") {
		t.Fatalf("unexpected warning:\n%s%s", out, errOut)
	}
}

func TestGitignorePattern(t *testing.T) {
	for format, want := range map[string]string{"x-*": "x-*", "*.agent.md": "*.agent.md", `a?[b]\c*`: `a\?\[b\]\\c*`,
		"x-* ": `x-*\ `} {
		if got := gitignorePattern(format); got != want {
			t.Errorf("gitignorePattern(%q) = %q, want %q", format, got, want)
		}
	}
}

// TestUnignoredLinksOwnNames checks that the lines to add to .gitignore name each link when the pattern of its format
// would also make git ignore other things in its target folder: a format of only wildcards (such as the skills of the
// modules of a monorepo, linked with their own names), or one matching something there that is not a playbook's link.
func TestUnignoredLinksOwnNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.root, "project"))
	e.git(project, "init", "-q")
	e.mustRun(ExitOK, "init", project, "-a", "claude")
	e.app.Cwd = project

	// Only wildcards: one line per link, even with nothing else in the folder.
	billing := e.playbook("project/billing-backend", "invoice", "refund")
	e.mustFail(ExitOK, []string{"2 links that aiplam made are not ignored by git",
		"(aiplam never changes it):\n  /.claude/skills/invoice\n  /.claude/skills/refund\n"},
		"add", "billing", billing, "--folder", "skills:skills:*")

	// A format matching something of the project's own there: one line per link too.
	e.skill(filepath.Join(project, ".claude", "skills"), "x-own")
	x := e.playbook("pb", "x-a")
	e.file("pb/subagents/x-rev.md")
	out, errOut := e.mustRun(ExitOK, "add", "x", x)
	if !contains(errOut, "\n  /.claude/skills/x-a") || !contains(errOut, "\n  /.claude/agents/x-*") ||
		contains(errOut, "/.claude/skills/x-*") {
		t.Fatalf("unexpected warning:\n%s%s", out, errOut)
	}

	// Links of other playbooks do not count as something else: the pattern is enough.
	e.mustRun(ExitOK, "remove", "x")
	if err := os.RemoveAll(filepath.Join(project, ".claude", "skills", "x-own")); err != nil {
		t.Fatal(err)
	}
	e.mustFail(ExitOK, []string{"\n  /.claude/skills/x-*\n  /.claude/agents/x-*"}, "add", "x", x)
}

// TestUnignoredLinksSpecialFolders checks that the suggested line for a target folder whose name has characters that
// are special in gitignore patterns does make git ignore the link.
func TestUnignoredLinksSpecialFolders(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.root, "project"))
	e.git(project, "init", "-q")
	e.mustRun(ExitOK, "init", project, "-f", "skills:[ai]/skills")
	e.app.Cwd = project
	line := `/\[ai\]/skills/x-*`
	e.mustFail(ExitOK, []string{"\n  " + line + "\n"}, "add", "x", e.playbook("pb", "x-a"))
	if err := os.WriteFile(filepath.Join(project, ".gitignore"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := exec.Command("git", "-C", project, "check-ignore", "-q", filepath.Join("[ai]", "skills", "x-a"))
	if err := check.Run(); err != nil {
		t.Fatalf("the suggested line does not make git ignore the link: %v", err)
	}
}

func TestGitignoreName(t *testing.T) {
	for name, want := range map[string]string{"invoice": "invoice", `a*?[b]\c`: `a\*\?\[b\]\\c`, "a b  ": `a b\ \ `} {
		if got := gitignoreName(name); got != want {
			t.Errorf("gitignoreName(%q) = %q, want %q", name, got, want)
		}
	}
	for folder, want := range map[string]string{".claude/skills": ".claude/skills", "[ai]/x*": `\[ai\]/x\*`} {
		if got := gitignoreFolder(folder); got != want {
			t.Errorf("gitignoreFolder(%q) = %q, want %q", folder, got, want)
		}
	}
	for _, c := range []struct {
		pattern string
		names   []string
		want    bool
	}{{"x-*", nil, false}, {"x-*", []string{"y-a", ".x-b"}, false}, {"x-*", []string{"y-a", "X-Own"}, true},
		{`a\[b`, []string{"a[b"}, true}, {"[", []string{"z"}, true}} {
		if got := matchesAny(c.pattern, c.names); got != c.want {
			t.Errorf("matchesAny(%q, %q) = %v, want %v", c.pattern, c.names, got, c.want)
		}
	}
}

// TestDrivePathsInFolder checks that an absolute Windows path in --folder, split at its drive letter's colon, is
// reported as not relative.
func TestDrivePathsInFolder(t *testing.T) {
	e := newEnv(t)
	e.mustRun(ExitOK, "init", "-u", "-a", "claude")
	pb := e.playbook("pb", "x-a")
	e.mustFail(ExitUsage, []string{`--folder skills: playbook subfolder 'C:\x' must be a relative path`},
		"add", "x", pb, "-f", `skills:C:\x`)
	e.mustFail(ExitUsage, []string{`--folder skills: target folder 'D:/x' must be a relative path`},
		"profile", "add", "p", "-f", "skills:D:/x")
	// A one-letter subfolder followed by a format is not a drive.
	e.mustRun(ExitOK, "add", "x", pb, "-f", "skills:s:x-*")
}
