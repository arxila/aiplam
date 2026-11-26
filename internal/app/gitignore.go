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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/playbook"
)

// ownedLink is a link in a target folder that belongs to a playbook.
type ownedLink struct {
	path   string // the link
	folder string // its target folder, as configured (relative to the managed folder)
	format string // the format of its playbook for the kind
}

// ownedLinks returns the links in the target folders of profiles that belong to their playbooks; when only is not nil,
// just those whose paths are in it.
func ownedLinks(profiles []*profileState, only map[string]bool) []ownedLink {
	var links []ownedLink
	for _, s := range profiles {
		for _, t := range s.targets {
			items, err := os.ReadDir(t.Dir)
			if err != nil {
				continue
			}
			for _, item := range items {
				link := filepath.Join(t.Dir, item.Name())
				if only != nil && !only[link] {
					continue
				}
				state, current, err := fsutil.Inspect(link)
				if err != nil || state != fsutil.Link {
					continue
				}
				if e := playbook.Owner(s.entries, t.Kind, item.Name(), current); e != nil {
					links = append(links, ownedLink{path: link, folder: t.Folder, format: e.Format(t.Kind)})
				}
			}
		}
	}
	return links
}

// warnUnignored warns about the links, among links, that the git repository holding the managed folder does not
// ignore (none when the managed folder is not in a git repository): links belong to each machine, so they must not be
// committed. The warning gives the lines to add to the managed folder's .gitignore (aiplam never writes it) and lists
// the links already committed. When git cannot check them (it is not in the PATH, or it fails), it says so only with
// failures set. It reports whether it warned.
func (a *App) warnUnignored(links []ownedLink, failures bool) bool {
	repo := config.GitRepository(a.Managed)
	if repo == "" {
		return false
	}
	var inRepo []string
	byPath := map[string]ownedLink{}
	for _, l := range links {
		if fsutil.Within(l.path, repo) { // not those under %APPDATA%
			inRepo = append(inRepo, l.path)
			byPath[l.path] = l
		}
	}
	if len(inRepo) == 0 {
		return false
	}
	unignored, tracked, err := a.Git.Unignored(repo, inRepo)
	if err != nil {
		if failures {
			a.warnf("cannot check whether the links that aiplam made are ignored by git (%s), so they could be "+
				"committed by mistake: links belong to each machine, and must be ignored by git.", err)
		}
		return failures
	}
	if len(unignored) == 0 {
		return false
	}
	var lines []string
	seen := map[string]bool{}
	for _, p := range unignored {
		l := byPath[p]
		if line := "/" + l.folder + "/" + gitignorePattern(l.format); !seen[line] {
			seen[line] = true
			lines = append(lines, line)
		}
	}
	var b strings.Builder
	b.WriteString(plural(len(unignored), "link", "links") + " that aiplam made " + verb(len(unignored), "is", "are") +
		" not ignored by git, so " + verb(len(unignored), "it", "they") + " could be committed; but links belong to " +
		"each machine (they lead to where each one keeps its playbooks, and a Windows clone cannot use them). Add " +
		"these lines to '" + a.pretty(filepath.Join(a.Managed, ".gitignore")) + "' (aiplam never changes it):")
	for _, line := range lines {
		b.WriteString("\n  " + line)
	}
	var committed []string
	for _, p := range unignored {
		if tracked[p] {
			committed = append(committed, p)
		}
	}
	if len(committed) > 0 {
		b.WriteString("\n" + plural(len(committed), "link is", "links are") + " already committed: stop tracking " +
			verb(len(committed), "it", "them") + " with 'git rm --cached' (the links stay in place):")
		for _, p := range committed {
			b.WriteString("\n  " + a.pretty(p))
		}
	}
	a.warnf("%s", b.String())
	return true
}

// warnUnignoredCreated warns (see warnUnignored) about the links a plan created, among the links of profiles.
func (a *App) warnUnignoredCreated(plan playbook.Plan, profiles []*profileState) {
	created := map[string]bool{}
	for _, act := range plan.Changes() {
		if act.Kind == playbook.Create {
			created[act.Link] = true
		}
	}
	if len(created) > 0 {
		a.warnUnignored(ownedLinks(profiles, created), false)
	}
}

// gitignorePattern returns a gitignore pattern matching the names a format matches: its '*' stay wildcards, and the
// characters that are special in gitignore patterns are escaped.
func gitignorePattern(format string) string {
	var b strings.Builder
	for _, r := range format {
		if strings.ContainsRune(`\?[]`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// plural returns "<n> <one>" or "<n> <many>".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// verb returns one when n is 1, and many otherwise.
func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
