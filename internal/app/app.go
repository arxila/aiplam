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
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"

	"arxila.io/aiplam/internal/config"
	"arxila.io/aiplam/internal/fsutil"
	"arxila.io/aiplam/internal/gitinfo"
	"arxila.io/aiplam/internal/playbook"
)

// sourceVersion is the version of this source code. The Makefile reads it from here.
const sourceVersion = "1.0.0"

// Version is aiplam's version: the one set at build time with -ldflags "-X arxila.io/aiplam/internal/app.Version=..."
// (as the Makefile does), or else the module version recorded by the go command (for example, by 'go install
// arxila.io/aiplam/cmd/aiplam@v1.2.3'), or else sourceVersion.
var Version string

func init() {
	Version = resolveVersion(Version, debug.ReadBuildInfo)
}

// resolveVersion returns the version to report, given the one set at build time (or "") and the build information.
func resolveVersion(set string, buildInfo func() (*debug.BuildInfo, bool)) string {
	if set != "" {
		return set
	}
	if info, ok := buildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return sourceVersion
}

// Copyright is the copyright line shown in the help.
const Copyright = "(c) 2026 Arxila OSS - https://aiplam.arxila.io"

// Exit codes.
const (
	ExitOK      = 0
	ExitFailure = 1 // an error, a cancelled operation, or (status) something needs attention
	ExitUsage   = 2 // invalid command line
)

// App runs aiplam commands. Its fields make it testable without touching the real home folder.
type App struct {
	// Home is the user's home folder: a leading "~" stands for it, it is the user managed folder when it is managed,
	// and the search for a managed folder stops there.
	Home string
	// Cwd is the current folder, where the search for a managed folder starts.
	Cwd    string
	Stdout io.Writer
	Stderr io.Writer
	Git    *gitinfo.Reader
	// AppData is the value of the APPDATA environment variable, for target folders under %APPDATA% (Windows).
	AppData string
	// GOOS is the operating system the agent templates are resolved for.
	GOOS string
	// Profiles are the profiles given with --profile (commands then act on them instead of the enabled profile). Run
	// sets them.
	Profiles []string
	// Managed is the managed folder commands act on (it holds the .aiplam folder). Run, or the command, sets it.
	Managed string
	// Sandbox, when set, is the only folder managed folders can be in: commands refuse any other. scripts/sandbox.sh
	// sets it, with the AIPLAM_SANDBOX environment variable, so that trying aiplam there cannot change the real
	// configuration, such as ~/.aiplam, even when run from a folder outside the sandbox or given one.
	Sandbox string
	// FileLinks reports whether links to files can be created (see fsutil.FileLinksAllowed, the default when nil).
	// When they cannot, the artifacts that are files are not linked, with a warning.
	FileLinks func() bool

	fileLinks    *bool // the cached answer of FileLinks
	user         bool  // whether --user was given
	elsewhere    bool  // whether the managed folder is not the one commands run from the current folder would use
	managedShown bool  // whether the "Managed folder: ..." line has been printed
	gitWarned    bool
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
	a := &App{Home: fsutil.Canonical(home), Cwd: cwd, Stdout: os.Stdout, Stderr: os.Stderr, Git: gitinfo.NewReader(),
		AppData: os.Getenv("APPDATA"), GOOS: runtime.GOOS}
	if sandbox := os.Getenv("AIPLAM_SANDBOX"); sandbox != "" {
		a.Sandbox = fsutil.Canonical(sandbox)
	}
	return a, nil
}

// checkSandbox fails when a sandbox is set and the managed folder is outside it.
func (a *App) checkSandbox() error {
	if a.Sandbox == "" || fsutil.Within(fsutil.Canonical(a.Managed), a.Sandbox) {
		return nil
	}
	return fmt.Errorf("managed folder '%s' is outside the aiplam sandbox ('%s'), so it is not used: run aiplam from "+
		"a folder inside the sandbox, or unset AIPLAM_SANDBOX", a.Managed, a.Sandbox)
}

type command struct {
	name    string
	summary string
	usage   string
	run     func(a *App, args []string) error
	// profiles says whether the command takes --profile.
	profiles bool
	// resolves says whether the command finds the managed folder itself (init, status and list), instead of Run.
	resolves bool
	// readOnly says whether the command changes nothing, so it does not take the lock.
	readOnly bool
}

var commands []command

