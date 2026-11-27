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

// Package playbook computes playbook ids, discovers the artifacts each registered playbook provides for each kind of
// its profile's targets, and plans the link changes needed to add, update or remove playbooks, or to change the
// enabled profiles, in the target folders.
package playbook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unicode"

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
	// Dir is the playbook folder, resolved against the managed folder, with links resolved.
	Dir string
}

// Key identifies a registered playbook: its profile, name and playbook folder.
type Key struct {
	Profile, Name, Dir string
}

// Key returns the entry's key.
func (e Entry) Key() Key {
	return Key{Profile: e.Profile, Name: e.Name, Dir: e.Dir}
}

// Label describes the entry in messages: "<id> (profile <profile>)".
func (e Entry) Label() string {
	return fmt.Sprintf("%s (profile %s)", e.ID, e.Profile)
}

// Entries builds the entries of the playbooks of a profile, computing their ids and resolving their folders against the
// managed folder.
func Entries(profile string, playbooks []config.Playbook, managed string) []Entry {
	count := map[string]int{}
	for _, p := range playbooks {
		count[p.Name]++
	}
	entries := make([]Entry, len(playbooks))
	for i, p := range playbooks {
		id, dir := p.Name, p.Dir(managed)
		if count[p.Name] > 1 {
			id = p.Name + "@" + filepath.Base(dir)
		}
		entries[i] = Entry{Playbook: p, ID: id, Profile: profile, Dir: dir}
	}
	return entries
}

// SourceDir returns the folder holding the playbook's artifacts of kind, with links resolved.
func (e Entry) SourceDir(kind string) string {
	folder, _ := e.Source(kind)
	return fsutil.Canonical(filepath.Join(e.Dir, filepath.FromSlash(folder)))
}

// Format returns the format of the playbook's artifacts of kind.
func (e Entry) Format(kind string) string {
	_, format := e.Source(kind)
	return format
}

// Owns reports whether a link named name in the target folder of kind, pointing to current, belongs to the playbook:
// it points to an entry of the same name in the playbook's folder for the kind, and the name matches its format.
func (e Entry) Owns(kind, name, current string) bool {
	return config.MatchFormat(e.Format(kind), name) && fsutil.SameName(filepath.Base(current), name) &&
		fsutil.SamePath(fsutil.Canonical(filepath.Dir(current)), e.SourceDir(kind))
}

// Owner returns the entry, among entries, that a link in the target folder of kind belongs to (see Owns), or nil.
func Owner(entries []Entry, kind, name, current string) *Entry {
	for i := range entries {
		if entries[i].Owns(kind, name, current) {
			return &entries[i]
		}
	}
	return nil
}

// Target is a profile's target folder for a kind, resolved.
type Target struct {
	Kind string
	// Folder is the target folder as configured.
	Folder string
	// Dir is the folder where the artifacts are linked.
	Dir string
	// Stop is the folder at which removing the folders that unlinking leaves empty stops (it is never removed): the
	// managed folder, or the %APPDATA% folder.
	Stop string
}

// Group is the playbooks of a profile and its targets.
type Group struct {
	Profile string
	Entries []Entry
	Targets []Target
}

// Item is an artifact provided by a playbook.
type Item struct {
	Name   string
	Source string
}

// Items returns the artifacts that e provides for kind: the entries of its folder for the kind (files and folders
// alike) whose names match its format, except hidden ones, those with control characters (see config.HasControl) and
// links leading outside the playbook folder (or that would, for broken links), sorted by name. A playbook without a
// folder for the kind (or with something else than a folder there) provides none. A folder, or a link among its
// entries, that cannot be read is an error, not something missing, so that its links are not taken for gone.
func Items(e Entry, kind string) ([]Item, error) {
	dir := e.SourceDir(kind)
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("playbook %s: cannot read its subfolder for %s: %w", e.Label(), kind, err)
	case !info.IsDir():
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("playbook %s: cannot read its subfolder for %s: %w", e.Label(), kind, err)
	}
	top, err := fsutil.Resolve(e.Dir)
	if err != nil {
		return nil, fmt.Errorf("playbook %s: %w", e.Label(), err)
	}
	format := e.Format(kind)
	var items []Item
	for _, entry := range entries {
		name := entry.Name()
		src := filepath.Join(dir, name)
		if !config.MatchFormat(format, name) || config.HasControl(name) {
			continue
		}
		resolved, err := fsutil.Resolve(src)
		if err != nil {
			return nil, fmt.Errorf("playbook %s: %w", e.Label(), err)
		}
		if !fsutil.Inside(resolved, top) {
			continue
		}
		items = append(items, Item{Name: name, Source: src})
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
	// Remove: the link belongs to the playbook but must go; it will be removed.
	Remove
	// Conflict: the link cannot be created; the operation must be cancelled.
	Conflict
	// Skip: the link, to a file, is not created, as links to files cannot be created (on Windows, without developer
	// mode or administrator rights; see Plan.SkipFileLinks).
	Skip
)

