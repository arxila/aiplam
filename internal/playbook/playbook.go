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
// link changes needed to install, update or remove a playbook in the target folders.
package playbook

import (
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
	// ID is the name, or "name@<last folder segment>" when several playbooks share the name.
	ID string
	// Dir is the playbook's folder, with "~" expanded and links resolved.
	Dir string
	// SkillsDir is the folder holding the playbook's skills, with links resolved.
	SkillsDir string
}

// Entries builds the entries of the given playbooks, computing their ids and resolving their folders ("~" stands for
// home).
func Entries(playbooks []config.Playbook, home string) []Entry {
	count := map[string]int{}
	for _, p := range playbooks {
		count[p.Name]++
	}
	entries := make([]Entry, len(playbooks))
	for i, p := range playbooks {
		id := p.Name
		if count[p.Name] > 1 {
			id = p.Name + "@" + filepath.Base(p.Folder)
		}
		entries[i] = Entry{Playbook: p, ID: id, Dir: p.Dir(home), SkillsDir: p.SkillsDir(home)}
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
// skills folder, and those that are links leading outside the playbook's folder. It fails when the skills folder
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
)

// Action is one planned change for one artifact in one target folder.
type Action struct {
	Kind ActionKind
	// Kind of artifact ("skill").
	Artifact string
	// Name of the artifact.
	Name string
	// Link is the path of the link in the target folder.
	Link string
	// Source is the folder the link points (or must point) to.
	Source string
	// Reason explains a Remove.
	Reason string
	// Cause explains a Conflict.
	Cause ConflictCause
	// Current is the target of the link found in the way, for an OtherLink conflict.
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

// SkillsFolder returns the folder of a target where skills are installed.
func SkillsFolder(target string) string {
	return filepath.Join(target, "skills")
}

// Build plans the changes for playbook e in the given targets, for the state in which final are the registered
// entries. When e is in final (by name and folder), its skills must be installed (add or update); otherwise e is being
// removed and all of its links go. Links that point into e's skills folder and are named after e are removed unless
// some entry in final wants exactly that link. A desired link whose path holds anything other than a link to the
// same skill is a conflict, as is a skill name that another playbook in final also provides from another folder.
func Build(final []Entry, e Entry, targets []string) (Plan, error) {
	plan := Plan{Entry: e}

	present := false
	for _, f := range final {
		if f.Name == e.Name && fsutil.SamePath(f.Dir, e.Dir) {
			present = true
			e = f
			break
		}
	}

	// What every entry in the final state wants installed, by artifact name.
	wanted := map[string][]Skill{}
	wantedBy := map[string][]string{}
	for _, f := range final {
		if !fsutil.IsDir(f.SkillsDir) {
			continue
		}
		skills, err := Skills(final, f)
		if err != nil {
			return plan, err
		}
		for _, s := range skills {
			wanted[s.Name] = append(wanted[s.Name], s)
			wantedBy[s.Name] = append(wantedBy[s.Name], f.ID)
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

	for _, target := range targets {
		dir := SkillsFolder(target)
		for _, s := range mine {
			link := filepath.Join(dir, s.Name)
			action := Action{Artifact: "skill", Name: s.Name, Link: link, Source: s.Source}
			if others := otherProviders(wantedBy[s.Name], e.ID); len(others) > 0 {
				action.Kind, action.Cause, action.Others = Conflict, OtherProvider, others
				plan.Actions = append(plan.Actions, action)
				continue
			}
			state, current, err := fsutil.Inspect(link)
			if err != nil {
				return plan, err
			}
			switch {
			case state == fsutil.Missing:
				action.Kind = Create
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
			if _, ok := mineByName[name]; ok || !strings.HasPrefix(name, e.Name+"-") {
				continue
			}
			link := filepath.Join(dir, name)
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

// sameFolder reports whether two paths lead to the same place once links are resolved, so that a link made through
// a path that has since become a link (for example, a playbook moved and linked from its old location) still counts.
func sameFolder(a, b string) bool {
	return fsutil.SamePath(fsutil.Canonical(a), fsutil.Canonical(b))
}

// Apply performs the creations and removals of a plan that has no conflicts, creating the targets' skills folders
// when needed. It is all-or-nothing: when a change fails, the changes already made are undone (and report has been
// called for them) before returning the error.
func Apply(plan Plan, report func(Action)) error {
	if len(plan.Conflicts()) > 0 {
		return fmt.Errorf("playbook %s has conflicts", plan.Entry.ID)
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
			} else if state == fsutil.Link && fsutil.SamePath(current, a.Source) {
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