func init() {
	commands = []command{
		{name: "init", summary: "make a folder a managed folder: its .aiplam folder and first profiles",
			usage: initUsage, run: (*App).cmdInit, profiles: true, resolves: true},
		{name: "status", summary: "show the managed folder, its enabled profiles, and anything that needs attention",
			usage: statusUsage, run: (*App).cmdStatus, resolves: true, readOnly: true},
		{name: "list", summary: "list the artifacts linked in the target folders of the enabled profiles",
			usage: listUsage, run: (*App).cmdList, resolves: true, readOnly: true},
		{name: "add", summary: "register a playbook and link its artifacts", usage: addUsage, run: (*App).cmdAdd,
			profiles: true},
		{name: "update", summary: "link new artifacts of a playbook and unlink deleted ones", usage: updateUsage,
			run: (*App).cmdUpdate, profiles: true},
		{name: "remove", summary: "unlink a playbook's artifacts and unregister it", usage: removeUsage,
			run: (*App).cmdRemove, profiles: true},
		{name: "profile", summary: "manage profiles: list, show, add, remove, enable, disable, switch", usage: profileUsage,
			run: (*App).cmdProfile},
	}
}

func (a *App) mainUsage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "aiplam %s - AI Playbook Manager\n%s\n\n", Version, Copyright)
	b.WriteString("" +
		"Links the artifacts of AI playbooks (git repositories shared by a team), such as skills, from your local clones\n" +
		"into folders where coding agents read them (.agents/skills, .claude/skills, ...): symbolic links (on Windows,\n" +
		"directory junctions for folders, and symbolic links for files, which need developer mode or administrator\n" +
		"rights: without either, files are skipped with a warning).\n" +
		`
Usage: aiplam <command> [arguments]

Commands:
`)
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-8s %s\n", c.name, c.summary)
	}
	b.WriteString(`
How it works:
  - a managed folder is a folder with a .aiplam folder, which holds aiplam's configuration for it: usually the home
    folder (the user managed folder) and folders where agent sessions start, such as a project's top folder;
  - commands use the nearest managed folder from the current folder up to the home folder; when there is none, the
    home folder, if it is managed;
  - the configuration holds profiles: working scopes (a client, a team, a kind of work...), each with its target
    folders (where it links each artifact kind: skills, subagents, rules...) and its registered playbooks;
  - the target folders are given by hand, or taken from an agent template (` + "`--agent claude`" + `, ...);
  - aiplam links the artifacts of the playbooks of the enabled profiles into their target folders, so updating a
    playbook only takes switching its checkout to another tag and running 'aiplam update'.

`)
	b.WriteString(wrapWords("Agent templates: "+config.TemplateList()+". 'aiplam profile --help' shows their target "+
		"folders.", helpWidth) + "\n")
	b.WriteString(`
Options:
  -u, --user                 use the user managed folder (the home folder, e.g. /home/username)
  -p, --profile PROFILE,...  for add, update and remove: the profiles to act on; for init: the profiles to create

Getting started:
  aiplam init --user --agent claude                   # ~ as managed folder, with a profile for Claude Code
  aiplam add arxila ~/Workspace/arxila-ai-playbook    # register a playbook and link its artifacts
  aiplam list

Run 'aiplam <command> --help' for a command's help, and 'aiplam --version' for the version.
`)
	return b.String()
}

// helpWidth is the width of the help texts.
const helpWidth = 120

// wrapWords wraps text at its spaces into lines of at most width characters (longer words are not split).
func wrapWords(text string, width int) string {
	var b strings.Builder
	line := 0
	for i, word := range strings.Fields(text) {
		switch {
		case i == 0:
		case line+1+len(word) > width:
			b.WriteString("\n")
			line = 0
		default:
			b.WriteString(" ")
			line++
		}
		b.WriteString(word)
		line += len(word)
	}
	return b.String()
}

// templatesHelp describes the agent templates and their target folders, from the templates table.
func templatesHelp() string {
	var b strings.Builder
	for _, t := range config.TemplateTable {
		name := t.Name
		if t.Standard {
			name += " (and " + t.Name + config.StandardVariant + ")"
		}
		fmt.Fprintf(&b, "  %s: %s\n", name, t.Product)
		for _, tt := range t.Targets {
			user := "not in the user managed folder"
			if tt.User != "" {
				user = "~/" + tt.User
				if tt.UserWindows != "" {
					user += " (Windows: " + tt.UserWindows + ")"
				}
			}
			project := "not in projects"
			if tt.Project != "" {
				project = tt.Project
			}
			fmt.Fprintf(&b, "    %-13s %s, %s\n", tt.Kind, project, user)
		}
	}
	fmt.Fprintf(&b, "  The %s variants link skills into .agents/skills instead (in projects and in ~).\n",
		config.StandardVariant)
	return b.String()
}

