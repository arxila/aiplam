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

//go:build unix

package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestFailedInitRemovesItsFolder checks that an init that fails after creating the .aiplam folder removes it, so that
// the folder does not become a managed folder without configuration, and that it never removes one it did not create.
func TestFailedInitRemovesItsFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not apply to root")
	}
	e := newEnv(t)
	project := e.mkdir(filepath.Join(e.root, "project"))
	dir := filepath.Join(project, ".aiplam")

	// With this umask, the .aiplam folder is created without write permission, so nothing can be written in it.
	old := syscall.Umask(0o222)
	code, _, _ := e.run("init", project, "-a", "claude")
	syscall.Umask(old)
	if code != ExitFailure {
		t.Fatalf("init: exit %d, want %d", code, ExitFailure)
	}
	e.assertMissing(dir)

	// A .aiplam folder that was already there (here, read-only) is left in place.
	e.mkdir(dir)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	e.mustRun(ExitFailure, "init", project, "-a", "claude")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the .aiplam folder that was there before was removed: %v", err)
	}
}
