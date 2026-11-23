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
	"os"
	"os/exec"
	"strings"
)

// Version is the checked-out version of a folder.
type Version struct {
	// Known is false when git is not available or the folder is not a git checkout.
	Known bool `json:"known"`
	// Tags are the tags pointing at the checked-out commit.
	Tags []string `json:"tags"`
	// Branch is the checked-out branch, when HEAD is attached.
	Branch string `json:"branch,omitempty"`
	// Commit is the abbreviated checked-out commit.
	Commit string `json:"commit,omitempty"`
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
