# Documentation

These documents are for people who connect a decision point to Limen, run
Limen, or change Limen. A decision point is the software that decides if
an automated action runs. Examples:

- An agent harness that decides about a tool call.
- A proxy that decides about a network request.
- A service that decides about a push or a payment.

Limen is in production. The [README](../README.md#limen) tells you which
parts are ready. If a page describes a part that is not ready, the page
tells you.

## Terms

All documents use the terms on the [Terms](terms.md) page. Each term has
one meaning only.

## Connect a decision point to Limen

The first time, read these pages in this order.

| Page | Contents |
|---|---|
| [Integration](integrating.md) | The three calls. Actions for tool calls, network requests, pushes, and payments, and the session of an action. How to combine the suggested verdict of Limen with your rules, and how to use the two kinds of `ask`. How to record decisions and answers. How to use the ledger alone. Cost profiles. A start in four steps. Notes for Topos on a computer and hosted, a replay of its history, and notes for Claude Code and Codex. |
| [The ledger](ledger.md) | What Limen records, the meaning of the review probability, and how answers count. How Limen calculates the error rate of each source of decisions, and the calibration report. |
| [API](api.md) | Authentication, each route with an example, repeated requests, the order of calls, and pages of results. The errors, and the permission that each route needs. |
| [OpenAPI](../api/openapi.yaml) | The HTTP API as an OpenAPI 3.1 document. |

## Understand Limen

| Page | Contents |
|---|---|
| [Principles](principles.md) | The rules that each implementation obeys, the reason for each rule, and how Limen checks it. |
| [Theory](theory.md) | The model of the tolerance of a person, the decision rule and its thresholds, the kinds of `ask`, the cover rule and the weight of an answer, and forgetting. The estimates and the certificates, the calibration report, and what you can know about a judge. |

## Operate Limen

| Page | Contents |
|---|---|
| [Running limend](running.md) | How to get the server, its configuration, and its ledger directory. How to check that it runs, how to stop it, and how to run it in a container. |

## Change Limen

| Page | Contents |
|---|---|
| [`CONTRIBUTING.md`](../CONTRIBUTING.md) | How to build and test Limen, and how to send a change. |
| [`specs/`](../specs/README.md) | The design records, one for each module, with the reason for each decision. |