// Run runs the command line args (without the program name) and returns the exit code.
func (a *App) Run(args []string) int {
	args, opts, err := extractGlobal(args)
	if err != nil {
		return a.exitCode(err, "Run 'aiplam --help' for help.\n")
	}
	a.Profiles, a.user = opts.profiles, opts.user
	a.Managed, a.managedShown, a.elsewhere = "", false, false
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
			a.errorf("unknown command: %s", args[1])
			fmt.Fprint(a.Stderr, "\nRun 'aiplam --help' for the commands.\n")
			return ExitUsage
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
		if len(a.Profiles) > 0 && !c.profiles {
			return a.exitCode(usagef("'aiplam %s' does not take --profile", c.name), c.usage)
		}
		if !c.resolves {
			if err := a.resolveManaged(a.Cwd); err != nil {
				return a.exitCode(err, c.usage)
			}
			if err := a.checkSandbox(); err != nil {
				return a.exitCode(err, c.usage)
			}
		}
		// Commands that change the configuration or the links run one at a time per managed folder (init takes the
		// lock itself, once it knows the managed folder).
		if !c.resolves && !c.readOnly {
			unlock, err := a.lock()
			if err != nil {
				return a.exitCode(err, c.usage)
			}
			defer unlock()
		}
		return a.exitCode(c.run(a, args[1:]), c.usage)
	}
	a.errorf("unknown command: %s", args[0])
	fmt.Fprint(a.Stderr, "\nRun 'aiplam --help' for the commands.\n")
	return ExitUsage
}

// globalOptions are the options that Run removes from the command line before running a command.
type globalOptions struct {
	profiles []string // --profile/-p (repeatable, each a comma-separated list)
	user     bool     // --user/-u
}

// extractGlobal removes the --profile/-p and --user/-u options (allowed anywhere before "--") from args and returns
// them.
func extractGlobal(args []string) ([]string, globalOptions, error) {
	var opts globalOptions
	var rest, profiles []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case arg == "--user" || arg == "-u":
			opts.user = true
		case arg == "--profile" || arg == "-p":
			if i+1 >= len(args) {
				return nil, opts, usagef("option %s needs a value", arg)
			}
			i++
			profiles = append(profiles, args[i])
		case strings.HasPrefix(arg, "--profile=") || strings.HasPrefix(arg, "-p="):
			_, v, _ := strings.Cut(arg, "=")
			profiles = append(profiles, v)
		default:
			rest = append(rest, arg)
		}
	}
	if len(profiles) > 0 {
		names, err := profileNameList(profiles)
		if err != nil {
			return nil, opts, err
		}
		opts.profiles = names
	}
	return rest, opts, nil
}

// resolveManaged sets a.Managed: the home folder with --user, or else the managed folder that applies to start (see
// config.FindManaged). It fails when there is none.
func (a *App) resolveManaged(start string) error {
	home := fsutil.Canonical(a.Home)
	if a.user {
		a.Managed = home
		if !config.IsManaged(home) {
			return fmt.Errorf("the home folder '%s' is not a managed folder (%s is missing): make it one with "+
				"'aiplam init --user --agent <agent>' first", a.pretty(home), a.pretty(config.Dir(home)))
		}
		return nil
	}
	managed, ok := config.FindManaged(a.absPathAsGiven(start), home)
	if !ok {
		return fmt.Errorf("no managed folder applies to '%s': none was found from it up to the home folder, and the "+
			"home folder is not one either. Make one with 'aiplam init <folder> --agent <agent>' (or 'aiplam init "+
			"--user --agent <agent>', for the home folder) first", a.pretty(a.absPathAsGiven(start)))
	}
	a.Managed = managed
	return nil
}

// isUser reports whether the managed folder is the user managed folder (the home folder).
func (a *App) isUser() bool {
	return fsutil.SamePath(a.Managed, fsutil.Canonical(a.Home))
}

// showManaged prints, once per command, the managed folder whose configuration the command uses.
func (a *App) showManaged() {
	if a.managedShown {
		return
	}
	a.managedShown = true
	if a.isUser() {
		a.printf("Managed folder: %s (user managed folder)\n", a.pretty(a.Managed))
	} else {
		a.printf("Managed folder: %s\n", a.pretty(a.Managed))
	}
}

// lockName is the name of the lock file in the .aiplam folder; ignoreName, of the file that keeps it (and the
// temporary files of interrupted saves) out of git, for when the .aiplam folder is committed.
const (
	lockName   = ".lock"
	ignoreName = ".gitignore"
)

// removeNewConfigDir removes a .aiplam folder that a failed init created: the files aiplam writes there before the
// configuration (the lock file, its .gitignore and temporary files of interrupted saves), and then the folder, only if
// that leaves it empty.
func removeNewConfigDir(dir string) {
	for _, name := range []string{lockName, ignoreName} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	if tmps, err := filepath.Glob(filepath.Join(dir, "*.tmp")); err == nil {
		for _, tmp := range tmps {
			_ = os.Remove(tmp)
		}
	}
	_ = os.Remove(dir)
}

