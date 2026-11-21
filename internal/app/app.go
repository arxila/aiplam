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
	// Set is the configuration set that commands act on; Run sets it from the --set option (default: "default").
	Set string

	gitWarned bool
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
		{"init", "create a set's configuration file and its target folders", initUsage, (*App).cmdInit},
		{"add", "register a playbook and install its artifacts", addUsage, (*App).cmdAdd},
		{"update", "install new artifacts of a playbook and remove deleted ones", updateUsage, (*App).cmdUpdate},
		{"remove", "uninstall a playbook's artifacts and unregister it", removeUsage, (*App).cmdRemove},
		{"list", "list registered playbooks, their versions and artifacts", listUsage, (*App).cmdList},
		{"check", "check the configuration and whether any update is needed", checkUsage, (*App).cmdCheck},
		{"sets", "list the configuration sets", setsUsage, (*App).cmdSets},
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
	b.WriteString("\nOptions for every command:\n")
	b.WriteString("  -s, --set SET   act on configuration set SET, stored in ~/.aiplam/SET.yaml (default: 'default').\n")
	b.WriteString("                  Each set has its own targets and playbooks.\n")
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
// the default set.
func extractSet(args []string) ([]string, string, error) {
	set := config.DefaultSet
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
			return nil, "", usagef("invalid set name '%s': use letters, digits, '_' and '-', starting with a letter "+
				"or digit", value)
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

// cmd returns an aiplam command line to suggest to the user, with the --set option when the set is not the default.
func (a *App) cmd(command string) string {
	if a.Set == "" || a.Set == config.DefaultSet {
		return "aiplam " + command
	}
	return "aiplam " + command + " --set " + a.Set
}

// configFile returns the path of the current set's configuration file, for display.
func (a *App) configFile() string {
	return a.pretty(config.File(a.Home, a.set()))
}

func (a *App) set() string {
	if a.Set == "" {
		return config.DefaultSet
	}
	return a.Set
}

// notInitialized returns the error for a set whose configuration file does not exist.
func (a *App) notInitialized() error {
	if a.set() == config.DefaultSet {
		return fmt.Errorf("aiplam is not initialized: run 'aiplam init' first")
	}
	return fmt.Errorf("set '%s' is not initialized: run '%s' first", a.set(), a.cmd("init"))
}

// loadConfig loads the current set's configuration and fails when it does not exist or is not valid. With
// exclusive, it also fails when another set lists one of its targets, as commands that change links must not run
// then.
func (a *App) loadConfig(exclusive bool) (*config.Config, error) {
	cfg, err := config.Load(a.Home, a.set())
	if errors.Is(err, config.ErrNotInitialized) {
		return nil, a.notInitialized()
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it by hand", a.configFile(), err)
	}
	if errs := cfg.Validate(a.Home); len(errs) > 0 {
		for _, e := range errs {
			a.errorf("%s: %s", a.configFile(), e)
		}
		a.errorf("fix %s manually; '%s' lists its problems.", a.configFile(), a.cmd("check"))
		return nil, errSilent
	}
	if exclusive {
		shared, err := config.SharedTargets(a.Home, a.set(), cfg)
		if err != nil {
			return nil, err
		}
		if len(shared) > 0 {
			for _, st := range shared {
				a.errorf("%s: target '%s' is also listed by set '%s'", a.configFile(), a.pretty(st.Target), st.Set)
			}
			a.errorf("sets cannot share targets, as their links would get in each other's way: edit the files in " +
				"~/.aiplam by hand so that each target belongs to one set.")
			return nil, errSilent
		}
	}
	return cfg, nil
}

// selectEntry finds the registered playbook that args designate:
//   - no argument: the playbook registered for the current folder (it must be the only one there);
//   - one argument: an id, else a name (which must be unique), else a folder (with a single playbook);
//   - two arguments: a name and a folder, which must match a registered playbook.
func (a *App) selectEntry(entries []playbook.Entry, args []string) (playbook.Entry, error) {
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
				"a name, id or folder ('%s' shows them)", a.pretty(a.Cwd), a.set(), a.cmd("list"))
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
				"('%s' shows them)", arg, a.set(), a.cmd("list"))
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
	for _, c := range plan.Conflicts() {
		a.warnf("%s", a.conflictText(c))
	}
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
