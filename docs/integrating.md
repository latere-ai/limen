# Integrating a harness

How a harness asks Limen about a tool call, applies the verdict, and sends
back what the person decided. The contract described here is written; the
engine, the server, the client, and the harness adapters that implement it
are not built yet, and the sections that depend on them say so.

## In process or over HTTP

A harness written in Go can embed the engine and call it in its own
process, with no network in the path. Any other harness, or one that
shares a model across machines, calls the server over HTTP. Both implement
the same `Gate` interface, so the choice is made at start-up and the rest
of the harness does not change:

```go
type Gate interface {
	Assess(ctx context.Context, a Action) (Assessment, error)
	Answer(ctx context.Context, assessment string, ans Answer) error
	Reliability(ctx context.Context, q ReliabilityQuery) (Reliability, error)
}
```

Always wrap the gate with `FailClosed` and give it a timeout that fits the
harness's latency budget:

```go
gate := limen.FailClosed(g, 200*time.Millisecond, func(err error) {
	log.Printf("limen: %v", err) // the call was held for a person
})
```

When the gate errors or runs past the timeout, `FailClosed` returns an
assessment with verdict `ask` (or `block` if the ceiling says so), source
`learned/unavailable`, and no error. That assessment has no `id`: there is
nothing to send an answer for, so the person's decision on that call is
not learned from.

## Building an action

An `Action` describes the call with what the harness knows before it runs.
Every field below is in the [API guide](api.md) and the
[OpenAPI document](../api/openapi.yaml).

| Field | What to put there |
|---|---|
| `subject` | the person the agent acts for, as the token's issuer and `sub` joined by a pipe: `https://issuer.example\|usr_42`. Limen learns one model per subject. |
| `org` | the organization whose cost profile applies |
| `agent`, `session` | who proposed the call; optional, and used for a profile scoped to one agent |
| `call_id` | the harness's own id for the call, such as a tool use id. Assessing the same subject and call id again returns the first assessment. |
| `tool` | the tool's name as the harness knows it: `bash`, `write`, `mcp__github__merge_pull_request` |
| `effect` | `none` (no effect outside the agent), `read`, `write` (inside the harness's boundary, such as the working tree or a sandbox), or `external` (anything that leaves it: the network, a push, a deploy). When unsure, use `external`. |
| `input` | the tool's input as JSON. Only deterministic feature extractors read it; no model inside Limen does. |
| `target` | what the call acts on: `kind` (`path`, `ref`, `host`, `resource`), `id`, and optionally `sensitivity` from 0 (scratch) to 1 (production) |
| `machine` | `kind` (`host` or `sandbox`) and `credentialed`: whether the machine holds a credential that reaches outside it |
| `signals` | other opinions: your rule engine's score as `{"source": "rules/1", "score": 0.4}`, a judge's verdict as `{"source": "judge/claude-auto-mode", "verdict": "allow"}`. Each carries a verdict, a score, or both; a score is in [0, 1], higher meaning riskier. |
| `ceiling` | the most permissive verdict your own rules leave open (see below) |
| `at` | when the call was proposed |

Name a signal's `source` as `<kind>/<name>` and keep it stable: Limen
reports each source's error rate for each person under that name.

### The ceiling

The ceiling is how your harness's deterministic rules stay in charge.
Compute it before asking Limen:

| Your rules say | `ceiling` |
|---|---|
| nothing about this call | `allow` |
| run it, but review it afterwards | `flag` |
| always confirm this kind of call | `ask` |
| never run this call | `block` |

Limen's verdict is never more permissive than the ceiling. Send the call
even when the ceiling is `block`: the assessment returns `block` at once,
and the decision record stays complete.

## Applying the verdict

| Verdict | What the harness does |
|---|---|
| `allow` | run the call |
| `flag` | run the call, then put it in the person's review queue; their answer comes later |
| `ask` | hold the call until the person answers. Do not time an ask out into a refusal: an agent that is refused routes around it. |
| `block` | do not run the call; return the assessment's `reason` to the agent as the call's result |

An assessment with an `audit` is a call sampled for review. An audited
allow arrives as `flag` and an audited block as `ask`; apply them like any
other `flag` or `ask`. The audit's `probability` is what makes the
person's answer usable for an unbiased error estimate, and Limen fixed it
when it assessed the call.

`reason` is one sentence written for the person, suitable for an approval
prompt or a decision log.

## Sending answers

Send the person's decision to `POST /v1/assessments/{id}/answer` (or
`Gate.Answer`) with the assessment's `id`:

```json
{"approve": true, "at": "2026-09-28T10:03:40Z"}
```

- **At an ask**: the person's approve or deny, when they give it.
- **After a review of a flagged or audited call**: whether the person
  would have approved it, even though it already ran (or, for an audited
  block, did not).
- **A later dispute**: when a call that ran is rejected afterwards, for
  example because a reviewer rejected its result, send
  `{"approve": false, "kind": "contest", "at": ...}`. A contest teaches
  the model but never enters an error estimate.

A call takes one answer; a second one is refused with `already_answered`.
The answer is attributed to the subject of the credential that sends it,
never to a field in the body. `GET /v1/assessments?subject=...&pending=true`
lists the calls that still need an answer, oldest first, which is what a
review queue shows.

## Choosing a cost profile

A profile states three costs, in any unit, since only their ratios matter:

| Cost | Meaning |
|---|---|
| `false_allow` | running a call the person would deny |
| `false_block` | refusing a call the person would approve |
| `ask` | one interruption |

The thresholds follow from them: `ask_at = ask / false_allow` and
`block_at = 1 - ask / false_block`, in score units (the probability of a
deny). Two profiles worked through:

| Profile | `false_allow` | `false_block` | `ask` | Asks when approval is between | `ask_at` | `block_at` |
|---|---|---|---|---|---|---|
| Symmetric | 2.857 | 2.857 | 1 | 0.35 and 0.65 | 0.35 | 0.65 |
| Safety-weighted | 10 | 4 | 1 | 0.25 and 0.90 | 0.10 | 0.75 |

If asking is never worth it at your costs, which happens exactly when
`ask * (false_allow + false_block) >= false_allow * false_block`, saving
the profile fails with `empty_ask_band`: raise a cost of a wrong verdict or
lower the cost of asking.

A profile also sets the audit rate and the certificate:

```http
PUT /v1/profiles/org_7
Content-Type: application/json

{
  "costs": {"false_allow": 10, "false_block": 4, "ask": 1},
  "audit_rate": 0.05,
  "certify": {"alpha": 0.01, "delta": 0.05}
}
```

- `audit_rate` is the share of automatic verdicts sampled for review. The
  paper's simulation used 5%. With a rate of zero, Limen cannot estimate
  its false-allow rate and says so.
- `certify` is the claim a reliability report tests: a false-allow rate
  below `alpha` at confidence `1 - delta`. It is optional; without it the
  claim is 1% at 95%. Certifying 1% at 95% takes 300
  consecutive clean audits of automatic allows; at a 5% audit rate that is
  about 6,000 automatic allows with no audited deny among them. A denied
  audit restarts the count. Certifying 25% at 95% takes 12.

`PUT /v1/profiles/org_7/agents/agt_ci` sets a profile for one agent; the
most specific profile wins. The response carries the derived `thresholds`.

## Reading a reliability report

`GET /v1/reliability?subject=...&of=learned&from=...&to=...` reports how
often Limen's own automatic verdicts disagreed with one person over one
window. With `of=judge/claude-auto-mode`, or any other signal source, it
reports that source's verdicts instead. An illustrative report for a
judge:

```json
{
  "subject": "https://issuer.example|usr_42",
  "of": "judge/claude-auto-mode",
  "from": "2026-09-01T00:00:00Z",
  "to": "2026-09-28T00:00:00Z",
  "false_allow": {"value": 0.10, "interval": [0.03, 0.25], "labels": 24, "audits": 9, "unanswered": 1},
  "false_block": {"value": 0.49, "interval": [0.31, 0.67], "labels": 31, "audits": 2, "unanswered": 0},
  "certificate": {"alpha": 0.01, "delta": 0.05, "clean": 112, "needed": 300, "holds": false}
}
```