// lock makes sure that no other aiplam command changes the managed folder while this one runs, and defers the first
// interruption (Ctrl+C) until it ends, so that the links and the configuration stay consistent: changes take
// milliseconds. A second interruption stops aiplam at once. It does nothing for a folder without a .aiplam folder (the
// command then fails, as it is not set up). The returned function releases the lock and reports an interruption
// received meanwhile.
func (a *App) lock() (func(), error) {
	dir := config.Dir(a.Managed)
	if !fsutil.IsDir(dir) {
		return func() {}, nil
	}
	if err := a.checkInside(dir, a.Managed, config.DirName, "its configuration"); err != nil {
		return nil, err
	}
	unlock, err := fsutil.Lock(filepath.Join(dir, lockName))
	if errors.Is(err, fsutil.ErrLocked) {
		return nil, fmt.Errorf("another aiplam command is changing managed folder '%s': try again when it ends",
			a.pretty(a.Managed))
	}
	if err != nil {
		return nil, fmt.Errorf("cannot lock managed folder '%s': %w", a.pretty(a.Managed), err)
	}
	if ignore := filepath.Join(dir, ignoreName); !fsutil.IsFile(ignore) {
		_ = os.WriteFile(ignore, []byte("# aiplam's lock file, and temporary files of interrupted saves.\n/"+lockName+
			"\n/*.tmp\n"), 0o644)
	}
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	var got atomic.Bool
	go func() {
		select {
		case <-interrupted:
			got.Store(true)
			signal.Stop(interrupted) // a second interruption is handled as usual: it stops aiplam
		case <-done:
		}
	}()
	return func() {
		close(done)
		signal.Stop(interrupted)
		unlock()
		if got.Load() {
			a.warnf("interrupted: the command was completed first, so that links and configuration stay consistent.")
		}
	}, nil
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
		// Only the synopsis: the full help is a --help away.
		if synopsis, _, ok := strings.Cut(usage, "\n\n"); ok && strings.HasPrefix(usage, "Usage: aiplam ") {
			name, _, _ := strings.Cut(strings.TrimPrefix(usage, "Usage: aiplam "), " ")
			usage = synopsis + "\n\nRun 'aiplam " + name + " --help' for details.\n"
		}
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
	return fsutil.Canonical(a.absPathAsGiven(path))
}

// absPathAsGiven returns a path given on the command line as an absolute, cleaned path ("~" expanded, relative paths
// taken from the current folder), without resolving links.
func (a *App) absPathAsGiven(path string) string {
	path = fsutil.ExpandHome(path, a.Home)
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Cwd, path)
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

// hint returns an aiplam command line to suggest to the user, acting on the managed folder of the command: with --user
// when it was given (or the managed folder, given as a folder, is the user one), or run from inside the managed folder
// when the current folder leads to another one.
func (a *App) hint(command string) string {
	switch {
	case a.user || (a.elsewhere && a.isUser()):
		return "aiplam " + command + " --user"
	case a.elsewhere:
		return "cd " + shellPath(runtime.GOOS, a.pretty(a.Managed), a.Managed) + " && aiplam " + command
	}
	return "aiplam " + command
}

// shellPath returns a path to paste in a shell on the operating system goos. On Windows, it is the absolute path (cmd
// does not expand "~"), in double quotes when it holds spaces or characters that cmd or PowerShell treat specially
// (Windows paths cannot hold double quotes, and backslashes are not escapes there). Elsewhere, it is pretty (the path,
// shortened with "~") when it needs no quoting, or else the absolute path in double quotes, escaped for POSIX shells
// ("~" is not expanded inside quotes).
func shellPath(goos, pretty, abs string) string {
	if goos == "windows" {
		if strings.ContainsAny(abs, " \t&|()<>^%!;,=`$'@{}#+") {
			return `"` + abs + `"`
		}
		return abs
	}
	if !strings.ContainsAny(pretty, " \t'\"$`\\;&|()<>*?[]#!{}") {
		return pretty
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return `"` + r.Replace(abs) + `"`
}

// loadConfig loads the aiplam configuration and fails when it does not exist or is not valid.
func (a *App) loadConfig() (*config.Config, error) {
	file := a.pretty(config.ConfigFile(a.Managed))
	conf, err := config.LoadConfig(a.Managed)
	if errors.Is(err, config.ErrNotInitialized) {
		return nil, a.notInitialized()
	}
	a.showManaged()
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it by hand", file, err)
	}
	if errs := conf.Validate(); len(errs) > 0 {
		for _, e := range errs {
			a.errorf("%s: %s", file, e)
		}
		a.errorf("fix %s by hand.", file)
		return nil, errSilent
	}
	return conf, nil
}

// notInitialized returns the error for a managed folder without aiplam configuration.
func (a *App) notInitialized() error {
	arg := a.pretty(a.Managed)
	if a.isUser() {
		arg = "--user"
	}
	return fmt.Errorf("'%s' has a %s folder but no %s: run 'aiplam init %s --agent <agent>' to set it up",
		a.pretty(a.Managed), config.DirName, config.ConfigName+config.FileExt, arg)
}

