---
title: "Explanations: why a prediction is what it is, what the model has learned about a person, and what another profile would have done, each exact for the model"
status: drafted
track: core
depends_on: [003-http-api.md, 004-engine.md, 008-ledger-and-estimation.md]
affects: [explanation.go, engine/, engine/gp/, engine/featurize/, engine/store/, server/, client/, stub/, conformance/, api/openapi.yaml, docs/]
effort: medium
created: 2026-11-01
updated: 2026-11-01
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
- **Two kinds of uncertainty.** The prior variance at `x` is
  `v₀ = k(x, x)` and the predictive variance `σ²(x) ≤ v₀`. The evidence
  ratio `e = 1 − σ² / v₀` is the share of the prior variance the answers
  removed. For an `ask`:
  - `e < 0.5` is **unsure**: the person's answers say little about this
    action. More answers will move it.
  - `e ≥ 0.5` is **split**: the answers say a lot, and they disagree, so
    the approval probability sits inside the ask range on evidence. More
    answers will not move it (paper, Proposition 3).

  The cut 0.5 is a constant in `engine`, named and tested; changing it is
  a change to this spec.

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
