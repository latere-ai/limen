---
title: "Contract: the Go types, the Predictor, Ledger and Admin interfaces, the error table, fail-closed"
status: drafted
track: core
depends_on: [001-architecture.md]
affects: [doc.go, action.go, prediction.go, decision.go, answer.go, profile.go, reliability.go, replay.go, service.go, errors.go]
effort: small
created: 2026-09-28
updated: 2026-10-02
author: changkun
---

# Contract

## Overview

The module root is the contract a decision point codes against. It is
small on purpose: a decision point imports it into its hot path, an
implementation (the engine, the HTTP client, the stub) satisfies its
interfaces, and the HTTP API of [[003-http-api]] is the same contract
over the wire. This spec records what each type means, the rules each
one holds, and why. The code exists; this spec is its record, and a
change to either changes both.

## Design

### Vocabulary

`Verdict` is an alias of `latere.ai/x/pkg/verdict.Verdict`, with the
four constants `Allow`, `Flag`, `Ask` and `Block` re-exported. The order,
`verdict.Least` (an unknown verdict counts as `Block`),
`verdict.OnFailure` and `verdict.Decide` live in the shared package so
that every decision point in the family composes verdicts the same way,
whether or not it uses Limen.

### Types

| Type | Meaning | Rules |
|---|---|---|
| `Key` | names one action in every record: `org`, `subject`, `action_id` | all three non-empty; the decision point keeps `action_id` unique within the org and subject |
| `Action` | one proposed action, as the decision point observes it | `Validate` reports every field at fault in one `*Error` with `invalid_action`: the key; `kind` and `name` non-empty; `effect` one of none, read, write, external; `input` valid JSON when present; `actor.kind`, `target.kind` and `environment.kind` non-empty when the struct is present; `target.sensitivity` in [0, 1]; at most 64 `attributes`, keys non-empty, values strings, booleans or finite numbers; each signal with a source and a valid verdict, a score in [0, 1], or both |
| `Actor`, `Target`, `Environment`, `Signal` | parts of an action | as above |
| `Prediction` | Limen's opinion: `verdict` (allow, ask or block), `approve`, `uncertainty`, `thresholds`, `profile` (the scope that applied), `audit_rate`, `source`, `reason` | `verdict` equals `thresholds.Verdict(approve)`; `source` is `learned/<version>`, or `learned/unavailable` for the `FailClosed` fallback, whose numbers are zero and mean nothing |
| `Thresholds` | `allow_above` and `block_below` in approval probability | derived by `Costs.Thresholds`; `Verdict(p)` is allow above `allow_above`, block below `block_below`, ask between with both ends included, and ask for a probability outside [0, 1] or NaN |
| `Decision` | what the decision point applied: `verdict`, `review_probability`, `source`, and `action` when Limen holds no prediction | `Validate`: the key; a valid verdict; the probability in [0, 1], positive for `flag` and `ask`, below one for `allow` and `block`; a non-empty source; an action, when present, valid and under the same key |
| `Answer` | a person's decision about an action: `approve`, and `by` when relayed | `Validate`: the key. No time and no kind: Limen stamps the time and derives the role |
| `Case`, `RecordedAnswer` | everything Limen holds about one action, without its input | `RecordedAnswer.review` marks the one answer that enters estimates |
| `CaseQuery`, `Page` | one page of a subject's cases, all or only the review queue | `Validate`: org and subject non-empty, `limit` in [0, 500] with 0 meaning 100; `next_cursor` empty on the last page |
| `Scope`, `Profile`, `Costs`, `Certify` | what an organization states | `Profile.Validate`: org non-empty; `audit_rate` in [0, 1]; `certify` zero or with 0 < alpha <= 1 and 0 < delta < 1; then `Costs.Thresholds` |
| `ReliabilityQuery`, `Reliability`, `Estimate`, `Certificate` | an error-rate report | `ReliabilityQuery.Validate`: org, subject and of non-empty; from and to set, to after from |
| `ReplayRecord`, `ReplayOptions`, `ReplayReport` | a recorded action and what Limen would have suggested | `ReplayRecord.Validate`: at set; a valid action; verdict and probability held to each other as in `Decision`; a source |

