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

// Package playbook computes playbook ids, discovers the artifacts (skills) of each registered playbook, and plans the
// link changes needed to add, update or remove a playbook in the agents configuration folders.
package playbook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
)

// SkillFile is the file that makes a folder a skill.
const SkillFile = "SKILL.md"

// Entry is a registered playbook with its computed id and resolved skills folder.
type Entry struct {
	config.Playbook
	// ID is the name, or "name@<last folder segment>" when several playbooks of its set share the name.
	ID string
	// Set is the set the playbook is registered in.
	Set string
	// Dir is the playbook folder, resolved against the base folder, with links resolved.
	Dir string
	// SkillsDir is the folder holding the playbook's skills, with links resolved.
	SkillsDir string
}

// Entries builds the entries of the playbooks of a set, computing their ids and resolving their folders against the
// base folder.
func Entries(set string, playbooks []config.Playbook, base string) []Entry {
	count := map[string]int{}
	for _, p := range playbooks {
		count[p.Name]++
	}
	entries := make([]Entry, len(playbooks))
	for i, p := range playbooks {
		id, dir := p.Name, p.Dir(base)
		if count[p.Name] > 1 {
			id = p.Name + "@" + filepath.Base(dir)
		}
		entries[i] = Entry{Playbook: p, ID: id, Set: set, Dir: dir, SkillsDir: p.SkillsDir(base)}
	}
	return entries
}

// Skill is a skill provided by a playbook.
type Skill struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// candidateSkills lists the skill folders (folders with a SKILL.md) in dir whose name starts with prefix + "-".
func candidateSkills(dir, prefix string) ([]Skill, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, item := range items {
		name := item.Name()
		if !strings.HasPrefix(name, prefix+"-") || len(name) == len(prefix)+1 {
			continue
		}
		src := filepath.Join(dir, name)
		if fsutil.IsDir(src) && fsutil.IsFile(filepath.Join(src, SkillFile)) {
			skills = append(skills, Skill{Name: name, Source: src})
		}
	}
	return skills, nil
}

// Owner returns the entry, among entries, that owns an artifact named name found in skillsDir: the entry with that
// skills folder whose name is the longest prefix of the artifact's name (followed by '-'). It returns nil when no
// entry matches.
func Owner(entries []Entry, skillsDir, name string) *Entry {
	var owner *Entry
	for i := range entries {
		e := &entries[i]
		if !fsutil.SamePath(e.SkillsDir, skillsDir) || !strings.HasPrefix(name, e.Name+"-") {
			continue
		}
		if owner == nil || len(e.Name) > len(owner.Name) {
			owner = e
		}
	}
	return owner
}

