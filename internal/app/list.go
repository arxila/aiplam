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
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/gitinfo"
	"arxila.io/aiplam/internal/playbook"
)

const listUsage = `Usage: aiplam list [--levels] [--json] [--set SET]

Lists the playbooks registered in the enabled sets, or in the set given with --set (id, name, folder and checked-out
version: its tags, or else its branch) and, for each, the artifacts it provides. For enabled sets, it marks the
artifacts not installed in every agent folder ('aiplam update' installs them).

  --levels        also list the higher-level configurations, whose agent folders agents may read too: those of the
                  parents of the root folder, nearest first, and that of the home folder (when the root folder is
                  not inside it)
  --json          print the information as JSON, for other tools (with --levels, an array with one item per level)
  -s, --set SET   the set (default: the enabled sets)
`

type listOutput struct {
	Root    string    `json:"root"`
	Types   []string  `json:"types"`
	Folders []string  `json:"folders"`
	Sets    []listSet `json:"sets"`
}

type listSet struct {
	Name      string         `json:"name"`
	Enabled   bool           `json:"enabled"`
	Playbooks []listPlaybook `json:"playbooks"`
}

type listPlaybook struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Folder    string          `json:"folder"`
	Version   gitinfo.Version `json:"version"`
	Label     string          `json:"versionLabel"`
	Artifacts listArtifacts   `json:"artifacts"`
	Problems  []string        `json:"problems"`
}

type listArtifacts struct {
	Skills listSkills `json:"skills"`
}

type listSkills struct {
	Path   string      `json:"path"`
	Folder string      `json:"folder"`
	Items  []listSkill `json:"items"`
}

type listSkill struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Installed bool     `json:"installed"`
	MissingIn []string `json:"missingIn"`
}

func (a *App) cmdList(args []string) error {
	p, err := parseArgs(args, nil, []string{"json", "levels"})
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	levels, asJSON := p.bools["levels"], p.bools["json"]
	if levels && a.Set != "" {
		return usagef("--levels cannot be used with --set")
	}
	a.rootShown = true // the root folder is part of the listing
	roots := []string{a.Root}
	if levels {
		roots = append(roots, config.Ancestors(a.Root, a.Home)...)
	}
	outs := []listOutput{}
	notInstalled, failed := false, false
	root0, rootArg0, global0 := a.Root, a.rootArg, a.global
	for i, root := range roots {
		if i > 0 {
			// Commands suggested for a higher level must name its root folder.
			a.Root, a.rootArg, a.global = root, root, false
		}
		st, err := a.load()
		if err != nil && i == 0 {
			return err
		}
		if err != nil {
			// A higher level that cannot be read: report it and list the others.
			if !errors.Is(err, errSilent) {
				a.errorf("the configuration of '%s' cannot be listed: %s", a.pretty(root), err)
			}
			failed = true
			continue
		}
		out := listOutput{Root: root, Types: st.inst.Types, Folders: st.targets, Sets: []listSet{}}
		for _, set := range st.sets {
			out.Sets = append(out.Sets, a.listSet(set, st.targets))
		}
		outs = append(outs, out)
		if !asJSON {
			if i > 0 {
				a.printf("\n----------------------------------------\n")
			}
			if a.printListing(st, out) {
				notInstalled = true
			}
		}
	}
	a.Root, a.rootArg, a.global = root0, rootArg0, global0
	if asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if levels {
			if err := enc.Encode(outs); err != nil {
				return err
			}
		} else if err := enc.Encode(outs[0]); err != nil {
			return err
		}
		if failed {
			return errSilent
		}
		return nil
	}
	if notInstalled {
		a.printf("\nSome skills are not installed: run '%s', or '%s' for details (with --root for a higher level).\n",
			a.hint("update --all"), a.hint("check"))
	}
	if failed {
		return errSilent
	}
	return nil
}

