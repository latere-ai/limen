# API

The HTTP API of Limen's server, `limend`, for a decision point that does
not embed the engine. The same routes are in the
[OpenAPI document](../api/openapi.yaml); the Go interfaces a Go decision
point calls are described in [Integrating](integrating.md), and running
the server in [Running limend](running.md).

## Conventions

- **Base path.** The server serves every route under `/v1`. A platform
  that hosts Limen may mount it under a prefix of its own.
- **Authentication.** Every request carries a bearer token from an issuer
  the installation lists, for the audience it verifies:
  `Authorization: Bearer <token>`. Limen verifies the token and asks the
  installation's authorization endpoint whether the caller may take the
  route's action, named on each route below, before it acts.
- **Bodies.** Requests and responses are JSON, except the replay upload,
  which is newline-delimited JSON. Times are RFC 3339. A receiver ignores
  fields it does not know, so a newer client can talk to an older server;
  the fields it knows are validated strictly, and every field at fault is
  named in one error.
- **Keys.** Every record is keyed by `org`, `subject`, and `action_id`, the
  decision point's own id for the action, unique within the organization
  and subject. There are no server-assigned ids.
- **Idempotency.** Predicting the same action again returns the first
  prediction, with status 200. Predicting a different action under a key
  already used is refused with `action_conflict`. An action takes one
  decision; a second is refused with `already_decided`.
- **Order.** A prediction is optional; a decision is required before an
  answer, which is refused with `no_decision` otherwise.
- **Time.** Limen stamps each record when it arrives and counts each
  person's decisions itself; no request carries a time except a replayed
  record.
- **Pagination.** A list returns `{"entries": [...], "next_cursor": "..."}`.
  `next_cursor` is present only when a further page exists; pass it back as
  `cursor` to read that page. `limit` is 1 to 500 and defaults to 100.

## Predictions

`POST /v1/predictions` asks `case.predict`.

The body is an action, described by what the decision point observes
before it runs.

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "action_id": "ses_9f/toolu_01",
  "actor": {"kind": "agent", "id": "agt_ci", "session": "ses_9f"},
  "kind": "tool",
  "name": "bash",
  "effect": "external",
  "reversible": false,
  "input": {"command": "git push origin main"},
  "target": {"kind": "ref", "id": "refs/heads/main", "sensitivity": 0.9},
  "environment": {"kind": "sandbox", "credentialed": true},
  "attributes": {"protected": true},
  "signals": [
    {"source": "rules/1", "score": 0.4},
    {"source": "classifier/claude-auto-mode", "verdict": "allow"}
  ]
}
```

| Field | Required | |
|---|---|---|
| `org`, `subject`, `action_id` | yes | the key |
| `actor` | no | who proposed the action: `kind` (required when `actor` is present), `id`, `session` |
| `kind`, `name` | yes | the domain and the operation |
| `effect` | yes | `none`, `read`, `write`, or `external` |
| `reversible` | no | whether the action can be undone |
| `input` | no | any JSON; read only by deterministic feature extractors and never stored |
| `target` | no | `kind` and `id` (both required when `target` is present), and `sensitivity` in [0, 1] |
| `environment` | no | `kind` and `credentialed` |
| `attributes` | no | up to 64 strings, finite numbers, or booleans |
| `signals` | no | each has a `source` and a `verdict`, a `score` in [0, 1], or both |

The response is the prediction:

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "action_id": "ses_9f/toolu_01",
  "verdict": "ask",
  "approve": 0.58,
  "uncertainty": 0.71,
  "thresholds": {"allow_above": 0.9, "block_below": 0.25},
  "profile": {"org": "org_7"},
  "audit_rate": 0.05,
  "source": "learned/3",
  "reason": "The chance this person approves (0.58) is inside the ask band (0.25 to 0.90)."
}
```

