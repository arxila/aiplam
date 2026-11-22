# aiplam: AI Playbook Manager

An **AI playbook** is a git repository of agent artifacts (such as skills) shared by a team or by a
family of related projects. Each developer keeps a local clone of each playbook, checked out at the tag (or commit)
they want to use. `aiplam` registers those clones and links their artifacts from the **agents configuration
folders** that coding agents read (`.agents`, `.claude`), so that switching a clone to another tag and running
`aiplam update` is all an update takes. It can do so in the home folder (`~/.agents`) and in any other folder where
agent sessions are started, such as the top folder of a large project with many modules.

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

- Its artifacts are those named `<name>-*`, found in a folder of the playbook for each kind. Its **skills**, for
  example, are the folders named `<name>-*` that contain a `SKILL.md`, inside its skills folder: `skills/` by
  default, or any other folder inside the playbook's, given relative to it (`--skills-path` or `-sp`, `.` for the
  folder itself). Artifacts never come from outside the playbook's folder, not even through links.
- One repository can be registered as several playbooks with different names (for example `x` and `x-y`). When names
  overlap, the longest one wins: `x-y-foo` belongs to `x-y`, not to `x`.
- The same name can be registered from several folders, as long as their last path segments differ. Their **ids**
  are then `<name>@<last folder segment>` (for example `arxila@arxila-ai-playbook`); otherwise the id is the name.
  Ids are computed, not stored. Two playbooks cannot provide an artifact with the same name.
- A playbook's version is whatever is checked out: the tags of its current commit, or else its branch.

## Usage

```bash
aiplam init <basefolder>|--global [--type T] [--set SET]    # set up a base folder, and a first set, enabled
aiplam add <name> <playbookfolder> [--skills-path P]        # register a playbook and link its artifacts
aiplam update [<name|id|playbookfolder>] [<playbookfolder>] # link new artifacts, unlink deleted ones
aiplam update --all
aiplam remove <name|id|playbookfolder> [<playbookfolder>]   # remove a playbook's links and unregister it
aiplam list [--all] [--json]                                # playbooks, versions and artifacts
aiplam check                                                # what update would change; config and broken links
aiplam set list|add|remove [<set>]                          # manage sets of playbooks
aiplam set enable|disable|switch <set>[,<set>...]           # choose the sets linked
aiplam type [agents|claude...]                              # types of agents configuration folders
```

Every command acts on a [base folder](#base-folders)'s configuration: the nearest one from the current folder up, or
the home folder's, unless given with `--base <basefolder>` or `--global` (the user's home folder, e.g.
`/home/username`). Playbooks are registered in [sets](#sets), and only the playbooks of the enabled sets are linked.
`update`, `remove`, `list` and `check` act on the enabled sets, or on the one given with `--set SET`. `add` needs
`--set` unless exactly one set is enabled.

Options have short forms: `-b` (`--base`), `-g` (`--global`), `-s` (`--set`), `-t` (`--type`) and `-sp`
(`--skills-path`).

Without a selector, `update` acts on the playbook registered for the current folder. A selector is an id, a name
(when only one playbook has it), a playbook folder (when only one playbook is registered there), or a name and a
playbook folder.

Example:

```bash
aiplam init --global --type agents,claude
cd ~/Workspace/arxila/arxila-ai-playbook
git switch --detach v1.2.0
aiplam add arxila .
aiplam list
```

### Base folders

A **base folder** holds:

- a `.aiplam` folder, with aiplam's configuration for that base folder;
- the **agents configuration folders** of the chosen types, from which artifacts are linked (skills, for example,
  from their `skills/` subfolder): `.agents` for type `agents` and `.claude` for type `claude`.

`aiplam init ~/Workspace/thymeleaf` (or `aiplam init .` from there) sets up that base folder with `.agents`;
`aiplam init --global` sets up the home folder. `--type agents,claude` chooses the agents configuration folders, and
`aiplam type` changes them later: the enabled sets' playbooks are unlinked from the agents configuration folders of
the types removed and linked from those of the types added, all-or-nothing.

Commands use the nearest base folder from the current folder up (any folder holding a `.aiplam` folder), or else
the home folder, and show which one they use (`Base folder: ...`). The current folder is walked up as given, so
inside a linked artifact (such as `.agents/skills/x`) the base folder is the one above the link, not the playbook's.

Agents read the agents configuration folders of the folder where a session starts and of the home folder and, some
of them, of every folder in between. So an artifact cannot be linked from an agents configuration folder of a base
folder if it is already linked from one of the same type (`.agents`, `.claude`) of a **higher-level** base folder:

- the parents of the base folder that are base folders as well: some agents read their agents configuration folders
  too;
- the home folder, if it is also being used as a base folder for playbook management: agents always read its agents
  configuration folders.

Agents configuration folders of different types do not collide: a skill, for example, can be in `~/.claude` and in a
project's `.agents`. Only higher levels are checked when linking (a command at `~` cannot know about the projects
below it); `aiplam check` in a project reports an artifact linked both there and at a higher level, and
`aiplam list --all` also lists the higher-level base folders, nearest first.

