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
	Home   string
	Cwd    string
	Stdout io.Writer
	Stderr io.Writer
	Git    *gitinfo.Reader
	// Set is the set given with the --set option, or "" (commands then act on the enabled set). Run sets it.
	Set string

	gitWarned bool
	hintSet   string // the set to name with --set in suggested commands, when it is not the enabled one
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
		{"init", "set up aiplam: its target folders and a first, enabled set", initUsage, (*App).cmdInit},
		{"add", "register a playbook and install its artifacts", addUsage, (*App).cmdAdd},
		{"update", "install new artifacts of a playbook and remove deleted ones", updateUsage, (*App).cmdUpdate},
		{"remove", "uninstall a playbook's artifacts and unregister it", removeUsage, (*App).cmdRemove},
		{"list", "list registered playbooks, their versions and artifacts", listUsage, (*App).cmdList},
		{"check", "check the configuration and whether any update is needed", checkUsage, (*App).cmdCheck},
		{"set", "list, add, remove or enable sets of playbooks", setUsage, (*App).cmdSet},
		{"target", "show or change the target folders", targetUsage, (*App).cmdTarget},
	}
}

func (a *App) mainUsage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "aiplam %s - AI Playbook Manager\n%s\n\n", Version, Copyright)
	b.WriteString("Installs the artifacts (skills) of AI playbooks, git checkouts shared by a team, into the folders\n")
	b.WriteString("that coding agents read (~/.agents, ~/.claude), as symbolic links (directory junctions on Windows).\n\n")
	b.WriteString("Usage: aiplam <command> [arguments]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-8s %s\n", c.name, c.summary)
	}
	b.WriteString("\nPlaybooks are registered in sets; only the enabled set is installed in the targets.\n")
	b.WriteString("\nOption for add, update, remove, list and check:\n")
	b.WriteString("  -s, --set SET   act on set SET instead of the enabled one\n")
	b.WriteString("\nRun 'aiplam <command> --help' for a command's options, 'aiplam --version' for the version.\n")
	return b.String()
}

// Run runs the command line args (without the program name) and returns the exit code.
func (a *App) Run(args []string) int {
	args, set, err := extractSet(args)
	if err != nil {
		return a.exitCode(err, a.mainUsage())
	}
	a.Set = set
	a.hintSet = ""
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
		return a.exitCode(c.run(a, args[1:]), c.usage)
	}
	a.errorf("unknown command: %s", args[0])
	fmt.Fprint(a.Stderr, "\n"+a.mainUsage())
	return ExitUsage
}

// extractSet removes the --set/-s options (allowed anywhere before "--") from args and returns the set they give, or
// "".
func extractSet(args []string) ([]string, string, error) {
	set := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		var value string
		switch {
		case arg == "--set" || arg == "-s":
			if i+1 >= len(args) {
				return nil, "", usagef("option %s needs a value", arg)
			}
			i++
			value = args[i]
		case strings.HasPrefix(arg, "--set="):
			value = strings.TrimPrefix(arg, "--set=")
		case strings.HasPrefix(arg, "-s="):
			value = strings.TrimPrefix(arg, "-s=")
		default:
			rest = append(rest, arg)
			continue
		}
		if !config.ValidSetName(value) {
			return nil, "", invalidSetName(value)
		}
		set = value
	}
	return rest, set, nil
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

func invalidSetName(name string) error {
	return usagef("invalid set name '%s': use letters, digits, '_' and '-', starting with a letter or digit ('%s' is "+
		"reserved)", name, config.InstallName)
}

// cmd returns an aiplam command line to suggest to the user, with the --set option when the command acts on a set
// that is not the enabled one.
func (a *App) cmd(command string) string {
	if a.hintSet == "" {
		return "aiplam " + command
	}
	return "aiplam " + command + " --set " + a.hintSet
}

// noSetOption fails when --set was given to a command that does not take it.
func (a *App) noSetOption(command string) error {
	if a.Set != "" {
		return usagef("'aiplam %s' does not take --set", command)
	}
	return nil
}

// loadInstall loads the installation's configuration and fails when it does not exist or is not valid.
func (a *App) loadInstall() (*config.Install, error) {
	file := a.pretty(config.InstallFile(a.Home))
	inst, err := config.LoadInstall(a.Home)
	if errors.Is(err, config.ErrNotInitialized) {
		return nil, errors.New("aiplam is not initialized: run 'aiplam init --target <folder>' first")
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it by hand", file, err)
	}
	if errs := inst.Validate(a.Home); len(errs) > 0 {
		for _, e := range errs {
			a.errorf("%s: %s", file, e)
		}
		a.errorf("fix %s by hand; 'aiplam check' lists its problems.", file)
		return nil, errSilent
	}
	return inst, nil
}

// state is what commands that act on a set work with.
type state struct {
	inst    *config.Install
	targets []string
	set     string      // the set acted on
	cfg     *config.Set // its content
	enabled bool        // whether it is the enabled set (installed in the targets)
}

// file returns the path of the set's file, for display.
func (a *App) setFile(set string) string {
	return a.pretty(config.SetFile(a.Home, set))
}

