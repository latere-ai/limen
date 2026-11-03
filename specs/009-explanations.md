---
title: "Explanations: why a prediction is what it is, what the model has learned about a person, and what another profile would have done, each exact for the model"
status: complete
track: core
depends_on: [003-http-api.md, 004-engine.md, 008-ledger-and-estimation.md]
affects: [explanation.go, engine/, engine/gp/, engine/featurize/, engine/store/, server/, client/, stub/, conformance/, api/openapi.yaml, docs/]
effort: medium
created: 2026-11-01
updated: 2026-11-03
author: changkun
---

# Explanations

## Overview

A person who lets a model decide for them should be able to see what it
decides from. Limen's model makes that cheap and exact rather than
approximate: the posterior mean of a Laplace GP classifier is a weighted
sum over the person's own answers, the additive kernel splits that sum
into the parts the model is built from, and the features are readable
because no language model produces them. This spec adds three read-only
views built on those facts:

1. **An explanation of one prediction**: the answers that moved it most,
   the split into what the model knows about this kind of action and
   about the person lately, and which of two kinds of uncertainty an
   `ask` comes from.
2. **What the model has learned about a person**: for each kind of
   action the person has answered, what Limen would suggest now, and how
   the person's tolerance has moved.
3. **A preview of another cost profile** on the person's own recorded
   predictions: how many actions would have run, waited or been stopped,
   and how many of those would have been wrong.

None of them changes a prediction, a decision or an estimate. Each is
the person's, and a decision point never hands one to an agent.

## Design

### The arithmetic an explanation reads

`engine/gp` fits a Laplace posterior with mode `f̂` and the gradient
`g = ∇ log p(y | f̂)`; `Predict` returns the predictive mean
`μ(x) = Σ_i k(x_i, x) g_i` over the person's `n` fitted answers. For the
probit likelihood, `g_i` is positive for an approval and negative for a
denial, and larger the more the answer disagreed with the mode. So:

- **Contribution of an answer.** `c_i(x) = k(x_i, x) · g_i`, and
  `Σ_i c_i(x) = μ(x)` exactly. The answers with the largest absolute `c_i` are
  the ones that moved this prediction most, in the model's own terms.
- **Parts.** The additive kernel is
  `k = s_s k_x + s_g k_t + s_d k_x k_t` ([[004-engine]]). The mean splits
  into the same three parts, each a sum over the same `g`:
  `action = s_s Σ k_x(x_i, x) g_i`, the static part (this kind of action,
  whatever the time), `recent = s_g Σ k_t(t_i, t) g_i`, the shared
  tolerance (every recent answer, about any action), and
  `interaction = s_d Σ k_x k_t g_i`. They sum to `μ(x)` exactly.
- **Two kinds of uncertainty.** *Amended 2026-11-02; see Amendments.*
  The prior variance at `x` is `v₀ = k(x, x)` and the predictive variance
  `σ²(x) ≤ v₀`. The evidence ratio `e = 1 − σ² / v₀` is the share of the
  prior variance the answers removed. For an `ask`:
  - `e < 0.5` is **unsure**: the person's answers say little about this
    action. More answers can move it.
  - `e ≥ 0.5` is **split**: the person's answers say much about this kind
    of action, and the approval probability is still inside the profile's
    ask range. That happens when the answers disagree, or when they agree
    but not yet strongly enough for the profile's thresholds. More
    answers that agree can still move it out of the ask range; answers
    that disagree keep it there. The paper's Proposition 3 holds in the
    limit of much evidence with mixed answers, not at a few answers.

  The cut 0.5 is a constant in `engine`, named and tested; changing it is
  a change to this spec. The kind does not say the answers disagree. A
  client that says so checks the evidence: an explanation's `evidence`
  holding an approval and a denial with one label, or a `learned` entry
  with `approved` and `denied` both above zero.

The product kernel has no parts; an explanation of a product-kernel
prediction carries contributions and the evidence ratio, and no parts.

### Which model explains

