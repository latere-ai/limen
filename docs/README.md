# Documentation

For people who connect a decision point to Limen, run it, or change it. A
decision point is the code that lets an automated action run or not: an
agent harness deciding on a tool call, a proxy deciding on a network
request, a service deciding on a push or a payment.

Limen is pre-release. The [README](../README.md#limen) says which parts
are built. Where a page describes behavior only an unbuilt part can have,
it says so.

## Using Limen from a decision point

Read these in order the first time.

| Page | |
|---|---|
| [Integrating](integrating.md) | the three calls; building actions for tool calls, network requests, pushes, and payments; composing Limen's suggestion with your rules; recording decisions and answers; using the ledger alone; cost profiles; rolling out in four steps; notes for Topos, Claude Code, and Codex |
| [The ledger](ledger.md) | what Limen records, what a review probability means, how answers count, and how the error rate of any decision source is computed |
| [API](api.md) | authentication, every route with an example, idempotency, ordering, pagination, the errors, and the permission each route asks |
| [OpenAPI](../api/openapi.yaml) | the same surface as an OpenAPI 3.1 document |

## Understanding it

| Page | |
|---|---|
| [Principles](principles.md) | the rules every implementation keeps, why each holds, and how it is checked |
| [Theory](theory.md) | the model of a person's tolerance, the decision rule and its thresholds, forgetting, the estimators and certificates, and what can be known about an LLM judge |

## Operating it

Installing and running the server, `limend`, will be documented here when
the server exists. Until then, [`specs/`](../specs/README.md) records how
it is meant to be built and deployed.

## Changing it

| Page | |
|---|---|
| [`CONTRIBUTING.md`](../CONTRIBUTING.md) | how to build, test, and send a change |
| [`specs/`](../specs/README.md) | the design records, one per module, with the reasoning behind each decision |
