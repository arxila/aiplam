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

const initUsage = `Usage: aiplam init [-t|--target agents|claude|DIR]... [-s|--set SET]

Creates the configuration file of a set, ~/.aiplam/<set>.yaml, and its target folders, where artifacts are installed
(skills go to their skills/ subfolder). The other commands need it. Each set is an independent configuration, with its
own targets and playbooks; commands act on the 'default' set unless given --set. Sets cannot share targets.

  -t, --target T   a target folder: 'agents' (~/.agents, the default), 'claude' (~/.claude) or any folder. Repeat
                   the option, or separate values with commas, for several targets: --target agents,claude
  -s, --set SET    the set to create (default: 'default')

To change the targets later, edit ~/.aiplam/<set>.yaml by hand and move or recreate the installed links yourself.
`

func (a *App) cmdInit(args []string) error {
	p, err := parseArgs(args, []string{"target"}, nil)
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	file := config.File(a.Home, a.set())
	if _, err := os.Stat(file); err == nil {
		what := "aiplam is"
		if a.set() != config.DefaultSet {
			what = fmt.Sprintf("set '%s' is", a.set())
		}
		return fmt.Errorf("%s already initialized: %s exists. To change its targets, edit it by hand (and move or "+
			"recreate the installed links yourself); '%s' validates it", what, a.pretty(file), a.cmd("check"))
	}

	var targets []string
	for _, v := range p.values["target"] {
		for _, t := range strings.Split(v, ",") {
			switch t = strings.TrimSpace(t); t {
			case "":
				return usagef("empty --target value")
			case "agents":
				targets = append(targets, "~/.agents")
			case "claude":
				targets = append(targets, "~/.claude")
			default:
				targets = append(targets, a.absPath(t))
			}
		}
	}
	if len(targets) == 0 {
		targets = []string{"~/.agents"}
	}
	cfg := &config.Config{Targets: targets}
	if errs := cfg.Validate(a.Home); len(errs) > 0 {
		return errs[0]
	}
	shared, err := config.SharedTargets(a.Home, a.set(), cfg)
	if err != nil {
		return err
	}
	if len(shared) > 0 {
		return fmt.Errorf("target '%s' already belongs to set '%s': sets cannot share targets, as their links would "+
			"get in each other's way", a.pretty(shared[0].Target), shared[0].Set)
	}
	for _, dir := range cfg.TargetDirs(a.Home) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := config.Save(a.Home, a.set(), cfg); err != nil {
		return err
	}
	a.printf("Created set '%s' in %s\nTargets:\n", a.set(), a.pretty(file))
	for _, dir := range cfg.TargetDirs(a.Home) {
		a.printf("  %s\n", a.pretty(dir))
	}
	a.printf("Next: register a playbook with '%s'.\n", a.cmd("add <name> <folder>"))
	return nil
}

// --- add ----------------------------------------------------------------------------------------------------------

const addUsage = `Usage: aiplam add <name> <folder> [-sp|--skills-path PATH] [-s|--set SET]

Registers the playbook in <folder> (for example '.') with the given name, and links its skills into the targets.
The name is the prefix of the playbook's artifacts: its skills are the folders named '<name>-*' holding a SKILL.md
in its skills folder. Several playbooks can share a name if their folders' last segments differ; their ids are then
'<name>@<last folder segment>'. The playbook is installed at whatever tag or commit is checked out.

Nothing is changed if any link cannot be created because something else is already in its place.

  -sp, --skills-path PATH   the skills folder, relative to <folder> (default: skills; '.' for <folder> itself)
  -s, --set SET             the configuration set to register it in (default: 'default')
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

	cfg, err := a.loadConfig(true)
	if err != nil {
		return err
	}
	current := playbook.Entries(cfg.Playbooks, a.Home)
	for _, e := range current {
		if e.Name != name {
			continue
		}
		if fsutil.SamePath(e.Dir, folder) {
			return fmt.Errorf("playbook '%s' is already registered for '%s' (id %s); use '%s'", name,
				a.pretty(folder), e.ID, a.cmd("update "+e.ID))
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
	updated := *cfg
	updated.Playbooks = append(append([]config.Playbook{}, cfg.Playbooks...), entry)
	final := playbook.Entries(updated.Playbooks, a.Home)
	e := final[len(final)-1]

	plan, err := playbook.Build(final, e, cfg.TargetDirs(a.Home))
	if err != nil {
		return err
	}
	if len(plan.Conflicts()) > 0 {
		a.printConflicts(plan)
		a.errorf("playbook '%s' was not added: remove or rename what is in the way, then try again.", e.ID)
		return errSilent
	}

	a.printf("Adding playbook %s (%s) from %s to set '%s'\n", e.ID, a.version(folder), a.pretty(folder), a.set())
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook was not registered: fix the problem and run '%s' again", err, a.cmd("add"))
	}
	if err := config.Save(a.Home, a.set(), &updated); err != nil {
		return a.undo(plan, err, "the playbook was not registered")
	}
	skills, _ := playbook.Skills(final, e)
	if len(skills) == 0 {
		a.warnf("no skills named '%s-*' (folders with a %s) were found in %s.", name, playbook.SkillFile,
			a.pretty(e.SkillsDir))
	}
	a.printf("Added %s with %d skill(s).\n", e.ID, len(skills))
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

Brings the installed artifacts of a playbook in line with its folder as currently checked out: links new skills and
removes the links of skills that no longer exist. The playbook is the one registered for the current folder, or the
one given by id, by name (if unique), by folder (if unique), or by name and folder. aiplam never fetches, pulls or
switches a checkout: select the version in the playbook's folder first.

Nothing is changed for a playbook if any link cannot be created because something else is already in its place.

  --all           update every playbook registered in the set
  -s, --set SET   the configuration set (default: 'default')
`

