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
# Signs the draft release that the Release workflow (.github/workflows/release.yml) created for a version, on the
# machine that holds the Arxila release GPG key. Usage ('make release-sign' runs it with the Makefile's values):
#
#   PLATFORMS='<os>/<arch> ...' scripts/sign-release.sh <version> <signing key fingerprint>
#
# PLATFORMS lists the platforms of the archives, for scripts/check-dist.sh.
# It downloads the draft's files, and only signs them if:
#   - the tag v<version> points to the same commit in this clone and on GitHub (origin);
#   - every archive and SHA256SUMS has a build provenance attestation made by the Release workflow of arxila/aiplam,
#     on a GitHub-hosted runner, for that tag, from that commit;
#   - scripts/check-dist.sh accepts them (exactly the expected files in each archive, and checksums that match).
# Then it signs SHA256SUMS into SHA256SUMS.asc, checks the signature, and uploads it to the draft. It does not
# publish the release.

set -eu

[ $# -eq 2 ] || { echo "usage: PLATFORMS='<os>/<arch> ...' $0 <version> <signing key fingerprint>" >&2; exit 2; }
[ -n "${PLATFORMS:-}" ] || { echo "sign-release: PLATFORMS must list the platforms of the archives" >&2; exit 2; }
VERSION=$1 SIGN_KEY=$2
REPO=arxila/aiplam
BINARY=aiplam
TAG=v$VERSION
WORKFLOW=$REPO/.github/workflows/release.yml

for tool in gh gpg git; do
	command -v $tool >/dev/null || { echo "sign-release: $tool is needed" >&2; exit 1; }
done
gpg --list-secret-keys "$SIGN_KEY" >/dev/null 2>&1 ||
	{ echo "sign-release: gpg has no secret key $SIGN_KEY" >&2; exit 1; }

# The commit the attestations must name: the one the tag points to here, which must be the one it points to on GitHub
# (for an annotated tag, git ls-remote gives the tag object, then the commit as <tag>^{}).
COMMIT=$(git rev-parse --verify --quiet "refs/tags/$TAG^{commit}") ||
	{ echo "sign-release: there is no tag $TAG in this clone (fetch it with 'git fetch origin tag $TAG')" >&2; exit 1; }
REMOTE=$(git ls-remote origin "refs/tags/$TAG" "refs/tags/$TAG^{}") ||
	{ echo "sign-release: cannot read the tag $TAG from origin" >&2; exit 1; }
REMOTE_COMMIT=$(printf '%s\n' "$REMOTE" | awk -v ref="refs/tags/$TAG" '
	$2 == ref { plain = $1 }
	$2 == ref "^{}" { peeled = $1 }
	END { print (peeled != "" ? peeled : plain) }')
[ -n "$REMOTE_COMMIT" ] || { echo "sign-release: there is no tag $TAG on origin" >&2; exit 1; }
[ "$REMOTE_COMMIT" = "$COMMIT" ] ||
	{ echo "sign-release: the tag $TAG points to $COMMIT here, but to $REMOTE_COMMIT on origin" >&2; exit 1; }

[ "$(gh release view "$TAG" --repo $REPO --json isDraft --jq .isDraft)" = true ] ||
	{ echo "sign-release: the release $TAG is not a draft" >&2; exit 1; }

DIR=$(mktemp -d)
trap 'rm -rf "$DIR"' EXIT
gh release download "$TAG" --repo $REPO --dir "$DIR"
[ ! -e "$DIR/SHA256SUMS.asc" ] || { echo "sign-release: the draft $TAG is already signed" >&2; exit 1; }

for file in "$DIR"/$BINARY-"$VERSION"-* "$DIR/SHA256SUMS"; do
	echo "checking the attestation of $(basename "$file")"
	gh attestation verify "$file" --repo $REPO --signer-workflow "$WORKFLOW" --source-ref "refs/tags/$TAG" \
		--source-digest "$COMMIT" --deny-self-hosted-runners >/dev/null ||
		{ echo "sign-release: $(basename "$file") has no valid attestation" >&2; exit 1; }
done
"$(dirname "$0")/check-dist.sh" "$DIR" $BINARY "$VERSION"

gpg --local-user "$SIGN_KEY" --armor --detach-sign --output "$DIR/SHA256SUMS.asc" "$DIR/SHA256SUMS"
"$(dirname "$0")/check-dist.sh" "$DIR" $BINARY "$VERSION" "$SIGN_KEY"

gh release upload "$TAG" "$DIR/SHA256SUMS.asc" --repo $REPO
echo "sign-release: the draft $TAG is signed; publish it with: gh release edit $TAG --repo $REPO --draft=false"