### The cost arithmetic

At approval probability `p`, allowing costs `(1-p) false_allow`,
blocking costs `p false_block`, and asking costs `ask`. The expected cost
is least for allow when `p > 1 - ask/false_allow` and for block when
`p < ask/false_block`, which is Chow's reject option (paper,
Proposition 2):

```
allow_above = 1 - ask / false_allow
block_below = ask / false_block
```

Limen also requires the ask band to contain one half, the probability it
assigns to an action it knows nothing about, so that no action is
allowed or blocked on no evidence. The band contains one half exactly
when each error costs more than two interruptions,
`false_allow > 2 ask` and `false_block > 2 ask`; this subsumes the band
being non-empty. Costs that break it are refused with `ask_too_costly`
(422), naming the cost at fault; costs that are not positive and finite
with `invalid_profile`.

`DefaultProfile(scope)` applies to a scope with no saved profile: costs
10, 4 and 1 (allow above 0.90, block below 0.25), an audit rate of 0.05,
and `DefaultCertify`, a false-allow rate below 1% at 95% confidence.
`CertificationSize(alpha, delta)` is `ceil(ln(1/delta)/alpha)`, 300 for
the default claim (paper, Proposition 5(b)).

Scope precedence for an action, most specific first: the org with the
action's kind and its agent (`actor.id` when `actor.kind` is `agent`);
the org with the agent; the org with the kind; the org; the default.

### Interfaces

| Interface | Methods | Served by |
|---|---|---|
| `Predictor` | `Predict` | the engine, the HTTP client, the stub |
| `Ledger` | `Record`, `Answer`, `Cases`, `Reliability` | the engine, the HTTP client, the stub |
| `Admin` | `Profile`, `PutProfile`, `Replay`, `Forget` | the engine, the HTTP client, the stub |
| `Service` | all three | the same |

`Predictor` is the hot path and holds one method. `Ledger` is what a
decision point writes to after it decides and what a review surface
reads; a decision point that never predicts uses the ledger alone.
`Admin` is the operator's.

### Rules of the ledger

- **Idempotent prediction.** `Predict` on a key Limen has seen with the
  same action returns the first prediction unchanged. The same key with a
  different action fails with `action_conflict` (409). Limen compares
  digests of the action as sent, input included, so the input itself is
  never kept. A decision point cannot draw a second prediction for an
  action by asking again.
- **One decision.** `Record` on a key that has a decision fails with
  `already_decided` (409). A decision for a key Limen holds no prediction
  for must carry the action; one without it fails with
  `invalid_decision` naming `action`. A decision that carries an action
  for a key Limen has a prediction for must carry the same action, by
  digest, or it fails with `action_conflict`.
- **Answers need a decision.** `Answer` on a key with no recorded
  decision fails with `no_decision` (409).
- **Derived roles.** The first answer to an action whose decision was
  shown (`flag` or `ask`) is its review and enters estimates with weight
  one over the decision's review probability. Every other answer (a
  dispute of an action that ran unseen, a second answer, a change of
  mind) teaches the model and never enters an estimate, because nothing
  fixed the probability with which it was given. The latest answer is
  the label the model learns ([[008-ledger-and-estimation]]).
- **Relays.** `Answer.by` names who answered when the caller relays for
  someone else, and is honored only when the authorizer allows the
  caller `case.answer` on the case; otherwise the answerer is the caller.
  The answer trains the subject's model either way.

### FailClosed

`FailClosed(p, timeout, report)` wraps a `Predictor`. When `Predict`
errors or does not return within `timeout`, the wrapper returns, with a
nil error, a prediction for the action's key whose verdict is `ask` and
whose source is `learned/unavailable`, and hands the error to `report`.
The wrapper waits on a goroutine, so a predictor that ignores its
context cannot hold the decision point past the timeout. Because Limen
may hold no record of the action, the decision point sends the action
with its `Decision`, and the answer still teaches the model.