| Field | |
|---|---|
| `verdict` | Limen's suggestion: `allow`, `ask`, or `block`. Never `flag`, which is the decision point's when it draws a review. |
| `approve` | the probability that the person approves the action |
| `uncertainty` | how uncertain the model is about that probability; an action like nothing seen before has a high value |
| `thresholds` | from the profile: above `allow_above` the suggestion is allow, below `block_below` block, between them ask |
| `profile` | the scope of the profile that applies |
| `audit_rate` | the share of automatic verdicts the profile asks a person to review at random |
| `source` | the model that made the prediction, `learned/<version>` |
| `reason` | one sentence for the person's record; never shown to an agent |

Errors: `invalid_action`, `action_conflict`, `body_too_large`, and the
common errors. A `503` means the decision point should hold the action for
a person.

## Decisions

`POST /v1/decisions` asks `case.decide`.

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "action_id": "ses_9f/toolu_01",
  "verdict": "flag",
  "review_probability": 0.05,
  "source": "topos/progressive"
}
```

| Field | Required | |
|---|---|---|
| `org`, `subject`, `action_id` | yes | the key |
| `verdict` | yes | the verdict applied: `allow`, `flag`, `ask`, or `block` |
| `review_probability` | yes | the probability, fixed before the action ran, that a person sees it: positive for `flag` and `ask`, below one for `allow` and `block`. [The ledger](ledger.md#the-review-probability) lists every case. |
| `source` | yes | the policy that decided |
| `action` | when Limen has no prediction for the action | the action, as for a prediction, with the same key; if Limen has a prediction, it must be the same action |

Returns 204 with no body.

Errors: `invalid_decision`; `already_decided` (the action already has a
decision) or `action_conflict` (the action differs from the one
predicted), both 409; `body_too_large`; and the common errors.

## Answers

`POST /v1/answers` asks `case.answer`.

```json
{"org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01", "approve": true}
```

| Field | Required | |
|---|---|---|
| `org`, `subject`, `action_id` | yes | the key |
| `approve` | yes | whether the person approves the action, or would have, for a reviewed action that already ran |
| `by` | no | who answered, when the caller relays the answer for someone else. Honored only when the authorization endpoint allows the caller `case.answer` for the subject; otherwise the answerer is the caller. |

Returns 204 with no body. The first answer to an action whose decision
showed a person is its review and enters error estimates; every other
answer trains the model only.

Errors: `invalid_answer`, `no_decision`, and the common errors.

## Cases

`GET /v1/cases?org=...&subject=...` asks `case.list`.

Lists one subject's cases in one organization, oldest first. Each case is
the action without its input, the prediction if one was made, the
decision, and the answers.

| Parameter | |
|---|---|
| `org`, `subject` | required |
| `action_id` | narrows the page to that action's case |
| `pending` | `true` for the review queue: actions a decision showed the person that have no review yet |
| `cursor` | the `next_cursor` of the previous page |
| `limit` | 1 to 500, default 100 |

```json
{
  "entries": [
    {
      "action": {"org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01", "kind": "tool", "name": "bash", "effect": "external"},
      "prediction": {"verdict": "ask", "approve": 0.58, "...": "..."},
      "decision": {"verdict": "ask", "review_probability": 1, "source": "topos/progressive", "...": "..."},
      "answers": [{"approve": true, "by": "https://issuer.example|usr_42", "at": "2026-10-02T10:03:40Z", "review": true}]
    }
  ],
  "next_cursor": "c_8Qx2"
}
```

An answer's `review` is true for the answer that reviews an action shown to
the person, the one answer that enters error estimates.

Errors: `invalid_query` and the common errors.

## Reliability

`GET /v1/reliability?org=...&subject=...&of=...&from=...&to=...` asks
`reliability.read`.

Reports how often one source's verdicts disagreed with one person over one
window: `of=learned` for Limen's suggestions, `of=applied` for the verdicts
decision points applied, a decision's `source` such as `rules/1`, or a
signal's such as `classifier/claude-auto-mode`. `from` and `to` are RFC 3339
times, `from` before `to`.

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "of": "classifier/claude-auto-mode",
  "from": "2026-09-01T00:00:00Z",
  "to": "2026-10-01T00:00:00Z",
  "false_allow": {"value": 0.10, "interval": [0.03, 0.25], "covered": 0.96, "reviews": 24, "audits": 9, "unanswered": 1},
  "false_block": {"value": 0.49, "interval": [0.31, 0.67], "covered": 1, "reviews": 31, "audits": 2, "unanswered": 0},
  "certificate": {"alpha": 0.01, "delta": 0.05, "clean": 112, "needed": 300, "holds": false}
}
```

