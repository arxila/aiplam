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

//go:build !windows

package fsutil

import (
	"os"
	"path/filepath"
)

func fileLinksAllowed() bool { return true }

// createLink creates a symbolic link whose target is relative to the link's folder (with links resolved), so that the
// link keeps working when a folder holding both the link and its target is moved.
func createLink(link, target string) error {
	if rel, err := filepath.Rel(Canonical(filepath.Dir(link)), Canonical(target)); err == nil {
		target = rel
	}
	return os.Symlink(target, link)
}
