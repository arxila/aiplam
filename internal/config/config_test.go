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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"arxila.io/aiplam/internal/fsutil"
)

// TestAgentTable checks the invariants the rest of aiplam relies on.
func TestAgentTable(t *testing.T) {
	if _, ok := LookupAgent(DefaultAgent); !ok {
		t.Fatalf("the default agent %q is not in the table", DefaultAgent)
	}
	if _, ok := LookupAgent("nope"); ok {
		t.Fatal("an unknown agent was found")
	}
	seen := map[string]bool{}
	for _, ag := range AgentTable {
		if ag.Name == "" || ag.Product == "" {
			t.Errorf("agent %+v has no name or product", ag)
		}
		if seen[ag.Name] {
			t.Errorf("agent %s is listed twice", ag.Name)
		}
		seen[ag.Name] = true
		if _, ok := ag.Kinds[Skills]; !ok {
			t.Errorf("agent %s does not read skills", ag.Name)
		}
		for k, s := range ag.Kinds {
			known := false
			for _, kind := range Kinds {
				known = known || kind == k
			}
			switch {
			case !known:
				t.Errorf("agent %s: unknown kind %s", ag.Name, k)
			case s.Project.IsZero():
				t.Errorf("agent %s: %s are not read in projects", ag.Name, k)
			case (s.Format.File == "") == (s.Format.Ext == ""):
				t.Errorf("agent %s: %s need either a file or an extension", ag.Name, k)
			case s.Format.Ext != "" && !strings.HasPrefix(s.Format.Ext, "."):
				t.Errorf("agent %s: %s extension %q does not start with a dot", ag.Name, k, s.Format.Ext)
			case (k == Skills) != (s.Format.File == "SKILL.md"):
				t.Errorf("agent %s: %s have the wrong format %+v", ag.Name, k, s.Format)
			}
			for _, p := range []Place{s.Project, s.Home} {
				if !p.IsZero() && (p.Sub == "" || strings.HasPrefix(p.Dir, "/") || strings.Contains(p.Dir, "..")) {
					t.Errorf("agent %s: %s place %+v is not a relative folder", ag.Name, k, p)
				}
			}
		}
		for _, k := range ag.ReadsStandard {
			if _, ok := ag.Kinds[k]; !ok {
				t.Errorf("agent %s reads standard %s but not its own", ag.Name, k)
			}
		}
	}
	if strings.Join(AgentNames(), ",") == "" || AgentNames()[0] != DefaultAgent {
		t.Fatalf("agent names = %v", AgentNames())
	}
}

func TestPlaces(t *testing.T) {
	copilot, _ := LookupAgent("copilot")
	for _, c := range []struct {
		agent string
		kind  Kind
		home  bool
		want  string // "" when not read there
	}{
		{"copilot", Skills, false, ".github/skills"},
		{"copilot", Skills, true, ".copilot/skills"},
		{"copilot", Prompts, false, ".github/prompts"},
		{"copilot", Prompts, true, ""},
		{"codex", Skills, true, ".agents/skills"},
		{"codex", Subagents, false, ".codex/agents"},
		{"devin", Skills, true, ".config/devin/skills"},
		{"devin", Rules, true, ".devin/rules"},
		{"opencode", Commands, true, ".config/opencode/commands"},
		{"cursor", Rules, true, ""},
		{"standard", Rules, false, ""},
	} {
		ag, _ := LookupAgent(c.agent)
		p, ok := ag.Place(c.kind, c.home)
		got := ""
		if ok {
			got = p.Dir + "/" + p.Sub
		}
		if got != c.want {
			t.Errorf("%s %s (home %v) = %q, want %q", c.agent, c.kind, c.home, got, c.want)
		}
	}

	base := filepath.Join(string(filepath.Separator), "base")
	folders := copilot.KindFolders(base, true)
	if len(folders) != 2 || folders[0].Kind != Skills || folders[1].Kind != Subagents {
		t.Fatalf("copilot home folders = %+v", folders)
	}
	f := folders[1]
	if f.Agent != "copilot" || f.DefaultPath != "agents" || f.Format.Ext != ".agent.md" ||
		f.Dir != filepath.Join(base, ".copilot", "agents") || f.AgentsConfigDir != filepath.Join(base, ".copilot") {
		t.Fatalf("copilot subagents folder = %+v", f)
	}
	opencode, _ := LookupAgent("opencode")
	if f := opencode.KindFolders(base, true)[0]; f.AgentsConfigDir != filepath.Join(base, ".config", "opencode") {
		t.Fatalf("opencode home folder = %+v", f)
	}
	junie, _ := LookupAgent("junie")
	if got := junie.SupportedKinds(); len(got) != 4 || got[3] != Rules {
		t.Fatalf("junie kinds = %v", got)
	}
}