Playbook folders are stored relative to the base folder, so a project's `.aiplam` folder can be committed and
shared. On Windows, a playbook must be on the same drive as the base folder. The links themselves are specific to
each machine (on Windows they are junctions, which git cannot store as links): when the base folder is inside a git
repository, `init` suggests adding them to `.gitignore`.

### Sets

A **set** is a group of registered playbooks. The **enabled** sets (any number of them) are the ones whose playbooks
are linked from the agents configuration folders. `init` creates a first set, `default` (or the one given with
`--set`), and enables it.

```bash
aiplam set add acme                                       # create an empty set
aiplam add acme ~/Workspace/acme/acme-ai-playbook -s acme # register in it: nothing is linked yet
aiplam set enable acme                                    # link its playbooks too
aiplam set disable default                                # unlink default's playbooks
aiplam set switch default,acme                            # enable exactly these sets
aiplam set list                                           # the sets; '*' marks the enabled ones
aiplam set remove old                                     # delete a set that is not enabled
```

Sets enabled together cannot collide: `set enable` fails, changing nothing, if any link that a set needs already
exists in an agents configuration folder, whether another enabled set made it or anything else did. `set switch`
disables the sets that are not listed first, so their links are not in the way, and leaves alone the links that stay
the same. Within the enabled sets, `add` and `update` also refuse an artifact that another enabled set provides.

In a set that is not enabled, `add` and `remove` only register and unregister playbooks, and `update` has nothing to
do. Enabled sets cannot be removed (disable them first), but any other can, including `default`. Several sets can be
given at once, separated by commas. Set names use letters, digits, `_` and `-`; `aiplam-config` is reserved.

### Safety

- `aiplam` only creates and removes **links**: relative symbolic links on Linux and macOS, directory junctions on
  Windows (which need no administrator rights or developer mode). It never deletes a real file or folder, and never
  touches a playbook's content.
- Commands that change links are all-or-nothing: if something other than the right link is already where a link
  must go (an old copy of a skill, a link elsewhere, an artifact another playbook provides), nothing is changed and the
  problem is reported. If creating or removing a link fails midway, the changes already made are undone.
- Only links that point into a playbook's artifact folders (such as its skills folder) and are named after it are
  ever removed.
- Every command can be run again safely. `check` changes nothing, and exits with 1 when anything needs attention.

### Configuration

A configuration lives in the `.aiplam` folder of its base folder, in YAML files that aiplam writes and `aiplam check`
validates:

- `aiplam-config.yaml`, the base folder's configuration: the types of agents configuration folders and the enabled
  sets. Written by `init`, `type` and `set enable`, `disable` and `switch`.

  ```yaml
  types:
    - agents
    - claude
  enabled:
    - default
  ```

- `<set>.yaml`, one per set: its playbooks. Written by `init`, `set add`, `add` and `remove`.

  ```yaml
  playbooks:
    - name: arxila
      folder: Workspace/arxila/arxila-ai-playbook
      artifacts:
        skills:
          path: skills
  ```

Playbook folders are relative to the base folder (`../playbooks/x` is fine). Prefer the commands to editing the files
by hand: a hand edit does not move any link.

## Exit codes

`0` success, `1` failure, cancelled operation or (for `check`) something needs attention, `2` invalid command line.

## License

aiplam is open-source software by [Arxila OSS](https://arxila.io), distributed under the
[Apache License 2.0](LICENSE.txt).
