# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

GO ?= go

.PHONY: build check clean fmt hooks run

# The whole bar. Every gate lives in latere.ai/x/ci-gate, pinned as a tool
# in go.mod and configured in .lateregate.yaml, so this target is a name for
# `go tool lateregate` and nothing else. One gate at a time: `go tool
# lateregate spec-lint`. The plan: `go tool lateregate list`.
check:
	@$(GO) tool lateregate

.DEFAULT_GOAL := check

OUT_DIR := out
MODULE := $(shell $(GO) list -m)

# Build metadata, deferred so the git and date calls run only for a build. A
# dirty tree marks the commit, because a binary built from uncommitted
# changes cannot be reproduced from its commit.
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DIRTY = $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo -dirty)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG = $(MODULE)/internal/version
LDFLAGS = -X $(VERSION_PKG).Version=$(VERSION) \
          -X $(VERSION_PKG).Commit=$(COMMIT)$(DIRTY) \
          -X $(VERSION_PKG).Date=$(BUILD_DATE)

# Both binaries: cribrumd, the server, and cribrum, the replay command line.
build:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(OUT_DIR)/cribrumd ./cmd/cribrumd
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(OUT_DIR)/cribrum ./cmd/cribrum
	@echo "built $(OUT_DIR)/cribrumd $(OUT_DIR)/cribrum"

# The server on loopback as one person runs it, with its ledger under out/
# so a trial run leaves the person's own ledger alone. CRIBRUM_TOKEN is the
# bearer a decision point sends, and cribrumd refuses to start without it.
run: build
	CRIBRUM_DATA_DIR=$${CRIBRUM_DATA_DIR:-$(OUT_DIR)/ledger} $(OUT_DIR)/cribrumd

fmt:
	gofmt -w $$(git ls-files '*.go')

# Point git at the delegating hooks. Per clone, so it is a target.
hooks:
	chmod +x .githooks/*
	git config core.hooksPath .githooks

# Remove the build output.
clean:
	rm -rf $(OUT_DIR)
