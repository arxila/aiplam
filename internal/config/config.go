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

// Package config reads, validates and writes aiplam's configuration, and holds the agent templates (templates.go). A
// configuration lives in the .aiplam folder (the aiplam configuration folder) of its managed folder (the user's home
// folder, or any other folder, such as a project's):
//
//   - aiplam-config.yaml, the aiplam configuration: the enabled profiles.
//   - <profile>.yaml, one file per profile: its target folders (where the artifacts of each kind are linked) and the
//     playbooks registered in it. Only the enabled profiles' playbooks are linked.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unicode"

	"go.yaml.in/yaml/v3"

	"arxila.io/aiplam/internal/fsutil"
)

const (
	// DirName is the name of the aiplam configuration folder in a managed folder.
	DirName = ".aiplam"
	// ConfigName is the name (without extension) of the aiplam configuration file. It cannot be a profile name.
	ConfigName = "aiplam-config"
	// FileExt is the extension of the configuration files.
	FileExt = ".yaml"
	// DefaultProfile is the profile that 'aiplam init' creates when no profile is given.
	DefaultProfile = "default"
)

var (
	// ErrNotInitialized is returned by LoadConfig when the aiplam configuration file does not exist.
	ErrNotInitialized = errors.New("not initialized")
	// ErrNoProfile is returned by LoadProfile when the profile's file does not exist.
	ErrNoProfile = errors.New("no such profile")
)

// Config is the content of aiplam-config.yaml.
type Config struct {
	// Enabled are the profiles whose playbooks are linked (possibly none).
	Enabled []string `yaml:"enabled"`
}

// Profile is the content of a profile's file.
type Profile struct {
	// Name is the profile's name, which must match its file name.
	Name string `yaml:"name"`
	// Agent is the template the targets were created from ("claude", "junie:standard"...), or CustomAgent.
	Agent string `yaml:"agent"`
	// Targets are the folders where the artifacts of each kind are linked.
	Targets Targets `yaml:"targets"`
	// Playbooks are the playbooks registered in the profile.
	Playbooks []Playbook `yaml:"playbooks"`
}

// Target is the folder where a profile links the artifacts of a kind.
type Target struct {
	Kind string
	// Folder is relative to the managed folder, in slash form; in the user managed folder, it can start with AppData.
	Folder string
}

// Targets are a profile's targets, in the order of its file. In the file, they are a mapping from kinds to folders.
type Targets []Target

// MarshalYAML writes the targets as a mapping, in order.
func (t Targets) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, tt := range t {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: tt.Kind},
			&yaml.Node{Kind: yaml.ScalarNode, Value: tt.Folder})
	}
	return node, nil
}

// UnmarshalYAML reads the targets from a mapping of kinds to folders, keeping their order.
func (t *Targets) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: targets must be a mapping from artifact kinds to target folders", node.Line)
	}
	*t = nil
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: targets must be a mapping from artifact kinds to target folders", k.Line)
		}
		if v.Tag == "!!null" { // 'kind:', 'kind: null' or 'kind: ~': not a folder named "null"
			return fmt.Errorf("line %d: kind '%s' has no target folder", k.Line, k.Value)
		}
		*t = append(*t, Target{Kind: k.Value, Folder: v.Value})
	}
	return nil
}

// Folder returns the target folder of kind.
func (t Targets) Folder(kind string) (string, bool) {
	for _, tt := range t {
		if tt.Kind == kind {
			return tt.Folder, true
		}
	}
	return "", false
}

// Kinds returns the kinds of the targets, in order.
func (t Targets) Kinds() []string {
	kinds := make([]string, len(t))
	for i, tt := range t {
		kinds[i] = tt.Kind
	}
	return kinds
}

// Same reports whether t and o map the same kinds to the same folders, in any order.
func (t Targets) Same(o Targets) bool {
	if len(t) != len(o) {
		return false
	}
	for _, tt := range t {
		if f, ok := o.Folder(tt.Kind); !ok || f != tt.Folder {
			return false
		}
	}
	return true
}

