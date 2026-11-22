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

// Package config reads, validates and writes aiplam's configuration. A configuration lives in the .aiplam folder of
// its base folder (the user's home, or any other folder, such as a project's), and its artifacts are linked from the
// agents configuration folders next to it (.agents, .claude, ...):
//
//   - aiplam-config.yaml, the installation's configuration: the types of agents configuration folders where
//     artifacts are linked from, and the enabled sets.
//   - <set>.yaml, one file per set: the playbooks registered in the set. Only the enabled sets' playbooks are
//     linked from the agents configuration folders.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"arxila.io/aiplam/internal/fsutil"
)

const (
	// DirName is the name of aiplam's configuration folder in a base folder.
	DirName = ".aiplam"
	// InstallName is the name (without extension) of the installation's configuration file. It cannot be a set name.
	InstallName = "aiplam-config"
	// FileExt is the extension of the configuration files.
	FileExt = ".yaml"
	// DefaultSet is the set that 'aiplam init' creates and enables when no set is given.
	DefaultSet = "default"
	// DefaultSkillsPath is the folder, relative to a playbook's folder, where its skills live by default.
	DefaultSkillsPath = "skills"
)

var (
	// ErrNotInitialized is returned by LoadInstall when the installation's configuration file does not exist.
	ErrNotInitialized = errors.New("not initialized")
	// ErrNoSet is returned by LoadSet when the set's file does not exist.
	ErrNoSet = errors.New("no such set")
)

// Types maps each type of agents configuration folder to its path, relative to the base folder (in slash form).
var Types = map[string]string{
	"agents": ".agents",
	"claude": ".claude",
}

// DefaultType is the type that 'aiplam init' uses when none is given.
const DefaultType = "agents"

