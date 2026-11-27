#!/bin/sh
# =========================================================================
#
#   Copyright (c) 2026 Arxila OSS (https://arxila.io)
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
#   implied. See the License for the specific language governing
#   permissions and limitations under the License.
#
# =========================================================================
#
# Opens a shell to try aiplam by hand, with HOME set to a new temporary folder, the aiplam built from this repository
# first in the PATH, and copies of the test playbooks in testdata/playbooks under $HOME/playbooks. The real ~/.aiplam
# and target folders, such as ~/.agents or ~/.claude, are never touched: AIPLAM_SANDBOX makes aiplam
# refuse any managed folder outside the sandbox, even when run from a folder outside it (where it would otherwise find
# the real ~/.aiplam walking up) or given one. Two of the playbooks are made git repositories:
#
#   aiplam-lettuce-playbook  tags v1.0.0 (without aiplam-lettuce-data-report) and v1.1.0 (everything),
#                            checked out at v1.1.0 on main
#   aiplam-tomato-playbook   branch main, no tags
#
# Usage: scripts/sandbox.sh [path/to/aiplam]   (make sandbox builds aiplam and runs it)

set -eu

repo=$(cd "$(dirname "$0")/.." && pwd)
bin=${1:-$repo/aiplam}
[ -x "$bin" ] || { echo "aiplam binary not found at $bin: run 'make build' first" >&2; exit 1; }
bin=$(cd "$(dirname "$bin")" && pwd)/$(basename "$bin")

tmp=${TMPDIR:-/tmp}
sandbox=$(mktemp -d "${tmp%/}/aiplam-sandbox.XXXXXX")
# Removed if setting it up fails; kept once the shell starts, for the session.
trap 'rm -rf "$sandbox"' EXIT
sandbox=$(cd "$sandbox" && pwd -P)
mkdir -p "$sandbox/bin"
ln -s "$bin" "$sandbox/bin/aiplam"
cp -R "$repo/testdata/playbooks" "$sandbox/playbooks"
# Shell start-up files, so that the shell does not run its first-use setup and the prompt shows the sandbox.
echo "PS1='(aiplam-sandbox) %~ %# '" >"$sandbox/.zshrc"
echo "PS1='(aiplam-sandbox) \\w \\$ '" >"$sandbox/.bashrc"

# git for the example repositories, without the user's global and system settings (hooks, ignore rules, templates...),
# which could stop it or leave files out.
g() {
  dir=$1
  shift
  GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 git -C "$dir" -c user.name=aiplam -c user.email=aiplam@example.com \
    -c commit.gpgsign=false -c tag.gpgsign=false "$@" >/dev/null
}

if command -v git >/dev/null 2>&1; then
  lettuce=$sandbox/playbooks/aiplam-lettuce-playbook
  mv "$lettuce/skills/aiplam-lettuce-data-report" "$sandbox/aiplam-lettuce-data-report"
  g "$lettuce" init -q -b main
  g "$lettuce" add -A
  g "$lettuce" commit -q -m "Lettuce playbook 1.0.0"
  g "$lettuce" tag v1.0.0
  mv "$sandbox/aiplam-lettuce-data-report" "$lettuce/skills/aiplam-lettuce-data-report"
  g "$lettuce" add -A
  g "$lettuce" commit -q -m "Lettuce playbook 1.1.0: add aiplam-lettuce-data-report"
  g "$lettuce" tag v1.1.0

  tomato=$sandbox/playbooks/aiplam-tomato-playbook
  g "$tomato" init -q -b main
  g "$tomato" add -A
  g "$tomato" commit -q -m "Tomato playbook"
else
  echo "git not found: the playbooks are plain folders and their versions show as unknown" >&2
fi

# The sandbox's path, quoted for the shell, for the command printed below.
quoted="'$(printf '%s' "$sandbox" | sed "s/'/'\\\\''/g")'"

cat <<MSG

aiplam sandbox: $sandbox
HOME is the sandbox and 'aiplam' is $bin. Playbooks are in ~/playbooks (see testdata/README.md for scenarios).
aiplam refuses managed folders outside the sandbox, so run it from folders inside it.
Type 'exit' to leave; then delete the sandbox with: rm -rf $quoted

MSG

trap - EXIT
cd "$sandbox"
HOME=$sandbox PATH=$sandbox/bin:$PATH AIPLAM_SANDBOX=$sandbox exec "${SHELL:-/bin/sh}"
