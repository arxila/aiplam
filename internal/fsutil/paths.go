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

// Package fsutil holds the file system helpers aiplam needs on every platform: path normalization and comparison,
// and links (symbolic links on Linux and macOS, directory junctions on Windows).
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// Canonical returns the absolute, cleaned form of path with symbolic links resolved. When the path does not exist
// (or cannot be resolved), the absolute cleaned path is returned instead.
func Canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// maxLinkHops is how many links Resolve follows before giving up (a loop of links never ends).
const maxLinkHops = 255

// Resolve returns the absolute, cleaned form of path with the symbolic links (and, on Windows, junctions) of the part
// of it that exists resolved: where the path would lead, once the missing folders are created. It follows the links
// itself, one path element at a time, as filepath.EvalSymlinks does not follow junctions since Go 1.23, and applies
// each '..' (in path, or in a link's target) as the system does: on Linux and macOS, to the folder reached so far,
// which holds no links (never to the text of the path, where it would cancel a link before it); on Windows, which
// reads paths and link targets as text first, to their text (see windowsText). It fails when an existing part cannot
// be resolved (a link it cannot read, a loop of links, or a folder it cannot read).
func Resolve(path string) (string, error) {
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		switch {
		case filepath.VolumeName(path) == "" && path != "" && os.IsPathSeparator(path[0]):
			path = filepath.VolumeName(cwd) + path // on Windows, rooted on the current drive
		default:
			path = cwd + string(filepath.Separator) + path // not filepath.Join, which would apply '..' as text
		}
	}
	path = windowsText(path)
	volume := filepath.VolumeName(path)
	// resolved exists and holds no links; missing holds the names below it that do not exist (yet).
	resolved := volume + string(filepath.Separator)
	var missing []string
	pending := splitPath(path[len(volume):])
	hops := 0
	for len(pending) > 0 {
		elem := pending[0]
		pending = pending[1:]
		switch {
		case elem == ".":
			continue
		case elem == "..":
			if len(missing) > 0 {
				missing = missing[:len(missing)-1]
			} else {
				resolved = filepath.Dir(resolved) // the root stays the root
			}
			continue
		case len(missing) > 0:
			missing = append(missing, elem) // below a missing folder, nothing exists
			continue
		}
		next := filepath.Join(resolved, elem)
		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			// Missing, or below a file (which the missing folders cannot be created under anyway).
			missing = append(missing, elem)
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot resolve '%s': %w", next, err)
		}
		if !isLinkMode(fi.Mode()) {
			resolved = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			if runtime.GOOS == "windows" && fi.Mode()&os.ModeSymlink == 0 {
				resolved = next // an irregular file that is not a readable reparse point (see Inspect)
				continue
			}
			return "", fmt.Errorf("cannot resolve '%s': %w", next, err)
		}
		if hops++; hops > maxLinkHops {
			return "", fmt.Errorf("cannot resolve '%s': too many links", next)
		}
		// The target replaces the link: an absolute target (or, on Windows, one rooted on the link's drive) starts
		// again from its root, and its elements are resolved before the rest of the path.
		if runtime.GOOS == "windows" && !filepath.IsAbs(target) && filepath.VolumeName(target) == "" &&
			(target == "" || !os.IsPathSeparator(target[0])) {
			target = filepath.Join(resolved, target) // relative to the link's folder, read as text (see windowsText)
		}
		target = windowsText(target)
		targetVolume := filepath.VolumeName(target)
		rest := target[len(targetVolume):]
		if targetVolume != "" || (rest != "" && os.IsPathSeparator(rest[0])) {
			if targetVolume == "" {
				targetVolume = filepath.VolumeName(resolved)
			}
			resolved = targetVolume + string(filepath.Separator)
		}
		pending = append(splitPath(rest), pending...)
	}
	return filepath.Join(append([]string{normalized(resolved)}, missing...)...), nil
}

// windowsText returns path cleaned as text on Windows, which applies '..' in a path (and in a link's target, relative
// to the link's folder) to its text, before following any link; elsewhere, it returns path as it is.
func windowsText(path string) string {
	if runtime.GOOS == "windows" {
		return filepath.Clean(path)
	}
	return path
}

// splitPath returns the elements of path, without its volume name, as written (with '.' and '..' kept).
func splitPath(path string) []string {
	return strings.FieldsFunc(path, isSeparator)
}

// normalized returns path, which exists and holds no links, as the system spells it: on Windows, with long names
// instead of short (8.3) ones, as link targets are spelled; elsewhere, as it is.
func normalized(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// SamePath reports whether two paths are equal after cleaning, ignoring case on Windows.
func SamePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Within reports whether path is dir or lies inside it, comparing the paths lexically (resolve links first with
// Canonical when needed). It ignores case on Windows.
func Within(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}

// Inside reports whether path (with links resolved, see Resolve) is dir or lies inside it, as the system sees them:
// whether path, or one of its folders, is dir itself (the same folder, however it is spelled), which also holds on
// disks that ignore case, or that ignore it only in some folders (on Windows). When dir does not exist, it compares
// the paths lexically (see Within).
func Inside(path, dir string) bool {
	top, err := os.Stat(dir)
	if err != nil {
		return Within(path, dir)
	}
	for p := path; ; {
		if info, err := os.Stat(p); err == nil && os.SameFile(info, top) {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// SameName reports whether two path segments are equal, ignoring case on Windows.
func SameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ExpandHome replaces a leading "~" in path with home.
func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:])
	}
	return path
}

// IsDir reports whether path exists and is a directory (following links).
func IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// IsFile reports whether path exists and is a regular file (following links).
func IsFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
