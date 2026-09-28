---
title: "Contract: the Go types, the gate interfaces, the verdict order, the error table, fail-closed"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
affects: [doc.go, verdict.go, action.go, assessment.go, answer.go, profile.go, reliability.go, gate.go, errors.go, authorizer/]
effort: small
created: 2026-09-28
updated: 2026-09-28
author: changkun
---

# Contract

## Overview

The root package `latere.ai/x/limen` is what a harness imports: the types
of one proposed call and of Limen's answer, the three interfaces an
implementation serves, the error table, and the handful of pure
functions whose arithmetic every implementation must share. It holds no
engine, no network client, and no storage, and it imports the standard
library only ([[001-architecture]], invariant 9), so a harness pays for
Limen's contract with nothing but the contract.

This spec records the contract as it is in the tree and the reasons for
its shape. [[003-http-api]] is the same contract on the wire, and
[[006-conformance]] is the suite that checks an implementation against
both.

## Design

### The types

| Type | What it is | The rules it carries |
|---|---|---|
| `Action` | one proposed call, as the harness observes it before it runs | `Validate` names every field at fault in one `*Error` with `invalid_action`: subject, org, call_id and tool non-empty; effect one of none, read, write, external; input valid JSON when present; target kind non-empty and sensitivity in [0, 1]; machine kind host or sandbox; each signal with a source and a verdict (allow or block), a score in [0, 1], or both; ceiling a valid verdict; at set |
| `Target`, `Machine`, `Signal` | parts of an action | as above |
| `Assessment` | Limen's answer for one action | `score = 1 - approve`, except for a `learned/unavailable` assessment, which has no id and whose numbers are zero and mean nothing; the verdict is `Thresholds.Verdict(score)` narrowed by the ceiling, except an audited call, which is `flag` for an audited allow and `ask` for an audited block; `audit` is present only when the call was sampled, and never under a `flag`, `ask` or `block` ceiling, which already decides whether the person sees the call; `source` is `learned/<version>` or `learned/unavailable` |
| `Thresholds` | `ask_at` and `block_at` in score units | derived by `Costs.Thresholds`, never tuned; `Verdict(score)` is allow below `ask_at`, block above `block_at`, ask between with both ends included, and ask for a score outside [0, 1] or NaN |
| `Audit` | the automatic verdict under review and the probability it was sampled with | the probability is in (0, 1] and is fixed at assessment |
| `Answer` | a person's decision, or a later contest | `Validate`: kind empty (a decision) or `contest`; at set. Who answered is the credential's subject, never a field |
| `Profile`, `Costs`, `Certify` | what an organization states | `Validate`: id non-empty, audit rate in [0, 1], a certification claim `CertificationSize` accepts when one is stated, and costs with a non-empty ask band, else `empty_ask_band`. `certify` is optional: a zero value takes `DefaultCertify`, a false-allow rate below 1% at 95% confidence, when the profile is saved |
| `ReliabilityQuery`, `Reliability`, `Estimate`, `Certificate` | an error-rate report for one subject, one source, one window | `Estimate.Value` is `nil` when nothing in the window has a known probability; `Interval` accompanies `Value`; `Unanswered` counts calls sampled for review that nobody answered, which the estimate treats as missing at random, so a count large next to `Audits` weakens it |
| `Record` | an assessment with its action, its answer, and who answered (`answered_by`, set by Limen) | the unit a review surface reads |
| `ListQuery`, `Page` | one page of a subject's records, all or only those awaiting an answer | `ListQuery.Validate`: subject non-empty, limit in [0, 500] with 0 meaning 100; `Page.next_cursor` is empty on the last page |
| `ReplayRecord`, `ReplayReport` | a recorded call and what Limen would have done on a history | a record's audit fixes its answer probability; without one, an ask, or a flag the deterministic layers forced, was answered with probability one, and an allow or a block with probability zero |

A field's JSON name is its contract. A name never changes; a new field
is optional; removing or retyping a field is a breaking change that the
CHANGELOG names. `api/openapi.yaml` is held to these tags by a test
([[003-http-api]]).

