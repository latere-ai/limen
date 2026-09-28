# Theory in brief

What Limen computes and why, for an engineer who wants to know what the
numbers in an assessment mean. The full argument, the proofs, and the
simulations are in the paper, Changkun Ou, *Learning When to Ask: Trust
Calibration for Agentic Tool Use, from Known Tools to Opaque LLM Judges*
([arXiv:2605.19151](https://arxiv.org/abs/2605.19151)). The cores of its
eight propositions are machine-checked in Lean 4 with Mathlib; the
theorem names below are those of the development at
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib).

This page describes the model the engine will implement. The engine is
not built yet.

## What Limen predicts

A proposed call $x$ is the action and its context: the tool, its input,
its target, where it runs. The person the agent acts for either approves
it ($y = 1$) or denies it ($y = 0$). Limen models their answer through a
latent tolerance $f$:

$$
\Pr(y = 1 \mid x, t) = \Phi\bigl(f(x, t)\bigr),
\qquad
f(x, t) = \tau(t) - r(x) + h(x, t).
$$

$\Phi$ is the standard normal distribution function, $r(x)$ is the static
risk of the action, $\tau(t)$ is the person's tolerance at time $t$, and
$h$ is a small interaction that lets tolerance drift differently for
different actions. The split matters: the risk of an action is a property
of the action, and the tolerance is a property of the person that changes
over time.

## Why approve and deny, not preferences

Limen learns from answers to "may this run?", not from comparisons such as
"which of these two calls is safer?".

**Proposition 1.** Approval probabilities identify $f$ exactly, and so the
boundary $\{x : f(x) > 0\}$ between calls to allow and calls to deny.
Pairwise comparisons, $\Pr(x \succ x') = \Phi(f(x) - f(x'))$, do not
change when a constant is added to $f$, so for every call there is another
tolerance with the same comparisons that puts the call on the other side
of the boundary. Comparisons rank calls; they cannot say where to stop.
(Lean: `unary_identifies`, `unary_identifies_boundary`,
`pairwise_shift_invariant`, `pairwise_cannot_identify_threshold`.)

This is why Limen is Gaussian-process classification with a reject
option, related to preferential Bayesian optimization but not an instance
of it.

## The model

Limen places a Gaussian-process prior on $f$. With kernels $k_x$ over
actions and contexts and $k_{\text{time}}(t, t') = e^{-|t - t'|/\lambda}$
over time, it uses the additive kernel

