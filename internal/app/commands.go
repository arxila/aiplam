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

// --- init ---------------------------------------------------------------------------------------------------------

const initUsage = `Usage: aiplam init <basefolder> [--type T] [--profile PROFILE]
       aiplam init --global [--type T] [--profile PROFILE]

Sets up <basefolder> as a base folder:
  - creates <basefolder>/.aiplam, holding its configuration (aiplam-config.yaml, and one <profile>.yaml per profile);
  - creates the agents configuration folders of the chosen types in <basefolder> (.agents, .claude), where
    artifacts are linked from (skills, for example, from their skills/ subfolder);
  - creates a first profile, and enables it.

From then on, commands run anywhere inside <basefolder> use its configuration: each command uses the nearest base
folder from the current folder up, or else the home folder (unless given --base or --global).

Agents read the agents configuration folders of the folder they start in, of the home folder and, some of them, of
every folder in between. So an artifact already linked from an agents configuration folder of a higher-level base
folder cannot be linked from one of the same type in <basefolder>.

Options:
  -g, --global            use the user's home folder (e.g. /home/username) as base folder, instead of <basefolder>
  -t, --type T            the types of agents configuration folders: 'agents' (.agents, the default) or 'claude'
                          (.claude); several, separated by commas: --type agents,claude
  -p, --profile PROFILE   the name of the first profile (default: 'default')

Examples:
  aiplam init --global                         # ~/.aiplam and ~/.agents
  aiplam init --global --type agents,claude    # ~/.aiplam, ~/.agents and ~/.claude
  aiplam init . --profile backend              # the current folder, with a first profile named 'backend'
  aiplam init ~/Workspace/thymeleaf            # a project: commands run inside it use its configuration
`

func (a *App) cmdInit(args []string) error {
	p, err := parseArgs(args, []string{"type"}, nil)
	if err != nil {
		return err
	}
	switch {
	case a.baseArg != "":
		return usagef("init takes the base folder to set up as an argument, not with --base")
	case a.global && len(p.positional) > 0:
		return usagef("give either a base folder or --global, not both")
	case !a.global && len(p.positional) == 0:
		return usagef("init needs the base folder to set up (for example '.'), or --global for the home folder")
	case len(p.positional) > 1:
		return usagef("unexpected argument: %s", p.positional[1])
	}
	a.Base = a.Home
	if !a.global {
		a.Base = a.absPath(p.positional[0])
		if !fsutil.IsDir(a.Base) {
			return fmt.Errorf("base folder '%s' does not exist", p.positional[0])
		}
	}
	types, err := parseTypes(p.values["type"])
	if err != nil {
		return err
	}
	if len(types) == 0 {
		types = []string{config.DefaultType}
	}
	profile := a.Profile
	if profile == "" {
		profile = config.DefaultProfile
	}
	// Commands suggested from here on name the base folder when the current folder does not lead to it.
	useBase := func() {
		if !a.global && !fsutil.SamePath(config.FindBase(a.absPathAsGiven(a.Cwd), a.Home), a.Base) {
			a.baseArg = a.Base
		}
	}
	file := config.BaseConfigFile(a.Base)
	if _, err := os.Stat(file); err == nil {
		useBase()
		return fmt.Errorf("'%s' is already set up as a base folder: %s exists. Change the types with '%s' and "+
			"manage profiles with '%s'", a.pretty(a.Base), a.pretty(file), a.hint("type"), a.hint("profile"))
	}
	if config.ProfileExists(a.Base, profile) {
		return fmt.Errorf("%s already exists: remove it, or choose another profile with --profile", a.profileFile(profile))
	}
	conf := &config.BaseConfig{Types: types, Enabled: []string{profile}}
	if errs := conf.Validate(); len(errs) > 0 {
		return errs[0]
	}
	for _, dir := range conf.AgentsConfigDirs(a.Base) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := config.SaveProfile(a.Base, profile, &config.Profile{}); err != nil {
		return err
	}
	if err := config.SaveBaseConfig(a.Base, conf); err != nil {
		return err
	}
	useBase()
	a.printf("Created %s\nAgents configuration folders:\n", a.pretty(file))
	for _, dir := range conf.AgentsConfigDirs(a.Base) {
		a.printf("  %s\n", a.pretty(dir))
	}
	a.printf("Created profile '%s' (%s), enabled.\n", profile, a.profileFile(profile))
	a.printHigherLevels()
	if repo := gitRepository(a.Base); repo != "" {
		a.printf("Note: '%s' is inside a git repository. The %s folder can be committed to share the configuration "+
			"(playbook folders are stored relative to '%s'), but the links in the agents configuration folders' "+
			"skills/ subfolders belong to each machine (and cannot be committed on Windows): add them to '%s', for "+
			"example with a line '/%s/skills/<name>-*' for each playbook name.\n", a.pretty(a.Base), config.DirName,
			a.pretty(a.Base), a.pretty(filepath.Join(a.Base, ".gitignore")), config.Types[types[0]])
	}
	a.printf("Next: register a playbook with '%s'.\n", a.cmd("add <name> <playbookfolder>"))
	return nil
}

