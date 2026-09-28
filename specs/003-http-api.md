---
title: "HTTP API: routes, bodies, the error envelope, pagination, idempotency, the authorizer action per route"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-contract.md
affects: [api/openapi.yaml, authorizer/, client/, cmd/limend/, docs/api.md]
effort: medium
created: 2026-09-28
updated: 2026-09-28
author: changkun
---

# HTTP API

## Overview

[[002-contract]] is the contract for a harness written in Go. This spec
is the same contract for a caller in any language: every route `limend`
serves, the body each takes and returns, the status codes, the error
envelope, pagination, idempotency, and the authorizer action each route
asks. `api/openapi.yaml` states it as an OpenAPI 3.1 document, and
`client/` is the Go `Gate`, `Reviews` and `Admin` over it, so a harness
switches between the engine in process and a running `limend` without
changing a call site.

The API has no hostname in it. `limend` serves every route under `/v1`;
a platform that mounts Limen under a prefix of its own rewrites the
prefix away before the request reaches `limend`, and a self-hoster
mounts it at the root.

## Design

### The route table

| Method | Path | Request body | Success | Authorizer action | Go method |
|---|---|---|---|---|---|
| POST | `/v1/assessments` | `Action` | 200 `Assessment` | `assessment.create` | `Gate.Assess` |
| GET | `/v1/assessments` | none | 200 `RecordPage` | `assessment.list` | `Reviews.List` |
| GET | `/v1/assessments/{id}` | none | 200 `Record` | `assessment.read` | `Reviews.Record` |
| POST | `/v1/assessments/{id}/answer` | `Answer` | 204 | `assessment.answer` | `Gate.Answer` |
| GET | `/v1/reliability` | none | 200 `Reliability` | `reliability.read` | `Gate.Reliability` |
| GET | `/v1/profiles/{org}` | none | 200 `Profile` | `profile.read` | `Admin.Profile` |
| PUT | `/v1/profiles/{org}` | `Profile` | 200 `Profile` | `profile.write` | `Admin.PutProfile` |
| GET | `/v1/profiles/{org}/agents/{agent}` | none | 200 `Profile` | `profile.read` | `Admin.Profile` |
| PUT | `/v1/profiles/{org}/agents/{agent}` | `Profile` | 200 `Profile` | `profile.write` | `Admin.PutProfile` |
| POST | `/v1/replays` | NDJSON of `ReplayRecord` | 200 `ReplayReport` | `replay.create` | `Admin.Replay` |
| POST | `/v1/subjects/forget` | `{"subject": "..."}` | 204 | `subject.forget` | `Admin.Forget` |

Every operation in `api/openapi.yaml` carries `x-limen-action` with its
action, and a test holds the set of those values equal to
`authorizer.Vocabulary()`, so a route cannot ship without an action an
authorization endpoint can be asked about, and an action cannot exist
without a route.

`GET /openapi.json` serves the document without a token, as the other
open cores do: a route name is not a secret.

### Bodies

Request and response bodies are `application/json`, UTF-8, and decode
into the types of [[002-contract]] strictly: an unknown member is refused
with the route's `invalid_*` code naming it, which is what
`additionalProperties: false` in the document says, so a misspelled
field is an error rather than a silent default. Times are RFC
3339 with a time zone. `POST /v1/replays` takes `application/x-ndjson`,
one `ReplayRecord` per line, in any order: the replay sorts them by
`action.at`.

A profile's id is set by the path, `org` or `org/agents/agent`, and is
`readOnly` in the document; a `PUT` body's `id` and `thresholds` are
ignored, and the response carries the stored id and the thresholds
derived from the costs. A profile saved without `certify` stores
`DefaultCertify`, a false-allow rate below 1% at 95% confidence, which
takes 300 clean audits to certify.

### Assess is idempotent

`POST /v1/assessments` answers 200 both for a new assessment and for a
repeat of the action's subject and call id, and a repeat returns the
first assessment byte for byte, audit sample included ([[002-contract]]
says why a second draw would bias every estimate). A repeat whose body
differs from the first is still answered with the first assessment: the
call id names the call, and the first description of it is the one the
harness acted on. The difference is logged at warn level with both
bodies' digests.

### Answers

`POST /v1/assessments/{id}/answer` records the person's decision or a
contest. Who answered is the verified subject of the credential; the
body names no one. An assessment takes at most one decision and at most
one contest; a second of either is 409 `already_answered`. A contest is
accepted on any assessment, including one with no decision, because the
common contest is a dispute of a call that ran without anyone being
asked. An answer from a credential whose subject differs from the
action's subject is recorded only when the authorizer allows
`assessment.answer` on that assessment for that credential, and the
record keeps both subjects: the action's `subject` and the answerer's
`answered_by`.

### Listing and pagination

