# aiplam: AI Playbook Manager

An **AI playbook** is a git repository of agent artifacts (such as skills) shared by a team or by a
family of related projects. Each developer keeps a local clone of each playbook, checked out at the tag (or commit)
they want to use. `aiplam` registers those clones and links their artifacts from the **agents configuration
folders** that coding agents read (`.agents`, `.claude`, `.github`, ...), so that switching a clone to another tag and
running `aiplam update` is all an update takes. It can do so in the home folder (`~/.agents`) and in other folders
where agent sessions may start, such as the top folder of a large project with many modules: each of them is a
[base folder](#base-folders).

`aiplam` never clones, fetches, pulls or switches a playbook checkout: selecting versions is the developer's job.

## Install

`aiplam` is a single executable with no dependencies, for Linux, macOS and Windows. Put it in a folder in your
`PATH` (`aiplam.exe` on Windows). `git` is optional: without it, playbook versions are shown as `unknown`.

With Go 1.26 or later installed, `go install arxila.io/aiplam/cmd/aiplam@latest` builds it into `$(go env GOPATH)/bin`.

To build it from a clone:

```bash
make build      # ./aiplam for this machine
make dist       # dist/aiplam-<version>-<os>-<arch>[.exe] for every supported platform
```

## Playbooks

A playbook has no manifest: it is a folder with its artifacts. A playbook is registered under a **name**, which is
the prefix of its artifacts:

- Its artifacts are those named `<name>-*`, found in its **artifact folder** for each kind that the profile's
  [agent](#agents) reads:
  - **skills**: folders that contain a `SKILL.md`;
  - **subagents**, **commands**, **prompts**, **rules** and **instructions**: files in the agent's format (`*.md` for
    Claude Code, `*.agent.md` for GitHub Copilot, `*.toml` for Codex, ...). Subagents are custom agent definitions,
    which some agents (such as Copilot and OpenCode) also offer as primary agents.

  By default, each folder is named like the subfolder where the agent reads that kind: `skills/`, `agents/` (for
  subagents), `commands/`, `prompts/`, `rules/` and `instructions/` for every agent known so far. Any other folder
  inside the playbook folder can be given, relative to it, with `--skills-path` (`-sp`), `--subagents-path` (`-sap`),
  `--commands-path` (`-cp`), `--prompts-path` (`-pp`), `--rules-path` (`-rp`) and `--instructions-path` (`-ip`)
  (`.` for the folder itself). A folder given is stored and used whatever the profile's agent; defaults are not
  stored, so they follow the agent if the profile's agent changes. A playbook needs at least one artifact folder for a
  kind its profile's agent reads, and artifacts never come from outside the playbook folder, not even through links.
- The same playbook can be registered in profiles for different agents: each profile links what its agent reads.
- One repository can be registered as several playbooks with different names (for example `x` and `x-y`). When names
  overlap, the longest one wins: `x-y-foo` belongs to `x-y`, not to `x`.
- The same name can be registered from several playbook folders, as long as their last path segments differ. Their
  **ids** are then `<name>@<last folder segment>` (for example `arxila@arxila-ai-playbook`); otherwise the id is the
  name. Ids are computed, not stored. Two playbooks linked for the same agent cannot provide an artifact with the
  same name.
- A playbook's version is whatever is checked out: the tags of its current commit, or else its branch.

## Usage

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

Every command acts on a [base folder](#base-folders)'s configuration: the nearest one from the current folder up, or
the home folder's, unless given with `--base <basefolder>` or `--global` (the user's home folder, e.g.
`/home/username`). Playbooks are registered in [profiles](#profiles), each for one [agent](#agents), and only the
playbooks of the enabled profiles are linked. `update`, `remove`, `list` and `check` act on the enabled profiles, or
on the one given with `--profile PROFILE`. `add` needs `--profile` unless exactly one profile is enabled.

Options have short forms: `-b` (`--base`), `-g` (`--global`), `-p` (`--profile`), `-a` (`--agent`), and `-sp`,
`-sap`, `-cp`, `-pp`, `-rp` and `-ip` (`--skills-path`, `--subagents-path`, `--commands-path`, `--prompts-path`,
`--rules-path` and `--instructions-path`).

Without a selector, `update` acts on the playbook registered for the current folder. A selector is an id, a name
(when only one playbook has it), a playbook folder (when only one playbook is registered there), or a name and a
playbook folder.

Example:

```bash
aiplam init --global --agent claude
cd ~/Workspace/arxila/arxila-ai-playbook
git switch --detach v1.2.0
aiplam add arxila .
aiplam list
```

### Base folders

A **base folder** holds:

- a `.aiplam` folder, with aiplam's configuration for that base folder;
- the **agents configuration folders** of the enabled profiles' agents, from which artifacts are linked (skills, for
  example, from `.agents/skills` for the standard agent, or `.claude/skills` for Claude Code).

`aiplam init ~/Workspace/thymeleaf` (or `aiplam init .` from there) sets up that base folder, with a first profile for
the standard agent (or the one given with `--agent`); `aiplam init --global` sets up the home folder. Agents
configuration folders, and their subfolders for each kind of artifact, are created when an artifact is first linked
there, and removed when unlinking leaves them empty: a folder holding anything else, such as an artifact added by
hand, is never removed.

Commands use the nearest base folder from the current folder up (any folder holding a `.aiplam` folder), or else
the home folder, and show which one they use (`Base folder: ...`). The current folder is walked up as given, so
inside a linked artifact (such as `.agents/skills/x`) the base folder is the one above the link, not one in the
playbook folder.

Agents read the agents configuration folders of the folder where a session starts and of the home folder and, some
of them, of every folder in between. So an artifact cannot be linked for an agent in a base folder if it is already
linked for the same agent in a **higher-level** base folder:

- the parents of the base folder that are base folders as well: some agents read their agents configuration folders
  too;
- the home folder, if it is also being used as a base folder for playbook management: agents always read its agents
  configuration folders.

Artifacts linked for different agents do not collide: a skill, for example, can be in `~/.claude` and in a project's
`.agents`. Only higher-level base folders are checked when linking (a command at `~` cannot know about the projects
below it); `aiplam check` in a project reports an artifact linked both there and in a higher-level base folder, and
`aiplam list --all` also lists the higher-level base folders, nearest first.

Playbook folders are stored relative to the base folder, so a project's `.aiplam` folder can be committed and
shared. On Windows, a playbook must be on the same drive as the base folder. The links themselves are specific to
each machine (on Windows, links to folders are junctions, which git cannot store as links): when the base folder is
inside a git repository, `init` suggests the lines to add to `.gitignore` for the first profile's agent (such as
`/.claude/skills/<name>-*` and `/.claude/agents/<name>-*`); profiles added later for other agents need the same for
their folders.

