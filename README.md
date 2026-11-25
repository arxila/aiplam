# aiplam: AI Playbook Manager

`aiplam` makes coding agents (Claude Code, GitHub Copilot, Cursor, Codex, Gemini CLI, ...) use the skills, subagents,
commands and other artifacts of **AI playbooks**: git repositories of such artifacts shared by a team or by a family
of related projects.

Each developer keeps a local clone of each playbook, checked out at the tag (or commit) they want to use. `aiplam`
registers those clones and links their artifacts into the **target folders** that coding agents read them from
(`.agents/skills`, `.claude/agents`, `.github/prompts`, ...), in the home folder or in any folder where agent sessions
start. Agents follow the links and read the files straight from the clones, so switching a clone to another tag and
running `aiplam update` is all an update takes.

```text
 ┌─────────────────────────┐   aiplam add   ┌─────────────────────────┐     links      ┌─────────────────────────┐
 │ playbook folder         │ ─────────────► │ .aiplam/<profile>.yaml  │ ─────────────► │ target folders:         │
 │ (a git clone, checked   │  registers it  │ the profile's target    │ (enabled only) │ .claude/skills,         │
 │ out at a tag)           │                │ folders and playbooks   │                │ .github/agents, ...     │
 └─────────────────────────┘                └─────────────────────────┘                └─────────────────────────┘
              ▲                                                                                     │
              └──────────── agents follow the links and read the playbook's own files ──────────────┘
```