### The verdict order

`allow < flag < ask < block`, from most to least permissive. `Least`
returns the least permissive of its arguments, treats an invalid verdict
as `block`, and returns `block` for no arguments, so a malformed input
can only narrow a decision. Every composition in this repository goes
through `Least`: the ceiling, `FailClosed`, and the harness's own layers
([[005-adapters]]).

### Thresholds from costs

With `p` the probability of approval, allowing costs `(1 - p) c_FA`,
blocking costs `p c_FB`, and asking costs `c_ask`. Allow is the
cheapest action exactly when `p > 1 - c_ask / c_FA` and block exactly
when `p < c_ask / c_FB`, which is Chow's reject option (Proposition 2 of
the paper, `Chow.decide_optimal` in its Lean development). In score
units, `score = 1 - p`:

```
ask_at   = c_ask / c_FA
block_at = 1 - c_ask / c_FB
```

The band between them is non-empty exactly when `c_ask (c_FA + c_FB) <
c_FA c_FB` (`Chow.band_nonempty_iff`). When it is empty an automatic
verdict is never worse than asking, and `Costs.Thresholds` refuses the
costs with `empty_ask_band` rather than return thresholds that would
never ask: a profile that means "never ask" should say so with a
ceiling, not by accident of arithmetic. The two profiles the paper
evaluates give `ask_at 0.35, block_at 0.65` (symmetric, `c_FA = c_FB =
c_ask / 0.35`) and `ask_at 0.10, block_at 0.75` (`c_FA = 10, c_FB = 4,
c_ask = 1`).

### Certification size

`CertificationSize(alpha, delta)` is `ceil(ln(1/delta) / alpha)`: if the
false-allow rate were at least `alpha`, that many independent audits would
all be clean with probability at most `delta` (Proposition 5(b),
`Audit.certify_sample_size`). It is 300 for `alpha = 0.01, delta = 0.05`.
It refuses parameters outside `0 < alpha <= 1, 0 < delta < 1`.

### The interfaces

| Interface | Methods | Served by |
|---|---|---|
| `Gate` | `Assess`, `Answer`, `Reliability` | the engine in process, the HTTP client, the stub |
| `Reviews` | `Record`, `List` | the engine, the HTTP client |
| `Admin` | `Profile`, `PutProfile`, `Replay`, `Forget` | the engine, the HTTP client |

`Gate` is the hot path and is kept to three methods so a harness can
wrap it, fake it, and reason about it. A review surface and an operator
need the other two, and a harness that only gates calls never imports
them.

`Assess` is idempotent on the action's subject and call id. The audit
sample is drawn once, at the first assessment, and returned unchanged
thereafter. Were a repeat to draw again, a caller could ask until the
draw missed, and the audited calls would no longer be a random sample of
the automatic ones, which is what makes an error estimate unbiased
(Proposition 8, `Judge.ht_unbiased`).

The audit probability is never read from a caller for the same reason:
the estimate weights each answer by one over the probability the call
had of being answered, and a probability the caller chose is a weight
the caller chose. `Assessment.Audit` is written by the implementation;
`Answer` has no probability field; a `ReplayRecord`'s audit is the
recording harness's own draw, read from its log.

A contest trains the model and never enters an estimate. Nothing fixes
the probability with which a call is later disputed, so a contested
call has no weight to count with.

### FailClosed

`FailClosed(g, timeout, report)` wraps a `Gate` so an assessment never
fails open. When `g.Assess` errors or does not return within `timeout`,
the wrapper returns, with a nil error, an assessment whose verdict is
`Least(ask, ceiling)`, whose source is `learned/unavailable`, and whose
reason says the call waits for a person (or, under a `block` ceiling,
that the deterministic rules block it), and hands the error to `report`.
A verdict `g` does return is narrowed to the ceiling, so a faulty gate
cannot exceed it either; when narrowing changes the verdict the audit is
dropped too, because the ceiling, not the sample, then decides whether the
person sees the call. `Answer` and `Reliability` run under the same
timeout and return their errors unchanged, because neither decides
whether a call runs. The wrapper waits for `g` on a goroutine, so a gate
that ignores its context still cannot hold the harness past `timeout`.

