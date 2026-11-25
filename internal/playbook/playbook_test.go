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
	"sort"
	"strings"
	"testing"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
)

// sandbox is a managed folder with a playbook folder "pb" next to it.
type sandbox struct {
	t       *testing.T
	root    string
	managed string
	pb      string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	root := fsutil.Canonical(t.TempDir())
	return &sandbox{t: t, root: root, managed: filepath.Join(root, "m"), pb: filepath.Join(root, "pb")}
}

// file creates a file (and its folders) at a path relative to the root.
func (s *sandbox) file(rel string) string {
	s.t.Helper()
	path := filepath.Join(s.root, filepath.FromSlash(rel))
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
	return filepath.Dir(s.file("pb/skills/" + name + "/SKILL.md"))
}

// group builds a profile's group, with targets skills (in .agents/skills) and subagents (in .claude/agents), and the
// given playbooks.
func (s *sandbox) group(profile string, playbooks ...config.Playbook) Group {
	g := Group{Profile: profile, Entries: Entries(profile, playbooks, s.managed)}
	for _, t := range []config.Target{{Kind: "skills", Folder: ".agents/skills"},
		{Kind: "subagents", Folder: ".claude/agents"}} {
		dir, stop, _ := config.TargetDir(s.managed, t.Folder, "")
		g.Targets = append(g.Targets, Target{Kind: t.Kind, Folder: t.Folder, Dir: dir, Stop: stop})
	}
	return g
}

// pb returns a playbook registered for the sandbox's playbook folder.
func pb(name string, mappings map[string]config.Mapping) config.Playbook {
	return config.Playbook{Name: name, Folder: "../pb", Mappings: mappings}
}

// scopeOf returns the scope of every playbook of groups.
func scopeOf(groups ...Group) map[Key]bool {
	scope := map[Key]bool{}
	for _, g := range groups {
		for _, e := range g.Entries {
			scope[e.Key()] = true
		}
	}
	return scope
}

