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

// Package app implements aiplam's command line: argument parsing, the commands and their output.
package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/gitinfo"
	"arxila.io/aiplam/internal/playbook"
)

// Version is aiplam's version. The Makefile reads it from here; it can be overridden at build time with
// -ldflags "-X arxila.io/aiplam/internal/app.Version=...".
var Version = "0.1.0"

// Copyright is the copyright line shown in the help.
const Copyright = "(c) 2026 Arxila OSS - https://aiplam.arxila.io"

// Exit codes.
const (
	ExitOK      = 0
	ExitFailure = 1 // an error, a cancelled operation, or (check) something needs attention
	ExitUsage   = 2 // invalid command line
)

// App runs aiplam commands. Its fields make it testable without touching the real home folder.
type App struct {
	// Home is the user's home folder: a leading "~" stands for it, and it is the base folder used when no other is
	// found.
	Home string
	// Cwd is the current folder, where the search for a base folder starts.
	Cwd    string
	Stdout io.Writer
	Stderr io.Writer
	Git    *gitinfo.Reader
	// Profile is the profile given with the --profile option, or "" (commands then act on the enabled profile). Run
	// sets it.
	Profile string
	// Base is the base folder of the configuration commands act on (it holds the .aiplam folder and the agents
	// configuration folders). Run sets it.
	Base string

	baseArg     string // the folder given with --base, or ""
	global      bool   // whether --global was given
	baseShown   bool   // whether the "Base folder: ..." line has been printed
	ancestors   map[string][]string
	gitWarned   bool
	hintProfile string // the profile to name with --profile in suggested commands, when it is not the enabled one
}

// New returns an App for the current user, working directory and standard streams.
func New() (*App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine the home folder: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine the current folder: %w", err)
	}
	// Resolve links (for example /var -> /private/var on macOS) so that home matches the canonical paths of the
	// folders under it.
	return &App{Home: fsutil.Canonical(home), Cwd: cwd, Stdout: os.Stdout, Stderr: os.Stderr,
		Git: gitinfo.NewReader()}, nil
}

type command struct {
	name    string
	summary string
	usage   string
	run     func(a *App, args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"init", "set up a base folder: its .aiplam folder, agents configuration folders and first profile", initUsage,
			(*App).cmdInit},
		{"add", "register a playbook and link its artifacts", addUsage, (*App).cmdAdd},
		{"update", "link new artifacts of a playbook and unlink deleted ones", updateUsage, (*App).cmdUpdate},
		{"remove", "unlink a playbook's artifacts and unregister it", removeUsage, (*App).cmdRemove},
		{"list", "list registered playbooks, their versions and artifacts", listUsage, (*App).cmdList},
		{"check", "check the configuration and whether any update is needed", checkUsage, (*App).cmdCheck},
		{"profile", "manage profiles (groups of playbooks): create, remove, enable, disable, switch", profileUsage,
			(*App).cmdProfile},
		{"type", "show or change the types of agents configuration folders", typeUsage, (*App).cmdType},
	}
}

func (a *App) mainUsage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "aiplam %s - AI Playbook Manager\n%s\n\n", Version, Copyright)
	b.WriteString(`Links the artifacts of AI playbooks (git checkouts shared by a team), such as skills, from the agents
configuration folders that coding agents read (.agents, .claude): symbolic links, or directory junctions on Windows.

Usage: aiplam <command> [arguments]

Commands:
`)
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-8s %s\n", c.name, c.summary)
	}
	b.WriteString(`
How it works:
  - a base folder is the home folder, or any folder where agent sessions start (such as a project's top folder),
    that holds:
      - a .aiplam folder, with aiplam's configuration for that base folder;
      - agents configuration folders, the ones agents read: .agents, .claude;
  - commands use the nearest base folder from the current folder up, or else the home folder;
  - aiplam links the playbooks' artifacts from the agents configuration folders of the chosen types, so updating
    a playbook only takes switching its checkout to another tag and running 'aiplam update';
  - playbooks are registered in profiles, and only the playbooks of the enabled profiles are linked.

Options for every command:
  -b, --base <basefolder>   use <basefolder> as base folder
  -g, --global              use the user's home folder (e.g. /home/username) as base folder
Option for add, update, remove, list and check:
  -p, --profile PROFILE     act on profile PROFILE instead of the enabled ones

Getting started:
  aiplam init --global                                # ~ as base folder: ~/.aiplam and ~/.agents
  aiplam add arxila ~/Workspace/arxila-ai-playbook    # register a playbook and link its artifacts
  aiplam list

Run 'aiplam <command> --help' for a command's help, and 'aiplam --version' for the version.
`)
	return b.String()
}