*Amended 2026-11-01; see Amendments.* A model is named by a `ModelRef`,
`{source, answers}`: the prediction source and the number of the
person's answers the posterior was fitted through. A case records the
`answers` of the model that predicted it (`predicted_by`). An explanation
uses the person's current model and names it (`model`); when
`predicted_by` equals `model`, the explanation repeats the recorded
prediction, and when it differs, the model has learned since and a
client says so. An explanation of an action with no recorded prediction
uses the current model, under the profile that applies now.

### Labels

Features are hashed vectors and cannot be shown. Each case gets a
**label** at prediction time, written by the featurizer from its closed
vocabulary only: the action's kind and name, and for a shell action the
program classes `ReadShell` recognizes with their subcommand from its
fixed table (`git push`, `go test`, `rm -r`), or `other command`. A label
never holds argument text, a path, a host, or any other string from the
input, so keeping it keeps [[001-architecture]] invariant 5: the input is
never stored. The label is stored with the case and versioned with the
featurizer (`features/1` labels are `labels/1`).

Cases recorded before labels exist have none; they are labeled from their
stored action, whose input is gone: the name alone for a tool, `<kind>
<name>` otherwise, and for a shell action `<name>: other command`.

### Routes

Three routes under the API's base path, all read-only, all subject-bound
like the cases route ([[003-http-api]]):

| Route | Answers | Permission |
|---|---|---|
| `GET /v1/explanations?org&subject&action_id` | the explanation of one case's prediction | `case.list` on the `Case` resource |
| `GET /v1/learned?org&subject[&agent][&kind]` | what the model has learned about the person | `case.list` on the `Case` resource |
| `POST /v1/previews` | another profile on the person's recorded predictions | `reliability.read` on the `Reliability` resource |

They ask existing actions, because each reveals no more than the cases
route or the reliability route already does; the authorizer vocabulary
does not change.

**An explanation** (`Explanation`):

| Field | |
|---|---|
| `org`, `subject`, `action_id` | the key |
| `model` | the model that explains, the person's current one, as a `ModelRef` |
| `predicted_by` | the model that made the recorded prediction; absent with no recorded prediction |
| `approve`, `uncertainty`, `verdict` | the prediction being explained, recomputed by `model` at the action's own time |
| `thresholds` | the recorded prediction's, or the profile's that applies now when there is none |
| `mean` | `μ(x)` |
| `parts` | `{action, recent, interaction}`, summing to `mean`; absent for a model without parts |
| `evidence_ratio` | `e` |
| `ask_kind` | `unsure` or `split` when `verdict` is `ask`, absent otherwise |
| `evidence` | up to three answers that pushed toward the verdict's side and up to two that pushed against it, each `{action_id, label, at, approve, share}`, where `share` is the answer's absolute contribution over the sum of all absolute contributions; ordered by `share` |

For an `allow`, the verdict's side is approval; for a `block`, denial; for
an `ask`, both sides are listed, three each at most.

**What it learned** (`Learned`): one entry per label among the person's
answered cases in the model's window, each with `label`, `kind`, `name`,
`approved` and `denied` counts, and the model's `approve`,
`uncertainty` and suggested `verdict` now, at the most recent case of
that label moved to the person's current time, under the profile that
applies to it (the profile scope of `kind` and the optional `agent`),
with the `evidence_ratio` and `ask_kind` an explanation at that point
carries, so a client can draw an unsure ask apart from a split one.
Plus `recent`: the shared tolerance part at up to 20 evenly spaced
points of the person's decision count, each `{t, at, value}`, so a
client can draw its trend.

An explanation of an action Limen has no record of is `no_case` (404),
a code this spec adds to the error table.

**A preview** (`Preview`): the body is `{org, subject, from, to, costs,
audit_rate}`; costs are validated like a profile's, `ask_too_costly`
included. The answer re-applies the thresholds of those costs to each
recorded prediction in the window, without refitting, and returns
`would` counts of `allow`, `ask` and `block`, `allowed_denied` and
`blocked_approved` among the answered cases, and the expected number of
asks including audits. It reads predictions as they were made, so it
answers "what would these costs have done with what the model knew
then", not what a refitted model would do.

### The rule for decision points

An explanation is for the person. A decision point never puts one, or
any part of one, in what its agent reads: a tool result, a system
message, a retry hint. The reason a prediction carries is already kept
from agents ([[005-adapters]]); an explanation names the person's own
past answers and the parts that would tip a decision, which is exactly
what an agent trying to pass a check would want. Topos's rule is in
[[005-adapters]] and holds here.

