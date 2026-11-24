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

var profileUsage = `Usage: aiplam profile list
       aiplam profile add <profile> <agent>
       aiplam profile remove <profile>
       aiplam profile enable <profile>[,<profile>...]
       aiplam profile disable <profile>[,<profile>...]
       aiplam profile switch <profile>[,<profile>...]
       aiplam profile agent <profile> <agent>

Manages profiles: working scopes, such as a client, a team or a kind of work, each a group of registered playbooks
kept in its own file (<basefolder>/.aiplam/<profile>.yaml), with an agent assigned:
  - only the playbooks of the enabled profiles are linked, so switching scopes is enabling and disabling profiles;
  - several profiles can be enabled at once, combining their scopes;
  - each profile's playbooks are linked for its agent: usually the agent you work with, the same for every profile,
    though each profile can have its own;
  - 'add', 'update', 'remove', 'list' and 'check' act on the enabled profiles unless given --profile.

Subcommands:
  list      list the profiles and their agents; '*' marks the enabled ones
  add       create an empty profile, with its agent; names: letters, digits, '_' and '-'
            ('` + config.BaseConfigName + `' is reserved)
  remove    delete a profile that is not enabled, with its playbook registrations (nothing is unlinked)
  enable    enable profiles besides those already enabled: link their playbooks
  disable   disable profiles: unlink their playbooks
  switch    enable exactly the given profiles: disable the others, then enable these (links that stay the same are left
            alone)
  agent     change a profile's agent; if it is enabled, its playbooks are unlinked for the old agent and linked for
            the new one, all-or-nothing (playbook folders left to their defaults follow the new agent's)

How a profile's agent is used:
  - its playbooks' artifacts are linked from the agent's folder for each kind, in the base folder (which can differ
    between a project and the home folder: .github for Copilot in a project, ~/.copilot in the home folder);
  - only the kinds the agent reads, and the files in its format, are linked; the others are ignored;
  - by default, a playbook's folder for each kind is named like the agent's subfolder for it ('aiplam add --help');
  - subagents are custom agent definitions, which some agents (such as Copilot and OpenCode) also offer as primary
    agents;
  - folders are created when an artifact is first linked there, and removed when unlinking leaves them empty (such
    as .claude/agents, and then .claude itself), but never when they hold anything else, such as artifacts added by
    hand.

Profiles enabled together cannot collide: enabling a profile fails, changing nothing, if any link it needs already
exists, whoever made it (another enabled profile, or anything else). When enabled profiles have different agents,
they only collide where the agents share a folder: codex and the standard agent both link skills from
.agents/skills. Many agents also read .agents/skills besides their own folders: when a profile links a skill for one
of them and another enabled profile links the same skill from .agents/skills, aiplam warns that the agent may load it
twice.

Agents, and where aiplam links each kind of artifact for them (in a project, in the home folder):
` + agentsHelp() + `
Examples:
  aiplam profile add acme claude                               # a profile for the work for client Acme
  aiplam add acme ~/Workspace/acme-ai-playbook --profile acme
  aiplam profile enable acme                                   # link Acme's playbooks too
  aiplam profile switch acme                                   # work only on Acme: disable the other profiles
  aiplam profile switch default,acme                           # enable exactly default and acme
  aiplam profile agent acme copilot                            # Acme's projects now use GitHub Copilot
  aiplam profile disable acme                                  # unlink Acme's playbooks
`

