# aiplam: AI Playbook Manager

`aiplam` makes coding agents (Claude Code, GitHub Copilot, Codex, Cursor, Gemini CLI, ...) use the skills, subagents,
commands and other artifacts of **AI playbooks**: git repositories of such artifacts shared by a team or by a family
of related projects.

Each developer keeps a local clone of each playbook, checked out at the tag (or commit) they want to use. `aiplam`
registers those clones and links their artifacts from the **agents configuration folders** that coding agents read
(`.agents`, `.claude`, `.github`, ...), in the home folder or in any folder where agent sessions may start. Agents
follow the links and read the files straight from the clones, so switching a clone to another tag and running `aiplam
update` is all an update takes.

```text
 ┌─────────────────────────┐   aiplam add   ┌─────────────────────────┐     links      ┌─────────────────────────┐
 │ playbook folder         │ ─────────────► │ .aiplam/<profile>.yaml  │ ─────────────► │ agents configuration    │
 │ (a git clone, checked   │  registers it  │ the profile's agent     │ (enabled only) │ folders: .claude/skills,│
 │ out at a tag)           │                │ and its playbooks       │                │ .github/agents, ...     │
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
make dist       # dist/aiplam-<version>-<os>-<arch>[.exe] for every supported platform
```

## Quick start

```bash
aiplam init --global --agent claude                 # the home folder as base folder, with a profile for Claude Code
cd ~/Workspace/acme-ai-playbook
git switch --detach v1.2.0                          # choose the playbook's version
aiplam add acme .                                   # register it as 'acme', and link its acme-* artifacts
aiplam list                                         # what is linked, and from where
git switch --detach v1.3.0                          # later: another version...
aiplam update                                       # ...and its new or removed artifacts are linked or unlinked
```

## Concepts

### Playbooks

A **playbook** is a folder (usually a git clone) holding agent artifacts. It has no manifest: when it is registered,
it is given a **name**, and its **artifacts** are the items named `<name>-*` in its **artifact folder** for each kind:

- **skills**: folders that contain a `SKILL.md` (the whole folder is linked, with any other files in it);
- **subagents**: files with custom agent definitions, which some agents (such as Copilot and OpenCode) also offer as
  primary agents;
- **commands**, **prompts**, **rules** and **instructions**: files.

Each agent reads only some of these kinds, and each kind in its own file format. A playbook meant for several agents
can therefore hold the same artifact in several formats, side by side:

```text
acme-ai-playbook/                    registered with: aiplam add acme ~/Workspace/acme-ai-playbook
├── README.md                        not an artifact
├── skills/                          skills: folders named acme-* holding a SKILL.md (every agent)
│   ├── acme-review/
│   │   ├── SKILL.md
│   │   └── checklist.md             part of the skill: the whole folder is linked
│   ├── acme-deploy/
│   │   └── SKILL.md
│   ├── acme-notes/                  no SKILL.md: not a skill
│   └── other-tool/                  not named acme-*: not an artifact of this playbook
├── agents/                          subagents: files named acme-*, in each agent's format
│   ├── acme-reviewer.md             Claude Code, Cursor, Gemini CLI, OpenCode, Devin, Junie
│   ├── acme-reviewer.agent.md       GitHub Copilot
│   └── acme-reviewer.toml           OpenAI Codex
├── commands/
│   ├── acme-ship.md                 Claude Code, Cursor, OpenCode, Junie
│   └── acme-ship.toml               Gemini CLI
├── prompts/
│   └── acme-ask.prompt.md           GitHub Copilot (in projects)
├── rules/
│   ├── acme-style.md                Claude Code, Devin; Junie (in projects)
│   └── acme-style.mdc               Cursor (in projects)
└── instructions/
    └── acme-java.instructions.md    GitHub Copilot (in projects)
```

The name works as a prefix, so that:

- several playbooks can be linked side by side in the same agents configuration folders without clashing;
- one repository can be registered as several playbooks with different names (for example `x` and `x-y`). When names
  overlap, the longest one wins: `x-y-foo` belongs to `x-y`, not to `x`;
