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

package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
)

// sandbox is a base folder with a playbook folder "pb" in it.
type sandbox struct {
	t    *testing.T
	base string
	pb   string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	base := fsutil.Canonical(t.TempDir())
	return &sandbox{t: t, base: base, pb: filepath.Join(base, "pb")}
}

// file creates a file (and its folders) at a path relative to the playbook folder.
func (s *sandbox) file(rel string) string {
	s.t.Helper()
	path := filepath.Join(s.pb, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		s.t.Fatal(err)
	}
	return path
}

// skill creates a skill folder in the playbook's skills/ folder.
func (s *sandbox) skill(name string) string {
	s.t.Helper()
	return filepath.Dir(s.file("skills/" + name + "/SKILL.md"))
}

// group returns the group of a profile for agent with the given playbooks, all in "pb".
func (s *sandbox) group(profile, agent string, names ...string) Group {
	s.t.Helper()
	ag, ok := config.LookupAgent(agent)
	if !ok {
		s.t.Fatalf("unknown agent %s", agent)
	}
	var playbooks []config.Playbook
	for _, n := range names {
		playbooks = append(playbooks, config.Playbook{Name: n, Folder: "pb"})
	}
	return Group{Profile: profile, Entries: Entries(profile, playbooks, s.base), Folders: ag.KindFolders(s.base, false)}
}

func folder(g Group, kind config.Kind) config.KindFolder {
	for _, f := range g.Folders {
		if f.Kind == kind {
			return f
		}
	}
	panic("no folder for " + string(kind))
}

func names(items []Item) string {
	var n []string
	for _, it := range items {
		n = append(n, it.Name)
	}
	return strings.Join(n, ",")
}

func kinds(plan Plan, kind ActionKind) string {
	var n []string
	for _, a := range plan.filter(kind) {
		n = append(n, a.Name)
	}
	return strings.Join(n, ",")
}

