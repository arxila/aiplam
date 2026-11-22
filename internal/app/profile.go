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

const profileUsage = `Usage: aiplam profile list
       aiplam profile add <profile>
       aiplam profile remove <profile>
       aiplam profile enable <profile>[,<profile>...]
       aiplam profile disable <profile>[,<profile>...]
       aiplam profile switch <profile>[,<profile>...]

Manages profiles: groups of registered playbooks, each kept in its own file (<basefolder>/.aiplam/<profile>.yaml):
  - only the playbooks of the enabled profiles are linked from the agents configuration folders;
  - 'add', 'update', 'remove', 'list' and 'check' act on the enabled profiles unless given --profile.

Subcommands:
  list      list the profiles; '*' marks the enabled ones
  add       create an empty profile (names: letters, digits, '_' and '-'; '` + config.BaseConfigName + `' is reserved)
  remove    delete a profile that is not enabled, with its playbook registrations (nothing is unlinked)
  enable    enable profiles besides those already enabled: link their playbooks
  disable   disable profiles: unlink their playbooks
  switch    enable exactly the given profiles: disable the others, then enable these (links that stay the same are left
            alone)

Profiles enabled together cannot collide: enabling a profile fails, changing nothing, if any link it needs already
exists in an agents configuration folder, whoever made it (another enabled profile, or anything else).

Examples:
  aiplam profile add acme
  aiplam add acme ~/Workspace/acme-ai-playbook --profile acme
  aiplam profile enable acme            # link acme's playbooks too
  aiplam profile switch default,acme    # enable exactly default and acme
  aiplam profile disable acme           # unlink acme's playbooks
`

func (a *App) cmdProfile(args []string) error {
	if err := a.noProfileOption("profile"); err != nil {
		return err
	}
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usagef("profile needs a subcommand: list, add, remove, enable, disable or switch")
	}
	sub, rest := p.positional[0], p.positional[1:]
	switch sub {
	case "list":
		if len(rest) > 0 {
			return usagef("unexpected argument: %s", rest[0])
		}
		return a.profileList()
	case "add", "remove":
		if len(rest) != 1 {
			return usagef("profile %s needs one profile name", sub)
		}
		if !config.ValidProfileName(rest[0]) {
			return invalidProfileName(rest[0])
		}
		conf, err := a.loadBaseConfig()
		if err != nil {
			return err
		}
		if sub == "add" {
			return a.profileAdd(rest[0])
		}
		return a.profileRemove(conf, rest[0])
	case "enable", "disable", "switch":
		names, err := profileNameList(rest)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return usagef("profile %s needs one or more profile names", sub)
		}
		conf, err := a.loadBaseConfig()
		if err != nil {
			return err
		}
		for _, name := range names {
			if sub != "disable" && !config.ProfileExists(a.Base, name) {
				return fmt.Errorf("profile '%s' does not exist; create it with '%s'", name, a.hint("profile add "+name))
			}
		}
		switch sub {
		case "enable":
			return a.profileEnable(conf, names)
		case "disable":
			return a.profileDisable(conf, names)
		default:
			return a.profileSwitch(conf, names)
		}
	default:
		return usagef("unknown profile subcommand: %s", sub)
	}
}