### Cost

An explanation costs one prediction plus `n` kernel evaluations split by
part, at `n` of a few hundred: under a millisecond in process. `learned`
costs one prediction per label. A preview reads recorded predictions and
fits nothing. None of them takes the per-subject lock longer than a
prediction does, and none runs on a decision's path.

## Not in this spec

- **A public route for people** beyond the base path. [[003-http-api]]
  serves the API under `LIMEN_BASE_PATH` as well, so a hosted
  installation can route a public prefix such as `/v1/oversight` to it,
  and the owner policy answers a person for their own subject; a
  console that acts for a person with another credential is the
  installation's.
- **Counterfactual edits** ("would it allow this if I had not denied
  that"): a refit per question, kept out until a client needs it.
- **Explanations of a judge's verdict.** A judge is opaque by design
  ([[008-ledger-and-estimation]]); its reliability report is what can be
  known about it.
- **Changing the model on request** beyond what exists: an answer, a
  dispute, and Forget.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | For every explained prediction, the contributions sum to `mean` and the parts sum to `mean`, within 1e-9 | an `engine` test over fitted posteriors of random histories |
| 2 | `mean`, `approve` and `uncertainty` of an explanation equal the prediction the same model returns | an `engine` test |
| 3 | `ask_kind` is `unsure` below the evidence-ratio cut and `split` at or above it, and is absent for `allow` and `block` | an `engine` test that constructs both kinds |
| 4 | An explanation names the current model and the model that made the recorded prediction; after a refit they differ, and an action with no prediction is explained under the profile that applies | an `engine` test that refits between the prediction and the explanation |
| 5 | A label holds only the action's kind, name and program classes from the featurizer's tables; no string of the input appears in any stored label | a featurize test over a corpus of commands with secrets in arguments, asserting none survives |
| 6 | A preview's counts equal the recorded predictions re-thresholded under the given costs, and `ask_too_costly` costs are refused | an `engine` test and a server test |
| 7 | The three routes ask exactly `case.list`, `case.list` and `reliability.read` with the subject-bound resource, and a caller acting for another subject without permission gets `forbidden` | `server` tests against `authz/stub` |
| 8 | The engine, the stub and the HTTP client agree on every field of the three views that no model decides: the key, the thresholds, the verdict as the thresholds' verdict, an ask's kind, the labels and their counts, and every count of a preview; by the model that predicted it, an explanation repeats the prediction; an unknown action is `no_case` | the conformance suite |
| 9 | `api/openapi.yaml` describes the three routes and types, held to the Go types by the existing test | `api` tests |
| 10 | Each entry of `learned` carries the `evidence_ratio` of an explanation at the entry's point, and `ask_kind` `unsure` or `split` by the same cut when its verdict is `ask`, absent otherwise | an `engine` test that compares each entry with the posterior at its point |
| 11 | A prediction carries the `evidence_ratio` and `ask_kind` of the explanation the same model gives at the same point, recorded with it; the `FailClosed` fallback is `unsure` | an `engine` test, the conformance suite, and the contract test of `FailClosed` |

## Amendments

**2026-11-01, at implementation.**

- *Which model explains.* The design assumed [[004-engine]] keeps a
  snapshot per model version. It keeps one snapshot per person, the
  current one, so an earlier posterior cannot be restored. An
  explanation therefore always uses the current model, named by a
  `ModelRef` (`source` and the number of answers it was fitted through);
  each case records the number of the model that predicted it, and the
  explanation reports it as `predicted_by`. The `version` field became
  `model` and `predicted_by`, and criterion 4 was reworded to match.
  Keeping past posteriors for exact explanations of old predictions is
  left out until a client needs it.
- *What every implementation agrees on.* The stub has no posterior, so
  criterion 8 holds the fields no model decides; the exact parts and
  shares are the engine's, held by criteria 1 to 3.
- *Learned entries* carry `evidence_ratio` and `ask_kind` (criterion
  10), so a client draws each entry's kind of uncertainty without asking
  for an explanation per label.
- *`no_case`.* An explanation of an action with no record answers a new
  code, `no_case` (404).
- *Labels of earlier cases* are the label of their stored action, as the
  Labels section states, and not `<kind> <name>` for every kind.
- *Base path and a person's own context.* [[003-http-api]] gained
  `LIMEN_BASE_PATH`, which serves these routes under a public prefix, and
  profiles of a person's own context, which the person may read and
  write.

**2026-11-01, the evidence on a prediction.** A decision point needs the
kind of an ask when it decides, not when a person asks why. `Prediction`
therefore carries `evidence_ratio` and, for an `ask`, `ask_kind`,
computed at prediction time exactly as for an explanation and recorded
with the prediction (criterion 11). Topos's `learned` mode reads
`ask_kind`: Limen's suggestion decides where it rests on evidence (an
`allow`, a `block`, a `split` ask) and the rules decide an `unsure` one,
or an `ask` with no `ask_kind`. The
`FailClosed` fallback, which has no model behind it, is `unsure`.

