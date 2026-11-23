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
	"slices"
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

// --- init ---------------------------------------------------------------------------------------------------------

const initUsage = `Usage: aiplam init <basefolder> [--agent AGENT] [--profile PROFILE]
       aiplam init --global [--agent AGENT] [--profile PROFILE]

Sets up <basefolder> as a base folder:
  - creates <basefolder>/.aiplam, holding its configuration (aiplam-config.yaml, and one <profile>.yaml per profile);
  - creates a first profile, with the chosen agent, and enables it.

A profile's agent decides which kinds of its playbooks' artifacts are linked, in which format, and from which agents
configuration folders of <basefolder> (.agents/skills for the standard agent, .claude/skills, .claude/agents, ... for
Claude Code): 'aiplam profile --help' lists the agents. Those folders are created when an artifact is first linked
there. More profiles, for other working scopes (a client, a team, a kind of work...), can be added later with
'aiplam profile add', usually with the same agent, though each profile can have its own.

From then on, commands run anywhere inside <basefolder> use its configuration: each command uses the nearest base
folder from the current folder up, or else the home folder (unless given --base or --global).

Agents read the agents configuration folders of the folder they start in, of the home folder and, some of them, of
every folder in between. So an artifact already linked for an agent in a higher-level base folder cannot be linked
for the same agent in <basefolder>.

Options:
  -g, --global            use the user's home folder (e.g. /home/username) as base folder, instead of <basefolder>
  -a, --agent AGENT       the agent assigned to the first profile (default: standard)
  -p, --profile PROFILE   the name of the first profile (default: 'default')

Examples:
  aiplam init --global                      # ~/.aiplam, with a first profile for the standard agent (~/.agents)
  aiplam init --global --agent claude       # ~/.aiplam, with a first profile for Claude Code (~/.claude)
  aiplam init . --profile backend           # the current folder, with a first profile named 'backend'
  aiplam init ~/Workspace/thymeleaf         # a project: commands run inside it use its configuration
`

func (a *App) cmdInit(args []string) error {
	p, err := parseArgs(args, []string{"agent"}, nil)
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
	agent, err := parseAgent(p, config.DefaultAgent)
	if err != nil {
		return err
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
		return fmt.Errorf("'%s' is already set up as a base folder: %s exists. Manage its profiles, and their "+
			"agents, with '%s'", a.pretty(a.Base), a.pretty(file), a.hint("profile"))
	}
	if config.ProfileExists(a.Base, profile) {
		return fmt.Errorf("%s already exists: remove it, or choose another profile with --profile", a.profileFile(profile))
	}
	prof := &config.Profile{Agent: agent.Name}
	if err := config.SaveProfile(a.Base, profile, prof); err != nil {
		return err
	}
	if err := config.SaveBaseConfig(a.Base, &config.BaseConfig{Enabled: []string{profile}}); err != nil {
		return err
	}
	useBase()
	s := a.newProfileState(profile, prof, true)
	a.printf("Created %s\n", a.pretty(file))
	a.printf("Created profile '%s' (%s), with agent %s (%s), enabled: its playbooks' artifacts are linked from %s.\n",
		profile, a.profileFile(profile), agent.Name, agent.Product, a.folderList(s))
	a.printHigherLevels()
	if repo := gitRepository(a.Base); repo != "" && len(s.folders) > 0 {
		var lines []string
		for _, f := range s.folders {
			rel, _ := filepath.Rel(a.Base, f.Dir)
			lines = append(lines, "'/"+filepath.ToSlash(rel)+"/<name>-*'")
		}
		a.printf("Note: '%s' is inside a git repository. The %s folder can be committed to share the configuration "+
			"(playbook folders are stored relative to '%s'), but the links aiplam makes in the agents configuration "+
			"folders belong to each machine (and cannot be committed on Windows): add them to '%s', with lines such as "+
			"%s for each playbook name (and the same for the agents of profiles added later).\n", a.pretty(a.Base),
			config.DirName, a.pretty(a.Base), a.pretty(filepath.Join(a.Base, ".gitignore")), strings.Join(lines, ", "))
	}
	a.printf("Next: register a playbook with '%s'.\n", a.cmd("add <name> <playbookfolder>"))
	return nil
}

