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
	"os"
	"path/filepath"
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
