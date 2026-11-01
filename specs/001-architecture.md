---
title: "Architecture: the shared vocabulary, the ledger, the predictor, and the decision point that composes them"
status: complete
track: core
depends_on: []
affects: [doc.go, action.go, prediction.go, decision.go, answer.go, profile.go, reliability.go, replay.go, service.go, errors.go, explanation.go, authorizer/, estimate/, stub/, conformance/, engine/, client/, server/, cmd/limend/, cmd/limen/, importer/topos/, api/, docs/]
effort: medium
created: 2026-09-28
updated: 2026-11-01
author: changkun
---

# Architecture

## Overview

Automated actors propose actions that something must let run, hold for a
person, or refuse: an agent's tool call, a sandbox's network request, a
push to a protected branch, a payment, a public share link, a deploy.
The code that makes that choice is a decision point. Most decision
points decide with a fixed list or a fixed score threshold, answer
uncertainty with a refusal, and cannot say how often they are wrong. A
refusal does not pause the actor: an agent reads it as a result and
routes around it. A fixed threshold does not learn that a person approves
one kind of action on sight and wants to see another every time, or
that their tolerance changes when they move to an unfamiliar system. And
an automatic allow never receives an answer, so its error rate is never
observed.

Limen is the oversight layer for those decisions. It is three orthogonal
parts, each usable without the parts that build on it:

1. **A shared vocabulary**, `latere.ai/x/pkg/verdict`: the four verdicts
   in their order, composition that can only narrow, the rule that a
   failure is never more permissive than an ask, and `Decide`, which
   applies a ceiling and random review sampling to a suggested verdict
   and returns the probability that a person sees the action. Every
   decision point in the family composes its layers with it.
2. **A ledger**, which keeps every decision a decision point made, with
   that probability, and every answer a person gave, and reports how
   often any source of decisions allows what the person would deny: a
   rule, an LLM judge whose verdict arrived as a signal, the verdicts
   applied, or Limen's own predictions. A decision point that decides by
   rules alone can use the ledger to learn how often its rules, and the
   judge it consults, are wrong.
3. **A predictor**, which learns each person's tolerance from the
   ledger's answers and suggests a verdict from the costs their
   organization states.

The decision point owns the decision. It asks the predictor, composes
the suggestion with its own deterministic layers and its review draw,
applies the result, and records it in the ledger. That division is the
architecture's main decision: whoever applies a verdict is the only
party that knows the probability with which a person sees the action,
so the probability is recorded by the decision point and never inferred
by Limen.

