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

// Package playbook computes playbook ids, discovers the artifacts of each registered playbook (skills, subagents,
// commands, ...), and plans the link changes needed to add, update or remove playbooks in the folders where agents
// read their artifacts.
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

// Entry is a registered playbook with its computed id and resolved folder.
type Entry struct {
	config.Playbook
	// ID is the name, or "name@<last folder segment>" when several playbooks of its profile share the name.
	ID string
	// Profile is the profile the playbook is registered in.
	Profile string
	// Dir is the playbook folder, resolved against the base folder, with links resolved.
	Dir string
}

// Entries builds the entries of the playbooks of a profile, computing their ids and resolving their folders against the
// base folder.
func Entries(profile string, playbooks []config.Playbook, base string) []Entry {
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
		entries[i] = Entry{Playbook: p, ID: id, Profile: profile, Dir: dir}
	}
	return entries
}

// KindDir returns the folder holding the playbook's artifacts for a kind folder (the path set for its kind, or else
// the default of the folder's agent), with links resolved.
func (e Entry) KindDir(folder config.KindFolder) string {
	return e.PathDir(e.Path(folder.Kind, folder.DefaultPath))
}

// PathDir returns a folder of the playbook, given relative to its playbook folder, with links resolved.
func (e Entry) PathDir(path string) string {
	return fsutil.Canonical(filepath.Join(e.Dir, filepath.FromSlash(path)))
}

// Group is the playbooks of a profile, and the folders where their artifacts are linked: those of the profile's
// agent in the base folder.
type Group struct {
	Profile string
	Entries []Entry
	Folders []config.KindFolder
}

// Item is an artifact provided by a playbook.
type Item struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// candidates lists the artifacts of a format in dir whose name starts with prefix + "-": folders holding the format's
// file, or files with the format's extension.
func candidates(dir, prefix string, format config.Format) ([]Item, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix+"-") || len(name) == len(prefix)+1 {
			continue
		}
		src := filepath.Join(dir, name)
		if format.File != "" {
			if fsutil.IsDir(src) && fsutil.IsFile(filepath.Join(src, format.File)) {
				items = append(items, Item{Name: name, Source: src})
			}
		} else if fsutil.IsFile(src) && config.ItemExt(name) == format.Ext {
			items = append(items, Item{Name: name, Source: src})
		}
	}
	return items, nil
}

// Owner returns the entry, among entries, that owns an artifact named name found in kindDir, for a kind folder: the
// entry with that folder for the kind whose name is the longest prefix of the artifact's name (followed by '-'). It
// returns nil when no entry matches.
func Owner(entries []Entry, folder config.KindFolder, kindDir, name string) *Entry {
	var owner *Entry
	for i := range entries {
		e := &entries[i]
		if !strings.HasPrefix(name, e.Name+"-") || !fsutil.SamePath(e.KindDir(folder), kindDir) {
			continue
		}
		if owner == nil || len(e.Name) > len(owner.Name) {
			owner = e
		}
	}
	return owner
}