// Playbook is a registered playbook. Its id is not stored: it is computed from its name and folder.
type Playbook struct {
	// Name is the playbook's name in its profile; the default format of its artifacts is "<name>-*".
	Name string `yaml:"name"`
	// Folder is the path of the playbook folder (usually a git checkout), relative to the managed folder, in slash
	// form, so that the configuration can be shared (for example, committed to a project's repository).
	Folder string `yaml:"folder"`
	// Mappings are the playbook mappings given with 'aiplam add --folder', by kind. Kinds without one use the default
	// (see Source).
	Mappings map[string]Mapping `yaml:"mappings,omitempty"`
}

// Mapping says where the artifacts of a kind are in a playbook folder, and which of its entries they are.
type Mapping struct {
	// Folder is the subfolder of the playbook folder holding the artifacts ("." for the playbook folder itself), in
	// slash form.
	Folder string `yaml:"folder"`
	// Format, when set, is the pattern the artifacts' names match ('*' matches any characters); by default "<name>-*".
	Format string `yaml:"format,omitempty"`
}

// Source returns where the playbook's artifacts of kind are: the subfolder of its playbook folder (by default, named
// like the kind) and the format of their names (by default, "<name>-*").
func (p Playbook) Source(kind string) (folder, format string) {
	folder, format = kind, p.Name+"-*"
	if m, ok := p.Mappings[kind]; ok {
		folder = m.Folder
		if m.Format != "" {
			format = m.Format
		}
	}
	return folder, format
}

// Dir returns the playbook folder, resolved against the managed folder, with links resolved.
func (p Playbook) Dir(managed string) string {
	return fsutil.Canonical(filepath.Join(managed, filepath.FromSlash(p.Folder)))
}

// Dir returns the path of the aiplam configuration folder of a managed folder.
func Dir(managed string) string {
	return filepath.Join(managed, DirName)
}

// ConfigFile returns the path of the aiplam configuration file of a managed folder.
func ConfigFile(managed string) string {
	return filepath.Join(Dir(managed), ConfigName+FileExt)
}

// ProfileFile returns the path of a profile's file.
func ProfileFile(managed, profile string) string {
	return filepath.Join(Dir(managed), profile+FileExt)
}

// IsManaged reports whether dir is a managed folder: it holds a .aiplam folder (whose configuration may still be
// missing).
func IsManaged(dir string) bool {
	return fsutil.IsDir(Dir(dir))
}

