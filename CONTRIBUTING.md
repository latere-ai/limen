# Contributing

Thank you for your interest. This file is for the people and the agents
that change Limen. Users read the [README](README.md) and
[`docs/`](docs/README.md). The design records, with the reason for each
decision, are in [`specs/`](specs/README.md).

Limen has three parts: the shared verdict vocabulary
(`latere.ai/x/pkg/verdict`), a ledger, and a predictor. The ledger records
what decision points decided and measures how often each source of
decisions is wrong. The predictor learns when a person must see an action.

The tree has these parts:

| Path | Contents |
|---|---|
| module root | The contract: the Go types and interfaces, and the error codes |
| `authorizer/` | The authorizer vocabulary |
| `api/openapi.yaml` | The HTTP API as an OpenAPI document |
| `estimate/` | The estimates that each ledger uses |
| `stub/` | A service in memory, for the tests of other projects |
| `conformance/` | The tests that each implementation must pass |
| `engine/` | The engine: the model, the stores, and the service |
| `client/`, `server/` | The HTTP client and the HTTP server |
| `cmd/limend`, `cmd/limen` | The server program and the replay command |
| `importer/topos` | The reader of Topos session logs for replays |

Two designs have no code in the tree: a hook command for Claude Code
and Codex ([spec 005](specs/005-adapters.md)), and a store that more
than one server can share ([spec 004](specs/004-engine.md)). Work on one
of them starts from its spec.

## Set up

You need Go 1.27 or newer, and `git`. You do not need other services.

```sh
make         # the quality gate: formatting, linting, the tests, coverage, the specs
make hooks   # run the same gate before each commit and each push
```

`make` needs only the Go toolchain and `git`. It gets each pinned tool
from a public module. For this reason, it gives the same result on your
computer and in CI.

## The quality gate

`make` runs all the gates of `go tool lateregate`:

- The format, the linter, and modernization.
- A build without cgo, and known vulnerabilities.
- The tests, with and without the race detector.
- Coverage of 90% or more in each package.
- The tests with only the toolchain on `PATH`, and with an empty
  temporary directory.
- The license notice, the dependency allow list, and the spec tree.

`go tool lateregate list` names the gates. `go tool lateregate <name>`
runs one gate.

Tests compare two documents with the code. A test compares
`api/openapi.yaml` with the Go types, the error table, and the authorizer
vocabulary ([spec 003](specs/003-http-api.md)). The spec gate compares the
index in `specs/README.md` with the frontmatter of the specs.

The root package imports only the standard library and the shared verdict
vocabulary (`latere.ai/x/pkg/verdict`). Each decision point imports the
root package and calls it while an action waits. `TestRootImports` and
`.lateregate.yaml` both check this rule. The numerical dependency of the
engine is only under `engine/`. To add an import, change
`.lateregate.yaml` and give the reason there.

A bug fix includes a test that fails without the fix. If a change lowers
a threshold or adds a waiver, write the reason in `.lateregate.yaml`.
Then a reviewer can see the exception.

## Send a change

1. Fork the repository.
2. Make a branch.
3. Make one logical change in each commit. Stage the files by name.
4. Write the commit subject as `scope: what changed`, in lowercase. For
   example: `engine: a prediction reads a fixed copy of the model`.
5. Open a pull request.

Maintainers push to `main` directly. The pipeline runs the gate on each
push and on each pull request. A maintainer cuts a release with
`go tool lateregate release vX.Y.Z`. After a rollout, `tools/smoke/release.sh`
checks the public route from the outside; set `BASE_URL` to run it by hand.

If you plan a large change, open an issue first. A reviewer can examine a
design more easily if its spec gives the reasons.

## Specs first

A feature starts as a spec. Its acceptance criteria are sentences that a
test can check. The implementation follows the spec. If the
implementation is different from the spec, record the difference in the
Outcome section of the spec, not only in the code. The names in a spec are
the names in the code: Go identifiers, JSON fields, error codes, routes,
and authorizer actions.

A small fix does not need a spec. These changes need a spec, and they
must change the related page in `docs/` in the same change:

- A change to a type or a field of the contract.
- A change to an error code, a route, or an authorizer action.
- A change to an invariant of [spec 001](specs/001-architecture.md).

The JSON name of a field never changes. A new field is optional. The
CHANGELOG names each incompatible change.

A change must keep the eight invariants of
[spec 001](specs/001-architecture.md):

1. A deny from the rules of a decision point is final.
2. If Limen fails, the action waits for a person.
3. The decision point sets the review probability. It makes the random
   selection one time and records the probability with the decision.
4. For an action that Limen knows nothing about, the suggestion is `ask`.
5. Limen never stores the input of an action.
6. The recorded decision sets the role of an answer.
7. A key identifies one action.
8. `Forget` removes all the data of a person in that organization.

The calculations in the contract come from the research in
[arXiv:2605.19151](https://arxiv.org/abs/2605.19151). These are the
thresholds from the costs, the ask range that contains one half, the
size of a certificate, and the error estimate. Lean 4 checks the
propositions of the paper at
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib).
A change to these calculations names the proposition that it follows, or
gives the reason why it is different.

## Three registers

Each sentence has one reader, and the register follows the reader. The
rule and the review checklist are in
[`docs/writing/registers.md`](https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md).
In this repository:

| Reader | Surfaces |
|---|---|
| User: a person, or a harness that shows Limen's output to one | `README.md`, `docs/`, `SECURITY.md`, the issue and pull request templates, the `message` of an API error, a prediction's `reason`, the output of `limend` and `limen` |
| Contributor: someone changing Limen | `specs/`, this file, package documentation, source comments, commit messages |
| Developer: someone debugging a running installation | an error's `details.detail` and `details.fields`, log lines, `Error.Error()` |

The sentence of each error code is in `errors.go`, next to the code. The
sentence does not change from error to error. The details contain the
values that change. Use American English. Do not use em dashes.

## Writing for users

Limen decides which actions run without a person. A reader who
misunderstands a document can let a harmful action run. Write each
technical document for developers, integrators, and operators to the
writing rules of ASD-STE100, Simplified Technical English. Use only the
terms in [`docs/terms.md`](docs/terms.md). These documents are the user
surfaces in the table above.

This is not certified STE. We apply its writing rules and keep our own
term list. We do not check words against the STE dictionary.

A website or a product page is not a technical document. Write it in the
language of its audience: the problem of the reader and what the reader
gets. Do not use the terms and the structure of the engineering.

Words:

- Use each term with the one meaning that `docs/terms.md` gives it. To
  add a term, add it to that page in the same change.
- Use `if` for a condition and `when` for a time. Use `must` for a
  requirement and `can` for a possibility. Do not use `may` or `should`.
- Do not use idioms, metaphors, or contractions. Write `for example`, not
  `e.g.`. Write `because`, not `since` or `as`.
- Write a verdict as code: `allow`, `flag`, `ask`, `block`.
- Do not put more than three nouns in a row.

Sentences and paragraphs:

- Write one topic in a sentence. A sentence that describes has 25 words
  or fewer. A step in a procedure has 20 words or fewer.
- Use the active voice and the present tense. Write a procedure as
  commands, with one command in each numbered step.
- Do not join two sentences with a semicolon.
- A paragraph has six sentences or fewer. Its first sentence gives its
  topic.
- Write a warning before the step that it applies to. Say what can go
  wrong and what to do.

## Report a vulnerability

Do not open an issue. [`SECURITY.md`](SECURITY.md) tells you where to send
the report.
