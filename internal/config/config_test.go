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

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"arxila.io/aiplam/internal/fsutil"
)

// TestTemplateTable checks the invariants the rest of aiplam relies on.
func TestTemplateTable(t *testing.T) {
	if TemplateTable[0].Name != "standard" {
		t.Fatalf("the standard template must come first")
	}
	seen := map[string]bool{}
	for _, tmpl := range TemplateTable {
		if seen[tmpl.Name] || tmpl.Name == CustomAgent || strings.Contains(tmpl.Name, ":") {
			t.Errorf("template name %q repeated, reserved or invalid", tmpl.Name)
		}
		seen[tmpl.Name] = true
		if tmpl.Product == "" {
			t.Errorf("%s: no product", tmpl.Name)
		}
		for _, user := range []bool{false, true} {
			for _, goos := range []string{"linux", "darwin", "windows"} {
				for _, spec := range []string{tmpl.Name, tmpl.Name + StandardVariant} {
					targets, ok := TemplateTargets(spec, user, goos)
					if !ok {
						if spec == tmpl.Name || tmpl.Standard {
							t.Errorf("%s: not found", spec)
						}
						continue
					}
					if len(targets) == 0 {
						t.Errorf("%s (user %v, %s): no targets", spec, user, goos)
					}
					if errs := CheckTargets(targets, user); len(errs) > 0 {
						t.Errorf("%s (user %v, %s): %v", spec, user, goos, errs)
					}
				}
			}
		}
	}
}

func TestTemplateTargets(t *testing.T) {
	check := func(spec string, user bool, goos, want string) {
		t.Helper()
		targets, ok := TemplateTargets(spec, user, goos)
		if !ok {
			t.Fatalf("%s: not found", spec)
		}
		var got []string
		for _, tt := range targets {
			got = append(got, tt.Kind+"="+tt.Folder)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%s (user %v, %s) = %s, want %s", spec, user, goos, strings.Join(got, " "), want)
		}
	}
	check("claude", false, "linux", "skills=.claude/skills subagents=.claude/agents commands=.claude/commands "+
		"rules=.claude/rules")
	check("copilot", false, "linux", "skills=.github/skills subagents=.github/agents prompts=.github/prompts "+
		"instructions=.github/instructions")
	// In the user managed folder: other folders, and kinds that are only read in projects are left out.
	check("copilot", true, "linux", "skills=.copilot/skills subagents=.copilot/agents "+
		"instructions=.copilot/instructions")
	check("copilot:standard", true, "linux", "skills=.agents/skills subagents=.copilot/agents "+
		"instructions=.copilot/instructions")
	check("junie:standard", false, "darwin", "skills=.agents/skills subagents=.junie/agents "+
		"commands=.junie/commands rules=.junie/rules")
	check("junie", true, "darwin", "skills=.junie/skills subagents=.junie/agents commands=.junie/commands")
	check("codex", false, "linux", "skills=.agents/skills subagents=.codex/agents")
	check("codex:standard", false, "linux", "skills=.agents/skills subagents=.codex/agents")
	check("devin", true, "linux", "skills=.config/devin/skills subagents=.config/devin/agents rules=.devin/rules")
	check("devin", true, "windows", "skills=%APPDATA%/devin/skills subagents=%APPDATA%/devin/agents "+
		"rules=.devin/rules")
	check("devin", false, "windows", "skills=.devin/skills subagents=.devin/agents rules=.devin/rules")
	check("standard", true, "linux", "skills=.agents/skills")

	for _, spec := range []string{"claude:standard", "standard:standard", "unknown", "", ":standard", "custom"} {
		if _, ok := TemplateTargets(spec, false, "linux"); ok {
			t.Errorf("%q should not be a template", spec)
		}
	}
	names := TemplateList()
	if !strings.HasPrefix(names, "standard, claude, copilot, copilot:standard, cursor") ||
		strings.Contains(names, "claude:standard") {
		t.Errorf("TemplateList() = %s", names)
	}
}