| Field | |
|---|---|
| `false_allow` | the share of the source's allows the person would have denied |
| `false_block` | the share of the source's blocks the person would have approved |
| `value` | the estimate, or `null` when none of those actions could have been seen: not estimable, which is not zero |
| `interval` | a 95% interval, present with `value` |
| `covered` | the share of the source's allows (or blocks) that had a chance of being seen; the estimate speaks for that share only |
| `reviews`, `audits` | the reviews the estimate rests on, and how many came from random review |
| `unanswered` | actions shown to the person with no review yet |
| `certificate` | the profile's claim, a false-allow rate below `alpha` at confidence `1 - delta`: `clean` consecutive clean random reviews of the source's allows in the window against the `needed` ones, and whether it `holds` |

The numbers above are illustrative. Errors: `invalid_query` and the common
errors.

## Cost profiles

`GET /v1/profiles?org=...&kind=...&agent=...` asks `profile.read`;
`PUT /v1/profiles` asks `profile.write`.

```json
{
  "org": "org_7",
  "kind": "spend",
  "costs": {"false_allow": 50, "false_block": 4, "ask": 1},
  "audit_rate": 0.05,
  "certify": {"alpha": 0.01, "delta": 0.05}
}
```

A `GET` returns the profile that applies to the scope: the most specific
saved one, in the order kind and agent, agent, kind, organization, or the
built-in default, marked `"default": true`. A `PUT` saves the profile for
the scope its body names. Both responses carry the `thresholds` derived
from the costs, here `{"allow_above": 0.98, "block_below": 0.25}`; a value
sent for `thresholds` or `default` is ignored.

| Field | |
|---|---|
| `org` | required; `kind` and `agent` narrow the scope |
| `costs.false_allow` | the cost of running an action the person would deny; more than twice `costs.ask` |
| `costs.false_block` | the cost of refusing an action the person would approve; more than twice `costs.ask` |
| `costs.ask` | the cost of one interruption; positive |
| `audit_rate` | the share of automatic verdicts reviewed at random, in [0, 1] |
| `certify` | optional: `alpha` in (0, 1] and `delta` in (0, 1). Without it, the claim is a rate below 1% at 95% confidence, which takes 300 clean reviews. |

Errors: `invalid_query` on a read; `invalid_profile` and `ask_too_costly`
(costs under which Limen would decide about someone it knows nothing about)
on a write; and the common errors.

## Replays

`POST /v1/replays` asks `replay.create`. Add `?keep=true` to learn the
records into the live models and the ledger; without it, the replay learns
in a scratch model and keeps nothing.

The body is newline-delimited JSON (`Content-Type: application/x-ndjson`),
one record per line, at most 100,000 records:

```json
{"at": "2026-09-02T09:12:03Z", "action": {"...": "..."}, "verdict": "ask", "review_probability": 1, "source": "rules/1", "approve": true}
{"at": "2026-09-02T09:15:44Z", "action": {"...": "..."}, "verdict": "allow", "review_probability": 0, "source": "rules/1"}
{"at": "2026-09-02T11:40:10Z", "action": {"...": "..."}, "verdict": "flag", "review_probability": 0.05, "source": "rules/1", "approve": false}
```

Limen predicts the records in the order of `at`, each before learning its
own answer, and reports what it would have suggested:

```json
{
  "records": 12840,
  "answered": 3102,
  "would": {"allow": 9811, "ask": 2790, "block": 239},
  "allowed_denied": 41,
  "blocked_approved": 17,
  "false_allow": {"value": 0.021, "interval": [0.012, 0.034], "covered": 0.19, "reviews": 1904, "audits": 0, "unanswered": 0},
  "kept": false
}
```

The numbers are illustrative. Errors: `invalid_replay` (naming the records
at fault), `body_too_large`, and the common errors.

## Forgetting a person

`POST /v1/subjects/forget` asks `subject.forget`.

