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

// Package config reads, validates and writes aiplam's configuration, kept in ~/.aiplam:
//
//   - aiplam-config.yaml, the installation's configuration: the target folders where artifacts are installed, and the
//     enabled set.
//   - <set>.yaml, one file per set: the playbooks registered in the set. Only the enabled set's playbooks are
//     installed in the targets.
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
	// DirName is the name of aiplam's folder in the user's home.
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

// Install is the content of aiplam-config.yaml.
type Install struct {
	// Targets are the folders where artifacts are installed (for example ~/.agents and ~/.claude); skills go to their
	// skills/ subfolder. A leading "~" stands for the user's home.
	Targets []string `yaml:"targets"`
	// Enabled is the set whose playbooks are installed in the targets.
	Enabled string `yaml:"enabled"`
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
	// Folder is the absolute path of the playbook's folder (usually a git checkout). A leading "~" stands for the
	// user's home.
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

// Dir returns the path of aiplam's folder for the given home folder.
func Dir(home string) string {
	return filepath.Join(home, DirName)
}

// InstallFile returns the path of the installation's configuration file.
func InstallFile(home string) string {
	return filepath.Join(Dir(home), InstallName+FileExt)
}

// SetFile returns the path of a set's file.
func SetFile(home, set string) string {
	return filepath.Join(Dir(home), set+FileExt)
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
func Sets(home string) ([]string, error) {
	items, err := os.ReadDir(Dir(home))
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
func SetExists(home, set string) bool {
	return fsutil.IsFile(SetFile(home, set))
}

// SameSet reports whether two set names designate the same set: equal names, or names of the same file (on file
// systems that ignore case, "Work" and "work" are the same set).
func SameSet(home, a, b string) bool {
	if a == b {
		return true
	}
	fa, errA := os.Stat(SetFile(home, a))
	fb, errB := os.Stat(SetFile(home, b))
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// LoadInstall reads and parses the installation's configuration file. It returns ErrNotInitialized when the file
// does not exist. Use Validate for semantic checks.
func LoadInstall(home string) (*Install, error) {
	data, err := os.ReadFile(InstallFile(home))
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
func LoadSet(home, set string) (*Set, error) {
	data, err := os.ReadFile(SetFile(home, set))
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
func SaveInstall(home string, inst *Install) error {
	return save(InstallFile(home), inst,
		"# aiplam (AI Playbook Manager) installation configuration.\n"+
			"# targets: folders where artifacts are installed (skills go to their skills/ subfolder); change them with\n"+
			"#   'aiplam target'.\n"+
			"# enabled: the set whose playbooks are installed; change it with 'aiplam set enable'.\n")
}

// SaveSet writes a set's file, replacing it atomically.
func SaveSet(home, set string, s *Set) error {
	if s.Playbooks == nil {
		s = &Set{Playbooks: []Playbook{}}
	}
	return save(SetFile(home, set), s, fmt.Sprintf(
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

// TargetDirs returns the target folders with "~" expanded.
func (c *Install) TargetDirs(home string) []string {
	return TargetDirs(c.Targets, home)
}

// TargetDirs returns the given target folders with "~" expanded.
func TargetDirs(targets []string, home string) []string {
	dirs := make([]string, len(targets))
	for i, t := range targets {
		dirs[i] = filepath.Clean(fsutil.ExpandHome(t, home))
	}
	return dirs
}

// ValidateTargets checks a list of target folders and returns every problem found.
func ValidateTargets(targets []string, home string) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if len(targets) == 0 {
		add("no targets: list at least one folder under 'targets' (for example ~/.agents)")
	}
	for i, t := range targets {
		dir := fsutil.ExpandHome(t, home)
		switch {
		case t == "":
			add("targets[%d] is empty", i)
		case !filepath.IsAbs(dir):
			add("target '%s' is not an absolute path (a leading ~ stands for the home folder)", t)
		}
		for j := 0; j < i; j++ {
			if t != "" && fsutil.SamePath(fsutil.ExpandHome(targets[j], home), dir) {
				add("target '%s' is listed more than once", t)
			}
		}
	}
	return errs
}

// Validate checks the installation's configuration and returns every problem found. It does not check that the
// enabled set exists.
func (c *Install) Validate(home string) []error {
	errs := ValidateTargets(c.Targets, home)
	switch {
	case c.Enabled == "":
		errs = append(errs, errors.New("no enabled set: set 'enabled' to the name of a set"))
	case !ValidSetName(c.Enabled):
		errs = append(errs, fmt.Errorf("enabled set '%s' is not a valid set name", c.Enabled))
	}
	return errs
}

// SkillsPath returns the playbook's skills path, or the default when none is set.
func (p Playbook) SkillsPath() string {
	if p.Artifacts.Skills.Path == "" {
		return DefaultSkillsPath
	}
	return p.Artifacts.Skills.Path
}

// Dir returns the playbook's folder with "~" expanded and links resolved.
func (p Playbook) Dir(home string) string {
	return fsutil.Canonical(fsutil.ExpandHome(p.Folder, home))
}

// SkillsDir returns the folder holding the playbook's skills, with links resolved.
func (p Playbook) SkillsDir(home string) string {
	return fsutil.Canonical(filepath.Join(p.Dir(home), filepath.FromSlash(p.SkillsPath())))
}

// ValidArtifactPath reports whether path can be the location of a kind of artifact inside a playbook: a relative
// path, in slash or native form, that does not lead outside the playbook's folder ("." is the folder itself).
func ValidArtifactPath(path string) bool {
	return path != "" && filepath.IsLocal(filepath.FromSlash(path))
}

// Validate checks the set's content and returns every problem found.
func (s *Set) Validate(home string) []error {
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
		case !filepath.IsAbs(fsutil.ExpandHome(p.Folder, home)):
			add("%s: folder '%s' is not an absolute path (a leading ~ stands for the home folder)", label, p.Folder)
		}
		if sp := p.Artifacts.Skills.Path; sp == "" {
			add("%s has no artifacts.skills.path (use '%s' for the default, '.' for the folder itself)", label,
				DefaultSkillsPath)
		} else if !ValidArtifactPath(sp) {
			add("%s: artifacts.skills.path '%s' must be a path inside the playbook folder, relative to it", label, sp)
		}
		for j := 0; j < i; j++ {
			q := s.Playbooks[j]
			if p.Name == "" || p.Folder == "" || p.Name != q.Name {
				continue
			}
			if fsutil.SamePath(p.Dir(home), q.Dir(home)) {
				add("%s is registered more than once for folder '%s'", label, p.Folder)
			} else if fsutil.SameName(filepath.Base(p.Folder), filepath.Base(q.Folder)) {
				add("%s is registered for '%s' and '%s', whose last segments are equal, so their ids collide",
					label, q.Folder, p.Folder)
			}
		}
	}
	return errs
}
