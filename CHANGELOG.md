# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

- The contract, before any implementation: the Go types a harness codes
  against (`Action`, `Assessment`, `Answer`, `Profile`, `Reliability`), the
  `Gate`, `Reviews` and `Admin` interfaces, the error codes with their
  sentences, and the arithmetic that defines the contract: thresholds from
  a cost profile, the verdict a score implies, the verdict order, the
  number of clean audits a certificate needs, and `FailClosed`.
- The HTTP API as an OpenAPI 3.1 document, `api/openapi.yaml`, held to the
  Go types by a test.
- The authorizer vocabulary: one action per route.
- Documentation for harness authors and operators, and the design specs
  for the engine, the server, the adapters, the conformance suite and the
  rollout.