func TestCleanTarget(t *testing.T) {
	for _, c := range []struct {
		folder string
		user   bool
		want   string // "" for an error
	}{
		{".claude/skills", false, ".claude/skills"},
		{"./a//b/", false, "a/b"},
		{"a/./b", false, "a/b"},
		{"my folder/x", false, "my folder/x"},
		{"../x", false, ""},
		{"a/../b", false, ""},
		{"/abs", false, ""},
		{"~/x", false, ""},
		{".", false, ""},
		{"", false, ""},
		{".aiplam/x", false, ""},
		{".AIPLAM", false, ""},
		{"%APPDATA%/devin/skills", false, ""},
		{"%APPDATA%/devin/skills", true, "%APPDATA%/devin/skills"},
		{"%APPDATA%/a/../b", true, ""},
		{"%APPDATA%", true, ""},
		{"%APPDATA%/", true, ""},
	} {
		got, err := CleanTarget(c.folder, c.user)
		if (err != nil) != (c.want == "") || got != c.want {
			t.Errorf("CleanTarget(%q, %v) = %q, %v; want %q", c.folder, c.user, got, err, c.want)
		}
	}
}

func TestCleanSubfolderAndFormat(t *testing.T) {
	for folder, want := range map[string]string{".": ".", "./": ".", "skills": "skills", "ai//skills/": "ai/skills",
		"..": "", "a/../b": "", "/x": "", "~/x": "", "": ""} {
		got, err := CleanSubfolder(folder)
		if (err != nil) != (want == "") || got != want {
			t.Errorf("CleanSubfolder(%q) = %q, %v; want %q", folder, got, err, want)
		}
	}
	for format, ok := range map[string]bool{"*": true, "x-*": true, "*.agent.md": true, "": false, "a/b": false,
		`a\b`: false, ".x*": false} {
		if err := CheckFormat(format); (err == nil) != ok {
			t.Errorf("CheckFormat(%q) = %v", format, err)
		}
	}
}

func TestMatchFormat(t *testing.T) {
	for _, c := range []struct {
		format, name string
		want         bool
	}{
		{"*", "anything", true},
		{"*", ".hidden", false},
		{"x-*", ".x-a", false},
		{"x-*", "x-a", true},
		{"x-*", "x-", true},
		{"x-*", "y-a", false},
		{"*.md", "a.md", true},
		{"*.md", "a.md.txt", false},
		{"*.agent.md", "a.agent.md", true},
		{"*.agent.md", "a.md", false},
		{"a*b*c", "abc", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "ac", false},
		{"a*b*b", "ab", false},
		{"a*b*b", "abb", true},
		{"ab*ba", "aba", false},
		{"exact", "exact", true},
		{"exact", "exactly", false},
		{"**", "x", true},
	} {
		if got := MatchFormat(c.format, c.name); got != c.want {
			t.Errorf("MatchFormat(%q, %q) = %v, want %v", c.format, c.name, got, c.want)
		}
	}
}

func TestCheckTargets(t *testing.T) {
	errs := CheckTargets(Targets{{"skills", ".claude/skills"}, {"skills", ".x"}, {"a", ".claude/skills"},
		{"b", ".claude"}, {"bad:kind", "y"}, {"c", "../z"}}, false)
	want := []string{"kind 'skills' has more than one target folder", "same target folder",
		"one inside the other", "invalid kind 'bad:kind'", "cannot contain '..'"}
	text := fmt.Sprint(errs)
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q in %s", w, text)
		}
	}
	// ".claude" and ".claude-x" are not one inside the other.
	if errs := CheckTargets(Targets{{"a", ".claude"}, {"b", ".claude-x"}}, false); len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	for kind, ok := range map[string]bool{"skills": true, "my-prompts": true, "": false, ".x": false, "a b": false,
		"a,b": false, "a/b": false, `a"b`: false} {
		if ValidKind(kind) != ok {
			t.Errorf("ValidKind(%q) != %v", kind, ok)
		}
	}
}

