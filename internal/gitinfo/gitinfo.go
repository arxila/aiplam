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

// Package gitinfo reports the version of a playbook folder from git: the tags of the checked-out commit, or else
// its branch (or commit, when detached). It runs the git command, never modifies the repository, and never fetches.
package gitinfo

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Version is the checked-out version of a folder.
type Version struct {
	// Known is false when git is not available or the folder is not a git checkout.
	Known bool
	// Tags are the tags pointing at the checked-out commit.
	Tags []string
	// Branch is the checked-out branch, when HEAD is attached.
	Branch string
	// Commit is the abbreviated checked-out commit.
	Commit string
}

// String formats the version for humans: the tags ("v1.2.0", or "v1.2.0, stable"), "branch: b", "commit: c
// (detached)" or "unknown".
func (v Version) String() string {
	switch {
	case !v.Known:
		return "unknown"
	case len(v.Tags) > 0:
		return strings.Join(v.Tags, ", ")
	case v.Branch != "":
		return "branch: " + v.Branch
	case v.Commit != "":
		return "commit: " + v.Commit + " (detached)"
	default:
		return "unknown"
	}
}

// Reader reads versions with the git command found at construction time.
type Reader struct {
	git string
}

// NewReader looks for git in the PATH. Available reports whether it was found.
func NewReader() *Reader {
	path, err := exec.LookPath("git")
	if err != nil {
		return &Reader{}
	}
	return &Reader{git: path}
}

// Available reports whether the git command was found.
func (r *Reader) Available() bool {
	return r != nil && r.git != ""
}

// Read returns the version checked out in folder.
func (r *Reader) Read(folder string) Version {
	if !r.Available() {
		return Version{}
	}
	commit, ok := r.run(folder, "rev-parse", "--short", "HEAD")
	if !ok || commit == "" {
		return Version{}
	}
	v := Version{Known: true, Commit: commit, Tags: []string{}}
	if out, ok := r.run(folder, "tag", "--points-at", "HEAD"); ok {
		for _, line := range strings.Split(out, "\n") {
			if t := strings.TrimSpace(line); t != "" {
				v.Tags = append(v.Tags, t)
			}
		}
	}
	if branch, ok := r.run(folder, "symbolic-ref", "--short", "-q", "HEAD"); ok {
		v.Branch = branch
	}
	return v
}

// Unignored returns, among paths (absolute paths inside the work tree whose top folder is repo), those that git does
// not ignore, and, among those, the ones it tracks (a tracked path is never ignored; a folder counts as tracked when
// files inside it are). It fails with ErrNotFound when git is not available, or with git's error.
func (r *Reader) Unignored(repo string, paths []string) (unignored []string, tracked map[string]bool, err error) {
	if !r.Available() {
		return nil, nil, ErrNotFound
	}
	if len(paths) == 0 {
		return nil, nil, nil
	}
	rels := make([]string, len(paths))
	for i, p := range paths {
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			return nil, nil, err
		}
		rels[i] = filepath.ToSlash(rel)
	}
	// check-ignore lists the paths that are ignored; it exits with 1 when none is.
	out, err := r.command(repo, strings.Join(rels, "\x00"), "check-ignore", "--stdin", "-z")
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return nil, nil, err
	}
	ignored := map[string]bool{}
	for _, rel := range strings.Split(string(out), "\x00") {
		ignored[rel] = true
	}
	var dirs []string
	seen := map[string]bool{}
	for i, rel := range rels {
		if ignored[rel] {
			continue
		}
		unignored = append(unignored, paths[i])
		if dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel))); !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, ":(literal)"+dir) // names are not patterns
		}
	}
	if len(unignored) == 0 {
		return nil, nil, nil
	}
	// The tracked files in the folders of the unignored paths.
	out, err = r.command(repo, "", append([]string{"ls-files", "-z", "--"}, dirs...)...)
	if err != nil {
		return nil, nil, err
	}
	files := strings.Split(string(out), "\x00")
	tracked = map[string]bool{}
	for i, rel := range rels {
		if ignored[rel] {
			continue
		}
		for _, f := range files {
			if f == rel || strings.HasPrefix(f, rel+"/") {
				tracked[paths[i]] = true
				break
			}
		}
	}
	return unignored, tracked, nil
}

// ErrNotFound is returned when the git command was not found.
var ErrNotFound = errors.New("git was not found in the PATH")

// command runs git in folder with stdin as its input, and returns its output. When git fails, the error is git's own
// message (its first line), or how it failed.
func (r *Reader) command(folder, stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command(r.git, append([]string{"-C", folder}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = strings.NewReader(stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) && exit.ExitCode() != 1 {
		if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
			return out.Bytes(), &gitError{msg: msg, err: err}
		}
	}
	return out.Bytes(), err
}

// gitError is a failure of git, with its message.
type gitError struct {
	msg string
	err error
}

func (e *gitError) Error() string { return e.msg }
func (e *gitError) Unwrap() error { return e.err }

func (r *Reader) run(folder string, args ...string) (string, bool) {
	cmd := exec.Command(r.git, append([]string{"-C", folder}, args...)...)
	// Read-only inspection: avoid taking optional locks (for example, refreshing the index).
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