// load loads the installation's configuration and the set to act on (the one given with --set, which must exist, or
// the enabled one), and fails when either is missing or not valid.
func (a *App) load() (*state, error) {
	inst, err := a.loadInstall()
	if err != nil {
		return nil, err
	}
	st := &state{inst: inst, targets: inst.TargetDirs(a.Home), set: inst.Enabled}
	if a.Set != "" {
		st.set = a.Set
		if !config.SetExists(a.Home, a.Set) {
			return nil, fmt.Errorf("set '%s' does not exist; create it with 'aiplam set add %s' ('aiplam set list' "+
				"lists the sets)", a.Set, a.Set)
		}
	} else if !config.SetExists(a.Home, inst.Enabled) {
		return nil, fmt.Errorf("the enabled set '%s' does not exist (%s is missing): enable another set with "+
			"'aiplam set enable <set>', or create it again with 'aiplam set add %s'", inst.Enabled,
			a.setFile(inst.Enabled), inst.Enabled)
	}
	st.enabled = config.SameSet(a.Home, st.set, inst.Enabled)
	if !st.enabled {
		a.hintSet = st.set
	}
	cfg, err := a.loadSet(st.set)
	if err != nil {
		return nil, err
	}
	st.cfg = cfg
	return st, nil
}

// loadSet loads a set and fails when it is missing or not valid.
func (a *App) loadSet(set string) (*config.Set, error) {
	cfg, err := config.LoadSet(a.Home, set)
	if errors.Is(err, config.ErrNoSet) {
		return nil, fmt.Errorf("set '%s' does not exist ('aiplam set list' lists the sets)", set)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it by hand", a.setFile(set), err)
	}
	if errs := cfg.Validate(a.Home); len(errs) > 0 {
		for _, e := range errs {
			a.errorf("%s: %s", a.setFile(set), e)
		}
		a.errorf("fix %s by hand; '%s' lists its problems.", a.setFile(set), a.cmd("check"))
		return nil, errSilent
	}
	return cfg, nil
}

// notEnabledNote explains that a set's playbooks are not installed.
func (a *App) notEnabledNote(set string) string {
	return fmt.Sprintf("set '%s' is not enabled (its playbooks are linked into the targets when it is enabled with "+
		"'aiplam set enable %s')", set, set)
}

// selectEntry finds the registered playbook that args designate:
//   - no argument: the playbook registered for the current folder (it must be the only one there);
//   - one argument: an id, else a name (which must be unique), else a folder (with a single playbook);
//   - two arguments: a name and a folder, which must match a registered playbook.
func (a *App) selectEntry(entries []playbook.Entry, args []string, set string) (playbook.Entry, error) {
	byFolder := func(folder string) []playbook.Entry {
		var out []playbook.Entry
		for _, e := range entries {
			if fsutil.SamePath(e.Dir, folder) {
				out = append(out, e)
			}
		}
		return out
	}
	ids := func(es []playbook.Entry) string {
		var s []string
		for _, e := range es {
			s = append(s, e.ID)
		}
		return strings.Join(s, ", ")
	}
	switch len(args) {
	case 0:
		matches := byFolder(fsutil.Canonical(a.Cwd))
		switch len(matches) {
		case 0:
			return playbook.Entry{}, fmt.Errorf("no playbook is registered for the current folder '%s' in set '%s'; give "+
				"a name, id or folder ('%s' shows them)", a.pretty(a.Cwd), set, a.cmd("list"))
		case 1:
			return matches[0], nil
		default:
			return playbook.Entry{}, fmt.Errorf("several playbooks are registered for the current folder (%s); "+
				"give a name or id", ids(matches))
		}
	case 1:
		arg := args[0]
		for _, e := range entries {
			if e.ID == arg {
				return e, nil
			}
		}
		var named []playbook.Entry
		for _, e := range entries {
			if e.Name == arg {
				named = append(named, e)
			}
		}
		if len(named) == 1 {
			return named[0], nil
		}
		if len(named) > 1 {
			return playbook.Entry{}, fmt.Errorf("several playbooks are named '%s' (%s); give an id, or the name and "+
				"the folder", arg, ids(named))
		}
		matches := byFolder(a.absPath(arg))
		switch len(matches) {
		case 0:
			return playbook.Entry{}, fmt.Errorf("no playbook is registered with name, id or folder '%s' in set '%s' "+
				"('%s' shows them)", arg, set, a.cmd("list"))
		case 1:
			return matches[0], nil
		default:
			return playbook.Entry{}, fmt.Errorf("several playbooks are registered for folder '%s' (%s); give a name "+
				"or id", arg, ids(matches))
		}
	case 2:
		folder := a.absPath(args[1])
		for _, e := range entries {
			if e.Name == args[0] && fsutil.SamePath(e.Dir, folder) {
				return e, nil
			}
		}
		return playbook.Entry{}, fmt.Errorf("no playbook named '%s' is registered for folder '%s'", args[0], args[1])
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

func (a *App) printConflicts(plan playbook.Plan) {
	for _, text := range a.conflictTexts(plan) {
		a.warnf("%s", text)
	}
}

// conflictTexts describes the conflicts of a plan, once each (a skill provided by two playbooks conflicts in every
// target).
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
	default:
		why = fmt.Sprintf("'%s' already exists and is not a link", a.pretty(c.Link))
	}
	return fmt.Sprintf("%s %s: %s", c.Artifact, c.Name, why)
}

// checkPlaybookFolders returns an error when a playbook's folder or skills folder is missing, or its skills folder
// lies outside its folder (through a link).
func (a *App) checkPlaybookFolders(e playbook.Entry) error {
	switch {
	case !fsutil.IsDir(e.Dir):
		return fmt.Errorf("playbook %s: folder '%s' does not exist; restore it, or unregister the playbook with '%s'",
			e.ID, a.pretty(e.Dir), a.cmd("remove "+e.ID))
	case !fsutil.IsDir(e.SkillsDir):
		return fmt.Errorf("playbook %s: skills folder '%s' does not exist", e.ID, a.pretty(e.SkillsDir))
	case !fsutil.Within(e.SkillsDir, e.Dir):
		return fmt.Errorf("playbook %s: skills folder '%s' is outside the playbook folder '%s' (through a link)", e.ID,
			a.pretty(e.SkillsDir), a.pretty(e.Dir))
	}
	return nil
}
