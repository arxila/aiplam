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

const listUsage = `Usage: aiplam list [--all] [--json] [--set SET]

Lists the playbooks of the enabled sets, and for each:
  - its id, name and playbook folder;
  - its version: the tags of the checked-out commit, or else its branch;
  - its artifacts, by kind (such as skills), marking those not linked from every agents configuration folder
    ('aiplam update' links them).

Options:
  --all           also list the higher-level base folders, nearest first:
                    - the parents of the base folder that are base folders as well: some agents read their
                      agents configuration folders too;
                    - the home folder, if it is also being used as a base folder for playbook management: agents
                      always read its agents configuration folders.
  --json          print JSON, for other tools (with --all, an array with one item per base folder)
  -s, --set SET   list set SET instead (default: the enabled sets)

Examples:
  aiplam list
  aiplam list --all
  aiplam list --set acme --json
`

type listOutput struct {
	Base             string    `json:"base"`
	Types            []string  `json:"types"`
	AgentsConfigDirs []string  `json:"agentsConfigurationFolders"`
	Sets             []listSet `json:"sets"`
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
	Linked    bool     `json:"linked"`
	MissingIn []string `json:"missingIn"`
}

func (a *App) cmdList(args []string) error {
	p, err := parseArgs(args, nil, []string{"json", "all"})
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	all, asJSON := p.bools["all"], p.bools["json"]
	if all && a.Set != "" {
		return usagef("--all cannot be used with --set")
	}
	a.baseShown = true // the base folder is part of the listing
	bases := []string{a.Base}
	if all {
		bases = append(bases, config.Ancestors(a.Base, a.Home)...)
	}
	outs := []listOutput{}
	notLinked, failed := false, false
	base0, baseArg0, global0 := a.Base, a.baseArg, a.global
	for i, base := range bases {
		if i > 0 {
			// Commands suggested for a higher-level base folder must name it.
			a.Base, a.baseArg, a.global = base, base, false
		}
		st, err := a.load()
		if err != nil && i == 0 {
			return err
		}
		if err != nil {
			// A higher-level base folder that cannot be read: report it and list the others.
			if !errors.Is(err, errSilent) {
				a.errorf("the configuration of base folder '%s' cannot be listed: %s", a.pretty(base), err)
			}
			failed = true
			continue
		}
		out := listOutput{Base: base, Types: st.conf.Types, AgentsConfigDirs: st.agentsConfigDirs, Sets: []listSet{}}
		for _, set := range st.sets {
			out.Sets = append(out.Sets, a.listSet(set, st.agentsConfigDirs))
		}
		outs = append(outs, out)
		if !asJSON {
			if i > 0 {
				a.printf("\n----------------------------------------\n")
			}
			if a.printListing(st, out) {
				notLinked = true
			}
		}
	}
	a.Base, a.baseArg, a.global = base0, baseArg0, global0
	if asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if all {
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
	if notLinked {
		a.printf("\nSome artifacts are not linked: run '%s', or '%s' for details (with --base for a higher-level "+
			"base folder).\n",
			a.hint("update --all"), a.hint("check"))
	}
	if failed {
		return errSilent
	}
	return nil
}

// printListing prints the listing of a configuration and reports whether any skill of an enabled set is not
// linked from every agents configuration folder.
func (a *App) printListing(st *state, out listOutput) bool {
	var folders []string
	for _, t := range out.AgentsConfigDirs {
		folders = append(folders, a.pretty(t))
	}
	a.printf("Base folder: %s\nAgents configuration folders: %s\n", a.pretty(out.Base), strings.Join(folders, ", "))
	if len(out.Sets) == 0 {
		a.printf("\nNo set is enabled: enable one with '%s' ('%s' lists them).\n", a.hint("set enable <set>"),
			a.hint("set list"))
		return false
	}
	notLinked := false
	for i, ls := range out.Sets {
		set := st.sets[i]
		a.useSet(st, set)
		status := "enabled"
		if !ls.Enabled {
			status = "not enabled"
		}
		a.printf("\nSet %s (%s), %s\n", ls.Name, a.setFile(ls.Name), status)
		if !ls.Enabled {
			a.printf("Nothing of this set is linked: %s.\n", a.notEnabledNote(ls.Name))
		}
		if len(ls.Playbooks) == 0 {
			a.printf("No playbooks are registered. Register one with '%s'.\n", a.cmd("add <name> <playbookfolder>"))
			continue
		}
		if a.listPlaybooks(ls) {
			notLinked = true
		}
	}
	a.hintSet = ""
	return notLinked
}

// listPlaybooks prints a set's playbooks and reports whether any of their artifacts is not linked from every agents
// configuration folder.
func (a *App) listPlaybooks(ls listSet) bool {
	notLinked := false
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
			if s.Linked || !ls.Enabled {
				a.printf("    %s\n", s.Name)
				continue
			}
			notLinked = true
			var where []string
			for _, t := range s.MissingIn {
				where = append(where, a.pretty(t))
			}
			a.printf("    %s  (not linked from %s)\n", s.Name, strings.Join(where, ", "))
		}
	}
	return notLinked
}

// listSet gathers the information of a set's playbooks.
func (a *App) listSet(set *setState, agentsConfigDirs []string) listSet {
	ls := listSet{Name: set.name, Enabled: set.enabled, Playbooks: []listPlaybook{}}
	for _, e := range set.entries {
		lp := listPlaybook{ID: e.ID, Name: e.Name, Folder: e.Dir, Problems: []string{},
			Artifacts: listArtifacts{Skills: listSkills{Path: e.SkillsPath(), Folder: e.SkillsDir,
				Items: []listSkill{}}}}
		switch {
		case !fsutil.IsDir(e.Dir):
			lp.Problems = append(lp.Problems, fmt.Sprintf("playbook folder '%s' does not exist", e.Dir))
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
				for _, t := range agentsConfigDirs {
					if !set.enabled {
						break // a set that is not enabled has nothing linked
					}
					state, current, err := fsutil.Inspect(filepath.Join(playbook.SkillsFolder(t), s.Name))
					if err != nil || state != fsutil.Link ||
						!fsutil.SamePath(fsutil.Canonical(current), fsutil.Canonical(s.Source)) {
						item.MissingIn = append(item.MissingIn, t)
					}
				}
				item.Linked = set.enabled && len(item.MissingIn) == 0
				lp.Artifacts.Skills.Items = append(lp.Artifacts.Skills.Items, item)
			}
		}
		ls.Playbooks = append(ls.Playbooks, lp)
	}
	return ls
}
