---
title: "Conformance suite and stub: the invariants every cribrum.Service holds, and an in-memory service for decision points' tests"
status: drafted
track: core
depends_on: [001-architecture.md, 002-contract.md, 008-ledger-and-estimation.md]
affects: [conformance/, stub/]
effort: medium
created: 2026-09-28
updated: 2026-10-02
author: changkun
---

# Conformance suite and stub

## Overview

Three implementations serve `cribrum.Service`: the engine, the HTTP
client against `cribrumd`, and the stub. A decision point must be able to
swap one for another without its behavior changing in any way the
contract promises. `conformance/` is an importable Go test package that
holds every implementation to those promises; `stub/` is an in-memory
implementation with a predictor the test chooses, so that a decision
point's own tests (Topos's, a hook's) exercise the real ledger rules
without the engine.

## Design

### The suite

```go
type Target struct {
	New     func(t *testing.T) cribrum.Service
	Steered func(t *testing.T, predict func(cribrum.Action) (approve, uncertainty float64)) cribrum.Service
}

func Run(t *testing.T, target Target)
```

`Run` calls `New` for a fresh service per case and runs each case as a
subtest. A case relies only on the contract, never on how a model
learns: it controls predictions through the one behavior every
implementation shares, that a subject with no answers predicts one half,
and it builds ledgers directly through decisions that carry their
actions. The few cases that need chosen approvals use `Steered`, a
constructor whose predictions come from the function it is given; a
learning engine leaves it nil and those cases skip. Report windows span the case's own run, because the service,
not the case, stamps time.

| Group | Case | Asserts |
|---|---|---|
| predict | `UnknownIsAsk` | a fresh subject predicts `approve` 0.5 and `ask`, under the default profile and under a saved profile at each end of the accepted costs |
| predict | `VerdictFollowsThresholds` | every prediction's `verdict` is `thresholds.Verdict(approve)` |
| predict | `ThresholdsFromProfile` | a prediction's `thresholds` and `audit_rate` are those of the effective profile, and `profile` names its scope |
| predict | `Idempotent` | a repeated prediction for a key and the same action is identical to the first |
| predict | `ActionConflict` | the same key with a different action is `action_conflict` |
| predict | `InvalidIsAnError` | an invalid action is `invalid_action` naming every field at fault, never a prediction |
| decide | `OneDecision` | a second decision for a key is `already_decided` |
| decide | `ActionRequiredWithoutPrediction` | a decision for an unseen key without an action is `invalid_decision` naming `action`; with the action it is recorded |
| decide | `DecisionActionMustMatch` | a decision whose action differs from the predicted one is `action_conflict` |
| answer | `NoDecision` | an answer for a key with no decision is `no_decision` |
| answer | `ReviewIsFirstShownAnswer` | the first answer to a `flag` or `ask` case is its review; a second answer is not |
| answer | `UnshownAnswerNeverReviews` | an answer to an `allow` or `block` case is never a review and changes no estimate |
| answer | `LatestAnswerShown` | `Cases` lists every answer in order with its role |
| cases | `PendingQueue` | `pending` lists shown cases without a review, oldest first, and drops a case once reviewed |
| cases | `InputNeverReturned` | no case returned by `Cases` carries the action's input |
| cases | `Pagination` | pages of `limit` cover every case once, in order, and the last has no cursor |
| reliability | `NotEstimableIsNull` | a source whose allows all had probability zero reports `value: null` and `covered: 0` |
| reliability | `WeightsFromDecisions` | for a scripted ledger of allows at an audit rate with some reviews denied, the value equals denied reviews over the rate over the allows |
| reliability | `UnansweredCounted` | shown cases without reviews are counted in `unanswered` |
| reliability | `PerSource` | `learned`, `applied`, a decision source and a signal source each report over their own allows |
| reliability | `Certificate` | `clean` counts the latest audited allows to the first denial; `needed` is `CertificationSize` of the profile's claim |
| profiles | `Precedence` | for an action, the profile of kind and agent beats agent, agent beats kind, kind beats org, and org beats the default |
| profiles | `Defaults` | `Profile` of an unsaved scope is the default with `default: true`; `PutProfile` fills a zero `certify` and derives the thresholds |
| profiles | `AskTooCostly` | costs with an error at most twice the cost of asking are `ask_too_costly` |
| replay | `ReplayReport` | a replay reports records, answered records, would-be verdicts, the allowed-denied and blocked-approved counts, and an estimate with its coverage, and keeps nothing without `keep` |
| forget | `ForgetPerOrg` | after `Forget(org, subject)` the subject has no cases, reviews or reports in that org and is untouched in another |
| fail-closed | `ErrorIsAsk`, `TimeoutIsAsk` | the target wrapped in `FailClosed` with a failing or a hanging inner predictor returns `ask` from `learned/unavailable` for the key |

### The stub

```go
func New(o stub.Options) *stub.Service
```

`stub.Service` holds the ledger in memory with the same rules as the
engine, computes reports with `estimate/`, and predicts with
`Options.Predict func(cribrum.Action) (approve, uncertainty float64)`, by
default one half with uncertainty one for every action, which every
profile Cribrum accepts turns into an ask. It stamps time with `Options.Clock`, by default
the wall clock. It satisfies `cribrum.Service` and passes `Run`. A decision
point's tests use it to script approvals and read back the decisions and
answers it recorded.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | `conformance.Run` holds every case above, and each case fails against a service seeded with the matching defect | `conformance/`'s own tests, with a deliberately broken service per case |
| 2 | `stub.Service` passes `conformance.Run` | `stub/`'s tests |
| 3 | `engine.Engine` passes `conformance.Run` with each store, and `client/` against `cribrumd` | [[004-engine]], [[003-http-api]] |
| 4 | The stub and the engine report identical estimates for an identical ledger | [[008-ledger-and-estimation]] |
| 5 | `conformance/` and `stub/` each have coverage at or above 90% | the `suite` gate |
