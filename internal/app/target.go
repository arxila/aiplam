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
	"fmt"
	"os"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

const targetUsage = `Usage: aiplam target
       aiplam target agents|claude|DIR...

Without arguments, shows the target folders: where the artifacts of the enabled set's playbooks are installed (skills
go to their skills/ subfolder). With arguments, replaces them: 'agents' (~/.agents), 'claude' (~/.claude) or any
folder; several can be given, as separate arguments or separated by commas. The enabled set's playbooks are unlinked
from the folders that are no longer targets and linked into the new ones. Nothing is changed if any link cannot be
created because something else is already in its place.
`

func (a *App) cmdTarget(args []string) error {
	if err := a.noSetOption("target"); err != nil {
		return err
	}
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	inst, err := a.loadInstall()
	if err != nil {
		return err
	}
	current := inst.TargetDirs(a.Home)
	if len(p.positional) == 0 {
		for _, t := range current {
			a.printf("%s\n", a.pretty(t))
		}
		return nil
	}
	targets, err := a.parseTargets(p.positional)
	if err != nil {
		return err
	}
	updated := *inst
	updated.Targets = targets
	if errs := updated.Validate(a.Home); len(errs) > 0 {
		return errs[0]
	}
	next := updated.TargetDirs(a.Home)
	if sameTargets(current, next) {
		a.printf("The targets are unchanged.\n")
		return nil
	}

	var entries []playbook.Entry
	if config.SetExists(a.Home, inst.Enabled) {
		cfg, err := a.loadSet(inst.Enabled)
		if err != nil {
			return err
		}
		entries = playbook.Entries(cfg.Playbooks, a.Home)
	} else {
		a.warnf("the enabled set '%s' does not exist: there is nothing to unlink or link.", inst.Enabled)
	}
	for _, e := range entries {
		if err := a.checkPlaybookFolders(e); err != nil {
			return fmt.Errorf("%w; the targets were not changed", err)
		}
	}
	plan, err := playbook.Switch(entries, entries, current, next, "no longer in the playbook")
	if err != nil {
		return err
	}
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("the targets were not changed: remove or rename what is in the way, then try again.")
		return errSilent
	}

	a.printf("Changing the targets (enabled set: '%s')\n", inst.Enabled)
	for _, dir := range next {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the targets were not changed", err)
	}
	if err := config.SaveInstall(a.Home, &updated); err != nil {
		return a.undo(plan, err, "the targets were not changed")
	}
	a.printf("Targets:\n")
	for _, t := range next {
		a.printf("  %s\n", a.pretty(t))
	}
	return nil
}

// sameTargets reports whether two lists hold the same folders, in any order.
func sameTargets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if fsutil.SamePath(x, y) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
