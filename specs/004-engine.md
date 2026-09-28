---
title: "Engine: featurizers, the GP-probit posterior, the cost-derived rule, audits, estimates"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-contract.md
affects: [engine/, engine/featurize/, engine/gp/, engine/store/, engine/testdata/]
effort: large
created: 2026-09-28
updated: 2026-09-28
author: changkun
---

# Engine

## Overview

`engine/` is the in-process implementation of `Gate`, `Reviews` and
`Admin` ([[002-contract]]): it turns an `Action` into features, keeps a
Gaussian-process posterior over each subject's latent tolerance, derives
the verdict from the organization's costs, samples audits, records every
answer with the probability it had of arriving, and estimates error
rates from those answers. `limend` serves it over HTTP ([[003-http-api]]);
a harness written in Go may embed it instead.

The design is the gateway of *Learning When to Ask*
([arXiv:2605.19151](https://arxiv.org/abs/2605.19151)). Its Python
reference (`experiment/` in
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib))
is the numerical authority, and its Go port (`go/` in the same
repository, Apache-2.0) is the starting point for the code: `kernel`,
`gp`, `gateway` (costs, audits, estimates), `featurizer` (the tool
taxonomy, the judge featurizer, the shell command map) and `persist`.
This spec says what is ported, what changes on the way in, and what is
deliberately left out.

## Design

### The model

A subject approves a call `x` at time `t` with probability `Phi(f(x, t))`,
where the latent tolerance decomposes as `f(x, t) = tau(t) - r(x) +
h(x, t)`: a static risk `r` of the action, a tolerance `tau` of the
person that drifts, and a small interaction `h` (Definition 1 of the
paper). The prior is a zero-mean Gaussian process with the additive
kernel

```
k((x,t),(x',t')) = s_static k_x(x,x') + s_global k_time(t,t') + s_inter k_x(x,x') k_time(t,t')
k_x = k_tool * k_ctx      (squared exponential, lengthscales l_tool, l_ctx)
k_time(t,t') = exp(-|t - t'| / lambda)
```

with the paper's pre-specified values `s_static = 1.6`, `s_global = 1.0`,
`s_inter = 0.6`, `l_tool = 1.1`, `l_ctx = 1.2`, `lambda = 90` (in
decisions of the subject's own stream). The first term remembers what an
action risks, the second tracks the person's tolerance and moves with
every answer about any action, and the third lets tolerance drift
differently for different actions.

The separable product kernel `sigma^2 k_x k_time` is ported only as a
baseline and is never a default. It forgets static risk with its time
factor, so every action the model stops asking about decays back into
the ask band on a fixed clock (Proposition 4, `Forgetting.eventually_ask`
and `Forgetting.reentry_bound`), which in the paper's simulation doubled
the labels per action (13.5% against 28.4%) and made a demanding
threshold unreachable (99% of actions escalated under the safety
profile).

`lambda` is configuration, not learned. The paper found that selecting
it by marginal likelihood prefers long memories that improve calibration
on average and double the allows inside a trust reset's veto window;
the evidence optimizes average fit, not the rare region that matters.

### Inference

The posterior is the Laplace approximation of GP classification with a
probit likelihood (Rasmussen and Williams, Algorithms 3.1 and 3.2), as
`gp.LaplaceGPC` implements it. The predictive approval probability is
`approve = Phi(mu / sqrt(1 + sigma^2))`, the posterior mean of
`Phi(f)`; `uncertainty` is `sigma`. Proposition 2(a) is why the verdict
may depend on `approve` alone (`Chow.expectedLoss_allow`,
`Chow.expectedLoss_block`).

A model is kept per organization, subject and featurizer
([[001-architecture]]). It refits after every 8 new answers and on the
first assessment after a restart, from a training set bounded to the
2000 most recent labels, the research port's defaults. A refit that
fails keeps the previous posterior and is reported; an assessment with
no posterior yet is the prior's, `approve = 0.5`, which is inside every
non-empty ask band, so a new subject is asked.

### Featurizers

A featurizer maps an `Action` to the two feature vectors the kernel
reads. Two are ported and one is new.

| Featurizer | For | Features |
|---|---|---|
| `tools` | a harness with a known tool set | the tool's reversibility, base sensitivity, blast radius and category one-hot; the target's sensitivity, a destructive-argument flag and the task one-hot (the research taxonomy, `featurizer/trustcalib`) |
| `judge` | a shell-centric harness whose calls reach an LLM judge | the judge's verdict or score from `Action.Signals` and a command category one-hot over read, search, vcs, exec, write, db, network, deploy and other (`featurizer/judge`, with `bashmap.CategoryFromBash`) |
| `patterns` | either, as an addition | deterministic argument patterns read from `Action.Input`: a recursive delete, a force push, a pipe into a shell, a write outside the working directory |

The featurizer is chosen per installation and recorded in every model
key, so changing it starts new models rather than reading old features
through a new map. A judge's verdict is a feature and never a decision:
the static term learns how this subject's approvals relate to the
judge's output in each category. The `patterns` featurizer is the
remedy the paper names for a judge's blind spot, which calibration on
the judge's verdict alone cannot repair (the simulated blind spot let 23
of 29 dangerous calls through). No featurizer passes `Action.Input` to a
model; it is read by regular expressions and parsers only
([[001-architecture]], invariant 8).

### The rule

The thresholds are `Costs.Thresholds` of the most specific profile (a
profile saved without a certification claim carries `DefaultCertify`): the
agent's when the action names an agent that has one, else the
organization's; none is `unknown_profile`. The verdict is
`Thresholds.Verdict(score)` with `score = 1 - approve`, narrowed by the
ceiling. There is no tuning of thresholds on answers: the research port's
`gateway.TuneThresholds` and its tuning history are not ported, because
tuning needs labels on the automatic verdicts the engine never receives
and because the costs already determine the optimum.

Proposition 3 bounds what the rule can achieve: even with a perfect
posterior, every call whose true approval probability lies inside the
band is asked (`Floor.escalation_floor`). A replay report and a shadow
cohort ([[007-evidence-gates]]) compare burden against that floor, not
against zero.

### Audits

After the rule, an automatic verdict (an allow or a block the ceiling
permits) is sampled for review with the profile's `audit_rate`, from a
source of randomness the engine owns. A sampled allow becomes `flag`
and a sampled block becomes `ask`, both with `audit: {of, probability}`,
and the probability is stored with the assessment before the assessment
is returned. A draw happens once per subject and call id; a repeat
returns the stored result. When the ceiling already holds the call for a
person the engine draws nothing, because the answer arrives with
probability one. The research port recorded the probability at decision
time in its hook for the same reason (`persist.PendingAudit.Propensity`);
here it is a column of the assessment row.

### Answers and provenance

Each answer is stored with its kind and the probability it had of
arriving: one for a decision on a call the rule or the ceiling held for
a person, the audit probability for a decision on an audited call, and
none for a contest. Every decision and every contest is a training label
(a contest's label is its `approve`). Only the first two enter an
estimate.

### Estimates

For a window, a subject, and a source (`learned` for Limen's own
automatic verdicts, or a signal source for a judge's), the false-allow
estimate is the Horvitz-Thompson sum over answered calls the source
allowed, `sum(deny_i / p_i) / N_allowed`, and the false-block estimate is
the same over blocked calls with approvals (Propositions 5(a) and 8,
`Audit.audit_unbiased`, `Judge.ht_unbiased`). `N_allowed` counts every
call the source allowed in the window, answered or not. The 95% interval
is the Jeffreys interval on the audited fraction, as in the research
simulation. When no call in the window has a positive probability of an
answer the value is `nil`: an audit rate of zero makes the rate
unknowable, and the report says so. `unanswered` counts the calls in the
window sampled for review that nobody has answered yet. The estimate
treats them as missing at random, which holds when whether a person
answers a review does not depend on whether they would deny the call; a
count that is large next to `audits` weakens that assumption, and the
report carries it so the reader can see it. The certificate counts the most
recent run of consecutive clean audits of allows and holds when the run
reaches `CertificationSize(alpha, delta)`, 300 for 1% at 95%
(`Audit.certify_sample_size`). A judge's rate is per subject and per
window because it depends on whose decisions it stands in for
(Proposition 7, `Judge.judgeFA_mono`) and cannot be read off its
verdicts alone (Proposition 6, `Judge.judge_unidentified`).

### The store

One interface, two implementations ([[001-architecture]]). The file
store writes each subject's records under one directory, atomically
(temporary file and rename, as `persist.Save` does), and serves one
`limend` replica. The Postgres store serves several replicas; the
idempotency of assess is a unique key on subject and call id, so two
replicas racing on one call keep one assessment and one audit draw.
Neither stores a fitted posterior.

### Numerical parity

The engine's kernel, Laplace fit and predictions agree with the Python
reference to 1e-9 on the golden fixtures the research repository exports
(`go/testdata/fixtures`: `kernel_additive.json`,
`laplace_fit_additive.json`, `predict_additive.json` and their product
kernel counterparts), copied into `engine/testdata/` with the commit
they came from. A change that moves a number regenerates the fixtures
from the reference, not from the engine.

### Not in this engine

- Value-of-information acquisition. The rule is one step: it values an
  answer for the call at hand only (the paper's Remark on escalation
  versus acquisition). A sequential rule is future work, not a knob.
- BALD or any other acquisition sampler. With the additive kernel the
  ask band already beats random querying at a matched budget, and a
  stream-based BALD sampler did not improve on it under either kernel.
- Learning `lambda` or the costs from answers, for the reasons above.
- An LLM of its own. A judge is an input.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | The kernel, the Laplace fit and the predictive probabilities match the golden fixtures to 1e-9 | parity tests in `engine/gp` |
| 2 | With a fixed training set, the product kernel's prediction for a benign action tends to 0.5 as time since the last label grows and the additive kernel's to its static part | a test reproducing the paper's forgetting curve |
| 3 | A new subject's first assessment has `approve = 0.5` and is `ask` under both paper profiles | an engine test |
| 4 | The verdict equals `Thresholds.Verdict(score)` narrowed by the ceiling for every unaudited assessment, and the thresholds equal `Costs.Thresholds` of the most specific profile | an engine test over a stream, and [[006-conformance]] |
| 5 | Over a long stream the audited share of automatic verdicts is within three standard errors of `audit_rate`, no call is drawn twice, and no audit is drawn under an ask or block ceiling | an engine test with a seeded source |
| 6 | The false-allow estimate is unbiased: over many seeded streams its mean is within three standard errors of the realized rate | a Monte Carlo test, as the research repository's `test_audit.py` |
| 7 | A window with audit rate zero and no asks on allowed calls reports `value: null` | an engine test |
| 8 | A call sampled for review and never answered is counted in `unanswered` and in no other field | an engine test |
| 9 | The certificate holds after `CertificationSize` consecutive clean audits and not one before, and a denied audit resets it | an engine test |
| 10 | A contest changes the model and changes no estimate | an engine test comparing reports with and without the contest |
| 11 | Two replicas on one Postgres store racing on one call id keep one assessment | a store-tier test |
| 12 | Changing the featurizer starts a new model for the subject | a store test |
| 13 | The engine passes [[006-conformance]] | the conformance tier |
