# aiplam: AI Playbook Manager

`aiplam` makes coding agents (Claude Code, GitHub Copilot, Cursor, Codex, Gemini CLI, ...) use the skills, subagents,
commands and rules of **AI playbooks**: git repositories of such artifacts, shared by a team or by a family of related
projects.

- Each developer keeps a local clone of each playbook, checked out at the tag (or commit) they want to use.
- `aiplam` links the artifacts of those clones into the folders that agents read them from (`.claude/skills`,
  `.github/agents`, ...).
- Agents follow the links, so they read the files straight from the clones.
- Updating a playbook is switching its clone to another tag and running `aiplam update`.

```text
   playbook folder (a git clone,                           managed folder (here, the home folder, ~)
   checked out at tag v1.2.0)
  ┌───────────────────────────────────┐                   ┌────────────────────────────────────────────┐
  │ ~/Workspace/acme-ai-playbook/     │                   │    ~/.aiplam/       aiplam's configuration │
  │ ├── skills/                       │                   │                                            │
  │ │   ├── acme-deploy/   ◄──────────┼───────────────────┼─── ~/.claude/skills/acme-deploy            │
  │ │   └── acme-review/   ◄──────────┼───────────────────┼─── ~/.claude/skills/acme-review            │
  │ └── subagents/                    │   links made by   │                                            │
  │     └── acme-reviewer.md  ◄───────┼───── aiplam ──────┼─── ~/.claude/agents/acme-reviewer.md       │
  └───────────────────────────────────┘                   └────────────────────────────────────────────┘
                                                                              ▲
                                                                              │ reads its skills and subagents
                                                                       ┌──────┴──────┐
                                                                       │ Claude Code │
                                                                       └─────────────┘
```

