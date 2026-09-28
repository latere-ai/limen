# API

The HTTP API of Limen's server, `limend`, for a harness that does not
embed the engine. The same routes are in the
[OpenAPI document](../api/openapi.yaml); the Go interface a Go harness
calls is described in [Integrating](integrating.md). The server is not
built yet: this page describes the contract it will serve.

## Conventions

- **Base path.** The server serves every route under `/v1`. A platform
  that hosts Limen may mount it under a prefix of its own.
- **Authentication.** Every request carries a bearer token from an issuer
  the installation lists, for the audience it verifies:
  `Authorization: Bearer <token>`. Limen verifies the token and asks the
  installation's authorizer whether the caller may take the route's
  action (named on each route below) before it acts.
- **Bodies.** Requests and responses are JSON, except the replay upload,
  which is newline-delimited JSON. Times are RFC 3339. Unknown
  fields in a request are refused.
- **Idempotency.** Assessing the same `subject` and `call_id` again returns
  the first assessment unchanged, audit sample included, with status 200.
  An answer is accepted once per assessment.
- **Pagination.** A list returns `{"entries": [...], "next_cursor": "..."}`.
  `next_cursor` is present only when a further page exists; pass it back
  as `cursor` to read that page. `limit` is 1 to 500 and defaults to 100.

## Assessments

### Assess a call

`POST /v1/assessments` asks `assessment.create`.

The body is an action: the proposed call, described by what the harness
knows before it runs.

```json
{
  "subject": "https://issuer.example|usr_42",
  "org": "org_7",
  "agent": "agt_ci",
  "session": "ses_9f",
  "call_id": "toolu_01",
  "tool": "bash",
  "effect": "external",
  "input": {"command": "git push origin main"},
  "target": {"kind": "ref", "id": "refs/heads/main", "sensitivity": 0.9},
  "machine": {"kind": "sandbox", "credentialed": true},
  "signals": [
    {"source": "rules/1", "score": 0.4},
    {"source": "judge/claude-auto-mode", "verdict": "allow"}
  ],
  "ceiling": "allow",
  "at": "2026-09-28T10:02:11Z"
}
```

| Field | Required | |
|---|---|---|
| `subject` | yes | the person the agent acts for: the token issuer and `sub`, joined by a pipe |
| `org` | yes | the organization whose cost profile applies |
| `agent`, `session` | no | who proposed the call |
| `call_id` | yes | the harness's id for the call; the idempotency key with `subject` |
| `tool` | yes | the tool's name as the harness knows it |
| `effect` | yes | `none`, `read`, `write`, or `external` |
| `input` | no | the tool's input; read only by deterministic feature extractors |
| `target` | no | `kind` (required when `target` is present), `id`, and `sensitivity` in [0, 1] |
| `machine` | yes | `kind` (`host` or `sandbox`) and `credentialed` |
| `signals` | no | each has a `source` and a `verdict` (`allow` or `block`), a `score` in [0, 1], or both |
| `ceiling` | yes | the most permissive verdict the harness's own rules leave open: `allow`, `flag`, `ask`, or `block` |
| `at` | yes | when the call was proposed |

The response is the assessment:

```json
{
  "id": "asm_01J8",
  "call_id": "toolu_01",
  "verdict": "ask",
  "approve": 0.58,
  "uncertainty": 0.71,
  "score": 0.42,
  "thresholds": {"ask_at": 0.1, "block_at": 0.75},
  "profile": "org_7",
  "source": "learned/3",
  "reason": "The chance this person approves (0.58) is inside the ask band (0.25 to 0.90) of profile org_7."
}
```

| Field | |
|---|---|
| `id` | the assessment's id, for the answer |
| `verdict` | what the harness applies: `allow`, `flag`, `ask`, or `block`. Never more permissive than the action's `ceiling`. |
| `approve` | the probability that the person approves the call |
| `uncertainty` | how uncertain the model is about that probability; a call like nothing seen before has a high value |
| `score` | `1 - approve`, the probability of a deny |
| `thresholds` | `ask_at` and `block_at` in score units, derived from the profile: below `ask_at` is an allow, above `block_at` a block, between them an ask |
| `profile` | the id of the cost profile the thresholds come from |
| `audit` | present when the call was sampled for review: `{"of": "allow", "probability": 0.05}`. An audited allow arrives as `flag`, an audited block as `ask`. |
| `source` | the model that produced the assessment, `learned/<version>` |
| `reason` | one sentence for the person, saying why the verdict is what it is |

Errors: `invalid_action`, `unknown_profile`, and the common errors below.
A `503 unavailable` means the call should be held for a person.

### Read one call

`GET /v1/assessments/{id}` asks `assessment.read`.