// Run runs the command line args (without the program name) and returns the exit code.
func (a *App) Run(args []string) int {
	args, opts, err := extractGlobal(args)
	if err != nil {
		return a.exitCode(err, a.mainUsage())
	}
	a.Profile, a.baseArg, a.global = opts.profile, opts.base, opts.global
	a.Base, a.baseShown, a.ancestors, a.hintProfile = "", false, nil, ""
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, a.mainUsage())
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		if len(args) > 1 {
			for _, c := range commands {
				if c.name == args[1] {
					fmt.Fprint(a.Stdout, c.usage)
					return ExitOK
				}
			}
		}
		fmt.Fprint(a.Stdout, a.mainUsage())
		return ExitOK
	case "-v", "--version", "version":
		fmt.Fprintf(a.Stdout, "aiplam %s\n", Version)
		return ExitOK
	}
	for _, c := range commands {
		if c.name != args[0] {
			continue
		}
		for _, arg := range args[1:] {
			if arg == "--" {
				break
			}
			if arg == "-h" || arg == "--help" {
				fmt.Fprint(a.Stdout, c.usage)
				return ExitOK
			}
		}
		if a.baseArg != "" && a.global {
			return a.exitCode(usagef("--base and --global cannot be used together"), c.usage)
		}
		if c.name != "init" {
			if err := a.resolveBase(); err != nil {
				return a.exitCode(err, c.usage)
			}
		}
		return a.exitCode(c.run(a, args[1:]), c.usage)
	}
	a.errorf("unknown command: %s", args[0])
	fmt.Fprint(a.Stderr, "\n"+a.mainUsage())
	return ExitUsage
}

// globalOptions are the options that Run removes from the command line before running a command.
type globalOptions struct {
	profile string // --profile/-p, or ""
	base    string // --base/-b, or ""
	global  bool   // --global/-g
}

// extractGlobal removes the --profile/-p, --base/-b and --global/-g options (allowed anywhere before "--") from args
// and returns them.
func extractGlobal(args []string) ([]string, globalOptions, error) {
	var opts globalOptions
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if arg == "--global" || arg == "-g" {
			opts.global = true
			continue
		}
		var name, value string
		switch {
		case arg == "--profile" || arg == "-p" || arg == "--base" || arg == "-b":
			if i+1 >= len(args) {
				return nil, opts, usagef("option %s needs a value", arg)
			}
			i++
			name, value = arg, args[i]
		default:
			found := false
			for _, prefix := range []string{"--profile=", "-p=", "--base=", "-b="} {
				if v, ok := strings.CutPrefix(arg, prefix); ok {
					name, value, found = strings.TrimSuffix(prefix, "="), v, true
					break
				}
			}
			if !found {
				rest = append(rest, arg)
				continue
			}
		}
		switch name {
		case "--profile", "-p":
			if !config.ValidProfileName(value) {
				return nil, opts, invalidProfileName(value)
			}
			opts.profile = value
		default:
			if value == "" {
				return nil, opts, usagef("option %s needs a base folder", name)
			}
			opts.base = value
		}
	}
	return rest, opts, nil
}