// Skills returns the skills that belong to e when entries (which must include e) are registered: the skill folders
// in its skills folder named "<name>-...", except those claimed by an entry with a longer matching name on the same
// skills folder, and those that are links leading outside the playbook folder. It fails when the skills folder
// cannot be read.
func Skills(entries []Entry, e Entry) ([]Skill, error) {
	candidates, err := candidateSkills(e.SkillsDir, e.Name)
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, s := range candidates {
		if !fsutil.Within(fsutil.Canonical(s.Source), e.Dir) {
			continue
		}
		if owner := Owner(entries, e.SkillsDir, s.Name); owner != nil && owner.ID == e.ID {
			skills = append(skills, s)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// ActionKind is the kind of a planned change.
type ActionKind int

const (
	// Keep: the link exists and points to the right skill.
	Keep ActionKind = iota
	// Create: the link is missing and will be created.
	Create
	// Remove: the link points into the playbook but its skill no longer exists or belongs to it; it will be removed.
	Remove
	// Conflict: something else exists where the link must go; the operation must be cancelled.
	Conflict
)

// ConflictCause says why an action is a Conflict.
type ConflictCause int

const (
	// NotLink: something that is not a link exists where the link must go.
	NotLink ConflictCause = iota + 1
	// OtherLink: a link to another folder (Action.Current) exists where the link must go.
	OtherLink
	// OtherProvider: other registered playbooks (Action.Others) provide an artifact with the same name.
	OtherProvider
	// Exists: the link already exists, even if it is right; enabling a set requires every one of its links to be new.
	Exists
	// Ancestor: an artifact with the same name exists in an agents configuration folder of a higher-level base folder
	// (Action.Current), which agents may read too.
	Ancestor
)

// Action is one planned change for one artifact in one agents configuration folder.
type Action struct {
	Kind ActionKind
	// Kind of artifact ("skill").
	Artifact string
	// Name of the artifact.
	Name string
	// Link is the path of the link in the agents configuration folder.
	Link string
	// Source is the folder the link points (or must point) to.
	Source string
	// Reason explains a Remove.
	Reason string
	// Cause explains a Conflict.
	Cause ConflictCause
	// Current is the target of the link found in the way, for an OtherLink conflict, or the artifact found in a
	// higher-level agents configuration folder, for an Ancestor conflict.
	Current string
	// Others are the ids of the other playbooks providing the artifact, for an OtherProvider conflict.
	Others []string
}

// Plan is the set of actions for one playbook.
type Plan struct {
	Entry   Entry
	Actions []Action
}

// Conflicts returns the conflicting actions.
func (p Plan) Conflicts() []Action { return p.filter(Conflict) }

// Changes returns the actions that would change something (creations and removals).
func (p Plan) Changes() []Action {
	return append(p.filter(Create), p.filter(Remove)...)
}

func (p Plan) filter(kind ActionKind) []Action {
	var out []Action
	for _, a := range p.Actions {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

// SkillsFolder returns the folder of an agents configuration folder where skills are linked from.
func SkillsFolder(agentsConfigDir string) string {
	return filepath.Join(agentsConfigDir, "skills")
}

// Build plans the changes for playbook e in the given agents configuration folders, for the state in which final
// are the registered entries of e's set and others are the entries of the other enabled sets, one slice per set.
// When e is in final (by name and folder), its artifacts must be linked (add or update); otherwise e is being
// removed and all of its links go. Links that point into e's skills folder and are named after e are removed unless
// some entry in final or others wants exactly that link. A desired link whose path holds anything other than a link
// to the same artifact is a conflict, as is an artifact name that another playbook of final or others also provides.
func Build(final []Entry, e Entry, agentsConfigDirs []string, others [][]Entry) (Plan, error) {
	return build(final, e, agentsConfigDirs, nil, others, false)
}

// build is Build for a state in which the links in gone have already been removed. With strict, a desired link that
// already exists is a conflict even when it is right.
func build(final []Entry, e Entry, agentsConfigDirs []string, gone map[string]bool, others [][]Entry,
	strict bool) (Plan, error) {
	plan := Plan{Entry: e}
	inspect := func(link string) (fsutil.LinkState, string, error) {
		if gone[link] {
			return fsutil.Missing, "", nil
		}
		return fsutil.Inspect(link)
	}

	present := false
	for _, f := range final {
		if f.Name == e.Name && fsutil.SamePath(f.Dir, e.Dir) {
			present = true
			e = f
			break
		}
	}

	// What every entry in the final state (of e's set and of the other enabled sets) wants linked, by artifact name.
	wanted := map[string][]Skill{}
	wantedBy := map[string][]string{}
	for i, group := range append([][]Entry{final}, others...) {
		for _, f := range group {
			if !fsutil.IsDir(f.SkillsDir) {
				continue
			}
			skills, err := Skills(group, f)
			if err != nil {
				return plan, err
			}
			label := f.ID
			if i > 0 {
				label = fmt.Sprintf("%s of set %s", f.ID, f.Set)
			}
			for _, s := range skills {
				wanted[s.Name] = append(wanted[s.Name], s)
				wantedBy[s.Name] = append(wantedBy[s.Name], label)
			}
		}
	}

	var mine []Skill
	if present {
		if !fsutil.IsDir(e.SkillsDir) {
			return plan, fmt.Errorf("playbook %s: skills folder '%s' does not exist", e.ID, e.SkillsDir)
		}
		if !fsutil.Within(e.SkillsDir, e.Dir) {
			return plan, fmt.Errorf("playbook %s: skills folder '%s' is outside the playbook folder '%s'", e.ID,
				e.SkillsDir, e.Dir)
		}
		skills, err := Skills(final, e)
		if err != nil {
			return plan, err
		}
		mine = skills
	}
	mineByName := map[string]Skill{}
	for _, s := range mine {
		mineByName[s.Name] = s
	}

	for _, agentsConfigDir := range agentsConfigDirs {
		dir := SkillsFolder(agentsConfigDir)
		for _, s := range mine {
			link := filepath.Join(dir, s.Name)
			action := Action{Artifact: "skill", Name: s.Name, Link: link, Source: s.Source}
			if others := otherProviders(wantedBy[s.Name], e.ID); len(others) > 0 {
				action.Kind, action.Cause, action.Others = Conflict, OtherProvider, others
				plan.Actions = append(plan.Actions, action)
				continue
			}
			state, current, err := inspect(link)
			if err != nil {
				return plan, err
			}
			switch {
			case state == fsutil.Missing:
				action.Kind = Create
			case state == fsutil.Link && sameFolder(current, s.Source) && strict:
				action.Kind, action.Cause = Conflict, Exists
			case state == fsutil.Link && sameFolder(current, s.Source):
				action.Kind = Keep
			case state == fsutil.Link:
				action.Kind, action.Cause, action.Current = Conflict, OtherLink, current
			default:
				action.Kind, action.Cause = Conflict, NotLink
			}
			plan.Actions = append(plan.Actions, action)
		}

		items, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return plan, err
		}
		for _, item := range items {
			name := item.Name()
			link := filepath.Join(dir, name)
			if _, ok := mineByName[name]; ok || gone[link] || !strings.HasPrefix(name, e.Name+"-") {
				continue
			}
			state, current, err := fsutil.Inspect(link)
			if err != nil || state != fsutil.Link ||
				!fsutil.SamePath(fsutil.Canonical(filepath.Dir(current)), e.SkillsDir) {
				continue
			}
			if wantedExactly(wanted[name], current) {
				continue // Another registered playbook (for example, one with a longer name) owns it.
			}
			reason := "no longer in the playbook"
			if !present {
				reason = "playbook removed"
			} else if !fsutil.IsDir(current) {
				reason = "broken link: its skill no longer exists"
			}
			plan.Actions = append(plan.Actions, Action{Kind: Remove, Artifact: "skill", Name: name, Link: link,
				Source: current, Reason: reason})
		}
	}
	return plan, nil
}

// Overlaps returns, for each skill of e that other entries in final also provide, the ids of those entries. It
// ignores entries whose skills folder does not exist.
func Overlaps(final []Entry, e Entry) (map[string][]string, error) {
	providers := map[string][]string{}
	for _, f := range final {
		if !fsutil.IsDir(f.SkillsDir) {
			continue
		}
		skills, err := Skills(final, f)
		if err != nil {
			return nil, err
		}
		for _, s := range skills {
			providers[s.Name] = append(providers[s.Name], f.ID)
		}
	}
	overlaps := map[string][]string{}
	for name, ids := range providers {
		if others := otherProviders(ids, e.ID); len(others) > 0 && len(others) < len(ids) {
			overlaps[name] = others
		}
	}
	return overlaps, nil
}

// Switch plans the move from one linked state to another. Each argument holding entries has one slice per set:
// the links of the sets in from are removed from the agents configuration folders fromAgentsConfigDirs, the sets in
// to are linked from toAgentsConfigDirs, and the sets in stay remain linked (their links are neither removed nor
// shared). Links that from and to both want (same path, same skill) are left alone. Conflicts are planned as in
// Build, except that links being removed are not in the way; with strict, any other link already in place of a link
// to create is a conflict. Entries of to must have their skills folders. dropReason explains the removals of links
// in agents configuration folders that are no longer used; reason explains the others.
func Switch(from, to, stay [][]Entry, fromAgentsConfigDirs, toAgentsConfigDirs []string, reason, dropReason string,
	strict bool) (Plan, error) {
	var plan Plan
	inNewAgentsConfigDir := func(link string) bool {
		for _, t := range toAgentsConfigDirs {
			if fsutil.SamePath(SkillsFolder(t), filepath.Dir(link)) {
				return true
			}
		}
		return false
	}
	removed := map[string]Action{}
	var removals []Action
	for _, group := range from {
		for _, e := range group {
			p, err := build(nil, e, fromAgentsConfigDirs, nil, stay, false)
			if err != nil {
				return plan, err
			}
			for _, a := range p.Actions {
				if _, dup := removed[a.Link]; a.Kind == Remove && !dup {
					a.Reason = reason
					if !inNewAgentsConfigDir(a.Link) {
						a.Reason = dropReason
					}
					removed[a.Link] = a
					removals = append(removals, a)
				}
			}
		}
	}
	gone := map[string]bool{}
	for link := range removed {
		gone[link] = true
	}
	var creations []Action
	kept := map[string]bool{}
	for i, group := range to {
		others := append([][]Entry{}, stay...)
		for j, g := range to {
			if j != i {
				others = append(others, g)
			}
		}
		for _, e := range group {
			p, err := build(group, e, toAgentsConfigDirs, gone, others, strict)
			if err != nil {
				return plan, err
			}
			for _, a := range p.Actions {
				if r, ok := removed[a.Link]; ok && a.Kind == Create && sameFolder(r.Source, a.Source) {
					kept[a.Link] = true // wanted by both states: leave it
					continue
				}
				if a.Kind == Remove {
					// Playbooks of one folder with overlapping names (x, x-y) can both plan removing a link.
					if _, dup := removed[a.Link]; dup {
						continue
					}
					removed[a.Link] = a
					gone[a.Link] = true
				}
				if a.Kind != Keep {
					creations = append(creations, a)
				}
			}
		}
	}
	for _, r := range removals {
		if !kept[r.Link] {
			plan.Actions = append(plan.Actions, r)
		}
	}
	plan.Actions = append(plan.Actions, creations...)
	return plan, nil
}

func otherProviders(ids []string, self string) []string {
	var others []string
	for _, id := range ids {
		if id != self {
			others = append(others, id)
		}
	}
	return others
}

func wantedExactly(skills []Skill, source string) bool {
	for _, s := range skills {
		if sameFolder(s.Source, source) {
			return true
		}
	}
	return false
}

// MarkAncestors turns into Ancestor conflicts the creations of a plan whose artifact name already exists in the skills
// folder of an agents configuration folder of a higher-level base folder, so that agents reading several levels
// never see an artifact twice. ancestors maps each agents configuration folder of the plan to the
// higher-level agents configuration folders of the same type.
func MarkAncestors(plan *Plan, ancestors map[string][]string) {
	for i, act := range plan.Actions {
		if act.Kind != Create {
			continue
		}
		dir := filepath.Dir(filepath.Dir(act.Link)) // the link is in an agents configuration folder's skills folder
		for _, t := range ancestors[dir] {
			path := filepath.Join(SkillsFolder(t), act.Name)
			if _, err := os.Lstat(path); err == nil {
				plan.Actions[i].Kind, plan.Actions[i].Cause, plan.Actions[i].Current = Conflict, Ancestor, path
				break
			}
		}
	}
}

// sameFolder reports whether two paths lead to the same place once links are resolved, so that a link made through
// a path that has since become a link (for example, a playbook moved and linked from its old location) still counts.
func sameFolder(a, b string) bool {
	return fsutil.SamePath(fsutil.Canonical(a), fsutil.Canonical(b))
}

// Apply performs the creations and removals of a plan that has no conflicts, creating the skills folders of the
// agents configuration folders when needed. It is all-or-nothing: when a change fails, the changes already made are
// undone (and report has been called for them) before returning the error.
func Apply(plan Plan, report func(Action)) error {
	if len(plan.Conflicts()) > 0 {
		return errors.New("the plan has conflicts")
	}
	var done []Action
	for _, a := range plan.Actions {
		var err error
		switch a.Kind {
		case Create:
			if err = os.MkdirAll(filepath.Dir(a.Link), 0o755); err == nil {
				err = fsutil.CreateLink(a.Link, a.Source)
			}
		case Remove:
			err = fsutil.RemoveLink(a.Link)
		default:
			continue
		}
		if err != nil {
			if rerr := Revert(done); rerr != nil {
				return fmt.Errorf("%w; undoing the changes already made also failed: %v", err, rerr)
			}
			if len(done) > 0 {
				return fmt.Errorf("%w; the changes already made were undone", err)
			}
			return err
		}
		done = append(done, a)
		if report != nil {
			report(a)
		}
	}
	return nil
}

// Revert undoes creations and removals applied by Apply, last first: it removes the created links (only if they are
// still links to their source) and recreates the removed ones (only if nothing has taken their place). It tries every
// action and returns the first error.
func Revert(actions []Action) error {
	var first error
	for i := len(actions) - 1; i >= 0; i-- {
		a := actions[i]
		var err error
		switch a.Kind {
		case Create:
			if state, current, ierr := fsutil.Inspect(a.Link); ierr != nil {
				err = ierr
			} else if state == fsutil.Link && sameFolder(current, a.Source) {
				err = fsutil.RemoveLink(a.Link)
			}
		case Remove:
			if state, _, ierr := fsutil.Inspect(a.Link); ierr != nil {
				err = ierr
			} else if state == fsutil.Missing {
				err = fsutil.CreateLink(a.Link, a.Source)
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
