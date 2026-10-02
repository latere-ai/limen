# Contributing

Thanks for looking. This file is for people and agents changing Limen.
Users read the [README](README.md) and [`docs/`](docs/README.md), and the
design records with the reasoning behind each decision are in
[`specs/`](specs/README.md).

Limen is the oversight layer for automated actions: a shared verdict
vocabulary (`latere.ai/x/pkg/verdict`), a ledger that records what
decision points decided and how often each source of decisions is
wrong, and a predictor that learns when a person needs to see an action.
The tree holds the contract (the module root), the authorizer vocabulary
(`authorizer/`), the HTTP surface as an OpenAPI document
(`api/openapi.yaml`), and the specs. The estimators (`estimate/`), the
in-memory service (`stub/`), the conformance suite (`conformance/`) and
the engine (`engine/`) are being built from their specs; the HTTP client,
the server and the hook command follow. A change that starts one of them
starts from its spec.

## Getting set up

You need Go 1.27 or newer and `git`. Nothing else: the contract has no
services to run.

```sh
make         # the quality gate: formatting, linting, the suite, coverage, the specs
make hooks   # run the same gate before each commit and push
```

`make` needs only the Go toolchain and git. Everything it pins comes from
public modules, so it runs the same on your machine as in CI.

## The bar

`make` runs the whole gate (`go tool lateregate`): formatting, the
linter, modernization, a build without cgo, known vulnerabilities, the
suite with and without the race detector, per-package coverage at 90% or
more, the suite with only the toolchain on `PATH`, the suite against an
empty temporary directory, the license notice, the dependency allow list,
and the spec tree. `go tool lateregate list` names the gates and `go tool
lateregate <name>` runs one.

Two documents are checked against the code. `api/openapi.yaml` is held
to the Go types, the error table and the authorizer vocabulary by a test
([spec 003](specs/003-http-api.md)), and `specs/README.md`'s index is
held to the specs' frontmatter by the spec gate.

The root package reaches the standard library and the shared verdict
vocabulary (`latere.ai/x/pkg/verdict`) and nothing else, because every
decision point imports it into its hot path; `TestRootImports` and
`.lateregate.yaml` both hold that. The engine's numerical dependency
lives under `engine/` alone. A new import is a reviewed change to
`.lateregate.yaml`, with its reason, not a line in a diff.

A bug fix carries a test that fails without it. A change that lowers a
threshold or adds a waiver records the reason in `.lateregate.yaml`, so
the exception is reviewable rather than invisible.

## Sending a change

Fork the repository, work on a branch, and open a pull request. Keep one
logical change per commit, stage the files explicitly, and write the
subject in the imperative, saying what changed for whoever reads the log.
Maintainers push to `main` directly; the pipeline runs the gate on every
push and pull request.

If you are planning something large, open an issue first. A design that
lands without a spec is harder to review than one that arrives with the
reasoning attached.

## Specs first

A feature starts as a spec with acceptance criteria that are testable
sentences. The implementation follows the spec, and a divergence is
recorded in the spec's Outcome section rather than left in the code.
Names in a spec (Go identifiers, JSON fields, error codes, routes,
authorizer actions) are the names the code uses.

Small fixes do not need a spec. Anything that changes a type or a field
of the contract, an error code, a route, an authorizer action, or one of
the invariants of [spec 001](specs/001-architecture.md) does, and it
updates the matching page under `docs/` in the same change. A field's
JSON name never changes; a new field is optional; a breaking change is
named in the CHANGELOG.

Eight invariants of [spec 001](specs/001-architecture.md) are not
negotiable in a change: a deterministic deny is final; a failure asks;
the review probability is the decision point's, drawn once and recorded
with its decision; an action Limen knows nothing about is an ask; an
action's input is never stored; an answer's role follows from the
recorded decision; a key names one action; and forgetting a person
removes everything of theirs in that organization.

The arithmetic in the contract (thresholds from costs, the band that
contains one half, the certification size, the error estimate) comes
from the research in
[arXiv:2605.19151](https://arxiv.org/abs/2605.19151), whose propositions
are machine-checked in Lean 4 at
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib).
A change to that arithmetic cites the proposition it follows or says why
it departs from it.

## Three registers

Every sentence is written for one reader, and the register follows the
reader. The rule and the review checklist are in
[`docs/writing/registers.md`](https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md).
In this repository:

| Reader | Surfaces |
|---|---|
| User: a person, or a harness that shows Limen's output to one | `README.md`, `docs/`, the `message` of an API error, a prediction's `reason` |
| Contributor: someone changing Limen | `specs/`, this file, package documentation, source comments, commit messages |
| Developer: someone debugging a running installation | an error's `details.detail` and `details.fields`, log lines, `Error.Error()` |

An error code's sentence is fixed in `errors.go` beside the code and
never interpolates; what varies goes in the details. Spell in American
English, and write no em dashes.

## Reporting a vulnerability

Do not open an issue. [`SECURITY.md`](SECURITY.md) says where to send it.