func (a *App) cmdProfile(args []string) error {
	if err := a.noProfileOption("profile"); err != nil {
		return err
	}
	p, err := parseArgs(args, []string{"agent"}, nil)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usagef("profile needs a subcommand: list, add, remove, enable, disable, switch or agent")
	}
	sub, rest := p.positional[0], p.positional[1:]
	if _, ok := p.values["agent"]; ok {
		return usagef("profile takes the agent as an argument, not with --agent: 'aiplam profile add <profile> " +
			"<agent>' creates a profile, and 'aiplam profile agent <profile> <agent>' changes its agent")
	}
	switch sub {
	case "list":
		if len(rest) > 0 {
			return usagef("unexpected argument: %s", rest[0])
		}
		return a.profileList()
	case "add":
		agents := strings.Join(config.AgentNames(), ", ")
		switch {
		case len(rest) == 0:
			return usagef("profile add needs a profile name and an agent (%s)", agents)
		case len(rest) == 1:
			if _, ok := config.LookupAgent(rest[0]); ok {
				return usagef("profile add needs the profile name before the agent: 'aiplam profile add <profile> %s'",
					rest[0])
			}
			return usagef("profile add needs the agent after the profile name: 'aiplam profile add %s <agent>', with "+
				"<agent> one of %s", rest[0], agents)
		case len(rest) > 2:
			return usagef("unexpected argument: %s", rest[2])
		}
		agent, ok := config.LookupAgent(rest[1])
		if !ok {
			if _, swapped := config.LookupAgent(rest[0]); swapped {
				return usagef("profile add takes the profile name first, then the agent: 'aiplam profile add %s %s'",
					rest[1], rest[0])
			}
			return usagef("unknown agent '%s': use %s", rest[1], agents)
		}
		if !config.ValidProfileName(rest[0]) {
			return invalidProfileName(rest[0])
		}
		if _, err := a.loadBaseConfig(); err != nil {
			return err
		}
		return a.profileAdd(rest[0], agent)
	case "remove":
		if len(rest) != 1 {
			return usagef("profile remove needs one profile name")
		}
		if !config.ValidProfileName(rest[0]) {
			return invalidProfileName(rest[0])
		}
		conf, err := a.loadBaseConfig()
		if err != nil {
			return err
		}
		return a.profileRemove(conf, rest[0])
	case "agent":
		if len(rest) != 2 {
			return usagef("profile agent needs a profile name and an agent")
		}
		if !config.ValidProfileName(rest[0]) {
			return invalidProfileName(rest[0])
		}
		agent, ok := config.LookupAgent(rest[1])
		if !ok {
			return usagef("unknown agent '%s': use %s", rest[1], strings.Join(config.AgentNames(), ", "))
		}
		conf, err := a.loadBaseConfig()
		if err != nil {
			return err
		}
		return a.profileAgent(conf, rest[0], agent)
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
				return fmt.Errorf("profile '%s' does not exist; create it with '%s'", name,
					a.hint("profile add "+name+" <agent>"))
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
		a.printf("%s %-20s %-10s %d playbook(s)\n", mark, profile, prof.Agent, len(prof.Playbooks))
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

func (a *App) profileAdd(name string, agent config.Agent) error {
	if config.ProfileExists(a.Base, name) {
		return fmt.Errorf("profile '%s' already exists (%s)", name, a.profileFile(name))
	}
	prof := &config.Profile{Agent: agent.Name}
	if err := config.SaveProfile(a.Base, name, prof); err != nil {
		return err
	}
	a.printf("Created profile '%s' (%s), with agent %s (%s): when enabled, its playbooks' artifacts are linked from "+
		"%s.\n", name, a.profileFile(name), agent.Name, agent.Product, a.folderList(a.newProfileState(name, prof, false)))
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
			if err := a.checkPlaybookFolders(e, s.folders); err != nil {
				return fmt.Errorf("%w; profile '%s' cannot be enabled, so nothing was changed", err, s.name)
			}
		}
	}
	plan, err := playbook.Switch(groups(removed), groups(added), groups(stay), "profile disabled", "profile disabled",
		true)
	if err != nil {
		return err
	}
	a.markAncestors(&plan)
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
	a.cleanup(plan)
	if len(updated.Enabled) == 0 {
		a.printf("No profile is enabled now.\n")
	} else {
		a.printf("Enabled profiles: %s.\n", strings.Join(updated.Enabled, ", "))
	}
	a.warnDoubleLoads(append(append([]*profileState{}, stay...), added...))
	return nil
}

// profileAgent changes a profile's agent. When the profile is enabled, its playbooks are unlinked for the old agent
// and linked for the new one, all-or-nothing.
func (a *App) profileAgent(conf *config.BaseConfig, name string, agent config.Agent) error {
	if !config.ProfileExists(a.Base, name) {
		return fmt.Errorf("profile '%s' does not exist ('%s' lists the profiles)", name, a.hint("profile list"))
	}
	enabled := conf.IsEnabled(a.Base, name)
	old, err := a.loadProfileState(name, enabled)
	if err != nil {
		return err
	}
	if old.agent.Name == agent.Name {
		a.printf("Profile '%s' already has agent %s.\n", name, agent.Name)
		return nil
	}
	updated := *old.prof
	updated.Agent = agent.Name
	next := a.newProfileState(name, &updated, enabled)
	if !enabled {
		if err := config.SaveProfile(a.Base, name, &updated); err != nil {
			return err
		}
		a.printf("Profile '%s' now has agent %s (%s); nothing was linked or unlinked, since it is not enabled.\n",
			name, agent.Name, agent.Product)
		return nil
	}
	current, err := a.enabledProfiles(conf)
	if err != nil {
		return err
	}
	_, stay := a.split(current, []string{name})
	for _, e := range next.entries {
		if err := a.checkPlaybookFolders(e, next.folders); err != nil {
			return fmt.Errorf("%w; the agent was not changed", err)
		}
	}
	plan, err := playbook.Switch([]playbook.Group{old.group()}, []playbook.Group{next.group()}, groups(stay),
		"agent changed", "agent changed", false)
	if err != nil {
		return err
	}
	a.markAncestors(&plan)
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("the agent was not changed: %s.", a.conflictAdvice(plan))
		return errSilent
	}
	a.printf("Changing the agent of profile '%s' from %s to %s\n", name, old.agent.Name, agent.Name)
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the agent was not changed", err)
	}
	if err := config.SaveProfile(a.Base, name, &updated); err != nil {
		return a.undo(plan, err, "the agent was not changed")
	}
	a.cleanup(plan)
	a.printf("Profile '%s' now has agent %s (%s).\n", name, agent.Name, agent.Product)
	a.warnDoubleLoads(append(stay, next))
	return nil
}
