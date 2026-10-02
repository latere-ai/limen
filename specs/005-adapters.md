---
title: "Adapters: decision points for Claude Code and Codex hooks, Topos, and any harness over HTTP"
status: drafted
track: core
depends_on: [001-architecture.md, 002-contract.md, 003-http-api.md]
affects: [cmd/limen-hook/, client/]
effort: medium
created: 2026-09-28
updated: 2026-10-02
author: changkun
---

# Adapters

## Overview

Limen is useful only where a decision point asks it. An adapter is a
decision point, or the part of one, that builds an `Action` from what a
harness knows about a proposed action, takes the harness's deterministic
rules as the ceiling, composes the prediction with `verdict.Decide`,
applies the verdict, records the decision, and sends the person's answer
back. This spec defines the shared mapping, `limen-hook` for harnesses
that call a command at their permission point (Claude Code and Codex),
and the sequence any other harness follows over HTTP. The integration
of [Topos](https://github.com/latere-ai/topos), whose harness embeds the
engine as a `Decider`, is specified in the Topos repository's own specs;
this spec states only what it relies on from Limen.

Every adapter holds the three rules that make Limen safe in front of a
harness: the ceiling comes from the harness's deterministic rules and not
from Limen; a failure to reach Limen is an ask (`FailClosed`); and the
review draw happens once per action, in the adapter, with the probability
recorded.

## Design

### The shared mapping

| Harness fact | Field |
|---|---|
| the person the agent works for | `subject`, their credential's issuer and sub joined by a pipe |
| their organization | `org` |
| the harness's id for the action | `action_id`: the session id and the tool call id, joined by a slash |
| the domain and the tool | `kind` (`tool`), `name` |
| what the tool can do | `effect`, and `reversible` when the harness knows |
| the input | `input`, read by the extractors and never stored |
| the file, ref or host acted on | `target` |
| the machine | `environment`: `host` or `sandbox`, and `credentialed` |
| the agent and its session | `actor` |
| a rule score or a judge's verdict the harness already has | `signals` |
| the harness's deny list, always-confirm list, plan mode | the ceiling, never sent to Limen |

| Verdict | Harness action |
|---|---|
| `allow` | run it |
| `flag` | run it, and put it on the person's review list |
| `ask` | hold it for the person, and send their answer |
| `block` | refuse it, with the harness's own refusal text; a prediction's `reason` is for the person reading the record and is never shown to the agent |

### `limen-hook` for Claude Code

Claude Code runs a command for a `PreToolUse` event with the event as
JSON on standard input and reads a permission decision from standard
output: `allow`, `ask` or `deny`, with a reason. `limen-hook claude-code`
reads the event, builds the action, calls `limend` through `client/`
wrapped in `FailClosed` with a two second timeout, composes with
`verdict.Decide` using its configured ceiling and the prediction's audit
rate, records the decision, and writes `allow` for `allow` and `flag`
(with a review entry), `ask` for `ask`, and `deny` for `block`.

**The ceiling gap.** Claude Code applies its own permission rules beside
the hook and keeps their denies and asks over a hook's allow. The hook
does not see those rules, so a call it records as `allow` may in fact be
held by Claude Code, and the recorded verdict is then not the one
applied. The adapter therefore reads the user's and the project's
permission settings, takes their deny and ask patterns as its ceiling,
and records what Claude Code will do; where it cannot tell, it records
`ask` with probability one, which is never wrong about what a person
saw, only conservative.

**Open question: the answer.** After an `ask`, a `PostToolUse` event for
the same call means the person approved. A denial does not reach a hook
as an event; it appears in the session transcript as the tool call's
result. The adapter reads the transcript at the next hook invocation and
at `Stop` and sends each answer it can establish, approvals and denials
alike, and never sends approvals alone: an adapter that reported only
the approvals it saw would bias every estimate toward allowing. Whether
the transcript format holds for a given Claude Code release is checked
when this spec is validated, and recorded in its Outcome.

**Open question: identifiers.** The hook event's tool call id, where the
release provides one, is the `action_id`'s second half; otherwise a
digest of the tool name, the input and a per-session counter.

### `limen-hook` for Codex

Codex decides through its sandbox and approval policy, and its auto
review can stand in for the person. Its `PreToolUse` hook answers allow
or deny and does not yet support ask; its permission request hook answers
allow or deny. The adapter therefore maps `ask` to leaving the decision
to Codex's own approval prompt and records `ask` only when the active
policy prompts for the command; `allow` and `block` map to allow and
deny. The reviewer's verdict, when the adapter can read it, is a signal
from `classifier/codex-auto-review`. The exact extension point is fixed
when this spec is validated against the Codex release it targets.

### Topos

The Topos harness embeds the engine through its own `Decider` interface
at the point where it scores and decides a tool call, and calls `limend`
over HTTP from hosted runners. It relies on Limen for: `Predict` and the
`FailClosed` fallback; `verdict.Decide` from `latere.ai/x/pkg/verdict`;
`Record` with its own source names (`topos/confirm`,
`topos/progressive`, `topos/plan`); `Answer` from its confirmation
events; and `Cases` with `pending` for its review list. Its shadow and
confirm modes record their own verdicts while Limen predicts, which the
ledger weights correctly because the probability comes from the
recorded decision ([[007-evidence-gates]]).

### Any other harness, over HTTP

```
POST /v1/predictions     the action
  -> on any error or after the timeout, treat the suggestion as ask
  -> verdict.Decide(suggestion, ceiling, audit_rate, u), u drawn once
POST /v1/decisions       the verdict, its review probability, the source,
                         and the action when the prediction failed
POST /v1/answers         when the person answers
GET  /v1/cases?pending=true   for a review list
```

A harness in another language implements `verdict.Decide` exactly: a
`block` ceiling is final with probability zero, a shown verdict has
probability one unless the draw made it so, and an automatic verdict
under review sampling carries the audit rate whether or not it was
drawn.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | For each suggestion and ceiling, `limen-hook claude-code` writes the decision the tables name, and `ask` when `limend` is unreachable, slow, or answers an error | tests feeding recorded hook events to the command against a stub `limend` |
| 2 | The decision `limen-hook` records matches the verdict it writes and the probability `verdict.Decide` returned | the same tests, reading the stub's ledger |
| 3 | A deny or ask pattern in Claude Code's settings is the hook's ceiling | a test with a settings fixture |
| 4 | The hook sends an answer only when both an approval and a denial are observable for the release it targets | review at validation, recorded in the Outcome |
| 5 | A `flag` appears in the subject's pending cases after the hook allows the action | an e2e test against `limend` |
| 6 | A harness that implements `verdict.Decide` in another language agrees with the Go function on a shared table of cases | a published table in `docs/` and the `verdict` package's tests |
