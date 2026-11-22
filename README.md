# aiplam: AI Playbook Manager

An **AI playbook** is a git repository of agent artifacts (skills, and later other kinds) shared by a team or by a
family of related projects. Each developer keeps a local clone of each playbook, checked out at the tag (or commit)
they want to use. `aiplam` registers those clones and installs their artifacts into the folders that coding agents
read (`~/.agents`, `~/.claude`), as links into the clones, so that switching a clone to another tag and running
`aiplam update` is all an update takes.

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

- Its **skills** are the folders named `<name>-*` that contain a `SKILL.md`, inside its skills folder: `skills/` by
  default, or any other folder inside the playbook's, given relative to it (`--skills-path` or `-sp`, `.` for the
  folder itself). Artifacts never come from outside the playbook's folder, not even through links.
- One repository can be registered as several playbooks with different names (for example `x` and `x-y`). When names
  overlap, the longest one wins: `x-y-foo` belongs to `x-y`, not to `x`.
- The same name can be registered from several folders, as long as their last path segments differ. Their **ids**
  are then `<name>@<last folder segment>` (for example `arxila@arxila-ai-playbook`); otherwise the id is the name.
  Ids are computed, not stored. Two playbooks cannot provide a skill with the same name.
- A playbook's version is whatever is checked out: the tags of its current commit, or else its branch.

## Usage

```bash
aiplam init -t|--target agents|claude|DIR... [-s SET]  # set up: targets, and a first set, enabled
aiplam add <name> <folder> [-sp|--skills-path P]       # register a playbook and link its skills
aiplam update [<name|id|folder>] [<folder>]            # link new skills, remove links to deleted ones
aiplam update --all
aiplam remove <name|id|folder> [<folder>]              # remove a playbook's links and unregister it
aiplam list [--json]                                   # playbooks, versions and skills
aiplam check                                           # what update would change; config and broken links
aiplam set list|add|remove|enable [<set>]              # manage sets of playbooks
aiplam target [agents|claude|DIR...]                   # show or change the target folders
```

Playbooks are registered in [sets](#sets), and only the enabled set is installed. `add`, `update`, `remove`, `list`
and `check` act on the enabled set, or on another one given with `--set SET` (or `-s SET`).

Without a selector, `update` acts on the playbook registered for the current folder. A selector is an id, a name
(when only one playbook has it), a folder (when only one playbook is registered there), or a name and a folder.

Example:

```bash
aiplam init --target agents,claude
cd ~/Workspace/arxila/arxila-ai-playbook
git switch --detach v1.2.0
aiplam add arxila .
aiplam list
```

### Sets

A **set** is a group of registered playbooks. Exactly one set is **enabled**: its playbooks are the ones linked into
the targets. `init` creates a first set, `default` (or the one given with `--set`), and enables it.

```bash
aiplam set add acme                                       # create an empty set
aiplam add acme ~/Workspace/acme/acme-ai-playbook -s acme # register in it: nothing is linked yet
aiplam set enable acme                                    # unlink the enabled set's playbooks, link acme's
aiplam set list                                           # the sets; '*' marks the enabled one
aiplam set remove default                                 # delete a set that is not enabled
```

In a set that is not enabled, `add` and `remove` only register and unregister playbooks, and `update` has nothing to
do. Enabling a set is all-or-nothing, like `add` (links that both sets want are left alone). The enabled set cannot
be removed, but any other can, including `default`. Set names use letters, digits, `_` and `-`; `aiplam-config` is
reserved.

### Targets

The **targets** are the folders where the enabled set's artifacts are installed: skills are linked into each
target's `skills/` subfolder. `init` needs at least one (`agents` for `~/.agents`, `claude` for `~/.claude`, or any
folder). `aiplam target` shows them, and `aiplam target agents claude` replaces them: the enabled set's playbooks
are unlinked from the folders that are no longer targets and linked into the new ones, all-or-nothing.

### Safety

- `aiplam` only creates and removes **links**: symbolic links on Linux and macOS, directory junctions on Windows
  (which need no administrator rights or developer mode). It never deletes a real file or folder, and never touches
  a playbook's content.
- Commands that change links are all-or-nothing: if something other than the right link is already where a link
  must go (an old copy of a skill, a link elsewhere, a skill another playbook provides), nothing is changed and the
  problem is reported. If creating or removing a link fails midway, the changes already made are undone.
- Only links that point into a playbook's skills folder and are named after it are ever removed.
- Every command can be run again safely. `check` changes nothing, and exits with 1 when anything needs attention.

### Configuration

The configuration lives in `~/.aiplam`, in YAML files that aiplam writes and `aiplam check` validates:

- `aiplam-config.yaml`, the installation: the targets and the enabled set. Written by `init`, `target` and
  `set enable`.

  ```yaml
  targets:
    - ~/.agents
    - ~/.claude
  enabled: default
  ```

- `<set>.yaml`, one per set: its playbooks. Written by `init`, `set add`, `add` and `remove`.

  ```yaml
  playbooks:
    - name: arxila
      folder: /Users/me/Workspace/arxila/arxila-ai-playbook
      artifacts:
        skills:
          path: skills
  ```

A leading `~` stands for the home folder in targets and playbook folders. Prefer the commands to editing the files by
hand: a hand edit does not move any link.

## Exit codes

`0` success, `1` failure, cancelled operation or (for `check`) something needs attention, `2` invalid command line.

## License

aiplam is open-source software by [Arxila OSS](https://arxila.io), distributed under the
[Apache License 2.0](LICENSE.txt).
