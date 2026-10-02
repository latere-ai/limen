# Integrating a decision point

How a decision point asks Limen about an action, decides, records what it
did, and sends back what the person said. A decision point is the code
that lets an automated action run or not: an agent harness deciding on a
tool call, a proxy on a network request, a service on a push or a payment.

## The three calls

| Call | Go | HTTP | When |
|---|---|---|---|
| Predict | `Predictor.Predict` | `POST /v1/predictions` | before you decide, on the hot path |
| Record | `Ledger.Record` | `POST /v1/decisions` | once per action, after you decide and before the action runs or anyone sees it |
| Answer | `Ledger.Answer` | `POST /v1/answers` | whenever a person answers about an action |

A Go decision point can embed the engine and call it in its own process,
with no network in the path; any other, or one that runs in many places,
calls the server over HTTP. Both are a `limen.Service`, so the choice is
made at start-up and the rest of the code does not change. The engine and
the server are specified but not built yet; until they are, the stub in
`stub/` serves the same interfaces for tests.

Wrap the predictor with `FailClosed` and give it a timeout that fits your
latency budget, such as 200 milliseconds in process or 2 seconds over
HTTP:

```go
predictor := limen.FailClosed(svc, 200*time.Millisecond, func(err error) {
	log.Printf("limen: %v", err) // the action waits for a person
})
```

When Limen errors or runs past the timeout, `FailClosed` returns a
suggestion of `ask` with the source `learned/unavailable` and no error.
Limen holds no record of that action, so put the action in your decision
(`Decision.Action`) and the person's answer still teaches the model.

## Building an action

An `Action` is the action as your decision point sees it before it runs.
Every field is in the [API guide](api.md) and the
[OpenAPI document](../api/openapi.yaml).

| Field | What to put there |
|---|---|
| `org` | the organization whose cost profile applies |
| `subject` | the person on whose behalf the action runs, as the token's issuer and `sub` joined by a pipe: `https://issuer.example\|usr_42`. Limen learns one model per subject. For an agent acting for a person, the person, not the agent. |
| `action_id` | your own id for the action, unique within the organization and subject, such as a session id and a tool call id |
| `actor` | who proposed it: `kind` (`agent`, `service`, or `person`), `id`, `session` |
| `kind`, `name` | the domain and the operation: `tool` and `bash`, `egress` and `https`, `repo` and `ref.update`, `spend` and `charge`. A cost profile can be scoped to a kind. |
| `effect` | `none`, `read`, `write` (inside the boundary of the system that runs it), or `external` (anything that leaves it: the network, a message sent, money moved). When unsure, `external`. |
| `reversible` | whether the action can be undone, when you know |
| `input` | the action's input as JSON, such as a tool call's arguments. Only deterministic feature extractors read it, and Limen never stores it. |
| `target` | what it acts on: `kind` (`path`, `ref`, `host`, `account`), `id`, and optionally `sensitivity` from 0 (scratch) to 1 (production) |
| `environment` | where it runs: `kind` (`host`, `sandbox`, `service`) and `credentialed`, whether a credential there reaches outside it |
| `attributes` | further facts your domain has, as strings, numbers, or booleans: an amount, whether a branch is protected, a file count. At most 64. |
| `signals` | other opinions: your rules' score as `{"source": "rules/1", "score": 0.4}`, a judge's verdict as `{"source": "classifier/claude-auto-mode", "verdict": "allow"}`. Each has a verdict, a score in [0, 1] (higher is riskier), or both. |

Predicting the same action again returns the first prediction. Predicting
a different action under an `action_id` already used is refused with
`action_conflict`, so give each action its own id.

### Examples from four domains

A tool call in an agent harness:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01",
  "actor": {"kind": "agent", "id": "agt_ci", "session": "ses_9f"},
  "kind": "tool", "name": "bash", "effect": "external",
  "input": {"command": "git push origin main"},
  "target": {"kind": "ref", "id": "refs/heads/main"},
  "environment": {"kind": "sandbox", "credentialed": true},
  "signals": [{"source": "rules/1", "score": 0.4}]
}
```

A network request through a sandbox's egress proxy:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "egr_5c1",
  "actor": {"kind": "agent", "id": "agt_ci", "session": "ses_9f"},
  "kind": "egress", "name": "https", "effect": "external",
  "target": {"kind": "host", "id": "api.github.com"},
  "environment": {"kind": "sandbox", "credentialed": true},
  "attributes": {"method": "POST", "bytes": 18234}
}
```

A push to a repository host:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "push_77a",
  "actor": {"kind": "agent", "id": "agt_ci"},
  "kind": "repo", "name": "ref.update", "effect": "write", "reversible": false,
  "target": {"kind": "ref", "id": "refs/heads/main", "sensitivity": 0.9},
  "attributes": {"protected": true, "force": false, "commits": 3}
}
```

A payment:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "chg_19",
  "actor": {"kind": "agent", "id": "agt_buyer"},
  "kind": "spend", "name": "charge", "effect": "external", "reversible": false,
  "target": {"kind": "account", "id": "acct_vendor_3"},
  "attributes": {"amount": 120.5, "currency": "EUR"}
}
```

