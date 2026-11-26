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

package playbook

import (
	"bytes"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
)

// maxFrontmatter limits how much of a file is read to find its frontmatter.
const maxFrontmatter = 64 << 10

// MetadataVersion returns the version that a file, such as a SKILL.md, declares in its YAML frontmatter as
// metadata.version (as in the Agent Skills specification), exactly as written ("1.0" stays "1.0"), or "" when it
// declares none, or its frontmatter cannot be read. It is informational only, so it only reads regular files: never
// something that could keep it waiting, such as a named pipe.
func MetadataVersion(file string) string {
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFrontmatter))
	if err != nil {
		return ""
	}
	front, ok := frontmatter(data)
	if !ok {
		return ""
	}
	var doc struct {
		Metadata map[string]yaml.Node `yaml:"metadata"`
	}
	if yaml.Unmarshal(front, &doc) != nil {
		return ""
	}
	if v, ok := doc.Metadata["version"]; ok && v.Kind == yaml.ScalarNode && v.Tag != "!!null" {
		return v.Value
	}
	return ""
}

// frontmatter returns the YAML between a leading "---" line and the next "---" line.
func frontmatter(data []byte) ([]byte, bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, false
	}
	if bytes.HasPrefix(rest, []byte("---\n")) || bytes.Equal(rest, []byte("---")) {
		return nil, true // empty frontmatter
	}
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		if !bytes.HasSuffix(rest, []byte("\n---")) {
			return nil, false
		}
		end = len(rest) - len("\n---")
	}
	return rest[:end+1], true
}