The design follows the research in *Learning When to Ask: Trust
Calibration for Agentic Tool Use, from Known Tools to Opaque LLM Judges*
([arXiv:2605.19151](https://arxiv.org/abs/2605.19151)), whose
propositions are machine-checked in Lean 4 at
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib).

## Design

### The flow of one action

```mermaid
sequenceDiagram
  participant A as actor (an agent)
  participant D as decision point
  participant P as Limen predictor
  participant L as Limen ledger
  participant H as person
  A->>D: proposes an action
  D->>D: ceiling from deterministic layers
  D->>P: Predict(action)
  P-->>D: Prediction (approve, suggestion, thresholds, audit rate)
  D->>D: verdict.Decide(suggestion, ceiling, rate, u)
  D->>L: Record(decision, review probability)
  alt ask
    D->>H: holds the action
    H-->>D: approve or deny
  else flag
    D->>A: runs it
    D->>H: queues it for review
  else allow or block
    D->>A: runs or refuses it
  end
  D->>L: Answer(approve) when the person answers
  L->>P: the answer is a label
```

1. The decision point evaluates its deterministic layers (deny lists,
   always-confirm lists, modes, hooks) and takes their least permissive
   verdict as the ceiling. A ceiling of `block` is a deterministic deny,
   final whatever Limen predicts.
2. It calls `Predictor.Predict` with the `Action`, wrapped in
   `FailClosed`, so an error or a timeout is a prediction of `ask`.
3. It calls `verdict.Decide(prediction.Verdict, ceiling,
   prediction.AuditRate, u)` with a uniform `u` drawn once for this
   action, and applies the verdict.
4. It calls `Ledger.Record` with the verdict, the review probability and
   its own source name. It records every decision, not only those a
   person sees, because a source's allows are the denominator of its
   error rate.
5. When the person answers, it calls `Ledger.Answer`. The ledger derives
   what the answer counts for from the recorded decision
   ([[008-ledger-and-estimation]]).

### The action

An `Action` is generalized beyond tool calls. `Kind` is the domain and
`Name` the operation within it:

| Domain | Kind | Name, for example | Typical decision point |
|---|---|---|---|
| agent tool calls | `tool` | `bash`, `write`, `mcp__github__merge_pull_request` | an agent harness |
| network from a sandbox | `egress` | `https`, `ssh` | a sandbox's egress gateway |
| repository writes | `repo` | `ref.update`, `ref.delete` | a git host |
| storage | `storage` | `share.create`, `link.create` | an object store |
| spending | `spend` | `charge`, `budget.raise` | a model gateway or a payments service |
| deploys | `deploy` | `create`, `rollback` | an application platform |

The rest of the action is what the decision point can observe before the
action runs: who proposed it (`Actor`), its widest `Effect` (none, read,
write, external), whether it is `Reversible`, its `Input` (read by
deterministic extractors only, never stored), its `Target` and the
target's sensitivity, the `Environment` it runs in, domain
`Attributes`, and `Signals`, the other opinions the decision point
already has. Limen keeps time itself ([[004-engine]]).

### Packages

| Path | What it holds | Spec |
|---|---|---|
| module root | the contract: types, interfaces, error codes, the cost arithmetic, `FailClosed`, and the `Explainer` with its views | [[002-contract]], [[009-explanations]] |
| `authorizer/` | the action vocabulary for the shared authorizer contract | [[003-http-api]] |
| `api/openapi.yaml` | the HTTP surface, held to the Go types by a test | [[003-http-api]] |
| `estimate/` | the estimators, shared by the stub and the engine | [[008-ledger-and-estimation]] |
| `stub/` | an in-memory `limen.Service` with a pluggable predictor, for the test tiers of decision points | [[006-conformance]] |
| `conformance/` | the suite every `limen.Service` passes | [[006-conformance]] |
| `engine/kernel`, `engine/gp` | the additive kernel and the Laplace GP-probit, ported from the research | [[004-engine]] |
| `engine/featurize` | the open-set featurizer | [[004-engine]] |
| `engine/store` | the store interface, the memory and file stores, and the contract tests every store passes (`storetest`) | [[004-engine]] |
| `engine/` | the in-process `limen.Service` | [[004-engine]] |
| `client/` | the HTTP `limen.Service` | [[003-http-api]] |
| `server/` | the HTTP handler: routes, authentication, the owner policy, the base path | [[003-http-api]] |
| `cmd/limend` | the server program and its access configuration | [[003-http-api]] |
| `cmd/limen` | the replay command line | [[008-ledger-and-estimation]] |
| `importer/topos` | the reader of Topos session logs for a replay | [[007-evidence-gates]] |
| `cmd/limen-hook` | the decision point for hook-based harnesses; not built | [[005-adapters]] |

### Deployment

A decision point that runs as one process with its own state, such as a
local agent CLI, embeds `engine/` in process. A decision point that runs
on many machines, such as hosted agent runners that move between hosts
and hold no store credential, calls one `limend` over HTTP through
`client/`. Both satisfy `limen.Service`, so the decision point codes
against one interface. `FailClosed` wraps both: 200 milliseconds in
process, 2 seconds over HTTP from a hook. Predictions never fit a model
on the hot path; fitting runs in the background ([[004-engine]]).

### Invariants

Every implementation holds these, and [[006-conformance]] checks each
that a `limen.Service` can show:

1. **A deterministic deny is final.** It lives in the decision point's
   ceiling; `verdict.Decide` never returns anything but `block` under a
   `block` ceiling, and nothing Limen predicts reaches past it.
2. **A failure asks.** `FailClosed` turns an error or a timeout into a
   prediction of `ask`; `verdict.OnFailure` is never more permissive than
   `ask`.
3. **The review probability is the decision point's, drawn once.** It is
   recorded with the decision, never inferred by Limen, never supplied by
   the person answering, and a decision is recorded once per key.
4. **An unknown action is an ask.** A subject Limen knows nothing about
   has approval probability one half, and every cost profile Limen
   accepts puts one half inside the ask band.
5. **Input is never stored.** Extractors read `Action.Input` at
   prediction time; the features are kept, the input is dropped, and no
   language model inside Limen reads it.
6. **An answer's role follows from the decision.** The first answer to
   an action a person was shown is its review; every other answer trains
   and never enters an estimate.
7. **One key, one action.** A key names one action; a second action
   under the same key is refused.
8. **A person's records are theirs.** Forgetting a subject in an
   organization removes every case, answer and model of theirs there.

### Identity

The repository's identity role is `core`: `limend` verifies a caller's
token, asks one authorizer through `latere.ai/x/pkg/authz` for the route's
action before it acts, with the resource fields [[003-http-api]] names,
and decides nothing about a person. A token is read by the family's shared
verifier, `latere.ai/x/pkg/authkit/jwt`, and by nothing of Limen's own.

`limend` serves one person or a hosted installation. For one person, one
bearer, `LIMEN_TOKEN`, stands for one subject, `LIMEN_SUBJECT` (default
`local`), and the shared owner policy decides with that subject as its
admin. A hosted `limend` verifies tokens from the issuers its
operator lists, `LIMEN_OIDC_ISSUERS` for `LIMEN_OIDC_AUDIENCE`, as Topos
configures them, and reads the key sets once at start so a wrong issuer
stops the start. It then asks the installation's authorization endpoint,
`LIMEN_AUTHORIZER_URL` with its bearer, or, without one, applies the
shared owner policy with `LIMEN_ADMINS` as its admins: the decision
point's service subject is one, since it predicts, decides and answers
for every person it runs for. The two modes exclude each other.

### Dependencies

The root package is imported into every decision point's hot path, so it
reaches the standard library and `latere.ai/x/pkg/verdict` only; a test
parses its imports and `.lateregate.yaml` records the decision. The
engine's numerical dependency (gonum) lives under `engine/` alone.

### Non-goals

Limen does not execute actions, hold the approval inbox, route an
escalation to a particular reviewer, issue identities, or read the
transcript of an agent. It does not choose which actions to show a
person for the sake of learning (value of information) beyond the random
review the decision point draws.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | The root package imports only the standard library and `latere.ai/x/pkg/verdict` | `TestRootImports` and the `depcheck` gate |
| 2 | Every package named in the table exists with the role given, or its spec is still open | review against the tree at each spec's completion |
| 3 | `verdict.Decide` never returns a verdict more permissive than its ceiling, and the probability it returns is positive for a shown verdict and below one for an unshown one (*amended 2026-11-01*) | the `verdict` package's tests and fuzz targets, and a test in this tree |
| 4 | Each invariant a `limen.Service` can show has a case in the conformance suite | [[006-conformance]] |
| 5 | The engine and the client both satisfy `limen.Service` and pass the conformance suite | `conformance.Run` in `engine/` and in an e2e test against `limend` |

## Outcome

Recorded on 2026-11-01, after v0.3.0. Limen is in production: hosted
[Topos](https://github.com/latere-ai/topos) v0.66.0 sends every tool call
of a hosted session to one hosted `limend`. The architecture holds as
designed. The decision point owns the decision, and Limen predicts and
keeps the ledger. Every criterion has its proof in the tree.

### What was built

| Piece | Where |
|---|---|
| the shared vocabulary | `latere.ai/x/pkg/verdict` |
| the contract and the explanation views | the module root ([[002-contract]], [[009-explanations]]) |
| the ledger and the predictor | `engine/`, with the memory and directory stores ([[004-engine]]) |
| the estimates | `estimate/` ([[008-ledger-and-estimation]]) |
| the HTTP surface | `server/`, `client/`, `cmd/limend`, `api/openapi.yaml` ([[003-http-api]]) |
| the replay command and the Topos reader | `cmd/limen`, `importer/topos` |
| the stub and the suite | `stub/`, `conformance/` ([[006-conformance]]) |
| the in-process deployment | a Go decision point embeds `engine/` |
| the HTTP deployment | hosted Topos calls one `limend`, which keeps its ledger in a directory on a volume, with one replica |

| # | Proof in the tree |
|---|---|
| 1 | `TestRootImports` and the `depcheck` gate |
| 2 | review: the table above matches the tree; its open rows are `cmd/limen-hook` and the shared store of [[004-engine]] |
| 3 | `TestDecideNeverExceedsTheCeiling` and `TestDecideProducesValidDecisions` (`contract_test.go`); also `TestDecide`, `TestDecideProbabilityIsTheShowFrequency` and `FuzzDecide` in `latere.ai/x/pkg/verdict` |
| 4 | conformance cases per invariant: 2 `FailClosed`; 3 `OneDecision`, `ReviewsWeightedByProbability`; 4 `UnknownIsAnAsk`, `AskTooCostly`; 5 `CasesNeverReturnInput`, `IdempotentPredict`; 6 `AnswerRoles`, `LaterAnswersNeverEstimate`; 7 `IdempotentPredict`, `DecisionNeedsAnAction`; 8 `ForgetIsScoped`, with the model's removal in the engine's `TestForgetDropsTheModel`. Invariant 1 is `verdict.Decide`'s, criterion 3 |
| 5 | the engine: `TestConformance`, `TestConformanceInline`, `TestEngineConformance`; the client against a started `limend` on a fresh ledger directory: `TestConformance` in `cmd/limend`; also `TestConformanceEngine`, `TestConformanceStub` in `client/` |

### What diverges from the design as written

| What it said | What was built | Why |
|---|---|---|
| the package table | `server/`, `cmd/limen` and `importer/topos` exist and were not listed; the Postgres store was listed and does not exist | the table above is corrected; the shared store is [[004-engine]]'s |
| a failure asks (invariant 2) | Limen's fallback is an `ask` with `ask_kind` `unsure`; a decision point that lets its rules decide an `unsure` ask, as hosted Topos does in its `learned` mode, also lets them decide when Limen fails | the invariant is about Limen's suggestion, and it holds; the decision point decides, never above its ceiling, and Limen never allows ([[007-evidence-gates]], Amendments) |
| criterion 3 said the probability is positive exactly for shown verdicts | an undrawn automatic verdict also carries the audit rate, so criterion 3 now says what holds: positive for a shown verdict, below one for an unshown one | the ledger weights each review by the probability that the action could be shown ([[008-ledger-and-estimation]]) |

### What this leaves open

| Open | Why |
|---|---|
| `cmd/limen-hook` | the hooks for Claude Code and Codex come after Limen is open source ([[005-adapters]]) |
| a store that more than one `limend` shares | planned on Storage (Arca) after Limen is open source ([[004-engine]]) |