The fallback assessment has no id, because no gate recorded it, so the
person's answer to that call cannot be sent and is not learned. This is
a known limitation and an open question for validation: a harness keeps
the call in its own log and may include it in a later replay, where its
answer counts, but no live model sees it. Recording fallbacks in the
harness and forwarding them when the gate returns would close the gap at
the cost of a second, harness-side store.

### The error table

Each `Code` has one HTTP status and one fixed sentence, both in
`errors.go`'s table and nowhere else. The sentence is for the person
reading a response and never names a field path, a value, or a package;
what varies travels in `Error.Fields` and `Error.Detail`, which are the
developer's (`details.fields` and `details.detail` on the wire).
`Error.Error()` returns the developer string, and `CodeOf` reads the code
of the first `*Error` in a chain, `internal` when there is none.

| Code | Status | When |
|---|---|---|
| `invalid_action` | 400 | `Action.Validate` fails, or an assess or replay body cannot be read |
| `invalid_answer` | 400 | `Answer.Validate` fails |
| `invalid_profile` | 400 | `Profile.Validate` fails on a field other than the ask band |
| `invalid_query` | 400 | `ReliabilityQuery.Validate` fails, or a list parameter is out of range |
| `empty_ask_band` | 422 | the costs leave no ask band |
| `unknown_assessment` | 404 | an answer or a read names no assessment |
| `unknown_profile` | 404 | no profile applies to the action's organization |
| `already_answered` | 409 | a second decision, or a second contest, for one assessment |
| `unauthenticated` | 401 | no valid bearer token |
| `forbidden` | 403 | the authorizer refused the route's action |
| `rate_limited` | 429 | the caller exceeded its rate |
| `internal` | 500 | anything else |
| `authorizer_unavailable` | 503 | the authorizer did not answer |
| `unavailable` | 503 | the engine or its store did not answer; the harness holds the call |

`Codes()` lists every code, and a test asserts each has a non-empty
sentence and a status, so a code cannot ship without either.

### The authorizer vocabulary

`authorizer.Vocabulary()` is Limen's table for `latere.ai/x/pkg/authz`:
nine actions over five resource kinds, one action per route of
[[003-http-api]]. A constant never changes its string and never
disappears; a new action is a row in [[003-http-api]] first and a
constant second.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | `Least` orders allow, flag, ask, block, returns `block` for an invalid verdict and for no arguments | a table test over every pair and the two edge cases |
| 2 | `Costs.Thresholds` gives `0.35, 0.65` and `0.10, 0.75` for the paper's two profiles, refuses a non-positive, infinite or NaN cost with `invalid_profile` naming each field, and an empty band with `empty_ask_band` | a table test including the boundary `c_ask (c_FA + c_FB) = c_FA c_FB` |
| 3 | `Thresholds.Verdict` is allow strictly below `ask_at`, block strictly above `block_at`, ask on both thresholds and for a score outside [0, 1] or NaN; `Band` returns `1 - block_at, 1 - ask_at` | a table test |
| 4 | `CertificationSize(0.01, 0.05)` is 300 and invalid parameters are refused | a table test |
| 5 | Each `Validate` reports every field at fault in one error, and none for a valid value | a table test per type, one case per rule |
| 6 | Every code in `Codes()` has a sentence and a status, and `CodeOf` reads a wrapped `*Error` | a test over the table |
| 7 | `FailClosed` returns `ask` narrowed by the ceiling on an error and on a timeout, even when the wrapped gate ignores its context; narrows a returned verdict to the ceiling and drops its audit when that changes the verdict; calls `report`; passes `Answer` and `Reliability` errors through | tests with fake gates that fail, hang, and exceed the ceiling |
| 8 | `authorizer.Vocabulary()` builds, names nine actions, and every action's kind has a label | a test in `authorizer/` |
| 9 | The root package's coverage is at or above 90% | the `suite` gate |
