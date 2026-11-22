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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

const checkUsage = `Usage: aiplam check [-s|--set SET]

Changes nothing. Checks the format and content of the installation's configuration (~/.aiplam/aiplam-config.yaml) and
of a set's file (~/.aiplam/<set>.yaml), the target and playbook folders and, if the set is enabled, whether 'aiplam
update' would change anything for each of its playbooks (links to create or remove) or would be cancelled by a
conflict. Also reports broken links in the targets' skills folders. Exits with 1 when anything needs attention.

  -s, --set SET   the set (default: the enabled set)
`

func (a *App) cmdCheck(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	attention := 0
	problem := func(format string, args ...any) {
		attention++
		a.warnf(format, args...)
	}
	invalid := func(file string, errs []error) error {
		for _, e := range errs {
			problem("%s: %s", file, e)
		}
		a.errorf("%d problem(s) in %s: fix them by hand; nothing else was checked.", len(errs), file)
		return errSilent
	}

	instFile := a.pretty(config.InstallFile(a.Home))
	inst, err := config.LoadInstall(a.Home)
	if errors.Is(err, config.ErrNotInitialized) {
		return errors.New("aiplam is not initialized: run 'aiplam init --target <folder>' first")
	}
	if err != nil {
		return invalid(instFile, []error{err})
	}
	a.printf("Installation: %s\n", instFile)
	if errs := inst.Validate(a.Home); len(errs) > 0 {
		return invalid(instFile, errs)
	}
	a.printf("  format and content are valid\n")

	set := inst.Enabled
	if a.Set != "" {
		set = a.Set
	}
	file := a.setFile(set)
	if !config.SetExists(a.Home, set) {
		if a.Set == "" {
			problem("the enabled set '%s' does not exist (%s is missing): enable another set with 'aiplam set enable "+
				"<set>', or create it again with 'aiplam set add %s'", set, file, set)
			return errSilent
		}
		return fmt.Errorf("set '%s' does not exist ('aiplam set list' lists the sets)", set)
	}
	enabled := config.SameSet(a.Home, set, inst.Enabled)
	if !enabled {
		a.hintSet = set
	}
	cfg, err := config.LoadSet(a.Home, set)
	if err != nil {
		return invalid(file, []error{err})
	}
	status := "enabled"
	if !enabled {
		status = "not enabled"
	}
	a.printf("Set %s (%s): %s\n", set, status, file)
	if errs := cfg.Validate(a.Home); len(errs) > 0 {
		return invalid(file, errs)
	}
	a.printf("  format and content are valid\n")

	targets := inst.TargetDirs(a.Home)
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
		if !enabled {
			// Nothing is installed; the set must only stay installable.
			overlaps, err := playbook.Overlaps(entries, e)
			if err != nil {
				problem("playbook %s: %s", e.ID, err)
				continue
			}
			for name, others := range overlaps {
				problem("playbook %s: skill %s: also provided by playbook %s; 'aiplam set enable %s' would be "+
					"cancelled", e.ID, name, strings.Join(others, ", "), set)
			}
			if len(overlaps) == 0 {
				a.printf("  folders are valid (not installed: the set is not enabled)\n")
			}
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
		if len(plan.Conflicts()) > 0 {
			for _, text := range a.conflictTexts(plan) {
				problem("playbook %s: %s", e.ID, text)
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