// printHigherLevels notes, after init, the higher-level base folders whose artifacts cannot be linked here: the home
// folder's agents configuration folders are read by every agent, and those of the base folders in between by some.
func (a *App) printHigherLevels() {
	home, between := false, []string{}
	for _, r := range config.Ancestors(a.Base, a.Home) {
		if fsutil.SamePath(r, fsutil.Canonical(a.Home)) {
			home = true
		} else {
			between = append(between, a.pretty(r))
		}
	}
	const rule = "artifacts already linked from those cannot be linked here from one of the same type"
	switch {
	case home && len(between) == 0:
		a.printf("Note: the home folder is also being used as a base folder for playbook management, and agents "+
			"always read its agents configuration folders: %s.\n", rule)
	case home:
		a.printf("Note: higher-level base folders exist: the home folder, whose agents configuration folders agents "+
			"always read, and %s, whose agents configuration folders some agents read too: %s.\n",
			strings.Join(between, ", "), rule)
	case len(between) > 0:
		a.printf("Note: higher-level base folders exist (%s), and some agents read their agents configuration "+
			"folders too: %s.\n", strings.Join(between, ", "), rule)
	}
}

// gitRepository returns the folder of the git repository that holds dir (the nearest folder from dir up with a .git
// folder or file), or "".
func gitRepository(dir string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// parseTypes turns --type values (or 'aiplam type' arguments) into types; values can hold several types separated by
// commas.
func parseTypes(values []string) ([]string, error) {
	var types []string
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if _, ok := config.Types[t]; !ok {
				return nil, usagef("unknown type '%s': use %s", t, strings.Join(config.TypeNames(), ", "))
			}
			for _, prev := range types {
				if prev == t {
					return nil, usagef("type '%s' is given more than once", t)
				}
			}
			types = append(types, t)
		}
	}
	return types, nil
}

// --- add ----------------------------------------------------------------------------------------------------------

const addUsage = `Usage: aiplam add <name> <playbookfolder> [--skills-path PATH] [--profile PROFILE]

Registers the playbook in <playbookfolder> under <name> and, if its profile is enabled, links its artifacts from the
agents configuration folders of the base folder, at whatever tag or commit is checked out:
  - its artifacts are those named '<name>-*': its skills, for example, are the folders named '<name>-*' that hold a
    SKILL.md, in its skills folder;
  - <playbookfolder> is stored relative to the base folder;
  - the profile is the one given with --profile, which can be left out when exactly one profile is enabled;
  - several playbooks can share a name if the last segments of their playbook folders differ: their ids are then
    '<name>@<last folder segment>'.

Nothing is changed if:
  - something other than the right link is already where a link must go;
  - another playbook of the profile, or of another enabled profile, provides an artifact with the same name;
  - an artifact with the same name is already linked from an agents configuration folder of the same type of a
    higher-level base folder.

Options:
  -sp, --skills-path PATH   the skills folder, relative to <playbookfolder> (default: skills; '.' for
                            <playbookfolder> itself)
  -p, --profile PROFILE     the profile to register it in (default: the enabled profile, when only one is)

Examples:
  aiplam add arxila .                                     # the current folder, skills in ./skills
  aiplam add arxila ~/Workspace/arxila-ai-playbook --skills-path ai/skills
  aiplam add acme ../acme-ai-playbook --profile acme      # in profile 'acme', linked only if it is enabled
`