**2026-11-02, what a split ask means.** The owner's live test found the
design's reading of `split` wrong. A person allowed `publish` twice and
denied nothing. Under Careful (allow above 0.90, block below 0.25),
Limen predicted approve 0.80, uncertainty 1.07 and so an evidence ratio
of about 0.64, which is `ask_kind` `split`. The design said a split ask
means the answers disagree and that more answers will not move it,
citing Proposition 3. Both were false here: the answers agree, and more
allows raise 0.80 past 0.90. The arithmetic, the cut and the behavior do
not change; the reading does, in the Design above, the docs and the Go
comments:

- `split` means `e ≥ 0.5`, so the person's answers say much about this
  kind of action, and the approval probability is still inside the
  profile's ask range. The answers disagree, or agree but not yet
  strongly enough for the thresholds. More answers that agree can move
  it out; answers that disagree keep it there.
- Proposition 3 is about a policy that knows the true approval
  probability. The posterior nears it only with much evidence, so the
  proposition holds for a split ask in the limit of many mixed answers,
  not at a few answers.
- A client that wants to say "your answers disagree" checks the
  evidence: an explanation's `evidence` holding an approval and a denial
  for the same label, or a `learned` entry with `approved > 0` and
  `denied > 0`. Latere's console now does this.
- A decision point may still keep a split ask for the person, as hosted
  Topos does in `learned` mode. The reason is restated: the ask rests on
  the person's own answers, not on too few of them.

`TestSplitOnAnswersThatAgree` holds the case: two approvals and no
denial under Careful are a split ask, with only approvals in its evidence
and a learned entry with no denial, and more approvals reach an allow.

**2026-11-03, consent, wave 1 of [[011-consent]].** The owner decided
that `split` and `short` are two kinds (011, decision 7). An `ask` in the
band at an evidence ratio of 0.5 or more is `split` if, in a family of
the action, the latest answers of the person's answered cases in the
window hold an approval and a denial, and `short` if they agree. The
case of the amendment above, two approvals and no denial under Careful,
is now `short`, and `TestShortOnAnswersThatAgree` holds it. Topos keeps
`split` and `wider` asks for the person, and its rules decide `unsure`,
`short` and `few`.

- An explanation carries `cover`, with `nearest`: up to three approved
  cases of the family whose scope the action exceeds, closest first.
  `calibration` waits for wave 2.
- The cap applies to the verdict that an explanation calculates again,
  to each entry of `learned`, and to replays. A preview applies the
  cover that each prediction recorded.
- The reason of an `ask` states no probability: `unsure`, `split`,
  `short`, `wider` and `few` each have a fixed sentence, which
  `estimate.AskReason` gives to every implementation. An explanation
  gives the same reason as a prediction.
- Labels are `labels/2`, the `labels/1` label and a phrase of the scope,
  so a learned entry is one reach of one action. A case of `labels/1`
  keeps its label, and so its entry.

