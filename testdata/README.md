# Test playbooks

Small playbooks with different layouts, for trying aiplam by hand and for the tests (`TestTestdataPlaybooks` in
`internal/app/testdata_test.go` runs the scenarios below). Go ignores folders named `testdata`, so nothing here is
built. Their names follow the `aiplam-<fruit or vegetable>` convention.

| Playbook                    | Skills subfolder       | Skills                                                    |
|-----------------------------|------------------------|-----------------------------------------------------------|
| `aiplam-lettuce-playbook`   | `skills/` (default)    | `aiplam-lettuce-review`, `aiplam-lettuce-deploy`,         |
|                             |                        | `aiplam-lettuce-data-clean`, `aiplam-lettuce-data-report` |
| `aiplam-tomato-playbook`    | the root (`.`)         | `aiplam-tomato-onboard`, `aiplam-tomato-style`,           |
|                             |                        | `aiplam-garlic-audit`                                     |
| `aiplam-lettuce-fork`       | `ai/skills/`           | `aiplam-lettuce-extra`, `aiplam-lettuce-data-export`      |
| `aiplam-blueberry-playbook` | `skills/`              | `aiplam-blueberry-search`, `aiplam-lettuce-review` (also  |
|                             |                        | in `aiplam-lettuce-playbook`)                             |

What each one covers:

- `aiplam-lettuce-playbook`: `skills/aiplam-lettuce-notes` has no `SKILL.md`, but it matches the default format
  (`aiplam-lettuce-*`), so it is linked as well: aiplam links whatever the format matches. `skills/other-tool` does
  not match it. Registering the same folder again as `aiplam-lettuce-data` fails: its format overlaps, and every link
  belongs to a single playbook. It also has artifacts of other kinds: a subagent in two formats
  (`agents/aiplam-lettuce-reviewer.md`, and `.agent.md` for Copilot), a command (`commands/aiplam-lettuce-ship.md`)
  and a rule (`rules/aiplam-lettuce-style.md`). Its subagents are in `agents/`, not in the default `subagents/`.
- `aiplam-tomato-playbook`: skills at the root of the repository, with two unrelated names (`aiplam-tomato` and
  `aiplam-garlic`) whose formats do not overlap; `docs/` matches neither.
- `aiplam-lettuce-fork`: the name `aiplam-lettuce` from a second playbook folder, with its skills in `ai/skills`.
- `aiplam-blueberry-playbook`: registered as `aiplam-lettuce` it is refused, because `aiplam-lettuce-review` is
  already linked; registered as `aiplam-blueberry` it works.

## Trying them

`make sandbox` builds aiplam and opens a shell where `HOME` is a new temporary folder, with copies of these playbooks
in `~/playbooks`. The real `~/.aiplam`, `~/.agents`, `~/.claude` and so on are not touched: in the sandbox shell
(where `AIPLAM_SANDBOX` is set), aiplam refuses any managed folder outside the sandbox, even when run from a folder
outside it. In the copies, `aiplam-lettuce-playbook` is a git repository with tags `v1.0.0` (without
`aiplam-lettuce-data-report`) and `v1.1.0`, and `aiplam-tomato-playbook` is a git repository on branch `main`; the
others are plain folders (version `unknown`).

```bash
cd ~/playbooks
aiplam init --user --agent standard                       # a profile with skills in ~/.agents/skills

# 5 skills (aiplam-lettuce-notes included)
aiplam add aiplam-lettuce aiplam-lettuce-playbook
# refused: its format overlaps aiplam-lettuce's in the same folder
aiplam add aiplam-lettuce-data aiplam-lettuce-playbook
# ids become aiplam-lettuce@aiplam-lettuce-playbook and aiplam-lettuce@aiplam-lettuce-fork
aiplam add aiplam-lettuce aiplam-lettuce-fork --folder skills:ai/skills
# refused: aiplam-lettuce-review is already linked
aiplam add aiplam-lettuce aiplam-blueberry-playbook
aiplam add aiplam-blueberry aiplam-blueberry-playbook
# skills at the root, two names on the same playbook folder
aiplam add aiplam-tomato aiplam-tomato-playbook --folder skills:.
aiplam add aiplam-garlic aiplam-tomato-playbook --folder skills:.
aiplam list
aiplam status

# select another version: status reports the link to remove, and update removes it
git -C aiplam-lettuce-playbook switch -q --detach v1.0.0
aiplam status
aiplam update aiplam-lettuce@aiplam-lettuce-playbook

# a second profile registering the same playbook: it can replace the first one, not join it
aiplam profile add other --agent standard
aiplam add aiplam-tomato aiplam-tomato-playbook --folder skills:. -p other
aiplam profile enable other                               # refused: the same playbook in two enabled profiles
aiplam profile switch other                               # disable default, enable other
aiplam profile list
aiplam profile switch default

# a third profile that does not collide can be enabled besides the first one
aiplam profile add extra --agent standard
aiplam remove aiplam-blueberry
aiplam add aiplam-blueberry aiplam-blueberry-playbook -p extra
aiplam profile enable extra
aiplam list

# another working scope for Claude Code, with the lettuce playbook under another name (the same playbook cannot be
# in two enabled profiles), its formats given by hand, and its subagents in agents/
aiplam profile add greens --agent claude
aiplam add greens aiplam-lettuce-playbook -p greens -f 'skills:skills:aiplam-lettuce-*' \
    -f 'subagents:agents:aiplam-lettuce-*.md' -f 'commands:commands:aiplam-lettuce-*' -f 'rules:rules:aiplam-lettuce-*'
aiplam profile enable greens
aiplam list
aiplam profile disable greens                             # ~/.claude is removed once empty

# a project's managed folder, used by the commands run anywhere inside ~/projects/demo
mkdir -p ~/projects/demo/module && cd ~/projects/demo
git init -q                                               # init then suggests what to add to .gitignore
aiplam init . --agent standard                            # .aiplam in ~/projects/demo
aiplam add aiplam-garlic ~/playbooks/aiplam-tomato-playbook --folder skills:.   # ~/projects/demo/.agents/skills
cat .aiplam/default.yaml                                  # folder: ../../playbooks/aiplam-tomato-playbook
cd module
aiplam status                                             # the project's managed folder, and its git repository
aiplam list --user                                        # the user managed folder's links
```
