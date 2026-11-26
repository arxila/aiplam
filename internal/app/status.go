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

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

const statusUsage = `Usage: aiplam status [<folder> | --user]

Shows the managed folder that applies to <folder> (default: the current folder), or the user managed folder with
--user: whether it is the user managed folder (the home folder), whether it is in a git repository (and where its
root is), and its enabled profiles, with their agents. It warns when a profile's target folders no longer match its
agent template.

Then it reports everything that needs attention, and exits with 1 if there is anything:
  - problems in the configuration files (aiplam-config.yaml and the enabled profiles' files);
  - missing playbook folders;
  - for each playbook of the enabled profiles, the links that 'aiplam update' would create or remove, or the reasons
    it would refuse to;
  - in the target folders of every profile, the links that belong to no playbook of the enabled profiles and are
    broken, or lead into a registered playbook folder: aiplam leaves those when the configuration is changed by hand
    (such as disabling a profile or changing a mapping), and they get in the way of the playbook's own links, so they
    must be removed by hand, even if you made them yourself;
  - target folders (or their parents) and playbook subfolders that are files, not folders.
It also warns, without counting them, about the profiles that are not enabled and not valid (they cannot be enabled
until they are fixed), about the artifacts that cannot be linked because they are files and, on Windows, links to
files need developer mode or administrator rights, and, when the managed folder is in a git repository, about the
links that git does not ignore: links belong to each machine and must not be committed, so it gives the lines to add
to .gitignore (aiplam never changes it), and lists the links already committed (or says that it cannot check them,
when git is not in the PATH or fails).

Examples:
  aiplam status
  aiplam status ~/Workspace/thymeleaf
  aiplam status --user
`

// startFolder returns the folder from which a command that takes an optional <folder> (or --user) finds its managed
// folder.
func (a *App) startFolder(positional []string) (string, error) {
	switch {
	case len(positional) > 1:
		return "", usagef("unexpected argument: %s", positional[1])
	case len(positional) == 1 && a.user:
		return "", usagef("give either a folder or --user, not both")
	case len(positional) == 1:
		dir := a.absPathAsGiven(positional[0])
		if !fsutil.IsDir(dir) {
			return "", fmt.Errorf("folder '%s' does not exist", positional[0])
		}
		return dir, nil
	}
	return a.Cwd, nil
}

// resolveFrom finds the managed folder for status and list.
func (a *App) resolveFrom(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	start, err := a.startFolder(p.positional)
	if err != nil {
		return err
	}
	if err := a.resolveManaged(start); err != nil {
		return err
	}
	// Suggested commands must act on this managed folder, not on the one the current folder leads to.
	if !a.user && len(p.positional) == 1 {
		here, ok := config.FindManaged(a.absPathAsGiven(a.Cwd), a.Home)
		a.elsewhere = !ok || !fsutil.SamePath(here, a.Managed)
	}
	return a.checkSandbox()
}

// printHeader prints the managed folder, whether it is the user managed folder, and its git repository.
func (a *App) printHeader() {
	a.managedShown = true
	a.printf("Managed folder: %s\n", a.pretty(a.Managed))
	if a.isUser() {
		a.printf("  user managed folder (the home folder): yes\n")
	} else {
		a.printf("  user managed folder (the home folder): no\n")
	}
	switch repo := config.GitRepository(a.Managed); {
	case repo == "":
		a.printf("  git repository: none\n")
	case fsutil.SamePath(repo, a.Managed):
		a.printf("  git repository: yes, the managed folder is its root\n")
	default:
		a.printf("  git repository: yes, with its root at %s\n", a.pretty(repo))
	}
}

// printProfiles prints the enabled profiles, with their agents, and warns when their targets no longer match their
// agent templates.
func (a *App) printProfiles(enabled []*profileState) {
	if len(enabled) == 0 {
		a.printf("  enabled profiles: none\n")
		return
	}
	a.printf("  enabled profiles:\n")
	for _, s := range enabled {
		a.printf("    %s: agent %s (%s)\n", s.name, s.prof.Agent, a.profileFile(s.name))
	}
	for _, s := range enabled {
		a.templateCheck(s)
	}
}

