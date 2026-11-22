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

const checkUsage = `Usage: aiplam check [--profile PROFILE]

Changes nothing. Reports everything that needs attention, and then exits with 1:
  - problems in the configuration files (aiplam-config.yaml and the profiles' files, in the base folder's .aiplam);
  - missing agents configuration folders and playbook folders;
  - for each playbook of the enabled profiles, the links that 'aiplam update' would create or remove, or the
    conflicts that would cancel it;
  - broken links in the agents configuration folders;
  - links whose artifact is also linked from an agents configuration folder of the same type of a higher-level
    base folder.

Options:
  -p, --profile PROFILE   check profile PROFILE only (default: the enabled profiles); for a profile that is not
                          enabled, only whether it could be enabled

Examples:
  aiplam check
  aiplam check --profile acme
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

	confFile := a.pretty(config.BaseConfigFile(a.Base))
	conf, err := config.LoadBaseConfig(a.Base)
	if errors.Is(err, config.ErrNotInitialized) {
		return a.notInitialized()
	}
	a.showBase()
	a.printf("Configuration: %s\n", confFile)
	if err == nil {
		for _, e := range conf.Validate() {
			problem("%s: %s", confFile, e)
		}
	} else {
		problem("%s: %s", confFile, err)
	}
	if attention > 0 {
		a.errorf("fix %s by hand; nothing else was checked.", confFile)
		return errSilent
	}
	a.printf("  format and content are valid\n")

	// The enabled profiles, and the one given with --profile.
	if a.Profile != "" && !config.ProfileExists(a.Base, a.Profile) {
		return fmt.Errorf("profile '%s' does not exist ('%s' lists the profiles)", a.Profile, a.hint("profile list"))
	}
	st := &state{conf: conf, agentsConfigDirs: conf.AgentsConfigDirs(a.Base)}
	loadProfile := func(name string, enabled bool) *profileState {
		file := a.profileFile(name)
		if !config.ProfileExists(a.Base, name) {
			problem("the enabled profile '%s' does not exist (%s is missing): disable it with '%s', or create it again "+
				"with '%s'", name, file, a.hint("profile disable "+name), a.hint("profile add "+name))
			return nil
		}
		prof, err := config.LoadProfile(a.Base, name)
		if err == nil {
			for _, e := range prof.Validate(a.Base) {
				problem("%s: %s", file, e)
				err = errSilent
			}
		} else {
			problem("%s: %s", file, err)
		}
		if err != nil {
			return nil
		}
		return &profileState{name: name, prof: prof, enabled: enabled,
			entries: playbook.Entries(name, prof.Playbooks, a.Base)}
	}
	for _, name := range conf.Enabled {
		if s := loadProfile(name, true); s != nil {
			st.enabled = append(st.enabled, s)
			if a.Profile == "" || config.SameProfile(a.Base, name, a.Profile) {
				st.profiles = append(st.profiles, s)
			}
		}
	}
	if a.Profile != "" && len(st.profiles) == 0 && !conf.IsEnabled(a.Base, a.Profile) {
		if s := loadProfile(a.Profile, false); s != nil {
			st.profiles = append(st.profiles, s)
		}
	}
	if attention > 0 {
		a.errorf("%d problem(s) in the profile files: fix them by hand; nothing else was checked.", attention)
		return errSilent
	}

	agentsConfigDirs := st.agentsConfigDirs
	a.printf("Agents configuration folders:\n")
	for _, t := range agentsConfigDirs {
		if !fsutil.IsDir(t) {
			problem("agents configuration folder '%s' does not exist; it is created when an artifact is next linked from it",
				a.pretty(t))
			continue
		}
		a.printf("  %s\n", a.pretty(t))
	}

	if len(st.profiles) == 0 {
		a.printf("No profile is enabled.\n")
	}
	reported := map[string]bool{} // links already reported by a playbook's plan
	for _, profile := range st.profiles {
		a.useProfile(st, profile)
		status := "enabled"
		if !profile.enabled {
			status = "not enabled"
		}
		a.printf("Profile %s (%s): %s\n", profile.name, status, a.profileFile(profile.name))
		if len(profile.entries) == 0 {
			a.printf("  no playbooks are registered\n")
		}
		for _, e := range profile.entries {
			a.checkPlaybook(st, profile, e, reported, problem)
		}
	}
	a.hintProfile = ""

	// Broken links that no playbook plan covers (for example, links to an unregistered or moved playbook), and links
	// whose artifact is also in the agents configuration folder of the same type of a higher-level base folder.
	for _, t := range agentsConfigDirs {
		dir := playbook.SkillsFolder(t)
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			link := filepath.Join(dir, item.Name())
			state, current, err := fsutil.Inspect(link)
			if err != nil || state != fsutil.Link {
				continue
			}
			if !reported[link] && !fsutil.IsDir(current) {
				problem("broken link %s -> %s (not managed by any registered playbook; remove it by hand)",
					a.pretty(link), a.pretty(current))
			}
			for _, higher := range a.ancestorAgentsConfigDirs()[t] {
				other := filepath.Join(playbook.SkillsFolder(higher), item.Name())
				if _, err := os.Lstat(other); err == nil {
					problem("%s is also in '%s', an agents configuration folder of the same type of a higher-level base "+
						"folder: agents reading both would see it twice; remove one of them", a.pretty(link), a.pretty(other))
					break
				}
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

// checkPlaybook checks a playbook's playbook folder and skills folder and, for an enabled profile, whether 'aiplam
// update' would change anything.
func (a *App) checkPlaybook(st *state, profile *profileState, e playbook.Entry, reported map[string]bool,
	problem func(string, ...any)) {
	a.printf("Playbook %s (%s) at %s\n", e.ID, a.version(e.Dir), a.pretty(e.Dir))
	if err := a.checkPlaybookFolders(e); err != nil {
		problem("%s", err)
		return
	}
	if !profile.enabled {
		// Nothing is linked; the profile must only stay linkable.
		overlaps, err := playbook.Overlaps(profile.entries, e)
		if err != nil {
			problem("playbook %s: %s", e.ID, err)
			return
		}
		for name, others := range overlaps {
			problem("playbook %s: skill %s: also provided by playbook %s; '%s' would be cancelled", e.ID, name,
				strings.Join(others, ", "), a.hint("profile enable "+profile.name))
		}
		if len(overlaps) == 0 {
			a.printf("  playbook folder and skills folder are valid (not linked: the profile is not enabled)\n")
		}
		return
	}
	plan, err := playbook.Build(profile.entries, e, st.agentsConfigDirs, st.others(profile.name))
	if err != nil {
		problem("playbook %s: %s", e.ID, err)
		return
	}
	playbook.MarkAncestors(&plan, a.ancestorAgentsConfigDirs())
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
		problem("playbook %s: '%s' would be cancelled until the conflicts are resolved", e.ID, a.cmd("update "+e.ID))
	}
	if len(changes) == 0 && len(plan.Conflicts()) == 0 {
		a.printf("  up to date\n")
		return
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
