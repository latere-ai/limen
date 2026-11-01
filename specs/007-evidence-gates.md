---
title: "Evidence gates: replay, shadow, learned decisions inside the ceiling, judge signals, and what stops a rollout"
status: drafted
track: core
depends_on: [001-architecture.md, 008-ledger-and-estimation.md]
affects: [docs/integrating.md, importer/topos/, cmd/limen/]
effort: small
created: 2026-09-28
updated: 2026-11-01
author: changkun
---

# Evidence gates

## Overview

Every result behind Limen's design comes from a controlled simulation
with a known oracle ([arXiv:2605.19151](https://arxiv.org/abs/2605.19151)).
Recovering the oracle shows that the inference is right under the model;
it does not show that real people approve and deny the way the model
assumes, that their tolerance drifts the way the simulation's does, or
that they stay reliable as ground truth when they are asked often. The
paper names a longitudinal study of real supervisors as its main gap.

This spec is the rule every deployment follows so that Limen earns each
step with evidence from the step before: four gates, the evidence each
produces from the ledger, the criteria that pass it, and the conditions
that stop a rollout at any step. Because the ledger measures any source,
the gates compare Limen with the decision point's current rules on the
same answers, rather than trusting either.

## Design

### Pre-registration

Before a gate's data is read, the operator writes down the cohort (the
subjects, who opt in, since their answers are theirs), the cost profile,
the audit rate, the window, and the numbers that pass the gate, and
commits them. A gate evaluated against criteria written after its data
does not pass.

### Gate 1: replay

**Runs.** `Admin.Replay` without `keep` over the decision point's
recorded history: each action with the verdict applied, its review
probability, its source and the person's answer where there was one.
Nothing is applied and nothing is kept.

**Produces.** The `ReplayReport`: records, answered records, Limen's
would-be verdicts, the answered records it would have allowed and the
person denied, and the false-allow estimate of its would-be allows with
its coverage.

**Passes when**, for the cohort: Limen's would-be ask share is below the
current rules' ask share by the pre-registered margin; the
allowed-but-denied count is at or below the pre-registered bound; and
the coverage is stated. History with no random review cannot measure
what the current rules allowed unasked, so a low coverage passes this
gate and moves the question to the next.

### Gate 2: shadow

*Amended 2026-11-01: hosted Topos did not pass this gate before Limen
decided. See Amendments.*

**Runs.** The decision point's current rules decide, as today. For every
action of the cohort it also calls `Predict`, composes its current
verdict with review sampling at the pre-registered audit rate through
`verdict.Decide`, and records the decision with its own source (for
example `topos/progressive`). Limen's suggestion is recorded and never
applied. The one change a person sees is the random review of a share of
the current rules' automatic allows.

**Produces.** Per subject, over the window, from one ledger:
`Reliability` of `applied` (the current rules) and of `learned`
(Limen's suggestions), each with its interval and coverage; Limen's
would-be ask share against the rules' ask share; and both across a change
the subject makes (a new repository, a new kind of work), the drift the
model is built for. This is the longitudinal study of real supervisors
the research lacks.

**Passes when**, for the cohort: the upper end of `learned`'s
false-allow interval is at or below `applied`'s estimate plus the
pre-registered non-inferiority margin; Limen's would-be ask share is
below the rules' by the pre-registered margin; and the certificate of
`learned` holds for the pre-registered share of subjects.

### Gate 3: learned decisions inside the ceiling

*Amended 2026-11-01: for hosted Topos, the operator's decision of that
date replaces this gate. See Amendments.*

**Runs.** For the cohort, the decision point composes Limen's suggestion
with its ceiling and review sampling, and applies the result. The
ceiling keeps every deterministic rule. Learned allows run only where a
wrong allow is contained: in a sandbox, or on a host with an operating
system sandbox; elsewhere the ceiling is `ask`.

**Produces.** The same reports for `applied`, now Limen's decisions, and
the burden the cohort actually carries.

**Passes when** the gate 2 criteria hold for `applied` over the window
and no stop condition fired.

### Gate 4: judge signals

**Runs.** The decision point passes its LLM judge's verdict as a signal.

**Produces.** `Reliability` of the judge's source per subject and window
(its false-allow and false-block rates for that person), and Limen's
reports with and without the signal.

**Passes when** Limen with the signal is non-inferior on false-allow and
lower on burden than without it, by the pre-registered margins, and a
pre-registered set of commands the judge is known to misjudge is never
allowed without a person. The second criterion exists because
calibrating on a judge's verdict cannot repair a blind spot the features
do not expose; the paper's simulated blind spot let 23 of 29 dangerous
actions through.

### What stops a rollout

Any of these returns the affected subjects, or the whole cohort, to the
current rules at once, at any gate:

| Condition | Scope | Why |
|---|---|---|
| a decision more permissive than its ceiling | the cohort | a broken invariant, not a statistical event |
| an allow after a prediction failed | the cohort | fail-open, the one failure the design exists to prevent |
| a shown decision with review probability zero, or an unshown one with probability one | the cohort | every later estimate from it is biased |
| the lower end of a subject's false-allow interval for `applied` above the profile's `certify.alpha` | the subject | Limen is allowing, for that person, more than the organization said it tolerates |
| `unanswered` above the pre-registered share of a subject's shown cases | the subject | the reviews no longer speak for the actions they stand for |
| a subject approving asks at a rate above the pre-registered bound | the subject | a person who approves almost everything is no longer ground truth, and a model calibrated to rubber-stamping learns to rubber-stamp |
| a subject asking to be forgotten | the subject | `Forget`, and their answers leave every estimate |

A stop is recorded with the condition, the time and the subjects, and a
rollout resumes only through the gate it stopped at, with its criteria
unchanged.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | Each gate's pre-registration exists and is committed before the gate's first prediction | the deployment's record, reviewed at each gate |
| 2 | Every quantity a gate's criteria name is a field of a `ReplayReport` or a `Reliability` | review of each pre-registration against [[002-contract]] |
| 3 | In shadow, no suggestion changes what the decision point does, and the random reviews are the only actions shown that the current rules would not have shown | a decision point's test with the stub ([[006-conformance]]) |
| 4 | Every stop condition is detectable from the ledger alone | a test in `engine/` per condition over a scripted history |
| 5 | A subject returned to the current rules by a stop receives no applied suggestion until the gate is passed again | a decision point's test |

## Amendments

**2026-11-01, Limen decides where it has evidence.** The operator of
hosted Topos decided, on the day Limen went live, to let Limen decide
without waiting for gates 2 and 3. Hosted Topos v0.66.0 runs its
decision service in `learned` mode (`TOPOS_DECISIONS_MODE=learned`,
[[005-adapters]]):

- **Gate 2 is not run for hosted Topos.** Its pass criteria, the
  non-inferiority of `learned` against `applied`, the ask-share margin
  and the share of subjects with a certificate, were not measured before
  Limen decided.
- **Gate 3 has a different shape.** It applies to every hosted
  `progressive` session, not to a pre-registered cohort. Limen's
  suggestion decides a call the rules leave open only where Limen has
  evidence about the person: its `allow`, its `block`, and an `ask` with
  `ask_kind` `split` ([[009-explanations]]). Where the ask is `unsure`,
  or Limen does not answer, the rules and the risk score decide exactly
  as they did without Limen, and Limen records its suggestion and learns
  from the answer. The containment rule, learned allows only in a
  sandbox, is not applied; a hosted session on a person's own computer
  still decides `progressive` as `confirm`.
- **What stays.** Every deterministic layer of the decision point
  decides first, and Limen's suggestion only narrows under it. A random
  share of automatic verdicts, at the audit rate of the person's
  profile, still goes to the person, so `Reliability` of `learned` and
  of `applied` stays measured per person. The rollback is one setting,
  `TOPOS_DECISIONS_MODE=shadow`.
- **Why.** Gate 2 followed by the full gate 3 would have asked about
  almost every write on the first day, because Limen suggests `ask` for
  anything it has not seen. Waiting for each person's certificate, 300
  clean audits under the default claim, would have kept Limen off for
  weeks. Deciding where the evidence is, and keeping the rules
  elsewhere, is no worse than the rules alone for any person, and it
  becomes personal one kind of action at a time as answers arrive.
- **Stop conditions.** The row "an allow after a prediction failed" does
  not fit `learned` mode: when Limen fails, the rules decide as before
  Limen, and they can allow. The other rows stand.

Gate 1, gate 4, pre-registration and the stop table remain the rule for
every other deployment, and for a hosted deployment that wants the
evidence before Limen decides.

## Outcome

Recorded on 2026-11-01. This spec is a rule for deployments, not code.
The status stays `drafted`: the first deployment followed the amendment
above, and criteria 2, 4 and 5 need work in this repository before the
gates can be run as written.

### What was built

| Piece | Where |
|---|---|
| gate 1, the replay | `Admin.Replay` and `POST /v1/replays`; `limen replay topos` and `limen replay file` (`cmd/limen`, `importer/topos`) |
| the reports the gates read | `Reliability` of `learned`, `applied`, a decision source and a signal source ([[008-ledger-and-estimation]]) |
| the evidence on a prediction that the amended gate 3 decides on | `evidence_ratio` and `ask_kind` ([[009-explanations]]) |

### What this leaves open

| Open | Why |
|---|---|
| criterion 2 | some quantities the gates name are not fields: the rules' ask share and Limen's would-be ask share in shadow, gate 3's burden, the approval rate of asks, `unanswered` as a share of shown cases, and gate 4's set of misjudged commands; they need `Cases` |
| criterion 3 | Topos's shadow mode applies its rules unchanged and draws no review, so the random review gate 2 describes does not happen there |
| criterion 4 | no `engine/` test per stop condition; the ledger cannot show three of them: a `Decision` has no ceiling, a fallback prediction is not recorded, and a decision whose probability breaks the verdict's rule is refused at `Record` |
| criterion 5 | neither Limen nor Topos keeps a per-subject stop; Topos's mode applies to the whole installation |