func (a *App) cmdStatus(args []string) error {
	if err := a.resolveFrom(args); err != nil {
		return err
	}
	a.printHeader()
	attention := 0
	problem := func(format string, args ...any) {
		attention++
		a.warnf(format, args...)
	}

	file := a.pretty(config.ConfigFile(a.Managed))
	conf, err := config.LoadConfig(a.Managed)
	if errors.Is(err, config.ErrNotInitialized) {
		return a.notInitialized()
	}
	if err == nil {
		for _, e := range conf.Validate() {
			problem("%s: %s", file, e)
		}
	} else {
		problem("%s: %s", file, err)
	}
	if attention > 0 {
		a.errorf("fix %s by hand; nothing else was checked.", file)
		return errSilent
	}

	st := &state{conf: conf}
	for _, name := range conf.Enabled {
		pfile := a.profileFile(name)
		if !config.ProfileExists(a.Managed, name) {
			problem("the enabled profile '%s' does not exist (%s is missing): disable it with '%s', or create it again "+
				"with '%s'", name, pfile, a.hint("profile disable "+name), a.hint("profile add "+name+" --agent <agent>"))
			continue
		}
		name = config.ProfileName(a.Managed, name)
		prof, err := config.LoadProfile(a.Managed, name)
		if err != nil {
			problem("%s: %s", pfile, err)
			continue
		}
		if errs := prof.Validate(a.Managed, name, a.isUser()); len(errs) > 0 {
			for _, e := range errs {
				problem("%s: %s", pfile, e)
			}
			continue
		}
		s, err := a.newProfileState(prof, true)
		if err != nil {
			problem("%s", err)
			continue
		}
		st.enabled = append(st.enabled, s)
	}
	a.printProfiles(st.enabled)
	others := a.otherProfiles(conf)
	if attention > 0 {
		a.errorf("%d problem(s) in the profile files: fix them by hand; nothing else was checked.", attention)
		return errSilent
	}

	// Every playbook of the enabled profiles, planned together: what 'aiplam update' would change for each.
	scope := map[playbook.Key]bool{}
	for _, s := range st.enabled {
		if err := a.checkTargetFolders(s); err != nil {
			problem("%s", err)
			continue
		}
		for _, e := range s.entries {
			if err := a.checkPlaybookFolders(e, s.targets); err != nil {
				problem("%s", err)
				continue
			}
			scope[e.Key()] = true
		}
	}
	plan, err := a.transition(st.enabled, st.enabled, scope)
	if err != nil {
		problem("%s", err)
	}
	update := func(act playbook.Action) string {
		return a.withProfile(st, act.Profile, "update "+act.ID)
	}
	for _, c := range plan.Conflicts() {
		problem("'%s' would be refused: %s", update(c), a.conflictText(c))
	}
	for _, act := range plan.Changes() {
		if act.Kind == playbook.Create {
			problem("'%s' would link %s -> %s", update(act), a.pretty(act.Link), a.pretty(act.Source))
		} else {
			problem("'%s' would remove %s (%s)", update(act), a.pretty(act.Link), act.Reason)
		}
	}

	// Links that belong to no playbook of the enabled profiles (those that do are covered above), in the target folders
	// of every profile: broken ones, and those leading into a registered playbook folder, left behind by hand edits of
	// the configuration (a profile no longer enabled, a mapping changed...). Links elsewhere are not aiplam's.
	var registered []string
	seen := map[string]bool{}
	var dirs []string
	for _, s := range append(append([]*profileState{}, st.enabled...), others...) {
		for _, e := range s.entries {
			registered = append(registered, e.Dir)
		}
		for _, t := range s.targets {
			if !seen[t.Dir] {
				seen[t.Dir] = true
				dirs = append(dirs, t.Dir)
			}
		}
	}
	for _, dir := range dirs {
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			link := filepath.Join(dir, item.Name())
			state, current, err := fsutil.Inspect(link)
			if err != nil || state != fsutil.Link || ownedBy(st.enabled, link, item.Name(), current) {
				continue
			}
			if _, err := os.Stat(current); err != nil {
				problem("broken link %s -> %s (it belongs to no playbook of the enabled profiles; remove it by hand)",
					a.pretty(link), a.pretty(current))
				continue
			}
			for _, r := range registered {
				if fsutil.Within(fsutil.Canonical(current), r) {
					problem("%s -> %s leads into a registered playbook folder, but it belongs to no playbook of the "+
						"enabled profiles: aiplam leaves such links when the configuration is changed by hand, and they "+
						"get in the way of the playbook's own links; remove it by hand (if you made it yourself, too)",
						a.pretty(link), a.pretty(current))
					break
				}
			}
		}
	}

	// Links to files that cannot be created are reported, but not counted: only developer mode or administrator
	// rights (on Windows) can fix them.
	if len(plan.Skipped()) > 0 {
		a.warnf("%s", a.skippedText(plan, "cannot be linked"))
	}
	// Links that git does not ignore: reported, but not counted (committing them is a choice, if a bad one).
	unignored := a.warnUnignored(ownedLinks(st.enabled, nil), true)

	if attention > 0 {
		a.printf("\n%d item(s) need attention; see the warnings above.\n", attention)
		return errSilent
	}
	if len(plan.Skipped()) > 0 || unignored {
		a.printf("\nEverything else is up to date.\n")
		return nil
	}
	a.printf("\nEverything is up to date.\n")
	return nil
}

// ownedBy reports whether a link belongs to a playbook of the enabled profiles, for the kind of the target folder it is
// in.
func ownedBy(enabled []*profileState, link, name, current string) bool {
	for _, s := range enabled {
		for _, t := range s.targets {
			if fsutil.SamePath(t.Dir, filepath.Dir(link)) && playbook.Owner(s.entries, t.Kind, name, current) != nil {
				return true
			}
		}
	}
	return false
}

// otherProfiles loads the profiles that are not enabled, to find their target folders and playbooks. It warns about
// those that cannot be loaded or are not valid (they cannot be enabled until they are fixed), without counting them as
// problems: nothing of theirs is linked.
func (a *App) otherProfiles(conf *config.Config) []*profileState {
	names, _ := config.Profiles(a.Managed)
	var others []*profileState
	for _, name := range names {
		if conf.IsEnabled(a.Managed, name) {
			continue
		}
		prof, err := config.LoadProfile(a.Managed, name)
		if err == nil {
			if errs := prof.Validate(a.Managed, name, a.isUser()); len(errs) > 0 {
				err = errs[0]
				if len(errs) > 1 {
					err = fmt.Errorf("%w (and %d more problem(s))", errs[0], len(errs)-1)
				}
			}
		}
		var s *profileState
		if err == nil {
			s, err = a.newProfileState(prof, false)
		}
		if err != nil {
			a.warnf("%s: %s; the profile cannot be enabled until it is fixed.", a.profileFile(name), err)
			continue
		}
		others = append(others, s)
	}
	return others
}