```json
{"org": "org_7", "subject": "https://issuer.example|usr_42"}
```

Deletes everything Limen holds about the subject in the organization:
cases, answers, and the model learned from them. Returns 204. It is
idempotent: forgetting a subject Limen knows nothing about also returns
204.

Errors: `invalid_query` and the common errors.

## Errors

Every error has the same shape:

```json
{
  "error": {
    "code": "invalid_action",
    "message": "The action is missing a field or has a value out of range. Fix the fields listed in the details and send it again.",
    "details": {
      "request_id": "req_7f3c9a",
      "detail": "kind is empty; environment.kind is empty",
      "fields": ["kind", "environment.kind"]
    }
  }
}
```

Branch on `code`; show `message` to a person; keep `details` for whoever
debugs. Each code has one status and one sentence:

| Code | Status | Message |
|---|---|---|
| `invalid_action` | 400 | The action is missing a field or has a value out of range. Fix the fields listed in the details and send it again. |
| `invalid_decision` | 400 | The decision is missing a field or has a value out of range. Fix the fields listed in the details and send it again. |
| `invalid_answer` | 400 | The answer is missing a field. Fix the fields listed in the details and send it again. |
| `invalid_profile` | 400 | The cost profile is missing a field or has a value out of range. Fix the fields listed in the details and save it again. |
| `invalid_query` | 400 | The request is missing a field or has a value out of range. Fix the fields listed in the details and ask again. |
| `invalid_replay` | 400 | A replayed record is missing a field or has a value out of range. Fix the records listed in the details and replay again. |
| `ask_too_costly` | 422 | With these costs, Limen would decide an action about someone it knows nothing about without asking. Make each kind of error cost more than two interruptions. |
| `action_conflict` | 409 | This action id was already used for a different action. Give each action its own id. |
| `already_decided` | 409 | This action already has a decision. Each action is decided once. |
| `no_decision` | 409 | This action has no recorded decision. Record the decision first, then send the answer. |
| `unauthenticated` | 401 | The request carries no valid credential. Sign in again and retry. |
| `forbidden` | 403 | You do not have permission for this. Ask an administrator of the organization for access. |
| `body_too_large` | 413 | The request is larger than Limen accepts. Send it in smaller parts. |
| `rate_limited` | 429 | Too many requests. Wait a moment and try again. |
| `internal` | 500 | Something failed on Limen's side. Try again; if it keeps failing, report it with the request id. |
| `authorizer_unavailable` | 503 | The permission service is unavailable. Try again shortly. |
| `unavailable` | 503 | Limen is unavailable right now. Hold the action for a person and try again shortly. |

The common errors, which any route can return, are `unauthenticated`,
`forbidden`, `rate_limited`, `internal`, `authorizer_unavailable`, and
`unavailable`.

## Permissions

Each route asks the authorization endpoint for one action about one
resource. An endpoint written for Limen answers these and no others:

| Route | Action | Resource kind | Resource fields |
|---|---|---|---|
| `POST /v1/predictions` | `case.predict` | `Case` | `org`, `subject` |
| `POST /v1/decisions` | `case.decide` | `Case` | `org`, `subject` |
| `POST /v1/answers` | `case.answer` | `Case` | `org`, `subject` |
| `GET /v1/cases` | `case.list` | `Case` | `org`, `subject` |
| `GET /v1/reliability` | `reliability.read` | `Reliability` | `org`, `subject` |
| `GET /v1/profiles` | `profile.read` | `Profile` | `org`, `action_kind`, `agent` |
| `PUT /v1/profiles` | `profile.write` | `Profile` | `org`, `action_kind`, `agent` |
| `POST /v1/replays` | `replay.create` | `Replay` | `org` |
| `POST /v1/subjects/forget` | `subject.forget` | `Subject` | `org`, `subject` |

A profile's kind travels as `action_kind`, because the shared
authorizer's resource object reserves `kind` for the kind of the resource
itself.

A caller acting for its own subject is the common case. A decision point
that predicts, decides, or answers for the person it runs for, such as an
agent harness, is allowed the `Case` actions on that person's resources by
the endpoint, which is also what lets it relay an answer with `by`.
