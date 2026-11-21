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
	"fmt"
	"path/filepath"
	"strings"

	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/gitinfo"
	"arxila.io/aiplam/internal/playbook"
)

const listUsage = `Usage: aiplam list [--json] [-s|--set SET]

Lists the playbooks registered in a set (id, name, folder and checked-out version: its tags, or else its branch) and,
for each, the artifacts it provides, marking those not installed in every target ('aiplam update' installs them).

  --json          print the information as JSON, for other tools
  -s, --set SET   the configuration set (default: 'default')
`

type listOutput struct {
	Set       string         `json:"set"`
	Targets   []string       `json:"targets"`
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
	p, err := parseArgs(args, nil, []string{"json"})
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	cfg, err := a.loadConfig(false)
	if err != nil {
		return err
	}
	targets := cfg.TargetDirs(a.Home)
	entries := playbook.Entries(cfg.Playbooks, a.Home)

	out := listOutput{Set: a.set(), Targets: targets, Playbooks: []listPlaybook{}}
	for _, e := range entries {
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
			skills, err := playbook.Skills(entries, e)
			if err != nil {
				lp.Problems = append(lp.Problems, err.Error())
			}
			for _, s := range skills {
				item := listSkill{Name: s.Name, Source: s.Source, MissingIn: []string{}}
				for _, t := range targets {
					state, current, err := fsutil.Inspect(filepath.Join(playbook.SkillsFolder(t), s.Name))
					if err != nil || state != fsutil.Link ||
						!fsutil.SamePath(fsutil.Canonical(current), fsutil.Canonical(s.Source)) {
						item.MissingIn = append(item.MissingIn, t)
					}
				}
				item.Installed = len(item.MissingIn) == 0
				lp.Artifacts.Skills.Items = append(lp.Artifacts.Skills.Items, item)
			}
		}
		out.Playbooks = append(out.Playbooks, lp)
	}

	if p.bools["json"] {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	var prettyTargets []string
	for _, t := range targets {
		prettyTargets = append(prettyTargets, a.pretty(t))
	}
	a.printf("Set %s (%s), targets: %s\n", a.set(), a.configFile(), strings.Join(prettyTargets, ", "))
	if len(out.Playbooks) == 0 {
		a.printf("\nNo playbooks are registered. Register one with '%s'.\n", a.cmd("add <name> <folder>"))
		return nil
	}
	notInstalled := false
	for _, lp := range out.Playbooks {
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
			if s.Installed {
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
	if notInstalled {
		a.printf("\nSome skills are not installed: run '%s', or '%s' for details.\n", a.cmd("update --all"),
			a.cmd("check"))
	}
	return nil
}
