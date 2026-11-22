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
	"arxila.io/aiplam/internal/playbook"
)

const setUsage = `Usage: aiplam set list
       aiplam set add <set>
       aiplam set remove <set>
       aiplam set enable <set>

Playbooks are registered in sets, each kept in its own file (~/.aiplam/<set>.yaml). Exactly one set is enabled: its
playbooks are the ones installed in the targets. 'add', 'update', 'remove', 'list' and 'check' act on the enabled set
unless given --set.

  list     list the sets, marking the enabled one with '*'
  add      create an empty set (names: letters, digits, '_' and '-'; '` + config.InstallName + `' is reserved)
  remove   delete a set that is not enabled, unregistering its playbooks (nothing is unlinked, since they are not
           installed)
  enable   enable a set: unlink the playbooks of the enabled set from the targets and link those of this set. Nothing
           is changed if any link cannot be created because something else is in its place
`

func (a *App) cmdSet(args []string) error {
	if err := a.noSetOption("set"); err != nil {
		return err
	}
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usagef("set needs a subcommand: list, add, remove or enable")
	}
	sub, rest := p.positional[0], p.positional[1:]
	if sub == "list" {
		if len(rest) > 0 {
			return usagef("unexpected argument: %s", rest[0])
		}
		return a.setList()
	}
	if sub != "add" && sub != "remove" && sub != "enable" {
		return usagef("unknown set subcommand: %s", sub)
	}
	if len(rest) != 1 {
		return usagef("set %s needs a set name", sub)
	}
	name := rest[0]
	if !config.ValidSetName(name) {
		return invalidSetName(name)
	}
	inst, err := a.loadInstall()
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return a.setAdd(name)
	case "remove":
		return a.setRemove(inst, name)
	default:
		return a.setEnable(inst, name)
	}
}

func (a *App) setList() error {
	inst, err := a.loadInstall()
	if err != nil {
		return err
	}
	sets, err := config.Sets(a.Home)
	if err != nil {
		return err
	}
	enabledFound := false
	for _, set := range sets {
		mark := " "
		if config.SameSet(a.Home, set, inst.Enabled) {
			mark, enabledFound = "*", true
		}
		cfg, err := config.LoadSet(a.Home, set)
		if err != nil {
			a.printf("%s %-20s cannot be read (%s); see 'aiplam check --set %s'\n", mark, set, err, set)
			continue
		}
		a.printf("%s %-20s %d playbook(s)\n", mark, set, len(cfg.Playbooks))
	}
	if !enabledFound {
		a.warnf("the enabled set '%s' does not exist: enable another set with 'aiplam set enable <set>'.", inst.Enabled)
	}
	return nil
}

func (a *App) setAdd(name string) error {
	if config.SetExists(a.Home, name) {
		return fmt.Errorf("set '%s' already exists (%s)", name, a.setFile(name))
	}
	if err := config.SaveSet(a.Home, name, &config.Set{}); err != nil {
		return err
	}
	a.printf("Created set '%s' (%s).\n", name, a.setFile(name))
	a.printf("Register playbooks in it with 'aiplam add <name> <folder> --set %s', and enable it with "+
		"'aiplam set enable %s'.\n", name, name)
	return nil
}

func (a *App) setRemove(inst *config.Install, name string) error {
	if !config.SetExists(a.Home, name) {
		return fmt.Errorf("set '%s' does not exist ('aiplam set list' lists the sets)", name)
	}
	if config.SameSet(a.Home, name, inst.Enabled) {
		return fmt.Errorf("set '%s' is enabled and cannot be removed: enable another set first", name)
	}
	count := 0
	if cfg, err := config.LoadSet(a.Home, name); err == nil {
		count = len(cfg.Playbooks)
	}
	if err := os.Remove(config.SetFile(a.Home, name)); err != nil {
		return err
	}
	a.printf("Removed set '%s' and its %d playbook registration(s); nothing was unlinked, since the set was not "+
		"enabled.\n", name, count)
	return nil
}

func (a *App) setEnable(inst *config.Install, name string) error {
	if !config.SetExists(a.Home, name) {
		return fmt.Errorf("set '%s' does not exist; create it with 'aiplam set add %s'", name, name)
	}
	if config.SameSet(a.Home, name, inst.Enabled) {
		a.printf("Set '%s' is already enabled.\n", name)
		return nil
	}
	targets := inst.TargetDirs(a.Home)

	// The playbooks of the enabled set, to unlink. If its file is gone, its links cannot be found.
	var from []playbook.Entry
	if config.SetExists(a.Home, inst.Enabled) {
		old, err := a.loadSet(inst.Enabled)
		if err != nil {
			return err
		}
		from = playbook.Entries(old.Playbooks, a.Home)
	} else {
		a.warnf("the enabled set '%s' does not exist: its links (if any) cannot be removed; 'aiplam check' reports "+
			"those that are broken.", inst.Enabled)
	}
	cfg, err := a.loadSet(name)
	if err != nil {
		return err
	}
	to := playbook.Entries(cfg.Playbooks, a.Home)
	for _, e := range to {
		if err := a.checkPlaybookFolders(e); err != nil {
			return fmt.Errorf("%w; set '%s' was not enabled", err, name)
		}
	}
	plan, err := playbook.Switch(from, to, targets, targets, fmt.Sprintf("set '%s' disabled", inst.Enabled))
	if err != nil {
		return err
	}
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("set '%s' was not enabled: remove or rename what is in the way, then try again.", name)
		return errSilent
	}

	a.printf("Enabling set '%s' (disabling '%s')\n", name, inst.Enabled)
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; set '%s' was not enabled", err, name)
	}
	updated := *inst
	updated.Enabled = name
	if err := config.SaveInstall(a.Home, &updated); err != nil {
		return a.undo(plan, err, fmt.Sprintf("set '%s' is still enabled", inst.Enabled))
	}
	a.printf("Set '%s' is enabled, with %d playbook(s).\n", name, len(to))
	return nil
}
