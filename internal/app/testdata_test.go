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

	installed := func() []string {
		t.Helper()
		items, err := os.ReadDir(e.agentsSkills())
		if err != nil {
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
		if got := installed(); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("installed skills = %v, want %v", got, want)
		}
	}

	e.mustRun(ExitOK, "init", "--target", "agents,claude")

	// Standard layout: only aiplam-lettuce-* folders with a SKILL.md are linked.
	e.mustRun(ExitOK, "add", "aiplam-lettuce", lettuce)
	expect("aiplam-lettuce-data-clean", "aiplam-lettuce-data-report", "aiplam-lettuce-deploy", "aiplam-lettuce-review")
	e.assertLink(filepath.Join(e.home, ".claude", "skills", "aiplam-lettuce-review"),
		filepath.Join(lettuce, "skills", "aiplam-lettuce-review"))

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

	// A second set: it collides with the first one (both provide aiplam-tomato), so it can replace it but not join it.
	all := installed()
	e.mustRun(ExitOK, "set", "add", "other")
	e.mustRun(ExitOK, "add", "aiplam-tomato", tomato, "-sp", ".", "-s", "other")
	e.mustRun(ExitFailure, "set", "enable", "other")
	e.mustRun(ExitOK, "set", "switch", "other")
	expect("aiplam-tomato-onboard", "aiplam-tomato-style")
	e.mustRun(ExitOK, "set", "switch", "default")
	expect(all...)

	// A third set that does not collide can be enabled besides the first one.
	e.mustRun(ExitOK, "set", "add", "extra")
	e.mustRun(ExitOK, "remove", "aiplam-blueberry")
	e.mustRun(ExitOK, "add", "aiplam-blueberry", blueberry, "-s", "extra")
	e.mustRun(ExitOK, "set", "enable", "extra")
	expect(all...)
	e.mustRun(ExitOK, "target", "agents")
	e.assertMissing(filepath.Join(e.home, ".claude", "skills", "aiplam-lettuce-review"))
	e.mustRun(ExitOK, "check")

	e.mustRun(ExitOK, "remove", "aiplam-lettuce@aiplam-lettuce-fork")
	e.mustRun(ExitOK, "remove", "aiplam-lettuce")
	e.mustRun(ExitOK, "remove", "aiplam-blueberry")
	e.mustRun(ExitOK, "set", "disable", "extra")
	_, errOut = e.mustRun(ExitFailure, "remove", tomato) // two playbooks are registered there
	if !strings.Contains(errOut, "aiplam-tomato, aiplam-garlic") {
		t.Fatalf("unexpected message: %s", errOut)
	}
	e.mustRun(ExitOK, "remove", "aiplam-tomato")
	e.mustRun(ExitOK, "remove", "aiplam-garlic", tomato)
	expect()
}
