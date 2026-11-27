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
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

var profileUsage = `Usage: aiplam profile list
       aiplam profile show <profile>
       aiplam profile add <profile> (--agent AGENT | --folder KIND:FOLDER...)
       aiplam profile remove <profile>
       aiplam profile enable <profile>[,<profile>...]
       aiplam profile disable <profile>[,<profile>...]
       aiplam profile switch <profile>[,<profile>...]

Manages profiles: working scopes, such as a client, a team or a kind of work, each kept in its own file
(<managedfolder>/.aiplam/<profile>.yaml) with its target folders (where the artifacts of each kind are linked), its
agent (the template its target folders were taken from, or '` + config.CustomAgent + `') and its registered playbooks:
  - only the playbooks of the enabled profiles are linked, so switching scopes is enabling and disabling profiles;
  - several profiles can be enabled at once, combining their scopes; every link belongs to a single playbook of a
    single profile, so profiles enabled together cannot link anything at the same place, nor register the same
    playbook (same name and playbook folder);
  - 'add', 'update' and 'remove' act on the enabled profile (when only one is), or on those given with --profile.

Subcommands:
  list      list the profiles and their agents; '*' marks the enabled ones
  show      show a profile: its agent, its target folders, and its playbooks, with their versions and, for each kind,
            their subfolder, their format and how many artifacts they provide
  add       create a profile, not enabled, with its target folders; names: letters, digits, '_' and '-'
            ('` + config.ConfigName + `' is reserved)
  remove    delete a profile, with its playbook registrations; if it is enabled, its links are removed first
  enable    enable profiles besides those already enabled: link their playbooks
  disable   disable profiles: unlink their playbooks
  switch    enable exactly the given profiles: disable the others and enable these, all-or-nothing

Enabling, disabling and switching are all-or-nothing: nothing changes if any link cannot be made (except links to
files on Windows without developer mode or administrator rights, which are skipped with a warning). A profile's target
folders and agent are not changed by any command: edit its file (and set its agent to '` + config.CustomAgent + `'
when the targets no longer match its template, as 'aiplam status' warns), or remove it and create it again.

Folders are created when an artifact is first linked there, and removed when unlinking leaves them empty (such as
.claude/agents, and then .claude itself, up to the managed folder), but never when they hold anything else, such as
artifacts added by hand.

` + targetOptions + `

Agent templates, and their target folders (in a project, in the user managed folder):
` + templatesHelp() + `
Examples:
  aiplam profile add acme --agent claude                       # a profile for the work for client Acme
  aiplam add acme ~/Workspace/acme-ai-playbook --profile acme
  aiplam profile enable acme                                   # link Acme's playbooks too
  aiplam profile switch acme                                   # work only on Acme: disable the other profiles
  aiplam profile switch default,acme                           # enable exactly default and acme
  aiplam profile disable acme                                  # unlink Acme's playbooks
`

