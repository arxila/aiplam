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
aiplam init [-t|--target agents|claude|DIR]...    # create a set's config file (default target: ~/.agents)
aiplam add <name> <folder> [-sp|--skills-path P]  # register a playbook and link its skills
aiplam update [<name|id|folder>] [<folder>]       # link new skills, remove links to deleted ones
aiplam update --all
aiplam remove <name|id|folder> [<folder>]         # remove a playbook's links and unregister it
aiplam list [--json]                              # playbooks, versions and skills
aiplam check                                      # what update would change; config and broken links
aiplam sets                                       # the configuration sets
```

Every command takes `--set SET` (or `-s SET`) to act on a configuration set other than `default` (see
[Sets](#sets)).

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

### Safety

- `aiplam` only creates and removes **links**: symbolic links on Linux and macOS, directory junctions on Windows
  (which need no administrator rights or developer mode). It never deletes a real file or folder, and never touches
  a playbook's content.
- `add` and `update` are all-or-nothing per playbook: if something other than the right link is already where a link
  must go (an old copy of a skill, a link elsewhere, a skill another playbook provides), nothing is changed and the
  problem is reported. If creating or removing a link fails midway, the changes already made are undone.
- `update` and `remove` only remove links that point into the playbook's skills folder and are named after it.
- Every command can be run again safely. `check` changes nothing, and exits with 1 when anything needs attention.

### Configuration

Each set's configuration is a file `~/.aiplam/<set>.yaml` (`~/.aiplam/default.yaml` for the default set), written
by `init`, `add` and `remove`:

```yaml
targets:
  - ~/.agents
  - ~/.claude
playbooks:
  - name: arxila
    folder: /Users/me/Workspace/arxila/arxila-ai-playbook
    artifacts:
      skills:
        path: skills
```

A leading `~` stands for the home folder in targets and playbook folders. Skills are linked into each target's
`skills/` subfolder. To change the targets after `init`, edit the file by hand and move or recreate the installed
links yourself; `aiplam check` validates the file.

### Sets

A **set** is an entirely separate configuration, with its own targets and playbooks, in its own file
`~/.aiplam/<set>.yaml`. Commands act on the `default` set unless given `--set SET` / `-s SET`, anywhere on the
command line. `aiplam init --set SET` creates a set, and `aiplam sets` lists them. Set names use letters, digits, `_`
and `-`.

Sets cannot share targets, because their links would get in each other's way: `init` refuses a target that another
set already has, and if hand edits make two sets share one, `add`, `update` and `remove` refuse to run (and `check`
reports it) until it is fixed.

```bash
aiplam init --set acme --target ~/Workspace/acme/.claude
aiplam add acme ~/Workspace/acme/acme-ai-playbook --set acme
aiplam list -s acme
```

## Exit codes

`0` success, `1` failure, cancelled operation or (for `check`) something needs attention, `2` invalid command line.

## License

aiplam is open-source software by [Arxila OSS](https://arxila.io), distributed under the
[Apache License 2.0](LICENSE.txt).
