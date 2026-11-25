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
	"path/filepath"
	"sort"
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

// targetOptions documents --agent and --folder for init and profile add.
const targetOptions = `Target folders (exactly one of):
  -a, --agent AGENT              take them from an agent template (` + "'aiplam profile --help'" + ` lists them): the
                                 template only fills them in; the profile then works as if they had been given by hand
  -f, --folder KIND:FOLDER,...   give them by hand: the folder, relative to the managed folder, where the artifacts of
                                 each kind are linked. Kinds are any names (skills, subagents, my-prompts...); they
                                 match the playbooks' subfolders of the same name. Repeat the option or separate the
                                 items with commas; quote folders holding commas or colons ("my, folder"). Folders
                                 cannot contain '..'; in the user managed folder, they can start with %APPDATA%
                                 (Windows)`

// targetsFromOptions returns the agent and the targets given with --agent or --folder, for the user managed folder when
// user is set.
func (a *App) targetsFromOptions(p parsedArgs, user bool, command string) (string, config.Targets, error) {
	agents, folders := p.values["agent"], p.values["folder"]
	switch {
	case len(agents) == 0 && len(folders) == 0:
		return "", nil, usagef("%s needs the target folders: an agent template with --agent (%s), or folders with "+
			"--folder <kind>:<folder>", command, config.TemplateList())
	case len(agents) > 0 && len(folders) > 0:
		return "", nil, usagef("give either --agent or --folder, not both")
	case len(agents) > 1:
		return "", nil, usagef("--agent can be given only once")
	case len(agents) == 1:
		targets, ok := config.TemplateTargets(agents[0], user, a.GOOS)
		if !ok {
			return "", nil, usagef("unknown agent template '%s': use %s", agents[0], config.TemplateList())
		}
		return agents[0], targets, nil
	}
	targets, err := parseTargets(folders, user, a.GOOS)
	return config.CustomAgent, targets, err
}

// --- init ---------------------------------------------------------------------------------------------------------

var initUsage = `Usage: aiplam init <folder> (--agent AGENT | --folder KIND:FOLDER...) [--profile PROFILE...]
       aiplam init --user (--agent AGENT | --folder KIND:FOLDER...) [--profile PROFILE...]

Makes <folder> (or, with --user, the home folder) a managed folder:
  - creates <folder>/.aiplam, holding its configuration (aiplam-config.yaml, and one <profile>.yaml per profile);
  - creates its first profiles (by default, one named 'default'), all with the same target folders, and enables them.

From then on, commands run anywhere inside <folder> use its configuration: each command uses the nearest managed
folder from the current folder up to the home folder (or, when there is none, the home folder, if it is managed).

` + targetOptions + `

Options:
  -u, --user                 make the home folder (e.g. /home/username) the managed folder, instead of <folder>
  -p, --profile PROFILE,...  the names of the first profiles (default: 'default'); repeat the option or separate the
                             names with commas

Examples:
  aiplam init --user --agent claude                 # ~/.aiplam, with a profile for Claude Code (~/.claude/...)
  aiplam init . --agent junie:standard              # Junie, with skills in .agents/skills
  aiplam init . --folder skills:.agents/skills,rules:.junie/rules --profile backend
  aiplam init ~/Workspace/thymeleaf --agent claude --profile work,personal
`