func TestItemExt(t *testing.T) {
	for name, want := range map[string]string{
		"x-r.md":                 ".md",
		"x-r.agent.md":           ".agent.md",
		"x-p.prompt.md":          ".prompt.md",
		"x-i.instructions.md":    ".instructions.md",
		"x-c.toml":               ".toml",
		"x-r.mdc":                ".mdc",
		"x-readme":               "",
		".agent.md":              ".md", // only an extension: not a compound one
		"x-notes.agent.md.bak":   ".bak",
		"x-agent.md":             ".md",
		"x-r.other.instructions": ".instructions",
	} {
		if got := ItemExt(name); got != want {
			t.Errorf("ItemExt(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPlaybookPaths(t *testing.T) {
	p := Playbook{Name: "x", Folder: "pb"}
	if p.Path(Subagents, "agents") != "agents" || p.PathSet(Subagents) {
		t.Fatal("an unset path should give the default")
	}
	for _, k := range Kinds {
		p.Artifacts.Kind(k).Path = "ai/" + string(k)
	}
	if p.Artifacts.Subagents.Path != "ai/subagents" || p.Artifacts.Instructions.Path != "ai/instructions" ||
		p.Artifacts.Skills.Path != "ai/skills" {
		t.Fatalf("artifacts = %+v", p.Artifacts)
	}
	if p.Path(Rules, "rules") != "ai/rules" || !p.PathSet(Rules) {
		t.Fatal("a set path should win over the default")
	}
	base := fsutil.Canonical(t.TempDir())
	if got := p.PathDir(base, "ai/skills"); got != filepath.Join(base, "pb", "ai", "skills") {
		t.Fatalf("PathDir = %s", got)
	}
}

func TestProfileFiles(t *testing.T) {
	base := fsutil.Canonical(t.TempDir())
	prof := &Profile{Agent: "claude", Playbooks: []Playbook{{Name: "x", Folder: "../pb",
		Artifacts: Artifacts{Subagents: KindSettings{Path: "ai/subagents"}}}}}
	if err := SaveProfile(base, "work", prof); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ProfileFile(base, "work"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "agent: claude\n") || !strings.Contains(text, "subagents:\n") ||
		strings.Contains(text, "skills:") || strings.Contains(text, "commands:") {
		t.Fatalf("unexpected file:\n%s", text)
	}
	loaded, err := LoadProfile(base, "work")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent != "claude" || len(loaded.Playbooks) != 1 ||
		loaded.Playbooks[0].Artifacts.Subagents.Path != "ai/subagents" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if errs := loaded.Validate(base); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}

	// No agent, or an empty file: the default agent.
	for _, content := range []string{"", "playbooks: []\n"} {
		if err := os.WriteFile(ProfileFile(base, "old"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if p, err := LoadProfile(base, "old"); err != nil || p.Agent != DefaultAgent {
			t.Fatalf("%q: profile %+v, err %v", content, p, err)
		}
	}
	if err := SaveProfile(base, "empty", &Profile{}); err != nil {
		t.Fatal(err)
	}
	if p, err := LoadProfile(base, "empty"); err != nil || p.Agent != DefaultAgent || p.Playbooks == nil {
		t.Fatalf("empty profile %+v, err %v", p, err)
	}
	if _, err := LoadProfile(base, "missing"); err != ErrNoProfile {
		t.Fatalf("missing profile: %v", err)
	}
	if names, err := Profiles(base); err != nil || strings.Join(names, ",") != "empty,old,work" {
		t.Fatalf("profiles = %v (%v)", names, err)
	}
}

func TestProfileValidate(t *testing.T) {
	base := fsutil.Canonical(t.TempDir())
	for _, c := range []struct {
		prof Profile
		want string
	}{
		{Profile{Agent: "nope"}, "unknown agent 'nope'"},
		{Profile{Playbooks: []Playbook{{Folder: "pb"}}}, "has no name"},
		{Profile{Playbooks: []Playbook{{Name: "x@y", Folder: "pb"}}}, "invalid name"},
		{Profile{Playbooks: []Playbook{{Name: "x"}}}, "has no folder"},
		{Profile{Playbooks: []Playbook{{Name: "x", Folder: "/abs"}}}, "must be a path relative"},
		{Profile{Playbooks: []Playbook{{Name: "x", Folder: "pb",
			Artifacts: Artifacts{Rules: KindSettings{Path: "../out"}}}}}, "artifacts.rules.path '../out'"},
		{Profile{Playbooks: []Playbook{{Name: "x", Folder: "pb"}, {Name: "x", Folder: "pb"}}}, "more than once"},
		{Profile{Playbooks: []Playbook{{Name: "x", Folder: "a/pb"}, {Name: "x", Folder: "b/pb"}}}, "ids collide"},
	} {
		var msgs []string
		for _, err := range c.prof.Validate(base) {
			msgs = append(msgs, err.Error())
		}
		if !strings.Contains(strings.Join(msgs, "\n"), c.want) {
			t.Errorf("%+v: errors %q, want %q", c.prof, msgs, c.want)
		}
	}
	ok := Profile{Agent: "junie", Playbooks: []Playbook{{Name: "x", Folder: "a/pb"}, {Name: "x", Folder: "b/other"}}}
	if errs := ok.Validate(base); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
}

func TestBaseConfigFiles(t *testing.T) {
	base := fsutil.Canonical(t.TempDir())
	if _, err := LoadBaseConfig(base); err != ErrNotInitialized {
		t.Fatalf("missing configuration: %v", err)
	}
	if err := SaveBaseConfig(base, &BaseConfig{}); err != nil {
		t.Fatal(err)
	}
	conf, err := LoadBaseConfig(base)
	if err != nil || conf.Enabled == nil || len(conf.Enabled) != 0 {
		t.Fatalf("configuration %+v, err %v", conf, err)
	}
	// A temporary file left by a crashed process whose id this one reuses is replaced.
	stale := fmt.Sprintf("%s.%d.tmp", BaseConfigFile(base), os.Getpid())
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveBaseConfig(base, &BaseConfig{}); err != nil {
		t.Fatalf("saving over a stale temporary file: %v", err)
	}
	// Saved files have the permissions of any new file (not os.CreateTemp's 0600), and no temporary file is left.
	if entries, _ := os.ReadDir(filepath.Dir(BaseConfigFile(base))); len(entries) != 1 {
		t.Fatalf("files left in the configuration folder: %v", entries)
	}
	reference := filepath.Join(base, "reference")
	if err := os.WriteFile(reference, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	saved, err1 := os.Stat(BaseConfigFile(base))
	ref, err2 := os.Stat(reference)
	if err1 != nil || err2 != nil || saved.Mode().Perm() != ref.Mode().Perm() {
		t.Fatalf("saved file: %v (a new file: %v), %v %v", saved.Mode(), ref.Mode(), err1, err2)
	}
	for content, want := range map[string]string{
		"types: [agents]\nenabled: [x]\n": "invalid file format",
		"":                                "empty",
	} {
		if err := os.WriteFile(BaseConfigFile(base), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBaseConfig(base); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", content, err, want)
		}
	}
	bad := &BaseConfig{Enabled: []string{"a", "A", "../x"}}
	if errs := bad.Validate(); len(errs) != 2 {
		t.Fatalf("errors: %v", errs)
	}
}

func TestNames(t *testing.T) {
	for name, want := range map[string]bool{"x": true, "x.y_z-1": true, "-x": false, "x@y": false, "x/y": false,
		"": false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) != %v", name, want)
		}
	}
	for name, want := range map[string]bool{"work": true, "a_b-1": true, "a.b": false, BaseConfigName: false,
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
	for path, want := range map[string]bool{".": true, "ai/skills": true, "../x": false, "": false, "/x": false} {
		if ValidArtifactPath(path) != want {
			t.Errorf("ValidArtifactPath(%q) != %v", path, want)
		}
	}
}

func TestFindBaseAndAncestors(t *testing.T) {
	root := fsutil.Canonical(t.TempDir())
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "ws", "project")
	module := filepath.Join(project, "module")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{Dir(home), Dir(project), module, Dir(outside)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := FindBase(module, home); got != project {
		t.Fatalf("FindBase(module) = %s", got)
	}
	if got := FindBase(filepath.Join(home, "ws"), home); got != home {
		t.Fatalf("FindBase(ws) = %s", got)
	}
	if got := FindBase(root, home); got != home {
		t.Fatalf("FindBase(root) = %s, want the home folder", got)
	}
	if got := Ancestors(project, home); len(got) != 1 || got[0] != home {
		t.Fatalf("Ancestors(project) = %v", got)
	}
	if got := Ancestors(outside, home); len(got) != 1 || got[0] != home {
		t.Fatalf("Ancestors(outside) = %v, want the home folder", got)
	}
	if got := Ancestors(home, home); len(got) != 0 {
		t.Fatalf("Ancestors(home) = %v", got)
	}
}

// TestReadmeAgents checks that the README lists every agent, in the order of the table.
func TestReadmeAgents(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(data), "\n") {
		if name, ok := strings.CutPrefix(line, "- `"); ok {
			if name, _, ok := strings.Cut(name, "`, "); ok {
				if _, known := LookupAgent(name); known {
					listed = append(listed, name)
				}
			}
		}
	}
	if got, want := strings.Join(listed, ","), strings.Join(AgentNames(), ","); got != want {
		t.Fatalf("README lists the agents as %s, want %s", got, want)
	}
}