Returns the record of one call: `{"assessment": {...}, "action": {...},
"answer": {...}, "answered_by": "..."}`, with `answer` and `answered_by`
absent until someone has answered. `answered_by` is the subject of the
credential that sent the answer; it differs from the action's `subject`
when someone your authorization endpoint allows answers on the person's
behalf.

Errors: `unknown_assessment` and the common errors.

### List a person's calls

`GET /v1/assessments?subject=...` asks `assessment.list`.

Lists one subject's records, oldest first, in pages. With `pending=true`,
it lists only the calls that still need an answer: asks nobody has
answered yet, and flagged or audited calls awaiting review. This is what a
review queue shows.

| Parameter | |
|---|---|
| `subject` | required |
| `pending` | `true` for calls awaiting an answer; default `false` |
| `cursor` | the `next_cursor` of the previous page |
| `limit` | 1 to 500, default 100 |

```json
{
  "entries": [
    {"assessment": {"id": "asm_01J8", "verdict": "ask", "...": "..."}, "action": {"...": "..."}}
  ],
  "next_cursor": "c_8Qx2"
}
```

Errors: `invalid_query` and the common errors.

### Answer a call

`POST /v1/assessments/{id}/answer` asks `assessment.answer`.

Records the person's decision on an assessed call. The answer is
attributed to the subject of the credential that sends it.

```json
{"approve": true, "at": "2026-09-28T10:03:40Z"}
```

| Field | |
|---|---|
| `approve` | whether the person approves the call, or would have approved it for a reviewed call |
| `kind` | absent for a person's decision; `contest` for a later dispute of a call that already ran, which trains the model but never enters an error estimate |
| `at` | when the person answered |

Returns 204 with no body.

Errors: `invalid_answer`, `unknown_assessment`, `already_answered` (a call
takes one answer), and the common errors.

## Reliability

`GET /v1/reliability?subject=...&of=...&from=...&to=...` asks
`reliability.read`.

Reports how often automatic verdicts disagreed with one person over one
window: Limen's own with `of=learned`, or one signal source's, such as a
judge's, with `of=judge/claude-auto-mode`. `from` and `to` are RFC 3339
times, `from` before `to`.

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

| Field | |
|---|---|
| `false_allow` | the share of allowed calls the person would have denied |
| `false_block` | the share of blocked calls the person would have approved |
| `value` | the estimate, or `null` when no answer in the window had a known probability: not estimable, which is not zero |
| `interval` | a 95% interval, present with `value` |
| `labels`, `audits` | the answers the estimate rests on, and how many of them were audits |
| `unanswered` | calls sampled for review that nobody has answered yet; the estimate treats them as missing at random, so a count that is large next to `audits` weakens it |
| `certificate` | the profile's claim, a false-allow rate below `alpha` at confidence `1 - delta`: `clean` consecutive clean audits against the `needed` ones, and whether it `holds` |

Errors: `invalid_query` and the common errors.

## Cost profiles

`GET /v1/profiles/{org}` asks `profile.read`; `PUT /v1/profiles/{org}`
asks `profile.write`. The same two routes at
`/v1/profiles/{org}/agents/{agent}` read and write a profile for one
agent. The most specific profile wins.

```json
{
  "costs": {"false_allow": 10, "false_block": 4, "ask": 1},
  "audit_rate": 0.05,
  "certify": {"alpha": 0.01, "delta": 0.05}
}
```

The response is the stored profile with its `id`, set by the path
(`org_7`, `org_7/agents/agt_ci`), and the `thresholds` derived from its
costs: here `{"ask_at": 0.1, "block_at": 0.75}`. A caller never sets
either.

| Field | |
|---|---|
| `costs.false_allow` | the cost of running a call the person would deny; positive |
| `costs.false_block` | the cost of refusing a call the person would approve; positive |
| `costs.ask` | the cost of one interruption; positive |
| `audit_rate` | the share of automatic verdicts sampled for review, in [0, 1] |
| `certify.alpha`, `certify.delta` | the false-allow claim a reliability report tests: `0 < alpha <= 1`, `0 < delta < 1`; optional, and without it the claim is a rate below 1% at 95% confidence, which takes 300 clean audits |

Errors: `unknown_profile` on a read; `invalid_profile` and
`empty_ask_band` (costs at which asking is never worth it) on a write; and
the common errors.

## Replays

`POST /v1/replays` asks `replay.create`.

Assesses recorded calls in time order, each before learning its own
answer, and reports what Limen would have done. The body is
newline-delimited JSON (`Content-Type: application/x-ndjson`), one record
per line:

```json
{"action": {"...": "..."}, "verdict": "ask", "answer": {"approve": true, "at": "2026-09-02T09:12:03Z"}}
{"action": {"...": "..."}, "verdict": "allow"}
{"action": {"...": "..."}, "verdict": "flag", "audit": {"of": "allow", "probability": 0.05}, "answer": {"approve": false, "at": "2026-09-02T11:40:10Z"}}
```

`verdict` is what the harness applied at the time and `audit` is present
when the harness sampled the call for review. Without an audit, an ask,
and a flag your deterministic rules forced, count as answered with
probability one, and an allow or a block with probability zero.

```json
{
  "calls": 12840,
  "answered": 3102,
  "would": {"allow": 9811, "flag": 0, "ask": 2790, "block": 239},
  "allowed_denied": 41,
  "blocked_approved": 17,
  "false_allow": {"value": 0.021, "interval": [0.012, 0.034], "labels": 1904, "audits": 0, "unanswered": 0},
  "covered": 0.19
}
```

`covered` is the share of Limen's would-be allows that had a positive
probability of an answer in the record. The false-allow estimate speaks
for that share only. These numbers are illustrative.

Errors: `invalid_action` (a record's action is malformed),
`unknown_profile`, and the common errors.

## Forgetting a person

`POST /v1/subjects/forget` asks `subject.forget`.

```json
{"subject": "https://issuer.example|usr_42"}
```

Deletes everything Limen holds about the subject: answers, assessments,
and the model learned from them. Returns 204. It is idempotent: forgetting
a subject Limen knows nothing about also returns 204.

## Errors

Every error has one shape:

```json
{
  "error": {
    "code": "invalid_action",
    "message": "The action is missing a field or has a value out of range. Fix the fields listed in the details and send it again.",
    "details": {
      "request_id": "req_7f3c9a",
      "detail": "machine.kind is not one of host, sandbox",
      "fields": ["machine.kind"]
    }
  }
}
```

Branch on `code`. Show `message` to a person: it is fixed for each code.
`details` is for whoever debugs the request: the request id, what was
observed and expected, and the fields at fault.

| Code | Status | Message |
|---|---|---|
| `invalid_action` | 400 | The action is missing a field or has a value out of range. Fix the fields listed in the details and send it again. |
| `invalid_answer` | 400 | The answer is missing a field or has a value out of range. Fix the fields listed in the details and send it again. |
| `invalid_profile` | 400 | The cost profile is missing a field or has a value out of range. Fix the fields listed in the details and save it again. |
| `invalid_query` | 400 | The report request is missing a field or has a value out of range. Fix the fields listed in the details and ask again. |
| `empty_ask_band` | 422 | With these costs, asking is never worth it, so Limen would never ask. Raise the cost of a wrong allow or a wrong block, or lower the cost of asking. |
| `unknown_assessment` | 404 | There is no assessment with this id. Assess the call first, then send the answer for the id it returned. |
| `unknown_profile` | 404 | There is no cost profile with this id. Create it first, or use the organization's default profile. |
| `already_answered` | 409 | This call already has an answer. Each call takes one answer; a later dispute is sent as a contest. |
| `unauthenticated` | 401 | The request carries no valid credential. Sign in again and retry. |
| `forbidden` | 403 | You do not have permission for this. Ask an administrator of the organization for access. |
| `rate_limited` | 429 | Too many requests. Wait a moment and try again. |
| `internal` | 500 | Something failed on Limen's side. Try again; if it keeps failing, report it with the request id. |
| `authorizer_unavailable` | 503 | The permission service is unavailable. Try again shortly. |
| `unavailable` | 503 | Limen is unavailable right now. Hold the call for a person and try again shortly. |

The common errors every route can return are `unauthenticated`,
`forbidden`, `internal`, and one of the two 503 codes; the assessment,
answer, list, and reliability routes can also return `rate_limited`.

## Permissions

Each route asks the installation's authorizer for one action. The table
is also Limen's vocabulary for the authorizer contract its open cores
share.

| Action | Resource kind | Route |
|---|---|---|
| `assessment.create` | Assessment | `POST /v1/assessments` |
| `assessment.read` | Assessment | `GET /v1/assessments/{id}` |
| `assessment.answer` | Assessment | `POST /v1/assessments/{id}/answer` |
| `assessment.list` | Assessment | `GET /v1/assessments` |
| `reliability.read` | Reliability | `GET /v1/reliability` |
| `profile.read` | Profile | `GET /v1/profiles/{org}`, `GET /v1/profiles/{org}/agents/{agent}` |
| `profile.write` | Profile | `PUT /v1/profiles/{org}`, `PUT /v1/profiles/{org}/agents/{agent}` |
| `replay.create` | Replay | `POST /v1/replays` |
| `subject.forget` | Subject | `POST /v1/subjects/forget` |