// printListing prints the listing of a configuration and reports whether any skill of an enabled set is not
// installed in every agent folder.
func (a *App) printListing(st *state, out listOutput) bool {
	var folders []string
	for _, t := range out.Folders {
		folders = append(folders, a.pretty(t))
	}
	a.printf("Root: %s\nAgent folders: %s\n", a.pretty(out.Root), strings.Join(folders, ", "))
	if len(out.Sets) == 0 {
		a.printf("\nNo set is enabled: enable one with '%s' ('%s' lists them).\n", a.hint("set enable <set>"),
			a.hint("set list"))
		return false
	}
	notInstalled := false
	for i, ls := range out.Sets {
		set := st.sets[i]
		a.useSet(st, set)
		status := "enabled"
		if !ls.Enabled {
			status = "not enabled"
		}
		a.printf("\nSet %s (%s), %s\n", ls.Name, a.setFile(ls.Name), status)
		if !ls.Enabled {
			a.printf("Nothing of this set is installed: %s.\n", a.notEnabledNote(ls.Name))
		}
		if len(ls.Playbooks) == 0 {
			a.printf("No playbooks are registered. Register one with '%s'.\n", a.cmd("add <name> <folder>"))
			continue
		}
		if a.listPlaybooks(ls) {
			notInstalled = true
		}
	}
	a.hintSet = ""
	return notInstalled
}

// listPlaybooks prints a set's playbooks and reports whether any of their skills is not installed in every target.
func (a *App) listPlaybooks(ls listSet) bool {
	notInstalled := false
	for _, lp := range ls.Playbooks {
		a.printf("\n%s\n", lp.ID)
		a.printf("  name:    %s\n", lp.Name)
		a.printf("  folder:  %s\n", a.pretty(lp.Folder))
		a.printf("  version: %s\n", lp.Label)
		for _, problem := range lp.Problems {
			a.printf("  problem: %s\n", problem)
		}
		a.printf("  skills (%s):", lp.Artifacts.Skills.Path)
		if len(lp.Artifacts.Skills.Items) == 0 {
			a.printf(" none\n")
			continue
		}
		a.printf("\n")
		for _, s := range lp.Artifacts.Skills.Items {
			if s.Installed || !ls.Enabled {
				a.printf("    %s\n", s.Name)
				continue
			}
			notInstalled = true
			var where []string
			for _, t := range s.MissingIn {
				where = append(where, a.pretty(t))
			}
			a.printf("    %s  (not installed in %s)\n", s.Name, strings.Join(where, ", "))
		}
	}
	return notInstalled
}

// listSet gathers the information of a set's playbooks.
func (a *App) listSet(set *setState, targets []string) listSet {
	ls := listSet{Name: set.name, Enabled: set.enabled, Playbooks: []listPlaybook{}}
	for _, e := range set.entries {
		lp := listPlaybook{ID: e.ID, Name: e.Name, Folder: e.Dir, Problems: []string{},
			Artifacts: listArtifacts{Skills: listSkills{Path: e.SkillsPath(), Folder: e.SkillsDir,
				Items: []listSkill{}}}}
		switch {
		case !fsutil.IsDir(e.Dir):
			lp.Problems = append(lp.Problems, fmt.Sprintf("folder '%s' does not exist", e.Dir))
		case !fsutil.IsDir(e.SkillsDir):
			lp.Problems = append(lp.Problems, fmt.Sprintf("skills folder '%s' does not exist", e.SkillsDir))
		case !fsutil.Within(e.SkillsDir, e.Dir):
			lp.Problems = append(lp.Problems, fmt.Sprintf("skills folder '%s' is outside the playbook folder",
				e.SkillsDir))
		}
		if fsutil.IsDir(e.Dir) {
			lp.Version = a.version(e.Dir)
		}
		if lp.Version.Tags == nil {
			lp.Version.Tags = []string{}
		}
		lp.Label = lp.Version.String()
		if fsutil.IsDir(e.SkillsDir) {
			skills, err := playbook.Skills(set.entries, e)
			if err != nil {
				lp.Problems = append(lp.Problems, err.Error())
			}
			for _, s := range skills {
				item := listSkill{Name: s.Name, Source: s.Source, MissingIn: []string{}}
				for _, t := range targets {
					if !set.enabled {
						break // a set that is not enabled has nothing installed
					}
					state, current, err := fsutil.Inspect(filepath.Join(playbook.SkillsFolder(t), s.Name))
					if err != nil || state != fsutil.Link ||
						!fsutil.SamePath(fsutil.Canonical(current), fsutil.Canonical(s.Source)) {
						item.MissingIn = append(item.MissingIn, t)
					}
				}
				item.Installed = set.enabled && len(item.MissingIn) == 0
				lp.Artifacts.Skills.Items = append(lp.Artifacts.Skills.Items, item)
			}
		}
		ls.Playbooks = append(ls.Playbooks, lp)
	}
	return ls
}