// TypeNames returns the known types, sorted.
func TypeNames() []string {
	names := make([]string, 0, len(Types))
	for name := range Types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Install is the content of aiplam-config.yaml.
type Install struct {
	// Types are the types of the agents configuration folders where artifacts are linked from (for example agents,
	// for the base folder's .agents folder); skills go to their skills/ subfolder.
	Types []string `yaml:"types"`
	// Enabled are the sets whose playbooks are linked from the agents configuration folders (possibly none).
	Enabled []string `yaml:"enabled"`
}

// Set is the content of a set's file.
type Set struct {
	// Playbooks are the playbooks registered in the set.
	Playbooks []Playbook `yaml:"playbooks"`
}

// Playbook is a registered playbook. Its id is not stored: it is computed from its name and folder.
type Playbook struct {
	// Name is the prefix of the playbook's artifacts: they are named "<name>-...".
	Name string `yaml:"name"`
	// Folder is the path of the playbook's folder (usually a git checkout), relative to the base folder, in slash
	// form, so that the configuration can be shared (for example, committed to a project's repository).
	Folder string `yaml:"folder"`
	// Artifacts says where each kind of artifact lives in the folder.
	Artifacts Artifacts `yaml:"artifacts"`
}

// Artifacts holds the settings of each kind of artifact.
type Artifacts struct {
	Skills Skills `yaml:"skills"`
}

// Skills holds the settings of the skills artifact kind.
type Skills struct {
	// Path is the folder, relative to the playbook's folder, holding the skill folders ("." for the folder itself). It
	// cannot lead outside the playbook's folder.
	Path string `yaml:"path"`
}

// Dir returns the path of the configuration folder of a base folder.
func Dir(base string) string {
	return filepath.Join(base, DirName)
}

// InstallFile returns the path of the installation's configuration file of a base folder.
func InstallFile(base string) string {
	return filepath.Join(Dir(base), InstallName+FileExt)
}

// SetFile returns the path of a set's file.
func SetFile(base, set string) string {
	return filepath.Join(Dir(base), set+FileExt)
}

// IsBase reports whether dir is a base folder: it holds a .aiplam folder (whose configuration may still be missing).
func IsBase(dir string) bool {
	return fsutil.IsDir(Dir(dir))
}

// FindBase returns the base folder whose configuration commands run in start use: start or its nearest parent that
// holds a .aiplam folder, or else home. start must be absolute; it is walked up as given (a folder reached through a
// link, such as a linked skill, belongs to the base folder above the link), and the base folder found is returned
// with links resolved.
func FindBase(start, home string) string {
	for dir := filepath.Clean(start); ; {
		if IsBase(dir) {
			return fsutil.Canonical(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return home
		}
		dir = parent
	}
}

// Ancestors returns the base folders whose agents configuration folders agents may read besides base's: the parents
// of base that hold a .aiplam folder, nearest first, and home when it holds one and base is not inside it.
func Ancestors(base, home string) []string {
	var bases []string
	for dir := filepath.Clean(base); ; {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		if IsBase(dir) {
			bases = append(bases, dir)
		}
	}
	if !fsutil.Within(base, home) && IsBase(home) {
		bases = append(bases, home)
	}
	return bases
}

// RelFolder returns the path to store for a playbook folder: dir relative to base, in slash form. It fails when there
// is no relative path (on Windows, when dir is on another drive than base).
func RelFolder(base, dir string) (string, error) {
	rel, err := filepath.Rel(base, dir)
	if err != nil || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("playbook folder '%s' must be on the same drive as the base folder '%s'", dir, base)
	}
	return filepath.ToSlash(rel), nil
}

var (
	nameRegexp    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	setNameRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

// ValidName reports whether name can be a playbook name: letters, digits, '.', '_' and '-', starting with a letter
// or digit. In particular, it cannot contain '@' (used in ids) or path separators.
func ValidName(name string) bool {
	return nameRegexp.MatchString(name)
}

// ValidSetName reports whether name can be a set name: letters, digits, '_' and '-', starting with a letter or digit,
// and not the name of the installation's configuration file.
func ValidSetName(name string) bool {
	return setNameRegexp.MatchString(name) && !strings.EqualFold(name, InstallName)
}

// Sets returns the names of the existing sets, sorted.
func Sets(base string) ([]string, error) {
	items, err := os.ReadDir(Dir(base))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sets []string
	for _, item := range items {
		name, ok := strings.CutSuffix(item.Name(), FileExt)
		if ok && !item.IsDir() && ValidSetName(name) {
			sets = append(sets, name)
		}
	}
	sort.Strings(sets)
	return sets, nil
}

// SetExists reports whether a set's file exists.
func SetExists(base, set string) bool {
	return fsutil.IsFile(SetFile(base, set))
}

// SameSet reports whether two set names designate the same set: equal names, or names of the same file (on file
// systems that ignore case, "Work" and "work" are the same set).
func SameSet(base, a, b string) bool {
	if a == b {
		return true
	}
	fa, errA := os.Stat(SetFile(base, a))
	fb, errB := os.Stat(SetFile(base, b))
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// LoadInstall reads and parses the installation's configuration file. It returns ErrNotInitialized when the file
// does not exist. Use Validate for semantic checks.
func LoadInstall(base string) (*Install, error) {
	data, err := os.ReadFile(InstallFile(base))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, err
	}
	inst := &Install{}
	if err := parse(data, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// LoadSet reads and parses a set's file. It returns ErrNoSet when the file does not exist. An empty file is an empty
// set. Use Validate for semantic checks.
func LoadSet(base, set string) (*Set, error) {
	data, err := os.ReadFile(SetFile(base, set))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSet
	}
	if err != nil {
		return nil, err
	}
	s := &Set{}
	if err := parse(data, s); err != nil && !errors.Is(err, errEmpty) {
		return nil, err
	}
	return s, nil
}

var errEmpty = errors.New("the file is empty")

// parse decodes a configuration file into v, rejecting unknown fields.
func parse(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errEmpty
		}
		return fmt.Errorf("invalid file format: %w", err)
	}
	return nil
}

// SaveInstall writes the installation's configuration file, replacing it atomically.
func SaveInstall(base string, inst *Install) error {
	if inst.Enabled == nil {
		copied := *inst
		copied.Enabled = []string{}
		inst = &copied
	}
	return save(InstallFile(base), inst,
		"# aiplam (AI Playbook Manager) configuration of this base folder.\n"+
			"# types: the agents configuration folders, next to this .aiplam folder, where artifacts are linked from\n"+
			"#   (skills from their skills/ subfolder): "+typeList()+"; change them with 'aiplam type'.\n"+
			"# enabled: the sets whose playbooks are linked; change them with 'aiplam set enable', 'aiplam set disable'\n"+
			"#   and 'aiplam set switch'.\n")
}

// SaveSet writes a set's file, replacing it atomically.
func SaveSet(base, set string, s *Set) error {
	if s.Playbooks == nil {
		s = &Set{Playbooks: []Playbook{}}
	}
	return save(SetFile(base, set), s, fmt.Sprintf(
		"# aiplam (AI Playbook Manager) set '%s'.\n"+
			"# playbooks: registered playbooks, managed with 'aiplam add', 'aiplam update' and 'aiplam remove'.\n", set))
}

func save(file string, v any, header string) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(file)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, file); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// typeList describes the known types and their folders: "agents (.agents), claude (.claude)".
func typeList() string {
	var items []string
	for _, name := range TypeNames() {
		items = append(items, fmt.Sprintf("%s (%s)", name, Types[name]))
	}
	return strings.Join(items, ", ")
}

// TargetDirs returns the agents configuration folders of the configuration's types, in the base folder.
func (c *Install) TargetDirs(base string) []string {
	return TargetDirs(c.Types, base)
}