// Items returns the artifacts that e provides for a kind folder when entries (which must include e) are registered:
// the items of its folder for the kind, in the folder's format, named "<name>-...", except those claimed by an entry
// with a longer matching name on the same folder, and those that are links leading outside the playbook folder. A
// playbook without a folder for the kind provides none.
func Items(entries []Entry, e Entry, folder config.KindFolder) ([]Item, error) {
	dir := e.KindDir(folder)
	if !fsutil.IsDir(dir) {
		return nil, nil
	}
	candidates, err := candidates(dir, e.Name, folder.Format)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, it := range candidates {
		if !fsutil.Within(fsutil.Canonical(it.Source), e.Dir) {
			continue
		}
		if owner := Owner(entries, folder, dir, it.Name); owner != nil && owner.ID == e.ID {
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

// ActionKind is the kind of a planned change.
type ActionKind int

const (
	// Keep: the link exists and points to the right artifact.
	Keep ActionKind = iota
	// Create: the link is missing and will be created.
	Create
	// Remove: the link points into the playbook but its artifact no longer exists or belongs to it; it will be removed.
	Remove
	// Conflict: something else exists where the link must go; the operation must be cancelled.
	Conflict
)

// ConflictCause says why an action is a Conflict.
type ConflictCause int

const (
	// NotLink: something that is not a link exists where the link must go.
	NotLink ConflictCause = iota + 1
	// OtherLink: a link to another artifact (Action.Current) exists where the link must go.
	OtherLink
	// OtherProvider: other registered playbooks (Action.Others) want a link at the same place.
	OtherProvider
	// Exists: the link already exists, even if it is right; enabling a profile requires every one of its links to be new.
	Exists
	// Ancestor: an artifact with the same name exists where the same agent reads the same kind in a higher-level base
	// folder (Action.Current), which agents may read too.
	Ancestor
)

// Action is one planned change for one artifact in one kind folder.
type Action struct {
	Kind ActionKind
	// Artifact is the kind of artifact.
	Artifact config.Kind
	// Agent is the agent the artifact is linked for.
	Agent string
	// Name of the artifact.
	Name string
	// Link is the path of the link.
	Link string
	// Source is the artifact the link points (or must point) to.
	Source string
	// AgentsConfigDir is the agents configuration folder holding the link's folder.
	AgentsConfigDir string
	// Reason explains a Remove.
	Reason string
	// Cause explains a Conflict.
	Cause ConflictCause
	// Current is the target of the link found in the way, for an OtherLink conflict, or the artifact found in a
	// higher-level base folder, for an Ancestor conflict.
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

// Build plans the changes for playbook e in the kind folders of final, for the state in which final is e's profile
// and others are the other enabled profiles. When e is in final (by name and folder), its artifacts must be linked
// (add or update); otherwise e is being removed and all of its links go. Links that point into e's folders and are
// named after e are removed unless some playbook of final or others wants exactly that link. A desired link whose path
// holds anything other than a link to the same artifact is a conflict, as is a link that another playbook of final or
// others also wants.
func Build(final Group, e Entry, others []Group) (Plan, error) {
	return build(final, e, others, nil, false)
}

// wanted is a link that a playbook wants.
type wanted struct {
	source string
	label  string // the playbook's id ("<id> of profile <profile>" for other profiles)
}

// build is Build for a state in which the links in gone have already been removed. With strict, a desired link that
// already exists is a conflict even when it is right.
func build(final Group, e Entry, others []Group, gone map[string]bool, strict bool) (Plan, error) {
	plan := Plan{Entry: e}
	inspect := func(link string) (fsutil.LinkState, string, error) {
		if gone[link] {
			return fsutil.Missing, "", nil
		}
		return fsutil.Inspect(link)
	}

	present := false
	for _, f := range final.Entries {
		if f.Name == e.Name && fsutil.SamePath(f.Dir, e.Dir) {
			present = true
			e = f
			break
		}
	}

	// What every playbook in the final state (of e's profile and of the other enabled profiles) wants linked, by link.
	want := map[string][]wanted{}
	for i, g := range append([]Group{final}, others...) {
		for _, f := range g.Entries {
			label := f.ID
			if i > 0 {
				label = fmt.Sprintf("%s of profile %s", f.ID, f.Profile)
			}
			for _, folder := range g.Folders {
				items, err := Items(g.Entries, f, folder)
				if err != nil {
					return plan, err
				}
				for _, it := range items {
					link := filepath.Join(folder.Dir, it.Name)
					want[link] = append(want[link], wanted{source: it.Source, label: label})
				}
			}
		}
	}

	for _, folder := range final.Folders {
		var mine []Item
		if present {
			if dir := e.KindDir(folder); fsutil.IsDir(dir) && !fsutil.Within(dir, e.Dir) {
				return plan, fmt.Errorf("playbook %s: %s folder '%s' is outside the playbook folder '%s'", e.ID,
					folder.Kind, dir, e.Dir)
			}
			items, err := Items(final.Entries, e, folder)
			if err != nil {
				return plan, err
			}
			mine = items
		}
		mineByName := map[string]bool{}
		for _, it := range mine {
			mineByName[it.Name] = true
			link := filepath.Join(folder.Dir, it.Name)
			action := Action{Artifact: folder.Kind, Agent: folder.Agent, Name: it.Name, Link: link, Source: it.Source,
				AgentsConfigDir: folder.AgentsConfigDir}
			if others := otherProviders(want[link], e.ID); len(others) > 0 {
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
			case state == fsutil.Link && sameFolder(current, it.Source) && strict:
				action.Kind, action.Cause = Conflict, Exists
			case state == fsutil.Link && sameFolder(current, it.Source):
				action.Kind = Keep
			case state == fsutil.Link:
				action.Kind, action.Cause, action.Current = Conflict, OtherLink, current
			default:
				action.Kind, action.Cause = Conflict, NotLink
			}
			plan.Actions = append(plan.Actions, action)
		}

		entries, err := os.ReadDir(folder.Dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return plan, err
		}
		kindDir := e.KindDir(folder)
		for _, entry := range entries {
			name := entry.Name()
			link := filepath.Join(folder.Dir, name)
			if mineByName[name] || gone[link] || !strings.HasPrefix(name, e.Name+"-") {
				continue
			}
			state, current, err := fsutil.Inspect(link)
			if err != nil || state != fsutil.Link ||
				!fsutil.SamePath(fsutil.Canonical(filepath.Dir(current)), kindDir) {
				continue
			}
			if wantedExactly(want[link], current) {
				continue // Another registered playbook (for example, one with a longer name) owns it.
			}
			reason := "no longer in the playbook"
			if !present {
				reason = "playbook removed"
			} else if _, err := os.Stat(current); err != nil {
				reason = "broken link: its artifact no longer exists"
			}
			plan.Actions = append(plan.Actions, Action{Kind: Remove, Artifact: folder.Kind, Agent: folder.Agent,
				Name: name, Link: link, Source: current, AgentsConfigDir: folder.AgentsConfigDir, Reason: reason})
		}
	}
	return plan, nil
}

// Overlaps returns, as OtherProvider conflicts, the artifacts of e that other playbooks of final would link at the
// same place.
func Overlaps(final Group, e Entry) ([]Action, error) {
	var conflicts []Action
	for _, folder := range final.Folders {
		providers := map[string][]wanted{}
		for _, f := range final.Entries {
			items, err := Items(final.Entries, f, folder)
			if err != nil {
				return nil, err
			}
			for _, it := range items {
				providers[it.Name] = append(providers[it.Name], wanted{source: it.Source, label: f.ID})
			}
		}
		var names []string
		for name := range providers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			ws := providers[name]
			if others := otherProviders(ws, e.ID); len(others) > 0 && len(others) < len(ws) {
				conflicts = append(conflicts, Action{Kind: Conflict, Artifact: folder.Kind, Agent: folder.Agent,
					Name: name, Link: filepath.Join(folder.Dir, name), Cause: OtherProvider, Others: others})
			}
		}
	}
	return conflicts, nil
}

// Switch plans the move from one linked state to another: the links of the profiles in from are removed, the profiles
// in to are linked, and the profiles in stay remain linked (their links are neither removed nor shared). Links that
// from and to both want (same path, same artifact) are left alone. Conflicts are planned as in Build, except that
// links being removed are not in the way; with strict, any other link already in place of a link to create is a
// conflict. dropReason explains the removals of links in folders that the profiles in to do not use; reason explains
// the others.
func Switch(from, to, stay []Group, reason, dropReason string, strict bool) (Plan, error) {
	var plan Plan
	inNewFolder := func(link string) bool {
		for _, g := range to {
			for _, f := range g.Folders {
				if fsutil.SamePath(f.Dir, filepath.Dir(link)) {
					return true
				}
			}
		}
		return false
	}
	removed := map[string]Action{}
	var removals []Action
	for _, g := range from {
		for _, e := range g.Entries {
			p, err := build(Group{Profile: g.Profile, Folders: g.Folders}, e, stay, nil, false)
			if err != nil {
				return plan, err
			}
			for _, a := range p.Actions {
				if _, dup := removed[a.Link]; a.Kind == Remove && !dup {
					a.Reason = reason
					if !inNewFolder(a.Link) {
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
	for i, g := range to {
		others := append([]Group{}, stay...)
		for j, o := range to {
			if j != i {
				others = append(others, o)
			}
		}
		for _, e := range g.Entries {
			p, err := build(g, e, others, gone, strict)
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

func otherProviders(ws []wanted, self string) []string {
	var others []string
	for _, w := range ws {
		if w.label != self {
			others = append(others, w.label)
		}
	}
	return others
}

func wantedExactly(ws []wanted, source string) bool {
	for _, w := range ws {
		if sameFolder(w.source, source) {
			return true
		}
	}
	return false
}

// MarkAncestors turns into Ancestor conflicts the creations of a plan whose artifact already exists where the same
// agent reads the same kind of artifact in a higher-level base folder (one of ancestors; home is the home folder,
// whose folders can differ from those of other base folders), so that agents reading several levels never see an
// artifact twice.
func MarkAncestors(plan *Plan, ancestors []string, home string) {
	for i, act := range plan.Actions {
		if act.Kind != Create {
			continue
		}
		agent, ok := config.LookupAgent(act.Agent)
		if !ok {
			continue
		}
		for _, base := range ancestors {
			place, ok := agent.Place(act.Artifact, fsutil.SamePath(base, home))
			if !ok {
				continue
			}
			path := filepath.Join(base, filepath.FromSlash(place.Dir), filepath.FromSlash(place.Sub), act.Name)
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

// Apply performs the creations and removals of a plan that has no conflicts, creating the folders of the links when
// needed. It is all-or-nothing: when a change fails, the changes already made are undone (and report has been called
// for them) before returning the error.
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
				if err = os.MkdirAll(filepath.Dir(a.Link), 0o755); err == nil {
					err = fsutil.CreateLink(a.Link, a.Source)
				}
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Cleanup removes the folders that the removals of an applied plan left empty: the folders the links were in and, when
// they become empty too, their agents configuration folders. Folders holding anything else (such as artifacts added by
// hand) are kept, and so are links to folders. It reports each folder removed.
func Cleanup(plan Plan, report func(dir string)) {
	removeEmpty := func(dir string) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) > 0 {
			return
		}
		if os.Remove(dir) == nil && report != nil {
			report(dir)
		}
	}
	for _, a := range plan.Actions {
		if a.Kind != Remove {
			continue
		}
		dir := filepath.Dir(a.Link)
		removeEmpty(dir)
		if a.AgentsConfigDir != "" && !fsutil.SamePath(a.AgentsConfigDir, dir) {
			removeEmpty(a.AgentsConfigDir)
		}
	}
}
