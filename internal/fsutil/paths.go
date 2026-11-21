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
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