`aiplam` never clones, fetches, pulls or switches a playbook checkout: selecting versions is the developer's job. It
only creates and removes links, and the folders that hold them (see [Safety](#safety)).

## Install

`aiplam` is a single executable with no dependencies, for Linux, macOS and Windows. Put it in a folder in your
`PATH` (`aiplam.exe` on Windows). `git` is optional: without it, playbook versions are shown as `unknown`.

With Go 1.26 or later installed, `go install arxila.io/aiplam/cmd/aiplam@latest` builds it into `$(go env GOPATH)/bin`.

To build it from a clone:

```bash
make build      # ./aiplam for this machine
make dist       # dist/aiplam-<version>-<os>-<arch>.tar.gz (.zip for Windows) per platform, and SHA256SUMS
```

## Quick start

```bash
aiplam init --user --agent claude                   # ~ as managed folder, with a profile for Claude Code
cd ~/Workspace/acme-ai-playbook
git switch --detach v1.2.0                          # choose the playbook's version
aiplam add acme .                                   # register it as 'acme', and link its acme-* artifacts
aiplam list                                         # what is linked, and from where
git switch --detach v1.3.0                          # later: another version...
aiplam update                                       # ...and its new or removed artifacts are linked or unlinked
```

## Concepts

### Managed folders

A **managed folder** is a folder with a `.aiplam` folder (the **aiplam configuration folder**), which holds aiplam's
configuration for it. Usually these are the home folder, the **user managed folder**, and the folders where agent
sessions start, such as a project's top folder. A managed folder holds:

- its `.aiplam` folder: `aiplam-config.yaml` (the **aiplam configuration**: which profiles are enabled) and one
  `<profile>.yaml` per profile. Agents never read it;
- the **target folders** of its profiles (`.claude/skills`, `.agents/skills`, ...), where the artifacts are linked and
  agents read them. aiplam only manages the links in them that belong to its playbooks: anything else there
  (settings, artifacts added by hand) is left alone.

This is a home folder set up with `aiplam init --user --agent claude`, after `aiplam add acme
~/Workspace/acme-ai-playbook`:

```text
~/                                         the user managed folder
├── .aiplam/                               aiplam's configuration (agents never read it)
│   ├── aiplam-config.yaml                 enabled: [default]
│   └── default.yaml                       agent: claude; targets: skills: .claude/skills, ...; playbooks: acme
├── .claude/                               Claude Code reads it
│   ├── settings.json                      not aiplam's: left alone
│   ├── skills/                            target folder of kind skills
│   │   ├── acme-deploy   ─── link ───►    ~/Workspace/acme-ai-playbook/skills/acme-deploy/
│   │   ├── acme-review   ─── link ───►    ~/Workspace/acme-ai-playbook/skills/acme-review/
│   │   └── my-own-skill/                  added by hand: left alone
│   ├── agents/                            target folder of kind subagents
│   │   └── acme-reviewer.md ── link ──►   ~/Workspace/acme-ai-playbook/subagents/acme-reviewer.md
│   └── rules/                             target folder of kind rules
│       └── acme-style.md ─── link ───►    ~/Workspace/acme-ai-playbook/rules/acme-style.md
└── Workspace/
    └── acme-ai-playbook/                  the playbook folder (a git clone)
```

**Which managed folder a command uses.** Commands use the nearest managed folder from the current folder up to the
home folder; when there is none (in particular, from folders outside the home folder, up to the root of the file
system), the home folder, if it is managed. Folders above the home folder are never used from inside it. `--user`
(`-u`) selects the user managed folder from anywhere. Commands that change something start by naming the managed
folder they act on (`Managed folder: ...`). The current folder is walked up as given, so inside a linked artifact
(such as `.agents/skills/x`) the managed folder is the one above the link, not one in the playbook folder.

```text
~/                                         user managed folder
├── .aiplam/
└── Workspace/
    ├── big-project/                       project's managed folder: sessions start here or below
    │   ├── .aiplam/
    │   ├── .claude/skills/...
    │   └── module-a/                      commands run here use big-project's configuration
    └── small-project/                     not managed: commands run here use ~'s configuration
```

`aiplam status` and `aiplam list` show the managed folder that applies, whether it is the user managed folder, and
whether it is in a git repository (and where its root is). Each managed folder is independent: aiplam does not check
what higher-level managed folders link, nor which of them an agent also reads.

**Sharing a project's configuration.** Playbook folders are stored relative to the managed folder, so a project's
`.aiplam` folder can be committed and shared (on Windows, a playbook must be on the same drive as the managed folder).
The links themselves are specific to each machine (and on Windows, links to folders are junctions, which git cannot
store as links): when the managed folder is inside a git repository, `init` suggests lines to add to `.gitignore` for
its target folders (such as `/.claude/skills/<name>-*`).

### Profiles and target folders

A **profile** is a working scope: a group of playbooks that are used together, such as those of a client, a team or a
kind of work. Each profile is kept in its own file (`.aiplam/<profile>.yaml`) with:

- its **target folders**: for each **kind** of artifact, the folder (relative to the managed folder) where its
  artifacts are linked. Kinds are any names (`skills`, `subagents`, `my-prompts`...): aiplam only knows `skills`, to
  show the versions skills declare. In the user managed folder on Windows, a target folder can also start with
  `%APPDATA%`; nothing else can leave the managed folder, and `..` is never allowed;
- its **agent**: the agent template its target folders were taken from (see [Agent templates](#agent-templates)), or
  `custom` when they were given by hand. It is only a label;
- its registered **playbooks**.

Only the playbooks of the **enabled** profiles are linked, so moving from one scope to another is a matter of enabling
and disabling profiles. Any number of profiles can be enabled at once, combining their scopes.

```text
 .aiplam/
 ├── aiplam-config.yaml   enabled: [default, acme]
 ├── default.yaml         agent: claude   playbooks: team-common, arxila     enabled: always needed
 ├── acme.yaml            agent: claude   playbooks: acme                    enabled: working for client Acme now
 └── initech.yaml         agent: custom   playbooks: initech, initech-data   disabled: back to it next month
```

`init` creates the first profiles (`default`, or those given with `--profile`), all with the same target folders,
and enables them; `profile add` creates more, not enabled. Both take the target folders in one of two ways:

```bash
aiplam init --user --agent claude                                    # from an agent template
aiplam init . --agent junie:standard                                 # a template's :standard variant
aiplam init . --folder skills:.agents/skills,rules:.junie/rules      # by hand
aiplam profile add acme --agent claude                               # a profile for the work for client Acme
aiplam add acme ~/Workspace/acme/acme-ai-playbook -p acme            # register a playbook in it: nothing linked yet
aiplam profile enable acme                                           # link its playbooks too
aiplam profile switch acme                                           # work only on Acme: disable the other profiles
aiplam profile switch default,acme                                   # enable exactly these profiles
aiplam profile disable acme                                          # unlink Acme's playbooks
aiplam profile list                                                  # the profiles and agents; '*' marks the enabled
aiplam profile show acme                                             # its target folders, playbooks and mappings
aiplam profile remove old                                            # delete a profile (unlinking it if enabled)
```

No command changes a profile's target folders or agent: edit its file, or remove the profile and create it again. A
template only fills in the target folders when the profile is created, so `aiplam status` and `aiplam list` warn when
a profile's target folders no longer match its template (because they were edited, or because a later aiplam changed
the template): set its agent to `custom` if the change is intended.

A profile's file repeats its name (`name: acme` in `acme.yaml`), exactly as in the file name. A profile whose file is
not valid cannot be enabled, but it can still be disabled and removed: its links, if any, are then left in place for
you to remove (`aiplam status` reports those that lead into a registered playbook folder).

**Every link belongs to a single playbook of a single profile.** So:

- two playbooks can never link something at the same place, whether they are in the same profile or in profiles
  enabled together, even if both would link the same artifact;
- the same playbook (same name and playbook folder) cannot be registered in two profiles that are enabled together;
- nothing can be linked where something already is, even a link to the same artifact that aiplam did not make for
  that playbook.

`add`, `update`, `profile enable` and `profile switch` check this for everything that would be linked afterwards, and
change nothing if anything collides. Profiles that are never enabled together never collide: `profile switch` can
replace a profile with another that registers the same playbooks, and the links that stay the same are left alone.

In a profile that is not enabled, `add` and `remove` only register and unregister playbooks, and `update` has nothing
to do. Profile names use letters, digits, `_` and `-`; `aiplam-config` is reserved.

### Playbooks

A **playbook** is a folder (usually a git clone) holding agent artifacts. It has no manifest: when it is registered,
it is given a **name**, and, for each kind of its profile's target folders, its **artifacts** are the entries (folders
and files alike) of its **subfolder** for that kind whose names match the kind's **format**:

- by default, the subfolder is named like the kind (`skills/`, `subagents/`, ...), and the format is `<name>-*`;
- `aiplam add --folder <kind>:<subfolder>[:<format>]` (`-f`) sets another subfolder (`.` for the playbook folder
  itself; never `..`) and format, in which `*` matches any characters (for example `-f subagents:agents:*.agent.md`
  or `-f 'skills:.:*'`);
- hidden entries (named `.*`) are never artifacts, whatever the format, and artifacts never come from outside the
  playbook folder, not even through links;
- each matched entry is linked as is, under its own name (a skill's whole folder is linked, with any other files in
  it). aiplam does not check what an entry holds: a skill folder without a `SKILL.md` that matches the format is linked
  too.

```text
acme-ai-playbook/                    registered with: aiplam add acme ~/Workspace/acme-ai-playbook
├── README.md                        not an artifact
├── skills/                          kind skills: entries named acme-*
│   ├── acme-review/
│   │   ├── SKILL.md
│   │   └── checklist.md             part of the skill: the whole folder is linked
│   ├── acme-deploy/
│   │   └── SKILL.md
│   ├── .acme-draft/                 hidden: never an artifact
│   └── other-tool/                  not named acme-*: not an artifact of this playbook
├── subagents/
│   ├── acme-reviewer.md             Claude Code, Cursor, Gemini CLI, Junie, OpenCode, Devin
│   ├── acme-reviewer.agent.md       GitHub Copilot: give a format to pick one (-f 'subagents:subagents:*.agent.md')
│   └── acme-reviewer.toml           OpenAI Codex
└── rules/
    ├── acme-style.md
    └── acme-style.mdc               Cursor
```

The default format works as a prefix, so that several playbooks can be linked side by side in the same target folders
without clashing. A playbook meant for several agents can hold the same artifact in several formats, side by side:
each profile then picks its own with a format when the playbook is registered in it (see [Formats agents
read](#formats-agents-read)).

The same name can be registered from several playbook folders, as long as their last path segments differ. Their
**ids** are then `<name>@<last folder segment>` (for example `acme@acme-ai-playbook`); otherwise the id is the name.
Ids are computed, not stored.

Subfolders that do not exist yet are fine: what a subfolder holds is linked whenever the playbook is updated or its
profile enabled.

**Versions.** A playbook's version is whatever is checked out: the tags of its current commit, or else its branch.
Since the links point into the clone, changes to existing artifacts are seen at once; `aiplam update` is only needed
to link the artifacts a version adds and unlink those it removes.

Skills can declare their own version in their `SKILL.md` frontmatter, as the Agent Skills specification suggests:

```yaml
---
name: acme-deploy
description: ...
metadata:
  version: "2.1"
---
```

`aiplam list` shows, next to each link, its playbook's version (its tags, such as `v1.2.0`, or `branch: main` when no
tag points at the checked-out commit) and, for the kind `skills`, the version the skill declares, if any. Versions are
only shown, for information: aiplam always links whatever is checked out, and agents ignore `metadata.version`.

### Agent templates

An agent template fills in a profile's target folders with those where an agent reads each kind of artifact, which
can differ between a project and the user managed folder. Templates are listed (here and by aiplam) from the most to
the least used agent, according to the developer surveys of 2026, after the standard one:

- `standard`, the cross-agent `.agents` convention: skills in `.agents/skills` (and `~/.agents/skills`).
- `claude`, Claude Code: skills, subagents, commands and rules, in `.claude/skills`, `.claude/agents`,
  `.claude/commands` and `.claude/rules` (and the same in `~`).
- `copilot`, GitHub Copilot: skills and subagents in `.github/skills` and `.github/agents` (`~/.copilot/skills` and
  `~/.copilot/agents`), instructions in `.github/instructions` (`~/.copilot/instructions`), and prompts (deprecated) in
  `.github/prompts`, in projects only.
- `cursor`, Cursor: skills, subagents and commands in `.cursor/skills`, `.cursor/agents` and `.cursor/commands` (and
  the same in `~`), and rules in `.cursor/rules`, in projects only.
- `codex`, OpenAI Codex: skills in `.agents/skills` and subagents in `.codex/agents` (and the same in `~`).
- `gemini`, Gemini CLI: skills, subagents and commands in `.gemini/skills`, `.gemini/agents` and `.gemini/commands`
  (and the same in `~`).
- `junie`, JetBrains Junie: skills, subagents and commands in `.junie/skills`, `.junie/agents` and `.junie/commands`
  (and the same in `~`), and rules in `.junie/rules`, in projects only.
- `opencode`, OpenCode: skills, subagents and commands in `.opencode/skills`, `.opencode/agents` and
  `.opencode/commands` (`~/.config/opencode/...`).
- `devin`, Devin (formerly Windsurf): skills and subagents in `.devin/skills` and `.devin/agents`
  (`~/.config/devin/...`; on Windows, `%APPDATA%\devin\...`), and rules in `.devin/rules` (`~/.devin/rules`).

Every template but `standard` and `claude` also has a `:standard` variant (`junie:standard`, ...), which links skills
from the standard `.agents/skills` instead, as these agents read it too. `codex` and `codex:standard` are the same for
now: Codex reads skills from `.agents/skills` only. `aiplam profile --help` shows the same list, from the single
source file that defines it (`internal/config/templates.go`).

#### Formats agents read

Templates only give target folders: which entries of a playbook are linked is decided when the playbook is
registered, by its formats. When a playbook holds artifacts for several agents side by side, or files an agent would
misread, give each kind a format matching the files the agent reads:

| Agent | Subagents | Commands | Prompts | Rules | Instructions |
|---|---|---|---|---|---|
| Claude Code | `*.md` | `*.md` | | `*.md` | |
| GitHub Copilot | `*.agent.md` | | `*.prompt.md` | | `*.instructions.md` |
| Cursor | `*.md` | `*.md` | | `*.mdc` | |
| OpenAI Codex | `*.toml` | | | | |
| Gemini CLI | `*.md` | `*.toml` | | | |
| JetBrains Junie | `*.md` | `*.md` | | `*.md` | |
| OpenCode | `*.md` | `*.md` | | | |
| Devin | `*.md` | | | `*.md` | |

Skills are folders holding a `SKILL.md`, for every agent. Formats are plain patterns: `*.md` also matches
`acme-reviewer.agent.md`, so a playbook that holds both should keep them in different subfolders, or name its files
so that one format cannot match the other's (`acme-*.claude.md` and `acme-*.agent.md`). For example, for Copilot:

```bash
aiplam add acme ~/Workspace/acme-ai-playbook -f 'subagents:subagents:acme-*.agent.md' \
    -f 'instructions:instructions:acme-*.instructions.md'
```

### Guidelines: `AGENTS.md`, `CLAUDE.md` and the like

Agents also read a main instructions file: `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`,
... aiplam does not link or edit those files. Each has a fixed name, so there can be only one per folder: several
playbooks could not each provide one, and it would collide with the one the user (or the project) already has.

Instead, a playbook provides its guidelines as **rules** or **instructions**: one file per playbook, named after it
like any other artifact (`acme-guidelines.md`), which agents load besides the main instructions file. They never
collide with each other or with the user's own files, and aiplam links and unlinks them like any other artifact:

```text
 .claude/
 ├── CLAUDE.md                     the user's own: aiplam never touches it
 └── rules/                        Claude Code loads every file here, besides CLAUDE.md
     ├── acme-guidelines.md   ──►  ~/Workspace/acme-ai-playbook/rules/acme-guidelines.md
     ├── tools-guidelines.md  ──►  ~/Workspace/tools-ai-playbook/rules/tools-guidelines.md
     └── my-rules.md               the user's own: left alone
```

Where each agent's template links such files, and what they need to be loaded always (rather than only for some files
or when the agent finds them relevant), according to each agent's documentation:

| Agent | Format | Target folder | Loaded always when |
|---|---|---|---|
| Claude Code | `*.md` | `.claude/rules`, `~/.claude/rules` | its frontmatter has no `paths:` |
| Copilot | `*.instructions.md` | `.github/instructions`, `~/.copilot/instructions` | frontmatter `applyTo: "**"` |
| Cursor | `*.mdc` | `.cursor/rules` | its frontmatter has `alwaysApply: true` |
| Junie | `*.md` | `.junie/rules` | the project has no `.junie/AGENTS.md` |
| Devin | `*.md` | `.devin/rules`, `~/.devin/rules` | its frontmatter has `trigger: always_on` |

Codex, Gemini CLI, OpenCode and the standard template read guidelines only from their main instructions file
(`AGENTS.md`, `GEMINI.md`, ...), so aiplam cannot link guidelines for them; nor, in the home folder, for Cursor and
Junie, which read such files in projects only. A later version may manage a marked block of its own inside those main
instructions files, leaving the rest of them untouched.

## Commands

```bash
aiplam init <folder>|--user (--agent A | --folder K:F...) [--profile P...]   # make a managed folder, and profiles
aiplam status [<folder>|--user]                                   # managed folder, profiles, what needs attention
aiplam list [<folder>|--user]                                     # the links of the enabled profiles, by kind
aiplam add <name> <playbookfolder> [--folder K:S[:F]...]          # register a playbook and link its artifacts
aiplam update [<name|id|playbookfolder>] [<playbookfolder>]       # link new artifacts, unlink deleted ones
aiplam remove <name|id|playbookfolder> [<playbookfolder>]         # remove a playbook's links and unregister it
aiplam profile list|show|remove [<profile>]                       # manage profiles (working scopes)
aiplam profile add <profile> (--agent A | --folder K:F...)        # create a profile, with its target folders
aiplam profile enable|disable|switch <profile>[,<profile>...]     # choose the profiles linked
```

Every command acts on the managed folder that applies to the current folder, or on the user managed folder with
`--user` (`-u`); `status` and `list` also take a folder to start from. `add`, `update` and `remove` act on the enabled
profile, which must be the only one enabled, or on the profiles given with `--profile` (`-p`), enabled or not. Options
that take lists accept repeated options or comma-separated values; quote values holding commas or colons. Other short
forms: `-a` (`--agent`) and `-f` (`--folder`).

Without a selector, `update` acts on the playbook registered for the current folder. A selector is an id, a name
(when only one playbook has it), a playbook folder (when only one playbook is registered there), or a name and a
playbook folder.

Run `aiplam <command> --help` for the details of each command.

## Safety

- `aiplam` only creates and removes **links**: relative symbolic links on Linux and macOS. On Windows, links to
  folders (such as skills) are directory junctions, which need no administrator rights or developer mode, while links
  to files (such as subagents and rules) are symbolic links, which need developer mode or administrator rights. It
  never deletes a real file, nor a folder that holds anything (it only removes the folders its unlinking leaves empty,
  up to the managed folder, or to `%APPDATA%`), and never touches a playbook's content.
- Commands that change links are all-or-nothing: if anything would collide (see [Profiles and target
  folders](#profiles-and-target-folders)), nothing is changed and the problem is reported. If creating or removing a
  link fails midway, the changes already made are undone.
- Only links that belong to a playbook (they point into its subfolder for the kind, and their names match its format)
  are ever removed.
- Every command can be run again safely. `status` and `list` change nothing; `status` exits with 1 when anything needs
  attention.
- Commands that change a managed folder run one at a time: while one runs, others on the same managed folder fail
  (`status` and `list` still work). Interrupting one (Ctrl+C) takes effect once it has finished its changes, which
  takes milliseconds, so that links and configuration never disagree.

## Configuration files

A configuration lives in the `.aiplam` folder of its managed folder, in YAML files that aiplam writes and `aiplam
status` validates:

- `aiplam-config.yaml`, the aiplam configuration: the enabled profiles. Written by `init` and `profile enable`,
  `disable` and `switch`.

  ```yaml
  enabled:
    - default
  ```

- `<profile>.yaml`, one per profile: its name, agent, target folders and playbooks. Written by `init`, `profile add`,
  `add` and `remove`.

  ```yaml
  name: default
  agent: claude
  targets:
    skills: .claude/skills
    subagents: .claude/agents
    commands: .claude/commands
    rules: .claude/rules
  playbooks:
    - name: arxila
      folder: Workspace/arxila/arxila-ai-playbook
      mappings:
        subagents:
          folder: ai/agents
          format: "*.md"
  ```

Target folders are relative to the managed folder. Playbook folders are relative to the managed folder too
(`../playbooks/x` is fine), and each mapping's folder is relative to the playbook folder; only the mappings given with
`--folder` are stored (other kinds use the subfolder named like the kind, and the format `<name>-*`).

Editing a file by hand does not move any link. `aiplam status` then reports what `update` would change, and the links
left behind: those in the target folders of any profile that lead into a registered playbook folder but belong to no
playbook of the enabled profiles (after disabling a profile by hand, or changing a playbook's mapping), which you
remove by hand. It cannot see links left in a folder that is no longer a target folder of any profile: before
changing a target folder by hand, disable the profile (its links go), and enable it again afterwards.

The `.aiplam` folder also holds `.lock`, which keeps two commands from changing the managed folder at once, and a
`.gitignore` that keeps it out of git when the folder is committed.

## Exit codes

`0` success, `1` failure, cancelled operation or (for `status`) something needs attention, `2` invalid command line.

## License

aiplam is open-source software by [Arxila OSS](https://arxila.io), distributed under the
[Apache License 2.0](LICENSE.txt).