func (a *App) cmdInit(args []string) error {
	p, err := parseArgs(args, []string{"agent", "folder"}, nil)
	if err != nil {
		return err
	}
	switch {
	case a.user && len(p.positional) > 0:
		return usagef("give either a folder or --user, not both")
	case !a.user && len(p.positional) == 0:
		return usagef("init needs the folder to make a managed folder, or --user for the home folder")
	case len(p.positional) > 1:
		return usagef("unexpected argument: %s", p.positional[1])
	}
	a.Managed = fsutil.Canonical(a.Home)
	if !a.user {
		a.Managed = a.absPath(p.positional[0])
	}
	// The targets depend on whether the folder is the home folder, given with --user or not.
	agent, targets, err := a.targetsFromOptions(p, a.isUser(), "init")
	if err != nil {
		return err
	}
	if !a.user {
		if info, err := os.Stat(a.Managed); err == nil && !info.IsDir() {
			return fmt.Errorf("'%s' is not a folder: give the folder to make a managed folder", p.positional[0])
		}
		if !fsutil.IsDir(a.Managed) {
			return fmt.Errorf("folder '%s' does not exist", p.positional[0])
		}
	}
	if err := a.checkSandbox(); err != nil {
		return err
	}
	profiles := a.Profiles
	if len(profiles) == 0 {
		profiles = []string{config.DefaultProfile}
	}
	if err := os.MkdirAll(config.Dir(a.Managed), 0o755); err != nil {
		return err
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	file := config.ConfigFile(a.Managed)
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("'%s' is already a managed folder: %s exists. Manage its profiles with 'aiplam profile'",
			a.pretty(a.Managed), a.pretty(file))
	}
	for _, profile := range profiles {
		if config.ProfileExists(a.Managed, profile) {
			// Left by an earlier setup whose configuration file was lost: overwriting it would lose its playbooks.
			return fmt.Errorf("%s already exists, from an earlier setup of this managed folder whose %s is missing: to "+
				"keep its profiles, recreate %s by hand, listing the profiles to enable (for example 'enabled: [%s]'); "+
				"or choose other profile names with --profile", a.profileFile(profile), a.pretty(file),
				config.ConfigName+config.FileExt, profile)
		}
	}
	a.showManaged()
	// On a failure, the profiles already written are removed, so that init can simply be run again.
	var written []string
	undo := func(err error) error {
		for _, profile := range written {
			_ = os.Remove(config.ProfileFile(a.Managed, profile))
		}
		return err
	}
	for _, profile := range profiles {
		if err := config.SaveProfile(a.Managed, &config.Profile{Name: profile, Agent: agent, Targets: targets}); err != nil {
			return undo(err)
		}
		written = append(written, profile)
	}
	if err := config.SaveConfig(a.Managed, &config.Config{Enabled: profiles}); err != nil {
		return undo(err)
	}
	a.printf("Created %s\n", a.pretty(file))
	for _, profile := range profiles {
		a.printf("Created profile '%s' (%s), with agent %s, enabled\n", profile, a.profileFile(profile), agent)
	}
	a.printf("Target folders (where the artifacts of each kind are linked, relative to the managed folder):\n%s",
		targetList(targets))
	if !a.isUser() {
		// Only a managed folder above it (the home folder applies to folders outside it too, but does not hold them).
		if above, ok := config.FindManaged(filepath.Dir(a.Managed), a.Home); ok && !fsutil.SamePath(above, a.Managed) &&
			fsutil.Within(a.Managed, above) {
			a.printf("Note: '%s' is inside managed folder '%s': from now on, commands run inside '%s' use its own "+
				"configuration.\n", a.pretty(a.Managed), a.pretty(above), a.pretty(a.Managed))
		}
	}
	if repo := config.GitRepository(a.Managed); repo != "" {
		var lines []string
		for _, t := range targets {
			if !strings.HasPrefix(t.Folder, config.AppData+"/") { // not in the managed folder, so not in its repository
				lines = append(lines, "'/"+t.Folder+"/<name>-*'")
			}
		}
		a.printf("Note: '%s' is inside a git repository. The %s folder can be committed to share the configuration "+
			"(playbook folders are stored relative to '%s'), but the links aiplam makes in the target folders belong to "+
			"each machine (and cannot be committed on Windows): add them to '%s', with lines such as %s for each "+
			"playbook name (or matching the format given to 'aiplam add').\n", a.pretty(a.Managed), config.DirName,
			a.pretty(a.Managed), a.pretty(filepath.Join(a.Managed, ".gitignore")), strings.Join(lines, ", "))
	}
	next := "add <name> <playbookfolder>"
	if len(profiles) > 1 {
		next += " --profile <profile>"
	}
	a.printf("Next: register a playbook with '%s'.\n", a.hint(next))
	return nil
}

// --- add ----------------------------------------------------------------------------------------------------------

