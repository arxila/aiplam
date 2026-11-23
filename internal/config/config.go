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

// Package config reads, validates and writes aiplam's configuration, and holds the table of the agents it links
// artifacts for (agents.go). A configuration lives in the .aiplam folder of its base folder (the user's home, or any
// other folder, such as a project's), and its artifacts are linked from the agents configuration folders next to it
// (.agents, .claude, ...):
//
//   - aiplam-config.yaml, the base folder's configuration: the enabled profiles.
//   - <profile>.yaml, one file per profile: the profile's agent and the playbooks registered in it. Only the enabled
//     profiles' playbooks are linked, from the agents configuration folders of their agents.
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
	// BaseConfigName is the name (without extension) of the base folder's configuration file. It cannot be a profile name.
	BaseConfigName = "aiplam-config"
	// FileExt is the extension of the configuration files.
	FileExt = ".yaml"
	// DefaultProfile is the profile that 'aiplam init' creates and enables when no profile is given.
	DefaultProfile = "default"
)

var (
	// ErrNotInitialized is returned by LoadBaseConfig when the base folder's configuration file does not exist.
	ErrNotInitialized = errors.New("not initialized")
	// ErrNoProfile is returned by LoadProfile when the profile's file does not exist.
	ErrNoProfile = errors.New("no such profile")
)

// BaseConfig is the content of aiplam-config.yaml.
type BaseConfig struct {
	// Enabled are the profiles whose playbooks are linked from the agents configuration folders (possibly none).
	Enabled []string `yaml:"enabled"`
}

// Profile is the content of a profile's file.
type Profile struct {
	// Agent is the agent the profile's playbooks are linked for (see AgentTable).
	Agent string `yaml:"agent"`
	// Playbooks are the playbooks registered in the profile.
	Playbooks []Playbook `yaml:"playbooks"`
}

// Playbook is a registered playbook. Its id is not stored: it is computed from its name and folder.
type Playbook struct {
	// Name is the prefix of the playbook's artifacts: they are named "<name>-...".
	Name string `yaml:"name"`
	// Folder is the path of the playbook folder (usually a git checkout), relative to the base folder, in slash
	// form, so that the configuration can be shared (for example, committed to a project's repository).
	Folder string `yaml:"folder"`
	// Artifacts says where each kind of artifact lives in the folder.
	Artifacts Artifacts `yaml:"artifacts,omitempty"`
}

// Artifacts holds the settings of each kind of artifact.
type Artifacts struct {
	Skills       KindSettings `yaml:"skills,omitempty"`
	Subagents    KindSettings `yaml:"subagents,omitempty"`
	Commands     KindSettings `yaml:"commands,omitempty"`
	Prompts      KindSettings `yaml:"prompts,omitempty"`
	Rules        KindSettings `yaml:"rules,omitempty"`
	Instructions KindSettings `yaml:"instructions,omitempty"`
}

// KindSettings holds the settings of a kind of artifact.
type KindSettings struct {
	// Path is the folder, relative to the playbook folder, holding the artifacts of the kind ("." for the folder
	// itself). When it is not set, the folder is named after the subfolder where the profile's agent reads the kind
	// ("agents" for Claude Code's subagents). It cannot lead outside the playbook folder.
	Path string `yaml:"path,omitempty"`
}

// Kind returns the settings of a kind of artifact.
func (a *Artifacts) Kind(k Kind) *KindSettings {
	switch k {
	case Subagents:
		return &a.Subagents
	case Commands:
		return &a.Commands
	case Prompts:
		return &a.Prompts
	case Rules:
		return &a.Rules
	case Instructions:
		return &a.Instructions
	}
	return &a.Skills
}

// Dir returns the path of the configuration folder of a base folder.
func Dir(base string) string {
	return filepath.Join(base, DirName)
}

// BaseConfigFile returns the path of the configuration file of a base folder.
func BaseConfigFile(base string) string {
	return filepath.Join(Dir(base), BaseConfigName+FileExt)
}

