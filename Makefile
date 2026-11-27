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

BINARY  := aiplam
PKG     := arxila.io/aiplam
# The version is defined in internal/app/app.go (sourceVersion); override it with 'make VERSION=x.y.z ...'.
VERSION ?= $(shell sed -n 's/^const sourceVersion = "\(.*\)"$$/\1/p' internal/app/app.go)
# Without a version (it could not be read), builds keep the one in the code; dist refuses to run.
LDFLAGS := -s -w $(if $(VERSION),-X $(PKG)/internal/app.Version=$(VERSION))
DIST    := dist
# The GPG key that signs SHA256SUMS: Arxila Releases <releases@arxila.io>, the key of every Arxila release (its
# fingerprint is in arxila-org's GPG-KEYS-arxila.txt). 'make SIGN_KEY= dist' leaves SHA256SUMS unsigned: the Release
# workflow builds this way, and 'make release-sign' then signs its draft release.
SIGN_KEY ?= 49E0B07D4209B610E1CA35ECF2E549DCAE5A5AB4

# tar options that keep the archives free of the building machine's details: owner names, and on macOS, extended
# attributes and AppleDouble ("._") files, which would show up as junk files when extracted elsewhere.
TARFLAGS := $(shell if tar --version 2>/dev/null | grep -q bsdtar; then \
	echo "--no-xattrs --no-mac-metadata --uid 0 --gid 0 --uname root --gname root"; \
	else echo "--owner=0 --group=0 --numeric-owner"; fi)

# OS/architecture pairs for release binaries.
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: build test vet dist release-sign clean sandbox

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/aiplam

test:
	go test ./...

vet:
	go vet ./...
	GOOS=windows go vet ./...

# Release archives: a .tar.gz (a .zip for Windows) per platform, holding the executable with the README and the license
# files (which the Apache License requires to go with every copy), a SHA256SUMS file, and its detached signature
# SHA256SUMS.asc. When HEAD is tagged, the tag must match the version. scripts/check-dist.sh then checks that the
# archives hold exactly these files, and nothing of the machine that built them, and that SIGN_KEY signed SHA256SUMS.
# They are built in $(DIST).new, which only replaces $(DIST) once all of that passed: a failed build leaves the previous
# one as it was. A $(DIST).old without $(DIST) (a build stopped while replacing it) is the previous one: put back first.
dist:
	@if [ -e $(DIST).old ] && [ ! -e $(DIST) ]; then \
		mv $(DIST).old $(DIST) || exit 1; \
		echo "$(DIST) put back from $(DIST).old, left by a build that was stopped"; \
	fi
	@[ -n "$(VERSION)" ] || { echo "cannot read the version from internal/app/app.go: run 'make VERSION=x.y.z dist'" >&2; exit 1; }
	@if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then \
		tags=$$(git tag --points-at HEAD 'v*') || exit 1; \
		for tag in $$tags; do \
			[ "$$tag" = "v$(VERSION)" ] || { echo "HEAD is tagged $$tag, but the version is $(VERSION)" >&2; exit 1; }; \
		done; \
	fi
	@command -v sha256sum >/dev/null || command -v shasum >/dev/null || \
		{ echo "sha256sum or shasum is needed" >&2; exit 1; }
	@[ -z "$(SIGN_KEY)" ] || gpg --list-secret-keys "$(SIGN_KEY)" >/dev/null 2>&1 || \
		{ echo "cannot sign: gpg is not installed, or has no secret key $(SIGN_KEY)" >&2; \
		echo "(for a build that is not going to be published, run 'make SIGN_KEY= dist')" >&2; exit 1; }
	@for tool in go tar gzip zip unzip python3 cp awk sort; do \
		command -v $$tool >/dev/null || { echo "$$tool is needed to build the release archives" >&2; exit 1; }; \
	done
	@rm -rf $(DIST).new
	@mkdir -p $(DIST).new
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		name=$(BINARY)-$(VERSION)-$$os-$$arch; dir=$(DIST).new/$$name; \
		echo "building $$name"; \
		mkdir -p $$dir && cp LICENSE.txt NOTICE.txt README.md $$dir/ || exit 1; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/$(BINARY)$$ext \
			./cmd/aiplam || exit 1; \
		if [ $$os = windows ]; then \
			(cd $(DIST).new && zip -q -r -X $$name.zip $$name) || exit 1; \
		else \
			COPYFILE_DISABLE=1 tar $(TARFLAGS) -C $(DIST).new -czf $(DIST).new/$$name.tar.gz $$name || exit 1; \
		fi; \
		rm $$dir/* && rmdir $$dir || exit 1; \
	done
	@cd $(DIST).new && { command -v sha256sum >/dev/null && sha256sum $(BINARY)-* || shasum -a 256 $(BINARY)-*; } \
		> SHA256SUMS
	@echo "written SHA256SUMS"
	@if [ -n "$(SIGN_KEY)" ]; then \
		gpg --batch --yes --local-user "$(SIGN_KEY)" --armor --detach-sign \
			--output $(DIST).new/SHA256SUMS.asc $(DIST).new/SHA256SUMS || exit 1; \
		echo "written SHA256SUMS.asc"; \
	else \
		echo "SHA256SUMS is not signed (SIGN_KEY is empty): it must be signed before it is published"; \
	fi
	@PLATFORMS="$(PLATFORMS)" ./scripts/check-dist.sh $(DIST).new $(BINARY) $(VERSION) $(SIGN_KEY)
	@# The previous build is moved aside, and only removed once the new one is in place (put back if that fails).
	@rm -rf $(DIST).old || exit 1; \
	if [ -e $(DIST) ]; then mv $(DIST) $(DIST).old || exit 1; fi; \
	if mv $(DIST).new $(DIST); then \
		rm -rf $(DIST).old $(BINARY); \
	else \
		[ ! -e $(DIST).old ] || mv $(DIST).old $(DIST); \
		exit 1; \
	fi
	@echo "the release archives are in $(DIST)"

# Signs the draft release that the Release workflow created for VERSION, after checking its attestations and archives
# (see scripts/sign-release.sh). Publishing the draft is left to the maintainer.
release-sign:
	@[ -n "$(VERSION)" ] && [ -n "$(SIGN_KEY)" ] || { echo "release-sign needs VERSION and SIGN_KEY" >&2; exit 1; }
	PLATFORMS="$(PLATFORMS)" ./scripts/sign-release.sh $(VERSION) $(SIGN_KEY)

clean:
	rm -rf $(DIST) $(DIST).new $(DIST).old $(BINARY)

# A shell with HOME set to a temporary folder holding copies of testdata/playbooks (see scripts/sandbox.sh).
sandbox: build
	./scripts/sandbox.sh ./aiplam