// resolveBase sets a.Base: the folder given with --base (which must hold a configuration), the home folder with
// --global, or else the nearest folder holding a configuration from the current folder up, or the home folder.
func (a *App) resolveBase() error {
	switch {
	case a.baseArg != "":
		dir := a.absPath(a.baseArg)
		if !config.IsBase(dir) {
			return fmt.Errorf("'%s' is not a base folder (%s is missing); set it up with 'aiplam init %s'",
				a.baseArg, a.pretty(config.Dir(dir)), a.baseArg)
		}
		a.Base = dir
	case a.global:
		a.Base = a.Home
	default:
		a.Base = config.FindBase(a.absPathAsGiven(a.Cwd), a.Home)
	}
	return nil
}

// showBase prints, once per command, the base folder whose configuration the command uses.
func (a *App) showBase() {
	if !a.baseShown {
		a.baseShown = true
		a.printf("Base folder: %s\n", a.pretty(a.Base))
	}
}

// baseOption returns the option that selects the base folder in suggested commands: the one given, if any.
func (a *App) baseOption() string {
	switch {
	case a.baseArg != "":
		return " --base " + a.pretty(a.Base)
	case a.global:
		return " --global"
	}
	return ""
}

// ancestorAgentsConfigDirs maps each agents configuration folder the base folder can have (one per type) to the agents
// configuration folders of the same type of the higher-level base folders (see config.Ancestors), which agents may
// read besides the base folder's: artifacts in them cannot be linked from its folder of that type. A configuration
// that cannot be read is skipped with a warning.
func (a *App) ancestorAgentsConfigDirs() map[string][]string {
	if a.ancestors != nil {
		return a.ancestors
	}
	a.ancestors = map[string][]string{}
	for _, r := range config.Ancestors(a.Base, a.Home) {
		conf, err := config.LoadBaseConfig(r)
		if err == nil && len(conf.Validate()) > 0 {
			err = errors.New("its content is not valid")
		}
		if err != nil {
			a.warnf("the configuration of base folder '%s' cannot be read (%s): its agents configuration folders are not "+
				"checked for artifacts already linked from them.", a.pretty(r), err)
			continue
		}
		for _, t := range conf.Types {
			own := config.AgentsConfigDirs([]string{t}, a.Base)[0]
			a.ancestors[own] = append(a.ancestors[own], config.AgentsConfigDirs([]string{t}, r)[0])
		}
	}
	return a.ancestors
}

// errSilent signals a failure whose messages have already been printed.
var errSilent = errors.New("failed")

func (a *App) exitCode(err error, usage string) int {
	if err == nil {
		return ExitOK
	}
	var ue *usageError
	if errors.As(err, &ue) {
		a.errorf("%s", ue.msg)
		fmt.Fprint(a.Stderr, "\n"+usage)
		return ExitUsage
	}
	if !errors.Is(err, errSilent) {
		a.errorf("%s", err)
	}
	return ExitFailure
}

func (a *App) printf(format string, args ...any) { fmt.Fprintf(a.Stdout, format, args...) }

func (a *App) warnf(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "WARNING: "+format+"\n", args...)
}

func (a *App) errorf(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "ERROR: "+format+"\n", args...)
}

// pretty shortens paths under the home folder to "~/..." for display.
func (a *App) pretty(path string) string {
	if a.Home == "" {
		return path
	}
	rel, err := filepath.Rel(a.Home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return path
	}
	if rel == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + rel
}

// absPath resolves a path given on the command line: "~" is expanded and relative paths are taken from the current
// folder; symbolic links are resolved when the path exists.
func (a *App) absPath(path string) string {
	path = fsutil.ExpandHome(path, a.Home)
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Cwd, path)
	}
	return fsutil.Canonical(path)
}

