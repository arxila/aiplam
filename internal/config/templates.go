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

import "strings"

// This file holds the agent templates: for each agent, the target folders where it reads each kind of artifact, in a
// project and in the user managed folder (the home folder). A template only fills in a profile's targets when the
// profile is created; from then on, the profile works as if its targets had been given by hand. Keep the table up to
// date with the agents' documentation.

// Skills is the kind of artifact whose items are skills (folders holding a SKILL.md): the only kind aiplam knows
// anything about, to show the version a skill declares.
const Skills = "skills"

// CustomAgent is the agent of profiles whose targets were given by hand.
const CustomAgent = "custom"

// StandardVariant is the suffix of the template variants that link skills from the standard .agents/skills folder
// instead of the agent's own skills folder: "junie:standard".
const StandardVariant = ":standard"

// standardSkills is the standard skills folder, the same in a project and in the user managed folder.
const standardSkills = ".agents/skills"

// AppData is the placeholder for the Windows %APPDATA% folder, allowed as the first segment of the target folders of
// the user managed folder.
const AppData = "%APPDATA%"

// TemplateTarget is where an agent reads one kind of artifact.
type TemplateTarget struct {
	Kind string
	// Project is the target folder in a project's managed folder ("" when the agent does not read the kind there).
	Project string
	// User is the target folder in the user managed folder ("" when the agent does not read the kind there).
	User string
	// UserWindows, when set, replaces User on Windows.
	UserWindows string
}

// Template is an agent template.
type Template struct {
	// Name is the template's name, as given with --agent.
	Name string
	// Product is the name of the agent's product, for messages.
	Product string
	// Targets are the kinds the agent reads, in the order they are shown.
	Targets []TemplateTarget
	// Standard says whether the template has a ":standard" variant, which links skills from .agents/skills.
	Standard bool
}

// both is a TemplateTarget read from the same folder in projects and in the user managed folder.
func both(kind, folder string) TemplateTarget {
	return TemplateTarget{Kind: kind, Project: folder, User: folder}
}

// TemplateTable lists the agent templates: the standard one first, then the others from the most to the least used
// (by the developer surveys of 2026: JetBrains' Developer Ecosystem, Stack Overflow's pulse survey and The Pragmatic
// Engineer's AI tooling survey). The order is the one aiplam shows them in.
var TemplateTable = []Template{
	{
		Name:    "standard",
		Product: "the cross-agent .agents convention",
		Targets: []TemplateTarget{both(Skills, standardSkills)},
	},
	{
		Name:    "claude",
		Product: "Claude Code",
		Targets: []TemplateTarget{
			both(Skills, ".claude/skills"),
			both("subagents", ".claude/agents"),
			both("commands", ".claude/commands"),
			both("rules", ".claude/rules"),
		},
	},
	{
		// Copilot reads .github in projects but ~/.copilot in the home folder. Prompt files are deprecated, and only
		// read in projects.
		Name:    "copilot",
		Product: "GitHub Copilot",
		Targets: []TemplateTarget{
			{Kind: Skills, Project: ".github/skills", User: ".copilot/skills"},
			{Kind: "subagents", Project: ".github/agents", User: ".copilot/agents"},
			{Kind: "prompts", Project: ".github/prompts"},
			{Kind: "instructions", Project: ".github/instructions", User: ".copilot/instructions"},
		},
		Standard: true,
	},
	{
		// Cursor keeps user rules in its settings, not in files.
		Name:    "cursor",
		Product: "Cursor",
		Targets: []TemplateTarget{
			both(Skills, ".cursor/skills"),
			both("subagents", ".cursor/agents"),
			both("commands", ".cursor/commands"),
			{Kind: "rules", Project: ".cursor/rules"},
		},
		Standard: true,
	},
	{
		// Codex reads skills from the standard folder: its ":standard" variant is the same for now, and is kept apart
		// in case Codex gets a skills folder of its own.
		Name:    "codex",
		Product: "OpenAI Codex",
		Targets: []TemplateTarget{
			both(Skills, standardSkills),
			both("subagents", ".codex/agents"),
		},
		Standard: true,
	},
	{
		Name:    "gemini",
		Product: "Gemini CLI",
		Targets: []TemplateTarget{
			both(Skills, ".gemini/skills"),
			both("subagents", ".gemini/agents"),
			both("commands", ".gemini/commands"),
		},
		Standard: true,
	},
	{
		// Junie keeps user-level guidelines in a single file (~/.junie/AGENTS.md), so rules are project-only.
		Name:    "junie",
		Product: "JetBrains Junie",
		Targets: []TemplateTarget{
			both(Skills, ".junie/skills"),
			both("subagents", ".junie/agents"),
			both("commands", ".junie/commands"),
			{Kind: "rules", Project: ".junie/rules"},
		},
		Standard: true,
	},
	{
		Name:    "opencode",
		Product: "OpenCode",
		Targets: []TemplateTarget{
			{Kind: Skills, Project: ".opencode/skills", User: ".config/opencode/skills"},
			{Kind: "subagents", Project: ".opencode/agents", User: ".config/opencode/agents"},
			{Kind: "commands", Project: ".opencode/commands", User: ".config/opencode/commands"},
		},
		Standard: true,
	},
	{
		// Devin Desktop (formerly Windsurf) and Devin CLI. In the home folder, rules live in ~/.devin but everything
		// else in ~/.config/devin (on Windows, %APPDATA%\devin).
		Name:    "devin",
		Product: "Devin (formerly Windsurf)",
		Targets: []TemplateTarget{
			{Kind: Skills, Project: ".devin/skills", User: ".config/devin/skills", UserWindows: AppData + "/devin/skills"},
			{Kind: "subagents", Project: ".devin/agents", User: ".config/devin/agents",
				UserWindows: AppData + "/devin/agents"},
			both("rules", ".devin/rules"),
		},
		Standard: true,
	},
}

// LookupTemplate returns the template named by spec ("claude", or "junie:standard" for a variant), and whether spec
// is its ":standard" variant.
func LookupTemplate(spec string) (Template, bool, bool) {
	name, standard := strings.CutSuffix(spec, StandardVariant)
	for _, t := range TemplateTable {
		if t.Name == name && (!standard || t.Standard) {
			return t, standard, true
		}
	}
	return Template{}, false, false
}

// TemplateNames returns the names of the templates, each followed by its ":standard" variant, in table order.
func TemplateNames() []string {
	var names []string
	for _, t := range TemplateTable {
		names = append(names, t.Name)
		if t.Standard {
			names = append(names, t.Name+StandardVariant)
		}
	}
	return names
}

// TemplateList describes the templates: "standard, claude, copilot, copilot:standard, ...".
func TemplateList() string {
	return strings.Join(TemplateNames(), ", ")
}

// TemplateTargets returns the targets that the template named by spec gives a profile in the user managed folder (when
// user is set) or in a project's, on the operating system goos ("windows", "darwin", ...).
func TemplateTargets(spec string, user bool, goos string) (Targets, bool) {
	t, standard, ok := LookupTemplate(spec)
	if !ok {
		return nil, false
	}
	var targets Targets
	for _, tt := range t.Targets {
		folder := tt.Project
		if user {
			folder = tt.User
			if goos == "windows" && tt.UserWindows != "" {
				folder = tt.UserWindows
			}
		}
		if standard && tt.Kind == Skills {
			folder = standardSkills
		}
		if folder != "" {
			targets = append(targets, Target{Kind: tt.Kind, Folder: folder})
		}
	}
	return targets, true
}
