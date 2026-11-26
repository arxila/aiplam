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

# tar options that keep the archives free of the building machine's details: owner names, and on macOS, extended
# attributes and AppleDouble ("._") files, which would show up as junk files when extracted elsewhere.
TARFLAGS := $(shell if tar --version 2>/dev/null | grep -q bsdtar; then \
	echo "--no-xattrs --no-mac-metadata --uid 0 --gid 0 --uname root --gname root"; \
	else echo "--owner=0 --group=0 --numeric-owner"; fi)

# OS/architecture pairs for release binaries.
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: build test vet dist clean sandbox

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/aiplam

test:
	go test ./...

vet:
	go vet ./...
	GOOS=windows go vet ./...

# Release archives: a .tar.gz (a .zip for Windows) per platform, holding the executable with the README and the license
# files (which the Apache License requires to go with every copy), and a SHA256SUMS file. When HEAD is tagged, the tag
# must match the version.
dist: clean
	@[ -n "$(VERSION)" ] || { echo "cannot read the version from internal/app/app.go: run 'make VERSION=x.y.z dist'" >&2; exit 1; }
	@for tag in $$(git tag --points-at HEAD 'v*' 2>/dev/null); do \
		[ "$$tag" = "v$(VERSION)" ] || { echo "HEAD is tagged $$tag, but the version is $(VERSION)" >&2; exit 1; }; \
	done
	@command -v sha256sum >/dev/null || command -v shasum >/dev/null || \
		{ echo "sha256sum or shasum is needed" >&2; exit 1; }
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		name=$(BINARY)-$(VERSION)-$$os-$$arch; dir=$(DIST)/$$name; \
		echo "building $$name"; \
		mkdir -p $$dir && cp LICENSE.txt NOTICE.txt README.md $$dir/ || exit 1; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/$(BINARY)$$ext \
			./cmd/aiplam || exit 1; \
		if [ $$os = windows ]; then \
			(cd $(DIST) && zip -q -r -X $$name.zip $$name) || exit 1; \
		else \
			COPYFILE_DISABLE=1 tar $(TARFLAGS) -C $(DIST) -czf $(DIST)/$$name.tar.gz $$name || exit 1; \
		fi; \
		rm $$dir/* && rmdir $$dir || exit 1; \
	done
	@cd $(DIST) && { command -v sha256sum >/dev/null && sha256sum $(BINARY)-* || shasum -a 256 $(BINARY)-*; } > SHA256SUMS
	@echo "written $(DIST)/SHA256SUMS"

clean:
	rm -rf $(DIST) $(BINARY)

# A shell with HOME set to a temporary folder holding copies of testdata/playbooks (see scripts/sandbox.sh).
sandbox: build
	./scripts/sandbox.sh ./aiplam