// TargetDirs returns the agents configuration folders of the given (known) types, in the base folder.
func TargetDirs(types []string, base string) []string {
	var dirs []string
	for _, t := range types {
		if path, ok := Types[t]; ok {
			dirs = append(dirs, filepath.Join(base, filepath.FromSlash(path)))
		}
	}
	return dirs
}

// ValidateTypes checks a list of types and returns every problem found.
func ValidateTypes(types []string) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if len(types) == 0 {
		add("no types: list at least one under 'types' (%s)", typeList())
	}
	for i, t := range types {
		if _, ok := Types[t]; !ok {
			add("unknown type '%s' (known types: %s)", t, typeList())
		}
		for j := 0; j < i; j++ {
			if t == types[j] {
				add("type '%s' is listed more than once", t)
			}
		}
	}
	return errs
}

// Validate checks the installation's configuration and returns every problem found. It does not check that the
// enabled sets exist.
func (c *Install) Validate() []error {
	errs := ValidateTypes(c.Types)
	for i, set := range c.Enabled {
		if !ValidSetName(set) {
			errs = append(errs, fmt.Errorf("enabled set '%s' is not a valid set name", set))
		}
		for j := 0; j < i; j++ {
			if strings.EqualFold(set, c.Enabled[j]) {
				errs = append(errs, fmt.Errorf("set '%s' is listed more than once under 'enabled'", set))
			}
		}
	}
	return errs
}

// IsEnabled reports whether set is one of the enabled sets.
func (c *Install) IsEnabled(base, set string) bool {
	for _, e := range c.Enabled {
		if SameSet(base, e, set) {
			return true
		}
	}
	return false
}

// SkillsPath returns the playbook's skills path, or the default when none is set.
func (p Playbook) SkillsPath() string {
	if p.Artifacts.Skills.Path == "" {
		return DefaultSkillsPath
	}
	return p.Artifacts.Skills.Path
}

// Dir returns the playbook's folder, resolved against the base folder, with links resolved.
func (p Playbook) Dir(base string) string {
	return fsutil.Canonical(filepath.Join(base, filepath.FromSlash(p.Folder)))
}

// SkillsDir returns the folder holding the playbook's skills, with links resolved.
func (p Playbook) SkillsDir(base string) string {
	return fsutil.Canonical(filepath.Join(p.Dir(base), filepath.FromSlash(p.SkillsPath())))
}

// ValidFolder reports whether folder can be a playbook's stored folder: a relative path (from the base folder), in
// slash or native form; it can lead outside the base folder with "..".
func ValidFolder(folder string) bool {
	native := filepath.FromSlash(folder)
	return folder != "" && !filepath.IsAbs(native) && filepath.VolumeName(native) == "" &&
		!strings.HasPrefix(folder, "/") && !strings.HasPrefix(folder, `\`) && folder != "~" &&
		!strings.HasPrefix(folder, "~/") && !strings.HasPrefix(folder, `~\`)
}

// ValidArtifactPath reports whether path can be the location of a kind of artifact inside a playbook: a relative
// path, in slash or native form, that does not lead outside the playbook's folder ("." is the folder itself).
func ValidArtifactPath(path string) bool {
	return path != "" && filepath.IsLocal(filepath.FromSlash(path))
}

// Validate checks the set's content and returns every problem found.
func (s *Set) Validate(base string) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	for i, p := range s.Playbooks {
		label := fmt.Sprintf("playbooks[%d]", i)
		if p.Name != "" {
			label = fmt.Sprintf("playbook '%s'", p.Name)
		}
		switch {
		case p.Name == "":
			add("%s has no name", label)
		case !ValidName(p.Name):
			add("%s: invalid name (use letters, digits, '.', '_' and '-', starting with a letter or digit)", label)
		}
		switch {
		case p.Folder == "":
			add("%s has no folder", label)
		case !ValidFolder(p.Folder):
			add("%s: folder '%s' must be a path relative to the base folder (the folder that holds %s)", label,
				p.Folder, DirName)
		}
		if sp := p.Artifacts.Skills.Path; sp == "" {
			add("%s has no artifacts.skills.path (use '%s' for the default, '.' for the playbook folder itself)", label,
				DefaultSkillsPath)
		} else if !ValidArtifactPath(sp) {
			add("%s: artifacts.skills.path '%s' must be a path inside the playbook folder, relative to it", label, sp)
		}
		for j := 0; j < i; j++ {
			q := s.Playbooks[j]
			if p.Name == "" || p.Folder == "" || p.Name != q.Name {
				continue
			}
			if fsutil.SamePath(p.Dir(base), q.Dir(base)) {
				add("%s is registered more than once for playbook folder '%s'", label, p.Folder)
			} else if fsutil.SameName(filepath.Base(p.Dir(base)), filepath.Base(q.Dir(base))) {
				add("%s is registered for '%s' and '%s', whose last segments are equal, so their ids collide",
					label, q.Folder, p.Folder)
			}
		}
	}
	return errs
}
