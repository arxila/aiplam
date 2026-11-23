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

package config

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// This file holds the table of the agents aiplam links artifacts for: which kinds of artifacts each agent reads,
// from which folders (in a project's base folder and in the home folder) and in which format. Keep it up to date
// with the agents' documentation; nothing else in aiplam needs changing to add an agent or adjust its folders.

// Kind is a kind of artifact a playbook can provide.
type Kind string

// The kinds of artifacts, in the order they are shown.
const (
	Skills       Kind = "skills"       // folders holding a SKILL.md
	Subagents    Kind = "subagents"    // custom agent definitions
	Commands     Kind = "commands"     // custom slash commands
	Prompts      Kind = "prompts"      // reusable prompt files
	Rules        Kind = "rules"        // rule files
	Instructions Kind = "instructions" // instruction files
)

// Kinds are the kinds of artifacts, in the order they are shown.
var Kinds = []Kind{Skills, Subagents, Commands, Prompts, Rules, Instructions}

// Singular returns the name of one artifact of the kind, for messages: "skill", "subagent", ...
func (k Kind) Singular() string {
	switch k {
	case Subagents:
		return "subagent"
	case Commands:
		return "command"
	case Prompts:
		return "prompt"
	case Rules:
		return "rule"
	case Instructions:
		return "instructions file"
	}
	return "skill"
}

// Place is where an agent reads one kind of artifact from: the folder Sub of the agents configuration folder Dir,
// both relative to the base folder, in slash form. A zero Place means the agent does not read that kind there.
type Place struct {
	// Dir is the agents configuration folder (for example ".claude", or ".config/opencode" in the home folder). It is
	// removed, like Sub, when it becomes empty after unlinking artifacts.
	Dir string
	// Sub is the folder of Dir where the artifacts are linked (for example "skills").
	Sub string
}

// IsZero reports whether p is the zero Place.
func (p Place) IsZero() bool { return p.Dir == "" }

// Format says which items of a kind's folder, in a playbook, are artifacts for an agent.
type Format struct {
	// File, when set, makes artifacts folders that hold a file with that name (for skills, "SKILL.md").
	File string
	// Ext, when File is not set, makes artifacts the files with that extension (".md", ".agent.md", ".toml", ...).
	Ext string
}

// Support describes how an agent reads one kind of artifact.
type Support struct {
	// Project is where the agent reads the kind in a base folder other than the home folder.
	Project Place
	// Home is where the agent reads the kind in the home folder.
	Home Place
	// Format says which artifacts the agent reads.
	Format Format
}

// Agent is an agent aiplam can link artifacts for.
type Agent struct {
	// Name is the agent's name in aiplam's configuration and command line.
	Name string
	// Product is the name of the agent's product, for messages.
	Product string
	// Kinds are the kinds of artifacts the agent reads, and how.
	Kinds map[Kind]Support
	// ReadsStandard are the kinds the agent also reads from the folders of the standard agent (besides its own).
	ReadsStandard []Kind
}

// DefaultAgent is the agent of the profile 'aiplam init' creates when none is given.
const DefaultAgent = "standard"

var skillFormat = Format{File: "SKILL.md"}

// both is a Support for a kind that the agent reads from the same subfolder of dir in project base folders and of
// homeDir in the home folder.
func both(dir, homeDir, sub string, format Format) Support {
	return Support{Project: Place{dir, sub}, Home: Place{homeDir, sub}, Format: format}
}

// projectOnly is a Support for a kind that the agent reads in project base folders only.
func projectOnly(dir, sub string, format Format) Support {
	return Support{Project: Place{dir, sub}, Format: format}
}

// files is the Format of artifacts that are files with an extension.
func files(ext string) Format { return Format{Ext: ext} }

