# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

Limen is the oversight layer for automated actions, in three parts: the
shared verdict vocabulary (`latere.ai/x/pkg/verdict`), a ledger that
measures any decision source against a person's answers, and a predictor
that learns each person's tolerance from the ledger.

- The contract: `Predictor`, `Ledger` and `Admin`; a general `Action` (a
  kind and a name, from tool calls to egress, repository writes, spend and
  deploys) keyed by organization, subject and the decision point's own
  action id; `Prediction`, which suggests allow, ask or block from the
  thresholds a cost profile implies; `Decision`, which the decision point
  records with the probability that a person sees the action; `Answer`,
  whose role in an estimate follows from that decision; reliability for
  any source; replays; the error codes with their sentences; and
  `FailClosed`.
- A cost profile is accepted only when each kind of error costs more than
  two interruptions, so an action about someone Limen knows nothing about
  is always an ask.
- The HTTP API as an OpenAPI 3.1 document, held to the Go types by a test.
- `estimate`, the Horvitz-Thompson estimators every ledger shares;
  `stub`, an in-memory service for other projects' tests; and
  `conformance`, the suite every implementation passes.
- `engine/kernel` and `engine/gp`, the additive and product kernels and
  the Laplace Gaussian-process classifier, ported from the research code
  and matching its fixtures.
- Documentation for decision-point authors and operators, and the design
  specs for the engine, the server, the adapters, the ledger, the
  conformance suite and the rollout.
- The engine: the `features/1` featurizer for open tool sets (a shell
  command's programs, destructive patterns and writes outside the
  workspace among them), predictions from an immutable posterior that
  background fitters replace when answers arrive, model time counted in
  each person's decisions, snapshots kept in the store so a restart loads
  rather than refits, a memory budget for cached models, and replays in
  time order through a scratch engine.
- Stores: in memory, and in a directory (an append-only log per person,
  atomic snapshots and profiles, one process per directory), with a
  contract suite every store passes.
- The HTTP server and client, with bearer-token and JWT authentication and
  the shared authorizer, both passing the conformance suite end to end.
- `limend`, the server, which serves the API for one person over a
  ledger on disk, configured by `LIMEN_TOKEN`, `LIMEN_ADDR`,
  `LIMEN_DATA_DIR` and `LIMEN_SUBJECT`, and its image, which runs it as a
  non-root user with the ledger in a volume.
- The `limen` command: `limen replay topos` replays a Topos data directory
  and `limen replay file` a file of records, and reports what Limen would
  have suggested.