// ProfileFile returns the path of a profile's file.
func ProfileFile(base, profile string) string {
	return filepath.Join(Dir(base), profile+FileExt)
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
	nameRegexp        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	profileNameRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

// ValidName reports whether name can be a playbook name: letters, digits, '.', '_' and '-', starting with a letter
// or digit. In particular, it cannot contain '@' (used in ids) or path separators.
func ValidName(name string) bool {
	return nameRegexp.MatchString(name)
}

// ValidProfileName reports whether name can be a profile name: letters, digits, '_' and '-', starting with a letter or
// digit, and not the name of the base folder's configuration file.
func ValidProfileName(name string) bool {
	return profileNameRegexp.MatchString(name) && !strings.EqualFold(name, BaseConfigName)
}

// Profiles returns the names of the existing profiles, sorted.
func Profiles(base string) ([]string, error) {
	items, err := os.ReadDir(Dir(base))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []string
	for _, item := range items {
		name, ok := strings.CutSuffix(item.Name(), FileExt)
		if ok && !item.IsDir() && ValidProfileName(name) {
			profiles = append(profiles, name)
		}
	}
	sort.Strings(profiles)
	return profiles, nil
}

// ProfileExists reports whether a profile's file exists.
func ProfileExists(base, profile string) bool {
	return fsutil.IsFile(ProfileFile(base, profile))
}

// SameProfile reports whether two profile names designate the same profile: equal names, or names of the same file (on
// file systems that ignore case, "Work" and "work" are the same profile).
func SameProfile(base, a, b string) bool {
	if a == b {
		return true
	}
	fa, errA := os.Stat(ProfileFile(base, a))
	fb, errB := os.Stat(ProfileFile(base, b))
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// LoadBaseConfig reads and parses the base folder's configuration file. It returns ErrNotInitialized when the file
// does not exist. Use Validate for semantic checks.
func LoadBaseConfig(base string) (*BaseConfig, error) {
	data, err := os.ReadFile(BaseConfigFile(base))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, err
	}
	conf := &BaseConfig{}
	if err := parse(data, conf); err != nil {
		return nil, err
	}
	return conf, nil
}

// LoadProfile reads and parses a profile's file. It returns ErrNoProfile when the file does not exist. An empty file is
// an empty profile, and a profile without agent has the default agent. Use Validate for semantic checks.
func LoadProfile(base, profile string) (*Profile, error) {
	data, err := os.ReadFile(ProfileFile(base, profile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoProfile
	}
	if err != nil {
		return nil, err
	}
	s := &Profile{}
	if err := parse(data, s); err != nil && !errors.Is(err, errEmpty) {
		return nil, err
	}
	if s.Agent == "" {
		s.Agent = DefaultAgent
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

// SaveBaseConfig writes the base folder's configuration file, replacing it atomically.
func SaveBaseConfig(base string, conf *BaseConfig) error {
	if conf.Enabled == nil {
		copied := *conf
		copied.Enabled = []string{}
		conf = &copied
	}
	return save(BaseConfigFile(base), conf,
		"# aiplam (AI Playbook Manager) configuration of this base folder.\n"+
			"# enabled: the profiles whose playbooks are linked; change them with 'aiplam profile enable',\n"+
			"#   'aiplam profile disable' and 'aiplam profile switch'.\n")
}

// SaveProfile writes a profile's file, replacing it atomically.
func SaveProfile(base, profile string, s *Profile) error {
	if s.Playbooks == nil || s.Agent == "" {
		copied := *s
		if copied.Playbooks == nil {
			copied.Playbooks = []Playbook{}
		}
		if copied.Agent == "" {
			copied.Agent = DefaultAgent
		}
		s = &copied
	}
	return save(ProfileFile(base, profile), s, fmt.Sprintf(
		"# aiplam (AI Playbook Manager) profile '%s'.\n"+
			"# agent: the agent the playbooks are linked for ("+agentList()+"); change it with 'aiplam profile agent'.\n"+
			"# playbooks: registered playbooks, managed with 'aiplam add', 'aiplam update' and 'aiplam remove'; their\n"+
			"#   artifacts.<kind>.path are the folders set with 'aiplam add --<kind>-path' (otherwise, the agent's defaults).\n",
		profile))
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
	// The new content goes to a file next to it, which then replaces it: a crash leaves either the old file or the new
	// one, complete. The temporary file is created with the usual permissions (0644 minus the umask), unlike
	// os.CreateTemp's 0600, and its name includes the process id, so that concurrent runs do not share it.
	tmpName := fmt.Sprintf("%s.%d.tmp", file, os.Getpid())
	tmp, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = tmp.Write(buf.Bytes())
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmpName, file)
	}
	if err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Validate checks the base folder's configuration and returns every problem found. It does not check that the
// enabled profiles exist.
func (c *BaseConfig) Validate() []error {
	var errs []error
	for i, profile := range c.Enabled {
		if !ValidProfileName(profile) {
			errs = append(errs, fmt.Errorf("enabled profile '%s' is not a valid profile name", profile))
		}
		for j := 0; j < i; j++ {
			if strings.EqualFold(profile, c.Enabled[j]) {
				errs = append(errs, fmt.Errorf("profile '%s' is listed more than once under 'enabled'", profile))
			}
		}
	}
	return errs
}

// IsEnabled reports whether profile is one of the enabled profiles.
func (c *BaseConfig) IsEnabled(base, profile string) bool {
	for _, e := range c.Enabled {
		if SameProfile(base, e, profile) {
			return true
		}
	}
	return false
}

// Path returns the playbook's path for a kind of artifact: the one set, or else def (the default of the profile's
// agent, see KindFolder.DefaultPath).
func (p Playbook) Path(k Kind, def string) string {
	if path := p.Artifacts.Kind(k).Path; path != "" {
		return path
	}
	return def
}

// PathSet reports whether the playbook's path for a kind of artifact is set (rather than left to the default of the
// profile's agent).
func (p Playbook) PathSet(k Kind) bool {
	return p.Artifacts.Kind(k).Path != ""
}

// Dir returns the playbook folder, resolved against the base folder, with links resolved.
func (p Playbook) Dir(base string) string {
	return fsutil.Canonical(filepath.Join(base, filepath.FromSlash(p.Folder)))
}

// PathDir returns a folder of the playbook, given relative to its playbook folder, with links resolved.
func (p Playbook) PathDir(base, path string) string {
	return fsutil.Canonical(filepath.Join(p.Dir(base), filepath.FromSlash(path)))
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
// path, in slash or native form, that does not lead outside the playbook folder ("." is the folder itself).
func ValidArtifactPath(path string) bool {
	return path != "" && filepath.IsLocal(filepath.FromSlash(path))
}

// Validate checks the profile's content and returns every problem found.
func (s *Profile) Validate(base string) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if _, ok := LookupAgent(s.Agent); !ok && s.Agent != "" {
		add("unknown agent '%s' (known agents: %s)", s.Agent, agentList())
	}
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
			add("%s has no folder (its playbook folder)", label)
		case !ValidFolder(p.Folder):
			add("%s: playbook folder '%s' must be a path relative to the base folder (the folder that holds %s)", label,
				p.Folder, DirName)
		}
		for _, k := range Kinds {
			if path := p.Artifacts.Kind(k).Path; path != "" && !ValidArtifactPath(path) {
				add("%s: artifacts.%s.path '%s' must be a path inside the playbook folder, relative to it", label, k, path)
			}
		}
		for j := 0; j < i; j++ {
			q := s.Playbooks[j]
			if p.Name == "" || p.Folder == "" || p.Name != q.Name {
				continue
			}
			if fsutil.SamePath(p.Dir(base), q.Dir(base)) {
				add("%s is registered more than once for playbook folder '%s'", label, p.Folder)
			} else if fsutil.SameName(filepath.Base(p.Dir(base)), filepath.Base(q.Dir(base))) {
				add("%s is registered for playbook folders '%s' and '%s', whose last segments are equal, so their ids "+
					"collide",
					label, q.Folder, p.Folder)
			}
		}
	}
	return errs
}
