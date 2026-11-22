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

const setUsage = `Usage: aiplam set list
       aiplam set add <set>
       aiplam set remove <set>
       aiplam set enable <set>[,<set>...]
       aiplam set disable <set>[,<set>...]
       aiplam set switch <set>[,<set>...]

Manages sets: groups of registered playbooks, each kept in its own file (<basefolder>/.aiplam/<set>.yaml):
  - only the playbooks of the enabled sets are linked from the agents configuration folders;
  - 'add', 'update', 'remove', 'list' and 'check' act on the enabled sets unless given --set.

Subcommands:
  list      list the sets; '*' marks the enabled ones
  add       create an empty set (names: letters, digits, '_' and '-'; '` + config.BaseConfigName + `' is reserved)
  remove    delete a set that is not enabled, with its playbook registrations (nothing is unlinked)
  enable    enable sets besides those already enabled: link their playbooks
  disable   disable sets: unlink their playbooks
  switch    enable exactly the given sets: disable the others, then enable these (links that stay the same are left
            alone)

Sets enabled together cannot collide: enabling a set fails, changing nothing, if any link it needs already exists in
an agents configuration folder, whoever made it (another enabled set, or anything else).

Examples:
  aiplam set add acme
  aiplam add acme ~/Workspace/acme-ai-playbook --set acme
  aiplam set enable acme            # link acme's playbooks too
  aiplam set switch default,acme    # enable exactly default and acme
  aiplam set disable acme           # unlink acme's playbooks
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
		return usagef("set needs a subcommand: list, add, remove, enable, disable or switch")
	}
	sub, rest := p.positional[0], p.positional[1:]
	switch sub {
	case "list":
		if len(rest) > 0 {
			return usagef("unexpected argument: %s", rest[0])
		}
		return a.setList()
	case "add", "remove":
		if len(rest) != 1 {
			return usagef("set %s needs one set name", sub)
		}
		if !config.ValidSetName(rest[0]) {
			return invalidSetName(rest[0])
		}
		conf, err := a.loadBaseConfig()
		if err != nil {
			return err
		}
		if sub == "add" {
			return a.setAdd(rest[0])
		}
		return a.setRemove(conf, rest[0])
	case "enable", "disable", "switch":
		names, err := setNameList(rest)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return usagef("set %s needs one or more set names", sub)
		}
		conf, err := a.loadBaseConfig()
		if err != nil {
			return err
		}
		for _, name := range names {
			if sub != "disable" && !config.SetExists(a.Base, name) {
				return fmt.Errorf("set '%s' does not exist; create it with '%s'", name, a.hint("set add "+name))
			}
		}
		switch sub {
		case "enable":
			return a.setEnable(conf, names)
		case "disable":
			return a.setDisable(conf, names)
		default:
			return a.setSwitch(conf, names)
		}
	default:
		return usagef("unknown set subcommand: %s", sub)
	}
}

// setNameList parses set names given as arguments, each possibly a comma-separated list, dropping repetitions.
func setNameList(args []string) ([]string, error) {
	var names []string
	for _, arg := range args {
		for _, name := range strings.Split(arg, ",") {
			name = strings.TrimSpace(name)
			if !config.ValidSetName(name) {
				return nil, invalidSetName(name)
			}
			dup := false
			for _, n := range names {
				dup = dup || strings.EqualFold(n, name)
			}
			if !dup {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

func (a *App) setList() error {
	conf, err := a.loadBaseConfig()
	if err != nil {
		return err
	}
	sets, err := config.Sets(a.Base)
	if err != nil {
		return err
	}
	for _, set := range sets {
		mark := " "
		if conf.IsEnabled(a.Base, set) {
			mark = "*"
		}
		cfg, err := config.LoadSet(a.Base, set)
		if err != nil {
			a.printf("%s %-20s cannot be read (%s); see '%s'\n", mark, set, err, a.hint("check --set "+set))
			continue
		}
		a.printf("%s %-20s %d playbook(s)\n", mark, set, len(cfg.Playbooks))
	}
	for _, name := range conf.Enabled {
		if !config.SetExists(a.Base, name) {
			a.warnf("the enabled set '%s' does not exist: disable it with '%s'.", name, a.hint("set disable "+name))
		}
	}
	if len(conf.Enabled) == 0 {
		a.printf("No set is enabled: enable one with '%s'.\n", a.hint("set enable <set>"))
	}
	return nil
}

func (a *App) setAdd(name string) error {
	if config.SetExists(a.Base, name) {
		return fmt.Errorf("set '%s' already exists (%s)", name, a.setFile(name))
	}
	if err := config.SaveSet(a.Base, name, &config.Set{}); err != nil {
		return err
	}
	a.printf("Created set '%s' (%s).\n", name, a.setFile(name))
	a.printf("Register playbooks in it with '%s', and enable it with '%s'.\n",
		a.hint("add <name> <playbookfolder> --set "+name), a.hint("set enable "+name))
	return nil
}

func (a *App) setRemove(conf *config.BaseConfig, name string) error {
	if !config.SetExists(a.Base, name) {
		return fmt.Errorf("set '%s' does not exist ('%s' lists the sets)", name, a.hint("set list"))
	}
	if conf.IsEnabled(a.Base, name) {
		return fmt.Errorf("set '%s' is enabled and cannot be removed: disable it first with '%s'", name,
			a.hint("set disable "+name))
	}
	count := 0
	if cfg, err := config.LoadSet(a.Base, name); err == nil {
		count = len(cfg.Playbooks)
	}
	if err := os.Remove(config.SetFile(a.Base, name)); err != nil {
		return err
	}
	a.printf("Removed set '%s' and its %d playbook registration(s); nothing was unlinked, since the set was not "+
		"enabled.\n", name, count)
	return nil
}

// enabledSets loads the enabled sets. Those whose file is missing are left out, with a warning: their links (if any)
// cannot be found.
func (a *App) enabledSets(conf *config.BaseConfig) ([]*setState, error) {
	var sets []*setState
	for _, name := range conf.Enabled {
		if !config.SetExists(a.Base, name) {
			a.warnf("the enabled set '%s' does not exist: its links (if any) cannot be found; '%s' reports those "+
				"that are broken.", name, a.hint("check"))
			continue
		}
		s, err := a.loadSetState(name, true)
		if err != nil {
			return nil, err
		}
		sets = append(sets, s)
	}
	return sets, nil
}

// split separates sets into those named in names and the others.
func (a *App) split(sets []*setState, names []string) (in, out []*setState) {
	for _, s := range sets {
		found := false
		for _, n := range names {
			found = found || config.SameSet(a.Base, s.name, n)
		}
		if found {
			in = append(in, s)
		} else {
			out = append(out, s)
		}
	}
	return in, out
}

func groups(sets []*setState) [][]playbook.Entry {
	var g [][]playbook.Entry
	for _, s := range sets {
		g = append(g, s.entries)
	}
	return g
}

func (a *App) setEnable(conf *config.BaseConfig, names []string) error {
	current, err := a.enabledSets(conf)
	if err != nil {
		return err
	}
	var added []*setState
	for _, name := range names {
		if conf.IsEnabled(a.Base, name) {
			a.printf("Set '%s' is already enabled.\n", name)
			continue
		}
		s, err := a.loadSetState(name, true)
		if err != nil {
			return err
		}
		added = append(added, s)
	}
	if len(added) == 0 {
		return nil
	}
	updated := *conf
	updated.Enabled = append(append([]string{}, conf.Enabled...), namesOf(added)...)
	return a.changeSets(conf, &updated, nil, added, current)
}

func (a *App) setDisable(conf *config.BaseConfig, names []string) error {
	var notEnabled []string
	for _, name := range names {
		if !conf.IsEnabled(a.Base, name) {
			notEnabled = append(notEnabled, name)
		}
	}
	if len(notEnabled) > 0 {
		return fmt.Errorf("not enabled: %s ('%s' shows the enabled sets)", strings.Join(notEnabled, ", "),
			a.hint("set list"))
	}
	current, err := a.enabledSets(conf)
	if err != nil {
		return err
	}
	removed, stay := a.split(current, names)
	updated := *conf
	updated.Enabled = nil
	for _, e := range conf.Enabled {
		keep := true
		for _, n := range names {
			keep = keep && !config.SameSet(a.Base, e, n) && !strings.EqualFold(e, n)
		}
		if keep {
			updated.Enabled = append(updated.Enabled, e)
		}
	}
	return a.changeSets(conf, &updated, removed, nil, stay)
}

func (a *App) setSwitch(conf *config.BaseConfig, names []string) error {
	current, err := a.enabledSets(conf)
	if err != nil {
		return err
	}
	stay, removed := a.split(current, names)
	var added []*setState
	for _, name := range names {
		if conf.IsEnabled(a.Base, name) {
			continue
		}
		s, err := a.loadSetState(name, true)
		if err != nil {
			return err
		}
		added = append(added, s)
	}
	updated := *conf
	updated.Enabled = names
	if len(added) == 0 && len(removed) == 0 && len(conf.Enabled) == len(names) {
		a.printf("The enabled sets are unchanged.\n")
		return nil
	}
	return a.changeSets(conf, &updated, removed, added, stay)
}

func namesOf(sets []*setState) []string {
	var names []string
	for _, s := range sets {
		names = append(names, s.name)
	}
	return names
}

// changeSets unlinks the playbooks of the sets in removed, links those of the sets in added (every one of their links
// must be new), keeps those of the sets in stay, and saves updated as the base folder's configuration. It is
// all-or-nothing.
func (a *App) changeSets(conf, updated *config.BaseConfig, removed, added, stay []*setState) error {
	for _, s := range added {
		for _, e := range s.entries {
			if err := a.checkPlaybookFolders(e); err != nil {
				return fmt.Errorf("%w; set '%s' cannot be enabled, so nothing was changed", err, s.name)
			}
		}
	}
	agentsConfigDirs := conf.AgentsConfigDirs(a.Base)
	plan, err := playbook.Switch(groups(removed), groups(added), groups(stay), agentsConfigDirs, agentsConfigDirs,
		"set disabled", "set disabled", true)
	if err != nil {
		return err
	}
	playbook.MarkAncestors(&plan, a.ancestorAgentsConfigDirs())
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		advice := a.conflictAdvice(plan)
		if hasConflictsOtherThan(plan, playbook.Ancestor) {
			advice = "sets enabled together cannot collide, so every link of a set being enabled must be new; " +
				strings.Replace(advice, "what is in the way", "what is in the way (or disable the set that provides it)", 1)
		}
		a.errorf("nothing was changed: %s.", advice)
		return errSilent
	}

	switch {
	case len(removed) > 0 && len(added) > 0:
		a.printf("Disabling %s; enabling %s\n", setNames(removed), setNames(added))
	case len(added) > 0:
		a.printf("Enabling %s\n", setNames(added))
	case len(removed) > 0:
		a.printf("Disabling %s\n", setNames(removed))
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the enabled sets were not changed", err)
	}
	if err := config.SaveBaseConfig(a.Base, updated); err != nil {
		return a.undo(plan, err, "the enabled sets were not changed")
	}
	if len(updated.Enabled) == 0 {
		a.printf("No set is enabled now.\n")
	} else {
		a.printf("Enabled sets: %s.\n", strings.Join(updated.Enabled, ", "))
	}
	return nil
}
