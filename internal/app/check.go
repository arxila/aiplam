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
  - missing playbook folders;
  - for each playbook of the enabled profiles, the links that 'aiplam update' would create or remove, or the
    conflicts that would cancel it;
  - broken links in the folders where the enabled profiles' agents read artifacts;
  - links whose artifact is also where the same agent reads it in a higher-level base folder.
It also warns, without counting them, about artifacts that an agent may load twice: those linked for an agent that
also reads the standard agent's folders (.agents/skills), when another enabled profile links them from there too.

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
	st := &state{conf: conf}
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
		return a.newProfileState(name, prof, enabled)
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
		a.printf("Profile %s (%s, agent %s): %s\n", profile.name, status, profile.agent.Name,
			a.profileFile(profile.name))
		if len(profile.entries) == 0 {
			a.printf("  no playbooks are registered\n")
		}
		for _, e := range profile.entries {
			a.checkPlaybook(st, profile, e, reported, problem)
		}
	}
	a.hintProfile = ""

	// Broken links that no playbook plan covers (for example, links to an unregistered or moved playbook), and links
	// whose artifact is also where the same agent reads it in a higher-level base folder.
	home := fsutil.Canonical(a.Home)
	seen := map[string]bool{}
	for _, profile := range st.profiles {
		if !profile.enabled {
			continue
		}
		for _, f := range profile.folders {
			if seen[f.Dir] {
				continue
			}
			seen[f.Dir] = true
			items, err := os.ReadDir(f.Dir)
			if err != nil {
				continue
			}
			for _, item := range items {
				link := filepath.Join(f.Dir, item.Name())
				state, current, err := fsutil.Inspect(link)
				if err != nil || state != fsutil.Link {
					continue
				}
				if _, err := os.Stat(current); !reported[link] && err != nil {
					problem("broken link %s -> %s (not managed by any registered playbook; remove it by hand)",
						a.pretty(link), a.pretty(current))
				}
				for _, higher := range config.Ancestors(a.Base, a.Home) {
					place, ok := profile.agent.Place(f.Kind, fsutil.SamePath(higher, home))
					if !ok {
						continue
					}
					other := filepath.Join(higher, filepath.FromSlash(place.Dir), filepath.FromSlash(place.Sub),
						item.Name())
					if _, err := os.Lstat(other); err == nil {
						problem("%s is also in '%s', where agent %s reads it in a higher-level base folder: agents "+
							"reading both would see it twice; remove one of them", a.pretty(link), a.pretty(other),
							profile.agent.Name)
						break
					}
				}
			}
		}
	}
	a.warnDoubleLoads(st.enabled)

	if attention > 0 {
		a.printf("\n%d item(s) need attention; see the warnings above.\n", attention)
		return errSilent
	}
	a.printf("\nEverything is up to date.\n")
	return nil
}

// checkPlaybook checks a playbook's folders and, for an enabled profile, whether 'aiplam
// update' would change anything.
func (a *App) checkPlaybook(st *state, profile *profileState, e playbook.Entry, reported map[string]bool,
	problem func(string, ...any)) {
	a.printf("Playbook %s (%s) at %s\n", e.ID, a.version(e.Dir), a.pretty(e.Dir))
	if err := a.checkPlaybookFolders(e, profile.folders); err != nil {
		problem("%s", err)
		return
	}
	if !profile.enabled {
		// Nothing is linked; the profile must only stay linkable.
		overlaps, err := playbook.Overlaps(profile.group(), e)
		if err != nil {
			problem("playbook %s: %s", e.ID, err)
			return
		}
		for _, c := range overlaps {
			problem("playbook %s: %s %s: also provided by playbook %s; '%s' would be cancelled", e.ID,
				c.Artifact.Singular(), c.Name, strings.Join(c.Others, ", "), a.hint("profile enable "+profile.name))
		}
		if len(overlaps) == 0 {
			a.printf("  playbook folders are valid (not linked: the profile is not enabled)\n")
		}
		return
	}
	plan, err := playbook.Build(profile.group(), e, st.others(profile.name))
	if err != nil {
		problem("playbook %s: %s", e.ID, err)
		return
	}
	a.markAncestors(&plan)
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