// actions describes the actions of a plan of a kind: "name,name".
func actions(plan Plan, kind ActionKind) string {
	var names []string
	for _, a := range plan.Actions {
		if a.Kind == kind {
			names = append(names, a.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (s *sandbox) link(rel string) string { return filepath.Join(s.managed, filepath.FromSlash(rel)) }

func (s *sandbox) transition(from, to []Group, scope map[Key]bool) Plan {
	s.t.Helper()
	plan, err := Transition(from, to, scope)
	if err != nil {
		s.t.Fatal(err)
	}
	return plan
}

func (s *sandbox) apply(plan Plan) {
	s.t.Helper()
	if err := Apply(plan, nil); err != nil {
		s.t.Fatal(err)
	}
}

func TestEntriesIDs(t *testing.T) {
	entries := Entries("p", []config.Playbook{{Name: "x", Folder: "a/pb"}, {Name: "x", Folder: "b/other"},
		{Name: "y", Folder: "a/pb"}}, "/m")
	if entries[0].ID != "x@pb" || entries[1].ID != "x@other" || entries[2].ID != "y" {
		t.Fatalf("ids: %s %s %s", entries[0].ID, entries[1].ID, entries[2].ID)
	}
	if entries[2].Label() != "y (profile p)" {
		t.Fatalf("label: %s", entries[2].Label())
	}
}

func TestItemsFormats(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.skill("x-b")
	s.skill("y-c")
	s.file("pb/skills/.x-hidden/SKILL.md")
	s.file("pb/skills/x-notes.txt") // a file matching the format: an artifact as well
	s.file("pb/agents/x-r.md")
	s.file("pb/agents/x-r.agent.md")
	s.file("pb/outside/x-out/SKILL.md")
	inside := filepath.Join(s.root, "pb", "outside", "x-out")
	if err := os.Symlink(inside, filepath.Join(s.pb, "skills", "x-in")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.root, "elsewhere", "x-far"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.root, "elsewhere", "x-far"),
		filepath.Join(s.pb, "skills", "x-far")); err != nil {
		t.Fatal(err)
	}
	names := func(e Entry, kind string) string {
		t.Helper()
		items, err := Items(e, kind)
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, it := range items {
			n = append(n, it.Name)
		}
		return strings.Join(n, ",")
	}
	g := s.group("p", pb("x", nil), pb("all", map[string]config.Mapping{"skills": {Folder: "skills", Format: "*"},
		"subagents": {Folder: "agents", Format: "*.agent.md"}}))
	x, all := g.Entries[0], g.Entries[1]
	// Hidden entries and links leading outside the playbook folder are never artifacts; links inside it are.
	if got := names(x, "skills"); got != "x-a,x-b,x-in,x-notes.txt" {
		t.Errorf("x skills: %s", got)
	}
	if got := names(x, "subagents"); got != "" { // the default subfolder, subagents/, does not exist
		t.Errorf("x subagents: %s", got)
	}
	if got := names(all, "skills"); got != "x-a,x-b,x-in,x-notes.txt,y-c" {
		t.Errorf("all skills: %s", got)
	}
	if got := names(all, "subagents"); got != "x-r.agent.md" {
		t.Errorf("all subagents: %s", got)
	}
	// Ownership: the link's name matches the format and it points into the playbook's folder for the kind.
	src := filepath.Join(s.pb, "skills", "x-a")
	if !x.Owns("skills", "x-a", src) || x.Owns("subagents", "x-a", src) || x.Owns("skills", "x-b", src) {
		t.Errorf("ownership")
	}
	if o := Owner(g.Entries, "skills", "y-c", filepath.Join(s.pb, "skills", "y-c")); o == nil || o.Name != "all" {
		t.Errorf("owner of y-c: %v", o)
	}
}

func TestTransitionAddUpdateRemove(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.skill("x-b")
	s.file("pb/subagents/x-r.md")
	none := s.group("p")
	with := s.group("p", pb("x", nil))
	scope := scopeOf(with)

	// Adding links everything; adding again is a no-op.
	plan := s.transition([]Group{none}, []Group{with}, scope)
	if got := actions(plan, Create); got != "x-a,x-b,x-r.md" || len(plan.Conflicts()) > 0 {
		t.Fatalf("add: %+v", plan)
	}
	s.apply(plan)
	plan = s.transition([]Group{with}, []Group{with}, scope)
	if actions(plan, Keep) != "x-a,x-b,x-r.md" || len(plan.Changes()) > 0 {
		t.Fatalf("update with no change: %+v", plan)
	}

	// Update: a new artifact, one gone and a hand-added file next to the links.
	s.skill("x-c")
	if err := os.RemoveAll(filepath.Join(s.pb, "skills", "x-b")); err != nil {
		t.Fatal(err)
	}
	s.file("m/.agents/skills/x-mine")
	plan = s.transition([]Group{with}, []Group{with}, scope)
	if actions(plan, Create) != "x-c" || actions(plan, Remove) != "x-b" {
		t.Fatalf("update: %+v", plan)
	}
	for _, a := range plan.Actions {
		if a.Kind == Remove && a.Reason != "broken link: its artifact no longer exists" {
			t.Errorf("reason: %s", a.Reason)
		}
	}
	s.apply(plan)

	// Removing the playbook removes its links only, and the folders left empty up to the managed folder.
	plan = s.transition([]Group{with}, []Group{none}, scope)
	if actions(plan, Remove) != "x-a,x-c,x-r.md" || plan.Actions[0].Reason != "playbook removed" {
		t.Fatalf("remove: %+v", plan)
	}
	s.apply(plan)
	var removed []string
	Cleanup(plan, func(dir string) { removed = append(removed, dir) })
	if strings.Join(removed, " ") != s.link(".claude/agents")+" "+s.link(".claude") {
		t.Fatalf("cleanup removed %v", removed)
	}
	if _, err := os.Stat(s.link(".agents/skills/x-mine")); err != nil {
		t.Fatal("hand-added file removed")
	}
	if _, err := os.Stat(s.managed); err != nil {
		t.Fatal("the managed folder must never be removed")
	}
}

func TestTransitionConflicts(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.skill("x-b")
	s.skill("x-c")
	s.skill("x-data-d")
	before := s.group("p")

	// Something in the way: a file, a link to something else, and a link to the same artifact made by hand.
	s.file("m/.agents/skills/x-a")
	if err := os.Symlink(filepath.Join(s.root, "elsewhere"), s.link(".agents/skills/x-b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.pb, "skills", "x-c"), s.link(".agents/skills/x-c")); err != nil {
		t.Fatal(err)
	}
	after := s.group("p", pb("x", nil))
	plan := s.transition([]Group{before}, []Group{after}, scopeOf(after))
	causes := map[string]ConflictCause{}
	for _, c := range plan.Conflicts() {
		causes[c.Name] = c.Cause
	}
	if causes["x-a"] != NotLink || causes["x-b"] != OtherLink || causes["x-c"] != Exists || len(causes) != 3 {
		t.Fatalf("conflicts: %+v", plan.Conflicts())
	}
	if err := Apply(plan, nil); err == nil {
		t.Fatal("a plan with conflicts must not be applied")
	}
	for _, name := range []string{"x-a", "x-b", "x-c"} {
		if err := os.Remove(s.link(".agents/skills/" + name)); err != nil {
			t.Fatal(err)
		}
	}

	// Two playbooks of the same profile linking the same artifact (overlapping formats on the same folder).
	both := s.group("p", pb("x", nil), pb("x-data", nil))
	plan = s.transition([]Group{before}, []Group{both}, scopeOf(both))
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Name != "x-data-d" || plan.Conflicts()[0].Cause != Duplicate {
		t.Fatalf("duplicate: %+v", plan.Conflicts())
	}

	// The same link from two profiles, from different playbook names: a duplicate as well.
	p1 := s.group("p1", pb("x", nil))
	p2 := s.group("p2", pb("y", map[string]config.Mapping{"skills": {Folder: "skills", Format: "x-a"}}))
	plan = s.transition([]Group{p1}, []Group{p1, p2}, scopeOf(p2))
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Cause != Duplicate ||
		plan.Conflicts()[0].Others[0] != "x (profile p1)" {
		t.Fatalf("duplicate across profiles: %+v", plan.Conflicts())
	}

	// The same playbook in two profiles enabled together: one conflict, not one per artifact.
	q1, q2 := s.group("q1", pb("x", nil)), s.group("q2", pb("x", nil))
	plan = s.transition(nil, []Group{q1, q2}, scopeOf(q2))
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Cause != SamePlaybook || len(plan.Changes()) > 0 {
		t.Fatalf("same playbook: %+v", plan)
	}
	// Different formats do not help: the same playbook cannot be in two enabled profiles.
	q3 := s.group("q3", pb("x", map[string]config.Mapping{"skills": {Folder: "skills", Format: "nothing-*"}}))
	plan = s.transition(nil, []Group{q1, q3}, scopeOf(q3))
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Cause != SamePlaybook {
		t.Fatalf("same playbook, other format: %+v", plan)
	}
}

func TestTransitionSwitch(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.skill("x-b")
	s.skill("y-c")
	a := s.group("a", pb("x", nil))
	b := s.group("b", pb("x", map[string]config.Mapping{"skills": {Folder: "skills", Format: "x-a"}}),
		pb("y", nil))
	s.apply(s.transition(nil, []Group{a}, scopeOf(a)))

	// Enabling b besides a: the same playbook in both.
	plan := s.transition([]Group{a}, []Group{a, b}, scopeOf(b))
	if len(plan.Conflicts()) == 0 {
		t.Fatalf("enable: want a conflict")
	}
	// Switching from a to b: x-a moves from a to b (kept), x-b goes, y-c comes.
	plan = s.transition([]Group{a}, []Group{b}, scopeOf(a, b))
	if len(plan.Conflicts()) > 0 || actions(plan, Keep) != "x-a" || actions(plan, Remove) != "x-b" ||
		actions(plan, Create) != "y-c" {
		t.Fatalf("switch: %+v", plan)
	}
	for _, act := range plan.Actions {
		if act.Kind == Remove && act.Reason != "profile disabled" {
			t.Errorf("reason: %s", act.Reason)
		}
	}
	s.apply(plan)
	// Disabling b removes everything; playbooks out of scope are not touched (z's x-b is not linked).
	other := s.group("c", pb("z", map[string]config.Mapping{"skills": {Folder: "skills", Format: "x-b"}}))
	plan = s.transition([]Group{b, other}, []Group{other}, scopeOf(b))
	if actions(plan, Remove) != "x-a,y-c" || len(plan.Changes()) != 2 || len(plan.Conflicts()) > 0 {
		t.Fatalf("disable: %+v", plan)
	}
}

func TestCleanupStops(t *testing.T) {
	s := newSandbox(t)
	appData := filepath.Join(s.root, "appdata")
	link := filepath.Join(appData, "devin", "skills", "x-a")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := s.file("m/.claude/keep.txt")
	plan := Plan{Actions: []Action{
		{Kind: Remove, Link: link, Stop: appData},
		{Kind: Remove, Link: s.link(".claude/skills/x-a"), Stop: s.managed},
	}}
	if err := os.MkdirAll(s.link(".claude/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	Cleanup(plan, nil)
	for _, gone := range []string{filepath.Join(appData, "devin"), s.link(".claude/skills")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", gone)
		}
	}
	for _, kept := range []string{appData, keep} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should have been kept", kept)
		}
	}
}

func TestRevert(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	g := s.group("p", pb("x", nil))
	plan := s.transition(nil, []Group{g}, scopeOf(g))
	s.apply(plan)
	link := s.link(".agents/skills/x-a")
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

// TestSamePlaybookReportedOnce checks that the same playbook in two profiles is one conflict, even when both
// registrations are in scope (as when status plans every playbook at once).
func TestSamePlaybookReportedOnce(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	a, b := s.group("a", pb("x", nil)), s.group("b", pb("x", nil))
	plan := s.transition(nil, []Group{a, b}, scopeOf(a, b))
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Cause != SamePlaybook || len(plan.Changes()) > 0 {
		t.Fatalf("plan: %+v", plan)
	}
}

func TestSkipFileLinks(t *testing.T) {
	s := newSandbox(t)
	s.skill("x-a")
	s.file("pb/subagents/x-r.md")
	s.file("pb/subagents/x-s.md")
	s.file("pb/subagents/x-t.md")
	before := s.group("p")
	// Something in the way of a link to a file (x-s), and of a link to a folder (x-a, left out of the rest).
	s.file("m/.claude/agents/x-s.md")
	after := s.group("p", pb("x", nil))
	plan := s.transition([]Group{before}, []Group{after}, scopeOf(after))
	plan.SkipFileLinks()
	if got := actions(plan, Skip); got != "x-r.md,x-s.md,x-t.md" {
		t.Fatalf("skipped: %s", got)
	}
	if got := actions(plan, Create); got != "x-a" {
		t.Fatalf("created: %s", got)
	}
	if len(plan.Conflicts()) > 0 {
		t.Fatalf("conflicts: %+v", plan.Conflicts())
	}
	if err := Apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(s.link(".claude/agents/x-r.md")); !os.IsNotExist(err) {
		t.Fatalf("a skipped link was created")
	}

	// A conflict between playbooks over a link to a file stays: it is in the configuration.
	both := s.group("p", pb("x", nil), pb("y", map[string]config.Mapping{"subagents": {Folder: "subagents",
		Format: "x-t.md"}}))
	plan = s.transition([]Group{after}, []Group{both}, scopeOf(both))
	plan.SkipFileLinks()
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Cause != Duplicate || plan.Conflicts()[0].Name != "x-t.md" {
		t.Fatalf("duplicate: %+v", plan.Conflicts())
	}

	// Something in the way of a link to a folder is still a conflict.
	s.file("m/.agents/skills/x-b")
	s.skill("x-b")
	plan = s.transition([]Group{after}, []Group{after}, scopeOf(after))
	plan.SkipFileLinks()
	if len(plan.Conflicts()) != 1 || plan.Conflicts()[0].Name != "x-b" || plan.Conflicts()[0].Cause != NotLink {
		t.Fatalf("folder in the way: %+v", plan.Conflicts())
	}
}
