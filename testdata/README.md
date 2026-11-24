# Test playbooks

Small playbooks with different layouts, for trying aiplam by hand and for the tests (`TestTestdataPlaybooks` in
`internal/app/testdata_test.go` runs the scenarios below). Go ignores folders named `testdata`, so nothing here is
built. Their prefixes follow the `aiplam-<fruit or vegetable>` convention.

| Playbook                    | Skills artifact folder | Skills                                                    |
|-----------------------------|------------------------|-----------------------------------------------------------|
| `aiplam-lettuce-playbook`   | `skills/` (default)    | `aiplam-lettuce-review`, `aiplam-lettuce-deploy`,         |
|                             |                        | `aiplam-lettuce-data-clean`, `aiplam-lettuce-data-report` |
| `aiplam-tomato-playbook`    | the root (`.`)         | `aiplam-tomato-onboard`, `aiplam-tomato-style`,           |
|                             |                        | `aiplam-garlic-audit`                                     |
| `aiplam-lettuce-fork`       | `ai/skills/`           | `aiplam-lettuce-extra`, `aiplam-lettuce-data-export`      |
| `aiplam-blueberry-playbook` | `skills/`              | `aiplam-blueberry-search`, `aiplam-lettuce-review` (also  |
|                             |                        | in `aiplam-lettuce-playbook`)                             |

What each one covers:

- `aiplam-lettuce-playbook`: two overlapping prefixes in one repository, `aiplam-lettuce` and the longer
  `aiplam-lettuce-data`. `skills/aiplam-lettuce-notes` (no `SKILL.md`) and `skills/other-tool` (no registered
  prefix) are not skills. It also has artifacts of other kinds, which the standard agent ignores: a subagent in two
  formats (`agents/aiplam-lettuce-reviewer.md`, and `.agent.md` for Copilot), a command
  (`commands/aiplam-lettuce-ship.md`) and a rule (`rules/aiplam-lettuce-style.md`).
- `aiplam-tomato-playbook`: skills at the root of the repository, with two unrelated prefixes (`aiplam-tomato` and
  `aiplam-garlic`); `docs/` is not a skill.
- `aiplam-lettuce-fork`: the name `aiplam-lettuce` from a second playbook folder, with a custom skills path.
- `aiplam-blueberry-playbook`: registered as `aiplam-lettuce` it is cancelled, because `aiplam-lettuce-review` is
  already provided; registered as `aiplam-blueberry` it works.

## Trying them

`make sandbox` builds aiplam and opens a shell where `HOME` is a new temporary folder, with copies of these playbooks
in `~/playbooks`. The real `~/.aiplam`, `~/.agents`, `~/.claude` and so on are not touched: in the sandbox shell
(where `AIPLAM_SANDBOX` is set), aiplam refuses any base folder outside the sandbox, even when run from a folder
outside it. In the copies,
`aiplam-lettuce-playbook` is a git repository with tags `v1.0.0` (without `aiplam-lettuce-data-report`) and `v1.1.0`,
and `aiplam-tomato-playbook` is a git repository on branch `main`; the others are plain folders (version `unknown`).

```bash
cd ~/playbooks
aiplam init --global standard                             # a profile for the standard agent: ~/.agents

# 4 skills
aiplam add aiplam-lettuce aiplam-lettuce-playbook
# longest prefix: takes over aiplam-lettuce-data-*
aiplam add aiplam-lettuce-data aiplam-lettuce-playbook
# ids become aiplam-lettuce@aiplam-lettuce-playbook and aiplam-lettuce@aiplam-lettuce-fork
aiplam add aiplam-lettuce aiplam-lettuce-fork --skills-path ai/skills
# cancelled: aiplam-lettuce-review is already provided
aiplam add aiplam-lettuce aiplam-blueberry-playbook
aiplam add aiplam-blueberry aiplam-blueberry-playbook
# skills at the root, two prefixes in the same playbook folder
aiplam add aiplam-tomato aiplam-tomato-playbook --skills-path .
aiplam add aiplam-garlic aiplam-tomato-playbook --skills-path .
aiplam list
aiplam check

# select another version: check reports the link to remove, and update removes it
git -C aiplam-lettuce-playbook switch -q --detach v1.0.0
aiplam check
aiplam update aiplam-lettuce-data

# a second profile that collides with the first one (both provide aiplam-tomato): it can replace it, not join it
aiplam profile add other standard
aiplam add aiplam-tomato aiplam-tomato-playbook --skills-path . -p other
aiplam profile enable other                               # fails: its links already exist
aiplam profile switch other                               # disable default, enable other
aiplam profile list
aiplam profile switch default

# a third profile that does not collide can be enabled besides the first one
aiplam profile add extra standard
aiplam remove aiplam-blueberry
aiplam add aiplam-blueberry aiplam-blueberry-playbook -p extra
aiplam profile enable extra
aiplam list

# another working scope, 'greens', assigned Claude Code: skills, subagent, command and rule linked from ~/.claude,
# with no collision with the profiles for the standard agent
aiplam profile add greens claude
aiplam add aiplam-lettuce aiplam-lettuce-playbook -p greens
aiplam profile enable greens
# greens assigned Copilot instead: the .agent.md subagent, from ~/.copilot; warns that Copilot also reads ~/.agents
aiplam profile agent greens copilot
aiplam profile disable greens                             # ~/.copilot is removed once empty

# a project base folder, used by the commands run anywhere inside the base folder ~/projects/demo
mkdir -p ~/projects/demo/module && cd ~/projects/demo
git init -q                                               # init then suggests what to add to .gitignore
aiplam init . standard                                    # .aiplam in ~/projects/demo
aiplam add aiplam-garlic ~/playbooks/aiplam-tomato-playbook -sp .   # fails: already linked from ~/.agents
aiplam remove aiplam-garlic --global
aiplam add aiplam-garlic ~/playbooks/aiplam-tomato-playbook -sp .   # linked from ~/projects/demo/.agents
cat .aiplam/default.yaml                                  # folder: ../../playbooks/aiplam-tomato-playbook
cd module
aiplam list --all                                         # the project's base folder, then ~
```
