---
title: "Conformance suite and stub: the invariants every limen.Service holds, and an in-memory service for decision points' tests"
status: complete
track: core
depends_on: [001-architecture.md, 002-contract.md, 008-ledger-and-estimation.md]
affects: [conformance/, stub/]
effort: medium
created: 2026-09-28
updated: 2026-11-01
author: changkun
---

# Conformance suite and stub

## Overview

Three implementations serve `limen.Service`: the engine, the HTTP
client against `limend`, and the stub. A decision point must be able to
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
	New     func(t *testing.T) limen.Service
	Steered func(t *testing.T, predict func(limen.Action) (approve, uncertainty float64)) limen.Service
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
| explanations | `Explanations` | an explanation names its key and model, repeats the prediction by the model that made it, is the thresholds' verdict with an ask's kind, and an unknown key is `no_case` ([[009-explanations]], added 2026-11-01) |
| explanations | `LearnedEntries` | one entry per label of the answered cases, most answered first, with their counts and the thresholds of the profile that applies |
| explanations | `Previews` | a preview's counts are the recorded predictions in its window re-thresholded under its costs; invalid costs are refused as a profile's are |
| forget | `ForgetPerOrg` | after `Forget(org, subject)` the subject has no cases, reviews or reports in that org and is untouched in another |
| fail-closed | `ErrorIsAsk`, `TimeoutIsAsk` | the target wrapped in `FailClosed` with a failing or a hanging inner predictor returns `ask` from `learned/unavailable` for the key |

### The stub

```go
func New(o stub.Options) *stub.Service
```

`stub.Service` holds the ledger in memory with the same rules as the
engine, computes reports with `estimate/`, and predicts with
`Options.Predict func(limen.Action) (approve, uncertainty float64)`, by
default one half with uncertainty one for every action, which every
profile Limen accepts turns into an ask. It stamps time with `Options.Clock`, by default
the wall clock. It satisfies `limen.Service` and passes `Run`. A decision
point's tests use it to script approvals and read back the decisions and
answers it recorded.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | `conformance.Run` holds every case above, and each case fails against a service seeded with the matching defect | `conformance/`'s own tests, with a deliberately broken service per case |
| 2 | `stub.Service` passes `conformance.Run` | `stub/`'s tests |
| 3 | `engine.Engine` passes `conformance.Run` with each store, and `client/` against `limend` | [[004-engine]], [[003-http-api]] |
| 4 | The stub and the engine report identical estimates for an identical ledger | [[008-ledger-and-estimation]] |
| 5 | `conformance/` and `stub/` each have coverage at or above 90% | the `suite` gate |

## Outcome

Recorded on 2026-11-01, after v0.3.0. The suite and the stub are built
and used: the engine with both stores, the HTTP client and the stub pass
`conformance.Run`. Every case of the table runs, and each case fails
against a stub seeded with its defect. Every criterion has a passing
test in the tree.

### What was built

| Piece | Where |
|---|---|
| the suite, `Run(t, Target)` with `Steered` | `conformance/` |
| the stub, `New(Options)` with `Predict` and `Clock` | `stub/` |
| the runs of the suite | `TestRunAgainstTheStub`, `TestRunUnsteered` (`conformance/`); `TestConformance` (`stub/`, `engine/`); `TestConformanceInline` and `TestEngineConformance` (the engine with each store); `TestConformance` in `cmd/limend` (the client against a started `limend`); `TestConformanceEngine` and `TestConformanceStub` (`client/`) |

| # | Proof in the tree |
|---|---|
| 1 | `TestDefects`: for each case, in the order of the suite, a child run against a stub with that case's defect must fail on that case (`conformance/defects_test.go`) |
| 2 | `TestConformance` in `stub/`, `TestRunAgainstTheStub` |
| 3 | the runs above |
| 4 | `TestEngineAndStubReportAlike` (`engine/parity_test.go`): one scripted history goes to both, and every report is the same |
| 5 | the `suite` gate, at 90% or more for each package |

### What diverges from the design as written

The cases have other names in `conformance/conformance.go`, and some
are merged:

| Case in the table | Case in the code |
|---|---|
| `UnknownIsAsk` | `UnknownIsAnAsk` |
| `VerdictFollowsThresholds`, `ThresholdsFromProfile` | `PredictionFollowsTheProfile` |
| `Idempotent`, `ActionConflict` | `IdempotentPredict` |
| `InvalidIsAnError` | `Validation` |
| `ActionRequiredWithoutPrediction`, `DecisionActionMustMatch` | `DecisionNeedsAnAction` |
| `ReviewIsFirstShownAnswer`, `UnshownAnswerNeverReviews`, `LatestAnswerShown` | `AnswerRoles` |
| `PendingQueue`, `Pagination` | `PendingAndPages` |
| `InputNeverReturned` | `CasesNeverReturnInput` |
| `NotEstimableIsNull` | `NullIsNotZero` |
| `WeightsFromDecisions` | `ReviewsWeightedByProbability` |
| `UnansweredCounted` | `Unanswered` |
| `PerSource` | `Sources`, `Learned` |
| `Precedence`, `Defaults` | `ProfilePrecedence`, `ProfileDefaults` |
| `ReplayReport` | `Replay` |
| `ForgetPerOrg` | `ForgetIsScoped` |
| `ErrorIsAsk`, `TimeoutIsAsk` | `FailClosed` |
| `Previews` | `Previews`, `PreviewErrors` |
| `Certificate` | `Certificate` |
| none | `SteeredVerdicts`, `LaterAnswersNeverEstimate`, `ExplanationsOfSteeredVerdicts` |

| What it said | What was built | Why |
|---|---|---|
| `InvalidIsAnError` names every field at fault | `Validation` checks one named field per error | the contract tests hold the full lists ([[002-contract]]) |
| `ReplayReport` checks the counts and the estimate | `Replay` checks the records and that nothing is kept without `keep` | the stub's `TestReplay` holds the counts for the stub alone |
| `ForgetPerOrg` checks cases, reviews and reports | `ForgetIsScoped` checks the cases and the reviews in the reports | the engine's `TestForgetDropsTheModel` holds the model |
| the fail-closed cases wrap the target | `FailClosed` wraps a failing and a hanging predictor, then records into the target | the target is not what fails |
| a decision point's own tests use the stub (Topos's, a hook's) | Topos tests against its own fake service over HTTP; no hook exists | Topos does not import this module ([[005-adapters]]) |

### What this leaves open

Nothing of this spec. A new rule of the contract adds its case and the
defect that the case catches.
