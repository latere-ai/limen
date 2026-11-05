#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0
#
# Live check of a running limend from the outside. The release pipeline runs
# it after a rollout and attaches its markdown evidence to the release; an
# operator can run it by hand after an upgrade.
#
# BASE_URL is the public route of this installation, such as
# https://api.example.com/v1/oversight (LIMEN_BASE_PATH). It has no default.
#
# Limen serves no probe route. Every route of its API but the version asks
# for a token, so the check that the route reaches limend is an
# unauthenticated GET /cases, which limend answers with a 401 in its own
# error envelope. A route that no Ingress rule claims answers 404, and a
# service that is down answers a 502, 503 or nothing.
#
# Checks, in order, stopping at the first failure:
#   1. GET $BASE_URL/cases answers 401 in limend's error envelope.
#   2. GET $BASE_URL/version serves a version, and when TAG is set it
#      equals TAG.
#
# Required tools: curl, grep.
#
# Environment:
#   BASE_URL   the public route to check; required
#   TAG        the release being proved; when set the served version must equal it
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

vcode=$(curl -sS -o "$tmp/version" -w '%{http_code}' "${BASE_URL}/version") \
  || fail "GET /version: curl failed"
[ "$vcode" = "200" ] \
  || fail "GET /version: expected 200, got $vcode (body: $(head -c 200 "$tmp/version" 2>/dev/null || true))"
pass "GET /version (200)"
# `|| true`: grep exits 1 on no match, and the message below should be said.
served=$(grep -o '"version":"[^"]*"' "$tmp/version" | head -1 | cut -d'"' -f4 || true)
[ -n "$served" ] || fail "GET /version: no version in $(head -c 200 "$tmp/version")"
if [ -n "$TAG" ]; then
  [ "$served" = "$TAG" ] || fail "version mismatch: served $served, expected $TAG"
  pass "served version matches the tag ($served)"
else
  pass "served version recorded ($served)"
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
    echo "- Served version: \`${served}\`${TAG:+, equal to the tag}"
    echo "- Smoke: \`GET ${path}\` answered 401 in limend's error envelope, so the route reaches limend"
  } > "$OUTPUT_MD"
fi

printf '\nrelease smoke passed\n'
