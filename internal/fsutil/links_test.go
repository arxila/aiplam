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
	"testing"
)

func TestLinks(t *testing.T) {
	root := Canonical(t.TempDir())
	target := filepath.Join(root, "target dir")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(content, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")

	if state, _, err := Inspect(link); err != nil || state != Missing {
		t.Fatalf("missing: state %v, err %v", state, err)
	}
	if err := CreateLink(link, target); err != nil {
		t.Fatal(err)
	}
	state, current, err := Inspect(link)
	if err != nil || state != Link || !SamePath(current, target) {
		t.Fatalf("link: state %v -> %q, err %v", state, current, err)
	}
	if !IsFile(filepath.Join(link, "SKILL.md")) {
		t.Fatal("the link does not give access to the target's content")
	}
	if err := RemoveLink(link); err != nil {
		t.Fatal(err)
	}
	if !IsFile(content) {
		t.Fatal("removing the link removed the target's content")
	}
	if state, _, _ := Inspect(link); state != Missing {
		t.Fatal("link not removed")
	}

	// Real folders are reported as such and never removed.
	if state, _, err := Inspect(target); err != nil || state != NotLink {
		t.Fatalf("folder: state %v, err %v", state, err)
	}
	if err := RemoveLink(target); err == nil {
		t.Fatal("RemoveLink removed a real folder")
	}

	// A broken link is still a link.
	gone := filepath.Join(root, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(link, gone); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if state, current, err := Inspect(link); err != nil || state != Link || !SamePath(current, gone) {
		t.Fatalf("broken link: state %v -> %q, err %v", state, current, err)
	}
	if err := RemoveLink(link); err != nil {
		t.Fatal(err)
	}

	// Links to files (on Windows, symbolic links, which need developer mode or administrator rights).
	fileLink := filepath.Join(root, "file-link.md")
	if err := CreateLink(fileLink, content); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links to files are not allowed here: %v", err)
		}
		t.Fatal(err)
	}
	if state, current, err := Inspect(fileLink); err != nil || state != Link || !SamePath(current, content) {
		t.Fatalf("file link: state %v -> %q, err %v", state, current, err)
	}
	if !IsFile(fileLink) {
		t.Fatal("the link does not give access to the file")
	}
	if err := RemoveLink(fileLink); err != nil {
		t.Fatal(err)
	}
	if !IsFile(content) {
		t.Fatal("removing the link removed the file")
	}
}

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	// File locks belong to each opening of the file, so a second Lock conflicts even in the same process.
	if _, err := Lock(path); err != ErrLocked {
		t.Fatalf("second lock: %v, want ErrLocked", err)
	}
	unlock()
	unlock2, err := Lock(path)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	unlock2()
	if !IsFile(path) {
		t.Fatal("the lock file was removed")
	}
	if _, err := Lock(filepath.Join(path, "missing", ".lock")); err == nil {
		t.Fatal("locked a file in a missing folder")
	}
	// A link is never followed, even a broken one.
	link := filepath.Join(filepath.Dir(path), "link.lock")
	target := filepath.Join(filepath.Dir(path), "target")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(link); err == nil {
		t.Fatal("locked through a link")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the link's target was created: %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	home := filepath.Join(string(filepath.Separator)+"h", "u")
	cases := map[string]string{
		"~":          home,
		"~/.agents":  filepath.Join(home, ".agents"),
		"/x/~/y":     "/x/~/y",
		"~other/dir": "~other/dir",
	}
	for in, want := range cases {
		if got := ExpandHome(in, home); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	root := Canonical(t.TempDir())
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(filepath.Join(root, "linked"), real); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		real:                                    real,
		filepath.Join(real, "a", "b"):           filepath.Join(real, "a", "b"),
		filepath.Join(root, "linked", "a", "b"): filepath.Join(real, "a", "b"),
		filepath.Join(root, "file", "a"):        filepath.Join(root, "file", "a"),
	} {
		if got, err := Resolve(path); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	// Names with characters whose code is a path separator's plus a multiple of 256 ('į' is U+012F, 'Ŝ' is U+015C).
	for _, name := range []string{"dirį", "Ŝdir"} {
		dir := filepath.Join(real, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if got, err := Resolve(filepath.Join(root, "linked", name, "a")); err != nil || got != filepath.Join(dir, "a") {
			t.Errorf("Resolve(linked/%s/a) = %q, %v; want %q", name, got, err, filepath.Join(dir, "a"))
		}
	}
	if runtime.GOOS == "windows" {
		return // symbolic links need developer mode
	}
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}
	// A broken link: where it would lead.
	if got, err := Resolve(filepath.Join(root, "broken", "a")); err != nil || got != filepath.Join(root, "gone", "a") {
		t.Errorf("Resolve(broken/a) = %q, %v; want %q", got, err, filepath.Join(root, "gone", "a"))
	}
	// A relative target going up, reached through a link: '..' applies to where the link really is.
	if err := os.MkdirAll(filepath.Join(root, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(filepath.Join(root, "deep", "inner"), real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(real, "up")); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "a")
	if got, err := Resolve(filepath.Join(root, "deep", "inner", "up", "a")); err != nil || got != want {
		t.Errorf("Resolve(deep/inner/up/a) = %q, %v; want %q", got, err, want)
	}
	// '..' after a link, in a link's target or in the path: it applies to where that link leads, as the system does.
	deep := filepath.Join(root, "other", "deep")
	inside := filepath.Join(root, "inside")
	for _, dir := range []string{deep, inside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(deep, filepath.Join(inside, "hop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("hop/../x", filepath.Join(inside, "relative")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside+"/hop/../y", filepath.Join(inside, "absolute")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(inside, "relative"):            filepath.Join(root, "other", "x"),
		filepath.Join(inside, "absolute", "a"):       filepath.Join(root, "other", "y", "a"),
		inside + "/hop/../z":                         filepath.Join(root, "other", "z"),
		inside + "/missing/../hop/../z":              filepath.Join(root, "other", "z"),
		inside + "/missing/more/../../relative/../w": filepath.Join(root, "other", "w"),
	} {
		if got, err := Resolve(path); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	if err := os.Symlink("loop", filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(filepath.Join(root, "loop", "a")); err == nil {
		t.Errorf("Resolve through a loop of links = %q, want an error", got)
	}
}

// TestResolveShortNames checks that, on Windows, a path written with short (8.3) names and the target of a link
// leading into it, written with long names, resolve to the same spelling.
func TestResolveShortNames(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("short names only exist on Windows")
	}
	short, err := os.MkdirTemp(os.TempDir(), "short-names-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	long := Canonical(short)
	if long == short {
		t.Skip("the temporary folder has no short names: " + short)
	}
	if err := os.MkdirAll(filepath.Join(long, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(filepath.Join(short, "linked"), filepath.Join(long, "real")); err != nil {
		t.Fatal(err)
	}
	top, err := Resolve(short)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(filepath.Join(short, "linked", "a")); err != nil || !Within(got, top) {
		t.Errorf("Resolve(linked/a) = %q, %v; want it inside %q", got, err, top)
	}
}