const addUsage = `Usage: aiplam add <name> <playbookfolder> [--folder KIND:SUBFOLDER[:FORMAT]...] [--profile PROFILE...]

Registers the playbook in <playbookfolder> under <name> in profiles and, in those that are enabled, links its
artifacts into their target folders, at whatever tag or commit is checked out:
  - the profiles are those given with --profile, enabled or not; it can be left out when exactly one profile is
    enabled;
  - for each kind of the profile's targets, the artifacts are the entries (folders and files alike) of the
    playbook's subfolder for the kind whose names match the kind's format: by default, the subfolder named like the
    kind and the format '<name>-*'. Hidden entries (named '.*') are never artifacts;
  - <playbookfolder> is stored relative to the managed folder;
  - several playbooks can share a name if the last segments of their playbook folders differ: their ids are then
    '<name>@<last folder segment>'.

Subfolders that do not exist yet are fine: what a subfolder holds is linked whenever the playbook is updated or its
profile enabled.

Every link belongs to a single playbook of a single profile, so nothing is registered or linked if:
  - something is already where a link must go, even a link to the same artifact made by another playbook or profile;
  - another playbook, of this profile or of another enabled profile, would link something at the same place;
  - the same playbook (same name and playbook folder) is registered in another enabled profile.

Options:
  -f, --folder KIND:SUBFOLDER[:FORMAT],...   where the artifacts of KIND are: SUBFOLDER of <playbookfolder> ('.' for
                                             <playbookfolder> itself, never '..'), and the FORMAT of their names, in
                                             which '*' matches any characters (default: '<name>-*'). KIND must be a
                                             kind of every profile given. Repeat the option or separate the items with
                                             commas; quote values holding commas or colons
  -p, --profile PROFILE,...                  the profiles to register it in (default: the enabled profile, when only
                                             one is)

Examples:
  aiplam add arxila .                                          # the current folder: skills/, subagents/... in it
  aiplam add arxila ~/Workspace/arxila-ai-playbook --folder skills:ai/skills --folder subagents:agents
  aiplam add team ../team-playbook --folder skills:.          # skills named team-* at the playbook's root
  aiplam add acme . -f 'subagents:agents:acme-*.agent.md'      # only the subagents in GitHub Copilot's format
  aiplam add acme ../acme-ai-playbook --profile acme,acme-ci   # in profiles 'acme' and 'acme-ci'
`

func (a *App) cmdAdd(args []string) error {
	p, err := parseArgs(args, []string{"folder"}, nil)
	if err != nil {
		return err
	}
	if len(p.positional) != 2 {
		return usagef("add needs a name and a playbook folder")
	}
	name := p.positional[0]
	if !config.ValidName(name) {
		return usagef("invalid name '%s': use letters, digits, '.', '_' and '-', starting with a letter or digit", name)
	}
	mappings, err := parseMappings(p.values["folder"])
	if err != nil {
		return err
	}
	if len(mappings) == 0 {
		mappings = nil
	}
	folder := a.absPath(p.positional[1])
	if !fsutil.IsDir(folder) {
		return fmt.Errorf("playbook folder '%s' does not exist", p.positional[1])
	}

	st, err := a.loadState(false)
	if err != nil {
		return err
	}
	selected, err := a.selectProfiles(st, "register the playbook in")
	if err != nil {
		return err
	}
	rel, err := config.RelFolder(a.Managed, folder)
	if err != nil {
		return err
	}
	var updated []*profileState
	var entries []playbook.Entry
	scope := map[playbook.Key]bool{}
	for _, s := range selected {
		for kind := range mappings {
			if _, ok := s.prof.Targets.Folder(kind); !ok {
				return usagef("--folder: '%s' is not a kind of profile '%s' (its kinds: %s)", kind, s.name,
					strings.Join(s.prof.Targets.Kinds(), ", "))
			}
		}
		for _, e := range s.entries {
			if e.Name != name {
				continue
			}
			if fsutil.SamePath(e.Dir, folder) {
				return fmt.Errorf("playbook '%s' is already registered for playbook folder '%s' in profile '%s' (id %s); "+
					"use '%s'", name, a.pretty(folder), s.name, e.ID, a.withProfile(st, s.name, "update "+e.ID))
			}
			if fsutil.SameName(filepath.Base(e.Dir), filepath.Base(folder)) {
				return fmt.Errorf("a playbook named '%s' is already registered in profile '%s' for playbook folder '%s', "+
					"whose last segment equals that of '%s': their ids would collide", name, s.name, a.pretty(e.Dir),
					a.pretty(folder))
			}
		}
		prof := *s.prof
		prof.Playbooks = append(append([]config.Playbook{}, s.prof.Playbooks...),
			config.Playbook{Name: name, Folder: rel, Mappings: mappings})
		u, err := a.newProfileState(&prof, s.enabled)
		if err != nil {
			return err
		}
		e := u.entries[len(u.entries)-1]
		if err := a.checkPlaybookFolders(e, u.targets); err != nil {
			return err
		}
		if u.enabled {
			if err := a.checkTargetFolders(u); err != nil {
				return err
			}
		}
		updated = append(updated, u)
		entries = append(entries, e)
		if u.enabled {
			scope[e.Key()] = true
		}
	}

	var plan playbook.Plan
	if len(scope) > 0 {
		plan, err = playbook.Transition(groups(st.enabled), groups(replaceProfiles(st.enabled, updated...)), scope)
		if err != nil {
			return err
		}
		if len(plan.Conflicts()) > 0 {
			a.printConflicts(plan)
			a.errorf("playbook '%s' was not added: %s.", name, a.conflictAdvice(plan))
			return errSilent
		}
	}

	a.printf("Adding playbook %s (%s) from %s to profile(s) %s\n", name, a.version(folder), a.pretty(folder),
		profileNames(selected))
	total := 0
	for i, u := range updated {
		a.printf("  profile '%s':\n", u.name)
		total += a.printSources(u, entries[i])
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook was not registered: fix the problem and add it again", err)
	}
	for i, u := range updated {
		if err := config.SaveProfile(a.Managed, u.prof); err != nil {
			// Put back the profiles already saved.
			for _, s := range selected[:i] {
				_ = config.SaveProfile(a.Managed, s.prof)
			}
			return a.undo(plan, err, "the playbook was not registered")
		}
	}
	if total == 0 {
		a.warnf("no artifacts were found in '%s' for the kinds of the profile(s): check the subfolders and formats "+
			"above (they are linked as they appear, with 'aiplam update').", a.pretty(folder))
	}
	for i, u := range updated {
		if !u.enabled {
			a.printf("Nothing was linked in profile '%s': %s.\n", u.name, a.notEnabledNote(u.name))
		}
		a.reportRenamedIDs(selected[i].entries, u.entries)
	}
	a.printf("Added %s.\n", name)
	return nil
}