`GET /v1/assessments?subject=...` lists one subject's records oldest
first; `pending=true` narrows the page to calls still waiting for an
answer: asks nobody has answered and flagged or audited calls awaiting
review. The page is the family's list envelope:

```json
{"entries": [{"assessment": {}, "action": {}, "answer": null}],
 "next_cursor": "01J8R4..."}
```

`entries` is never null and is empty on an empty page. `next_cursor` is
present only when a further page exists, so its presence is the only
has-more signal; a client sends it back as `?cursor=` unchanged, and its
content is the server's. `?limit=` defaults to 100 and is capped at 500;
a limit outside `[1, 500]` is 400 `invalid_query` rather than a silent
clamp. Pagination is keyset on assessment time and id, so a page is
stable under concurrent writes.

### Reliability

`GET /v1/reliability?subject=&of=&from=&to=` takes all four parameters;
`of` is `learned` or a signal source such as `judge/claude-auto-mode`,
and `from` must be before `to`. A rate with no answer of known
probability in the window is `"value": null`, never `0`. Each estimate
carries `unanswered`, the calls in the window sampled for review that
nobody has answered, so a reader sees when the audits behind a rate are
thin.

### Replays

`POST /v1/replays` assesses the records in time order, each before its
own answer is learned, in a scratch model seeded empty. It applies
nothing, stores nothing, and never touches the subject's live model, so
an operator can replay a history on a production installation without
changing what it decides. A record that fails `Action.Validate` fails the
whole replay with 400 `invalid_action` naming the line in `details.detail`.

### The error envelope

The envelope is the one the other open cores write with
`latere.ai/x/pkg/httpjson`:

```json
{"error": {"code": "invalid_action",
  "message": "The action is missing a field or has a value out of range. Fix the fields listed in the details and send it again.",
  "details": {"request_id": "req_01J8R4...",
    "detail": "machine.kind is not one of host, sandbox",
    "fields": ["machine.kind"]}}}
```

`code` and its status come from the table of [[002-contract]], and
`message` is that code's fixed sentence, never interpolated.
`details.request_id` is always present; `details.detail` is the
developer sentence, `Error.Detail`; `details.fields` is `Error.Fields`
when the code names fields. Every route lists in the document exactly
the codes it can answer.

### The request id

Every request has one, `req_` and a ULID. A client's `X-Request-Id` of
at most 128 printable ASCII characters is kept, anything else replaced.
It is the `details.request_id` of an error, the request id the
authorizer sees, and the `request_id` of the request's log line, and
every response carries it back in `X-Request-Id`.

### Identity

Every route but `GET /openapi.json` requires a bearer token from an
issuer the operator lists, verified with `latere.ai/x/pkg/authkit/jwt`
against the audience `limen`. `limend` then asks the authorization
endpoint, through `latere.ai/x/pkg/authz`, for the route's action on the
route's resource, with the verified claims; a refusal is 403 `forbidden`
and an endpoint that does not answer is 503 `authorizer_unavailable`,
never an allow. Without an endpoint configured the owner policy of
`latere.ai/x/pkg/authz` applies ([[001-architecture]]).

### The document and its test

`api/openapi.yaml` is written by hand while the tree holds no server.
A test in `api/` holds it to the code: every schema's properties and
required members against the JSON tags of the Go type it names, every
error response against `Codes()`, `Code.Status()` and `Code.Message()`,
and the `x-limen-action` values against `authorizer.Vocabulary()`. When
`limend` lands, the document is generated from its route table and the
Go types, as the other open cores generate theirs, the committed file is
checked against a fresh generation, and the served `GET /openapi.json`
is the same document.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | Every schema in `api/openapi.yaml` names exactly the JSON members of its Go type, with the same required set | a test in `api/` reading the document and reflecting over the types |
| 2 | Every error response in the document carries the status and the sentence `errors.go` gives its code, and every code is used by at least one route | the same test |
| 3 | The set of `x-limen-action` values equals `authorizer.Vocabulary()`'s actions, one per operation | the same test |
| 4 | A repeat assess returns the first assessment unchanged, including when its body differs | a `limend` e2e test and [[006-conformance]] |
| 5 | A second decision or a second contest is 409 `already_answered`; a contest without a decision is accepted | [[006-conformance]] |
| 6 | A list page carries `entries` always and `next_cursor` only when a further page exists; a limit outside `[1, 500]` is 400 `invalid_query` | a `limend` e2e test |
| 7 | A replay changes no stored record and no live assessment | a `limend` e2e test comparing the store before and after |
| 8 | A request with no token is 401, a refused action 403, an unanswering authorizer 503 `authorizer_unavailable` | `limend` tests against the stub authorizer of `latere.ai/x/pkg/authz/stub` |
| 9 | `client/` passes [[006-conformance]] against a running `limend` | the conformance tier |