// profileState is a loaded profile.
type profileState struct {
	name    string
	prof    *config.Profile
	enabled bool              // whether it is enabled (its playbooks are linked)
	entries []playbook.Entry  // its playbooks
	targets []playbook.Target // its target folders, resolved
}

// group returns the profile's playbooks and targets.
func (s *profileState) group() playbook.Group {
	return playbook.Group{Profile: s.name, Entries: s.entries, Targets: s.targets}
}

// newProfileState builds the state of a loaded (valid) profile.
func (a *App) newProfileState(prof *config.Profile, enabled bool) (*profileState, error) {
	s := &profileState{name: prof.Name, prof: prof, enabled: enabled,
		entries: playbook.Entries(prof.Name, prof.Playbooks, a.Managed)}
	for _, t := range prof.Targets {
		dir, stop, err := config.TargetDir(a.Managed, t.Folder, a.AppData)
		if err == nil {
			err = a.checkInside(dir, stop, "target folder '"+t.Folder+"'", "links")
		}
		if err != nil {
			return nil, fmt.Errorf("profile '%s': %w", prof.Name, err)
		}
		s.targets = append(s.targets, playbook.Target{Kind: t.Kind, Folder: t.Folder, Dir: dir, Stop: stop})
	}
	return s, nil
}

// checkInside fails when dir, once the links on its way are resolved, does not lead inside top (the managed folder, or
// %APPDATA%), where aiplam keeps everything it writes. what names dir in the error, and writes what aiplam would write
// in it.
func (a *App) checkInside(dir, top, what, writes string) error {
	name := "managed folder '" + a.pretty(top) + "'"
	if !fsutil.SamePath(top, a.Managed) {
		name = "%APPDATA%"
	}
	resolved, err := fsutil.Resolve(dir)
	if err != nil {
		return fmt.Errorf("%s ('%s'): %w", what, a.pretty(dir), err)
	}
	resolvedTop, err := fsutil.Resolve(top)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !fsutil.Within(resolved, resolvedTop) {
		return fmt.Errorf("%s ('%s') leads outside %s, to '%s', through a link: aiplam only writes %s inside it; "+
			"replace the link with a folder, or move what it leads to inside %s", what, a.pretty(dir), name,
			a.pretty(resolved), writes, name)
	}
	return nil
}

// transition plans the link changes from the profiles in from to those in to for the playbooks in scope (see
// playbook.Transition). When links to files cannot be created, the plan skips them (see playbook.Plan.SkipFileLinks).
// Whether they can is only found out when the plan wants one, as finding it out creates a test link (on Windows).
//
// Undoing a failed change recreates the links it removed: without the rights, a link to a file made earlier (when
// they were available) cannot be recreated, and the failure to undo is reported (see undo).
func (a *App) transition(from, to []*profileState, scope map[playbook.Key]bool) (playbook.Plan, error) {
	plan, err := playbook.Transition(groups(from), groups(to), scope)
	if err == nil && plan.WantsFileLinks() && !a.fileLinksAllowed() {
		plan.SkipFileLinks()
	}
	return plan, err
}

// fileLinksAllowed reports, once per run, whether links to files can be created.
func (a *App) fileLinksAllowed() bool {
	if a.fileLinks == nil {
		check := a.FileLinks
		if check == nil {
			check = fsutil.FileLinksAllowed
		}
		allowed := check()
		a.fileLinks = &allowed
	}
	return *a.fileLinks
}

// skippedText describes the links to files a plan skips ("<count> artifact(s) <verb>", why, and one line per
// artifact).
func (a *App) skippedText(plan playbook.Plan, verb string) string {
	skipped := plan.Skipped()
	var b strings.Builder
	fmt.Fprintf(&b, "%d artifact(s) %s, as they are files and, on Windows, links to files need developer mode or "+
		"administrator rights; once you have either, '%s' shows what to update to link them:", len(skipped), verb,
		a.hint("status"))
	for _, s := range skipped {
		fmt.Fprintf(&b, "\n  %s %s of playbook %s -> %s", s.Artifact, s.Name, s.Playbook, a.pretty(s.Source))
	}
	return b.String()
}

// warnSkipped warns about the links to files a plan skips, if any.
func (a *App) warnSkipped(plan playbook.Plan) {
	if len(plan.Skipped()) > 0 {
		a.warnf("%s", a.skippedText(plan, "were not linked"))
	}
}

// groups returns the groups of profiles.
func groups(profiles []*profileState) []playbook.Group {
	var g []playbook.Group
	for _, s := range profiles {
		g = append(g, s.group())
	}
	return g
}

// state is what commands that act on profiles work with.
type state struct {
	conf    *config.Config
	enabled []*profileState // the enabled profiles (those whose file exists)
}

// profile returns the enabled profile named name, or nil.
func (st *state) profile(managed, name string) *profileState {
	for _, s := range st.enabled {
		if config.SameProfile(managed, s.name, name) {
			return s
		}
	}
	return nil
}