// profileNameList parses profile names given as arguments, each possibly a comma-separated list, dropping repetitions.
func profileNameList(args []string) ([]string, error) {
	var names []string
	for _, arg := range args {
		for _, name := range strings.Split(arg, ",") {
			name = strings.TrimSpace(name)
			if !config.ValidProfileName(name) {
				return nil, invalidProfileName(name)
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

func (a *App) profileList() error {
	conf, err := a.loadBaseConfig()
	if err != nil {
		return err
	}
	profiles, err := config.Profiles(a.Base)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		mark := " "
		if conf.IsEnabled(a.Base, profile) {
			mark = "*"
		}
		prof, err := config.LoadProfile(a.Base, profile)
		if err != nil {
			a.printf("%s %-20s cannot be read (%s); see '%s'\n", mark, profile, err, a.hint("check --profile "+profile))
			continue
		}
		a.printf("%s %-20s %d playbook(s)\n", mark, profile, len(prof.Playbooks))
	}
	for _, name := range conf.Enabled {
		if !config.ProfileExists(a.Base, name) {
			a.warnf("the enabled profile '%s' does not exist: disable it with '%s'.", name, a.hint("profile disable "+name))
		}
	}
	if len(conf.Enabled) == 0 {
		a.printf("No profile is enabled: enable one with '%s'.\n", a.hint("profile enable <profile>"))
	}
	return nil
}

func (a *App) profileAdd(name string) error {
	if config.ProfileExists(a.Base, name) {
		return fmt.Errorf("profile '%s' already exists (%s)", name, a.profileFile(name))
	}
	if err := config.SaveProfile(a.Base, name, &config.Profile{}); err != nil {
		return err
	}
	a.printf("Created profile '%s' (%s).\n", name, a.profileFile(name))
	a.printf("Register playbooks in it with '%s', and enable it with '%s'.\n",
		a.hint("add <name> <playbookfolder> --profile "+name), a.hint("profile enable "+name))
	return nil
}

func (a *App) profileRemove(conf *config.BaseConfig, name string) error {
	if !config.ProfileExists(a.Base, name) {
		return fmt.Errorf("profile '%s' does not exist ('%s' lists the profiles)", name, a.hint("profile list"))
	}
	if conf.IsEnabled(a.Base, name) {
		return fmt.Errorf("profile '%s' is enabled and cannot be removed: disable it first with '%s'", name,
			a.hint("profile disable "+name))
	}
	count := 0
	if prof, err := config.LoadProfile(a.Base, name); err == nil {
		count = len(prof.Playbooks)
	}
	if err := os.Remove(config.ProfileFile(a.Base, name)); err != nil {
		return err
	}
	a.printf("Removed profile '%s' and its %d playbook registration(s); nothing was unlinked, since the profile was not "+
		"enabled.\n", name, count)
	return nil
}

// enabledProfiles loads the enabled profiles. Those whose file is missing are left out, with a warning: their links (if
// any) cannot be found.
func (a *App) enabledProfiles(conf *config.BaseConfig) ([]*profileState, error) {
	var profiles []*profileState
	for _, name := range conf.Enabled {
		if !config.ProfileExists(a.Base, name) {
			a.warnf("the enabled profile '%s' does not exist: its links (if any) cannot be found; '%s' reports those "+
				"that are broken.", name, a.hint("check"))
			continue
		}
		s, err := a.loadProfileState(name, true)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, s)
	}
	return profiles, nil
}

// split separates profiles into those named in names and the others.
func (a *App) split(profiles []*profileState, names []string) (in, out []*profileState) {
	for _, s := range profiles {
		found := false
		for _, n := range names {
			found = found || config.SameProfile(a.Base, s.name, n)
		}
		if found {
			in = append(in, s)
		} else {
			out = append(out, s)
		}
	}
	return in, out
}

func groups(profiles []*profileState) [][]playbook.Entry {
	var g [][]playbook.Entry
	for _, s := range profiles {
		g = append(g, s.entries)
	}
	return g
}

func (a *App) profileEnable(conf *config.BaseConfig, names []string) error {
	current, err := a.enabledProfiles(conf)
	if err != nil {
		return err
	}
	var added []*profileState
	for _, name := range names {
		if conf.IsEnabled(a.Base, name) {
			a.printf("Profile '%s' is already enabled.\n", name)
			continue
		}
		s, err := a.loadProfileState(name, true)
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
	return a.changeProfiles(conf, &updated, nil, added, current)
}

func (a *App) profileDisable(conf *config.BaseConfig, names []string) error {
	var notEnabled []string
	for _, name := range names {
		if !conf.IsEnabled(a.Base, name) {
			notEnabled = append(notEnabled, name)
		}
	}
	if len(notEnabled) > 0 {
		return fmt.Errorf("not enabled: %s ('%s' shows the enabled profiles)", strings.Join(notEnabled, ", "),
			a.hint("profile list"))
	}
	current, err := a.enabledProfiles(conf)
	if err != nil {
		return err
	}
	removed, stay := a.split(current, names)
	updated := *conf
	updated.Enabled = nil
	for _, e := range conf.Enabled {
		keep := true
		for _, n := range names {
			keep = keep && !config.SameProfile(a.Base, e, n) && !strings.EqualFold(e, n)
		}
		if keep {
			updated.Enabled = append(updated.Enabled, e)
		}
	}
	return a.changeProfiles(conf, &updated, removed, nil, stay)
}

func (a *App) profileSwitch(conf *config.BaseConfig, names []string) error {
	current, err := a.enabledProfiles(conf)
	if err != nil {
		return err
	}
	stay, removed := a.split(current, names)
	var added []*profileState
	for _, name := range names {
		if conf.IsEnabled(a.Base, name) {
			continue
		}
		s, err := a.loadProfileState(name, true)
		if err != nil {
			return err
		}
		added = append(added, s)
	}
	updated := *conf
	updated.Enabled = names
	if len(added) == 0 && len(removed) == 0 && len(conf.Enabled) == len(names) {
		a.printf("The enabled profiles are unchanged.\n")
		return nil
	}
	return a.changeProfiles(conf, &updated, removed, added, stay)
}

func namesOf(profiles []*profileState) []string {
	var names []string
	for _, s := range profiles {
		names = append(names, s.name)
	}
	return names
}

// changeProfiles unlinks the playbooks of the profiles in removed, links those of the profiles in added (every one of
// their links must be new), keeps those of the profiles in stay, and saves updated as the base folder's configuration.
// It is all-or-nothing.
func (a *App) changeProfiles(conf, updated *config.BaseConfig, removed, added, stay []*profileState) error {
	for _, s := range added {
		for _, e := range s.entries {
			if err := a.checkPlaybookFolders(e); err != nil {
				return fmt.Errorf("%w; profile '%s' cannot be enabled, so nothing was changed", err, s.name)
			}
		}
	}
	agentsConfigDirs := conf.AgentsConfigDirs(a.Base)
	plan, err := playbook.Switch(groups(removed), groups(added), groups(stay), agentsConfigDirs, agentsConfigDirs,
		"profile disabled", "profile disabled", true)
	if err != nil {
		return err
	}
	playbook.MarkAncestors(&plan, a.ancestorAgentsConfigDirs())
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		advice := a.conflictAdvice(plan)
		if hasConflictsOtherThan(plan, playbook.Ancestor) {
			advice = "profiles enabled together cannot collide, so every link of a profile being enabled must be new; " +
				strings.Replace(advice, "what is in the way", "what is in the way (or disable the profile that provides it)", 1)
		}
		a.errorf("nothing was changed: %s.", advice)
		return errSilent
	}

	switch {
	case len(removed) > 0 && len(added) > 0:
		a.printf("Disabling %s; enabling %s\n", profileNames(removed), profileNames(added))
	case len(added) > 0:
		a.printf("Enabling %s\n", profileNames(added))
	case len(removed) > 0:
		a.printf("Disabling %s\n", profileNames(removed))
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the enabled profiles were not changed", err)
	}
	if err := config.SaveBaseConfig(a.Base, updated); err != nil {
		return a.undo(plan, err, "the enabled profiles were not changed")
	}
	if len(updated.Enabled) == 0 {
		a.printf("No profile is enabled now.\n")
	} else {
		a.printf("Enabled profiles: %s.\n", strings.Join(updated.Enabled, ", "))
	}
	return nil
}
