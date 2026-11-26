#!/bin/sh
#
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
# Checks the release archives that 'make dist' wrote, before they are published. Usage:
#
#   scripts/check-dist.sh <dist folder> <binary name> <version> [<signing key fingerprint>]
#
# Each archive (aiplam-<version>-<os>-<arch>.tar.gz, or .zip for Windows) must hold exactly its top folder, named like
# the archive, with the executable (executable, in the .tar.gz), README.md, LICENSE.txt and NOTICE.txt: nothing else,
# so no files of the building machine (macOS's "._*" and .DS_Store files), no agent files (AGENTS.md, CLAUDE.md,
# .agents/, .claude/...), nothing from the repository. The .tar.gz archives must not record extended attributes, nor
# the name of the user who built them. SHA256SUMS must list exactly the archives, with their checksums. With a
# signing key, SHA256SUMS.asc must be a valid signature of SHA256SUMS made with that key; without one, there must be no
# SHA256SUMS.asc.

set -eu

[ $# -eq 3 ] || [ $# -eq 4 ] ||
	{ echo "usage: $0 <dist folder> <binary name> <version> [<signing key fingerprint>]" >&2; exit 2; }
DIST=$1 BINARY=$2 VERSION=$3 SIGN_KEY=$(printf %s "${4:-}" | tr a-f A-F)
USER_NAME=$(id -un)
failed=0
fail() { echo "check-dist: $*" >&2; failed=1; }

archives=$(cd "$DIST" && ls "$BINARY-$VERSION"-*.tar.gz "$BINARY-$VERSION"-*.zip 2>/dev/null || true)
[ -n "$archives" ] || { echo "check-dist: no archives in $DIST" >&2; exit 1; }

for archive in $archives; do
	file=$DIST/$archive
	case $archive in
		*.zip) name=${archive%.zip}; exe=$BINARY.exe; entries=$(unzip -Z1 "$file") ;;
		*) name=${archive%.tar.gz}; exe=$BINARY; entries=$(tar -tzf "$file") ;;
	esac
	expected=$(printf '%s\n' "$name/" "$name/$exe" "$name/README.md" "$name/LICENSE.txt" "$name/NOTICE.txt" | sort)
	actual=$(printf '%s\n' "$entries" | sort)
	if [ "$actual" != "$expected" ]; then
		fail "$archive holds other entries than expected:"
		printf '%s\n' "$actual" | sed 's/^/    /' >&2
	fi
	case $archive in
		*.tar.gz)
			if gzip -dc "$file" | grep -aq 'xattr\.'; then
				fail "$archive records extended attributes"
			fi
			if tar -tvzf "$file" | grep -q "[[:space:]]$USER_NAME[[:space:]]"; then
				fail "$archive records the user name '$USER_NAME'"
			fi
			if ! tar -tvzf "$file" | grep -q "^-rwx.*$name/$exe\$"; then
				fail "$archive: $name/$exe is not executable"
			fi
			;;
	esac
done

# SHA256SUMS: one line per archive, with its checksum.
listed=$(awk '{ sub(/^\*/, "", $2); print $2 }' "$DIST/SHA256SUMS" | sort)
[ "$listed" = "$(printf '%s\n' $archives | sort)" ] || fail "SHA256SUMS does not list exactly the archives"
if command -v sha256sum >/dev/null; then
	(cd "$DIST" && sha256sum -c --quiet SHA256SUMS) || fail "SHA256SUMS does not match the archives"
else
	(cd "$DIST" && shasum -a 256 -c --quiet SHA256SUMS) || fail "SHA256SUMS does not match the archives"
fi

# SHA256SUMS.asc: signed with the signing key (gpg reports the fingerprint of the key's primary key last in VALIDSIG).
if [ -n "$SIGN_KEY" ]; then
	if [ ! -f "$DIST/SHA256SUMS.asc" ]; then
		fail "SHA256SUMS.asc is missing"
	else
		signer=$(gpg --batch --status-fd 1 --verify "$DIST/SHA256SUMS.asc" "$DIST/SHA256SUMS" 2>/dev/null |
			awk '$2 == "VALIDSIG" { print $NF }')
		[ "$signer" = "$SIGN_KEY" ] || fail "SHA256SUMS.asc is not a valid signature of SHA256SUMS by $SIGN_KEY"
	fi
elif [ -e "$DIST/SHA256SUMS.asc" ]; then
	fail "SHA256SUMS.asc is there, but no signing key was given to check it"
fi

[ $failed -eq 0 ] || exit 1
echo "check-dist: the archives in $DIST hold exactly what they must"
