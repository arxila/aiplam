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
# Opens a shell to try aiplam by hand, with HOME set to a new temporary folder (so the real ~/.aiplam, ~/.agents and
# ~/.claude are never touched), the aiplam built from this repository first in the PATH, and copies of the test
# playbooks in testdata/playbooks under $HOME/playbooks. Two of them are made git repositories:
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
sandbox=$(cd "$sandbox" && pwd -P)
mkdir -p "$sandbox/bin"
ln -s "$bin" "$sandbox/bin/aiplam"
cp -R "$repo/testdata/playbooks" "$sandbox/playbooks"
# Shell start-up files, so that the shell does not run its first-use setup and the prompt shows the sandbox.
echo "PS1='(aiplam-sandbox) %~ %# '" >"$sandbox/.zshrc"
echo "PS1='(aiplam-sandbox) \\w \\$ '" >"$sandbox/.bashrc"

g() {
  dir=$1
  shift
  git -C "$dir" -c user.name=aiplam -c user.email=aiplam@example.com -c commit.gpgsign=false -c tag.gpgsign=false \
    "$@" >/dev/null
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

cat <<MSG

aiplam sandbox: $sandbox
HOME is the sandbox and 'aiplam' is $bin. Playbooks are in ~/playbooks (see testdata/README.md for scenarios).
Type 'exit' to leave; then delete the sandbox with: rm -rf $sandbox

MSG

cd "$sandbox"
HOME=$sandbox PATH=$sandbox/bin:$PATH AIPLAM_SANDBOX=$sandbox exec "${SHELL:-/bin/sh}"