`aiplam` never clones, fetches, pulls or switches a playbook's checkout: choosing versions is the developer's job. It
only creates and removes links, and the folders that hold them (see [Safety](#safety)).

**Contents:**

- [Install](#install) and [Quick start](#quick-start)
- [Concepts](#concepts): artifacts, playbooks, target folders, agents, profiles, managed folders
- [How it works](#how-it-works): linking, Windows, updating, collisions, finding the managed folder, sharing
- [Agents in detail](#agents-in-detail): the formats each agent reads, and guidelines (`AGENTS.md` and the like)
- [Safety](#safety)
- [Reference](#reference): commands, configuration files, exit codes

## Install

`aiplam` is a single executable with no dependencies (`aiplam`, or `aiplam.exe` on Windows), for Linux, macOS and
Windows:

- put it in your `PATH`;
- `git` is optional: without it, playbook versions are shown as `unknown`.

With Go 1.26 or later installed, `go install arxila.io/aiplam/cmd/aiplam@latest` builds it into `$(go env GOPATH)/bin`.
To build it from a clone:

```bash
make build      # ./aiplam for this machine
make dist       # dist/aiplam-<version>-<os>-<arch>.tar.gz (.zip for Windows) per platform, and SHA256SUMS
```

## Quick start

```bash
aiplam init --user --agent claude                   # make ~ a managed folder, with a profile for Claude Code
cd ~/Workspace/acme-ai-playbook
git checkout v1.2.0                                 # choose the playbook's version
aiplam add acme .                                   # register it as 'acme', and link its acme-* artifacts
aiplam list                                         # what is linked, and from where
git checkout v1.3.0                                 # later: another version...
aiplam update                                       # ...and its new or removed artifacts are linked or unlinked
```

`aiplam add` shows what it finds and links:

```text
Managed folder: ~ (user managed folder)
Adding playbook acme (v1.2.0) from ~/Workspace/acme-ai-playbook to profile(s) default
  profile 'default':
    skills: ~/Workspace/acme-ai-playbook/skills, format 'acme-*': 2 artifact(s)
    subagents: ~/Workspace/acme-ai-playbook/subagents, format 'acme-*': 1 artifact(s)
    commands: ~/Workspace/acme-ai-playbook/commands, format 'acme-*': not found
    rules: ~/Workspace/acme-ai-playbook/rules, format 'acme-*': 1 artifact(s)
  + ~/.claude/skills/acme-deploy -> ~/Workspace/acme-ai-playbook/skills/acme-deploy
  + ~/.claude/skills/acme-review -> ~/Workspace/acme-ai-playbook/skills/acme-review
  + ~/.claude/agents/acme-reviewer.md -> ~/Workspace/acme-ai-playbook/subagents/acme-reviewer.md
  + ~/.claude/rules/acme-style.md -> ~/Workspace/acme-ai-playbook/rules/acme-style.md
Added acme.
```

## Concepts

At a glance:

- **Artifact**: a file or folder an agent reads: a skill (a folder with a `SKILL.md`), a subagent, a command, a rule...
- **Artifact kind** (or just **kind**): a type of artifact, by name: `skills`, `subagents`, `commands`, `rules`, or
  any other name.
- **Playbook**: a folder (usually a git clone) holding artifacts, usually in one subfolder per kind.
- **Target folder**: a folder where an agent reads the artifacts of a kind, such as `.claude/skills`; aiplam links
  the artifacts there.
- **Agent template**: the set of target folders of a known agent, ready to be used as a template for a profile's
  target folders (`--agent claude`).
- **Profile**: a working scope: a set of target folders and the playbooks to link into them. Only the playbooks of
  **enabled** profiles are linked.
- **Managed folder**: a folder whose target folders aiplam manages: the home folder, or a folder where agent sessions
  start (such as a project's top folder). Its `.aiplam` folder holds the profiles.

```text
 managed folder ── holds ──► profiles ── each has ──► target folders, one per kind (where to link)
                                 │
                                 └────── each has ──► playbooks (what to link): their artifacts, by kind
```

### Artifacts and kinds

- An **artifact** is an entry, a file or a folder, that an agent reads. aiplam links it as is, under its own name: a
  skill's whole folder is linked, with every file in it.
- Artifacts are grouped by **kind**. A kind is only a name, which joins two things:
  - the playbook's subfolder holding the artifacts of the kind (by default, named like the kind: `skills/`);
  - the target folder they are linked into (such as `.claude/skills`).
- Kinds are any names (`skills`, `subagents`, `my-prompts`, ...). aiplam only treats `skills` specially, to show the
  versions skills declare.

### Playbooks

A **playbook** is a folder, usually a git clone, holding agent artifacts. It has no manifest: it is given a **name**
when it is registered (`aiplam add acme <folder>`), and, for each kind, its artifacts are the files and folders
directly inside its **subfolder** for the kind whose names match the kind's **format**. A matching file is linked; a
matching folder is linked as a whole, with everything inside it:

- by default, the subfolder is named like the kind (`skills/`, `subagents/`, ...), and the format is `<name>-*`;
- `aiplam add --folder <kind>:<subfolder>[:<format>]` (`-f`) sets another subfolder (`.` for the playbook folder
  itself) and format, in which `*` matches any characters (`-f subagents:agents:*.agent.md`, `-f 'skills:.:*'`);
- hidden entries (named `.*`) are never artifacts, whatever the format;
- aiplam does not check what an entry holds: a folder without a `SKILL.md` that matches the format is linked too.

```text
acme-ai-playbook/                    registered with: aiplam add acme ~/Workspace/acme-ai-playbook
├── README.md                        not in a kind's subfolder: not an artifact
├── skills/                          kind skills: entries named acme-*
│   ├── acme-review/                 an artifact: the whole folder is linked
│   │   ├── SKILL.md
│   │   └── checklist.md
│   ├── acme-deploy/                 an artifact
│   │   └── SKILL.md
│   ├── .acme-draft/                 hidden: never an artifact
│   └── other-tool/                  not named acme-*: not an artifact of this playbook
├── subagents/                       kind subagents: entries named acme-*
│   └── acme-reviewer.md             an artifact
└── rules/                           kind rules: entries named acme-*
    └── acme-style.md                an artifact
```

- **Why a prefix.** The default format, `<name>-*`, lets several playbooks be linked side by side into the same target
  folders without clashing: `acme-*` next to `tools-*`.
- **Missing subfolders are fine.** A playbook does not need a subfolder for every kind: once a subfolder appears, what
  it holds is linked the next time the playbook is updated or its profile enabled.
- **Several formats side by side.** A playbook meant for several agents can hold the same artifact in several formats
  (`acme-reviewer.md` and `acme-reviewer.agent.md`); each registration picks its own with a format (see [Formats agents
  read](#formats-agents-read)).
- **Ids.** The same name can be registered from several playbook folders, as long as the last segments of their paths
  differ. Their **ids** are then `<name>@<last folder segment>` (`acme@acme-ai-playbook`); otherwise the id is the name.

**Versions.** A playbook's version is whatever is checked out: the tags of its current commit, or else its branch.

- Changes to existing artifacts are seen at once, as the links point into the clone.
- `aiplam update` is only needed to link the artifacts a version adds, and unlink those it removes.
- Skills can also declare their own version, in the frontmatter of their `SKILL.md`, as the Agent Skills specification
  suggests (`metadata.version: "2.1"`). `aiplam list` shows it, for information only: agents ignore it.

### Target folders

A **target folder** is a folder where aiplam links the artifacts of a kind, and where agents read them:

- it is relative to the managed folder: `.claude/skills` in the home folder is `~/.claude/skills`;
- it never leaves the managed folder: `..` is never allowed. The only exception is `%APPDATA%`, which a target folder
  of the user managed folder can start with, on Windows;
- aiplam only manages the links in it that belong to its playbooks: anything else there (settings, artifacts added by
  hand) is left alone;
- it is created when the first artifact is linked there, and removed again when unlinking leaves it empty (with its
  empty parents, such as `.claude`, up to the managed folder).

### Supported agents

Each agent reads its artifacts from a set of folders, some of its own (`.claude/skills`) and some shared with other
agents (`.agents/skills`), which can differ between a project and the home folder. **Agent templates** hold them, so
that `--agent <template>` fills in a profile's target folders:

| Template | Agent | Kinds, in a project (and in the home folder) |
|---|---|---|
| `standard` | any agent reading `.agents` | skills in `.agents/skills` |
| `claude` | Claude Code | skills, subagents, commands, rules in `.claude/...` |
| `copilot` | GitHub Copilot | skills, subagents, instructions in `.github/...` (`~/.copilot/...`); prompts |
| `cursor` | Cursor | skills, subagents, commands in `.cursor/...`; rules |
| `codex` | OpenAI Codex | skills in `.agents/skills`; subagents in `.codex/agents` |
| `gemini` | Gemini CLI | skills, subagents, commands in `.gemini/...` |
| `junie` | JetBrains Junie | skills, subagents, commands in `.junie/...`; rules |
| `opencode` | OpenCode | skills, subagents, commands in `.opencode/...` (`~/.config/opencode/...`) |
| `devin` | Devin (formerly Windsurf) | skills, subagents in `.devin/...` (`~/.config/devin/...`); rules |

- Copilot's prompts and the rules of Cursor and Junie are read in projects only, so these templates have no target
  folder for them in the home folder.
- Every template but `standard` and `claude` has a `:standard` variant (`junie:standard`, ...) that links skills into
  the cross-agent `.agents/skills` instead, which these agents read too.
- `aiplam profile --help` lists every template with its exact target folders.
- A template only fills in the target folders when a profile is created: the profile then works as if they had been
  given by hand (`--folder skills:.agents/skills,...`).

### Profiles

A **profile** is a working scope: the playbooks used together, such as those of a client, a team or a kind of work.
Each is kept in its own file, `.aiplam/<profile>.yaml`, with:

- its **target folders**: for each kind, where its artifacts are linked;
- its **agent**: the template its target folders were taken from, or `custom` when they were given by hand. aiplam
  only uses it to warn when the target folders no longer match the template;
- its registered **playbooks**, with the subfolders and formats given for them.

Only the playbooks of the **enabled** profiles are linked, so moving from one scope to another is enabling and
disabling profiles. Any number of profiles can be enabled at once, combining their scopes:

```text
 .aiplam/
 ├── aiplam-config.yaml   enabled: [default, acme]
 ├── default.yaml         agent: claude   playbooks: team-common, arxila     enabled: always needed
 ├── acme.yaml            agent: claude   playbooks: acme                    enabled: working for client Acme now
 └── initech.yaml         agent: custom   playbooks: initech, initech-data   disabled: back to it next month
```

```bash
aiplam profile switch default,initech    # next month: unlink acme's playbooks, link initech's
```

- `init` creates the first profiles (`default`, unless others are named with `--profile`) and enables them;
  `profile add` creates more, not enabled.
- No command changes a profile's target folders or agent: edit its file, or remove the profile and create it again.

### Managed folders

A **managed folder** is a folder with a `.aiplam` folder (the **aiplam configuration folder**):

- the **user managed folder** is the home folder, once it has a `.aiplam` folder: its target folders are those that
  agents read in every session, whatever the project (`~/.claude/skills`, ...);
- a project's managed folder is usually its top folder, where agent sessions start: its target folders are those that
  agents read in that project only (`<project>/.claude/skills`, ...);
- each managed folder is independent: it has its own profiles, playbooks and links.

This is a home folder after `aiplam init --user --agent claude` and `aiplam add acme ~/Workspace/acme-ai-playbook`:

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

## How it works

### Linking artifacts

For each playbook of each enabled profile, and for each kind of the profile's target folders, aiplam links every
artifact into the target folder:

```text
           profile 'default'                       playbook 'acme'
  ┌──────────────────────────────────┐   ┌──────────────────────────────────┐
  │ kind skills:                     │   │ subfolder for skills:  skills/   │
  │   target folder: .claude/skills  │   │ format for skills:     acme-*    │
  └─────────────────┬────────────────┘   └─────────────────┬────────────────┘
                    │                                      │
                    ▼                                      ▼
 <managed folder>/.claude/skills/<entry>  ── link ──►  <playbook folder>/skills/<entry>
                              (for each <entry> matching acme-*)
```

- A link has the artifact's own name: `.claude/skills/acme-deploy` leads to `skills/acme-deploy`.
- Links are relative symbolic links on Linux and macOS. On Windows, links to folders (skills) are directory junctions,
  and links to files (subagents, rules) are symbolic links, which need developer mode or administrator rights (see
  [Windows without developer mode](#windows-without-developer-mode)).
- Artifacts never come from outside the playbook folder, not even through links inside it.
- Playbooks of profiles that are not enabled are registered, but not linked.

### Windows without developer mode

On Windows, links to files need developer mode or administrator rights, which many users do not have. Without them,
aiplam links what it can and skips the rest:

- folders (skills) are always linked: directory junctions need no rights;
- files (subagents, commands, rules, instructions) are **skipped**, with a warning listing them, and everything else
  is linked: `add` still registers the playbook, and `profile enable` still enables the profile;
- `aiplam status` lists them too, but does not count them as problems, so it can still succeed;
- `aiplam list` ends by saying how many artifacts are not linked for this reason;
- links to files made earlier, when the rights were available, are still removed when needed (that needs no rights);
- once developer mode is turned on (or administrator rights are available), `aiplam status` shows which
  `aiplam update` commands link what was skipped.

```text
WARNING: 2 artifact(s) were not linked, as they are files and, on Windows, links to files need developer mode or
administrator rights; once you have either, 'aiplam status' shows what to update to link them:
  commands acme-ship.md of playbook acme (profile default) -> ~/Workspace/acme-ai-playbook/commands/acme-ship.md
  rules acme-style.md of playbook acme (profile default) -> ~/Workspace/acme-ai-playbook/rules/acme-style.md
```

Agents do not see the skipped artifacts. Collisions between playbooks over a skipped artifact are still refused (they
are in the configuration), but a file that is already where a skipped link would go is left alone, as nothing is
created there.

### Keeping links in step

aiplam does not watch anything: links change only when an aiplam command runs. Each command that changes links
computes the links that should exist afterwards, compares them with the links on disk, and creates and removes the
difference:

| Command | What changes | Links created | Links removed |
|---|---|---|---|
| `add` | a playbook is registered | its artifacts | none |
| `update` | the playbook's checkout changed | its new artifacts | those of deleted artifacts |
| `remove` | a playbook is unregistered | none | all of its links |
| `profile enable` | profiles are enabled | their playbooks' artifacts | none |
| `profile disable` | profiles are disabled | none | their playbooks' links |
| `profile switch` | exactly the given profiles are enabled | those not already linked | those no longer wanted |

```text
$ git checkout v1.3.0              # v1.3.0 adds skills/acme-audit/ and deletes skills/acme-review/
$ aiplam update acme
Managed folder: ~ (user managed folder)
Updating playbook acme of profile 'default' (v1.3.0) from ~/Workspace/acme-ai-playbook
  - ~/.claude/skills/acme-review (broken link: its artifact no longer exists)
  + ~/.claude/skills/acme-audit -> ~/Workspace/acme-ai-playbook/skills/acme-audit
```

- Links that stay the same are left alone: `profile switch` from one profile to another that registers the same
  playbook does not touch its links.
- Removing a link removes the folders this leaves empty, up to the managed folder.
- `aiplam status` shows what `update` would change, without changing anything.

### One owner per link

Every link belongs to a single playbook of a single profile, so that removing a playbook, or disabling a profile, never
removes a link that something else still needs. A command refuses to run, and changes nothing, when, afterwards:

- two playbooks would link something at the same place, in the same profile or in profiles enabled together, even if
  both would link the same artifact;
- the same playbook (same name and playbook folder) would be registered in two enabled profiles;
- something is already where a link must go: a file, a folder, or even a link to the same artifact that aiplam did not
  make for that playbook. The one exception is a link to a file that is skipped on Windows without developer mode
  (see [Windows without developer mode](#windows-without-developer-mode)): what is there is left alone, as nothing is
  created there.

```text
  profile 'default' (enabled)        playbook tools:  skills/tools-git/  ─┐
                                                                          ├─► .claude/skills/tools-git  refused
  profile 'acme' (being enabled)     playbook extras: skills/tools-git/  ─┘
```

`add`, `update`, `profile enable` and `profile switch` check this for everything that would be linked afterwards, and
report every collision at once.

### Finding the managed folder

Every command acts on one managed folder, found from the folder it starts from (the current folder, or the folder
given to `status` and `list`):

```text
          start folder
               │
               ▼
  ┌──────────────────────────┐   yes
  │ has a .aiplam folder?    ├────────►  use it
  └────────────┬─────────────┘
               │ no
               ▼
  ┌──────────────────────────┐   yes
  │ is it the home folder,   ├────────►  use the home folder, if it is managed (if not, fail)
  │ or the file system root? │
  └────────────┬─────────────┘
               │ no
               ▼
      go up to its parent folder, and check again
```

- `--user` (`-u`) skips the search: the command uses the user managed folder, from anywhere.
- Folders above the home folder are never used from inside it.
- From a folder outside the home folder, the search goes up to the root of the file system, and then falls back to the
  home folder.
- The start folder is walked up as given, without resolving links: inside a linked artifact (such as
  `.claude/skills/acme-deploy`), the managed folder is the one above the link, not one in the playbook folder.
- Commands that change something start by naming the managed folder they act on (`Managed folder: ...`); `status` and
  `list` also say whether it is the user managed folder, and whether it is in a git repository.

```text
~/                                         user managed folder
├── .aiplam/
└── Workspace/
    ├── big-project/                       a project's managed folder: agent sessions start here
    │   ├── .aiplam/
    │   ├── .claude/skills/...
    │   └── module-a/                      commands run here use big-project's configuration
    └── small-project/                     not managed: commands run here use ~'s configuration
```

aiplam does not check what other managed folders link, nor which of them an agent reads: an agent started in
`big-project` usually reads both `big-project/.claude/skills` and `~/.claude/skills`.

### Sharing a project's configuration

- Playbook folders are stored relative to the managed folder, so a project's `.aiplam` folder can be committed and
  shared, as long as everyone keeps the playbooks at the same place relative to the project (on Windows, a playbook
  must be on the same drive as the managed folder).
- The links themselves are specific to each machine (and on Windows, links to folders are junctions, which git cannot
  store as links). When the managed folder is in a git repository, `init` suggests lines for `.gitignore` that leave
  them out (such as `/.claude/skills/<name>-*`).
- After cloning a project with a shared configuration, `aiplam status` shows what is not linked yet, naming the
  `aiplam update` command that links it.

### Editing the configuration by hand

The configuration files are plain YAML (see [Configuration files](#configuration-files)), and can be edited. Editing a
file does not move any link, so afterwards:

- `aiplam status` reports what `update` would change, naming the `aiplam update` command to run;
- it reports the links left behind: those in the target folders of any profile that are broken, or lead into a
  registered playbook folder, but belong to no playbook of the enabled profiles (after disabling a profile by hand, or
  changing a mapping). Remove them by hand: they get in the way of the playbook's own links, even if you made them
  yourself;
- it warns when a profile's target folders no longer match its agent template: set its agent to `custom` if the change
  is intended;
- it cannot see links left in a folder that is no longer a target folder of any profile: before changing a target
  folder by hand, disable the profile (its links go), and enable it again afterwards.

A profile whose file is not valid cannot be enabled, but it can still be disabled and removed: its links, if any, are
then left in place, for you to remove.

## Agents in detail

### Formats agents read

Templates only give target folders: which entries of a playbook are linked is decided by the formats it is registered
with. When a playbook holds artifacts for several agents side by side, or files an agent would misread, give each kind
a format matching the files the agent reads:

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

- Skills are folders holding a `SKILL.md`, for every agent.
- Formats are plain patterns: `*.md` also matches `acme-reviewer.agent.md`. A playbook that holds both should keep them
  in different subfolders, or name its files so that one format cannot match the other's (`acme-*.claude.md` and
  `acme-*.agent.md`).

```text
acme-ai-playbook/subagents/
├── acme-reviewer.md             Claude Code, Cursor, Gemini CLI, Junie, OpenCode, Devin
├── acme-reviewer.agent.md       GitHub Copilot
└── acme-reviewer.toml           OpenAI Codex
```

For example, for Copilot:

```bash
aiplam add acme ~/Workspace/acme-ai-playbook -f 'subagents:subagents:acme-*.agent.md' \
    -f 'instructions:instructions:acme-*.instructions.md'
```

### Guidelines: `AGENTS.md`, `CLAUDE.md` and the like

Agents also read a main instructions file: `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`...
aiplam does not link or edit those files:

- each has a fixed name, so there can be only one per folder: several playbooks could not each provide one;
- it would collide with the one the user (or the project) already has.

Instead, a playbook provides its guidelines as **rules** or **instructions**: files named after the playbook like any
other artifact (`acme-guidelines.md`), which agents load besides the main instructions file:

```text
 .claude/
 ├── CLAUDE.md                     the user's own: aiplam never touches it
 └── rules/                        Claude Code reads the files here, besides CLAUDE.md
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

- Codex, Gemini CLI and OpenCode read guidelines only from their main instructions file, so aiplam cannot link
  guidelines for them, nor for profiles made with the `standard` template, which has no rules folder.
- Nor can it in the home folder for Cursor and Junie, which read such files in projects only.
- A later version may manage a marked block of its own inside the main instructions files, leaving the rest of them
  untouched.

## Safety

- **Only links.** aiplam only creates and removes links. It never deletes a real file, nor a folder that holds
  anything: it only removes the folders its unlinking leaves empty, up to the managed folder (or `%APPDATA%`). It never
  touches a playbook's content.
- **Only its own links.** It only removes links that belong to a registered playbook: they lead into its subfolder for
  the kind, and their names match its format.
- **All or nothing.** Commands that change links check everything first: if anything collides (see [One owner per
  link](#one-owner-per-link)), nothing is changed and every problem is reported. If creating or removing a link fails
  midway, the changes already made are undone. The one exception is decided before anything changes: links to files
  that Windows does not allow (see [Windows without developer mode](#windows-without-developer-mode)) are skipped.
- **Safe to repeat.** Every command can be run again. `status` and `list` change nothing.
- **One at a time.** Commands that change a managed folder run one at a time: while one runs, others on the same
  managed folder fail (`status` and `list` still work). Interrupting one (Ctrl+C) takes effect once it has finished its
  changes, which takes milliseconds, so that links and configuration never disagree.

## Reference

### Commands

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

Run `aiplam <command> --help` for the full details of each command.

**Common options:**

- `-u`, `--user`: use the user managed folder, wherever the command is run.
- `-p`, `--profile P,...`: for `add`, `update` and `remove`, the profiles to act on, enabled or not. It can be left out
  when exactly one profile is enabled. For `init`, the names of the first profiles.
- `-h`, `--help`: the command's help.
- Options that take lists accept repeated options or comma-separated values; quote values holding commas or colons
  (`--folder 'skills:"my, folder"'`). `--` ends the options.

**`aiplam init <folder> | --user`** makes a folder a managed folder:

- creates its `.aiplam` folder, and its first profiles (`default`, or those given with `--profile`), all with the same
  target folders, enabled;
- `-a`, `--agent A`: take the target folders from an agent template (`claude`, `junie:standard`, ...);
- `-f`, `--folder K:F,...`: or give them by hand, as `<kind>:<target folder>` (`skills:.agents/skills`);
- when the folder is in a git repository, it suggests `.gitignore` lines for the links.

**`aiplam status [<folder> | --user]`** shows the managed folder and its enabled profiles, and reports everything
that needs attention, exiting with 1 if there is anything:

- problems in the configuration files, and missing playbook folders;
- the links `update` would create or remove, or why it would refuse to;
- links left behind (see [Editing the configuration by hand](#editing-the-configuration-by-hand));
- target folders, or playbook subfolders, that are files instead of folders;
- without counting them, the artifacts that cannot be linked on Windows without developer mode.

**`aiplam list [<folder> | --user]`** lists the links of the enabled profiles, by profile and kind, with the playbook
and version of each, the version a skill declares, and broken links:

```text
Profile default (agent claude)
  skills (~/.claude/skills):
    acme-deploy  playbook acme (v1.2.0), skill version 2.1
    acme-review  playbook acme (v1.2.0)
  subagents (~/.claude/agents):
    acme-reviewer.md  playbook acme (v1.2.0)
```

**`aiplam add <name> <playbookfolder>`** registers a playbook in profiles and, in those that are enabled, links its
artifacts:

- `-f`, `--folder K:S[:F],...`: the subfolder `S` and format `F` of kind `K` (defaults: `K` and `<name>-*`). `S` is
  `.` for the playbook folder itself; `K` must be a kind of every profile given;
- `-p`, `--profile P,...`: the profiles to register it in.

**`aiplam update [<selector>]`** links a playbook's new artifacts and unlinks those that no longer exist, in each
profile given (or the only enabled one). Without a selector, it updates the playbook registered for the current folder.

**`aiplam remove <selector>`** removes a playbook's links (in enabled profiles) and unregisters it. Only links are
removed: the playbook folder is left in place.

A **selector** designates a playbook in a profile, as:

- an id (`acme@acme-ai-playbook`);
- a name, when only one playbook has it;
- a playbook folder, when only one playbook is registered there;
- a name and a playbook folder.

**`aiplam profile <subcommand>`** manages profiles:

| Subcommand | What it does |
|---|---|
| `list` | lists the profiles and their agents; `*` marks the enabled ones |
| `show <profile>` | shows its agent, target folders and playbooks (versions, subfolders, formats, artifacts) |
| `add <profile>` | creates a profile, not enabled, with `--agent` or `--folder` as in `init` |
| `remove <profile>` | deletes it, with its registrations, removing its links first if it is enabled |
| `enable <profile>,...` | enables profiles besides those already enabled, linking their playbooks |
| `disable <profile>,...` | disables profiles, unlinking their playbooks |
| `switch <profile>,...` | enables exactly the given profiles: disables the others and enables these |

Profile names use letters, digits, `_` and `-`, starting with a letter or digit; `aiplam-config` is reserved. A
profile's file repeats its name exactly (`name: acme` in `acme.yaml`).

### Configuration files

A managed folder's configuration lives in its `.aiplam` folder, in YAML files that aiplam writes and `aiplam status`
validates:

- `aiplam-config.yaml`, the **aiplam configuration**: the enabled profiles. Written by `init` and `profile enable`,
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

- `.lock`, which keeps two commands from changing the managed folder at once, and a `.gitignore` that keeps `.lock`
  (and the temporary files of interrupted saves) out of git when the `.aiplam` folder is committed.

Paths in the profile files:

- target folders are relative to the managed folder;
- playbook folders are relative to the managed folder too (`../playbooks/x` is fine);
- each mapping's folder is relative to the playbook folder. Only the mappings given with `--folder` are stored: other
  kinds use the subfolder named like the kind, and the format `<name>-*`.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | failure, cancelled operation or (for `status`) something needs attention |
| `2` | invalid command line |

## License

aiplam is open-source software by [Arxila OSS](https://arxila.io), distributed under the
[Apache License 2.0](LICENSE.txt).