func (a *App) cmdAdd(args []string) error {
	p, err := parseArgs(args, []string{"skills-path"}, nil)
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
	folder := a.absPath(p.positional[1])
	if !fsutil.IsDir(folder) {
		return fmt.Errorf("playbook folder '%s' does not exist", p.positional[1])
	}
	skillsPath := config.DefaultSkillsPath
	if v, ok := p.values["skills-path"]; ok {
		skillsPath = v[len(v)-1]
		if !config.ValidArtifactPath(skillsPath) {
			return usagef("--skills-path must be a path inside the playbook folder, relative to it")
		}
		skillsPath = filepath.ToSlash(filepath.Clean(skillsPath))
	}

	st, err := a.load()
	if err != nil {
		return err
	}
	if len(st.profiles) != 1 {
		if len(st.profiles) == 0 {
			return errors.New("no profile is enabled: choose the profile to register the playbook in with --profile")
		}
		return fmt.Errorf("%d profiles are enabled (%s): choose the one to register the playbook in with --profile",
			len(st.profiles), profileNames(st.profiles))
	}
	profile := st.profiles[0]
	a.useProfile(st, profile)
	current := profile.entries
	for _, e := range current {
		if e.Name != name {
			continue
		}
		if fsutil.SamePath(e.Dir, folder) {
			return fmt.Errorf("playbook '%s' is already registered for playbook folder '%s' in profile '%s' (id %s); "+
				"use '%s'", name,
				a.pretty(folder), profile.name, e.ID, a.cmd("update "+e.ID))
		}
		if fsutil.SameName(filepath.Base(e.Dir), filepath.Base(folder)) {
			return fmt.Errorf("a playbook named '%s' is already registered for playbook folder '%s', whose last segment equals "+
				"that of '%s': their ids would collide", name, a.pretty(e.Dir), a.pretty(folder))
		}
	}
	rel, err := config.RelFolder(a.Base, folder)
	if err != nil {
		return err
	}

	entry := config.Playbook{Name: name, Folder: rel,
		Artifacts: config.Artifacts{Skills: config.Skills{Path: skillsPath}}}
	if !fsutil.IsDir(entry.SkillsDir(a.Base)) {
		return fmt.Errorf("skills folder '%s' does not exist; use --skills-path to set it ('.' for the playbook folder "+
			"itself)", a.pretty(entry.SkillsDir(a.Base)))
	}
	if !fsutil.Within(entry.SkillsDir(a.Base), folder) {
		return fmt.Errorf("skills folder '%s' leads outside the playbook folder (through a link)",
			a.pretty(entry.SkillsDir(a.Base)))
	}
	updated := &config.Profile{Playbooks: append(append([]config.Playbook{}, profile.prof.Playbooks...), entry)}
	final := playbook.Entries(profile.name, updated.Playbooks, a.Base)
	e := final[len(final)-1]

	var plan playbook.Plan
	if profile.enabled {
		plan, err = playbook.Build(final, e, st.agentsConfigDirs, st.others(profile.name))
		if err != nil {
			return err
		}
		playbook.MarkAncestors(&plan, a.ancestorAgentsConfigDirs())
	} else {
		// Nothing is linked, but the profile must stay linkable: no artifact provided by two playbooks.
		overlaps, err := playbook.Overlaps(final, e)
		if err != nil {
			return err
		}
		for name, others := range overlaps {
			plan.Actions = append(plan.Actions, playbook.Action{Kind: playbook.Conflict, Artifact: "skill",
				Name: name, Cause: playbook.OtherProvider, Others: others})
		}
	}
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("playbook '%s' was not added: %s.", e.ID, a.conflictAdvice(plan))
		return errSilent
	}

	a.printf("Adding playbook %s (%s) from %s to profile '%s'\n", e.ID, a.version(folder), a.pretty(folder), profile.name)
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook was not registered: fix the problem and run '%s' again", err, a.cmd("add"))
	}
	if err := config.SaveProfile(a.Base, profile.name, updated); err != nil {
		return a.undo(plan, err, "the playbook was not registered")
	}
	skills, _ := playbook.Skills(final, e)
	if len(skills) == 0 {
		a.warnf("no skills named '%s-*' (folders with a %s) were found in %s.", name, playbook.SkillFile,
			a.pretty(e.SkillsDir))
	}
	a.printf("Added %s with %d skill(s).\n", e.ID, len(skills))
	if !profile.enabled {
		a.printf("Nothing was linked: %s.\n", a.notEnabledNote(profile.name))
	}
	a.reportRenamedIDs(current, final)
	return nil
}

// reportRenamedIDs tells the user when registering or removing a playbook changed the ids of others.
func (a *App) reportRenamedIDs(before, after []playbook.Entry) {
	for _, b := range before {
		for _, f := range after {
			if b.Name == f.Name && fsutil.SamePath(b.Dir, f.Dir) && b.ID != f.ID {
				a.printf("Playbook %s is now identified as %s.\n", b.ID, f.ID)
			}
		}
	}
}

// --- update -------------------------------------------------------------------------------------------------------

const updateUsage = `Usage: aiplam update [<name|id|playbookfolder>] [<playbookfolder>] [--profile PROFILE]
       aiplam update --all [--profile PROFILE]

Brings a playbook's links in line with its playbook folder, as currently checked out:
  - links its new artifacts;
  - removes the links of artifacts that no longer exist.

The playbook is chosen by:
  - nothing: the playbook registered for the current folder;
  - an id, a name or a playbook folder, when it designates a single playbook;
  - a name and a playbook folder.

aiplam never fetches, pulls or switches a checkout: select the version in the playbook folder first. Only the
playbooks of the enabled profiles are linked, so only they have anything to update. Nothing is changed for a
playbook if something other than the right link is already where a link must go.

Options:
  --all                   update every playbook of the enabled profiles (or of the profile given with --profile)
  -p, --profile PROFILE   look for the playbook in profile PROFILE (default: the enabled profiles)

Examples:
  cd ~/Workspace/arxila-ai-playbook
  git switch --detach v1.3.0
  aiplam update                # the playbook registered for the current folder
  aiplam update arxila
  aiplam update --all
`

