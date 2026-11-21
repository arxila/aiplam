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

// Package config reads, validates and writes aiplam's configuration. The configuration is split into sets: each set is
// an independent configuration (target folders where artifacts are installed, and registered playbooks) stored in its
// own file, ~/.aiplam/<set>.yaml. Commands act on the "default" set unless told otherwise.
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
	// FileExt is the extension of the set configuration files inside DirName.
	FileExt = ".yaml"
	// DefaultSet is the set that commands act on when no set is given.
	DefaultSet = "default"
	// DefaultSkillsPath is the folder, relative to a playbook's folder, where its skills live by default.
	DefaultSkillsPath = "skills"
)

// ErrNotInitialized is returned by Load when the set's configuration file does not exist.
var ErrNotInitialized = errors.New("not initialized")

// Config is the content of a set's configuration file.
type Config struct {
	// Targets are the folders where artifacts are installed (for example ~/.agents and ~/.claude); skills go to their
	// skills/ subfolder. A leading "~" stands for the user's home.
	Targets []string `yaml:"targets"`
	// Playbooks are the registered playbooks.
	Playbooks []Playbook `yaml:"playbooks,omitempty"`
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

// File returns the path of a set's configuration file for the given home folder.
func File(home, set string) string {
	return filepath.Join(Dir(home), set+FileExt)
}

var (
	nameRegexp    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	setNameRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

// ValidSetName reports whether name can be a set name: letters, digits, '_' and '-', starting with a letter or digit.
func ValidSetName(name string) bool {
	return setNameRegexp.MatchString(name)
}

// Sets returns the names of the sets that have a configuration file, sorted.
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

// ValidName reports whether name can be a playbook name: letters, digits, '.', '_' and '-', starting with a letter
// or digit. In particular, it cannot contain '@' (used in ids) or path separators.
func ValidName(name string) bool {
	return nameRegexp.MatchString(name)
}

// Load reads and parses a set's configuration file. It returns ErrNotInitialized when the file does not exist. Use
// Validate for semantic checks.
func Load(home, set string) (*Config, error) {
	data, err := os.ReadFile(File(home, set))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse parses the content of a configuration file, rejecting unknown fields.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the configuration file is empty")
		}
		return nil, fmt.Errorf("invalid configuration file format: %w", err)
	}
	return cfg, nil
}

// Save writes a set's configuration file, replacing it atomically.
func Save(home, set string, cfg *Config) error {
	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# aiplam (AI Playbook Manager) configuration of set '%s'.\n", set)
	buf.WriteString("# targets: folders where artifacts are installed (skills go to their skills/ subfolder).\n")
	buf.WriteString("# playbooks: registered playbooks, managed with 'aiplam add', 'aiplam update' and 'aiplam remove'.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, set+FileExt+".*.tmp")
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
	if err := os.Rename(tmpName, File(home, set)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// TargetDirs returns the target folders with "~" expanded.
func (c *Config) TargetDirs(home string) []string {
	dirs := make([]string, len(c.Targets))
	for i, t := range c.Targets {
		dirs[i] = filepath.Clean(fsutil.ExpandHome(t, home))
	}
	return dirs
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

// Validate checks the configuration's content and returns every problem found.
func (c *Config) Validate(home string) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if len(c.Targets) == 0 {
		add("no targets: list at least one folder under 'targets' (for example ~/.agents)")
	}
	for i, t := range c.Targets {
		dir := fsutil.ExpandHome(t, home)
		switch {
		case t == "":
			add("targets[%d] is empty", i)
		case !filepath.IsAbs(dir):
			add("target '%s' is not an absolute path (a leading ~ stands for the home folder)", t)
		}
		for j := 0; j < i; j++ {
			if t != "" && fsutil.SamePath(fsutil.ExpandHome(c.Targets[j], home), dir) {
				add("target '%s' is listed more than once", t)
			}
		}
	}

	for i, p := range c.Playbooks {
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
			q := c.Playbooks[j]
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

// SharedTarget is a target folder that two sets list.
type SharedTarget struct {
	Target string // the target folder, with "~" expanded
	Set    string // the other set that lists it
}

// SharedTargets returns the targets of cfg (the configuration of set) that other sets also list. Sets cannot share
// targets: their links would get in each other's way. Other sets whose files cannot be read or parsed are skipped.
func SharedTargets(home, set string, cfg *Config) ([]SharedTarget, error) {
	sets, err := Sets(home)
	if err != nil {
		return nil, err
	}
	own, _ := os.Stat(File(home, set))
	var shared []SharedTarget
	for _, other := range sets {
		if other == set {
			continue
		}
		// On file systems that ignore case (macOS, Windows), "Work" and "work" are the same set.
		if fi, err := os.Stat(File(home, other)); err == nil && own != nil && os.SameFile(own, fi) {
			continue
		}
		otherCfg, err := Load(home, other)
		if err != nil {
			continue
		}
		for _, t := range cfg.TargetDirs(home) {
			for _, o := range otherCfg.TargetDirs(home) {
				if fsutil.SamePath(t, o) {
					shared = append(shared, SharedTarget{Target: t, Set: other})
				}
			}
		}
	}
	return shared, nil
}