// printSources prints, for each kind of a profile's targets, where a playbook's artifacts are and how many there are,
// and returns how many there are in all.
func (a *App) printSources(s *profileState, e playbook.Entry) int {
	total := 0
	for _, t := range s.targets {
		dir := e.SourceDir(t.Kind)
		what := "not found"
		if _, err := os.Stat(dir); err == nil && !fsutil.IsDir(dir) {
			what = "not a folder"
		} else if fsutil.IsDir(dir) {
			items, _ := playbook.Items(e, t.Kind)
			what = fmt.Sprintf("%d artifact(s)", len(items))
			total += len(items)
		}
		a.printf("    %s: %s, format '%s': %s\n", t.Kind, a.pretty(dir), e.Format(t.Kind), what)
	}
	return total
}

// reportRenamedIDs tells the user when registering or removing a playbook changed the ids of others.
func (a *App) reportRenamedIDs(before, after []playbook.Entry) {
	for _, b := range before {
		for _, f := range after {
			if b.Name == f.Name && fsutil.SamePath(b.Dir, f.Dir) && b.ID != f.ID {
				a.printf("Playbook %s of profile '%s' is now identified as %s.\n", b.ID, f.Profile, f.ID)
			}
		}
	}
}

// --- update -------------------------------------------------------------------------------------------------------

const updateUsage = `Usage: aiplam update [<name|id|playbookfolder>] [<playbookfolder>] [--profile PROFILE...]

Brings a playbook's links in line with its playbook folder, as currently checked out:
  - links its new artifacts;
  - removes the links of artifacts that no longer exist, and the folders this leaves empty.

The playbook is chosen, in each profile, by:
  - nothing: the playbook registered for the current folder;
  - an id, a name or a playbook folder, when it designates a single playbook;
  - a name and a playbook folder.

aiplam never fetches, pulls or switches a checkout: select the version in the playbook folder first. Only the
playbooks of the enabled profiles are linked, so only they have anything to update. Nothing is changed if a link
cannot be made ('aiplam add --help' explains why that can happen).

Options:
  -p, --profile PROFILE,...   the profiles to update the playbook in (default: the enabled profile, when only one is)

Examples:
  cd ~/Workspace/arxila-ai-playbook
  git switch --detach v1.3.0
  aiplam update                # the playbook registered for the current folder
  aiplam update arxila
`