// ConflictCause says why an action is a Conflict.
type ConflictCause int

const (
	// NotLink: something that is not a link exists where the link must go.
	NotLink ConflictCause = iota + 1
	// OtherLink: a link to another artifact (Action.Current) exists where the link must go.
	OtherLink
	// Exists: a link to the same artifact exists where the link must go, but it does not belong to the playbook (it
	// was made by another playbook or profile, or by hand).
	Exists
	// Duplicate: other playbooks (Action.Others) would link an artifact at the same place.
	Duplicate
	// SamePlaybook: the same playbook (same name and playbook folder) is registered in other profiles that would be
	// enabled together (Action.Others).
	SamePlaybook
)

// Action is one planned change for one artifact in one target folder.
type Action struct {
	Kind ActionKind
	// Artifact is the kind of artifact.
	Artifact string
	// Name of the artifact (or of the playbook, for a SamePlaybook conflict).
	Name string
	// Link is the path of the link.
	Link string
	// Source is the artifact the link points (or must point) to.
	Source string
	// Stop is the folder at which removing the folders left empty stops (see Target.Stop).
	Stop string
	// Playbook describes the playbook the link belongs to (see Entry.Label); ID and Profile are its id and profile.
	Playbook    string
	ID, Profile string
	// Reason explains a Remove.
	Reason string
	// Cause explains a Conflict.
	Cause ConflictCause
	// Current is the target of the link found in the way, for an OtherLink conflict.
	Current string
	// Others describe the other playbooks of a Duplicate or SamePlaybook conflict.
	Others []string
	// madeDirs are the folders that Apply created for a Create's link, deepest first, which undoing it removes again.
	madeDirs []string
	// linkKind is, for a Remove, the kind of the link removed, so that undoing it makes the same kind of link again.
	linkKind fsutil.LinkKind
}

// Plan is a set of link changes.
type Plan struct {
	Actions []Action
}

// Conflicts returns the conflicting actions.
func (p Plan) Conflicts() []Action { return p.filter(Conflict) }

// Skipped returns the links to files that are not created (see SkipFileLinks).
func (p Plan) Skipped() []Action { return p.filter(Skip) }

// SkipFileLinks turns the creations of links to files into Skip actions, for when links to files cannot be created.
// So does it with the conflicts with what is found where such a link must go (NotLink, OtherLink and Exists), as
// nothing will be created there; conflicts between playbooks (Duplicate and SamePlaybook) stay, as they are conflicts
// in the configuration.
func (p *Plan) SkipFileLinks() {
	for i, a := range p.Actions {
		if wantsFileLink(a) {
			p.Actions[i].Kind, p.Actions[i].Cause, p.Actions[i].Current = Skip, 0, ""
		}
	}
}

// WantsFileLinks reports whether the plan would create a link to a file, or is in conflict with what is found where
// one must go: whether SkipFileLinks would change it.
func (p Plan) WantsFileLinks() bool {
	for _, a := range p.Actions {
		if wantsFileLink(a) {
			return true
		}
	}
	return false
}

// wantsFileLink reports whether an action creates a link to a file, or is a conflict with what is found where one
// must go (see SkipFileLinks).
func wantsFileLink(a Action) bool {
	inTheWay := a.Kind == Conflict && (a.Cause == NotLink || a.Cause == OtherLink || a.Cause == Exists)
	return (a.Kind == Create || inTheWay) && !fsutil.IsDir(a.Source)
}

// Changes returns the actions that would change something (removals, then creations).
func (p Plan) Changes() []Action {
	var out []Action
	for _, i := range p.changes() {
		out = append(out, p.Actions[i])
	}
	return out
}

