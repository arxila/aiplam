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
       aiplam type <type>[,<type>...]

Shows or changes the types of agents configuration folders in the base folder, where artifacts are linked from
(skills, for example, from their skills/ subfolder):
  - agents: the .agents folder;
  - claude: the .claude folder.

Changing them is all-or-nothing:
  - the enabled profiles' playbooks are unlinked from the agents configuration folders of the types removed, and
    linked from those of the types added;
  - nothing is changed if something other than the right link is already where a link must go.

Examples:
  aiplam type                  # show the types and their folders
  aiplam type agents,claude    # link from .agents and .claude
  aiplam type claude           # .claude only: everything is unlinked from .agents
`

func (a *App) cmdType(args []string) error {
	if err := a.noProfileOption("type"); err != nil {
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
	conf, err := a.loadBaseConfig()
	if err != nil {
		return err
	}
	current := conf.AgentsConfigDirs(a.Base)
	if len(types) == 0 {
		a.printTypes(conf.Types)
		return nil
	}
	updated := *conf
	updated.Types = types
	if errs := updated.Validate(); len(errs) > 0 {
		return errs[0]
	}
	if sameTypes(conf.Types, types) {
		a.printf("The types are unchanged.\n")
		return nil
	}
	next := updated.AgentsConfigDirs(a.Base)

	profiles, err := a.enabledProfiles(conf)
	if err != nil {
		return err
	}
	for _, s := range profiles {
		for _, e := range s.entries {
			if err := a.checkPlaybookFolders(e); err != nil {
				return fmt.Errorf("%w; the types were not changed", err)
			}
		}
	}
	plan, err := playbook.Switch(groups(profiles), groups(profiles), nil, current, next, "no longer in the playbook",
		"type removed", false)
	if err != nil {
		return err
	}
	playbook.MarkAncestors(&plan, a.ancestorAgentsConfigDirs())
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("the types were not changed: %s.", a.conflictAdvice(plan))
		return errSilent
	}

	if len(conf.Enabled) == 0 {
		a.printf("Changing the types (no profile is enabled)\n")
	} else {
		a.printf("Changing the types (enabled profiles: %s)\n", strings.Join(conf.Enabled, ", "))
	}
	for _, dir := range next {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the types were not changed", err)
	}
	if err := config.SaveBaseConfig(a.Base, &updated); err != nil {
		return a.undo(plan, err, "the types were not changed")
	}
	a.printTypes(updated.Types)
	return nil
}

// printTypes prints types and their agents configuration folders: "agents  ~/.agents".
func (a *App) printTypes(types []string) {
	dirs := config.AgentsConfigDirs(types, a.Base)
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