func TestProfileFiles(t *testing.T) {
	managed := fsutil.Canonical(t.TempDir())
	if _, err := LoadProfile(managed, "default"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("missing profile: %v", err)
	}
	prof := &Profile{Name: "default", Agent: "claude",
		Targets: Targets{{"skills", ".claude/skills"}, {"zeta", "z"}, {"alpha", "a"}},
		Playbooks: []Playbook{{Name: "x", Folder: "../pb", Mappings: map[string]Mapping{
			"skills": {Folder: ".", Format: "*"}, "alpha": {Folder: "a"}}}}}
	if err := SaveProfile(managed, prof); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ProfileFile(managed, "default"))
	if err != nil {
		t.Fatal(err)
	}
	// The targets keep their order.
	if !strings.Contains(string(data), "targets:\n  skills: .claude/skills\n  zeta: z\n  alpha: a\n") {
		t.Fatalf("unexpected file:\n%s", data)
	}
	got, err := LoadProfile(managed, "default")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "default" || got.Agent != "claude" || len(got.Targets) != 3 || got.Targets[1].Kind != "zeta" ||
		got.Playbooks[0].Mappings["skills"].Format != "*" || got.Playbooks[0].Mappings["alpha"].Folder != "a" {
		t.Fatalf("round trip: %+v", got)
	}
	if errs := got.Validate(managed, "default", false); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	folder, format := got.Playbooks[0].Source("skills")
	if folder != "." || format != "*" {
		t.Errorf("Source(skills) = %s, %s", folder, format)
	}
	if folder, format := got.Playbooks[0].Source("alpha"); folder != "a" || format != "x-*" {
		t.Errorf("Source(alpha) = %s, %s", folder, format)
	}
	if folder, format := got.Playbooks[0].Source("zeta"); folder != "zeta" || format != "x-*" {
		t.Errorf("Source(zeta) = %s, %s", folder, format)
	}

	// Unknown fields and targets that are not a mapping are rejected.
	for _, content := range []string{"name: default\nfoo: 1\n", "name: default\ntargets: [a, b]\n",
		"name: default\ntargets:\n  skills: [a]\n"} {
		if err := os.WriteFile(ProfileFile(managed, "default"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadProfile(managed, "default"); err == nil {
			t.Errorf("%q: want an error", content)
		}
	}
	// An empty file is an empty profile, which is not valid.
	if err := os.WriteFile(ProfileFile(managed, "default"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty, err := LoadProfile(managed, "default")
	if err != nil || len(empty.Validate(managed, "default", false)) != 2 {
		t.Fatalf("empty profile: %v, %v", err, empty.Validate(managed, "default", false))
	}
}

func TestProfileValidate(t *testing.T) {
	managed := fsutil.Canonical(t.TempDir())
	prof := &Profile{Name: "other", Agent: "nope", Targets: Targets{{"skills", "%APPDATA%/x"}},
		Playbooks: []Playbook{
			{Name: "", Folder: ""},
			{Name: "x", Folder: "/abs", Mappings: map[string]Mapping{"rules": {Folder: "r"},
				"skills": {Folder: "../out", Format: ".x"}}},
			{Name: "y", Folder: "pb"},
			{Name: "y", Folder: "pb"},
			{Name: "y", Folder: "other/pb"},
		}}
	text := fmt.Sprint(prof.Validate(managed, "default", false))
	for _, want := range []string{"name 'other' does not match", "unknown agent 'nope'", "can only be used in the user",
		"playbooks[0] has no name", "playbooks[0] has no folder", "must be a path relative", "'rules' is not a kind",
		"cannot contain '..'", "only match hidden", "registered more than once", "ids collide"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	// The name must be the file's, exactly (even on file systems that ignore case, so that the profile is saved back
	// to the same file everywhere); custom is a valid agent.
	ok := &Profile{Name: "default", Agent: CustomAgent, Targets: Targets{{"skills", "%APPDATA%/x"}}}
	if errs := ok.Validate(managed, "default", true); len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	ok.Name = "Default"
	if errs := ok.Validate(managed, "default", true); len(errs) != 1 ||
		!strings.Contains(errs[0].Error(), "it must be 'default'") {
		t.Errorf("case mismatch: %v", errs)
	}
	// ProfileName finds the file's name.
	if err := SaveProfile(managed, &Profile{Name: "work"}); err != nil {
		t.Fatal(err)
	}
	if got := ProfileName(managed, "work"); got != "work" {
		t.Errorf("ProfileName(work) = %s", got)
	}
	want := "Work" // a file system that distinguishes case has no file for Work
	if ProfileExists(managed, "Work") {
		want = "work"
	}
	if got := ProfileName(managed, "Work"); got != want {
		t.Errorf("ProfileName(Work) = %s, want %s", got, want)
	}
}

func TestConfigFiles(t *testing.T) {
	managed := fsutil.Canonical(t.TempDir())
	if _, err := LoadConfig(managed); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("missing configuration: %v", err)
	}
	if err := SaveConfig(managed, &Config{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(ConfigFile(managed))
	if !strings.Contains(string(data), "enabled: []") {
		t.Fatalf("unexpected file:\n%s", data)
	}
	if err := SaveConfig(managed, &Config{Enabled: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	conf, err := LoadConfig(managed)
	if err != nil || strings.Join(conf.Enabled, ",") != "a,b" || !conf.IsEnabled(managed, "a") ||
		conf.IsEnabled(managed, "c") {
		t.Fatalf("round trip: %+v, %v", conf, err)
	}
	bad := &Config{Enabled: []string{"a", "A", "../x"}}
	if errs := bad.Validate(); len(errs) != 2 {
		t.Fatalf("errors: %v", errs)
	}
	if err := SaveProfile(managed, &Profile{Name: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(managed), "x.yml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if profiles, err := Profiles(managed); err != nil || strings.Join(profiles, ",") != "b" {
		t.Fatalf("Profiles = %v, %v", profiles, err)
	}
}

// TestFileContents checks what configuration files may hold: one YAML document (a final '---' is harmless), and a
// folder for every target kind.
func TestFileContents(t *testing.T) {
	managed := fsutil.Canonical(t.TempDir())
	write := func(file, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for content, ok := range map[string]bool{
		"enabled: [a]\n":                    true,
		"enabled: [a]\n---\n":               true,
		"enabled: [a]\n---\nenabled: [b]\n": false,
	} {
		write(ConfigFile(managed), content)
		if _, err := LoadConfig(managed); (err == nil) != ok {
			t.Errorf("LoadConfig(%q): %v", content, err)
		}
	}
	for _, folder := range []string{"", "null", "~"} {
		write(ProfileFile(managed, "p"), "name: p\ntargets:\n  skills: "+folder+"\n")
		if _, err := LoadProfile(managed, "p"); err == nil || !strings.Contains(err.Error(), "has no target folder") {
			t.Errorf("target folder %q: %v", folder, err)
		}
	}
	write(ProfileFile(managed, "p"), "name: p\ntargets:\n  skills: 'null'\n")
	if prof, err := LoadProfile(managed, "p"); err != nil || prof.Targets[0].Folder != "null" {
		t.Errorf("a folder named null, quoted: %+v, %v", prof, err)
	}
}

// TestSaveKeepsPermissions checks that saving a file keeps the permissions it had.
func TestSaveKeepsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no such permissions")
	}
	managed := fsutil.Canonical(t.TempDir())
	if err := SaveProfile(managed, &Profile{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	file := ProfileFile(managed, "p")
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(managed, &Profile{Name: "p", Agent: CustomAgent}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions after saving: %v, %v", info.Mode(), err)
	}
}

func TestNames(t *testing.T) {
	for name, want := range map[string]bool{"x": true, "x.y_z-1": true, "-x": false, "x@y": false, "x/y": false,
		"": false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) != %v", name, want)
		}
	}
	for name, want := range map[string]bool{"work": true, "a_b-1": true, "a.b": false, ConfigName: false,
		"AIPLAM-CONFIG": false} {
		if ValidProfileName(name) != want {
			t.Errorf("ValidProfileName(%q) != %v", name, want)
		}
	}
	for folder, want := range map[string]bool{"pb": true, "../pb": true, "/pb": false, "~/pb": false, "": false} {
		if ValidFolder(folder) != want {
			t.Errorf("ValidFolder(%q) != %v", folder, want)
		}
	}
}

func TestFindManaged(t *testing.T) {
	root := fsutil.Canonical(t.TempDir())
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "w", "project")
	outside := filepath.Join(root, "outside", "deep")
	for _, dir := range []string{filepath.Join(project, "a", "b"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mkManaged := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	check := func(start, want string) {
		t.Helper()
		got, ok := FindManaged(start, home)
		if (want == "") == ok || (ok && got != want) {
			t.Errorf("FindManaged(%s) = %s, %v; want %q", start, got, ok, want)
		}
	}
	// Nothing managed.
	check(filepath.Join(project, "a"), "")
	check(outside, "")
	// Above home is never considered from inside it: the walk stops at home.
	mkManaged(root)
	check(filepath.Join(project, "a"), "")
	// Outside home, the walk goes up to the root of the file system.
	check(outside, root)
	// A managed home applies from inside it, and from outside when nothing else is managed.
	mkManaged(home)
	check(filepath.Join(project, "a"), home)
	check(home, home)
	if err := os.Remove(Dir(root)); err != nil {
		t.Fatal(err)
	}
	check(outside, home)
	// The nearest managed folder wins.
	mkManaged(project)
	check(filepath.Join(project, "a", "b"), project)
	check(project, project)
	mkManaged(filepath.Join(project, "a"))
	check(filepath.Join(project, "a", "b"), filepath.Join(project, "a"))
}

func TestTargetDir(t *testing.T) {
	dir, stop, err := TargetDir("/m", ".claude/skills", "")
	if err != nil || dir != filepath.Join("/m", ".claude", "skills") || stop != "/m" {
		t.Errorf("TargetDir = %s, %s, %v", dir, stop, err)
	}
	dir, stop, err = TargetDir("/m", "%APPDATA%/devin/skills", "/appdata")
	if err != nil || dir != filepath.Join("/appdata", "devin", "skills") || stop != "/appdata" {
		t.Errorf("TargetDir = %s, %s, %v", dir, stop, err)
	}
	if _, _, err := TargetDir("/m", "%APPDATA%/devin/skills", ""); err == nil {
		t.Errorf("want an error without APPDATA")
	}
	if runtime.GOOS == "windows" { // written with backslashes, as CleanTarget accepts them there
		dir, stop, err = TargetDir(`C:\m`, `%APPDATA%\devin\skills`, `C:\appdata`)
		if err != nil || dir != `C:\appdata\devin\skills` || stop != `C:\appdata` {
			t.Errorf("TargetDir with backslashes = %s, %s, %v", dir, stop, err)
		}
	}
}

// TestReadmeTemplates checks that the README's table of agent templates lists every template, in the order of
// TemplateTable.
func TestReadmeTemplates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(data), "\n") {
		if name, ok := strings.CutPrefix(line, "| `"); ok {
			if name, _, ok := strings.Cut(name, "`"); ok {
				if _, _, known := LookupTemplate(name); known && !strings.Contains(name, ":") {
					listed = append(listed, name)
				}
			}
		}
	}
	var want []string
	for _, tmpl := range TemplateTable {
		want = append(want, tmpl.Name)
	}
	if got := strings.Join(listed, ","); got != strings.Join(want, ",") {
		t.Fatalf("README lists the templates as %s, want %s", got, strings.Join(want, ","))
	}
}
