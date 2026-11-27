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
	"sort"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

const listUsage = `Usage: aiplam list [<folder> | --user]

Lists, for the managed folder that applies to <folder> (default: the current folder), or for the user managed folder
with --user, the artifacts linked in the target folders of its enabled profiles: as 'aiplam status', it first shows
whether it is the user managed folder and its git repository, and then, by profile (with its agent) and by kind (with
its target folder), each link:
  - the artifact's name;
  - the playbook it belongs to, and its version: the tags of the checked-out commit, or else its branch;
  - for skills, the version the skill declares, if any (metadata.version in its SKILL.md);
  - whether the link is broken.

Only the links that belong to a registered playbook are listed: anything else in the target folders (artifacts added
by hand, for example) is left out. Versions are shown for information only: aiplam links whatever is checked out.
'aiplam status' shows what 'aiplam update' would change. On Windows without developer mode or administrator rights,
it ends by saying how many artifacts are not linked because they are files ('aiplam status' lists them).

Examples:
  aiplam list
  aiplam list ~/Workspace/thymeleaf
  aiplam list --user
`

// listRow is a link listed by list.
type listRow struct {
	name, playbook, version, skillVersion string
	broken                                bool
}

func (a *App) cmdList(args []string) error {
	if err := a.resolveFrom(args); err != nil {
		return err
	}
	a.printHeader()
	st, err := a.loadState(true)
	if err != nil {
		return err
	}
	a.printProfiles(st.enabled)
	if len(st.enabled) == 0 {
		a.printf("\nNo profile is enabled: enable one with '%s' ('%s' lists them).\n",
			a.hint("profile enable <profile>"), a.hint("profile list"))
		return nil
	}
	versions := map[string]string{}
	version := func(e *playbook.Entry) string {
		if v, ok := versions[e.Dir]; ok {
			return v
		}
		v := "unknown"
		if fsutil.IsDir(e.Dir) {
			v = a.version(e.Dir).String()
		}
		versions[e.Dir] = v
		return v
	}
	// What update would do, to tell apart the artifacts that cannot be linked (files, on Windows without the rights),
	// which are not on disk.
	scope := map[playbook.Key]bool{}
	for _, s := range st.enabled {
		for _, e := range s.entries {
			scope[e.Key()] = true
		}
	}
	// What cannot be read is reported, never shown as nothing linked (status tells more).
	failed := false
	plan, err := a.transition(st.enabled, st.enabled, scope)
	planFailed := err != nil
	if err != nil {
		a.warnf("%s", err)
		failed = true
		plan = playbook.Plan{}
	}
	count := func(kind playbook.ActionKind, profile string) int {
		n := 0
		for _, act := range plan.Actions {
			if act.Kind == kind && act.Profile == profile {
				n++
			}
		}
		return n
	}

	for _, s := range st.enabled {
		a.printf("\nProfile %s (agent %s)\n", s.name, s.prof.Agent)
		linked, unread := false, false
		for _, t := range s.targets {
			items, err := os.ReadDir(t.Dir)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					a.warnf("cannot read target folder '%s': %s", a.pretty(t.Dir), err)
					failed, unread = true, true
				}
				continue
			}
			var rows []listRow
			for _, item := range items {
				link := filepath.Join(t.Dir, item.Name())
				state, current, err := fsutil.Inspect(link)
				// A link that cannot be resolved, but that may be one of a playbook's (as when its playbook folder
				// cannot be read), is reported; one that cannot be any playbook's is left to status.
				if err != nil && state == fsutil.Link {
					if e := playbook.MayOwner(s.entries, t.Kind, item.Name(), current); e != nil {
						a.warnf("cannot check %s, which may be a link of playbook %s: %s", a.pretty(link), e.Label(),
							err)
						failed, unread = true, true
					}
				}
				if err != nil || state != fsutil.Link {
					continue
				}
				e := playbook.Owner(s.entries, t.Kind, item.Name(), current)
				if e == nil {
					continue
				}
				row := listRow{name: item.Name(), playbook: e.ID, version: version(e)}
				if _, err := os.Stat(current); err != nil {
					row.broken = true
				} else if t.Kind == config.Skills {
					row.skillVersion = playbook.MetadataVersion(filepath.Join(current, "SKILL.md"))
				}
				rows = append(rows, row)
			}
			if len(rows) == 0 {
				continue
			}
			linked = true
			sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
			width := 0
			for _, r := range rows {
				width = max(width, len(r.name))
			}
			a.printf("  %s (%s):\n", t.Kind, a.pretty(t.Dir))
			for _, r := range rows {
				line := fmt.Sprintf("    %-*s  playbook %s (%s)", width, r.name, r.playbook, r.version)
				if r.skillVersion != "" {
					line += ", skill version " + r.skillVersion
				}
				if r.broken {
					line += "  BROKEN LINK"
				}
				a.printf("%s\n", line)
			}
		}
		switch {
		case linked:
		case unread || planFailed:
			a.printf("  links that cannot be checked are not listed (see above)\n")
		case len(s.entries) == 0:
			a.printf("  no playbooks are registered: register one with '%s'\n",
				a.withProfile(st, s.name, "add <name> <playbookfolder>"))
		case count(playbook.Skip, s.name) > 0 && count(playbook.Create, s.name) == 0:
			a.printf("  nothing is linked: its playbooks' artifacts are files, which cannot be linked here (see below)\n")
		default:
			a.printf("  nothing is linked ('%s' shows what '%s' would link)\n", a.hint("status"),
				a.withProfile(st, s.name, "update <playbook>"))
		}
	}

	if len(plan.Skipped()) > 0 {
		a.printf("\n%d artifact(s) are not linked, as they are files and, on Windows, links to files need developer "+
			"mode or administrator rights ('%s' lists them).\n", len(plan.Skipped()), a.hint("status"))
	}
	if failed {
		return errSilent
	}
	return nil
}
