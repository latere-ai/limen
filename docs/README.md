# Documentation

For people who connect a harness to Limen, run it, or change it.

Limen is pre-release. The contract (the Go types and interfaces, the HTTP
API, and the error codes) is written and is what these pages describe; the
engine, the server `limend`, the client, and the harness hooks are
specified but not built yet. Where a page describes behavior that only a
built component can have, it says so.

## Using Limen from a harness

Read these in order the first time.

| Page | |
|---|---|
| [Integrating](integrating.md) | in process or over HTTP; building an action from a tool call; applying each verdict; sending answers; choosing a cost profile; reading a reliability report; rolling out in four steps; notes for Topos, Claude Code, and Codex |
| [API](api.md) | authentication, every route with an example, idempotency, pagination, the errors, and the permission each route asks |
| [OpenAPI](../api/openapi.yaml) | the same surface as an OpenAPI 3.1 document |

## Understanding it

| Page | |
|---|---|
| [Principles](principles.md) | the twelve rules every implementation keeps, why each holds, and how it is checked |
| [Theory](theory.md) | the model of a person's tolerance, the decision rule and its thresholds, forgetting, audits and certificates, and what can be known about an LLM judge |

## Operating it

Installing and running `limend` will be documented here when the server
exists. Until then, [`specs/`](../specs/README.md) records how it is meant
to be built and deployed.

## Changing it

| Page | |
|---|---|
| [`CONTRIBUTING.md`](../CONTRIBUTING.md) | how to build, test, and send a change |
| [`specs/`](../specs/README.md) | the design records, one per module, with the reasoning behind each decision |