// profileFile returns the path of a profile's file, for display.
func (a *App) profileFile(profile string) string {
	return a.pretty(config.ProfileFile(a.Managed, profile))
}

// loadState loads the aiplam configuration and every enabled profile. An enabled profile whose file is missing makes
// it fail, unless skipMissing is set: it is then left out, with a warning.
func (a *App) loadState(skipMissing bool) (*state, error) {
	conf, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	st := &state{conf: conf}
	for _, name := range conf.Enabled {
		if !config.ProfileExists(a.Managed, name) {
			missing := fmt.Sprintf("the enabled profile '%s' does not exist (%s is missing): disable it with '%s', or "+
				"create it again with '%s'", name, a.profileFile(name), a.hint("profile disable "+name),
				a.hint("profile add "+name+" --agent <agent>"))
			if !skipMissing {
				return nil, errors.New(missing)
			}
			a.warnf("%s.", missing)
			continue
		}
		s, err := a.loadProfileState(name, true)
		if err != nil {
			return nil, err
		}
		st.enabled = append(st.enabled, s)
	}
	return st, nil
}

// loadProfileState loads a profile and builds its state; it fails when the profile is missing or not valid.
func (a *App) loadProfileState(name string, enabled bool) (*profileState, error) {
	prof, err := a.loadProfile(name)
	if err != nil {
		return nil, err
	}
	return a.newProfileState(prof, enabled)
}

// loadProfile loads a profile and fails when it is missing or not valid.
func (a *App) loadProfile(profile string) (*config.Profile, error) {
	// The profile is known by its file's name ("work", even when given as "Work" on a file system that ignores case),
	// which its content must repeat, so that it is saved back to the same file.
	profile = config.ProfileName(a.Managed, profile)
	prof, err := config.LoadProfile(a.Managed, profile)
	if errors.Is(err, config.ErrNoProfile) {
		return nil, fmt.Errorf("profile '%s' does not exist ('%s' lists the profiles)", profile, a.hint("profile list"))
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it by hand", a.profileFile(profile), err)
	}
	if errs := prof.Validate(a.Managed, profile, a.isUser()); len(errs) > 0 {
		for _, e := range errs {
			a.errorf("%s: %s", a.profileFile(profile), e)
		}
		a.errorf("fix %s by hand.", a.profileFile(profile))
		return nil, errSilent
	}
	return prof, nil
}

// selectProfiles returns the profiles a command acts on: those given with --profile (enabled or not), or else the
// enabled profile, which must be the only one.
func (a *App) selectProfiles(st *state, verb string) ([]*profileState, error) {
	if len(a.Profiles) == 0 {
		switch len(st.enabled) {
		case 1:
			return st.enabled, nil
		case 0:
			return nil, fmt.Errorf("no profile is enabled: choose the profile to %s with --profile", verb)
		}
		return nil, fmt.Errorf("%d profiles are enabled (%s): choose the profile to %s with --profile",
			len(st.enabled), profileNames(st.enabled), verb)
	}
	var selected []*profileState
	for _, name := range a.Profiles {
		if s := st.profile(a.Managed, name); s != nil {
			selected = append(selected, s)
			continue
		}
		if !config.ProfileExists(a.Managed, name) {
			return nil, fmt.Errorf("profile '%s' does not exist; create it with '%s' ('%s' lists the profiles)", name,
				a.hint("profile add "+name+" --agent <agent>"), a.hint("profile list"))
		}
		s, err := a.loadProfileState(name, st.conf.IsEnabled(a.Managed, name))
		if err != nil {
			return nil, err
		}
		selected = append(selected, s)
	}
	return selected, nil
}

// withProfile returns a suggested aiplam command, with --profile when the profile has to be named (it is not the only
// enabled one).
func (a *App) withProfile(st *state, profile, command string) string {
	if len(st.enabled) == 1 && st.enabled[0].name == profile {
		return a.hint(command)
	}
	return a.hint(command + " --profile " + profile)
}

// replaceProfiles returns profiles with those named like one of updated replaced by it.
func replaceProfiles(profiles []*profileState, updated ...*profileState) []*profileState {
	out := append([]*profileState{}, profiles...)
	for i, p := range out {
		for _, u := range updated {
			if p.name == u.name {
				out[i] = u
			}
		}
	}
	return out
}

// profileNames returns the names of profiles, separated by commas.
func profileNames(profiles []*profileState) string {
	var names []string
	for _, s := range profiles {
		names = append(names, s.name)
	}
	return strings.Join(names, ", ")
}

// notEnabledNote explains that a profile's playbooks are not linked.
func (a *App) notEnabledNote(profile string) string {
	return fmt.Sprintf("profile '%s' is not enabled (its playbooks are linked when it is enabled with '%s')", profile,
		a.hint("profile enable "+profile))
}