// AgentTable lists the agents. For agents that read several folders for a kind, the one listed is the agent's own
// folder with the highest precedence (and, on equal precedence, the most standard spelling: "skills", not "skill").
var AgentTable = []Agent{
	{
		Name:    "standard",
		Product: "the cross-agent .agents convention",
		Kinds: map[Kind]Support{
			Skills: both(".agents", ".agents", "skills", skillFormat),
		},
	},
	{
		Name:    "claude",
		Product: "Claude Code",
		Kinds: map[Kind]Support{
			Skills:    both(".claude", ".claude", "skills", skillFormat),
			Subagents: both(".claude", ".claude", "agents", files(".md")),
			Commands:  both(".claude", ".claude", "commands", files(".md")),
			Rules:     both(".claude", ".claude", "rules", files(".md")),
		},
	},
	{
		// Codex reads skills from the standard folders only; its other artifacts live in .codex.
		Name:    "codex",
		Product: "OpenAI Codex",
		Kinds: map[Kind]Support{
			Skills:    both(".agents", ".agents", "skills", skillFormat),
			Subagents: both(".codex", ".codex", "agents", files(".toml")),
		},
	},
	{
		// Copilot reads .github in projects but ~/.copilot in the home folder. Its home folder for agents is only
		// documented by community guides.
		Name:    "copilot",
		Product: "GitHub Copilot",
		Kinds: map[Kind]Support{
			Skills:       both(".github", ".copilot", "skills", skillFormat),
			Subagents:    both(".github", ".copilot", "agents", files(".agent.md")),
			Prompts:      projectOnly(".github", "prompts", files(".prompt.md")),
			Instructions: projectOnly(".github", "instructions", files(".instructions.md")),
		},
		ReadsStandard: []Kind{Skills},
	},
	{
		Name:    "gemini",
		Product: "Gemini CLI",
		Kinds: map[Kind]Support{
			Skills:    both(".gemini", ".gemini", "skills", skillFormat),
			Subagents: both(".gemini", ".gemini", "agents", files(".md")),
			Commands:  both(".gemini", ".gemini", "commands", files(".toml")),
		},
		ReadsStandard: []Kind{Skills},
	},
	{
		// Cursor keeps user rules in its settings, not in files.
		Name:    "cursor",
		Product: "Cursor",
		Kinds: map[Kind]Support{
			Skills:    both(".cursor", ".cursor", "skills", skillFormat),
			Subagents: both(".cursor", ".cursor", "agents", files(".md")),
			Commands:  both(".cursor", ".cursor", "commands", files(".md")),
			Rules:     projectOnly(".cursor", "rules", files(".mdc")),
		},
		ReadsStandard: []Kind{Skills},
	},
	{
		Name:    "opencode",
		Product: "OpenCode",
		Kinds: map[Kind]Support{
			Skills:    both(".opencode", ".config/opencode", "skills", skillFormat),
			Subagents: both(".opencode", ".config/opencode", "agents", files(".md")),
			Commands:  both(".opencode", ".config/opencode", "commands", files(".md")),
		},
		ReadsStandard: []Kind{Skills},
	},
	{
		// Devin Desktop (formerly Windsurf) and Devin CLI. In the home folder, rules live in ~/.devin but everything
		// else in ~/.config/devin (on Windows, %APPDATA%\devin, which is not supported yet).
		Name:    "devin",
		Product: "Devin (formerly Windsurf)",
		Kinds: map[Kind]Support{
			Skills:    both(".devin", ".config/devin", "skills", skillFormat),
			Subagents: both(".devin", ".config/devin", "agents", files(".md")),
			Rules:     both(".devin", ".devin", "rules", files(".md")),
		},
		ReadsStandard: []Kind{Skills},
	},
	{
		// Junie keeps user-level guidelines in a single file (~/.junie/AGENTS.md), so rules are project-only.
		Name:    "junie",
		Product: "JetBrains Junie",
		Kinds: map[Kind]Support{
			Skills:    both(".junie", ".junie", "skills", skillFormat),
			Subagents: both(".junie", ".junie", "agents", files(".md")),
			Commands:  both(".junie", ".junie", "commands", files(".md")),
			Rules:     projectOnly(".junie", "rules", files(".md")),
		},
		ReadsStandard: []Kind{Skills},
	},
}

// LookupAgent returns the agent named name.
func LookupAgent(name string) (Agent, bool) {
	for _, ag := range AgentTable {
		if ag.Name == name {
			return ag, true
		}
	}
	return Agent{}, false
}

// AgentNames returns the names of the agents, in table order.
func AgentNames() []string {
	names := make([]string, len(AgentTable))
	for i, ag := range AgentTable {
		names[i] = ag.Name
	}
	return names
}

// Place returns where the agent reads kind in a base folder (the home folder when home is set), and whether it
// reads it there at all.
func (ag Agent) Place(kind Kind, home bool) (Place, bool) {
	s, ok := ag.Kinds[kind]
	if !ok {
		return Place{}, false
	}
	p := s.Project
	if home {
		p = s.Home
	}
	return p, !p.IsZero()
}

// KindFolder is a folder of a base folder where an agent reads one kind of artifact, and where aiplam links the
// artifacts of that kind for the agent.
type KindFolder struct {
	Agent string
	Kind  Kind
	// DefaultPath is the folder, relative to a playbook folder, where the playbook's artifacts of the kind are by
	// default: the name of the subfolder where the agent reads them ("agents" for Claude Code's subagents).
	DefaultPath string
	// Dir is the folder the artifacts are linked from.
	Dir string
	// AgentsConfigDir is the agents configuration folder holding Dir. Like Dir, it is removed when it becomes empty
	// after unlinking artifacts.
	AgentsConfigDir string
	Format          Format
}

// KindFolders returns the folders of base (the home folder when home is set) where the agent reads artifacts, in
// the order of Kinds.
func (ag Agent) KindFolders(base string, home bool) []KindFolder {
	var folders []KindFolder
	for _, k := range Kinds {
		p, ok := ag.Place(k, home)
		if !ok {
			continue
		}
		dir := filepath.Join(base, filepath.FromSlash(p.Dir))
		folders = append(folders, KindFolder{Agent: ag.Name, Kind: k, DefaultPath: path.Base(p.Sub),
			Dir: filepath.Join(dir, filepath.FromSlash(p.Sub)), AgentsConfigDir: dir, Format: ag.Kinds[k].Format})
	}
	return folders
}

// SupportedKinds returns the kinds the agent reads in project base folders or in the home folder, in the order of
// Kinds.
func (ag Agent) SupportedKinds() []Kind {
	var kinds []Kind
	for _, k := range Kinds {
		if _, ok := ag.Kinds[k]; ok {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// compoundExts are the extensions of the table with more than one dot (".agent.md"), longest first: a file
// "x.agent.md" has the extension ".agent.md", not ".md".
var compoundExts = func() []string {
	seen := map[string]bool{}
	var exts []string
	for _, ag := range AgentTable {
		for _, s := range ag.Kinds {
			if e := s.Format.Ext; strings.Count(e, ".") > 1 && !seen[e] {
				seen[e] = true
				exts = append(exts, e)
			}
		}
	}
	sort.Slice(exts, func(i, j int) bool { return len(exts[i]) > len(exts[j]) })
	return exts
}()

// ItemExt returns the extension of a file name as the table sees it: a compound extension of the table
// (".agent.md") when the name ends with one, or else its last extension (".md").
func ItemExt(name string) string {
	for _, e := range compoundExts {
		if strings.HasSuffix(name, e) && len(name) > len(e) {
			return e
		}
	}
	return filepath.Ext(name)
}

// agentList describes the known agents: "standard, claude, codex, ...".
func agentList() string {
	return strings.Join(AgentNames(), ", ")
}
