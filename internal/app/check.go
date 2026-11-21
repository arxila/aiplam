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
	"errors"
	"os"
	"path/filepath"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

const checkUsage = `Usage: aiplam check [-s|--set SET]

Changes nothing. Checks the format and content of the set's configuration file (~/.aiplam/<set>.yaml), that no other
set lists its targets, the target and playbook folders, and, for every registered playbook, whether 'aiplam update'
would change anything (links to create or remove) or would be cancelled by a conflict. Also reports broken links in
the targets' skills folders. Exits with 1 when anything needs attention.

  -s, --set SET   the configuration set (default: 'default')
`

func (a *App) cmdCheck(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	file := a.configFile()
	cfg, err := config.Load(a.Home, a.set())
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			return a.notInitialized()
		}
		a.errorf("%s: %s", file, err)
		return errSilent
	}

	attention := 0
	problem := func(format string, args ...any) {
		attention++
		a.warnf(format, args...)
	}

	a.printf("Set %s: %s\n", a.set(), file)
	if errs := cfg.Validate(a.Home); len(errs) > 0 {
		for _, e := range errs {
			problem("%s: %s", file, e)
		}
		a.errorf("%d problem(s) in %s: fix them by hand; playbooks were not checked.", len(errs), file)
		return errSilent
	}
	a.printf("  format and content are valid\n")

	shared, err := config.SharedTargets(a.Home, a.set(), cfg)
	if err != nil {
		return err
	}
	for _, st := range shared {
		problem("target '%s' is also listed by set '%s': sets cannot share targets; 'add', 'update' and 'remove' "+
			"refuse to run until one of the files is fixed by hand", a.pretty(st.Target), st.Set)
	}

	targets := cfg.TargetDirs(a.Home)
	a.printf("Targets:\n")
	for _, t := range targets {
		if !fsutil.IsDir(t) {
			problem("target folder '%s' does not exist; it is created when a skill is next linked into it",
				a.pretty(t))
			continue
		}
		a.printf("  %s\n", a.pretty(t))
	}

	entries := playbook.Entries(cfg.Playbooks, a.Home)
	reported := map[string]bool{} // links already reported by a playbook's plan
	if len(entries) == 0 {
		a.printf("No playbooks are registered.\n")
	}
	for _, e := range entries {
		a.printf("Playbook %s (%s) at %s\n", e.ID, a.version(e.Dir), a.pretty(e.Dir))
		if err := a.checkPlaybookFolders(e); err != nil {
			problem("%s", err)
			continue
		}
		plan, err := playbook.Build(entries, e, targets)
		if err != nil {
			problem("playbook %s: %s", e.ID, err)
			continue
		}
		var changes []playbook.Action
		for _, act := range plan.Changes() {
			// A link can match several playbooks of the same folder (prefixes x and x-y): report it once.
			if !(act.Kind == playbook.Remove && reported[act.Link]) {
				changes = append(changes, act)
			}
		}
		for _, act := range plan.Actions {
			reported[act.Link] = true
		}
		if conflicts := plan.Conflicts(); len(conflicts) > 0 {
			for _, c := range conflicts {
				problem("playbook %s: %s", e.ID, a.conflictText(c))
			}
			problem("playbook %s: '%s' would be cancelled until the conflicts are resolved", e.ID,
				a.cmd("update "+e.ID))
		}
		if len(changes) == 0 && len(plan.Conflicts()) == 0 {
			a.printf("  up to date\n")
			continue
		}
		for _, act := range changes {
			switch act.Kind {
			case playbook.Create:
				problem("playbook %s: '%s' would link %s -> %s", e.ID, a.cmd("update "+e.ID), a.pretty(act.Link),
					a.pretty(act.Source))
			case playbook.Remove:
				problem("playbook %s: '%s' would remove %s (%s)", e.ID, a.cmd("update "+e.ID), a.pretty(act.Link),
					act.Reason)
			}
		}
	}

	// Broken links that no playbook plan covers (for example, links to an unregistered or moved playbook).
	for _, t := range targets {
		dir := playbook.SkillsFolder(t)
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			link := filepath.Join(dir, item.Name())
			if reported[link] {
				continue
			}
			state, current, err := fsutil.Inspect(link)
			if err == nil && state == fsutil.Link && !fsutil.IsDir(current) {
				problem("broken link %s -> %s (not managed by any registered playbook; remove it by hand)",
					a.pretty(link), a.pretty(current))
			}
		}
	}

	if attention > 0 {
		a.printf("\n%d item(s) need attention; see the warnings above.\n", attention)
		return errSilent
	}
	a.printf("\nEverything is up to date.\n")
	return nil
}
