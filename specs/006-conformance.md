---
title: "Conformance suite: the invariants every gate must hold, as an importable test package"
status: drafted
track: core
depends_on:
  - specs/002-contract.md
  - specs/003-http-api.md
affects: [conformance/, stub/, engine/, client/, cmd/limend/]
effort: medium
created: 2026-09-28
updated: 2026-09-28
author: changkun
---

# Conformance suite

## Overview

The invariants of [[001-architecture]] and the contract of
[[002-contract]] are worth something only if they are checked on every
implementation, not only on the one in this tree. `conformance/` is an
importable Go test package that, given a `Gate` (and, when present, its
`Reviews` and `Admin`) and a way to control the implementation's audit
draws and profiles, drives each invariant as a named case and reports
which held, which failed, and which were skipped and why.

Four parties run it: the engine in process ([[004-engine]]); `client/`
against a running `limend` ([[003-http-api]]); `stub/`, so a stub other
projects use in their tests cannot drift from the contract it stands in
for; and any alternative implementation, for which passing the suite is
the whole claim to serve Limen's contract.

The suite is black box. It holds no import of an implementation's
internals and reads no store; anything that needs a hand inside is not a
case here.

## Design

### Driving an implementation

The suite takes a `Target`: the three interfaces, a function that saves
a profile for a fresh organization, and an audit control that forces the
next draw for a call id to sample or to skip. The audit control is the
one hook into an implementation the suite needs, because an audit is a
random event and a case must provoke both outcomes. An implementation
that cannot offer it runs the audit cases as skipped, with that reason in
the report, and does not pass.

Every case runs in its own organization and subject, so two runs against
one installation touch none of each other's records, and every case
calls `Forget` on its subject when it ends.

### The cases

| Group | Case | Asserts |
|---|---|---|
| ceiling | `CeilingNeverExceeded` | over a stream with every ceiling, no verdict is more permissive than its ceiling |
| ceiling | `CeilingBlockIsBlock` | a `block` ceiling yields `block` whatever the posterior |
| ceiling | `CeilingAskDrawsNoAudit` | a `flag`, `ask` or `block` ceiling carries no audit |
| fail-closed | `ErrorIsAsk` | the target wrapped in `FailClosed` and sent an invalid action returns `ask` narrowed by the ceiling, source `learned/unavailable`, and no error |
| fail-closed | `TimeoutIsAsk` | the target wrapped in `FailClosed` with a one nanosecond timeout returns the same, within a margin |
| fail-closed | `InvalidIsAnError` | the unwrapped target answers an invalid action with `invalid_action` naming every field at fault, and never with an assessment |
| arithmetic | `ScoreIsOneMinusApprove` | every assessment the implementation produces, other than a `learned/unavailable` fallback, has `score = 1 - approve` within 1e-12 |
| arithmetic | `VerdictFollowsThresholds` | an unaudited assessment's verdict is `Least(thresholds.Verdict(score), ceiling)` |
| arithmetic | `ThresholdsFromCosts` | an assessment's thresholds equal `Costs.Thresholds` of the profile it names |
| audit | `AuditedAllowIsFlag` | a forced draw on an allow yields `flag` with `audit.of = allow` |
| audit | `AuditedBlockIsAsk` | a forced draw on a block yields `ask` with `audit.of = block` |
| audit | `AuditProbabilityInRange` | an audit's probability is in (0, 1] and equals the profile's audit rate at assessment time |
| audit | `AuditProbabilityFixed` | changing the profile's audit rate after an assessment does not change that assessment's probability |
| idempotency | `RepeatReturnsFirst` | a repeat of subject and call id returns the first assessment, audit included, also when the repeat's body differs |
| idempotency | `NoSecondDraw` | repeating an unaudited call with a forced sample does not audit it |
| answers | `OneDecision` | a second decision is `already_answered` |
| answers | `ContestAccepted` | a contest on an assessment with no decision is accepted, and a second contest is `already_answered` |
| answers | `UnknownAssessment` | an answer to an id never returned is `unknown_assessment` |
| estimates | `ContestNeverEstimates` | a reliability report is identical with and without a contest on an allowed call |
| estimates | `NotEstimableIsNull` | with audit rate zero and no answered allow, `false_allow.value` is `null` |
| estimates | `UnansweredCounted` | a forced audit that is never answered appears in the report's `unanswered`, and changes neither `labels` nor `audits` |
| estimates | `HorvitzThompson` | a scripted window of allows, audits and denials yields the Horvitz-Thompson value computed by hand |
| profiles | `EmptyAskBand` | saving costs with no ask band is `empty_ask_band`, and nothing is stored |
| profiles | `DefaultCertify` | a profile saved without `certify` is stored and reported with alpha 0.01 and delta 0.05, needing 300 clean audits |
| profiles | `AgentOverridesOrg` | an action naming an agent with a profile uses that profile |
| errors | `EveryCodeHasASentence` | every code the implementation answers is in `Codes()`, with the status and sentence of the table |
| forget | `ForgetRemovesEverything` | after `Forget`, the subject's records are gone, a read is `unknown_assessment`, and the next assessment is the prior's |

A case names the invariant it proves, and a test in this package holds
the case list equal to the invariants of [[001-architecture]] and the
criteria of [[002-contract]] that are visible through the interfaces, so
an invariant added there without a case fails the build here.

### The stub

`stub/` is a `Gate`, `Reviews` and `Admin` held in memory that returns a
configured verdict, derives thresholds with `Costs.Thresholds`, and
honours the audit control. It exists for other projects' test tiers,
which need a Limen that behaves by the contract without a model. It
passes this suite; that is what keeps it honest.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | Every case in the table exists, and a skipped case carries its reason in the report | `TestEveryCaseExists`, `TestSkipsCarryAReason` |
| 2 | The case list covers every invariant of [[001-architecture]] visible through the interfaces | `TestEveryInvariantHasACase`, reading `specs/001-architecture.md` |
| 3 | The engine, `stub/`, and `client/` against `limend` each pass every case | the unit tier for the first two, the conformance tier for the third |
| 4 | A deliberately broken gate (one that exceeds the ceiling, one that fails open, one that redraws on repeat) fails exactly the matching case | `TestSuiteCatchesEachBreak` with three broken fakes |
| 5 | A run leaves no record behind, and two concurrent runs share none | `TestRunCleansUp`, `TestConcurrentRuns` |
| 6 | The package imports nothing from this repository's internal packages | the `depcheck` gate over `conformance/` |
