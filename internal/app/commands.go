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

const initUsage = `Usage: aiplam init -t|--target agents|claude|DIR... [-s|--set SET]

Sets up aiplam: creates ~/.aiplam/aiplam-config.yaml with the target folders, where the artifacts of the enabled
set's playbooks are installed (skills go to their skills/ subfolder), creates the target folders, and creates an empty
set and enables it. The other commands need it.

  -t, --target T   a target folder: 'agents' (~/.agents), 'claude' (~/.claude) or any folder. Required. Repeat the
                   option, or separate values with commas, for several targets: --target agents,claude
  -s, --set SET    the name of the first set (default: 'default')

Change the targets later with 'aiplam target', and manage sets with 'aiplam set'.
`

func (a *App) cmdInit(args []string) error {
	p, err := parseArgs(args, []string{"target"}, nil)
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	targets, err := a.parseTargets(p.values["target"])
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return usagef("init needs at least one target folder: --target agents|claude|DIR")
	}
	set := a.Set
	if set == "" {
		set = config.DefaultSet
	}
	file := config.InstallFile(a.Home)
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("aiplam is already initialized: %s exists. Change the targets with 'aiplam target' and "+
			"manage sets with 'aiplam set'", a.pretty(file))
	}
	if config.SetExists(a.Home, set) {
		return fmt.Errorf("%s already exists: remove it, or choose another set with --set", a.setFile(set))
	}
	inst := &config.Install{Targets: targets, Enabled: []string{set}}
	if errs := inst.Validate(a.Home); len(errs) > 0 {
		return errs[0]
	}
	for _, dir := range inst.TargetDirs(a.Home) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := config.SaveSet(a.Home, set, &config.Set{}); err != nil {
		return err
	}
	if err := config.SaveInstall(a.Home, inst); err != nil {
		return err
	}
	a.printf("Created %s\nTargets:\n", a.pretty(file))
	for _, dir := range inst.TargetDirs(a.Home) {
		a.printf("  %s\n", a.pretty(dir))
	}
	a.printf("Created set '%s' (%s), enabled.\n", set, a.setFile(set))
	a.printf("Next: register a playbook with 'aiplam add <name> <folder>'.\n")
	return nil
}

// parseTargets turns --target values (or 'aiplam target' arguments) into target folders: 'agents' and 'claude' stand
// for ~/.agents and ~/.claude, other values are folders; values can hold several targets separated by commas.
func (a *App) parseTargets(values []string) ([]string, error) {
	var targets []string
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			switch t = strings.TrimSpace(t); t {
			case "":
				return nil, usagef("empty target value")
			case "agents":
				targets = append(targets, "~/.agents")
			case "claude":
				targets = append(targets, "~/.claude")
			default:
				targets = append(targets, a.absPath(t))
			}
		}
	}
	return targets, nil
}

// --- add ----------------------------------------------------------------------------------------------------------

const addUsage = `Usage: aiplam add <name> <folder> [-sp|--skills-path PATH] [-s|--set SET]

Registers the playbook in <folder> (for example '.') with the given name in a set and, if the set is enabled, links
its skills into the targets. The set is the one given with --set, which is needed unless exactly one set is enabled. The name is the prefix of the playbook's
artifacts: its skills are the folders named '<name>-*' holding a SKILL.md in its skills folder. Several playbooks can
share a name if their folders' last segments differ; their ids are then '<name>@<last folder segment>'. The playbook
is installed at whatever tag or commit is checked out.

Nothing is changed if any link cannot be created because something else is already in its place, or if another
playbook of the set (or of another enabled set, if the set is enabled) provides a skill with the same name.

  -sp, --skills-path PATH   the skills folder, inside <folder> and relative to it (default: skills; '.' for <folder>
                            itself)
  -s, --set SET             the set to register it in (default: the enabled set, if only one is)
`