// FindManaged returns the managed folder whose configuration applies to start (absolute): start or its nearest parent
// that is a managed folder, stopping at the home folder (only a managed home folder applies there, and folders above
// it are never considered). When the root of the file system is reached without finding one, the home folder applies
// if it is managed. It reports false when no managed folder applies. start is walked up as given (a folder reached
// through a link belongs to the managed folder above the link); the folder found is returned with links resolved.
func FindManaged(start, home string) (string, bool) {
	home = fsutil.Canonical(home)
	for dir := filepath.Clean(start); ; {
		if fsutil.SamePath(fsutil.Canonical(dir), home) {
			return home, IsManaged(home)
		}
		if IsManaged(dir) {
			return fsutil.Canonical(dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return home, IsManaged(home)
}

// GitRepository returns the folder of the git repository that holds dir (the nearest folder from dir up with a .git
// folder or file), or "".
func GitRepository(dir string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// RelFolder returns the path to store for a playbook folder: dir relative to managed, in slash form. It fails when
// there is no relative path (on Windows, when dir is on another drive than managed).
func RelFolder(managed, dir string) (string, error) {
	rel, err := filepath.Rel(managed, dir)
	if err != nil || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("playbook folder '%s' must be on the same drive as the managed folder '%s'", dir,
			managed)
	}
	return filepath.ToSlash(rel), nil
}

// TargetDir returns the folder that a target folder of a profile in managed designates, and the folder above which
// aiplam never removes empty folders after unlinking (managed, or appData for a folder under AppData). appData is the
// value of the APPDATA environment variable.
func TargetDir(managed, folder, appData string) (dir, stop string, err error) {
	// As CleanTarget reads it: a folder written by hand may have backslashes on Windows, or start with './'.
	folder = path.Clean(toSlash(folder))
	if rest, ok := strings.CutPrefix(folder, AppData+"/"); ok {
		if appData == "" {
			return "", "", fmt.Errorf("target folder '%s' needs the APPDATA environment variable, which is not set",
				folder)
		}
		return filepath.Join(appData, filepath.FromSlash(rest)), appData, nil
	}
	return filepath.Join(managed, filepath.FromSlash(folder)), managed, nil
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
// digit, and not the name of the aiplam configuration file.
func ValidProfileName(name string) bool {
	return profileNameRegexp.MatchString(name) && !strings.EqualFold(name, ConfigName)
}

// ValidKind reports whether kind can be the name of a kind of artifact: not empty, not starting with '.', and without
// ':', ',', quotes, path separators or whitespace.
func ValidKind(kind string) bool {
	return kind != "" && !strings.HasPrefix(kind, ".") && !strings.ContainsAny(kind, ":,/\\\"' \t\r\n")
}

// toSlash converts a path given in native form to slash form.
func toSlash(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return p
}

// relative checks that p (in slash form) is a relative path without '..' segments.
func relative(p string) error {
	native := filepath.FromSlash(p)
	if path.IsAbs(p) || filepath.IsAbs(native) || filepath.VolumeName(native) != "" || p == "~" ||
		strings.HasPrefix(p, "~/") {
		return errors.New("must be a relative path")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return errors.New("cannot contain '..'")
		}
	}
	return nil
}

// CleanTarget checks a target folder and returns it cleaned, in slash form. It must be relative to the managed
// folder, inside it, other than the managed folder itself and outside its .aiplam folder; in the user managed folder
// (user), it can instead be under the Windows %APPDATA% folder, written as AppData + "/...".
func CleanTarget(folder string, user bool) (string, error) {
	f := toSlash(folder)
	if f == "" {
		return "", errors.New("the target folder is empty")
	}
	if HasControl(f) {
		return "", fmt.Errorf("target folder %q cannot contain control characters, such as line breaks", folder)
	}
	if err := relative(f); err != nil {
		return "", fmt.Errorf("target folder '%s' %s", folder, err)
	}
	// Cleaned first, so that %APPDATA% is recognized however it is written ('./%APPDATA%/...' too).
	c, prefix := path.Clean(f), ""
	if first, rest, _ := strings.Cut(c, "/"); first == AppData {
		if !user {
			return "", fmt.Errorf("target folder '%s': %s can only be used in the user managed folder", folder, AppData)
		}
		prefix, c = AppData+"/", rest
	}
	switch {
	case c == "." || c == "":
		return "", fmt.Errorf("target folder '%s' cannot be the managed folder itself", folder)
	case !filepath.IsLocal(filepath.FromSlash(c)):
		return "", fmt.Errorf("target folder '%s' is not a valid folder name", folder)
	case prefix == "" && strings.EqualFold(strings.Split(c, "/")[0], DirName):
		return "", fmt.Errorf("target folder '%s' cannot be inside %s", folder, DirName)
	}
	return prefix + c, nil
}

// CleanSubfolder checks a playbook subfolder (relative to its playbook folder, without '..'; "." is the playbook
// folder itself) and returns it cleaned, in slash form.
func CleanSubfolder(folder string) (string, error) {
	f := toSlash(folder)
	if f == "" {
		return "", errors.New("the playbook subfolder is empty ('.' is the playbook folder itself)")
	}
	if HasControl(f) {
		return "", fmt.Errorf("playbook subfolder %q cannot contain control characters, such as line breaks", folder)
	}
	if err := relative(f); err != nil {
		return "", fmt.Errorf("playbook subfolder '%s' %s", folder, err)
	}
	c := path.Clean(f)
	if !filepath.IsLocal(filepath.FromSlash(c)) {
		return "", fmt.Errorf("playbook subfolder '%s' is not a valid folder name", folder)
	}
	return c, nil
}

// HasControl reports whether s contains a control character (such as a line break or a tab), which no folder, format
// or artifact name aiplam handles may contain: it could not be shown, nor written in a .gitignore line, reliably.
func HasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// CheckFormat checks a format: a pattern for the names of a playbook's artifacts, where '*' matches any characters.
func CheckFormat(format string) error {
	switch {
	case format == "":
		return errors.New("the format is empty")
	case HasControl(format):
		return fmt.Errorf("format %q cannot contain control characters, such as line breaks", format)
	case strings.ContainsAny(format, `/\`):
		return fmt.Errorf("format '%s' cannot contain path separators: it matches the names of files and folders", format)
	case strings.HasPrefix(format, "."):
		return fmt.Errorf("format '%s' would only match hidden files and folders, which are never artifacts", format)
	}
	return nil
}

// MatchFormat reports whether name matches format, where '*' matches zero or more characters and every other
// character matches itself (ignoring case on Windows, like file names there). Hidden names (starting with '.') never
// match.
func MatchFormat(format, name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	if runtime.GOOS == "windows" {
		format, name = strings.ToLower(format), strings.ToLower(name)
	}
	parts := strings.Split(format, "*")
	if len(parts) == 1 {
		return name == format
	}
	rest, ok := strings.CutPrefix(name, parts[0])
	if !ok {
		return false
	}
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, p)
		if i < 0 {
			return false
		}
		rest = rest[i+len(p):]
	}
	return len(rest) >= len(last) && strings.HasSuffix(rest, last)
}

// Profiles returns the names of the existing profiles, sorted.
func Profiles(managed string) ([]string, error) {
	items, err := os.ReadDir(Dir(managed))
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

// ProfileExists reports whether a profile's file exists: anything at its path, even a broken link, so that no command
// takes its name for a free one.
func ProfileExists(managed, profile string) bool {
	_, err := os.Lstat(ProfileFile(managed, profile))
	return err == nil
}

// ProfileName returns the name of the file of a profile designated by name: name itself when a file has exactly that
// name, or else the name of the file that name designates on a file system that ignores case ("work" for "Work" when
// the file is work.yaml). It returns name when there is no such file.
func ProfileName(managed, name string) string {
	if items, err := os.ReadDir(Dir(managed)); err == nil {
		for _, item := range items {
			if item.Name() == name+FileExt {
				return name
			}
		}
		for _, item := range items {
			if other, ok := strings.CutSuffix(item.Name(), FileExt); ok && strings.EqualFold(other, name) &&
				SameProfile(managed, other, name) {
				return other
			}
		}
	}
	return name
}

// SameProfile reports whether two profile names designate the same profile: equal names, or names of the same file (on
// file systems that ignore case, "Work" and "work" are the same profile). The files are compared themselves, not
// what they lead to when they are links (even broken ones).
func SameProfile(managed, a, b string) bool {
	if a == b {
		return true
	}
	fa, errA := os.Lstat(ProfileFile(managed, a))
	fb, errB := os.Lstat(ProfileFile(managed, b))
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// LoadConfig reads and parses the aiplam configuration file. It returns ErrNotInitialized when the file does not
// exist. Use Validate for semantic checks.
func LoadConfig(managed string) (*Config, error) {
	data, err := os.ReadFile(ConfigFile(managed))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, err
	}
	conf := &Config{}
	if err := parse(data, conf); err != nil && !errors.Is(err, errEmpty) {
		return nil, err
	}
	return conf, nil
}

// LoadProfile reads and parses a profile's file. It returns ErrNoProfile when the file does not exist. Use Validate
// for semantic checks.
func LoadProfile(managed, profile string) (*Profile, error) {
	data, err := os.ReadFile(ProfileFile(managed, profile))
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
	return s, nil
}

var errEmpty = errors.New("the file is empty")

// parse decodes a configuration file into v, rejecting unknown fields, and files with more than one YAML document
// (saving the file would lose those after the first).
func parse(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errEmpty
		}
		return fmt.Errorf("invalid file format: %w", err)
	}
	for {
		var more yaml.Node
		err := dec.Decode(&more)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil || !emptyDocument(&more) {
			return errors.New("invalid file format: the file holds more than one YAML document ('---' starts another)")
		}
	}
}

// emptyDocument reports whether a decoded YAML document holds nothing, as after a final '---'.
func emptyDocument(doc *yaml.Node) bool {
	if len(doc.Content) == 0 {
		return true
	}
	n := doc.Content[0]
	return len(doc.Content) == 1 && n.Kind == yaml.ScalarNode && n.Tag == "!!null" && n.Value == ""
}

// SaveConfig writes the aiplam configuration file, replacing it atomically.
func SaveConfig(managed string, conf *Config) error {
	if conf.Enabled == nil {
		copied := *conf
		copied.Enabled = []string{}
		conf = &copied
	}
	return save(managed, ConfigFile(managed), conf,
		"# aiplam (AI Playbook Manager) configuration of this managed folder.\n"+
			"# enabled: the profiles whose playbooks are linked; change them with 'aiplam profile enable',\n"+
			"#   'aiplam profile disable' and 'aiplam profile switch'.\n")
}

// SaveProfile writes a profile's file, replacing it atomically.
func SaveProfile(managed string, s *Profile) error {
	if s.Playbooks == nil || s.Targets == nil {
		copied := *s
		if copied.Playbooks == nil {
			copied.Playbooks = []Playbook{}
		}
		if copied.Targets == nil {
			copied.Targets = Targets{}
		}
		s = &copied
	}
	return save(managed, ProfileFile(managed, s.Name), s, fmt.Sprintf(
		"# aiplam (AI Playbook Manager) profile '%s'.\n"+
			"# agent: the agent template the target folders were taken from, or '%s' if they were given by hand.\n"+
			"#   aiplam only uses it to warn ('aiplam status', 'aiplam list') when the target folders no longer match\n"+
			"#   the template; set it to '%s' when changing them on purpose.\n"+
			"# targets: for each artifact kind, the target folder (relative to the managed folder) where its artifacts\n"+
			"#   are linked.\n"+
			"# playbooks: registered playbooks, managed with 'aiplam add', 'aiplam update' and 'aiplam remove'; their\n"+
			"#   mappings are those given with 'aiplam add --folder' (other kinds use the subfolder named like the kind,\n"+
			"#   and the format '<name>-*').\n",
		s.Name, CustomAgent, CustomAgent))
}

// CheckWritable returns an error when saving file, a configuration file of the managed folder managed, would be
// refused (see writePath). Commands check it before changing any link, so that saving does not fail afterwards for
// this.
func CheckWritable(managed, file string) error {
	_, err := writePath(managed, file)
	return err
}

// writePath returns the file that saving file writes: file itself when it is a regular file, or nothing is there; or,
// when it is a link (as dotfiles managers such as GNU Stow, dotbot or rcm make them), the file it leads to, so that
// the link stays, as long as that file is inside the managed folder, where aiplam keeps everything it writes, is a
// regular file that exists (a link that leads nowhere for now may have lost what it went through), and is not a file
// of another managed folder's configuration. Anything else is refused.
func writePath(managed, file string) (string, error) {
	info, err := os.Lstat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return file, nil
	case err != nil:
		return "", err
	case info.Mode().IsRegular():
		return file, nil
	case info.Mode()&os.ModeSymlink == 0:
		return "", fmt.Errorf("'%s' is not a regular file: aiplam only replaces regular files in %s", file, DirName)
	}
	target, err := fsutil.Resolve(file)
	if err != nil {
		return "", fmt.Errorf("'%s' is a link that cannot be resolved: %w", file, err)
	}
	top, err := fsutil.Resolve(managed)
	if err != nil {
		return "", err
	}
	if !fsutil.Inside(target, top) {
		return "", fmt.Errorf("'%s' is a link leading outside the managed folder '%s', to '%s': aiplam only writes "+
			"inside it; keep the file it leads to inside the managed folder, or replace the link with the file itself",
			file, managed, target)
	}
	if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("'%s' is a link to '%s', which is not a regular file that exists", file, target)
	}
	// Every file the link goes through on its way, and the one it leads to: none may be another managed folder's.
	hops, err := linkHops(file)
	if err != nil {
		return "", fmt.Errorf("'%s' is a link that cannot be resolved: %w", file, err)
	}
	for _, hop := range append(hops, target) {
		if err := otherConfiguration(managed, hop); err != nil {
			return "", fmt.Errorf("'%s' is a link to '%s': %w", file, hop, err)
		}
	}
	return target, nil
}

// linkHops returns the files that the link file leads to in turn, one link after the other, each as the system reads
// the link (see fsutil.Inspect: its folder resolved, a '..' applied where the system applies it), until one that is
// not a link (which is not returned).
func linkHops(file string) ([]string, error) {
	var hops []string
	for p := file; len(hops) < 255; {
		state, next, err := fsutil.Inspect(p)
		if err != nil {
			return nil, err
		}
		if state != fsutil.Link {
			return hops, nil
		}
		p = next
		hops = append(hops, p)
	}
	return nil, errors.New("too many links")
}

// otherConfiguration fails when target lies in a folder holding another managed folder's configuration file: the
// configuration folder of a managed folder nested in managed (whatever its name, as its .aiplam folder may be a link to
// it), whose commands use it as theirs. A folder without a configuration file is no managed folder's (a dotfiles
// folder holding profiles, in GNU Stow's layout), and neither is one whose configuration file is managed's own (the
// dotfiles folder that managed's configuration files are links to, or managed's own .aiplam folder).
func otherConfiguration(managed, target string) error {
	dir := filepath.Dir(target)
	otherConf, err := os.Stat(filepath.Join(dir, ConfigName+FileExt))
	if err != nil || !otherConf.Mode().IsRegular() { // a folder with that name is no configuration file
		return nil
	}
	if ownConf, err := os.Stat(ConfigFile(managed)); err == nil && os.SameFile(ownConf, otherConf) {
		return nil
	}
	return fmt.Errorf("it is in '%s', which holds the configuration of another managed folder", dir)
}

func save(managed, file string, v any, header string) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := writePath(managed, file)
	if err != nil {
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
	// The new content goes to a temporary file, which then replaces the file: a crash leaves either the old file or the
	// new one, complete. The temporary file is in the .aiplam folder (whose .gitignore covers such files), also when
	// file is the file a link leads to (which so stays a link) elsewhere; but next to that file when it cannot be moved
	// from there because it is on another disk (and only then).
	data := buf.Bytes()
	err = replace(filepath.Join(Dir(managed), fmt.Sprintf("%s.%d.tmp", filepath.Base(file), os.Getpid())), file, data)
	if err != nil && crossDevice(err) {
		err = replace(fmt.Sprintf("%s.%d.tmp", file, os.Getpid()), file, data)
	}
	return err
}

// crossDevice reports whether err is that of moving a file to another disk (EXDEV; ERROR_NOT_SAME_DEVICE on Windows).
func crossDevice(err error) bool {
	if runtime.GOOS == "windows" {
		return errors.Is(err, syscall.Errno(17))
	}
	return errors.Is(err, syscall.EXDEV)
}

// replace writes data to the temporary file tmpName and moves it to file. The temporary file is created with the usual
// permissions (0644 minus the umask), unlike os.CreateTemp's 0600, and then given those of the file it replaces, if
// any, so that a file made private stays so. Its name includes the process id, so that concurrent runs do not share
// it: a file with that name can only be left by a process that crashed (with a process id since reused), and is
// replaced.
func replace(tmpName, file string, data []byte) error {
	_ = os.Remove(tmpName)
	tmp, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if old, serr := os.Stat(file); serr == nil && old.Mode().IsRegular() {
		err = tmp.Chmod(old.Mode().Perm())
	}
	if err == nil {
		_, err = tmp.Write(data)
	}
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

// Validate checks the aiplam configuration and returns every problem found. It does not check that the enabled
// profiles exist.
func (c *Config) Validate() []error {
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
func (c *Config) IsEnabled(managed, profile string) bool {
	for _, e := range c.Enabled {
		if SameProfile(managed, e, profile) {
			return true
		}
	}
	return false
}

// ValidFolder reports whether folder can be a playbook's stored folder: a relative path (from the managed folder), in
// slash or native form; it can lead outside the managed folder with "..".
func ValidFolder(folder string) bool {
	native := filepath.FromSlash(folder)
	return folder != "" && !filepath.IsAbs(native) && filepath.VolumeName(native) == "" &&
		!strings.HasPrefix(folder, "/") && !strings.HasPrefix(folder, `\`) && folder != "~" &&
		!strings.HasPrefix(folder, "~/") && !strings.HasPrefix(folder, `~\`)
}

// CheckTargets checks a profile's targets (user is set for the user managed folder) and returns every problem found:
// invalid kinds or folders, kinds given twice, and folders that are the same as, or inside, another.
func CheckTargets(targets Targets, user bool) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	clean := make([]string, len(targets))
	for i, t := range targets {
		if !ValidKind(t.Kind) {
			add("invalid kind '%s' (not starting with '.', and without ':', ',', quotes, path separators or whitespace)",
				t.Kind)
		}
		c, err := CleanTarget(t.Folder, user)
		if err != nil {
			add("%s: %s", t.Kind, err)
			continue
		}
		clean[i] = c
		for j := 0; j < i; j++ {
			switch {
			case targets[j].Kind == t.Kind:
				add("kind '%s' has more than one target folder", t.Kind)
			case clean[j] == "":
			case clean[j] == c:
				add("kinds '%s' and '%s' have the same target folder '%s'", targets[j].Kind, t.Kind, c)
			case strings.HasPrefix(c+"/", clean[j]+"/") || strings.HasPrefix(clean[j]+"/", c+"/"):
				add("the target folders of kinds '%s' and '%s' ('%s' and '%s') are one inside the other",
					targets[j].Kind, t.Kind, clean[j], c)
			}
		}
	}
	return errs
}

// Validate checks the profile's content and returns every problem found. managed is its managed folder, profile the
// name of its file (see ProfileName), and user is set for the user managed folder.
func (s *Profile) Validate(managed, profile string, user bool) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	switch {
	case s.Name == "":
		add("the profile has no name (it must be '%s', like its file)", profile)
	case s.Name != profile:
		add("name '%s' does not match the file's name: it must be '%s'", s.Name, profile)
	}
	if _, _, ok := LookupTemplate(s.Agent); !ok && s.Agent != CustomAgent {
		if s.Agent == "" {
			add("the profile has no agent ('%s', or one of the agent templates: %s)", CustomAgent, TemplateList())
		} else {
			add("unknown agent '%s' (use '%s', or one of the agent templates: %s)", s.Agent, CustomAgent, TemplateList())
		}
	}
	for _, err := range CheckTargets(s.Targets, user) {
		add("targets: %s", err)
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
			add("%s: playbook folder '%s' must be a path relative to the managed folder (the folder that holds %s)",
				label, p.Folder, DirName)
		}
		kinds := make([]string, 0, len(p.Mappings))
		for kind := range p.Mappings {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			m := p.Mappings[kind]
			if _, ok := s.Targets.Folder(kind); !ok {
				add("%s: mappings: '%s' is not a kind of the profile's target folders (%s)", label, kind,
					strings.Join(s.Targets.Kinds(), ", "))
			}
			if _, err := CleanSubfolder(m.Folder); err != nil {
				add("%s: mappings: %s: %s", label, kind, err)
			}
			if m.Format != "" {
				if err := CheckFormat(m.Format); err != nil {
					add("%s: mappings: %s: %s", label, kind, err)
				}
			}
		}
		for j := 0; j < i; j++ {
			q := s.Playbooks[j]
			if p.Name == "" || p.Folder == "" || p.Name != q.Name {
				continue
			}
			if fsutil.SamePath(p.Dir(managed), q.Dir(managed)) {
				add("%s is registered more than once for playbook folder '%s'", label, p.Folder)
			} else if fsutil.SameName(filepath.Base(p.Dir(managed)), filepath.Base(q.Dir(managed))) {
				add("%s is registered for playbook folders '%s' and '%s', whose last segments are equal, so their ids "+
					"collide", label, q.Folder, p.Folder)
			}
		}
	}
	return errs
}