- `false_allow` is the share of calls the source allowed that this person
  would have denied; `false_block` the share it blocked that they would
  have approved. Each answer counts with weight one over the probability
  with which it was going to be given, so answers at an ask and audit
  answers count together without bias.
- `labels` is how many answers the estimate rests on and `audits` how many
  of those were audits. Few labels means a wide `interval`: read the
  interval before the value.
- `unanswered` counts calls sampled for review that nobody has answered.
  The estimate assumes they would have gone the way the answered ones
  did; if people only review the easy ones, it is too optimistic. Keep
  this count small by answering the review queue.
- `"value": null` means the rate cannot be estimated in this window,
  because no answer there had a known probability. It does not mean zero.
  A harness that only ever shows the person calls its judge blocked has no
  answers on judge-allowed calls, and its judge's false-allow rate is
  always `null`.
- The rates hold for this person and this window only. The same judge is
  more or less reliable for a stricter or a more permissive person, and
  for the same person after their tolerance changes. In the paper's
  simulation, one unchanged judge's false-allow rate for one person went
  from 2.9% to 10.0% after the person's trust reset, and back to 3.4%.
- The certificate holds when `clean` reaches `needed`. It is the claim to
  quote: "false-allow rate below 1% for this person over the last 300
  audits".

## Rolling out

Limen's usefulness has to be shown on your own history before it decides
anything. Four steps, each needing the previous step's evidence:

1. **Replay.** Send recorded calls to `POST /v1/replays` as
   newline-delimited JSON, one record per line: the `action`, the
   `verdict` your harness applied, the `answer` if the person gave one,
   and the `audit` if your harness sampled the call for review. Limen
   assesses the records in time order, each before learning its own
   answer, and reports how many calls it would have allowed, flagged,
   asked about, and blocked (`would`), how often it would have allowed
   what the person denied (`allowed_denied`) or blocked what they approved
   (`blocked_approved`), and a false-allow estimate. `covered` is the share
   of Limen's would-be allows that had any chance of an answer in your
   record; the estimate speaks only for that share, so a history in which
   your harness allowed most calls without asking covers little.
2. **Shadow.** Assess live calls beside your current rule and keep
   applying your rule's verdict. Send answers for the calls your rule
   asks about. Where you can afford the reviews, honor Limen's audits too:
   they are what makes its false-allow rate estimable on calls your rule
   also allows.
3. **Learned thresholds.** Apply Limen's verdict, inside your rules'
   ceiling.
4. **Judge signals.** Pass your judge's verdicts in as signals, and watch
   the judge's own reliability report per person.

## Harness notes

The adapters below are planned, not built.

**Topos** ([github.com/latere-ai/topos](https://github.com/latere-ai/topos))
embeds the engine in process. Its `progressive` permission mode maps a
risk score to allow, flag, ask, or block, and reserves a learned source
for that score. Limen enters there, as the score source and the setter of
the mode's thresholds, not as a hook: a Topos hook can only make a verdict
stricter, and Limen's value is the asks it removes.

**Claude Code** calls the server through a command hook. A `PreToolUse`
hook can answer allow, ask, or deny, so there the hook can remove an ask.
`flag` maps to allow plus an entry in the review queue. Claude Code's auto
mode lets its classifier allow or block and asks the person only after
repeated blocks
([Anthropic](https://www.anthropic.com/engineering/claude-code-auto-mode)),
so its classifier fits as a signal source. How a person's denial of an
ask surfaces in Claude Code's hook events has not been checked yet; the
adapter's design depends on it.

**Codex** ([github.com/openai/codex](https://github.com/openai/codex))
consults its auto-review reviewer only when its static rules would
otherwise ask, and returns a denial to the agent; a person sees a call
only after repeated denials. Its reviewer-allowed calls therefore never
get an answer, and its false-allow rate for a person is estimable only if
the harness audits a share of them.
