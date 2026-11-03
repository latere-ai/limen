# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

GO ?= go

.PHONY: build check clean figures fmt hooks run

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

# Both binaries: limend, the server, and limen, the replay command line.
build:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(OUT_DIR)/limend ./cmd/limend
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(OUT_DIR)/limen ./cmd/limen
	@echo "built $(OUT_DIR)/limend $(OUT_DIR)/limen"

# The server on loopback as one person runs it, with its ledger under out/
# so a trial run leaves the person's own ledger alone. LIMEN_TOKEN is the
# bearer a decision point sends, and limend refuses to start without it.
run: build
	LIMEN_DATA_DIR=$${LIMEN_DATA_DIR:-$(OUT_DIR)/ledger} $(OUT_DIR)/limend

fmt:
	gofmt -w $$(git ls-files '*.go')

# Point git at the delegating hooks. Per clone, so it is a target.
hooks:
	chmod +x .githooks/*
	git config core.hooksPath .githooks

# The figures under docs/figures: Limen's model against the paper's
# simulated person and Limen's engine against a person whose actions
# differ in scope, refitted after every answer (about seven minutes on 18
# cores), then the drawings from that data (about 40 seconds; uv reads
# render.py's own dependencies). docs/figures is a module of its own, so
# the gates above do not build or test it.
figures:
	cd docs/figures && $(GO) run ./cmd/simulate -persons 20 -out data
	cd docs/figures && uv run render.py

# Remove the build output.
clean:
	rm -rf $(OUT_DIR)