- the same name can be registered from several playbook folders, as long as their last path segments differ. Their
  **ids** are then `<name>@<last folder segment>` (for example `acme@acme-ai-playbook`); otherwise the id is the name.
  Ids are computed, not stored.

**Artifact folders.** By default, each artifact folder is named like the subfolder where the profile's agent reads
that kind: `skills/`, `agents/` (for subagents), `commands/`, `prompts/`, `rules/` and `instructions/`, for every
agent known so far. Any other folder inside the playbook folder can be given, relative to it, with `--skills-path`
(`-sp`), `--subagents-path` (`-sap`), `--commands-path` (`-cp`), `--prompts-path` (`-pp`), `--rules-path` (`-rp`) and
`--instructions-path` (`-ip`) (`.` for the playbook folder itself). A folder given is stored, and used whatever the
profile's agent; defaults are not stored, so they follow the agent if the profile's agent changes. A playbook needs at
least one artifact folder for a kind its profile's agent reads, and artifacts never come from outside the playbook
folder, not even through links.

**Versions.** A playbook's version is whatever is checked out: the tags of its current commit, or else its branch.
Since the links point into the clone, changes to existing artifacts are seen at once; `aiplam update` is only needed
to link the artifacts a version adds and unlink those it removes.

### Base folders and the `.aiplam` folder

A **base folder** is a folder whose agents configuration folders aiplam manages: the home folder, or a folder where
agent sessions may start, such as a project's top folder. It holds:

- a **`.aiplam` folder**, with aiplam's configuration for that base folder: which profiles exist, which agent each one
  is for, which playbooks each one has, and which profiles are enabled. Agents never read it;
- the **agents configuration folders** of the enabled profiles' agents (`.claude`, `.github`, `.agents`, ...), which
  agents read. aiplam only manages the links in them: anything else there (settings, artifacts added by hand) is left
  alone.

This is a home folder set up with `aiplam init --global --agent claude`, after `aiplam add acme
~/Workspace/acme-ai-playbook`:

```text
~/                                         the base folder
├── .aiplam/                               aiplam's configuration (agents never read it)
│   ├── aiplam-config.yaml                 enabled: [default]
│   └── default.yaml                       agent: claude; playbooks: acme (folder: Workspace/acme-ai-playbook)
├── .claude/                               Claude Code's agents configuration folder (Claude Code reads it)
│   ├── settings.json                      not aiplam's: left alone
│   ├── skills/
│   │   ├── acme-deploy   ─── link ───►    ~/Workspace/acme-ai-playbook/skills/acme-deploy/
│   │   ├── acme-review   ─── link ───►    ~/Workspace/acme-ai-playbook/skills/acme-review/
│   │   └── my-own-skill/                  added by hand: left alone
│   ├── agents/
│   │   └── acme-reviewer.md ── link ──►   ~/Workspace/acme-ai-playbook/agents/acme-reviewer.md
│   ├── commands/
│   │   └── acme-ship.md  ─── link ───►    ~/Workspace/acme-ai-playbook/commands/acme-ship.md
│   └── rules/
│       └── acme-style.md ─── link ───►    ~/Workspace/acme-ai-playbook/rules/acme-style.md
└── Workspace/
    └── acme-ai-playbook/                  the playbook folder (a git clone)
```