// absPathAsGiven returns path as an absolute, cleaned path, without resolving links.
func (a *App) absPathAsGiven(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// version returns the version of a playbook folder, warning once when git is not available.
func (a *App) version(folder string) gitinfo.Version {
	if !a.Git.Available() {
		if !a.gitWarned {
			a.warnf("git was not found in the PATH: playbook versions are shown as unknown.")
			a.gitWarned = true
		}
		return gitinfo.Version{}
	}
	return a.Git.Read(folder)
}

func invalidProfileName(name string) error {
	return usagef("invalid profile name '%s': use letters, digits, '_' and '-', starting with a letter or digit ('%s' is "+
		"reserved)", name, config.BaseConfigName)
}

// cmd returns an aiplam command line to suggest to the user, with the --profile option when the command acts on a
// profile that is not the enabled one.
func (a *App) cmd(command string) string {
	if a.hintProfile == "" {
		return a.hint(command)
	}
	return a.hint(command) + " --profile " + a.hintProfile
}

// hint returns an aiplam command line to suggest to the user, with the option that selects the base folder when one
// was given.
func (a *App) hint(command string) string {
	return "aiplam " + command + a.baseOption()
}

// noProfileOption fails when --profile was given to a command that does not take it.
func (a *App) noProfileOption(command string) error {
	if a.Profile != "" {
		return usagef("'aiplam %s' does not take --profile", command)
	}
	return nil
}

// loadBaseConfig loads the base folder's configuration and fails when it does not exist or is not valid.
func (a *App) loadBaseConfig() (*config.BaseConfig, error) {
	file := a.pretty(config.BaseConfigFile(a.Base))
	conf, err := config.LoadBaseConfig(a.Base)
	if errors.Is(err, config.ErrNotInitialized) {
		return nil, a.notInitialized()
	}
	a.showBase()
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it by hand", file, err)
	}
	if errs := conf.Validate(); len(errs) > 0 {
		for _, e := range errs {
			a.errorf("%s: %s", file, e)
		}
		a.errorf("fix %s by hand; '%s' lists its problems.", file, a.hint("check"))
		return nil, errSilent
	}
	return conf, nil
}

// notInitialized returns the error for a base folder without configuration.
func (a *App) notInitialized() error {
	if a.baseArg != "" || a.global || config.IsBase(a.Base) {
		arg := a.pretty(a.Base)
		if fsutil.SamePath(a.Base, fsutil.Canonical(a.Home)) {
			arg = "--global"
		}
		return fmt.Errorf("'%s' is not set up as a base folder (%s is missing): run 'aiplam init %s' first",
			a.pretty(a.Base), a.pretty(config.BaseConfigFile(a.Base)), arg)
	}
	return errors.New("no base folder was found from the current folder up, nor in the home folder: run " +
		"'aiplam init <basefolder>' (or 'aiplam init --global', for the home folder) first")
}

// profileState is a loaded profile.
type profileState struct {
	name    string
	prof    *config.Profile
	enabled bool             // whether it is enabled (linked from the agents configuration folders)
	entries []playbook.Entry // its playbooks
}

// state is what commands that act on profiles work with.
type state struct {
	conf             *config.BaseConfig
	agentsConfigDirs []string
	profiles         []*profileState // the profiles acted on: the one given with --profile, or the enabled ones
	enabled          []*profileState // every enabled profile
}

// others returns the entries of the enabled profiles other than profile, one slice per profile.
func (st *state) others(profile string) [][]playbook.Entry {
	var others [][]playbook.Entry
	for _, s := range st.enabled {
		if s.name != profile {
			others = append(others, s.entries)
		}
	}
	return others
}

// entries returns the playbooks of the profiles acted on.
func (st *state) entries() []playbook.Entry {
	var all []playbook.Entry
	for _, s := range st.profiles {
		all = append(all, s.entries...)
	}
	return all
}

// profile returns the loaded profile named name.
func (st *state) profile(name string) *profileState {
	for _, s := range st.profiles {
		if s.name == name {
			return s
		}
	}
	return nil
}

// file returns the path of the profile's file, for display.
func (a *App) profileFile(profile string) string {
	return a.pretty(config.ProfileFile(a.Base, profile))
}