$$
k\bigl((x, t), (x', t')\bigr)
= \sigma_s^2\, k_x(x, x')
+ \sigma_g^2\, k_{\text{time}}(t, t')
+ \sigma_d^2\, k_x(x, x')\, k_{\text{time}}(t, t'),
$$

whose three terms mirror the decomposition above: static risk, shared
tolerance (every answer, about any action, updates it), and the
interaction. The common alternative, the separable product
$\sigma^2 k_x k_{\text{time}}$, makes everything drift, including the risk
of an action; the next sections show what that costs.

The posterior after the person's answers is approximated by the Laplace
method. Its predictive latent mean $\mu$ and variance $\sigma^2$ give the
probability in an assessment:

$$
\texttt{approve} = \hat p = \Phi\!\left(\frac{\mu}{\sqrt{1 + \sigma^2}}\right),
\qquad
\texttt{score} = 1 - \hat p,
\qquad
\texttt{uncertainty} = \sigma.
$$

Large uncertainty pulls $\hat p$ toward one half, so under any profile whose
ask band contains one half, which covers every profile below, a call Limen
has never seen anything like lands in the ask band on its own.

## The decision rule

With costs $c_{\text{FA}}$ for running a call the person would deny,
$c_{\text{FB}}$ for refusing one they would approve, and $c_{\text{ask}}$
for one interruption, the expected costs at approval probability $p$ are
$(1 - p)\,c_{\text{FA}}$ to allow, $p\,c_{\text{FB}}$ to block, and
$c_{\text{ask}}$ to ask.

**Proposition 2.** The expected cost depends on the posterior only
through $\hat p$, and the optimal choice is

$$
\text{allow if } \hat p > \tau_{\text{high}} = 1 - \frac{c_{\text{ask}}}{c_{\text{FA}}},
\qquad
\text{block if } \hat p < \tau_{\text{low}} = \frac{c_{\text{ask}}}{c_{\text{FB}}},
\qquad
\text{ask otherwise.}
$$

The band between the two thresholds is non-empty exactly when
$c_{\text{ask}}(c_{\text{FA}} + c_{\text{FB}}) < c_{\text{FA}}\,c_{\text{FB}}$;
otherwise some automatic verdict is always at least as good as asking,
and Limen refuses such a profile with `empty_ask_band`. (Lean:
`expectedLoss_allow`, `expectedLoss_block`, `band_nonempty_iff`,
`allow_strictly_optimal`, `block_strictly_optimal`, `ask_strictly_optimal`,
`decide_optimal`, `ask_never_needed`.)

An assessment reports the thresholds in score units, $s = 1 - \hat p$:
`ask_at` $= c_{\text{ask}}/c_{\text{FA}}$ and `block_at`
$= 1 - c_{\text{ask}}/c_{\text{FB}}$. A score below `ask_at` is an allow,
above `block_at` a block, and anything between an ask.

| Profile | $c_{\text{FA}}$ | $c_{\text{FB}}$ | $c_{\text{ask}}$ | Ask band in $\hat p$ | `ask_at` | `block_at` |
|---|---|---|---|---|---|---|
| Symmetric | 2.857 | 2.857 | 1 | 0.35 to 0.65 | 0.35 | 0.65 |
| Safety-weighted | 10 | 4 | 1 | 0.25 to 0.90 | 0.10 | 0.75 |

Only the ratios matter. The safety-weighted profile says a wrong allow
costs ten interruptions and a wrong block four, so Limen allows on its own
only above a 90% chance of approval.

The rule values an answer only for the call at hand. An answer also
improves later predictions, so a rule that priced that value might ask
somewhat less; the one-step rule is the one whose thresholds an operator
can read off the costs.

## Some asks never go away

**Proposition 3.** Even a policy that knows each call's true approval
probability $q(x)$ must ask about every call with
$\tau_{\text{low}} < q(x) < \tau_{\text{high}}$. The share of calls that
must be asked is bounded below by the share of the person's own ambiguity
at your costs, which no learner can remove. (Lean:
`optimal_asks_on_band`, `escalation_floor`.)

In the paper's simulation this floor was 10.3% of calls at the symmetric
profile and 31.3% at the safety-weighted one.

## What forgetting costs

$k_{\text{time}}$ lets recent answers dominate, which tracks drift, but it
also pulls every call that stops getting answers back toward the prior.

**Proposition 4.** Under the separable product kernel, once answers stop,
the latent mean of every call decays as $\mu(t + \Delta) =
e^{-\Delta/\lambda}\mu(t)$, so every call drifts back into the ask band: a
call with mean $m > 0$ is no longer allowed after at most $\lambda
\ln(m/z)$ steps, where $z = \Phi^{-1}(\tau_{\text{high}})$. Under the
additive kernel only the time terms decay, and the mean converges to its
static part. (Lean: `kT_shift`, `meanProd_shift`, `quadForm_shift`,
`exp_decay_tendsto`, `eventually_ask`, `reentry_bound`, `reentry_prob`,
`meanAdd_shift`, `meanAdd_tendsto`.)

With $\lambda = 90$ and $\tau_{\text{high}} = 0.65$ ($z \approx 0.385$), a
call with $m = 2$ comes back to the ask band within
$90 \ln(2/0.385) \approx 148$ steps whether or not the person changed. In
the simulation, the product kernel's prediction for a benign file read
decayed from 0.65 to 0.50 with no new answers, while the additive kernel's
settled at its static part, 0.76.

## Generalization cuts both ways

The kernel is what lets an answer about one call inform similar calls,
and it is why Limen needs far fewer answers than a table with one entry
per tool and target. It also means approval spreads toward dangerous
neighbors: a call that resembles approved ones inherits their approval.
In the simulation, on a force-push to production infrastructure that was
never labeled and that the person mostly denies, the additive model
allowed 7 of the 64 occurrences the person would have denied (11%). This
is the case audits and the ceiling exist for.

## An automatic verdict is not a label

Only calls a person answers produce labels, and those are the calls Limen
was unsure about. The model's posterior is not biased by this, but every
error rate counted from the labels is. Limen therefore samples a share
$\varepsilon$ of its automatic verdicts for review (an audit) and fixes
each sample's probability when it assesses the call.

**Proposition 5.** (a) With audit indicators of mean $\varepsilon > 0$ and
false-allow indicators $e_i$ over $N$ automatic allows, the
Horvitz-Thompson estimator $\sum_i A_i e_i / (N \varepsilon)$ is unbiased
for the realized false-allow rate. (b) If the true rate is at least
$\alpha$, $n$ independent audits are all clean with probability at most
$(1 - \alpha)^n \le e^{-\alpha n}$, which is at most $\delta$ once

$$
n \ge \frac{\ln(1/\delta)}{\alpha}.
$$

(Lean: `audit_unbiased`, `one_sub_pow_le_exp`, `certify_sample_size`.)

| Claim: false-allow rate below | Confidence | Consecutive clean audits |
|---|---|---|
| 25% | 95% | 12 |
| 1% | 95% | 300 |

**Proposition 8** generalizes (a): if call $i$ receives an answer with a
known probability $\pi_i > 0$, weighting each answer by $1/\pi_i$ is
unbiased. Answers at an ask ($\pi = 1$) and audit answers ($\pi =
\varepsilon$) then count together, which is why an assessment records
`audit.probability` and why a contest, whose probability nobody fixed,
never counts. (Lean: `ht_unbiased`.)

In the simulation, 5% audits raised answers per call from 13.5% to 18.2%
and halved allows while a rare veto was active (11 to 5 of 109). Counted
as a full interruption each, audits did not pay for themselves in average
regret (0.056 without, 0.089 with); they pay off when a review costs at
most 0.22 of an interruption. Audits make the error rate estimable, not
small.

## What can be known about a judge

For a judge $J$ that returns allow or block, and a person with approval
probability $q$, the judge's error rates for that person are

$$
\mathrm{FA}_J = \Pr(y = 0 \mid J = \text{allow}),
\qquad
\mathrm{FB}_J = \Pr(y = 1 \mid J = \text{block}).
$$

**Proposition 6.** $\mathrm{FA}_J$ cannot be learned from the judge's
verdicts. For any allow rate in $(0, 1)$, every value of $\mathrm{FA}_J$ in
$[0, 1]$ is possible, and even knowing both how often the judge allows and
how often the person approves does not fix it: two situations with a judge
allow rate of 0.8 and a person approval rate of 0.9 can have
$\mathrm{FA}_J = 0$ and $\mathrm{FA}_J = 1/8$. (Lean:
`judge_unidentified`, `judge_marginals_not_enough`.)

Asking several judges does not help. Latent-class models recover an
annotator's error rates from three or more annotators only when their
errors are independent given the truth, and the errors of language models
are strongly correlated.

**Proposition 7.** For a fixed judge, if one person is nowhere more
permissive than another on the calls the judge allows, the judge's
false-allow rate is at least as high for the stricter person. The same
unchanged judge is more or less trustworthy depending on whose decisions
it stands in for, and when. (Lean: `judgeFA_mono`.)

So Limen reports a judge's error rates per person and per window, from
answers with known probabilities (Proposition 8). In designs where a
person sees a call only after the judge blocks it, no judge-allowed call
is ever answered, every such probability is zero, and $\mathrm{FA}_J$ is
not estimable at all. How likely one verdict is to be right is Limen's
calibrated $\hat p$ for that call with the verdict as an input, which is
limited by what the other inputs can tell apart.

## What the simulation showed, and its limits

No public dataset records one person's approve and deny decisions while
their tolerance drifts, so the paper evaluates in a controlled simulation
with a known oracle. The numbers below are simulation results: they show
that the inference is right under the model, not that real people behave
like the model.

| Finding | Separable product kernel | Additive kernel |
|---|---|---|
| Answers needed per call | 28.4% | 13.5% |
| Cost-weighted regret | 0.137 | 0.056 (59% lower) |
| False-allow rate | 1.2% | 1.0% |
| Calls asked under safety-weighted costs | 99.1% | 45.9% (floor 31.3%) |

With the product kernel, choosing which calls to label by the ask band
was no better than labeling at random; with the additive kernel it beat
random by about 3 points of accuracy on contested calls. The controlling
variable was forgetting, not the choice of acquisition rule.

In the judge study (pre-registered before it ran), letting the judge
decide alone gave regret 0.296; turning its blocks into escalations gave
0.088 at 25.3% answers per call; a calibrated gateway over the judge's
verdict reached 0.086 with 11.8%. The same judge's false-allow rate for
the simulated person was 2.9% before a trust reset, 10.0% after it, and
3.4% later; audit estimates tracked it without bias but noisily, with 8
to 24 answers per window. A blind spot of the judge (destructive shell
commands that looked benign to it) was not caught: 23 of the 29 such
calls the person would deny were still allowed, because they looked like
benign commands in the gateway's inputs too.

The limits matter as much as the numbers:

- The oracle is additive by construction, which favors the additive
  kernel; the size of the gain is indicative.
- The simulated judge blocks about a quarter of all calls, far more than
  the 0.4% false-positive rate reported for a production classifier, so
  the gain from escalating its blocks is inflated.
- The simulated person is noisy but unbiased. Real people approve almost
  every prompt they see and grow less careful as prompts repeat; a model
  calibrated to a rubber stamp learns to rubber-stamp.
- Reliability estimated from ordinary audits says nothing about an
  adversary. Prompt injections written by an agent have passed production
  judges.
- Calibration cannot see past its inputs. A blind spot is fixed by an
  input that exposes it, such as a feature of the command's arguments,
  not by more calibration.