func (a *App) cmdAdd(args []string) error {
	p, err := parseArgs(args, []string{"skills-path"}, nil)
	if err != nil {
		return err
	}
	if len(p.positional) != 2 {
		return usagef("add needs a name and a folder")
	}
	name := p.positional[0]
	if !config.ValidName(name) {
		return usagef("invalid name '%s': use letters, digits, '.', '_' and '-', starting with a letter or digit", name)
	}
	folder := a.absPath(p.positional[1])
	if !fsutil.IsDir(folder) {
		return fmt.Errorf("folder '%s' does not exist", p.positional[1])
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
	if len(st.sets) != 1 {
		if len(st.sets) == 0 {
			return errors.New("no set is enabled: choose the set to register the playbook in with --set")
		}
		return fmt.Errorf("%d sets are enabled (%s): choose the one to register the playbook in with --set",
			len(st.sets), setNames(st.sets))
	}
	set := st.sets[0]
	a.useSet(st, set)
	current := set.entries
	for _, e := range current {
		if e.Name != name {
			continue
		}
		if fsutil.SamePath(e.Dir, folder) {
			return fmt.Errorf("playbook '%s' is already registered for '%s' in set '%s' (id %s); use '%s'", name,
				a.pretty(folder), set.name, e.ID, a.cmd("update "+e.ID))
		}
		if fsutil.SameName(filepath.Base(e.Folder), filepath.Base(folder)) {
			return fmt.Errorf("a playbook named '%s' is already registered for '%s', whose last segment equals that of "+
				"'%s': their ids would collide", name, a.pretty(e.Folder), a.pretty(folder))
		}
	}

	entry := config.Playbook{Name: name, Folder: folder,
		Artifacts: config.Artifacts{Skills: config.Skills{Path: skillsPath}}}
	if !fsutil.IsDir(entry.SkillsDir(a.Home)) {
		return fmt.Errorf("skills folder '%s' does not exist; use --skills-path to set it ('.' for the playbook folder "+
			"itself)", a.pretty(entry.SkillsDir(a.Home)))
	}
	if !fsutil.Within(entry.SkillsDir(a.Home), folder) {
		return fmt.Errorf("skills folder '%s' leads outside the playbook folder (through a link)",
			a.pretty(entry.SkillsDir(a.Home)))
	}
	updated := &config.Set{Playbooks: append(append([]config.Playbook{}, set.cfg.Playbooks...), entry)}
	final := playbook.Entries(set.name, updated.Playbooks, a.Home)
	e := final[len(final)-1]

	var plan playbook.Plan
	if set.enabled {
		plan, err = playbook.Build(final, e, st.targets, st.others(set.name))
		if err != nil {
			return err
		}
	} else {
		// Nothing is linked, but the set must stay installable: no skill provided by two playbooks.
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
		a.errorf("playbook '%s' was not added: remove or rename what is in the way, then try again.", e.ID)
		return errSilent
	}

	a.printf("Adding playbook %s (%s) from %s to set '%s'\n", e.ID, a.version(folder), a.pretty(folder), set.name)
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook was not registered: fix the problem and run '%s' again", err, a.cmd("add"))
	}
	if err := config.SaveSet(a.Home, set.name, updated); err != nil {
		return a.undo(plan, err, "the playbook was not registered")
	}
	skills, _ := playbook.Skills(final, e)
	if len(skills) == 0 {
		a.warnf("no skills named '%s-*' (folders with a %s) were found in %s.", name, playbook.SkillFile,
			a.pretty(e.SkillsDir))
	}
	a.printf("Added %s with %d skill(s).\n", e.ID, len(skills))
	if !set.enabled {
		a.printf("Nothing was linked: %s.\n", a.notEnabledNote(set.name))
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

const updateUsage = `Usage: aiplam update [<name|id|folder>] [<folder>] [-s|--set SET]
       aiplam update --all [-s|--set SET]

Brings the installed artifacts of a playbook of the enabled sets in line with its folder as currently checked out:
links new skills and removes the links of skills that no longer exist. The playbook is the one registered for the
current folder, or the one given by id, by name (if unique), by folder (if unique), or by name and folder. aiplam
never fetches, pulls or switches a checkout: select the version in the playbook's folder first. A set that is not
enabled has nothing installed, so there is nothing to update in it.

Nothing is changed for a playbook if any link cannot be created because something else is already in its place.

  --all           update every playbook of the enabled sets (or of the set given with --set)
  -s, --set SET   the set (default: the enabled sets)
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
	if len(st.sets) == 1 && !st.sets[0].enabled {
		a.printf("Nothing to update: %s.\n", a.notEnabledNote(st.sets[0].name))
		return nil
	}

	failed := false
	for i, e := range selected {
		if i > 0 {
			a.printf("\n")
		}
		set := st.set(e.Set)
		a.useSet(st, set)
		if err := a.updateOne(set.entries, e, st.targets, st.others(set.name)); err != nil {
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

func (a *App) updateOne(entries []playbook.Entry, e playbook.Entry, targets []string,
	others [][]playbook.Entry) error {
	if err := a.checkPlaybookFolders(e); err != nil {
		return err
	}
	plan, err := playbook.Build(entries, e, targets, others)
	if err != nil {
		return err
	}
	a.printf("Updating playbook %s (%s) from %s\n", e.ID, a.version(e.Dir), a.pretty(e.Dir))
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("update of playbook %s cancelled: remove or rename what is in the way, then try again.", e.ID)
		return errSilent
	}
	if len(plan.Changes()) == 0 {
		a.printf("  up to date\n")
		return nil
	}
	return playbook.Apply(plan, a.printAction)
}

// --- remove -------------------------------------------------------------------------------------------------------

const removeUsage = `Usage: aiplam remove <name|id|folder> [<folder>] [-s|--set SET]

Unregisters a playbook from its set and, if the set is enabled, removes the links to its artifacts from the targets.
The playbook is looked for in the enabled sets (or in the set given with --set), by id, by name (if unique), by
folder (if unique), or by name and folder. Only links are
removed: the playbook's folder and anything that is not a link to it are left in place.

  -s, --set SET   the set (default: the enabled sets)
`

func (a *App) cmdRemove(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usagef("remove needs a name, id or folder")
	}
	st, err := a.load()
	if err != nil {
		return err
	}
	e, err := a.selectEntry(st, p.positional)
	if err != nil {
		return err
	}
	set := st.set(e.Set)
	a.useSet(st, set)
	updated := &config.Set{}
	for _, pb := range set.cfg.Playbooks {
		if !(pb.Name == e.Name && fsutil.SamePath(pb.Dir(a.Home), e.Dir)) {
			updated.Playbooks = append(updated.Playbooks, pb)
		}
	}
	final := playbook.Entries(set.name, updated.Playbooks, a.Home)
	var plan playbook.Plan
	if set.enabled {
		plan, err = playbook.Build(final, e, st.targets, st.others(set.name))
		if err != nil {
			return err
		}
	}
	a.printf("Removing playbook %s (%s) from set '%s'\n", e.ID, a.pretty(e.Dir), set.name)
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook is still registered: fix the problem and run '%s' again", err,
			a.cmd("remove"))
	}
	if err := config.SaveSet(a.Home, set.name, updated); err != nil {
		return a.undo(plan, err, "the playbook is still registered")
	}
	a.printf("Removed %s.\n", e.ID)
	a.reportRenamedIDs(set.entries, final)
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
