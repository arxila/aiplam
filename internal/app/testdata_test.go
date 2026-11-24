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
	pbs := filepath.Join(e.root, "playbooks")
	if err := os.CopyFS(pbs, os.DirFS(filepath.Join("..", "..", "testdata", "playbooks"))); err != nil {
		t.Fatal(err)
	}
	lettuce := filepath.Join(pbs, "aiplam-lettuce-playbook")
	fork := filepath.Join(pbs, "aiplam-lettuce-fork")
	tomato := filepath.Join(pbs, "aiplam-tomato-playbook")
	blueberry := filepath.Join(pbs, "aiplam-blueberry-playbook")

	linked := func() []string {
		t.Helper()
		items, err := os.ReadDir(e.agentsSkills())
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
	expect := func(want ...string) {
		t.Helper()
		sort.Strings(want)
		if got := linked(); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("linked skills = %v, want %v", got, want)
		}
	}

	e.mustRun(ExitOK, "init", "-g", "standard")

	// Standard layout: only aiplam-lettuce-* folders with a SKILL.md are linked; the standard agent reads only skills.
	e.mustRun(ExitOK, "add", "aiplam-lettuce", lettuce)
	expect("aiplam-lettuce-data-clean", "aiplam-lettuce-data-report", "aiplam-lettuce-deploy", "aiplam-lettuce-review")
	e.assertMissing(filepath.Join(e.home, ".agents", "agents"))

	// A longer prefix in the same repository takes over aiplam-lettuce-data-*.
	e.mustRun(ExitOK, "add", "aiplam-lettuce-data", lettuce)
	out, _ := e.mustRun(ExitOK, "list")
	if !strings.Contains(out, "aiplam-lettuce-data\n") || strings.Count(out, "aiplam-lettuce-data-clean") != 1 {
		t.Fatalf("unexpected list: %s", out)
	}

	// Same name from a fork with a custom skills path: ids get the folder's last segment.
	out, _ = e.mustRun(ExitOK, "add", "aiplam-lettuce", fork, "--skills-path", "ai/skills")
	if !strings.Contains(out, "aiplam-lettuce is now identified as aiplam-lettuce@aiplam-lettuce-playbook") {
		t.Fatalf("unexpected output: %s", out)
	}
	expect("aiplam-lettuce-data-clean", "aiplam-lettuce-data-export", "aiplam-lettuce-data-report",
		"aiplam-lettuce-deploy", "aiplam-lettuce-extra", "aiplam-lettuce-review")

	// A playbook providing a skill that another one already provides is not added.
	_, errOut := e.mustRun(ExitFailure, "add", "aiplam-lettuce", blueberry)
	if !strings.Contains(errOut, "aiplam-lettuce-review") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "add", "aiplam-blueberry", blueberry)

	// Skills at the root of the repository, with two prefixes.
	e.mustRun(ExitOK, "add", "aiplam-tomato", tomato, "--skills-path", ".")
	e.mustRun(ExitOK, "add", "aiplam-garlic", tomato, "--skills-path", ".")
	expect("aiplam-lettuce-data-clean", "aiplam-lettuce-data-export", "aiplam-lettuce-data-report",
		"aiplam-lettuce-deploy", "aiplam-lettuce-extra", "aiplam-lettuce-review", "aiplam-tomato-onboard", "aiplam-tomato-style", "aiplam-blueberry-search", "aiplam-garlic-audit")
	e.mustRun(ExitOK, "check")

	// Removing the longer prefix hands its skills back to aiplam-lettuce: the links stay.
	e.mustRun(ExitOK, "remove", "aiplam-lettuce-data")
	e.assertLink(filepath.Join(e.agentsSkills(), "aiplam-lettuce-data-clean"),
		filepath.Join(lettuce, "skills", "aiplam-lettuce-data-clean"))
	e.mustRun(ExitOK, "check")

	// A second profile: it collides with the first one (both provide aiplam-tomato), so it can replace it but not join it.
	all := linked()
	e.mustRun(ExitOK, "profile", "add", "other", "standard")
	e.mustRun(ExitOK, "add", "aiplam-tomato", tomato, "-sp", ".", "-p", "other")
	e.mustRun(ExitFailure, "profile", "enable", "other")
	e.mustRun(ExitOK, "profile", "switch", "other")
	expect("aiplam-tomato-onboard", "aiplam-tomato-style")
	e.mustRun(ExitOK, "profile", "switch", "default")
	expect(all...)

	// A third profile that does not collide can be enabled besides the first one.
	e.mustRun(ExitOK, "profile", "add", "extra", "standard")
	e.mustRun(ExitOK, "remove", "aiplam-blueberry")
	e.mustRun(ExitOK, "add", "aiplam-blueberry", blueberry, "-p", "extra")
	e.mustRun(ExitOK, "profile", "enable", "extra")
	expect(all...)
	e.mustRun(ExitOK, "check")

	// Another working scope, assigned Claude Code, links every kind of artifact Claude Code reads, from ~/.claude: it
	// does not collide with the profiles for the standard agent.
	claude := filepath.Join(e.home, ".claude")
	e.mustRun(ExitOK, "profile", "add", "greens", "claude")
	e.mustRun(ExitOK, "add", "aiplam-lettuce", lettuce, "-p", "greens")
	e.mustRun(ExitOK, "profile", "enable", "greens")
	e.assertLink(filepath.Join(claude, "skills", "aiplam-lettuce-review"),
		filepath.Join(lettuce, "skills", "aiplam-lettuce-review"))
	e.assertLink(filepath.Join(claude, "agents", "aiplam-lettuce-reviewer.md"),
		filepath.Join(lettuce, "agents", "aiplam-lettuce-reviewer.md"))
	e.assertLink(filepath.Join(claude, "commands", "aiplam-lettuce-ship.md"),
		filepath.Join(lettuce, "commands", "aiplam-lettuce-ship.md"))
	e.assertLink(filepath.Join(claude, "rules", "aiplam-lettuce-style.md"),
		filepath.Join(lettuce, "rules", "aiplam-lettuce-style.md"))
	expect(all...)

	// Assigned Copilot instead: its own format, from ~/.copilot; Copilot also reads ~/.agents, hence the warning.
	_, errOut = e.mustRun(ExitOK, "profile", "agent", "greens", "copilot")
	if !strings.Contains(errOut, "may be loaded twice") {
		t.Fatalf("want a warning: %s", errOut)
	}
	e.assertMissing(claude)
	e.assertLink(filepath.Join(e.home, ".copilot", "agents", "aiplam-lettuce-reviewer.agent.md"),
		filepath.Join(lettuce, "agents", "aiplam-lettuce-reviewer.agent.md"))
	e.mustRun(ExitOK, "profile", "disable", "greens")
	e.assertMissing(filepath.Join(e.home, ".copilot"))
	e.mustRun(ExitOK, "check")
	_, errOut = e.mustRun(ExitFailure, "remove", tomato) // two playbooks are registered there
	if !strings.Contains(errOut, "aiplam-tomato of profile default, aiplam-garlic") {
		t.Fatalf("unexpected message: %s", errOut)
	}

	// A project base folder: a skill linked from the home folder's agents configuration folder cannot be linked
	// from the project's.
	project := filepath.Join(e.home, "projects", "demo")
	if err := os.MkdirAll(filepath.Join(project, "module"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Cwd = project
	e.mustRun(ExitOK, "init", ".", "standard")
	e.mustRun(ExitFailure, "add", "aiplam-garlic", tomato, "-sp", ".")
	e.mustRun(ExitOK, "remove", "aiplam-garlic", "--global")
	e.mustRun(ExitOK, "add", "aiplam-garlic", tomato, "-sp", ".")
	e.assertLink(filepath.Join(project, ".agents", "skills", "aiplam-garlic-audit"),
		filepath.Join(tomato, "aiplam-garlic-audit"))
	e.app.Cwd = filepath.Join(project, "module")
	out, _ = e.mustRun(ExitOK, "list", "--all")
	if strings.Count(out, "Base folder: ") != 2 {
		t.Fatalf("unexpected list: %s", out)
	}
	e.mustRun(ExitOK, "check")
	e.app.Cwd = e.root

	e.mustRun(ExitOK, "remove", "aiplam-lettuce@aiplam-lettuce-fork")
	e.mustRun(ExitOK, "remove", "aiplam-lettuce")
	e.mustRun(ExitOK, "remove", "aiplam-blueberry")
	e.mustRun(ExitOK, "profile", "disable", "extra")
	e.mustRun(ExitOK, "remove", "aiplam-tomato")
	expect()
	e.assertMissing(filepath.Join(e.home, ".agents")) // removed once empty
}