// parseAgent returns the agent given with --agent, or the agent named def when none was given.
func parseAgent(p parsedArgs, def string) (config.Agent, error) {
	name := def
	if v := p.value("agent"); v != "" {
		name = v
	}
	agent, ok := config.LookupAgent(name)
	if !ok {
		return agent, usagef("unknown agent '%s': use %s", name, strings.Join(config.AgentNames(), ", "))
	}
	return agent, nil
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
	const rule = "artifacts already linked there for an agent cannot be linked here for the same agent"
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

// --- add ----------------------------------------------------------------------------------------------------------

const addUsage = `Usage: aiplam add <name> <playbookfolder> [--<kind>-path PATH...] [--profile PROFILE]

Registers the playbook in <playbookfolder> under <name> in a profile and, if the profile is enabled, links the
playbook's artifacts that the profile's agent reads, at whatever tag or commit is checked out:
  - the profile is the one given with --profile, which can be left out when exactly one profile is enabled;
  - its artifacts are those named '<name>-*' in its artifact folder for each kind: skills (folders holding a
    SKILL.md), and subagents (custom agent definitions, which some agents, such as Copilot and OpenCode, also offer
    as primary agents), commands, prompts, rules and instructions (files);
  - only the kinds the agent reads are linked, and only the files in its format ('.md', '.agent.md', '.toml', ...),
    each from the agent's folder for the kind in the base folder ('aiplam profile --help' lists them); a playbook
    registered in several profiles with different agents is linked by each for its agent;
  - <playbookfolder> is stored relative to the base folder;
  - several playbooks can share a name if the last segments of their playbook folders differ: their ids are then
    '<name>@<last folder segment>'.

The playbook's artifact folder for each kind is, by default, named like the subfolder where the profile's agent
reads that kind: skills/, agents/ (for subagents), commands/, prompts/, rules/ and instructions/ for every agent known
so far. Defaults are not stored, so they follow the profile's agent if it changes; a folder set with the options
below is stored, and used whatever the agent. At least one artifact folder for a kind the agent reads must exist in
<playbookfolder>.

Nothing is changed if:
  - something other than the right link is already where a link must go;
  - another playbook of the profile, or of another enabled profile, needs a link at the same place (profiles for
    codex and for the standard agent, for example, share .agents/skills);
  - an artifact with the same name is already linked for the same agent in a higher-level base folder.

Options:
  -sp, --skills-path PATH         the skills folder, relative to <playbookfolder> ('.' for <playbookfolder> itself)
  -sap, --subagents-path PATH     the subagents folder
  -cp, --commands-path PATH       the commands folder
  -pp, --prompts-path PATH        the prompts folder
  -rp, --rules-path PATH          the rules folder
  -ip, --instructions-path PATH   the instructions folder
  -p, --profile PROFILE           the profile to register it in (default: the enabled profile, when only one is)

Examples:
  aiplam add arxila .                                     # the current folder: skills/, agents/, ... in it
  aiplam add arxila ~/Workspace/arxila-ai-playbook --skills-path ai/skills --subagents-path ai/subagents
  aiplam add acme ../acme-ai-playbook --profile acme      # in profile 'acme', linked only if it is enabled
`

// pathOption returns the name of the option that sets the folder of a kind of artifact: "skills-path".
func pathOption(k config.Kind) string { return string(k) + "-path" }

func (a *App) cmdAdd(args []string) error {
	var options []string
	for _, k := range config.Kinds {
		options = append(options, pathOption(k))
	}
	p, err := parseArgs(args, options, nil)
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
	var artifacts config.Artifacts
	for _, k := range config.Kinds {
		if _, ok := p.values[pathOption(k)]; !ok {
			continue
		}
		path := p.value(pathOption(k))
		if !config.ValidArtifactPath(path) {
			return usagef("--%s must be a path inside the playbook folder, relative to it", pathOption(k))
		}
		artifacts.Kind(k).Path = filepath.ToSlash(filepath.Clean(path))
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

	entry := config.Playbook{Name: name, Folder: rel, Artifacts: artifacts}
	updated := &config.Profile{Agent: profile.prof.Agent,
		Playbooks: append(append([]config.Playbook{}, profile.prof.Playbooks...), entry)}
	final := a.newProfileState(profile.name, updated, profile.enabled)
	e := final.entries[len(final.entries)-1]
	if err := a.checkPlaybookFolders(e, final.folders); err != nil {
		return err
	}
	// The playbook must have at least one artifact folder for a kind that its profile's agent reads.
	var expected []string
	found := false
	for _, f := range final.folders {
		if path := e.Path(f.Kind, f.DefaultPath) + "/"; !slices.Contains(expected, path) {
			expected = append(expected, path)
		}
		found = found || fsutil.IsDir(e.KindDir(f))
	}
	if !found {
		return fmt.Errorf("none of the artifact folders for the kinds that agent %s reads (%s) was found in '%s': set "+
			"them with --skills-path and the like ('.' for the playbook folder itself)", final.agent.Name,
			strings.Join(expected, ", "), a.pretty(folder))
	}

	var plan playbook.Plan
	if profile.enabled {
		plan, err = playbook.Build(final.group(), e, st.others(profile.name))
		if err != nil {
			return err
		}
		a.markAncestors(&plan)
	} else {
		// Nothing is linked, but the profile must stay linkable: no two playbooks needing a link at the same place.
		plan.Actions, err = playbook.Overlaps(final.group(), e)
		if err != nil {
			return err
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
	counts := a.countItems(final, e)
	if counts == "" {
		a.warnf("no artifacts named '%s-*' that agent %s reads were found in '%s'.", name, final.agent.Name,
			a.pretty(folder))
		counts = "no artifacts"
	}
	a.printf("Added %s with %s.\n", e.ID, counts)
	if !profile.enabled {
		a.printf("Nothing was linked: %s.\n", a.notEnabledNote(profile.name))
	} else {
		st.enabled = replaceProfile(st.enabled, final)
		a.warnDoubleLoads(st.enabled)
	}
	a.reportRenamedIDs(current, final.entries)
	return nil
}

// countItems describes the artifacts a playbook provides for its profile's agent: "3 skill(s), 1 subagent(s)", or ""
// when there are none.
func (a *App) countItems(s *profileState, e playbook.Entry) string {
	var counts []string
	for _, f := range s.folders {
		if items, _ := playbook.Items(s.entries, e, f); len(items) > 0 {
			counts = append(counts, fmt.Sprintf("%d %s(s)", len(items), f.Kind.Singular()))
		}
	}
	return strings.Join(counts, ", ")
}

// replaceProfile returns profiles with the one named like s replaced by s.
func replaceProfile(profiles []*profileState, s *profileState) []*profileState {
	out := append([]*profileState{}, profiles...)
	for i, p := range out {
		if p.name == s.name {
			out[i] = s
		}
	}
	return out
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

Brings a playbook's links in line with its playbook folder, as currently checked out, for its profile's agent:
  - links its new artifacts;
  - removes the links of artifacts that no longer exist, and the folders that leaves empty.

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
		if err := a.updateOne(profile.group(), e, st.others(profile.name)); err != nil {
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

func (a *App) updateOne(group playbook.Group, e playbook.Entry, others []playbook.Group) error {
	if err := a.checkPlaybookFolders(e, group.Folders); err != nil {
		return err
	}
	plan, err := playbook.Build(group, e, others)
	if err != nil {
		return err
	}
	a.markAncestors(&plan)
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
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return err
	}
	a.cleanup(plan)
	return nil
}

// --- remove -------------------------------------------------------------------------------------------------------

const removeUsage = `Usage: aiplam remove <name|id|playbookfolder> [<playbookfolder>] [--profile PROFILE]

Unregisters a playbook and, if its profile is enabled, removes its links from the folders of its profile's agent, and
the folders that leaves empty:
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
	updated.Agent = profile.prof.Agent
	final := a.newProfileState(profile.name, updated, profile.enabled)
	var plan playbook.Plan
	if profile.enabled {
		plan, err = playbook.Build(final.group(), e, st.others(profile.name))
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
	a.cleanup(plan)
	a.printf("Removed %s.\n", e.ID)
	a.reportRenamedIDs(profile.entries, final.entries)
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
