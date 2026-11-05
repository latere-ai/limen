#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0
#
# Live check of a running limend from the outside. The release pipeline runs
# it after a rollout and attaches its markdown evidence to the release; an
# operator can run it by hand after an upgrade.
#
# BASE_URL is the public route of this installation, which at Latere is
# https://api.latere.ai/v1/oversight (LIMEN_BASE_PATH). It has no default.
#
# Limen serves no probe route and no version route: every route of its API
# asks for a token. So the only public fact is that the route reaches
# limend, which answers an unauthenticated GET /cases with a 401 in its own
# error envelope. A route that no Ingress rule claims answers 404, and a
# service that is down answers a 502, 503 or nothing. The served version is
# not readable from any public route (missing: GET <BASE_URL>/version), so
# TAG cannot be compared; the evidence says so instead of claiming a match.
#
# Required tools: curl, grep.
#
# Environment:
#   BASE_URL   the public route to check; required
#   TAG        the release being proved; recorded, not verifiable (see above)
#   COMMIT     the release commit, for the evidence
#   DEPLOY_URL the run that deployed it, for the evidence
#   OUTPUT_MD  where to write the markdown evidence; nowhere when unset

set -euo pipefail

BASE_URL="${BASE_URL:-}"
BASE_URL="${BASE_URL%/}"
TAG="${TAG:-}"
COMMIT="${COMMIT:-unknown}"
DEPLOY_URL="${DEPLOY_URL:-}"
OUTPUT_MD="${OUTPUT_MD:-}"

pass() { printf 'ok: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

[ -n "$BASE_URL" ] || fail "BASE_URL is required; this smoke has no default target"
for cmd in curl grep; do
  command -v "$cmd" >/dev/null || fail "$cmd is required"
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

path="/cases"
code=$(curl -sS -o "$tmp/cases" -w '%{http_code}' "${BASE_URL}${path}") \
  || fail "GET ${path}: curl failed"
[ "$code" = "401" ] \
  || fail "GET ${path}: expected 401 from limend, got $code (body: $(head -c 200 "$tmp/cases" 2>/dev/null || true))"
grep -q '"unauthenticated"' "$tmp/cases" \
  || fail "GET ${path}: the 401 is not limend's error (body: $(head -c 200 "$tmp/cases" 2>/dev/null || true))"
pass "GET ${path} (401, limend refused the request for want of a token)"

if [ -n "$TAG" ]; then
  pass "tag ${TAG} recorded; limend serves no public version route, so it is not compared"
fi

if [ -n "$OUTPUT_MD" ]; then
  {
    echo "<!-- release-evidence -->"
    echo
    echo "## Release evidence"
    echo
    echo "- Tag: \`${TAG:-unknown}\`"
    echo "- Commit: \`${COMMIT}\`"
    [ -n "$DEPLOY_URL" ] && echo "- Deploy: ${DEPLOY_URL}"
    echo "- Origin: ${BASE_URL}"
    echo "- Served version: not publicly readable (limend serves no version route), so the tag is not compared"
    echo "- Smoke: \`GET ${path}\` answered 401 in limend's error envelope, so the route reaches limend"
  } > "$OUTPUT_MD"
fi

printf '\nrelease smoke passed\n'