func (a *App) cmdProfile(args []string) error {
	p, err := parseArgs(args, []string{"agent", "folder"}, nil)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usagef("profile needs a subcommand: list, show, add, remove, enable, disable or switch")
	}
	sub, rest := p.positional[0], p.positional[1:]
	if sub != "add" && (len(p.values["agent"]) > 0 || len(p.values["folder"]) > 0) {
		return usagef("only 'profile add' takes --agent and --folder")
	}
	switch sub {
	case "list":
		if len(rest) > 0 {
			return usagef("unexpected argument: %s", rest[0])
		}
		return a.profileList()
	case "add":
		if len(rest) != 1 {
			return usagef("profile add needs one profile name, and its target folders with --agent or --folder")
		}
		if !config.ValidProfileName(rest[0]) {
			return invalidProfileName(rest[0])
		}
		agent, targets, err := a.targetsFromOptions(p, a.isUser(), "profile add")
		if err != nil {
			return err
		}
		if _, err := a.loadConfig(); err != nil {
			return err
		}
		return a.profileAdd(rest[0], agent, targets)
	case "show":
		if len(rest) != 1 {
			return usagef("profile show needs one profile name")
		}
		if !config.ValidProfileName(rest[0]) {
			return invalidProfileName(rest[0])
		}
		conf, err := a.loadConfig()
		if err != nil {
			return err
		}
		return a.profileShow(conf, rest[0])
	case "remove":
		if len(rest) != 1 {
			return usagef("profile remove needs one profile name")
		}
		if !config.ValidProfileName(rest[0]) {
			return invalidProfileName(rest[0])
		}
		conf, err := a.loadConfig()
		if err != nil {
			return err
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
		conf, err := a.loadConfig()
		if err != nil {
			return err
		}
		for i, name := range names {
			if sub != "disable" && !config.ProfileExists(a.Managed, name) {
				return fmt.Errorf("profile '%s' does not exist; create it with '%s'", name,
					a.hint("profile add "+name+" --agent <agent>"))
			}
			// The configuration names profiles by their files ("work", even when given as "Work" on a file system
			// that ignores case), so that it works on any file system.
			if sub != "disable" {
				names[i] = config.ProfileName(a.Managed, name)
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

func (a *App) profileList() error {
	conf, err := a.loadConfig()
	if err != nil {
		return err
	}
	profiles, err := config.Profiles(a.Managed)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		mark := " "
		if conf.IsEnabled(a.Managed, profile) {
			mark = "*"
		}
		prof, err := config.LoadProfile(a.Managed, profile)
		if err != nil {
			a.printf("%s %-20s cannot be read (%s); see '%s'\n", mark, profile, err, a.hint("status"))
			continue
		}
		line := fmt.Sprintf("%s %-20s %-18s %d playbook(s)", mark, profile, prof.Agent, len(prof.Playbooks))
		if errs := prof.Validate(a.Managed, profile, a.isUser()); len(errs) > 0 {
			line += fmt.Sprintf("; %d problem(s) to fix by hand ('%s' shows them)", len(errs), a.hint("status"))
		}
		a.printf("%s\n", line)
	}
	for _, name := range conf.Enabled {
		if !config.ProfileExists(a.Managed, name) {
			a.warnf("the enabled profile '%s' does not exist: disable it with '%s'.", name, a.hint("profile disable "+name))
		}
	}
	if len(conf.Enabled) == 0 {
		a.printf("No profile is enabled: enable one with '%s'.\n", a.hint("profile enable <profile>"))
	}
	return nil
}

// profileShow shows a profile's agent, target folders and playbooks.
func (a *App) profileShow(conf *config.Config, name string) error {
	if !config.ProfileExists(a.Managed, name) {
		return fmt.Errorf("profile '%s' does not exist ('%s' lists the profiles)", name, a.hint("profile list"))
	}
	s, err := a.loadProfileState(name, conf.IsEnabled(a.Managed, name))
	if err != nil {
		return err
	}
	status := "not enabled"
	if s.enabled {
		status = "enabled"
	}
	a.printf("Profile %s (%s): %s, agent %s\nTarget folders:\n%s", s.name, a.profileFile(s.name), status, s.prof.Agent,
		targetList(s.prof.Targets))
	a.templateCheck(s)
	if len(s.entries) == 0 {
		a.printf("No playbooks are registered.\n")
		return nil
	}
	a.printf("Playbooks:\n")
	for _, e := range s.entries {
		version := "unknown"
		if err := a.checkPlaybookFolders(e, s.targets); err != nil {
			a.warnf("%s", err)
		}
		if fsutil.IsDir(e.Dir) {
			version = a.version(e.Dir).String()
		}
		a.printf("  %s: %s (%s)\n", e.ID, a.pretty(e.Dir), version)
		a.printSources(s, e)
	}
	return nil
}

func (a *App) profileAdd(name, agent string, targets config.Targets) error {
	if config.ProfileExists(a.Managed, name) {
		return fmt.Errorf("profile '%s' already exists (%s)", name, a.profileFile(name))
	}
	// Names that differ only in case are one profile on disks that ignore case, and are refused together in the
	// enabled list: never two profiles of them, wherever the configuration is used.
	existing, err := config.Profiles(a.Managed)
	if err != nil {
		return err
	}
	for _, other := range existing {
		if strings.EqualFold(other, name) {
			return fmt.Errorf("profile '%s' already exists (%s): profile names that differ only in case cannot be "+
				"told apart", other, a.profileFile(other))
		}
	}
	if err := config.SaveProfile(a.Managed, &config.Profile{Name: name, Agent: agent, Targets: targets}); err != nil {
		return err
	}
	a.printf("Created profile '%s' (%s), with agent %s, not enabled. Target folders:\n%s", name, a.profileFile(name),
		agent, targetList(targets))
	a.printf("Register playbooks in it with '%s', and enable it with '%s'.\n",
		a.hint("add <name> <playbookfolder> --profile "+name), a.hint("profile enable "+name))
	return nil
}

func (a *App) profileRemove(conf *config.Config, name string) error {
	if !config.ProfileExists(a.Managed, name) {
		return fmt.Errorf("profile '%s' does not exist ('%s' lists the profiles)", name, a.hint("profile list"))
	}
	count := 0
	if prof, err := config.LoadProfile(a.Managed, name); err == nil {
		count = len(prof.Playbooks)
	}
	if conf.IsEnabled(a.Managed, name) {
		// Disable it first: its links go.
		if err := a.profileDisable(conf, []string{name}); err != nil {
			if errors.Is(err, errSilent) {
				a.errorf("the profile was not removed.")
				return errSilent
			}
			return fmt.Errorf("%w; the profile was not removed", err)
		}
	}
	if err := os.Remove(config.ProfileFile(a.Managed, name)); err != nil {
		return err
	}
	a.printf("Removed profile '%s' and its %d playbook registration(s).\n", name, count)
	return nil
}

// enabledProfiles loads the enabled profiles. Those whose file is missing are left out, with a warning: their links (if
// any) cannot be found. With lenient (when only disabling profiles), so are those that cannot be loaded or are not
// valid: disabling must stay possible, to get out of such a state.
func (a *App) enabledProfiles(conf *config.Config, lenient bool) ([]*profileState, error) {
	var profiles []*profileState
	for _, name := range conf.Enabled {
		if !config.ProfileExists(a.Managed, name) {
			a.warnf("the enabled profile '%s' does not exist: its links (if any) cannot be found; '%s' reports those "+
				"that are broken.", name, a.hint("status"))
			continue
		}
		s, err := a.loadProfileState(name, true)
		if err != nil && lenient {
			if !errors.Is(err, errSilent) {
				a.errorf("%s", err)
			}
			a.warnf("profile '%s' cannot be loaded: its links (if any) are left in place; remove them by hand if needed.",
				name)
			continue
		}
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
			found = found || config.SameProfile(a.Managed, s.name, n)
		}
		if found {
			in = append(in, s)
		} else {
			out = append(out, s)
		}
	}
	return in, out
}

func (a *App) profileEnable(conf *config.Config, names []string) error {
	current, err := a.enabledProfiles(conf, false)
	if err != nil {
		return err
	}
	var added []*profileState
	for _, name := range names {
		if conf.IsEnabled(a.Managed, name) {
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
	return a.changeProfiles(&updated, nil, added, current)
}

func (a *App) profileDisable(conf *config.Config, names []string) error {
	var notEnabled []string
	for _, name := range names {
		if !conf.IsEnabled(a.Managed, name) {
			notEnabled = append(notEnabled, name)
		}
	}
	if len(notEnabled) > 0 {
		return fmt.Errorf("not enabled: %s ('%s' shows the enabled profiles)", strings.Join(notEnabled, ", "),
			a.hint("profile list"))
	}
	current, err := a.enabledProfiles(conf, true)
	if err != nil {
		return err
	}
	removed, stay := a.split(current, names)
	updated := *conf
	updated.Enabled = nil
	for _, e := range conf.Enabled {
		keep := true
		for _, n := range names {
			keep = keep && !config.SameProfile(a.Managed, e, n) && !strings.EqualFold(e, n)
		}
		if keep {
			updated.Enabled = append(updated.Enabled, e)
		}
	}
	return a.changeProfiles(&updated, removed, nil, stay)
}

func (a *App) profileSwitch(conf *config.Config, names []string) error {
	current, err := a.enabledProfiles(conf, false)
	if err != nil {
		return err
	}
	stay, removed := a.split(current, names)
	var added []*profileState
	for _, name := range names {
		if conf.IsEnabled(a.Managed, name) {
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
	return a.changeProfiles(&updated, removed, added, stay)
}

func namesOf(profiles []*profileState) []string {
	var names []string
	for _, s := range profiles {
		names = append(names, s.name)
	}
	return names
}

// changeProfiles unlinks the playbooks of the profiles in removed, links those of the profiles in added, keeps those of
// the profiles in stay, and saves updated as the aiplam configuration. It is all-or-nothing.
func (a *App) changeProfiles(updated *config.Config, removed, added, stay []*profileState) error {
	scope := map[playbook.Key]bool{}
	for _, s := range added {
		if err := a.checkTargetFolders(s); err != nil {
			return fmt.Errorf("%w; profile '%s' cannot be enabled, so nothing was changed", err, s.name)
		}
		for _, e := range s.entries {
			if err := a.checkPlaybookFolders(e, s.targets); err != nil {
				return fmt.Errorf("%w; profile '%s' cannot be enabled, so nothing was changed", err, s.name)
			}
			scope[e.Key()] = true
		}
	}
	for _, s := range removed {
		for _, e := range s.entries {
			scope[e.Key()] = true
		}
	}
	from := append(append([]*profileState{}, stay...), removed...)
	to := append(append([]*profileState{}, stay...), added...)
	plan, err := a.transition(from, to, scope)
	if err != nil {
		return err
	}
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("nothing was changed: %s.", a.conflictAdvice(plan))
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
	if err := config.SaveConfig(a.Managed, updated); err != nil {
		return a.undo(plan, err, "the enabled profiles were not changed")
	}
	a.cleanup(plan)
	a.warnSkipped(plan)
	a.warnUnignoredCreated(plan, to)
	if len(updated.Enabled) == 0 {
		a.printf("No profile is enabled now.\n")
	} else {
		a.printf("Enabled profiles: %s.\n", strings.Join(updated.Enabled, ", "))
	}
	return nil
}
