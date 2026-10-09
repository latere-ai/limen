# Changelog

Each release tag has a section in this file. The section is the body of
the GitHub release. The pre-push hook and the release workflow refuse a
tag without a section. Add each change under `Unreleased` when it lands.
`lateregate release vX.Y.Z` makes that text the section of the tag, and
then commits, tags, and pushes.

A section tells the users of the release what changed. It does not repeat
the commit log.

## Unreleased

This is the first version of Limen. Limen has three parts:

- The shared verdict vocabulary, `latere.ai/x/pkg/verdict`.
- A ledger. It measures each source of decisions against the answers of
  a person.
- A predictor. It learns the tolerance of each person from the ledger.

The contract:

- `Predictor`, `Ledger`, and `Admin`, the three interfaces.
- `Action`, a general description of an action: a kind and a name. Its
  kinds include tool calls, network access, repository writes, payments,
  and deploys. Its key has three fields: the organization, the person, and
  the action ID of the decision point.
- `Prediction`. It suggests `allow`, `ask`, or `block` with the thresholds
  of a cost profile.
- `Decision`. The decision point records it with the probability that a
  person sees the action.
- `Answer`. The decision for an action sets the role of its answer in an
  estimate.
- Reliability reports for each source, replays, the error codes with
  their messages, and `FailClosed`.
- Limen accepts a cost profile only if each type of error costs more than
  two asks. For this reason, if Limen has no answers from a person, it
  suggests `ask`.
- The HTTP API as an OpenAPI 3.1 document. A test compares it with the Go
  types.

The packages:

- `estimate`: the Horvitz-Thompson estimates that each ledger uses.
- `stub`: a service in memory for the tests of other projects.
- `conformance`: the tests that each implementation must pass.
- `engine/kernel` and `engine/gp`: the additive kernel, the product
  kernel, and the Laplace Gaussian-process classifier. They come from the
  research code and give the same results as its test data.
- `engine`: the service that predicts and keeps the ledger.
  - The `features/1` feature extractor for open sets of tools. For a
    shell command, it reads the programs, the destructive patterns, and
    the writes outside the workspace.
  - Each prediction reads a fixed copy of the model. When answers arrive,
    a background task calculates a new model and replaces the copy.
  - The model counts time in the decisions of each person, not in clock
    time.
  - The store keeps model snapshots. After a restart, Limen loads a
    snapshot and does not calculate the model again.
  - A memory limit for the models in memory.
  - Replays in time order through a separate engine.
- Stores: a store in memory and a store in a directory. The directory
  store keeps one log for each person. Each write adds to the log. It
  writes snapshots and cost profiles as complete files. Only one process
  can use a directory. Each store passes the same contract tests.
- `server` and `client`: the HTTP server and its client. The server
  accepts a bearer token or a JWT and asks the shared authorizer. The
  server and the client pass the conformance tests.

The programs:

- `limend`, the server. It supplies the API for one person and keeps the
  ledger on disk. `LIMEN_TOKEN`, `LIMEN_ADDR`, `LIMEN_DATA_DIR`, and
  `LIMEN_SUBJECT` configure it. Its container image runs it as a user
  that is not root, with the ledger in a volume.
- `limen`, the command line. `limen replay topos` replays a Topos data
  directory. `limen replay file` replays a file of records. Each replay
  shows what Limen would suggest.

The documents:

- Documents for authors of a decision point and for operators, and the
  design specs.
- The documents for users follow the writing rules of ASD-STE100. A term
  list gives each term one meaning.

### Security

- Built with Go 1.27.2 and golang.org/x/net v0.60.0, which fix GO-2026-6611, GO-2026-6612, GO-2026-6613 and GO-2026-6617.