// changes returns the indexes of the actions that would change something, in the order of Changes.
func (p Plan) changes() []int {
	var out []int
	for _, kind := range []ActionKind{Remove, Create} {
		for i, a := range p.Actions {
			if a.Kind == kind {
				out = append(out, i)
			}
		}
	}
	return out
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

// want is a link that a playbook wants.
type want struct {
	source string
	entry  Entry
}

// Transition plans the link changes of the playbooks in scope when the linked state goes from the groups in from (the
// enabled profiles now) to those in to (the enabled profiles afterwards). The links of playbooks out of scope are
// never touched. For the playbooks in scope:
//   - their links in from that to does not want any more (with the same artifact) are removed;
//   - their links in to are created, or kept when they already belong to them in from (a link that a playbook
//     leaving wants to keep for a playbook arriving, with the same artifact, is kept too);
//   - every link belongs to exactly one playbook of one profile, so a link that to wants more than once, or that
//     already exists without belonging to the playbook (whatever it points to), is a conflict, as is the same playbook
//     in two profiles of to.
func Transition(from, to []Group, scope map[Key]bool) (Plan, error) {
	var plan Plan
	type entryKind struct {
		key  Key
		kind string
	}
	cache := map[entryKind][]Item{}
	items := func(e Entry, kind string) ([]Item, error) {
		k := entryKind{e.Key(), kind}
		if its, ok := cache[k]; ok {
			return its, nil
		}
		its, err := Items(e, kind)
		cache[k] = its
		return its, err
	}

	// Links are told apart by where they are, as the system sees them (see places.key), so that target folders that
	// are the same folder (through a link, or in another case on a disk that ignores it) collide.
	keys := newPlaces()
	key := func(dir, name string) string { return keys.key(dir, name) }

	// What every playbook of to wants linked, by link.
	wants := map[string][]want{}
	for _, g := range to {
		for _, e := range g.Entries {
			for _, t := range g.Targets {
				its, err := items(e, t.Kind)
				if err != nil {
					return plan, err
				}
				for _, it := range its {
					k := key(t.Dir, it.Name)
					wants[k] = append(wants[k], want{source: it.Source, entry: e})
				}
			}
		}
	}

	// The same playbook in two profiles of to: its artifacts are not planned (they would only repeat the conflict), and
	// the conflict is reported once, even when several of its registrations are in scope.
	samePlaybook := map[Key]bool{}
	reported := map[Key]bool{}
	for i, g := range to {
		for _, e := range g.Entries {
			if !scope[e.Key()] {
				continue
			}
			var others []string
			for j, o := range to {
				if j == i {
					continue
				}
				for _, f := range o.Entries {
					if f.Name == e.Name && fsutil.SamePath(f.Dir, e.Dir) {
						others = append(others, f.Label())
					}
				}
			}
			if len(others) > 0 {
				samePlaybook[e.Key()] = true
				if pair := (Key{Name: e.Name, Dir: e.Dir}); !reported[pair] {
					reported[pair] = true
					plan.Actions = append(plan.Actions, Action{Kind: Conflict, Cause: SamePlaybook, Name: e.Name,
						Playbook: e.Label(), ID: e.ID, Profile: e.Profile, Source: e.Dir, Others: others})
				}
			}
		}
	}

	// Removals.
	removed := map[string]bool{}
	transferred := map[string]bool{}
	inTo := map[Key]bool{}
	profilesTo := map[string]bool{}
	for _, g := range to {
		profilesTo[g.Profile] = true
		for _, e := range g.Entries {
			inTo[e.Key()] = true
		}
	}
	for _, g := range from {
		for _, e := range g.Entries {
			if !scope[e.Key()] {
				continue
			}
			for _, t := range g.Targets {
				dirEntries, err := os.ReadDir(t.Dir)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return plan, err
				}
				for _, de := range dirEntries {
					name := de.Name()
					link, k := filepath.Join(t.Dir, name), key(t.Dir, name)
					if removed[k] || transferred[k] {
						continue
					}
					state, current, err := fsutil.Inspect(link)
					if err != nil {
						return plan, err
					}
					if state != fsutil.Link || !e.Owns(t.Kind, name, current) {
						continue
					}
					keep := false
					for _, w := range wants[k] {
						if sameFolder(w.source, current) {
							keep = true
							if scope[w.entry.Key()] {
								transferred[k] = true
							}
						}
					}
					if keep {
						continue
					}
					reason := "no longer in the playbook"
					switch {
					case !profilesTo[g.Profile]:
						reason = "profile disabled"
					case !inTo[e.Key()]:
						reason = "playbook removed"
					default:
						if _, err := os.Stat(current); err != nil {
							reason = "broken link: its artifact no longer exists"
						}
					}
					removed[k] = true
					plan.Actions = append(plan.Actions, Action{Kind: Remove, Artifact: t.Kind, Name: name, Link: link,
						Source: current, Stop: t.Stop, Playbook: e.Label(), ID: e.ID, Profile: e.Profile, Reason: reason,
						linkKind: fsutil.KindOf(link)})
				}
			}
		}
	}

	// Creations.
	duplicates := map[string]bool{}
	for _, g := range to {
		for _, e := range g.Entries {
			if !scope[e.Key()] || samePlaybook[e.Key()] {
				continue
			}
			for _, t := range g.Targets {
				its, _ := items(e, t.Kind)
				for _, it := range its {
					link, k := filepath.Join(t.Dir, it.Name), key(t.Dir, it.Name)
					act := Action{Artifact: t.Kind, Name: it.Name, Link: link, Source: it.Source, Stop: t.Stop,
						Playbook: e.Label(), ID: e.ID, Profile: e.Profile}
					// Every other want of the same place: other playbooks, or this one through another target folder
					// that is the same folder.
					var others []string
					self := false
					for _, w := range wants[k] {
						if !self && w.entry.Key() == e.Key() && w.source == it.Source {
							self = true
							continue
						}
						others = append(others, w.entry.Label())
					}
					if len(others) > 0 {
						if !duplicates[k] {
							duplicates[k] = true
							act.Kind, act.Cause, act.Others = Conflict, Duplicate, others
							plan.Actions = append(plan.Actions, act)
						}
						continue
					}
					if transferred[k] {
						act.Kind = Keep
						plan.Actions = append(plan.Actions, act)
						continue
					}
					state, current := fsutil.Missing, ""
					if !removed[k] {
						var err error
						if state, current, err = fsutil.Inspect(link); err != nil {
							return plan, err
						}
					}
					switch {
					case state == fsutil.Missing:
						act.Kind = Create
					case state == fsutil.Link && sameFolder(current, it.Source):
						act.Kind, act.Cause = Conflict, Exists
					case state == fsutil.Link:
						act.Kind, act.Cause, act.Current = Conflict, OtherLink, current
					default:
						act.Kind, act.Cause = Conflict, NotLink
					}
					plan.Actions = append(plan.Actions, act)
				}
			}
		}
	}
	return plan, nil
}