func (a *App) cmdUpdate(args []string) error {
	p, err := parseArgs(args, nil, []string{"all"})
	if err != nil {
		return err
	}
	if p.bools["all"] && len(p.positional) > 0 {
		return usagef("--all takes no other arguments")
	}
	cfg, err := a.loadConfig(true)
	if err != nil {
		return err
	}
	entries := playbook.Entries(cfg.Playbooks, a.Home)
	selected := entries
	if !p.bools["all"] {
		e, err := a.selectEntry(entries, p.positional)
		if err != nil {
			return err
		}
		selected = []playbook.Entry{e}
	} else if len(entries) == 0 {
		a.printf("No playbooks are registered in set '%s'.\n", a.set())
		return nil
	}

	failed := false
	for i, e := range selected {
		if i > 0 {
			a.printf("\n")
		}
		if err := a.updateOne(entries, e, cfg.TargetDirs(a.Home)); err != nil {
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

func (a *App) updateOne(entries []playbook.Entry, e playbook.Entry, targets []string) error {
	if err := a.checkPlaybookFolders(e); err != nil {
		return err
	}
	plan, err := playbook.Build(entries, e, targets)
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

Removes the links to a playbook's artifacts from the targets and unregisters it. The playbook is given by id, by
name (if unique), by folder (if unique), or by name and folder. Only links are removed: the playbook's folder and
anything that is not a link to it are left in place.

  -s, --set SET   the configuration set (default: 'default')
`

func (a *App) cmdRemove(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usagef("remove needs a name, id or folder")
	}
	cfg, err := a.loadConfig(true)
	if err != nil {
		return err
	}
	entries := playbook.Entries(cfg.Playbooks, a.Home)
	e, err := a.selectEntry(entries, p.positional)
	if err != nil {
		return err
	}
	updated := *cfg
	updated.Playbooks = nil
	for _, pb := range cfg.Playbooks {
		if !(pb.Name == e.Name && fsutil.SamePath(pb.Dir(a.Home), e.Dir)) {
			updated.Playbooks = append(updated.Playbooks, pb)
		}
	}
	final := playbook.Entries(updated.Playbooks, a.Home)
	plan, err := playbook.Build(final, e, cfg.TargetDirs(a.Home))
	if err != nil {
		return err
	}
	a.printf("Removing playbook %s (%s) from set '%s'\n", e.ID, a.pretty(e.Dir), a.set())
	if err := playbook.Apply(plan, a.printAction); err != nil {
		return fmt.Errorf("%w; the playbook is still registered: fix the problem and run '%s' again", err,
			a.cmd("remove"))
	}
	if err := config.Save(a.Home, a.set(), &updated); err != nil {
		return a.undo(plan, err, "the playbook is still registered")
	}
	a.printf("Removed %s.\n", e.ID)
	a.reportRenamedIDs(entries, final)
	return nil
}

// undo reverts the link changes of an applied plan after saving the configuration failed.
func (a *App) undo(plan playbook.Plan, saveErr error, state string) error {
	if err := playbook.Revert(plan.Changes()); err != nil {
		return fmt.Errorf("%w; undoing the link changes also failed (%v): run '%s' to see what is left", saveErr, err,
			a.cmd("check"))
	}
	return fmt.Errorf("%w; the link changes were undone and %s", saveErr, state)
}