// load loads the base folder's configuration, every enabled profile, and the profile given with --profile (which must
// exist). It fails when any of them is missing or not valid.
func (a *App) load() (*state, error) {
	conf, err := a.loadBaseConfig()
	if err != nil {
		return nil, err
	}
	st := &state{conf: conf, agentsConfigDirs: conf.AgentsConfigDirs(a.Base)}
	for _, name := range conf.Enabled {
		if !config.ProfileExists(a.Base, name) {
			return nil, fmt.Errorf("the enabled profile '%s' does not exist (%s is missing): disable it with '%s', or "+
				"create it again with '%s'", name, a.profileFile(name), a.hint("profile disable "+name),
				a.hint("profile add "+name))
		}
		s, err := a.loadProfileState(name, true)
		if err != nil {
			return nil, err
		}
		st.enabled = append(st.enabled, s)
	}
	if a.Profile == "" {
		st.profiles = st.enabled
		return st, nil
	}
	if !config.ProfileExists(a.Base, a.Profile) {
		return nil, fmt.Errorf("profile '%s' does not exist; create it with '%s' ('%s' lists the profiles)", a.Profile,
			a.hint("profile add "+a.Profile), a.hint("profile list"))
	}
	for _, s := range st.enabled {
		if config.SameProfile(a.Base, s.name, a.Profile) {
			st.profiles = []*profileState{s}
			return st, nil
		}
	}
	s, err := a.loadProfileState(a.Profile, false)
	if err != nil {
		return nil, err
	}
	st.profiles = []*profileState{s}
	return st, nil
}

func (a *App) loadProfileState(name string, enabled bool) (*profileState, error) {
	prof, err := a.loadProfile(name)
	if err != nil {
		return nil, err
	}
	return &profileState{name: name, prof: prof, enabled: enabled,
		entries: playbook.Entries(name, prof.Playbooks, a.Base)}, nil
}

// useProfile makes the commands suggested from now on name profile with --profile, unless it is the only enabled
// profile.
func (a *App) useProfile(st *state, s *profileState) {
	a.hintProfile = ""
	if !s.enabled || len(st.enabled) > 1 {
		a.hintProfile = s.name
	}
}

// loadProfile loads a profile and fails when it is missing or not valid.
func (a *App) loadProfile(profile string) (*config.Profile, error) {
	prof, err := config.LoadProfile(a.Base, profile)
	if errors.Is(err, config.ErrNoProfile) {
		return nil, fmt.Errorf("profile '%s' does not exist ('%s' lists the profiles)", profile, a.hint("profile list"))
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it by hand", a.profileFile(profile), err)
	}
	if errs := prof.Validate(a.Base); len(errs) > 0 {
		for _, e := range errs {
			a.errorf("%s: %s", a.profileFile(profile), e)
		}
		a.errorf("fix %s by hand; '%s' lists its problems.", a.profileFile(profile), a.hint("check --profile "+profile))
		return nil, errSilent
	}
	return prof, nil
}

// notEnabledNote explains that a profile's playbooks are not linked.
func (a *App) notEnabledNote(profile string) string {
	return fmt.Sprintf("profile '%s' is not enabled (its playbooks are linked from the agents configuration folders "+
		"when it is enabled with '%s')", profile, a.hint("profile enable "+profile))
}

// profileNames returns the names of profiles, separated by commas.
func profileNames(profiles []*profileState) string {
	var names []string
	for _, s := range profiles {
		names = append(names, s.name)
	}
	return strings.Join(names, ", ")
}

// where describes the profiles acted on, for messages: "profile 'x'", "the enabled profiles (x, y)" or "no enabled
// profile".
func (st *state) where() string {
	switch {
	case len(st.profiles) == 1:
		return fmt.Sprintf("profile '%s'", st.profiles[0].name)
	case len(st.profiles) == 0:
		return "no enabled profile"
	}
	return "the enabled profiles (" + profileNames(st.profiles) + ")"
}

