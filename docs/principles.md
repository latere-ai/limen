# Principles

Twelve rules every implementation of Limen keeps. Each says what holds,
why, and how it is checked. Most are checked by the conformance suite,
which will run against any implementation of the contract, the engine and
the server included; it is specified in [`specs/`](../specs/README.md) and
not built yet. The reasoning behind the rules is in [Theory](theory.md) and
in the paper it cites.

## 1. Escalate, don't refuse

When Limen is unsure about a call, the call waits for a person (`ask`).
It is not refused.

**Why.** An agent that is refused does not stop. It reads the refusal as
a result and tries something else, which is often worse than the call it
was denied. A person asked at the right moment costs one interruption; an
agent routing around a block can cost much more. In the paper's
simulation of an LLM judge, turning the judge's blocks into escalations
instead of enforcing them cut cost-weighted regret from 0.296 to 0.088.

**Checked by.** The conformance suite checks that a call inside the ask
band gets `ask`, never `block`.

## 2. Thresholds come from stated costs

You state three costs: running a call the person would deny, refusing a
call the person would approve, and one interruption. The thresholds
follow from them by a closed formula and are never tuned on data.

**Why.** With those three costs, allow, ask, and block is the optimal
decision exactly when the thresholds are
`ask_at = ask / false_allow` and `block_at = 1 - ask / false_block`
(Chow's reject option). Tuning thresholds instead would need answers on
calls Limen decided on its own, and those calls never get answers. A cost
profile also writes down the organization's appetite for risk where
anyone can read it.

**Checked by.** The conformance suite checks that every assessment's
thresholds are the ones its profile's costs imply, and that costs which
leave no ask band are refused with `empty_ask_band`.

## 3. A deterministic deny is final

Every action carries a `ceiling`: the most permissive verdict your
harness's own rules leave open. Limen's verdict is never more permissive.

**Why.** Rules you wrote down, such as "never push to a protected
branch", are commitments, not evidence. A learned model can only operate
inside the envelope they leave. A `ceiling` of `block` gives `block`, a
`ceiling` of `ask` means the call always waits for a person, and nothing
Limen learns changes either.

**Checked by.** The conformance suite checks that no verdict is more
permissive than its ceiling, for every ceiling.

## 4. Failure means ask

When Limen fails, is slow, or cannot be reached, the call waits for a
person. It is never allowed by default.

**Why.** A gate that fails open is not a gate. Wrapping any gate with
`FailClosed` bounds the time it may take and turns every error or timeout
into `ask` (or `block`, if the ceiling says so), with the source
`learned/unavailable` so the decision record shows what happened.

**Checked by.** The contract's own tests check `FailClosed`, and the
conformance suite checks that an implementation's errors reach the
caller as errors, not as verdicts.

## 5. Some asks never go away

Where a person is genuinely undecided about a kind of call, Limen keeps
asking, however much it has learned.

**Why.** If a person approves a kind of call about half the time, no model
can predict their answer, and under most costs asking is the cheapest
choice. The share of calls that must be asked, even with a perfect model,
is set by the person and the costs, not by Limen. In the paper's
simulation it was 10.3% of calls under symmetric costs and 31.3% under
costs that weigh a wrong allow ten times an interruption. A promise of
"95% automation" is a claim about the person, not the product.

**Checked by.** This is a consequence rather than a rule to test: it holds
because thresholds come only from the profile (rule 2), which the
conformance suite checks.

## 6. Forget the person's tolerance, not the action's risk

Limen's model lets a person's tolerance drift over time, but keeps what it
learned about how risky each kind of action is.

**Why.** People change: trust grows over a project and drops on a new
one. The risk of force-pushing to production does not. A model that
forgets both, as a common time-decaying construction does, drifts every
action back to "unsure" on a fixed clock and asks again about calls it
already knew. In the paper's simulation, keeping the static part cut
human answers per call from 28.4% to 13.5% and cost-weighted regret by
59%, at a false-allow rate of about 1%.

**Checked by.** The engine's tests, against fixtures exported from the
research implementation.

## 7. An automatic verdict is not a label

A call Limen allows or blocks on its own never gets an answer. To know
its own error rate, Limen samples a small share of those calls for
review, fixes the sampling probability when it assesses the call, and
weights each review answer by one over that probability.

**Why.** Every error rate computed only from the calls a person was asked
about is biased: those calls are the ones Limen was unsure about. Sampled
reviews with known probabilities give an unbiased estimate
(Horvitz-Thompson). When no answer with a known probability exists, a
rate is reported as `null`, meaning "cannot be estimated", never as zero.
A sampled call nobody answers is counted as `unanswered` rather than
dropped, because an estimate that silently skipped the hard reviews would
look better than it is. Certifying a false-allow rate below 1% with 95%
confidence takes 300 consecutive clean audits.

**Checked by.** The conformance suite checks that `audit.probability` is
set by Limen and never taken from a caller, and that a rate with no
eligible answer is `null`.

## 8. No re-rolling

Assessing the same call twice returns the first assessment, audit sample
included.

**Why.** If a harness could ask again until the audit draw missed, the
sample would no longer be random and every estimate built on it would be
biased. Assessments are idempotent on the person (`subject`) and the
harness's call id (`call_id`).

**Checked by.** The conformance suite checks that a repeated assessment
is identical to the first.

## 9. A judge is evidence, not a verdict

An LLM judge's verdict, or any rule score, enters as a `signal`: an input
to Limen's prediction of what this person would say. It never decides a
call by itself.

**Why.** A judge's error rate for a person cannot be read off its
verdicts; it depends on the person's decisions too, and it changes when
that person's tolerance changes, even though the judge does not. In the
paper's simulation, the same unchanged judge allowed calls its supervisor
would deny 2.9% of the time before a trust reset, 10.0% after it, and
3.4% later. So a judge's reliability is reported per person and per
window, from audited answers.

**Checked by.** The conformance suite checks that reliability is reported
per signal source and per subject, and that a judge-allowed call can be
audited.

## 10. Contests train, never estimate

A later dispute of a call that already ran, such as a reviewer rejecting
its result, is sent as an answer of kind `contest`. It teaches the model,
but it never enters an error estimate.

**Why.** Nothing fixed the probability with which a call gets contested,
so a contest cannot be weighted the way an audit answer is. Counting it
would bias the estimate toward whatever gets noticed.

**Checked by.** The conformance suite checks that a contest changes no
reliability estimate.

## 11. Text an agent writes never reaches a model inside Limen

A tool call's input is read only by deterministic feature extractors, for
example one that recognizes a recursive delete in a shell command. No
language model inside Limen ever reads it.

**Why.** The agent writes the input. If a model inside the gate read it,
the agent could write instructions to the gate. Judges that read the
transcript have been attacked this way; Limen takes a judge's verdict as
a signal and never the text the judge read.

**Checked by.** The engine's architecture test: no package that reads an
action's input depends on a model client.

## 12. A person's data is theirs

Limen learns from a person's answers only for that person, and forgets
everything about them on request: answers, assessments, and the model
learned from them.

**Why.** A person's approve and deny history describes how they work.
Using it is their choice, and deleting it must be complete and
idempotent.

**Checked by.** The conformance suite checks that after forgetting a
subject, no record, pending review, or reliability report for that
subject remains.