// templateCheck warns when a profile created from an agent template no longer has the template's targets.
func (a *App) templateCheck(s *profileState) {
	if s.prof.Agent == config.CustomAgent {
		return
	}
	targets, ok := config.TemplateTargets(s.prof.Agent, a.isUser(), a.GOOS)
	if !ok || targets.Same(s.prof.Targets) {
		return
	}
	var want []string
	for _, t := range targets {
		want = append(want, t.Kind+": "+t.Folder)
	}
	a.warnf("profile '%s': its target folders no longer match those of its agent template '%s' (%s); if they were "+
		"changed on "+
		"purpose, set 'agent: %s' in %s.", s.name, s.prof.Agent, strings.Join(want, ", "), config.CustomAgent,
		a.profileFile(s.name))
}

// targetList describes a profile's targets, one per line, indented.
func targetList(targets config.Targets) string {
	var b strings.Builder
	for _, t := range targets {
		fmt.Fprintf(&b, "  %s: %s\n", t.Kind, t.Folder)
	}
	return b.String()
}

// selectEntry finds the registered playbook of a profile that args designate:
//   - no argument: the playbook registered for the current folder (it must be the only one there);
//   - one argument: an id, else a name, else a folder, each of which must designate a single playbook;
//   - two arguments: a name and a folder, which must match a single registered playbook.
func (a *App) selectEntry(s *profileState, args []string) (playbook.Entry, error) {
	list := func(es []playbook.Entry) string {
		var ids []string
		for _, e := range es {
			ids = append(ids, e.ID)
		}
		return strings.Join(ids, ", ")
	}
	filter := func(match func(playbook.Entry) bool) []playbook.Entry {
		var out []playbook.Entry
		for _, e := range s.entries {
			if match(e) {
				out = append(out, e)
			}
		}
		return out
	}
	where := fmt.Sprintf("profile '%s'", s.name)
	switch len(args) {
	case 0:
		cwd := fsutil.Canonical(a.Cwd)
		matches := filter(func(e playbook.Entry) bool { return fsutil.SamePath(e.Dir, cwd) })
		switch len(matches) {
		case 0:
			return playbook.Entry{}, fmt.Errorf("no playbook of %s is registered for the current folder '%s'; give "+
				"a name, id or playbook folder", where, a.pretty(a.Cwd))
		case 1:
			return matches[0], nil
		default:
			return playbook.Entry{}, fmt.Errorf("several playbooks of %s are registered for the current folder (%s); "+
				"give a name or id", where, list(matches))
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
				return playbook.Entry{}, fmt.Errorf("several playbooks of %s match '%s' (%s); give an id, or the name "+
					"and the playbook folder", where, arg, list(matches))
			}
		}
		folder := a.absPath(arg)
		matches := filter(func(e playbook.Entry) bool { return fsutil.SamePath(e.Dir, folder) })
		switch len(matches) {
		case 0:
			return playbook.Entry{}, fmt.Errorf("no playbook of %s is registered with name, id or playbook folder '%s'",
				where, arg)
		case 1:
			return matches[0], nil
		default:
			return playbook.Entry{}, fmt.Errorf("several playbooks of %s are registered for playbook folder '%s' (%s); "+
				"give a name or id", where, arg, list(matches))
		}
	case 2:
		folder := a.absPath(args[1])
		matches := filter(func(e playbook.Entry) bool { return e.Name == args[0] && fsutil.SamePath(e.Dir, folder) })
		if len(matches) == 1 {
			return matches[0], nil
		}
		return playbook.Entry{}, fmt.Errorf("no playbook of %s named '%s' is registered for playbook folder '%s'",
			where, args[0], args[1])
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

// cleanup removes the folders that the removals of an applied plan left empty, printing each one.
func (a *App) cleanup(plan playbook.Plan) {
	playbook.Cleanup(plan, func(dir string) { a.printf("  - %s (empty folder removed)\n", a.pretty(dir)) })
}

// undo reverts the link changes of an applied plan after saving the configuration failed.
func (a *App) undo(plan playbook.Plan, saveErr error, state string) error {
	if err := playbook.Revert(plan.Changes()); err != nil {
		return fmt.Errorf("%w; undoing the link changes also failed (%v): run '%s' to see what is left", saveErr, err,
			a.hint("status"))
	}
	if len(plan.Changes()) == 0 {
		return fmt.Errorf("%w; %s", saveErr, state)
	}
	return fmt.Errorf("%w; the link changes were undone and %s", saveErr, state)
}

// Advice for each kind of conflict (see conflictAdvice).
const (
	adviceInTheWay = "remove or rename what is in the way (a link to the same artifact that aiplam did not make for " +
		"this playbook is in the way too: every link belongs to a single playbook of a single profile)"
	adviceDuplicate = "give one of the playbooks a format (or name) that does not match the same artifacts, or " +
		"unregister one of them"
	adviceSame = "register the playbook in only one of the profiles enabled together"
)

// conflictAdvice says how to resolve the conflicts of a plan, for each kind of conflict it has.
func (a *App) conflictAdvice(plan playbook.Plan) string {
	var advice []string
	if hasConflicts(plan, playbook.NotLink, playbook.OtherLink, playbook.Exists) {
		advice = append(advice, adviceInTheWay)
	}
	if hasConflicts(plan, playbook.Duplicate) {
		advice = append(advice, adviceDuplicate)
	}
	if hasConflicts(plan, playbook.SamePlaybook) {
		advice = append(advice, adviceSame)
	}
	return strings.Join(advice, "; and ") + "; then try again"
}

// hasConflicts reports whether plan has conflicts with any of causes.
func hasConflicts(plan playbook.Plan, causes ...playbook.ConflictCause) bool {
	for _, c := range plan.Conflicts() {
		if slices.Contains(causes, c.Cause) {
			return true
		}
	}
	return false
}

// printConflicts prints the conflicts of a plan, once each.
func (a *App) printConflicts(plan playbook.Plan) {
	seen := map[string]bool{}
	for _, c := range plan.Conflicts() {
		if text := a.conflictText(c); !seen[text] {
			seen[text] = true
			a.errorf("%s", text)
		}
	}
}

// conflictText describes a conflicting action: "skills x-a (playbook x (profile default)): '~/.agents/skills/x-a'
// already exists and ...".
func (a *App) conflictText(c playbook.Action) string {
	var why string
	switch c.Cause {
	case playbook.SamePlaybook:
		return fmt.Sprintf("playbook %s: the same playbook (name '%s', playbook folder '%s') is registered in %s, and "+
			"the same playbook cannot be in two enabled profiles", c.Playbook, c.Name, a.pretty(c.Source),
			strings.Join(c.Others, ", "))
	case playbook.OtherLink:
		why = fmt.Sprintf("'%s' already exists and links to '%s'", a.pretty(c.Link), a.pretty(c.Current))
	case playbook.Exists:
		why = fmt.Sprintf("'%s' already exists, linking to the same artifact, but it does not belong to this playbook",
			a.pretty(c.Link))
	case playbook.Duplicate:
		why = fmt.Sprintf("'%s' would also be linked by %s", a.pretty(c.Link), strings.Join(c.Others, ", "))
	default:
		why = fmt.Sprintf("'%s' already exists and is not a link", a.pretty(c.Link))
	}
	return fmt.Sprintf("%s %s of playbook %s: %s", c.Artifact, c.Name, c.Playbook, why)
}

// checkPlaybookFolders returns an error when a playbook folder is missing, or when one of its folders for the kinds
// of its profile's targets lies outside the playbook folder (through a link).
func (a *App) checkPlaybookFolders(e playbook.Entry, targets []playbook.Target) error {
	if _, err := os.Stat(e.Dir); err == nil && !fsutil.IsDir(e.Dir) {
		return fmt.Errorf("playbook %s: playbook folder '%s' is not a folder", e.Label(), a.pretty(e.Dir))
	}
	if !fsutil.IsDir(e.Dir) {
		return fmt.Errorf("playbook %s: playbook folder '%s' does not exist; restore it, or unregister the playbook",
			e.Label(), a.pretty(e.Dir))
	}
	for _, t := range targets {
		dir := e.SourceDir(t.Kind)
		if _, err := os.Stat(dir); err == nil && !fsutil.IsDir(dir) {
			return fmt.Errorf("playbook %s: its subfolder for %s, '%s', is not a folder", e.Label(), t.Kind,
				a.pretty(dir))
		}
		if fsutil.IsDir(dir) && !fsutil.Within(dir, e.Dir) {
			return fmt.Errorf("playbook %s: its subfolder for %s, '%s', is outside the playbook folder '%s' (through a link)",
				e.Label(), t.Kind, a.pretty(dir), a.pretty(e.Dir))
		}
	}
	return nil
}

// checkTargetFolders returns an error when a target folder of a profile, or one of its parents up to the managed folder
// (or %APPDATA%), exists and is not a folder, so that nothing can be linked there.
func (a *App) checkTargetFolders(s *profileState) error {
	for _, t := range s.targets {
		for dir := t.Dir; fsutil.Within(dir, t.Stop) && !fsutil.SamePath(dir, t.Stop); dir = filepath.Dir(dir) {
			info, err := os.Stat(dir)
			if err != nil {
				continue // missing: created when the first artifact is linked
			}
			if !info.IsDir() {
				return fmt.Errorf("profile '%s': target folder '%s' cannot be used: '%s' exists and is not a folder",
					s.name, t.Folder, a.pretty(dir))
			}
			break
		}
	}
	return nil
}