## Deciding

Limen suggests; your decision point decides. Compose the suggestion with
your own rules and the random review in one call to the shared verdict
package:

```go
v, prob := verdict.Decide(p.Verdict, ceiling, p.AuditRate, rand.Float64())
```

- **`ceiling`** is the most permissive verdict your own rules leave open:

  | Your rules say | `ceiling` |
  |---|---|
  | nothing about this action | `allow` |
  | run it, but review it afterwards | `flag` |
  | always confirm this kind of action | `ask` |
  | never run this action | `block` |

  The result is never more permissive than the ceiling, and a `block`
  ceiling is final: never reviewed, never overturned.
- **`p.AuditRate`** is the share of automatic verdicts the organization's
  profile asks a person to review at random. Pass a rate of your own if
  your policy differs; what matters is that the probability you record is
  the one you used.
- **The draw** is made once per action, here. Do not draw again.

`verdict.Decide` returns the verdict to apply and the probability that a
person sees the action. An automatic allow picked for review becomes
`flag`, and an automatic block picked for review becomes `ask`.

| Verdict | What the decision point does |
|---|---|
| `allow` | run the action |
| `flag` | run it, then put it in the person's review queue; their answer comes later |
| `ask` | hold it until the person answers. Do not time an ask out into a refusal: an agent that is refused routes around it. |
| `block` | do not run it, and tell the agent why in your own words. A prediction's `reason` is written for the person's record and is never shown to an agent. |

## Recording

Record every decision, before the action runs or anyone sees it:

```go
d := limen.Decision{Key: a.Key, Verdict: v, ReviewProbability: prob, Source: "myharness/learned"}
if p.Source == limen.SourceUnavailable {
	d.Action = &a
}
err := svc.Record(ctx, d)
```

`source` names the policy that decided, such as `rules/1` or
`topos/progressive`, so the ledger can report each policy's error rate.
An action takes one decision; a second is refused with `already_decided`.
Recording asynchronously is fine as long as the record does not depend on
how the action turned out. [The ledger](ledger.md) explains what the
review probability means and the mistakes that bias the record.

## Answers

Send the person's answer whenever they give it:

```json
{"org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01", "approve": true}
```

- **At an ask**: whether the person approved.
- **After the review of a flagged action**: whether they would have
  approved it, even though it already ran (or, for a reviewed block, did
  not).
- **Later**: when an action that ran is disputed afterwards, for example
  because a reviewer rejected its result, send `"approve": false`. It
  teaches the model and never enters an error estimate.

Limen works out which answer is a review from the recorded decision, so an
answer needs a recorded decision first (`no_decision` otherwise).

The answer is attributed to the caller. A harness that relays a person's
answer, such as a confirmation the person gave in its interface, names
them in `by`; Limen honors it only when your authorization endpoint allows
the harness to answer for that subject.

`GET /v1/cases?org=...&subject=...&pending=true` lists the actions shown to
the person that still need an answer, oldest first: the review queue.

## Using the ledger alone

The ledger needs no predictor. A decision point that decides by rules can
measure its rules and its judge:

1. For each action, compose your rules' verdict instead of Limen's:
   `verdict.Decide(rulesVerdict, ceiling, rate, rand.Float64())`.
2. Record the decision with `source` naming your rules, put the action in
   it (Limen has no prediction to take it from), and pass your judge's
   verdict as a signal on the action.
3. Send the answers.

Then `GET /v1/reliability?...&of=rules/1` is your rules' error rate for
that person, and `of=classifier/<name>` your judge's. A rate of `null`
means nothing your source allowed could have been seen: set a review rate
above zero, and the allows become measurable.

## Choosing a cost profile

A profile states three costs, in any unit, since only their ratios matter:

| Cost | Meaning |
|---|---|
| `false_allow` | running an action the person would deny |
| `false_block` | refusing an action the person would approve |
| `ask` | one interruption |

The thresholds follow: `allow_above = 1 - ask / false_allow` and
`block_below = ask / false_block`. Each kind of error must cost more than
two interruptions, so that an action about someone Limen knows nothing
about (a 50% chance of approval) is always an ask; otherwise saving fails
with `ask_too_costly`.

| Profile | `false_allow` | `false_block` | `ask` | Asks when approval is between | `allow_above` | `block_below` |
|---|---|---|---|---|---|---|
| Symmetric | 2.857 | 2.857 | 1 | 0.35 and 0.65 | 0.65 | 0.35 |
| Safety-weighted (the default) | 10 | 4 | 1 | 0.25 and 0.90 | 0.90 | 0.25 |

```http
PUT /v1/profiles
Content-Type: application/json

{
  "org": "org_7",
  "kind": "spend",
  "costs": {"false_allow": 50, "false_block": 4, "ask": 1},
  "audit_rate": 0.05,
  "certify": {"alpha": 0.01, "delta": 0.05}
}
```

- **Scope.** A profile applies to an organization, optionally narrowed to
  one kind of action, one agent, or both. For an action, the most specific
  saved profile applies, in the order kind and agent, agent, kind,
  organization; with none saved, the built-in default does, and
  `GET /v1/profiles` says so with `"default": true`.