// places tells links apart by where they are, as the system sees them: two paths to one place have the same key.
type places struct {
	resolved map[string]string // target folders, with links resolved
	seen     []seenDir         // existing folders, by the first spelling seen of each
	folds    map[string]bool   // whether the disk of an existing folder ignores case
}

type seenDir struct {
	path string
	info os.FileInfo
}

func newPlaces() *places {
	return &places{resolved: map[string]string{}, folds: map[string]bool{}}
}

// key returns the key of the link name in the target folder dir: its path with links resolved, in which the deepest
// folder that exists is spelled as the first path seen to the same folder (so that two spellings of one folder, such
// as another case on a disk that ignores it, count as one), and what follows it is in lower case when that folder's
// disk ignores case (on Windows, all of it is, as fsutil.SamePath does).
func (p *places) key(dir, name string) string {
	r, ok := p.resolved[dir]
	if !ok {
		r = dir
		if res, err := fsutil.Resolve(dir); err == nil {
			r = res
		}
		p.resolved[dir] = r
	}
	full := filepath.Join(r, name)
	existing := filepath.Dir(full)
	var info os.FileInfo
	for {
		var err error
		if info, err = os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return linkKey(full)
		}
		existing = parent
	}
	rest, _ := filepath.Rel(existing, full)
	same := false
	for _, s := range p.seen {
		if os.SameFile(s.info, info) {
			existing, same = s.path, true
			break
		}
	}
	if !same {
		p.seen = append(p.seen, seenDir{existing, info})
	}
	folds, ok := p.folds[existing]
	if !ok {
		folds = ignoresCase(existing)
		p.folds[existing] = folds
	}
	if folds {
		rest = strings.ToLower(rest)
	}
	return linkKey(filepath.Join(existing, rest))
}

