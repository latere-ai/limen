---
title: "HTTP API: routes, bodies, statuses, the authorizer action and resource fields per route, subject binding, limits"
status: drafted
track: core
depends_on: [001-architecture.md, 002-contract.md]
affects: [api/, authorizer/, client/, cmd/limend/]
effort: medium
created: 2026-09-28
updated: 2026-10-02
author: changkun
---

# HTTP API

## Overview

`limend` serves the contract of [[002-contract]] over HTTP for decision
points that do not embed the engine: hosted agent runners, hook
commands, decision points written in other languages. `client/` is the
Go implementation of `limen.Service` over this API, so a Go decision
point changes nothing but its constructor to move between the engine and
the server. `api/openapi.yaml` is the same surface as an OpenAPI 3.1
document.

## Design

### Routes

`limend` serves the routes under `/v1`. A platform that serves several
services behind one origin may mount them under a prefix of its own; the
server is told its public base so the links it writes carry it.

| Method | Path | Body | Success | Authorizer action | Contract |
|---|---|---|---|---|---|
| POST | `/v1/predictions` | `Action` | 200 `Prediction` | `case.predict` | `Predictor.Predict` |
| POST | `/v1/decisions` | `Decision` | 204 | `case.decide` | `Ledger.Record` |
| POST | `/v1/answers` | `Answer` | 204 | `case.answer` | `Ledger.Answer` |
| GET | `/v1/cases?org&subject&action_id&pending&cursor&limit` | none | 200 `Page` | `case.list` | `Ledger.Cases` |
| GET | `/v1/reliability?org&subject&of&from&to` | none | 200 `Reliability` | `reliability.read` | `Ledger.Reliability` |
| GET | `/v1/profiles?org&kind&agent` | none | 200 `Profile` (the effective one) | `profile.read` | `Admin.Profile` |
| PUT | `/v1/profiles` | `Profile` | 200 `Profile` (as stored) | `profile.write` | `Admin.PutProfile` |
| POST | `/v1/replays?keep` | NDJSON of `ReplayRecord` | 200 `ReplayReport` | `replay.create` | `Admin.Replay` |
| POST | `/v1/subjects/forget` | `{"org", "subject"}` | 204 | `subject.forget` | `Admin.Forget` |

Every route answers the errors of [[002-contract]] that its contract
method can return, plus `unauthenticated`, `forbidden`, `body_too_large`
for a body past its limit, `internal` and `authorizer_unavailable`; the
OpenAPI document lists them per operation. `rate_limited` is reserved for
an installation that puts a rate limit in front of `limend`, which itself
limits nothing.

### The authorizer

Each route asks the installation's authorization endpoint
(`latere.ai/x/pkg/authz`) one action of `authorizer.Vocabulary()` before
it acts, about a resource whose kind and fields are fixed per action:

| Action | Resource kind | Resource fields |
|---|---|---|
| `case.predict`, `case.decide`, `case.answer`, `case.list` | `Case` | `org`, `subject` |
| `reliability.read` | `Reliability` | `org`, `subject` |
| `profile.read`, `profile.write` | `Profile` | `org`, `action_kind`, `agent` (empty when unscoped). The field is `action_kind`, not `kind`, because the shared authorizer's flat wire object reserves `kind` for the resource kind |
| `replay.create` | `Replay` | `org`, asked once for each organization the records name, because the route takes no `org` of its own |
| `subject.forget` | `Subject` | `org`, `subject` |

With no endpoint configured, `limend` applies the shared owner policy: a
caller acts on cases and reports whose `subject` is the caller's own
rendered subject, admins act on everything, and every other request is
refused.

### Subject binding and relays

A prediction, a decision and an answer name a subject, and in general
the caller is not that subject: a harness predicts for the person its
agent works for. The authorizer decides whether the caller may act for
the subject, from the resource fields above; nothing in a body is
trusted to grant it. A caller that may `case.answer` for a subject may
set `Answer.by` to say who answered; without that permission `by` is
ignored and the answerer is the caller. Decisions carry the review
probability the ledger weights answers by, so `case.decide` is granted
to decision points and not to the people who answer.

### Ordering and pagination

`GET /v1/cases` returns a subject's cases oldest first by the time Limen
received the action, `{"entries": [...], "next_cursor": "..."}`, where
`next_cursor` is present only when a further page exists. `limit` runs
from 1 to 500 with a default of 100; a value outside is `invalid_query`.
`pending=true` narrows the page to actions whose decision was shown
(`flag` or `ask`) and that have no review yet: the review queue.
`action_id` narrows it to one case.

### Bodies and limits

- Request bodies are JSON (`application/json`), except a replay, which
  is newline-delimited JSON (`application/x-ndjson`), one `ReplayRecord`
  per line, at most 100000 records; beyond that the server answers
  `body_too_large` before learning anything.
- Every other body is at most 1 MiB; an `Action` is bounded further by
  its 64 attributes.
- Receivers ignore unknown fields, so a newer client works against an
  older server; known fields are validated strictly, and a value that
  breaks a rule is the route's `invalid_*` code naming every field at
  fault.
- `GET /v1/profiles` answers the effective profile for the scope, the
  default included (`"default": true`), and never 404. `PUT` derives
  `thresholds`, fills a zero `certify` with the default, and ignores a
  sent `thresholds` or `default`.
- Times in bodies are RFC 3339. Limen stamps its own receive times;
  `ReplayRecord.at` is the one time a caller supplies, and only for
  history.

### Errors

The error body is the family's envelope:

```json
{"error": {"code": "invalid_action", "message": "<the code's fixed sentence>",
  "details": {"request_id": "...", "detail": "<developer detail>", "fields": ["kind"]}}}
```

`message` is `Code.Message()`, `detail` is the `*Error`'s detail, and
`fields` its fields. A 503 answer to a prediction means the decision
point holds the action for a person (`FailClosed` does this for a Go
caller).

### The OpenAPI document and its test

`api/openapi.yaml` is written by hand and held to the code by
`api/openapi_test.go`, which fails when:

- a schema's properties differ from the JSON names of its Go type
  (embedded structs flattened), or a field the encoder always writes is
  not `required`;
- an enum differs from the Go constants (verdicts, effects);
- a response for an error code differs from `"<status> <code>:
  <message>"`, or a code has no response;
- an operation's `x-limen-action` is not in the vocabulary, an action is
  asked by no route, or an error response sits under a status that is not
  its code's.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | The OpenAPI document agrees with the Go types, the error table and the vocabulary in the four ways listed | `api/openapi_test.go` |
| 2 | Every route asks exactly its action with exactly the resource fields above before acting, and acts on nothing when the authorizer refuses or is unavailable | `limend` handler tests against `authz/stub` |
| 3 | A caller the authorizer does not allow for a subject cannot predict, decide, answer, list or read reliability for that subject | the same tests |
| 4 | `Answer.by` is recorded only for a caller allowed `case.answer` on the case | a handler test |
| 5 | A replay of 100001 records is `body_too_large` and learns nothing; an invalid record is `invalid_replay` naming its line | handler tests |
| 6 | An unknown request field is ignored, and a known field out of range is the route's `invalid_*` naming it | handler tests |
| 7 | `client/` satisfies `limen.Service` and passes the conformance suite against a running `limend` | an e2e test with `conformance.Run` ([[006-conformance]]) |
