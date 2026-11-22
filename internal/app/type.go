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
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/playbook"
)

const typeUsage = `Usage: aiplam type
       aiplam type agents|claude...

Without arguments, shows the types of agent folders of the configuration, and their folders (next to the .aiplam
folder): where the artifacts of the enabled sets' playbooks are installed (skills go to their skills/ subfolder).
With arguments, replaces them: ` + "'agents' (.agents) or 'claude' (.claude)" + `; several can be given, as separate
arguments or separated by commas. The enabled sets' playbooks are unlinked from the agent folders of the types
removed and linked into those of the types added. Nothing is changed if any link cannot be created because something
else is already in its place.
`

func (a *App) cmdType(args []string) error {
	if err := a.noSetOption("type"); err != nil {
		return err
	}
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	types, err := parseTypes(p.positional)
	if err != nil {
		return err
	}
	inst, err := a.loadInstall()
	if err != nil {
		return err
	}
	current := inst.TargetDirs(a.Root)
	if len(types) == 0 {
		a.printTypes(inst.Types)
		return nil
	}
	updated := *inst
	updated.Types = types
	if errs := updated.Validate(); len(errs) > 0 {
		return errs[0]
	}
	if sameTypes(inst.Types, types) {
		a.printf("The types are unchanged.\n")
		return nil
	}
	next := updated.TargetDirs(a.Root)

	sets, err := a.enabledSets(inst)
	if err != nil {
		return err
	}
	for _, s := range sets {
		for _, e := range s.entries {
			if err := a.checkPlaybookFolders(e); err != nil {
				return fmt.Errorf("%w; the types were not changed", err)
			}
		}
	}
	plan, err := playbook.Switch(groups(sets), groups(sets), nil, current, next, "type removed", false)
	if err != nil {
		return err
	}
	playbook.MarkAncestors(&plan, a.ancestorTargets())
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("the types were not changed: remove or rename what is in the way, then try again.")
		return errSilent
	}

	if len(inst.Enabled) == 0 {
		a.printf("Changing the types (no set is enabled)\n")
	} else {
		a.printf("Changing the types (enabled sets: %s)\n", strings.Join(inst.Enabled, ", "))
	}
	for _, dir := range next {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the types were not changed", err)
	}
	if err := config.SaveInstall(a.Root, &updated); err != nil {
		return a.undo(plan, err, "the types were not changed")
	}
	a.printTypes(updated.Types)
	return nil
}

// printTypes prints types and their agent folders: "agents  ~/.agents".
func (a *App) printTypes(types []string) {
	dirs := config.TargetDirs(types, a.Root)
	for i, t := range types {
		a.printf("%-8s %s\n", t, a.pretty(dirs[i]))
	}
}

// sameTypes reports whether two lists hold the same types, in any order.
func sameTypes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			found = found || x == y
		}
		if !found {
			return false
		}
	}
	return true
}