func (a *App) cmdUpdate(args []string) error {
	p, err := parseArgs(args, nil, []string{"all"})
	if err != nil {
		return err
	}
	if p.bools["all"] && len(p.positional) > 0 {
		return usagef("--all takes no other arguments")
	}
	st, err := a.load()
	if err != nil {
		return err
	}
	selected := st.entries()
	if !p.bools["all"] {
		e, err := a.selectEntry(st, p.positional)
		if err != nil {
			return err
		}
		selected = []playbook.Entry{e}
	} else if len(selected) == 0 {
		a.printf("No playbooks are registered in %s.\n", st.where())
		return nil
	}
	if len(st.profiles) == 1 && !st.profiles[0].enabled {
		a.printf("Nothing to update: %s.\n", a.notEnabledNote(st.profiles[0].name))
		return nil
	}

	failed := false
	for i, e := range selected {
		if i > 0 {
			a.printf("\n")
		}
		profile := st.profile(e.Profile)
		a.useProfile(st, profile)
		if err := a.updateOne(profile.entries, e, st.agentsConfigDirs, st.others(profile.name)); err != nil {
			if !errors.Is(err, errSilent) {
				a.errorf("%s", err)
			}
			failed = true
		}
	}
	if failed {
		return errSilent
	}
	return nil
}

func (a *App) updateOne(entries []playbook.Entry, e playbook.Entry, agentsConfigDirs []string,
	others [][]playbook.Entry) error {
	if err := a.checkPlaybookFolders(e); err != nil {
		return err
	}
	plan, err := playbook.Build(entries, e, agentsConfigDirs, others)
	if err != nil {
		return err
	}
	playbook.MarkAncestors(&plan, a.ancestorAgentsConfigDirs())
	a.printf("Updating playbook %s (%s) from %s\n", e.ID, a.version(e.Dir), a.pretty(e.Dir))
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("update of playbook %s cancelled: %s.", e.ID, a.conflictAdvice(plan))
		return errSilent
	}
	if len(plan.Changes()) == 0 {
		a.printf("  up to date\n")
		return nil
	}
	return playbook.Apply(plan, a.printAction)
}

// --- remove -------------------------------------------------------------------------------------------------------

const removeUsage = `Usage: aiplam remove <name|id|playbookfolder> [<playbookfolder>] [--profile PROFILE]

Unregisters a playbook and, if its profile is enabled, removes its links from the agents configuration folders:
  - the playbook is chosen by an id, a name or a playbook folder (when it designates a single playbook), or by a
    name and a playbook folder;
  - only links are removed: the playbook folder, and anything that is not a link into it, are left in place.

Options:
  -p, --profile PROFILE   look for the playbook in profile PROFILE (default: the enabled profiles)

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
	st, err := a.load()
	if err != nil {
		return err
	}
	e, err := a.selectEntry(st, p.positional)
	if err != nil {
		return err
	}
	profile := st.profile(e.Profile)
	a.useProfile(st, profile)
	updated := &config.Profile{}
	for _, pb := range profile.prof.Playbooks {
		if !(pb.Name == e.Name && fsutil.SamePath(pb.Dir(a.Base), e.Dir)) {
			updated.Playbooks = append(updated.Playbooks, pb)
		}
	}
	final := playbook.Entries(profile.name, updated.Playbooks, a.Base)
	var plan playbook.Plan
	if profile.enabled {
		plan, err = playbook.Build(final, e, st.agentsConfigDirs, st.others(profile.name))
		if err != nil {
			return err
		}
	}
	a.printf("Removing playbook %s (%s) from profile '%s'\n", e.ID, a.pretty(e.Dir), profile.name)
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook is still registered: fix the problem and run '%s' again", err,
			a.cmd("remove"))
	}
	if err := config.SaveProfile(a.Base, profile.name, updated); err != nil {
		return a.undo(plan, err, "the playbook is still registered")
	}
	a.printf("Removed %s.\n", e.ID)
	a.reportRenamedIDs(profile.entries, final)
	return nil
}

// undo reverts the link changes of an applied plan after saving the configuration failed.
func (a *App) undo(plan playbook.Plan, saveErr error, state string) error {
	if err := playbook.Revert(plan.Changes()); err != nil {
		return fmt.Errorf("%w; undoing the link changes also failed (%v): run '%s' to see what is left", saveErr, err,
			a.cmd("check"))
	}
	if len(plan.Changes()) == 0 {
		return fmt.Errorf("%w; %s", saveErr, state)
	}
	return fmt.Errorf("%w; the link changes were undone and %s", saveErr, state)
}