### Agents

Each profile is for one agent, and aiplam links the artifacts of its playbooks that the agent reads, in the agent's
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
linked from `agents` (`.claude/agents`, `.github/agents`, ...); a playbook's folders are named the same way by default.
Kinds of artifacts that an agent does not read, and files in other formats, are ignored for its profiles: a
playbook's rules, for example, are not linked for the standard agent, and its `.agent.md` subagents are linked for
Copilot but not for Claude Code. `aiplam profile --help` shows the same list, from the single source file that defines
it (`internal/config/agents.go`).

How profiles for different agents interact:

- **Separate folders, no collision.** Profiles for different agents link into different folders, so they can be
  enabled together even with the same playbooks.
- **Shared folders collide.** Codex reads skills only from the standard folders, so for skills `codex` and `standard`
  share `.agents/skills`: two enabled profiles for them cannot link the same skill.
- **Agents that also read `.agents/skills`.** Copilot, Gemini CLI, Cursor, OpenCode, Devin and Junie read the
  standard `.agents/skills` besides their own folders. When an enabled profile links a skill for one of them and
  another enabled profile links the same skill from `.agents/skills`, aiplam warns that the agent may load it twice
  (`add`, `profile enable`, `switch` and `agent`, and `check`), and leaves it to you to check how that agent handles it.
- **Higher-level base folders** are checked per agent: an artifact linked for Claude Code in `~/.claude` blocks the
  same artifact for Claude Code in a project, not for the standard agent in the project's `.agents`.
- **Changing a profile's agent** (`aiplam profile agent`) moves an enabled profile's links from the old agent's
  folders to the new one's, all-or-nothing, and removes the folders left empty.

### Profiles

A **profile** is a group of registered playbooks for one agent. The **enabled** profiles (any number of them, for the
same agent or for different ones) are the ones whose playbooks are linked. `init` creates a first profile, `default`
(or the one given with `--profile`), for the standard agent (or the one given with `--agent`), and enables it.

```bash
aiplam profile add claude --agent claude                  # create an empty profile for Claude Code
aiplam add acme ~/Workspace/acme/acme-playbook -p claude  # register in it: nothing is linked yet
aiplam profile enable claude                              # link its playbooks too, from ~/.claude
aiplam profile agent claude cursor                        # link them for Cursor instead, from ~/.cursor
aiplam profile disable default                            # unlink default's playbooks
aiplam profile switch default,claude                      # enable exactly these profiles
aiplam profile list                                       # the profiles and their agents; '*' marks the enabled ones
aiplam profile remove old                                 # delete a profile that is not enabled
```

Profiles enabled together cannot collide: `profile enable` fails, changing nothing, if any link that a profile needs
already exists, whether another enabled profile made it or anything else did. `profile switch` disables the profiles
that are not listed first, so their links are not in the way, and leaves alone the links that stay the same. Within
the enabled profiles, `add` and `update` also refuse an artifact that another enabled profile links at the same place.
`profile agent` unlinks an enabled profile's playbooks for its old agent and links them for the new one,
all-or-nothing; a playbook's folders left to their defaults follow the new agent's, while those set with `--*-path`
stay as they are. A profile that is not enabled only has its file changed.

In a profile that is not enabled, `add` and `remove` only register and unregister playbooks, and `update` has nothing to
do. Enabled profiles cannot be removed (disable them first), but any other can, including `default`. Several profiles
can be given at once, separated by commas. Profile names use letters, digits, `_` and `-`; `aiplam-config` is reserved.

### Safety

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

### Configuration

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
