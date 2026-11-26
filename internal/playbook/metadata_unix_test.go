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

package playbook

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestMetadataVersionSpecialFiles checks that only regular files are read: a named pipe nobody writes to does not
// keep it waiting.
func TestMetadataVersionSpecialFiles(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "SKILL.md")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() { done <- MetadataVersion(pipe) }()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("named pipe: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a named pipe did not return")
	}
}
