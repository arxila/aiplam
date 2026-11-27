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

//go:build windows

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOnNetwork checks which paths are taken for network shares, whose folders junctions cannot lead to.
func TestOnNetwork(t *testing.T) {
	local := filepath.VolumeName(os.TempDir()) + `\x`
	for path, want := range map[string]bool{
		`\\server\share\x`:        true,
		`\\?\UNC\server\share\x`:  true,
		local:                     false,
		`\\?\` + local:            false,
		filepath.Join(local, "y"): false,
	} {
		if got := onNetwork(path); got != want {
			t.Errorf("onNetwork(%q) = %v, want %v", path, got, want)
		}
	}
}
