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
#   PLATFORMS='<os>/<arch> ...' scripts/check-dist.sh <dist folder> <binary name> <version> [<signing key fingerprint>]
#
# PLATFORMS lists the platforms there must be an archive for, and no other ('make dist' and 'make release-sign' give
# the Makefile's). The folder must hold nothing else than the archives, SHA256SUMS and SHA256SUMS.asc.
# Each archive (aiplam-<version>-<os>-<arch>.tar.gz, or .zip for Windows) must hold exactly its top folder, named like
# the archive, with the executable (executable, in the .tar.gz), README.md, LICENSE.txt and NOTICE.txt: nothing else,
# so no files of the building machine (macOS's "._*" and .DS_Store files), no agent files (AGENTS.md, CLAUDE.md,
# .agents/, .claude/...), nothing from the repository. The .tar.gz archives must not record extended attributes, nor
# anyone but root (uid 0) as an owner (it reads their headers with python3). SHA256SUMS must list exactly the
# archives, with their checksums. With a signing key, SHA256SUMS.asc must be a good signature of SHA256SUMS made with
# that key (not expired, nor revoked); without one, there must be no SHA256SUMS.asc.

set -eu

[ $# -eq 3 ] || [ $# -eq 4 ] ||
	{ echo "usage: $0 <dist folder> <binary name> <version> [<signing key fingerprint>]" >&2; exit 2; }
DIST=$1 BINARY=$2 VERSION=$3 SIGN_KEY=$(printf %s "${4:-}" | tr a-f A-F)
command -v python3 >/dev/null || { echo "check-dist: python3 is needed (to read the archives' headers)" >&2; exit 1; }
[ -n "${PLATFORMS:-}" ] || { echo "check-dist: PLATFORMS must list the platforms of the archives" >&2; exit 2; }
failed=0
fail() { echo "check-dist: $*" >&2; failed=1; }

archives=$(cd "$DIST" && ls "$BINARY-$VERSION"-*.tar.gz "$BINARY-$VERSION"-*.zip 2>/dev/null || true)
[ -n "$archives" ] || { echo "check-dist: no archives in $DIST" >&2; exit 1; }

# Exactly one archive per platform: a .zip for Windows, a .tar.gz for the others.
expected=$(for p in $PLATFORMS; do
	os=${p%/*} arch=${p#*/} ext=tar.gz
	[ "$os" = windows ] && ext=zip
	echo "$BINARY-$VERSION-$os-$arch.$ext"
done | sort)
# Nothing else than the expected archives, SHA256SUMS and its signature: no other file can be published with them.
# Python compares the names exactly (shell words would split a name with spaces into allowed ones).
extra=$(python3 -I -c '
import os, sys
for name in sorted(set(os.listdir(sys.argv[1])) - set(sys.argv[2:])):
    print(repr(name))
' "$DIST" $expected SHA256SUMS SHA256SUMS.asc)
[ -z "$extra" ] || fail "$DIST holds files that are not part of a release:$(printf '\n    %s' "$extra")"
[ "$(printf '%s\n' $archives | sort)" = "$expected" ] ||
	fail "the archives are not exactly one per platform:$(printf '\n    %s' $archives)" \
		"(expected:$(printf '\n    %s' $expected))"

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
	# Read from the headers with Python's tarfile and zipfile: the top folder must be a folder and every other entry a
	# regular file (not a link); in a .tar.gz, extended attributes are in pax headers (SCHILY.xattr.*, LIBARCHIVE.xattr.*),
	# not to be searched for in the whole archive, where a file could hold those words, and owners are in each entry's
	# header, whatever tar's listing shows them like.
	problems=$(python3 -I -c '
import stat, sys, tarfile, zipfile
path, top = sys.argv[1], sys.argv[2]
def kind(is_dir, is_file):
    return "a folder" if is_dir else "a regular file" if is_file else "something else (such as a link)"
try:
    if path.endswith(".zip"):
        with zipfile.ZipFile(path) as archive:
            for info in archive.infolist():
                mode = info.external_attr >> 16
                is_dir = info.is_dir()
                is_file = not is_dir and (mode == 0 or stat.S_ISREG(mode))
                if info.filename.rstrip("/") == top and not is_dir or info.filename.rstrip("/") != top and not is_file:
                    print("holds %s as %s" % (info.filename, kind(is_dir, is_file)))
            damaged = archive.testzip()  # reads every entry, checking its CRC
            if damaged:
                print("holds %s damaged (its content does not match its CRC)" % damaged)
        sys.exit(0)
    with tarfile.open(path) as archive:
        members = archive.getmembers()
        headers = [archive.pax_headers] + [member.pax_headers for member in members]
        for member in members:  # read in full, so that gzip checks the whole archive
            if member.isfile():
                content = archive.extractfile(member)
                while content.read(1 << 20):  # in chunks: a member never has to fit in memory
                    pass
except Exception as error:
    print("cannot be read: %s" % error)
    sys.exit(0)
if any("xattr" in key for h in headers for key in h):
    print("records extended attributes")
for m in members:
    if m.name.rstrip("/") == top and not m.isdir() or m.name.rstrip("/") != top and not m.isfile():
        print("holds %s as %s" % (m.name, kind(m.isdir(), m.isfile())))
    if m.uid != 0 or m.gid != 0 or m.uname not in ("", "root") or m.gname not in ("", "root", "wheel"):
        print("records %s as owned by %s:%s (%d:%d), not root" % (m.name, m.uname, m.gname, m.uid, m.gid))
' "$file" "$name")
	if [ -n "$problems" ]; then
		printf '%s\n' "$problems" | while IFS= read -r problem; do echo "check-dist: $archive $problem" >&2; done
		failed=1
	fi
	case $archive in
		*.tar.gz)
			gzip -t "$file" 2>/dev/null || fail "$archive is damaged (gzip finds its content wrong)"
			if ! tar -tvzf "$file" | grep -q "^-rwx.*$name/$exe\$"; then
				fail "$archive: $name/$exe is not executable"
			fi
			;;
	esac
done

# SHA256SUMS: one line per archive, each a SHA-256 checksum and a file name, and every checksum checked (--strict:
# a line in another format fails, instead of being skipped).
malformed=$(awk 'NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-f]/ { print NR }' "$DIST/SHA256SUMS")
[ -z "$malformed" ] || fail "SHA256SUMS has lines that are not a checksum and a file name: $(echo $malformed)"
listed=$(awk '{ sub(/^\*/, "", $2); print $2 }' "$DIST/SHA256SUMS" | sort)
[ "$listed" = "$(printf '%s\n' $archives | sort)" ] || fail "SHA256SUMS does not list exactly the archives"
if command -v sha256sum >/dev/null; then
	(cd "$DIST" && sha256sum -c --quiet --strict SHA256SUMS) || fail "SHA256SUMS does not match the archives"
else
	(cd "$DIST" && shasum -a 256 -c --quiet --strict SHA256SUMS) || fail "SHA256SUMS does not match the archives"
fi

# SHA256SUMS.asc: a good signature (GOODSIG, which gpg does not report for a bad signature, nor for one made with an
# expired or revoked key) made with the signing key (gpg reports the fingerprint of the key's primary key last in
# VALIDSIG).
if [ -n "$SIGN_KEY" ]; then
	if [ ! -f "$DIST/SHA256SUMS.asc" ]; then
		fail "SHA256SUMS.asc is missing"
	else
		status=$(gpg --batch --status-fd 1 --verify "$DIST/SHA256SUMS.asc" "$DIST/SHA256SUMS" 2>/dev/null) || true
		signer=$(printf '%s\n' "$status" | awk '$2 == "VALIDSIG" { print $NF }')
		if ! printf '%s\n' "$status" | grep -q '^\[GNUPG:\] GOODSIG '; then
			fail "SHA256SUMS.asc is not a good signature of SHA256SUMS (bad, or made with an expired or revoked key)"
		elif [ "$signer" != "$SIGN_KEY" ]; then
			fail "SHA256SUMS.asc is not a signature of SHA256SUMS by $SIGN_KEY"
		fi
	fi
elif [ -e "$DIST/SHA256SUMS.asc" ]; then
	fail "SHA256SUMS.asc is there, but no signing key was given to check it"
fi

[ $failed -eq 0 ] || exit 1
echo "check-dist: the archives in $DIST hold exactly what they must"
