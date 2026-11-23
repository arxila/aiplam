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
	"path/filepath"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/gitinfo"
	"arxila.io/aiplam/internal/playbook"
)

const listUsage = `Usage: aiplam list [--all] [--json] [--profile PROFILE]

Lists the enabled profiles, with their agents, and the playbooks of each, with:
  - its id, name and playbook folder;
  - its version: the tags of the checked-out commit, or else its branch;
  - its artifacts that the profile's agent reads, by kind (such as skills), marking those not linked ('aiplam
    update' links them).

Options:
  --all                   also list the higher-level base folders, nearest first:
                            - the parents of the base folder that are base folders as well: some agents read
                              their agents configuration folders too;
                            - the home folder, if it is also being used as a base folder for playbook
                              management: agents always read its agents configuration folders.
  --json                  print JSON, for other tools: each profile with its agent, and each playbook's artifacts by
                          kind, with the folder they are linked from (with --all, an array with one item per base
                          folder)
  -p, --profile PROFILE   list profile PROFILE instead (default: the enabled profiles)

Examples:
  aiplam list
  aiplam list --all
  aiplam list --profile acme --json
`

type listOutput struct {
	Base     string        `json:"base"`
	Profiles []listProfile `json:"profiles"`
}

type listProfile struct {
	Name      string         `json:"name"`
	Enabled   bool           `json:"enabled"`
	Agent     string         `json:"agent"`
	Playbooks []listPlaybook `json:"playbooks"`
}

type listPlaybook struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Folder    string          `json:"folder"`
	Version   gitinfo.Version `json:"version"`
	Label     string          `json:"versionLabel"`
	Artifacts []listKind      `json:"artifacts"`
	Problems  []string        `json:"problems"`
}

// listKind is a kind of artifact that the profile's agent reads.
type listKind struct {
	Kind string `json:"kind"`
	// Path is the playbook's folder for the kind, relative to its playbook folder, and Folder the same, resolved.
	Path   string `json:"path"`
	Folder string `json:"folder"`
	// LinkFolder is the folder where the agent reads the kind, where the artifacts are linked.
	LinkFolder string     `json:"linkFolder"`
	Items      []listItem `json:"items"`
}

type listItem struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Linked bool   `json:"linked"`
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
	if all && a.Profile != "" {
		return usagef("--all cannot be used with --profile")
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
		out := listOutput{Base: base, Profiles: []listProfile{}}
		for _, profile := range st.profiles {
			out.Profiles = append(out.Profiles, a.listProfile(profile))
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

// printListing prints the listing of a configuration and reports whether any artifact of an enabled profile is not
// linked.
func (a *App) printListing(st *state, out listOutput) bool {
	a.printf("Base folder: %s\n", a.pretty(out.Base))
	if len(out.Profiles) == 0 {
		a.printf("\nNo profile is enabled: enable one with '%s' ('%s' lists them).\n", a.hint("profile enable <profile>"),
			a.hint("profile list"))
		return false
	}
	notLinked := false
	for i, lprof := range out.Profiles {
		profile := st.profiles[i]
		a.useProfile(st, profile)
		status := "enabled"
		if !lprof.Enabled {
			status = "not enabled"
		}
		a.printf("\nProfile %s (%s), %s, agent %s (%s): linked from %s\n", lprof.Name, a.profileFile(lprof.Name),
			status, profile.agent.Name, profile.agent.Product, a.folderList(profile))
		if !lprof.Enabled {
			a.printf("Nothing of this profile is linked: %s.\n", a.notEnabledNote(lprof.Name))
		}
		if len(lprof.Playbooks) == 0 {
			a.printf("No playbooks are registered. Register one with '%s'.\n", a.cmd("add <name> <playbookfolder>"))
			continue
		}
		if a.listPlaybooks(lprof) {
			notLinked = true
		}
	}
	a.hintProfile = ""
	return notLinked
}

// listPlaybooks prints a profile's playbooks and reports whether any of their artifacts is not linked.
func (a *App) listPlaybooks(lprof listProfile) bool {
	notLinked := false
	for _, lp := range lprof.Playbooks {
		a.printf("\n%s\n", lp.ID)
		a.printf("  name:    %s\n", lp.Name)
		a.printf("  folder:  %s\n", a.pretty(lp.Folder))
		a.printf("  version: %s\n", lp.Label)
		for _, problem := range lp.Problems {
			a.printf("  problem: %s\n", problem)
		}
		shown := false
		for _, lk := range lp.Artifacts {
			if len(lk.Items) == 0 && lk.Kind != string(config.Skills) {
				continue
			}
			shown = true
			a.printf("  %s (%s):", lk.Kind, lk.Path)
			if len(lk.Items) == 0 {
				a.printf(" none\n")
				continue
			}
			a.printf("\n")
			for _, it := range lk.Items {
				if it.Linked || !lprof.Enabled {
					a.printf("    %s\n", it.Name)
					continue
				}
				notLinked = true
				a.printf("    %s  (not linked from %s)\n", it.Name, a.pretty(lk.LinkFolder))
			}
		}
		if !shown {
			a.printf("  artifacts: none for agent %s\n", lprof.Agent)
		}
	}
	return notLinked
}

// listProfile gathers the information of a profile's playbooks.
func (a *App) listProfile(profile *profileState) listProfile {
	lprof := listProfile{Name: profile.name, Enabled: profile.enabled, Agent: profile.agent.Name,
		Playbooks: []listPlaybook{}}
	for _, e := range profile.entries {
		lp := listPlaybook{ID: e.ID, Name: e.Name, Folder: e.Dir, Problems: []string{}, Artifacts: []listKind{}}
		if err := a.checkPlaybookFolders(e, profile.folders); err != nil {
			lp.Problems = append(lp.Problems, err.Error())
		}
		if fsutil.IsDir(e.Dir) {
			lp.Version = a.version(e.Dir)
		}
		if lp.Version.Tags == nil {
			lp.Version.Tags = []string{}
		}
		lp.Label = lp.Version.String()
		for _, f := range profile.folders {
			lk := listKind{Kind: string(f.Kind), Path: e.Path(f.Kind, f.DefaultPath), Folder: e.KindDir(f), LinkFolder: f.Dir,
				Items: []listItem{}}
			items, err := playbook.Items(profile.entries, e, f)
			if err != nil {
				lp.Problems = append(lp.Problems, err.Error())
			}
			for _, it := range items {
				linked := false
				if profile.enabled {
					state, current, err := fsutil.Inspect(filepath.Join(f.Dir, it.Name))
					linked = err == nil && state == fsutil.Link &&
						fsutil.SamePath(fsutil.Canonical(current), fsutil.Canonical(it.Source))
				}
				lk.Items = append(lk.Items, listItem{Name: it.Name, Source: it.Source, Linked: linked})
			}
			lp.Artifacts = append(lp.Artifacts, lk)
		}
		lprof.Playbooks = append(lprof.Playbooks, lp)
	}
	return lprof
}