func (a *App) cmdUpdate(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) > 2 {
		return usagef("too many arguments")
	}
	st, err := a.load(p.positional, "update the playbook in")
	if err != nil {
		return err
	}
	scope := map[playbook.Key]bool{}
	for _, sel := range st.selections {
		if !sel.profile.enabled {
			a.printf("Nothing to update for playbook %s: %s.\n", sel.entry.Label(), a.notEnabledNote(sel.profile.name))
			continue
		}
		if err := a.checkPlaybookFolders(sel.entry, sel.profile.targets); err != nil {
			return err
		}
		if err := a.checkTargetFolders(sel.profile); err != nil {
			return err
		}
		scope[sel.entry.Key()] = true
		a.printf("Updating playbook %s of profile '%s' (%s) from %s\n", sel.entry.ID, sel.profile.name,
			a.version(sel.entry.Dir), a.pretty(sel.entry.Dir))
	}
	if len(scope) == 0 {
		return nil
	}
	plan, err := playbook.Transition(groups(st.enabled), groups(st.enabled), scope)
	if err != nil {
		return err
	}
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("update cancelled, nothing was changed: %s.", a.conflictAdvice(plan))
		return errSilent
	}
	if len(plan.Changes()) == 0 {
		a.printf("  up to date\n")
		return nil
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return err
	}
	a.cleanup(plan)
	return nil
}

// selection is a playbook chosen in a profile.
type selection struct {
	profile *profileState
	entry   playbook.Entry
}

// selectionState is a state with the playbooks chosen by update or remove.
type selectionState struct {
	*state
	selections []selection
}

// load loads the state and chooses, in each profile acted on (see selectProfiles), the playbook args designate.
func (a *App) load(args []string, verb string) (*selectionState, error) {
	st, err := a.loadState(false)
	if err != nil {
		return nil, err
	}
	profiles, err := a.selectProfiles(st, verb)
	if err != nil {
		return nil, err
	}
	sst := &selectionState{state: st}
	for _, s := range profiles {
		e, err := a.selectEntry(s, args)
		if err != nil {
			return nil, err
		}
		sst.selections = append(sst.selections, selection{profile: s, entry: e})
	}
	return sst, nil
}

// --- remove -------------------------------------------------------------------------------------------------------

const removeUsage = `Usage: aiplam remove <name|id|playbookfolder> [<playbookfolder>] [--profile PROFILE...]

Unregisters a playbook and, in enabled profiles, removes its links from their target folders, and the folders this
leaves empty (up to the managed folder):
  - the playbook is chosen, in each profile, by an id, a name or a playbook folder (when it designates a single
    playbook), or by a name and a playbook folder;
  - only links are removed: the playbook folder, and anything that is not a link into it, are left in place.

Options:
  -p, --profile PROFILE,...   the profiles to remove it from (default: the enabled profile, when only one is)

Examples:
  aiplam remove arxila
  aiplam remove arxila@arxila-ai-playbook         # by id, when several playbooks share the name
  aiplam remove ../old-playbook --profile acme    # by playbook folder, in profile 'acme'
`

func (a *App) cmdRemove(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usagef("remove needs a name, id or playbook folder")
	}
	if len(p.positional) > 2 {
		return usagef("too many arguments")
	}
	st, err := a.load(p.positional, "remove the playbook from")
	if err != nil {
		return err
	}
	var updated []*profileState
	scope := map[playbook.Key]bool{}
	for _, sel := range st.selections {
		prof := *sel.profile.prof
		prof.Playbooks = nil
		for _, pb := range sel.profile.prof.Playbooks {
			if !(pb.Name == sel.entry.Name && fsutil.SamePath(pb.Dir(a.Managed), sel.entry.Dir)) {
				prof.Playbooks = append(prof.Playbooks, pb)
			}
		}
		u, err := a.newProfileState(&prof, sel.profile.enabled)
		if err != nil {
			return err
		}
		updated = append(updated, u)
		if u.enabled {
			scope[sel.entry.Key()] = true
		}
		a.printf("Removing playbook %s of profile '%s' (%s)\n", sel.entry.ID, sel.profile.name, a.pretty(sel.entry.Dir))
	}
	var plan playbook.Plan
	if len(scope) > 0 {
		plan, err = playbook.Transition(groups(st.enabled), groups(replaceProfiles(st.enabled, updated...)), scope)
		if err != nil {
			return err
		}
	}
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook is still registered: fix the problem and remove it again", err)
	}
	for i, u := range updated {
		if err := config.SaveProfile(a.Managed, u.prof); err != nil {
			for _, sel := range st.selections[:i] {
				_ = config.SaveProfile(a.Managed, sel.profile.prof)
			}
			return a.undo(plan, err, "the playbook is still registered")
		}
	}
	a.cleanup(plan)
	for i, u := range updated {
		a.reportRenamedIDs(st.selections[i].profile.entries, u.entries)
	}
	var ids []string
	for _, sel := range st.selections {
		ids = append(ids, sel.entry.Label())
	}
	sort.Strings(ids)
	a.printf("Removed %s.\n", strings.Join(ids, ", "))
	return nil
}
