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

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// LinkState describes what exists at a path where aiplam wants a link.
type LinkState int

const (
	// Missing means nothing exists at the path.
	Missing LinkState = iota
	// Link means the path is a symbolic link or a directory junction.
	Link
	// NotLink means the path exists and is a regular file or folder.
	NotLink
)

// Inspect returns what exists at path and, for a link, its target as an absolute path (relative link targets are
// resolved against the link's folder). The target may not exist (a broken link).
func Inspect(path string) (LinkState, string, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Missing, "", nil
	}
	if err != nil {
		return Missing, "", err
	}
	if !isLinkMode(fi.Mode()) {
		return NotLink, "", nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		if runtime.GOOS == "windows" && fi.Mode()&os.ModeSymlink == 0 {
			// An irregular file that is not a readable reparse point (for example a deduplicated file).
			return NotLink, "", nil
		}
		return Link, "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return Link, filepath.Clean(target), nil
}

// isLinkMode reports whether a Lstat mode can be a symbolic link or a directory junction. Since Go 1.23, Windows
// reports junctions (mount points) as irregular files instead of symbolic links.
func isLinkMode(mode os.FileMode) bool {
	if mode&os.ModeSymlink != 0 {
		return true
	}
	return runtime.GOOS == "windows" && mode&os.ModeIrregular != 0
}

// CreateLink creates a link at link pointing to the folder target: a symbolic link on Linux and macOS, a directory
// junction on Windows (which needs no administrator rights or developer mode).
func CreateLink(link, target string) error {
	return createLink(link, target)
}

// RemoveLink removes the link at path, never its target's content. It fails when path is not a link.
func RemoveLink(path string) error {
	state, _, err := Inspect(path)
	if err != nil {
		return err
	}
	if state != Link {
		return &os.PathError{Op: "remove link", Path: path, Err: errors.New("not a symbolic link or junction")}
	}
	// os.Remove deletes a symbolic link itself, and on Windows removes a junction with RemoveDirectory, which does not
	// touch the target's content. Never use os.RemoveAll here.
	return os.Remove(path)
}
