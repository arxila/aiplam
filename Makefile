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
# The version is defined in internal/app/app.go; override it with 'make VERSION=x.y.z ...'.
VERSION ?= $(shell sed -n 's/^var Version = "\(.*\)"$$/\1/p' internal/app/app.go)
# Without a version (it could not be read), builds keep the one in the code; dist refuses to run.
LDFLAGS := -s -w $(if $(VERSION),-X $(PKG)/internal/app.Version=$(VERSION))
DIST    := dist

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

dist: clean
	@[ -n "$(VERSION)" ] || { echo "cannot read the version from internal/app/app.go: run 'make VERSION=x.y.z dist'" >&2; exit 1; }
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=$(DIST)/$(BINARY)-$(VERSION)-$$os-$$arch$$ext; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd/aiplam || exit 1; \
	done

clean:
	rm -rf $(DIST) $(BINARY)

# A shell with HOME set to a temporary folder holding copies of testdata/playbooks (see scripts/sandbox.sh).
sandbox: build
	./scripts/sandbox.sh ./aiplam
