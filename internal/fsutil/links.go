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
	"strings"
	"unicode/utf8"
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
// resolved against the link's folder, with links resolved, as the system does; see linkPath). The target may not
// exist (a broken link). When the target cannot be resolved, it returns Link, the target as written (made absolute),
// and the error.
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
		if notReparsePoint(fi) {
			return NotLink, "", nil
		}
		return Link, "", err
	}
	if !filepath.IsAbs(target) {
		target = Canonical(filepath.Dir(path)) + string(filepath.Separator) + target
	}
	resolved, err := linkPath(target)
	if err != nil {
		return Link, target, err
	}
	return Link, resolved, nil
}

// linkPath returns a link's absolute target, as written, cleaned as the system reads it: on Linux and macOS, a '..'
// that follows a link in it applies to where that link leads, not to the text before it; so its folder is resolved
// (see Resolve, which also resolves it for missing folders, as it will lead once they exist), and its last name is
// kept, as the target itself may be a link, or missing. Windows reads it as text (see windowsText), and so does it
// without '..'. It fails when the target's folder cannot be resolved.
func linkPath(target string) (string, error) {
	if runtime.GOOS == "windows" {
		return filepath.Clean(target), nil
	}
	elems := splitPath(target[len(filepath.VolumeName(target)):])
	dotDot := false
	for _, elem := range elems {
		dotDot = dotDot || elem == ".."
	}
	if !dotDot || len(elems) == 0 {
		return filepath.Clean(target), nil
	}
	if last := elems[len(elems)-1]; last == "." || last == ".." {
		return Resolve(target)
	}
	end := strings.LastIndexFunc(strings.TrimRightFunc(target, isSeparator), isSeparator)
	dir, err := Resolve(target[:end+1])
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, elems[len(elems)-1]), nil
}

// isLink reports whether path is a link (a symbolic link or a junction), as Inspect sees it, without looking at its
// target.
func isLink(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !isLinkMode(fi.Mode()) {
		return false
	}
	if _, err := os.Readlink(path); err != nil && notReparsePoint(fi) {
		return false
	}
	return true
}

// notReparsePoint reports whether fi, of a file whose link target cannot be read, is not a link at all: on Windows, an
// irregular file that is not a readable reparse point (for example a deduplicated file).
func notReparsePoint(fi os.FileInfo) bool {
	return runtime.GOOS == "windows" && fi.Mode()&os.ModeSymlink == 0
}

// isSeparator reports whether r is a path separator.
func isSeparator(r rune) bool { return r < utf8.RuneSelf && os.IsPathSeparator(uint8(r)) }

// isLinkMode reports whether a Lstat mode can be a symbolic link or a directory junction. Since Go 1.23, Windows
// reports junctions (mount points) as irregular files instead of symbolic links.
func isLinkMode(mode os.FileMode) bool {
	if mode&os.ModeSymlink != 0 {
		return true
	}
	return runtime.GOOS == "windows" && mode&os.ModeIrregular != 0
}

// CreateLink creates a link at link pointing to target, a folder (such as a skill) or a file (such as a subagent): a
// symbolic link with a relative target on Linux and macOS; on Windows, a directory junction for a folder (which needs
// no administrator rights or developer mode, and always holds an absolute target) and a symbolic link for a file
// (which needs developer mode or administrator rights).
func CreateLink(link, target string) error {
	return createLink(link, target)
}

// LinkKind is the kind of a link that RestoreLink must make again, as KindOf reports it.
type LinkKind int

const (
	// AnyLink is the link that CreateLink makes for its target: every link, but on Windows (see KindOf).
	AnyLink LinkKind = iota
	// FileSymlink and DirSymlink are, on Windows, symbolic links to a file and to a folder: they are made differently,
	// and a link to a folder may also be a junction (AnyLink).
	FileSymlink
	DirSymlink
)

// KindOf returns the kind of the link at path (AnyLink when it cannot tell, or nothing is there).
func KindOf(path string) LinkKind {
	return kindOf(path)
}

// RestoreLink creates again a link that was removed, of the kind it was (see KindOf), so that it works as it did even
// when its target is missing for now: CreateLink, unable to tell a missing folder from a missing file, would choose.
func RestoreLink(link, target string, kind LinkKind) error {
	return restoreLink(link, target, kind)
}

// linkTarget returns the path a link to target must hold: target's folder with links resolved, and target's own name,
// so that a link to an artifact that is itself a link still names the artifact (and its playbook folder), as
// ownership is decided by the name and the folder a link leads to.
func linkTarget(target string) string {
	return filepath.Join(Canonical(filepath.Dir(target)), filepath.Base(target))
}

// FileLinksAllowed reports whether links to files can be created: always on Linux and macOS; on Windows, only with
// developer mode or administrator rights, which it finds out by trying to create one in a temporary folder (any failure
// other than missing rights counts as allowed, so that creating the real links reports it).
func FileLinksAllowed() bool {
	return fileLinksAllowed()
}

// RemoveLink removes the link at path, never its target's content. It fails when path is not a link. Its target
// does not matter: it is not resolved (a link whose target cannot be resolved can be removed too).
func RemoveLink(path string) error {
	if !isLink(path) {
		return &os.PathError{Op: "remove link", Path: path, Err: errors.New("not a symbolic link or junction")}
	}
	// os.Remove deletes a symbolic link itself (to a folder or a file), and on Windows removes a junction with
	// RemoveDirectory, which does not touch the target's content. Never use os.RemoveAll here.
	return os.Remove(path)
}