The `.aiplam` folder says *what* must be linked; the agents table built into aiplam (see [Agents](#agents)) says
*where* and *in which format* for each agent. Every command compares both with what is actually in the agents
configuration folders:

```text
 aiplam-config.yaml ──► the enabled profiles
                                │
                                ▼
 <profile>.yaml ──────► the profile's agent ──────► agents table (built into aiplam): the kinds the agent
                                │                   reads, their formats, and its folders (in a project,
                                │                   or in the home folder)
                                ▼                                        │
                        the profile's playbooks                          │
                                │                                        │
                                ▼                                        ▼
                        their artifacts named ─────────────────► links in the agent's folders:
                        <name>-*, in the agent's                 .claude/skills/acme-review,
                        formats                                  .claude/agents/acme-reviewer.md, ...
```

`aiplam init <basefolder>` (or `aiplam init --global`, for the home folder) creates the `.aiplam` folder and a first
profile. The agents configuration folders, and their subfolders for each kind (`.claude/skills`, ...), are created
when an artifact is first linked there, and removed when unlinking leaves them empty: a folder holding anything else,
such as `settings.json` or an artifact added by hand, is never removed.

**Which base folder a command uses.** Commands use the nearest base folder from the current folder up (any folder
holding a `.aiplam` folder), or else the home folder, and show which one they use (`Base folder: ...`); `--base
<basefolder>` and `--global` choose one explicitly. The current folder is walked up as given, so inside a linked
artifact (such as `.agents/skills/x`) the base folder is the one above the link, not one in the playbook folder.

**Higher-level base folders.** Agents read the agents configuration folders of the folder where a session starts and
of the home folder and, some of them, of every folder in between. So an artifact cannot be linked for an agent in a
base folder if it is already linked for the same agent in a **higher-level** base folder: one of its parents that is
a base folder as well (some agents read their agents configuration folders too), or the home folder, if it is also
being used as a base folder for playbook management (agents always read its agents configuration folders).

```text
~/                                         home folder: base folder too (agents always read ~/.claude, ~/.agents, ...)
├── .aiplam/
├── .claude/skills/acme-review             linked for Claude Code
└── Workspace/
    └── big-project/                       project base folder: sessions start here or below
        ├── .aiplam/
        ├── .claude/skills/acme-review     ✗ refused: Claude Code would see it twice (here and in ~/.claude)
        ├── .agents/skills/acme-review     ✓ fine: linked for the standard agent, a different agent
        └── module-a/                      commands run here use big-project's configuration
```

Only higher-level base folders are checked when linking (a command at `~` cannot know about the projects below it):
`aiplam check` in a project reports an artifact linked both there and in a higher-level base folder, and `aiplam list
--all` also lists the higher-level base folders, nearest first.

**Sharing a project's configuration.** Playbook folders are stored relative to the base folder, so a project's
`.aiplam` folder can be committed and shared (on Windows, a playbook must be on the same drive as the base folder).
The links themselves are specific to each machine (and on Windows, links to folders are junctions, which git cannot
store as links): when the base folder is inside a git repository, `init` suggests the lines to add to `.gitignore`
for the first profile's agent (such as `/.claude/skills/<name>-*` and `/.claude/agents/<name>-*`); profiles added
later for other agents need the same for their folders.

### Agents

Each profile is for one agent. aiplam links the artifacts of its playbooks that the agent reads, in the agent's
format, from the agent's own folders, which can differ between a project and the home folder:

- `standard`, the cross-agent `.agents` convention: `.agents` (`~/.agents` at home); skills.
- `claude`, Claude Code: `.claude` (`~/.claude`); skills, subagents (`.md`), commands (`.md`) and rules (`.md`).
- `codex`, OpenAI Codex: `.agents` for skills and `.codex` for subagents (`~/.agents`, `~/.codex`); skills and subagents
  (`.toml`).
- `copilot`, GitHub Copilot: `.github` (`~/.copilot`); skills, subagents (`.agent.md`), and in projects only prompts
  (`.prompt.md`) and instructions (`.instructions.md`).
- `gemini`, Gemini CLI: `.gemini` (`~/.gemini`); skills, subagents (`.md`) and commands (`.toml`).
- `cursor`, Cursor: `.cursor` (`~/.cursor`); skills, subagents (`.md`), commands (`.md`), and in projects only rules
  (`.mdc`).
- `opencode`, OpenCode: `.opencode` (`~/.config/opencode`); skills, subagents (`.md`) and commands (`.md`).
- `devin`, Devin (formerly Windsurf): `.devin` (`~/.config/devin`, and `~/.devin` for rules); skills, subagents (`.md`)
  and rules (`.md`).
- `junie`, JetBrains Junie: `.junie` (`~/.junie`); skills, subagents (`.md`), commands (`.md`), and in projects only
  rules (`.md`).

Each kind is linked from the subfolder of the same name (`.claude/skills`, `.github/prompts`, ...), except subagents,
linked from `agents` (`.claude/agents`, `.github/agents`, ...). Kinds of artifacts that an agent does not read, and
files in other formats, are ignored for its profiles: a playbook's rules, for example, are not linked for the standard
agent, and its `.agent.md` subagents are linked for Copilot but not for Claude Code. `aiplam profile --help` shows the
same list, from the single source file that defines it (`internal/config/agents.go`).

### Profiles

A **profile** is a group of registered playbooks for one agent, kept in its own file (`.aiplam/<profile>.yaml`). The
**enabled** profiles (any number of them, for the same agent or for different ones) are the ones whose playbooks are
linked. `init` creates a first profile, `default` (or the one given with `--profile`), for the standard agent (or the
one given with `--agent`), and enables it.

The same playbook can be registered in profiles for different agents, each linking what its agent reads:

```text
                             ┌─► profile default (agent standard) ──► .agents/skills/acme-review
                             │
 playbook acme ──────────────┤
 (~/Workspace/acme-...)      │                                  ┌──► .claude/skills/acme-review
                             └─► profile claude (agent claude) ─┼──► .claude/agents/acme-reviewer.md
                                                                ├──► .claude/commands/acme-ship.md
                                                                └──► .claude/rules/acme-style.md
```

How enabled profiles interact:

- **Separate folders, no collision.** Profiles for different agents link into different folders, so they can be
  enabled together, even with the same playbooks.
- **Shared folders collide.** Profiles enabled together cannot need the same link: `profile enable` fails, changing
  nothing, if any link that a profile needs already exists, whether another enabled profile made it or anything else
  did, and `add` and `update` refuse an artifact that another enabled profile links at the same place. Codex reads
  skills only from the standard folders, so `codex` and `standard` share `.agents/skills`.
- **Agents that also read `.agents/skills`.** Copilot, Gemini CLI, Cursor, OpenCode, Devin and Junie read the standard
  `.agents/skills` besides their own folders. When an enabled profile links a skill for one of them and another
  enabled profile links the same skill from `.agents/skills`, aiplam warns that the agent may load it twice (`add`,
  `profile enable`, `switch` and `agent`, and `check`), and leaves it to you to check how that agent handles it:

  ```text
  profile default (agent standard) ──► .agents/skills/acme-review ◄──┐
                                                                     ├── Copilot reads both: may be loaded twice
  profile work (agent copilot) ──────► .github/skills/acme-review ◄──┘
  ```

```bash
aiplam profile add claude --agent claude                  # create an empty profile for Claude Code
aiplam add acme ~/Workspace/acme-ai-playbook -p claude    # register in it: nothing is linked yet
aiplam profile enable claude                              # link its playbooks too, from ~/.claude
aiplam profile agent claude cursor                        # link them for Cursor instead, from ~/.cursor
aiplam profile disable default                            # unlink default's playbooks
aiplam profile switch default,claude                      # enable exactly these profiles
aiplam profile list                                       # the profiles and their agents; '*' marks the enabled ones
aiplam profile remove old                                 # delete a profile that is not enabled
```

`profile switch` disables the profiles that are not listed first, so their links are not in the way, and leaves alone
the links that stay the same. `profile agent` unlinks an enabled profile's playbooks for its old agent and links them
for the new one, all-or-nothing, removing the folders left empty; a playbook's artifact folders left to their defaults
follow the new agent's, while those set with `--*-path` stay as they are. A profile that is not enabled only has its
file changed.

In a profile that is not enabled, `add` and `remove` only register and unregister playbooks, and `update` has nothing to
do. Enabled profiles cannot be removed (disable them first), but any other can, including `default`. Several profiles
can be given at once, separated by commas. Profile names use letters, digits, `_` and `-`; `aiplam-config` is reserved.

## Commands

```bash
aiplam init <basefolder>|--global [--agent A] [--profile P]      # set up a base folder, and a first profile, enabled
aiplam add <name> <playbookfolder> [--skills-path P ...]         # register a playbook and link its artifacts
aiplam update [<name|id|playbookfolder>] [<playbookfolder>]      # link new artifacts, unlink deleted ones
aiplam update --all
aiplam remove <name|id|playbookfolder> [<playbookfolder>]        # remove a playbook's links and unregister it
aiplam list [--all] [--json]                                     # profiles, playbooks, versions and artifacts
aiplam check                                                     # what update would change; config and broken links
aiplam profile list|remove [<profile>]                           # manage profiles (groups of playbooks for an agent)
aiplam profile add <profile> [--agent A]                         # create a profile for an agent
aiplam profile enable|disable|switch <profile>[,<profile>...]    # choose the profiles linked
aiplam profile agent <profile> <agent>                           # change a profile's agent
```

Every command acts on a base folder's configuration: the nearest one from the current folder up, or the home folder's,
unless given with `--base <basefolder>` or `--global` (the user's home folder, e.g. `/home/username`). `update`,
`remove`, `list` and `check` act on the enabled profiles, or on the one given with `--profile PROFILE`. `add` needs
`--profile` unless exactly one profile is enabled.

Options have short forms: `-b` (`--base`), `-g` (`--global`), `-p` (`--profile`), `-a` (`--agent`), and `-sp`,
`-sap`, `-cp`, `-pp`, `-rp` and `-ip` (`--skills-path`, `--subagents-path`, `--commands-path`, `--prompts-path`,
`--rules-path` and `--instructions-path`).

Without a selector, `update` acts on the playbook registered for the current folder. A selector is an id, a name
(when only one playbook has it), a playbook folder (when only one playbook is registered there), or a name and a
playbook folder.

Run `aiplam <command> --help` for the details of each command.

## Safety

- `aiplam` only creates and removes **links**: relative symbolic links on Linux and macOS. On Windows, links to
  folders (skills) are directory junctions, which need no administrator rights or developer mode, while links to
  files (subagents, commands, prompts, rules, instructions) are symbolic links, which need developer mode or
  administrator rights. It never deletes a real file, nor a folder that
  holds anything (it only removes the folders its unlinking leaves empty), and never touches a playbook's content.
- Commands that change links are all-or-nothing: if something other than the right link is already where a link
  must go (an old copy of a skill, a link elsewhere, an artifact another playbook provides), nothing is changed and the
  problem is reported. If creating or removing a link fails midway, the changes already made are undone.
- Only links that point into a playbook's artifact folders (such as its skills folder) and are named after it are
  ever removed.
- Every command can be run again safely. `check` changes nothing, and exits with 1 when anything needs attention.

## Configuration files

A configuration lives in the `.aiplam` folder of its base folder, in YAML files that aiplam writes and `aiplam check`
validates:

- `aiplam-config.yaml`, the base folder's configuration: the enabled profiles. Written by `init` and `profile enable`,
  `disable` and `switch`.

  ```yaml
  enabled:
    - default
  ```

- `<profile>.yaml`, one per profile: its agent and its playbooks. Written by `init`, `profile add`, `profile agent`,
  `add` and `remove`.

  ```yaml
  agent: claude
  playbooks:
    - name: arxila
      folder: Workspace/arxila/arxila-ai-playbook
      artifacts:
        subagents:
          path: ai/subagents
  ```

Playbook folders are relative to the base folder (`../playbooks/x` is fine), and each kind's path is relative to the
playbook folder (only paths given are stored: otherwise the default of the profile's agent applies). Prefer the
commands to editing the files by hand: a hand edit does not move any link.

## Exit codes

`0` success, `1` failure, cancelled operation or (for `check`) something needs attention, `2` invalid command line.

## License

aiplam is open-source software by [Arxila OSS](https://arxila.io), distributed under the
[Apache License 2.0](LICENSE.txt).