func TestEntriesIDs(t *testing.T) {
	s := newSandbox(t)
	entries := Entries("p", []config.Playbook{{Name: "x", Folder: "a/pb"}, {Name: "x", Folder: "b/other"},
		{Name: "y", Folder: "a/pb"}}, s.base)
	if entries[0].ID != "x@pb" || entries[1].ID != "x@other" || entries[2].ID != "y" || entries[2].Profile != "p" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestItemsFormatsAndOwnership(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.skill("x-y-b")
	s.file("skills/x-notes/README.md") // no SKILL.md
	s.file("skills/x-file")            // not a folder
	s.skill("other")
	s.file("agents/x-r.md")
	s.file("agents/x-r.agent.md")
	s.file("agents/x-r.toml")
	s.file("agents/y-r.md")
	s.file("agents/x-.md") // nothing after the prefix: still named "x-..."

	claude := s.group("p", "claude", "x", "x-y")
	x, xy := claude.Entries[0], claude.Entries[1]
	for _, c := range []struct {
		g    Group
		e    Entry
		kind config.Kind
		want string
	}{
		{claude, x, config.Skills, "x-a"},
		{claude, xy, config.Skills, "x-y-b"},
		{claude, x, config.Subagents, "x-.md,x-r.md"},
		{s.group("p", "copilot", "x"), x, config.Subagents, "x-r.agent.md"},
		{s.group("p", "codex", "x"), x, config.Subagents, "x-r.toml"},
		{claude, x, config.Commands, ""}, // no commands/ folder
	} {
		items, err := Items(c.g.Entries, c.e, folder(c.g, c.kind))
		if err != nil {
			t.Fatal(err)
		}
		if got := names(items); got != c.want {
			t.Errorf("%s of %s for %s = %q, want %q", c.kind, c.e.ID, folder(c.g, c.kind).Agent, got, c.want)
		}
	}
	if o := Owner(claude.Entries, folder(claude, config.Skills), filepath.Join(s.pb, "skills"), "x-y-b"); o == nil ||
		o.ID != "x-y" {
		t.Fatalf("owner = %+v", o)
	}
	if o := Owner(claude.Entries, folder(claude, config.Skills), filepath.Join(s.pb, "skills"), "z-a"); o != nil {
		t.Fatalf("owner = %+v", o)
	}
}

func TestItemsFollowExplicitPaths(t *testing.T) {
	s := newSandbox(t)
	s.file("subagents/x-r.md")
	s.file("agents/x-old.md")
	g := s.group("p", "claude", "x")
	f := folder(g, config.Subagents)
	if items, _ := Items(g.Entries, g.Entries[0], f); names(items) != "x-old.md" {
		t.Fatalf("default folder: %s", names(items))
	}
	g.Entries[0].Artifacts.Subagents.Path = "subagents"
	if items, _ := Items(g.Entries, g.Entries[0], f); names(items) != "x-r.md" {
		t.Fatalf("explicit folder: %s", names(items))
	}
	// An item that is a link leading outside the playbook folder is not an artifact.
	outside := filepath.Join(s.base, "outside.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.pb, "subagents", "x-escape.md")); err != nil {
		t.Skip("symbolic links are not available:", err)
	}
	if items, _ := Items(g.Entries, g.Entries[0], f); names(items) != "x-r.md" {
		t.Fatalf("with an escaping link: %s", names(items))
	}
}

func TestBuildApplyAndUpdate(t *testing.T) {
	s := newSandbox(t)
	skill := s.skill("x-a")
	s.skill("x-b")
	agent := s.file("agents/x-r.md")
	g := s.group("p", "claude", "x")
	plan, err := Build(g, g.Entries[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(plan, Create); got != "x-a,x-b,x-r.md" {
		t.Fatalf("creations = %s", got)
	}
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.base, ".claude", "agents", "x-r.md")
	if state, current, _ := fsutil.Inspect(link); state != fsutil.Link || !fsutil.SamePath(current, agent) {
		t.Fatalf("link %s -> %s", link, current)
	}

	// Nothing to do the second time.
	plan, _ = Build(g, g.Entries[0], nil)
	if len(plan.Changes()) != 0 || kinds(plan, Keep) != "x-a,x-b,x-r.md" {
		t.Fatalf("changes = %+v", plan.Changes())
	}

	// A removed artifact is unlinked (its link is broken), and the folder it leaves empty is removed.
	if err := os.Remove(agent); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(skill); err != nil {
		t.Fatal(err)
	}
	plan, _ = Build(g, g.Entries[0], nil)
	if got := kinds(plan, Remove); got != "x-a,x-r.md" || !strings.Contains(plan.filter(Remove)[0].Reason, "broken") {
		t.Fatalf("removals = %+v", plan.filter(Remove))
	}
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	var removed []string
	Cleanup(plan, func(dir string) { removed = append(removed, dir) })
	if len(removed) != 1 || removed[0] != filepath.Join(s.base, ".claude", "agents") {
		t.Fatalf("removed folders = %v", removed)
	}
	if !fsutil.IsDir(filepath.Join(s.base, ".claude", "skills")) {
		t.Fatal("a folder still holding links was removed")
	}

	// Removing the playbook removes every link, then the folders and the agents configuration folder.
	plan, _ = Build(Group{Profile: "p", Folders: g.Folders}, g.Entries[0], nil)
	if got := kinds(plan, Remove); got != "x-b" || plan.filter(Remove)[0].Reason != "playbook removed" {
		t.Fatalf("removals = %+v", plan.filter(Remove))
	}
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	Cleanup(plan, nil)
	if _, err := os.Lstat(filepath.Join(s.base, ".claude")); !os.IsNotExist(err) {
		t.Fatal(".claude should have been removed")
	}
}

func TestBuildConflicts(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.skill("x-b")
	s.skill("x-c")
	g := s.group("p", "standard", "x")
	dir := filepath.Join(s.base, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(dir, "x-a"), 0o755); err != nil { // a real folder
		t.Fatal(err)
	}
	if err := os.Symlink(s.base, filepath.Join(dir, "x-b")); err != nil { // a link elsewhere
		t.Skip("symbolic links are not available:", err)
	}
	plan, err := Build(g, g.Entries[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	causes := map[string]ConflictCause{}
	for _, c := range plan.Conflicts() {
		causes[c.Name] = c.Cause
	}
	if causes["x-a"] != NotLink || causes["x-b"] != OtherLink || len(causes) != 2 {
		t.Fatalf("conflicts = %+v", plan.Conflicts())
	}
	if err := Apply(plan, nil); err == nil {
		t.Fatal("a plan with conflicts was applied")
	}

	// Codex links skills from the same folder as the standard agent: two profiles collide there, not elsewhere.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	codex := s.group("q", "codex", "x")
	plan, _ = Build(g, g.Entries[0], []Group{codex})
	if len(plan.Conflicts()) != 3 || plan.Conflicts()[0].Cause != OtherProvider ||
		plan.Conflicts()[0].Others[0] != "x of profile q" {
		t.Fatalf("conflicts = %+v", plan.Conflicts())
	}
	claude := s.group("q", "claude", "x")
	if plan, _ = Build(g, g.Entries[0], []Group{claude}); len(plan.Conflicts()) != 0 {
		t.Fatalf("conflicts = %+v", plan.Conflicts())
	}
}

func TestOverlaps(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	other := filepath.Join(s.base, "other")
	if err := os.MkdirAll(filepath.Join(other, "skills", "x-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "skills", "x-a", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ag, _ := config.LookupAgent("claude")
	g := Group{Profile: "p", Folders: ag.KindFolders(s.base, false), Entries: Entries("p", []config.Playbook{
		{Name: "x", Folder: "pb"}, {Name: "x", Folder: "other"}}, s.base)}
	conflicts, err := Overlaps(g, g.Entries[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Name != "x-a" || conflicts[0].Others[0] != "x@pb" {
		t.Fatalf("conflicts = %+v", conflicts)
	}
}

func TestSwitchBetweenAgents(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.file("agents/x-r.toml")
	s.file("agents/x-r.md")
	standard, codex, claude := s.group("p", "standard", "x"), s.group("p", "codex", "x"), s.group("p", "claude", "x")
	plan, _ := Build(standard, standard.Entries[0], nil)
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}

	// standard -> codex: the skill's link is the same, so it is left alone; codex's subagent is linked.
	plan, err := Switch([]Group{standard}, []Group{codex}, nil, "agent changed", "agent changed", false)
	if err != nil {
		t.Fatal(err)
	}
	if kinds(plan, Remove) != "" || kinds(plan, Create) != "x-r.toml" {
		t.Fatalf("plan = %+v", plan.Actions)
	}
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}

	// codex -> claude: everything moves, and the old folders go once empty.
	plan, _ = Switch([]Group{codex}, []Group{claude}, nil, "agent changed", "agent changed", false)
	if kinds(plan, Remove) != "x-a,x-r.toml" || kinds(plan, Create) != "x-a,x-r.md" {
		t.Fatalf("plan = %+v", plan.Actions)
	}
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	Cleanup(plan, nil)
	for _, gone := range []string{".agents", ".codex"} {
		if _, err := os.Lstat(filepath.Join(s.base, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s should have been removed", gone)
		}
	}

	// Enabling (strict) fails when a link already exists, even a right one.
	plan, _ = Switch(nil, []Group{claude}, nil, "", "", true)
	if len(plan.Conflicts()) != 2 || plan.Conflicts()[0].Cause != Exists {
		t.Fatalf("plan = %+v", plan.Actions)
	}
}

func TestMarkAncestors(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	home := filepath.Join(s.base, "home")
	// Copilot reads ~/.copilot/skills in the home folder, but .github/skills in a project.
	if err := os.MkdirAll(filepath.Join(home, ".copilot", "skills", "x-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".github", "skills", "x-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	copilot := s.group("p", "copilot", "x")
	plan, _ := Build(copilot, copilot.Entries[0], nil)
	MarkAncestors(&plan, []string{home}, home)
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Cause != Ancestor ||
		plan.Conflicts()[0].Current != filepath.Join(home, ".copilot", "skills", "x-a") {
		t.Fatalf("plan = %+v", plan.Actions)
	}
	// As a project base folder (not the home folder), it is .github that counts.
	plan, _ = Build(copilot, copilot.Entries[0], nil)
	MarkAncestors(&plan, []string{home}, filepath.Join(s.base, "elsewhere"))
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Current != filepath.Join(home, ".github", "skills", "x-a") {
		t.Fatalf("plan = %+v", plan.Actions)
	}
	// Another agent is not concerned.
	claude := s.group("p", "claude", "x")
	plan, _ = Build(claude, claude.Entries[0], nil)
	MarkAncestors(&plan, []string{home}, home)
	if len(plan.Conflicts()) != 0 {
		t.Fatalf("plan = %+v", plan.Actions)
	}
}

func TestCleanupKeepsWhatIsNotEmpty(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.file("agents/x-r.md")
	g := s.group("p", "claude", "x")
	plan, _ := Build(g, g.Entries[0], nil)
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(s.base, ".claude", "agents", "mine.md")
	if err := os.WriteFile(mine, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ = Build(Group{Profile: "p", Folders: g.Folders}, g.Entries[0], nil)
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	Cleanup(plan, nil)
	if !fsutil.IsFile(mine) {
		t.Fatal("a file added by hand was removed")
	}
	if _, err := os.Lstat(filepath.Join(s.base, ".claude", "skills")); !os.IsNotExist(err) {
		t.Fatal("the empty skills folder should have been removed")
	}

	// A folder that is a link (for example, a .claude kept elsewhere) is never removed.
	real := filepath.Join(s.base, "dotfiles-claude")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(s.base, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(s.base, ".claude")); err != nil {
		t.Skip("symbolic links are not available:", err)
	}
	plan, _ = Build(g, g.Entries[0], nil)
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	plan, _ = Build(Group{Profile: "p", Folders: g.Folders}, g.Entries[0], nil)
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	Cleanup(plan, nil)
	if state, _, _ := fsutil.Inspect(filepath.Join(s.base, ".claude")); state != fsutil.Link {
		t.Fatal("a linked agents configuration folder was removed")
	}
}

func TestRevert(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	g := s.group("p", "standard", "x")
	plan, _ := Build(g, g.Entries[0], nil)
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.base, ".agents", "skills", "x-a")
	if err := Revert(plan.Changes()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("the creation was not reverted")
	}
	removal := []Action{{Kind: Remove, Link: link, Source: filepath.Join(s.pb, "skills", "x-a")}}
	if err := Revert(removal); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := fsutil.Inspect(link); state != fsutil.Link {
		t.Fatal("the removal was not reverted")
	}
}

func TestMetadataVersion(t *testing.T) {
	dir := t.TempDir()
	for i, c := range []struct{ content, want string }{
		{"---\nname: x\nmetadata:\n  version: \"1.0\"\n---\nBody\n", "1.0"},
		{"---\nname: x\nmetadata:\n  version: 1.0\n  author: me\n---\n", "1.0"}, // as written, not as a number
		{"\xef\xbb\xbf---\r\nmetadata:\r\n  version: 2.1.3\r\n---\r\n", "2.1.3"},
		{"---\nmetadata:\n  version: v3\n---", "v3"},
		{"---\nname: x\nversion: 1.0\n---\n", ""}, // not the Agent Skills field
		{"---\nmetadata:\n  version:\n---\n", ""},
		{"---\nmetadata:\n  version: [1, 2]\n---\n", ""},
		{"---\nmetadata: [\n---\n", ""}, // invalid YAML
		{"---\n---\n", ""},
		{"---\nmetadata:\n  version: 1.0\n", ""}, // unterminated
		{"metadata:\n  version: 1.0\n", ""},      // no frontmatter
		{"# Title\n\n---\nmetadata:\n  version: 1.0\n---\n", ""},
	} {
		file := filepath.Join(dir, fmt.Sprintf("SKILL%d.md", i))
		if err := os.WriteFile(file, []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := MetadataVersion(file); got != c.want {
			t.Errorf("%q: %q, want %q", c.content, got, c.want)
		}
	}
	if got := MetadataVersion(filepath.Join(dir, "missing.md")); got != "" {
		t.Errorf("missing file: %q", got)
	}
}