// selectEntry finds the registered playbook that args designate among those of the profiles acted on:
//   - no argument: the playbook registered for the current folder (it must be the only one there);
//   - one argument: an id, else a name, else a folder, each of which must designate a single playbook;
//   - two arguments: a name and a folder, which must match a single registered playbook.
func (a *App) selectEntry(st *state, args []string) (playbook.Entry, error) {
	entries := st.entries()
	several := len(st.profiles) > 1
	label := func(e playbook.Entry) string {
		if several {
			return e.ID + " of profile " + e.Profile
		}
		return e.ID
	}
	list := func(es []playbook.Entry) string {
		var s []string
		for _, e := range es {
			s = append(s, label(e))
		}
		return strings.Join(s, ", ")
	}
	filter := func(match func(playbook.Entry) bool) []playbook.Entry {
		var out []playbook.Entry
		for _, e := range entries {
			if match(e) {
				out = append(out, e)
			}
		}
		return out
	}
	// ambiguous builds the error for several matches, suggesting --profile when they belong to different profiles.
	ambiguous := func(what string, matches []playbook.Entry, give string) error {
		for _, m := range matches[1:] {
			if m.Profile != matches[0].Profile {
				return fmt.Errorf("several playbooks match %s (%s); choose the profile with --profile", what, list(matches))
			}
		}
		return fmt.Errorf("several playbooks match %s (%s); %s", what, list(matches), give)
	}
	switch len(args) {
	case 0:
		cwd := fsutil.Canonical(a.Cwd)
		matches := filter(func(e playbook.Entry) bool { return fsutil.SamePath(e.Dir, cwd) })
		switch len(matches) {
		case 0:
			return playbook.Entry{}, fmt.Errorf("no playbook of %s is registered for the current folder '%s'; give "+
				"a name, id or playbook folder ('%s' shows them)", st.where(), a.pretty(a.Cwd), a.hint("list"))
		case 1:
			return matches[0], nil
		default:
			return playbook.Entry{}, ambiguous("the current folder", matches, "give a name or id")
		}
	case 1:
		arg := args[0]
		for _, match := range []func(playbook.Entry) bool{
			func(e playbook.Entry) bool { return e.ID == arg },
			func(e playbook.Entry) bool { return e.Name == arg },
		} {
			if matches := filter(match); len(matches) == 1 {
				return matches[0], nil
			} else if len(matches) > 1 {
				return playbook.Entry{}, ambiguous("'"+arg+"'", matches, "give an id, or the name and the playbook folder")
			}
		}
		folder := a.absPath(arg)
		matches := filter(func(e playbook.Entry) bool { return fsutil.SamePath(e.Dir, folder) })
		switch len(matches) {
		case 0:
			return playbook.Entry{}, fmt.Errorf("no playbook of %s is registered with name, id or playbook folder '%s' "+
				"('%s' shows them)", st.where(), arg, a.hint("list"))
		case 1:
			return matches[0], nil
		default:
			return playbook.Entry{}, ambiguous("playbook folder '"+arg+"'", matches, "give a name or id")
		}
	case 2:
		folder := a.absPath(args[1])
		matches := filter(func(e playbook.Entry) bool { return e.Name == args[0] && fsutil.SamePath(e.Dir, folder) })
		switch len(matches) {
		case 0:
			return playbook.Entry{}, fmt.Errorf("no playbook of %s named '%s' is registered for playbook folder '%s'",
				st.where(), args[0], args[1])
		case 1:
			return matches[0], nil
		default:
			return playbook.Entry{}, ambiguous("'"+args[0]+"' in playbook folder '"+args[1]+"'", matches, "")
		}
	default:
		return playbook.Entry{}, usagef("too many arguments")
	}
}

