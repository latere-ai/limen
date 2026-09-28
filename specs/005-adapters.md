---
title: "Adapters: Claude Code and Codex hooks, Topos in process, a generic HTTP caller"
status: drafted
track: core
depends_on:
  - specs/002-contract.md
  - specs/003-http-api.md
affects: [cmd/limen-hook/, docs/integrating.md]
effort: medium
created: 2026-09-28
updated: 2026-09-28
author: changkun
---

# Adapters

## Overview

Limen is useful only where a harness asks it. An adapter is the code
that sits at a harness's permission decision, builds an `Action` from
what the harness knows about a call, sets the ceiling from the harness's
own deterministic layers, applies the verdict, and sends the person's
answer back. This spec defines four: a command a hook runs, for Claude
Code and for Codex; the in-process integration a Go harness such as
[Topos](https://github.com/latere-ai/topos) uses; and the HTTP sequence
any other harness follows.

Every adapter holds the same three rules, because they are what make
Limen safe to put in front of a harness at all: the ceiling comes from
the harness's deterministic layers and not from Limen; a failure to
reach Limen is an ask, never an allow (`FailClosed`); and a `flag`
verdict runs the call and leaves a review, never a silent allow.

## Design

### The shared mapping

| Harness fact | `Action` field |
|---|---|
| the person the agent acts for | `subject` (issuer and sub of their credential, joined by a pipe) |
| their organization | `org` |
| the harness's id for the call | `call_id`; where a harness has none, a digest of the session id, the tool name, the input and the time |
| the tool name and its input | `tool`, `input` |
| read-only, writes inside the working tree, or reaches outside | `effect` |
| the file, ref or host the call acts on, when the harness can tell | `target` |
| a sandbox or the person's machine, and whether it holds a credential | `machine` |
| a rule score or a judge's verdict the harness already computed | `signals` |
| the harness's deny list, always-confirm list, plan mode | `ceiling`: `block`, `ask`, or `block` for any write in plan mode; `allow` otherwise |

The verdict maps back the same way everywhere:

| Verdict | Harness action |
|---|---|
| `allow` | run the call |
| `flag` | run the call and put it on the person's review list ([[003-http-api]], `pending=true`) |
| `ask` | hold the call for the person; send their answer to `Answer` |
| `block` | refuse the call with the assessment's `reason` as the result the agent reads |

### `limen-hook` for Claude Code

Claude Code runs a command for a `PreToolUse` hook with the event as JSON
on standard input and reads a permission decision from standard output
(`allow`, `ask` or `deny`, with a reason). `limen-hook claude-code`
reads the event, builds the action, calls a running `limend` through
`client/` wrapped in `FailClosed` with a 2 second timeout, and writes
the decision: `allow` for `allow` and for `flag` (with a review entry),
`ask` for `ask`, `deny` for `block`. Claude Code's own permission rules
run beside the hook and keep their denies, so the ceiling for its hook
is `allow` unless the adapter's configuration names deny or confirm
patterns of its own.

A hook returning `allow` removes Claude Code's prompt for that call.
This is the one harness here where an adapter can raise a verdict, and
it is why the adapter never returns `allow` for anything but an `allow`
or `flag` assessment and returns `ask` on every failure.

**Open question: the answer.** A `PostToolUse` event for the same tool
use id means the call ran, so after an `ask` it is an approval. How a
denial of Claude Code's prompt reaches a hook has to be confirmed against
the Claude Code release this adapter is built for; until it is, the
adapter records a denial only when the harness reports one, and an ask
with no answer stays pending rather than being guessed. Whether Claude
Code exposes its own auto-mode classifier's verdict to a hook is the
second question; when it does not, the adapter sends no judge signal
and the `tools` or `patterns` featurizer applies ([[004-engine]]).

### `limen-hook` for Codex

Codex's approval policy and sandbox decide when a command needs the
person, and its reviewer sub-agent can decide instead of the person. The
adapter belongs at the point where Codex would ask: Limen assesses the
command with Codex's sandbox and policy as the ceiling and the
reviewer's verdict, when the adapter can read it, as a judge signal.
Which extension point Codex offers for this (a hook, an approval
callback, or wrapping the command the reviewer runs) is decided when
this spec is validated against the Codex release it targets; this spec
does not assume one.

### Topos, in process

Topos's permission design records a risk score with its source for
every tool call and reserves the source `learned/<version>`; its
`progressive` mode maps the score to a verdict with thresholds that a
learned source may set per person and per agent. The integration is:

1. Topos imports `engine/` and wraps it in `FailClosed` with a timeout
   of 200 milliseconds, because the engine runs in the harness's hot
   path.
2. Topos evaluates its hard boundary, its always-confirm list and its
   mode as it does today and passes the result as the ceiling.
3. In `progressive` mode with a `learned/*` source, Topos takes the
   assessment's verdict as the mode's verdict and records the
   assessment's score, source and thresholds on the call's event. Its
   deterministic `flag` band is empty (`flag_at` equals `ask_at`): a flag
   from Limen is an audit, and a flag band on top of it would add
   reviews that carry no known probability.
4. Topos's hooks still run after the mode and may lower the verdict.
5. A person's confirmation of a call is sent to `Answer`.

Limen is a score source here and not a hook, because a Topos hook may
only make a verdict stricter: a learned tolerance that could never turn
an ask into an allow would add cost and remove nothing.

### Any other harness, over HTTP

```
POST /v1/assessments                 {action, ceiling from the harness's rules}
  -> apply the verdict; on any error or after the timeout, ask
POST /v1/assessments/{id}/answer     {approve, at}   when the person decides
GET  /v1/assessments?subject=...&pending=true         for a review list
```

A caller in Go uses `client/` and `FailClosed`; a caller in another
language implements the same fallback: an error, a timeout, or a
verdict more permissive than its own ceiling is treated as `ask`
narrowed by the ceiling.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | For each verdict, `limen-hook claude-code` writes the decision the table above names, and writes `ask` when `limend` is unreachable, slow, or answers an error | tests feeding recorded hook events to the command against a stub `limend` |
| 2 | `limen-hook` never writes `allow` for an assessment whose verdict is `ask` or `block` | the same tests, with a stub that returns each verdict |
| 3 | A `flag` assessment appears in the subject's pending list after the hook allows the call | an e2e test against `limend` |
| 4 | A `PostToolUse` event after an `ask` records an approval for the same assessment | an e2e test |
| 5 | The two open questions (how a denial reaches the hook, whether the classifier's verdict does) are answered in this spec's Outcome with the harness release they were checked against | review at validation |
| 6 | In the Topos integration, an always-confirm call is `ask` whatever Limen assesses, a hard-boundary call is `block`, and an engine that times out yields `ask` | tests in the Topos repository that embed the engine |
