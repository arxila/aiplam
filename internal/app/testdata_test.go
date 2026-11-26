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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestTestdataPlaybooks runs the scenarios described in testdata/README.md against copies of testdata/playbooks.
func TestTestdataPlaybooks(t *testing.T) {
	e := newEnv(t)
	pbs := filepath.Join(e.home, "playbooks")
	if err := os.CopyFS(pbs, os.DirFS(filepath.Join("..", "..", "testdata", "playbooks"))); err != nil {
		t.Fatal(err)
	}
	lettuce := filepath.Join(pbs, "aiplam-lettuce-playbook")
	fork := filepath.Join(pbs, "aiplam-lettuce-fork")
	tomato := filepath.Join(pbs, "aiplam-tomato-playbook")
	blueberry := filepath.Join(pbs, "aiplam-blueberry-playbook")
	e.app.Cwd = pbs

	linked := func(dir string) []string {
		t.Helper()
		items, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		var names []string
		for _, item := range items {
			names = append(names, item.Name())
		}
		sort.Strings(names)
		return names
	}
	expect := func(dir string, want ...string) {
		t.Helper()
		sort.Strings(want)
		if got := linked(dir); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("%s holds %v, want %v", dir, got, want)
		}
	}
	skills := e.homeSkills()

	e.mustRun(ExitOK, "init", "--user", "--agent", "standard")

	// Default layout: the aiplam-lettuce-* entries of skills/, whatever they are (aiplam-lettuce-notes has no
	// SKILL.md, but it matches the format).
	e.mustRun(ExitOK, "add", "aiplam-lettuce", "aiplam-lettuce-playbook") // relative to the current folder
	lettuceSkills := []string{"aiplam-lettuce-data-clean", "aiplam-lettuce-data-report", "aiplam-lettuce-deploy",
		"aiplam-lettuce-notes", "aiplam-lettuce-review"}
	expect(skills, lettuceSkills...)

	// A playbook whose format overlaps another's in the same folder is refused: every link belongs to one playbook.
	e.mustFail(ExitFailure, []string{"would also be linked by aiplam-lettuce"}, "add", "aiplam-lettuce-data",
		"aiplam-lettuce-playbook")

	// The same name from a fork, with its skills in ai/skills: ids get the folder's last segment.
	out, _ := e.mustRun(ExitOK, "add", "aiplam-lettuce", fork, "-f", "skills:ai/skills")
	if !contains(out, "aiplam-lettuce of profile 'default' is now identified as "+
		"aiplam-lettuce@aiplam-lettuce-playbook") {
		t.Fatalf("unexpected output: %s", out)
	}
	expect(skills, append(lettuceSkills, "aiplam-lettuce-data-export", "aiplam-lettuce-extra")...)

	// A playbook providing a skill that another one already provides is not added.
	e.mustFail(ExitFailure, []string{"aiplam-lettuce-review"}, "add", "aiplam-lettuce", blueberry)
	e.mustRun(ExitOK, "add", "aiplam-blueberry", blueberry)

	// Skills at the root of the repository, with two names (formats) on the same folder; docs/ does not match.
	e.mustRun(ExitOK, "add", "aiplam-tomato", tomato, "-f", "skills:.")
	e.mustRun(ExitOK, "add", "aiplam-garlic", tomato, "-f", "skills:.")
	all := append(lettuceSkills, "aiplam-lettuce-data-export", "aiplam-lettuce-extra", "aiplam-blueberry-search",
		"aiplam-tomato-onboard", "aiplam-tomato-style", "aiplam-garlic-audit")
	expect(skills, all...)
	e.mustRun(ExitOK, "status")

	// A second profile registering the same playbook: it cannot be enabled besides the first one, but it can
	// replace it.
	e.mustRun(ExitOK, "profile", "add", "other", "--agent", "standard")
	e.mustRun(ExitOK, "add", "aiplam-tomato", tomato, "-f", "skills:.", "-p", "other")
	e.mustFail(ExitFailure, []string{"the same playbook cannot be in two enabled profiles"}, "profile", "enable",
		"other")
	e.mustRun(ExitOK, "profile", "switch", "other")
	expect(skills, "aiplam-tomato-onboard", "aiplam-tomato-style")
	e.mustRun(ExitOK, "profile", "switch", "default")
	expect(skills, all...)

	// A third profile that does not collide can be enabled besides the first one.
	e.mustRun(ExitOK, "profile", "add", "extra", "--agent", "standard")
	e.mustRun(ExitOK, "remove", "aiplam-blueberry")
	e.mustRun(ExitOK, "add", "aiplam-blueberry", blueberry, "-p", "extra")
	e.mustRun(ExitOK, "profile", "enable", "extra")
	expect(skills, all...)
	e.mustRun(ExitOK, "status")

	// Another working scope, for Claude Code, with the lettuce playbook under another name (the same playbook could
	// not be enabled in two profiles), its formats given by hand, and its subagents in agents/.
	claude := filepath.Join(e.home, ".claude")
	e.mustRun(ExitOK, "profile", "add", "greens", "--agent", "claude")
	e.mustRun(ExitOK, "add", "greens", lettuce, "-p", "greens", "-f", "skills:skills:aiplam-lettuce-*",
		"-f", "subagents:agents:aiplam-lettuce-*.md", "-f", "commands:commands:aiplam-lettuce-*",
		"-f", "rules:rules:aiplam-lettuce-*")
	e.mustRun(ExitOK, "profile", "enable", "greens")
	e.assertLink(filepath.Join(claude, "skills", "aiplam-lettuce-review"),
		filepath.Join(lettuce, "skills", "aiplam-lettuce-review"))
	expect(filepath.Join(claude, "agents"), "aiplam-lettuce-reviewer.agent.md", "aiplam-lettuce-reviewer.md")
	e.assertLink(filepath.Join(claude, "commands", "aiplam-lettuce-ship.md"),
		filepath.Join(lettuce, "commands", "aiplam-lettuce-ship.md"))
	e.assertLink(filepath.Join(claude, "rules", "aiplam-lettuce-style.md"),
		filepath.Join(lettuce, "rules", "aiplam-lettuce-style.md"))
	expect(skills, all...)
	out, _ = e.mustRun(ExitOK, "list")
	if !contains(out, "Profile greens (agent claude)") || !contains(out, "subagents (~/.claude/agents):") {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitOK, "profile", "disable", "greens")
	e.assertMissing(claude)
	e.mustFail(ExitFailure, []string{"several playbooks of profile 'default' are registered for playbook folder"},
		"remove", tomato, "-p", "default")

	// A project's managed folder, used by the commands run anywhere inside it: it is independent of the home folder's.
	project := filepath.Join(e.home, "projects", "demo")
	e.mkdir(filepath.Join(project, "module"))
	e.app.Cwd = project
	e.mustRun(ExitOK, "init", ".", "--agent", "standard")
	e.mustRun(ExitOK, "add", "aiplam-garlic", tomato, "-f", "skills:.")
	e.assertLink(filepath.Join(project, ".agents", "skills", "aiplam-garlic-audit"),
		filepath.Join(tomato, "aiplam-garlic-audit"))
	if folder := e.profile(project, "default").Playbooks[0].Folder; folder != "../../playbooks/aiplam-tomato-playbook" {
		t.Fatalf("stored folder: %s", folder)
	}
	e.app.Cwd = filepath.Join(project, "module")
	out, _ = e.mustRun(ExitOK, "list")
	if !contains(out, "Managed folder: ~/projects/demo\n") || contains(out, "aiplam-tomato-style") {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitOK, "status")
	e.mustRun(ExitOK, "status", "--user")

	e.app.Cwd = pbs
	e.mustRun(ExitOK, "profile", "disable", "extra") // a single enabled profile: no need for --profile
	e.mustRun(ExitOK, "remove", "aiplam-lettuce@aiplam-lettuce-fork")
	e.mustRun(ExitOK, "remove", "aiplam-lettuce")
	e.mustRun(ExitOK, "remove", "aiplam-garlic")
	e.mustRun(ExitOK, "remove", "aiplam-tomato")
	expect(skills)
	e.assertMissing(filepath.Join(e.home, ".agents")) // removed once empty
}