### Errors

Every failure is an `*Error` with a `Code`, the JSON paths at fault, and
a developer detail. Each code has one HTTP status and one fixed sentence
for the person who reads it, written once in `errors.go`; the sentence
never interpolates, and a test asserts every code has one.

| Code | Status | When |
|---|---|---|
| `invalid_action` | 400 | `Action.Validate` fails |
| `invalid_decision` | 400 | `Decision.Validate` fails, or a decision without an action for a key with no prediction |
| `invalid_answer` | 400 | `Answer.Validate` fails |
| `invalid_profile` | 400 | `Profile.Validate` fails on anything but the band rule |
| `invalid_query` | 400 | `CaseQuery.Validate` or `ReliabilityQuery.Validate` fails |
| `invalid_replay` | 400 | a replay record fails `ReplayRecord.Validate`; the fields name the record's line |
| `ask_too_costly` | 422 | the costs leave one half outside the ask band |
| `action_conflict` | 409 | the key names a different action |
| `already_decided` | 409 | a second decision for a key |
| `no_decision` | 409 | an answer for a key with no decision |
| `unauthenticated` | 401 | no valid bearer token |
| `forbidden` | 403 | the authorizer refused the route's action |
| `body_too_large` | 413 | a body beyond the route's limit |
| `rate_limited` | 429 | the caller exceeded its rate |
| `internal` | 500 | anything else |
| `authorizer_unavailable` | 503 | the authorizer did not answer |
| `unavailable` | 503 | the engine or its store did not answer |

### Compatibility

A JSON field's name never changes, and a new field is optional.
Receivers ignore unknown fields, so a decision point built against a
newer contract still talks to an older server, and a decision point that
does not know a new response field still reads the rest. Known fields
are validated strictly.

### Known limitations

- A decision point can misstate its review probability. The ledger holds
  a decision to its verdict (positive for a shown verdict, below one for
  an unshown one) but cannot check a draw it did not see; the decision
  point is trusted with it, and only a credential the authorizer allows
  `case.decide` may record decisions ([[003-http-api]]).
- A decision point whose host can narrow its verdict afterwards without
  telling it (a host application's permission rules applied after a
  hook) records a verdict that is not the one applied
  ([[005-adapters]]).

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | `Costs.Thresholds` gives allow above 0.65 and block below 0.35 for the symmetric costs, 0.90 and 0.25 for 10, 4 and 1, and the same for any scaling of them | `TestCostsThresholds` |
| 2 | Costs with either error at most twice the cost of asking fail with `ask_too_costly` naming that cost, and every accepted profile puts one half in the ask band | `TestCostsRefusesDecidingOnNoEvidence`, `TestCostsThresholds` |
| 3 | `Thresholds.Verdict` is the minimum-expected-cost verdict on a grid of probabilities | `TestThresholdsVerdictIsBayesOptimal` |
| 4 | `CertificationSize(0.01, 0.05)` is 300 and invalid parameters are refused | `TestCertificationSize` |
| 5 | Each `Validate` reports every field at fault in one error, and none for a valid value | a table test per type |
| 6 | Every pair `verdict.Decide` returns is a valid `Decision` | `TestDecideProducesValidDecisions` |
| 7 | Every code in `Codes()` has a sentence and a status, and `CodeOf` reads a wrapped `*Error` | `TestErrorTable` |
| 8 | `FailClosed` returns an `ask` from `learned/unavailable` for the action's key on an error and on a timeout, even when the wrapped predictor ignores its context, and passes a prediction through unchanged | `service_test.go` |
| 9 | The root package's coverage is at or above 90% | the `suite` gate |
| 10 | The ledger rules above hold for every implementation | [[006-conformance]] |
