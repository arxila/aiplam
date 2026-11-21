# Test playbooks

Small playbooks with different layouts, for trying aiplam by hand and for the tests (`TestTestdataPlaybooks` in
`internal/app/testdata_test.go` runs the scenarios below). Go ignores folders named `testdata`, so nothing here is
built. Their prefixes follow the `aiplam-<fruit or vegetable>` convention.

| Playbook                    | Skills folder          | Skills                                                    |
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
  prefix) are not skills.
- `aiplam-tomato-playbook`: skills at the root of the repository, with two unrelated prefixes (`aiplam-tomato` and
  `aiplam-garlic`); `docs/` is not a skill.
- `aiplam-lettuce-fork`: the name `aiplam-lettuce` from a second folder, with a custom skills path.
- `aiplam-blueberry-playbook`: registered as `aiplam-lettuce` it is cancelled, because `aiplam-lettuce-review` is
  already provided; registered as `aiplam-blueberry` it works.

## Trying them

`make sandbox` builds aiplam and opens a shell where `HOME` is a new temporary folder (the real `~/.aiplam`,
`~/.agents` and `~/.claude` are not touched), with copies of these playbooks in `~/playbooks`. In the copies,
`aiplam-lettuce-playbook` is a git repository with tags `v1.0.0` (without `aiplam-lettuce-data-report`) and `v1.1.0`,
and `aiplam-tomato-playbook` is a git repository on branch `main`; the others are plain folders (version `unknown`).

```bash
cd ~/playbooks
aiplam init --target agents,claude

# 4 skills
aiplam add aiplam-lettuce aiplam-lettuce-playbook
# longest prefix: takes over aiplam-lettuce-data-*
aiplam add aiplam-lettuce-data aiplam-lettuce-playbook
# ids become aiplam-lettuce@aiplam-lettuce-playbook and aiplam-lettuce@aiplam-lettuce-fork
aiplam add aiplam-lettuce aiplam-lettuce-fork --skills-path ai/skills
# cancelled: aiplam-lettuce-review is already provided
aiplam add aiplam-lettuce aiplam-blueberry-playbook
aiplam add aiplam-blueberry aiplam-blueberry-playbook
# skills at the root, two prefixes in the same folder
aiplam add aiplam-tomato aiplam-tomato-playbook --skills-path .
aiplam add aiplam-garlic aiplam-tomato-playbook --skills-path .
aiplam list
aiplam check

# select another version: check reports the link to remove, and update removes it
git -C aiplam-lettuce-playbook switch -q --detach v1.0.0
aiplam check
aiplam update aiplam-lettuce-data

# a second, independent set
aiplam init --set other --target ~/other-agents
aiplam add aiplam-tomato aiplam-tomato-playbook --skills-path . -s other
aiplam sets
```