- **`audit_rate`** is the share of automatic verdicts a person reviews at
  random. The paper's simulation used 5%. At zero, the false-allow rate of
  automatic verdicts cannot be estimated, and reports say so.
- **`certify`** is the claim reliability reports test: a false-allow rate
  below `alpha` at confidence `1 - delta`. It is optional; without it the
  claim is 1% at 95%, which takes 300 consecutive clean reviews of
  automatic allows. At a 5% review rate that is about 6,000 automatic
  allows with no denied review among them; a denied review restarts the
  count. Certifying 25% at 95% takes 12.

## Reading a reliability report

`GET /v1/reliability?org=...&subject=...&of=...&from=...&to=...` reports
one source's error rates for one person over one window: `learned` for
Limen's suggestions, `applied` for the verdicts you applied, a policy's
`source`, or a signal's. [The ledger](ledger.md#reading-a-report) explains
every field; in short, read the `interval` before the `value`, read
`covered` to know what the value speaks for, and keep `unanswered` small.

## Rolling out

Limen's usefulness has to be shown on your own history before it decides
anything. Four steps, each needing the previous step's evidence:

1. **Replay.** Send your recorded history to `POST /v1/replays` as
   newline-delimited JSON, one record per line: when the action was
   proposed (`at`), the `action`, the `verdict` you applied with its
   `review_probability` and `source`, and `approve` when the person
   answered. Without an explicit review policy, an ask, and a flag your
   rules forced, were answered with probability one, and an allow or a
   block with probability zero. Limen predicts the records in time order,
   each before learning its own answer, and reports what it would have
   suggested (`would`), how often it would have allowed what the person
   denied (`allowed_denied`) or blocked what they approved
   (`blocked_approved`), and a false-allow estimate whose `covered` share
   says how much of that your history can speak to. With `keep=true` the
   records also seed the live models and the ledger.
2. **Shadow.** Your rules keep deciding. For every action, ask Limen for a
   prediction, compose your rules' verdict (not Limen's) with
   `verdict.Decide` and a review rate above zero, and record the decision
   with your rules' source. Limen learns from the answers, and its
   reliability report (`of=learned`) measures its suggestions on the
   actions your decisions made reviewable; `covered` says how many of
   Limen's would-be allows your reviews reach.
3. **Learn inside the ceiling.** Compose Limen's suggestion instead, still
   under your rules' ceiling, first where an automatic allow can do the
   least harm, such as inside a sandbox.
4. **Judge signals.** Pass your judge's verdicts in as signals, and watch
   the judge's own reliability report per person.

## Notes by harness

**Topos** ([github.com/latere-ai/topos](https://github.com/latere-ai/topos))
is the first decision point, and it works today. Topos speaks to any
decision service with this API: its rules set the ceiling,
`verdict.Decide` composes, every decision is recorded with the session's
tool call, and a person's confirmations are forwarded as answers. Two modes
are all you need:

- **Manual mode** (`topos run --mode manual`, Topos's `confirm`). Every
  call that is not read-only asks you, as it always did, and every answer
  teaches Limen. Each prediction is recorded beside the call, so you can
  see what Limen would have done.
- **Auto mode** (`topos run --mode auto`, Topos's `progressive`). Limen's
  suggestion decides inside Topos's rules: a familiar safe call runs, a
  call you would deny is blocked, and anything Limen is unsure about asks
  you. A share of automatic verdicts is still shown to you for review, so
  the ledger keeps measuring how often auto mode is wrong.

Start Limen, then point Topos at it:

```sh
export LIMEN_TOKEN=$(openssl rand -hex 16)
limen serve &                                   # http://127.0.0.1:7720

export TOPOS_DECISIONS_URL=http://127.0.0.1:7720
export TOPOS_DECISIONS_TOKEN=$LIMEN_TOKEN
topos run --mode manual "Fix the failing test."  # for a while: Limen learns
topos run --mode auto   "Fix the failing test."  # then: Limen decides inside the rules
```

To see what Limen would have done on sessions you already ran, without
serving anything, replay Topos's data directory:

```sh
limen replay topos
```

A person who knows nothing yet gets asks: Limen suggests allow or block
only once their answers say so. A few dozen answers per kind of call is
typical before auto mode stops asking about it.

The adapters below are planned, not built.

**Claude Code** calls the server from a command hook. A `PreToolUse` hook
answers allow, ask, or deny: apply `allow` as allow, `flag` as allow plus
an entry in your review queue, `ask` as ask, and `block` as deny. Claude
Code's own permission rules can still ask about or deny an action the hook
allowed, which the hook cannot see, so the recorded review probability can
understate how often a person saw such actions. How a person's answer to
an ask surfaces in Claude Code's hook events has not been checked yet; the
adapter's design depends on it.

**Codex** ([github.com/openai/codex](https://github.com/openai/codex))
consults its auto-review reviewer only when its static rules would
otherwise ask, and returns a denial to the agent; a person sees an action
only after repeated denials. Its reviewer-allowed actions are never
answered, so the reviewer's false-allow rate for a person is `null` until a
share of those actions is reviewed at random.
