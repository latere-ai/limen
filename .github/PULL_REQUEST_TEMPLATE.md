## What this changes

<!-- What behavior is different after this, for whoever integrates a decision point, runs limend, or builds on the packages. -->

## Why

<!-- The problem it solves. Link the issue or the spec if there is one. -->

## Checklist

- [ ] `make` passes locally.
- [ ] A bug fix carries a test that fails without it.
- [ ] A change to the contract, the `/v1` API, a configuration variable, or
      what the ledger stores is reflected in its spec, in
      `api/openapi.yaml` where it is the API, and in `docs/`.
- [ ] A change to an exported package keeps every existing call site compiling,
      or the CHANGELOG names the break.