**2026-11-03, consent, wave 2 of [[011-consent]].** An explanation
carries `calibration`: the state of the bin of the recorded prediction's
`approve` in the person's calibration report for the last 90 days, the
explanation's own time included. It is the recorded prediction's bin,
not the bin of the `approve` that the explanation calculates again,
because a client holds the recorded prediction when it decides what to
show. An action with no recorded prediction has no `calibration`, and a
client then states no probability. The engine and the stub read it from
the cases the explanation already reads, about 3% of the time of an
explanation at 512 and at 4096 cases, so neither keeps a cache.
`TestExplanationReadsTheLast90Days` (`engine`), `TestCalibrationWindow`
(`stub`) and the conformance case `ExplanationCalibration` hold it.

## Outcome

Recorded on 2026-11-01, after v0.3.0. The three views and the evidence on a
prediction are built, in production, and every criterion has a passing
test in the tree. Hosted Topos decides on `ask_kind` in its `learned`
mode ([[007-evidence-gates]], Amendments). People read the views through
the hosted route: Latere's console and chat, the `latere oversight`
command of [latere-cli](https://github.com/latere-ai/latere-cli), and
the `oversight` package of
[sdk-go](https://github.com/latere-ai/sdk-go) v0.24.0.

### What was built

| Piece | Where |
|---|---|
| the types and the `Explainer` | `explanation.go` |
| the terms of the mean and its parts | `engine/gp` |
| the labels | `engine/featurize` |
| the three views, the evidence on a prediction, `EvidenceCut` | `engine/explain.go`, `engine/engine.go` |
| the stub's views | `stub/explain.go` |
| the routes and the client | `server/`, `client/`, `api/openapi.yaml` |

| # | Proof in the tree |
|---|---|
| 1 | `TestExplainSumsToTheMean` (`engine/gp`); the conformance case `Explanations` |
| 2 | `TestExplainSumsToTheMean`, `TestExplanationIsThePrediction` |
| 3 | `TestAskKinds`, `TestSplitOnAnswersThatAgree`, `TestExplanationIsThePrediction`, `TestBlockHasNoAskKind` |
| 4 | `TestExplanationAfterARefit` |
| 5 | `TestLabelKeepsNoInput`, `TestLabel`; the conformance case `LearnedEntries` |
| 6 | `TestPreview`; the server's `TestQueryErrors`; the conformance cases `Previews` and `PreviewErrors` |
| 7 | `TestAuthorizerQuestions`, `TestRefusalsActOnNothing`, `TestSubjectBinding`, `TestRoutesMatchOpenAPI` |
| 8 | the conformance cases `Explanations`, `ExplanationsOfSteeredVerdicts`, `LearnedEntries`, `Previews`, `PreviewErrors`, run for the engine, the stub and the client |
| 9 | `TestSchemasMatchTypes`, `TestOperations`, `TestResponsesMatchErrorTable` |
| 10 | `TestLearnedEntriesAreExplanations` |
| 11 | `TestExplanationIsThePrediction`, `TestAskKinds`, `TestFailClosedOnError`; the conformance cases `PredictionFollowsTheProfile` and `IdempotentPredict` |

### What diverges from the design as written

The Amendments above record the decisions made while building. These
divergences remain:

| What it said | What was built | Why |
|---|---|---|
| `learned` covers the answered cases in the model's window | it covers every stored answered case, beyond the 512 the model is fitted on | a person asks about every kind of action they answered |
| a case gets its label at prediction time | also when a decision arrives with an action and no prediction | such a case has no prediction to label it |
| a label holds the kind and the name | a tool's label is its name alone; a shell label is `<name>: <classes>`, at most two segments and then `…`; the kind appears for other kinds | the format is `featurize.Label`'s; it keeps no input string either way |
| no view holds the subject's lock longer than a prediction | `Explain`, `Learned` and `Preview` hold it for their whole run, and `Learned` explains each label and up to 20 trend points under it | a view reads one subject's cases consistently; no view runs on a decision's path, and its effect on a waiting prediction is not measured |
| the views' fields | `Learned` also names its `model`, each entry carries the profile's `thresholds`, entries come most answered first, and a preview echoes its request with `records`, `answered` and `thresholds` | a client needs them to draw the views |

### What this leaves open

| Open | Why |
|---|---|
| explanations of old predictions by the model that made them | one snapshot per person is kept ([[004-engine]]) |
| counterfactual edits | kept out until a client needs them |