// ignoresCase reports whether the disk of dir (which exists) ignores case: whether the nearest of dir and its parents
// with a name that has letters is found again with the case of those letters swapped, as the same folder.
func ignoresCase(dir string) bool {
	for d := dir; filepath.Dir(d) != d; d = filepath.Dir(d) {
		name := filepath.Base(d)
		swapped := strings.Map(func(r rune) rune {
			if unicode.IsUpper(r) {
				return unicode.ToLower(r)
			}
			return unicode.ToUpper(r)
		}, name)
		if swapped == name {
			continue
		}
		a, errA := os.Stat(d)
		b, errB := os.Stat(filepath.Join(filepath.Dir(d), swapped))
		return errA == nil && errB == nil && os.SameFile(a, b)
	}
	return false
}

// linkKey returns path as the key of a link, ignoring case on Windows, as fsutil.SamePath does.
func linkKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// sameFolder reports whether two paths lead to the same place once links are resolved, so that a link made through
// a path that has since become a link (for example, a playbook moved and linked from its old location) still counts.
func sameFolder(a, b string) bool {
	return fsutil.SamePath(fsutil.Canonical(a), fsutil.Canonical(b))
}

// Apply performs the removals and then the creations of a plan that has no conflicts, creating the folders of the
// links when needed (it records them in the plan's actions, so that Revert removes them too). It is all-or-nothing:
// when a change fails, the changes already made are undone (and report has been called for them) before returning
// the error.
func Apply(plan Plan, report func(Action)) error {
	if len(plan.Conflicts()) > 0 {
		return errors.New("the plan has conflicts")
	}
	var done []Action
	for _, i := range plan.changes() {
		a := &plan.Actions[i]
		var err error
		switch a.Kind {
		case Create:
			a.madeDirs = missingDirs(filepath.Dir(a.Link), a.Stop)
			if err = os.MkdirAll(filepath.Dir(a.Link), 0o755); err == nil {
				err = fsutil.CreateLink(a.Link, a.Source)
			}
		case Remove:
			err = fsutil.RemoveLink(a.Link)
		}
		if err != nil {
			rerr := removeDirs(a.madeDirs)
			a.madeDirs = nil
			if verr := Revert(done); rerr == nil {
				rerr = verr
			}
			if rerr != nil {
				return fmt.Errorf("%w; undoing the changes already made also failed: %v", err, rerr)
			}
			if len(done) > 0 {
				return fmt.Errorf("%w; the changes already made were undone", err)
			}
			return err
		}
		done = append(done, *a)
		if report != nil {
			report(*a)
		}
	}
	return nil
}

// missingDirs returns dir and its parents that do not exist, deepest first, up to (and never including) stop.
func missingDirs(dir, stop string) []string {
	var dirs []string
	for ; fsutil.Within(dir, stop) && !fsutil.SamePath(dir, stop); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

// removeDirs removes the folders of dirs (deepest first) that are empty, and returns the first error removing one of
// them. Folders with something in them are kept: that is no error.
func removeDirs(dirs []string) error {
	var first error
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && len(entries) > 0) {
			continue
		}
		if err == nil {
			err = os.Remove(dir)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Revert undoes creations and removals applied by Apply, last first: it removes the created links (only if they are
// still links to their source) and the folders Apply created for them (only if empty), and recreates the removed ones
// (only if nothing has taken their place). It tries every action and returns the first error.
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
			if err == nil {
				err = removeDirs(a.madeDirs)
			}
		case Remove:
			if state, _, ierr := fsutil.Inspect(a.Link); ierr != nil {
				err = ierr
			} else if state == fsutil.Missing {
				if err = os.MkdirAll(filepath.Dir(a.Link), 0o755); err == nil {
					err = fsutil.RestoreLink(a.Link, a.Source, a.linkKind)
				}
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Cleanup removes the folders that the removals of an applied plan left empty: the folder of each removed link and,
// as they become empty too, its parents, up to (and never including) the action's Stop folder. Folders holding
// anything else (such as artifacts added by hand) are kept, and so are links to folders. It reports each folder
// removed.
func Cleanup(plan Plan, report func(dir string)) {
	for _, a := range plan.Actions {
		if a.Kind != Remove || a.Stop == "" {
			continue
		}
		for dir := filepath.Dir(a.Link); !fsutil.SamePath(dir, a.Stop) && fsutil.Within(dir, a.Stop); {
			info, err := os.Lstat(dir)
			if err != nil || !info.IsDir() {
				break
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) > 0 {
				break
			}
			if os.Remove(dir) != nil {
				break
			}
			if report != nil {
				report(dir)
			}
			dir = filepath.Dir(dir)
		}
	}
}