// printAction prints a creation as "  + link -> source" and a removal as "  - link (reason)".
func (a *App) printAction(act playbook.Action) {
	switch act.Kind {
	case playbook.Create:
		a.printf("  + %s -> %s\n", a.pretty(act.Link), a.pretty(act.Source))
	case playbook.Remove:
		a.printf("  - %s (%s)\n", a.pretty(act.Link), act.Reason)
	}
}

// conflictAdvice says how to resolve the conflicts of a plan: "remove or rename what is in the way, then try again",
// and, for artifacts linked by a higher-level base folder, that they must be removed there first.
func (a *App) conflictAdvice(plan playbook.Plan) string {
	var advice []string
	if hasConflictsOtherThan(plan, playbook.Ancestor) {
		advice = append(advice, "remove or rename what is in the way")
	}
	for _, c := range plan.Conflicts() {
		if c.Cause == playbook.Ancestor {
			advice = append(advice, "remove from the higher-level base folder the artifacts it already links "+
				"(with 'aiplam remove ... --base <basefolder>'; 'aiplam list --all' lists them)")
			break
		}
	}
	return strings.Join(advice, ", and ") + ", then try again"
}

// hasConflictsOtherThan reports whether plan has conflicts with a cause other than cause.
func hasConflictsOtherThan(plan playbook.Plan, cause playbook.ConflictCause) bool {
	for _, c := range plan.Conflicts() {
		if c.Cause != cause {
			return true
		}
	}
	return false
}

func (a *App) printConflicts(plan playbook.Plan) {
	for _, text := range a.conflictTexts(plan) {
		a.warnf("%s", text)
	}
}

// conflictTexts describes the conflicts of a plan, once each (an artifact provided by two playbooks conflicts in
// every agents configuration folder).
func (a *App) conflictTexts(plan playbook.Plan) []string {
	var texts []string
	seen := map[string]bool{}
	for _, c := range plan.Conflicts() {
		if text := a.conflictText(c); !seen[text] {
			seen[text] = true
			texts = append(texts, text)
		}
	}
	return texts
}

// conflictText describes a conflicting action: "skill x-a: '~/.agents/skills/x-a' already exists and ...".
func (a *App) conflictText(c playbook.Action) string {
	var why string
	switch c.Cause {
	case playbook.OtherLink:
		why = fmt.Sprintf("'%s' already exists and links to '%s'", a.pretty(c.Link), a.pretty(c.Current))
	case playbook.OtherProvider:
		why = "also provided by playbook " + strings.Join(c.Others, ", ")
	case playbook.Exists:
		why = fmt.Sprintf("'%s' already exists", a.pretty(c.Link))
	case playbook.Ancestor:
		why = fmt.Sprintf("'%s' already exists in an agents configuration folder of the same type of a "+
			"higher-level base folder, and agents reading both would see it twice", a.pretty(c.Current))
	default:
		why = fmt.Sprintf("'%s' already exists and is not a link", a.pretty(c.Link))
	}
	return fmt.Sprintf("%s %s: %s", c.Artifact, c.Name, why)
}

// checkPlaybookFolders returns an error when a playbook folder or skills folder is missing, or its skills folder
// lies outside its folder (through a link).
func (a *App) checkPlaybookFolders(e playbook.Entry) error {
	switch {
	case !fsutil.IsDir(e.Dir):
		return fmt.Errorf("playbook %s: playbook folder '%s' does not exist; restore it, or unregister the playbook "+
			"with '%s'", e.ID, a.pretty(e.Dir), a.cmd("remove "+e.ID))
	case !fsutil.IsDir(e.SkillsDir):
		return fmt.Errorf("playbook %s: skills folder '%s' does not exist", e.ID, a.pretty(e.SkillsDir))
	case !fsutil.Within(e.SkillsDir, e.Dir):
		return fmt.Errorf("playbook %s: skills folder '%s' is outside the playbook folder '%s' (through a link)", e.ID,
			a.pretty(e.SkillsDir), a.pretty(e.Dir))
	}
	return nil
}
