---
title: "Engine: the time axis, the open-set featurizer, the additive GP-probit, fitting off the hot path, versions, the store"
status: testing
track: core
depends_on: [001-architecture.md, 002-contract.md, 008-ledger-and-estimation.md]
affects: [engine/, engine/kernel/, engine/gp/, engine/featurize/, engine/store/]
effort: large
created: 2026-09-28
updated: 2026-11-01
author: changkun
---

# Engine

## Overview

The engine is the in-process `limen.Service`: it predicts, keeps the
ledger, and fits each subject's model. It is what a local decision point
embeds and what `limend` serves. Its model is the one the research
validated in simulation: a Gaussian-process prior over a subject's
latent tolerance, observed through a probit likelihood on approve and
deny answers, with an additive kernel that forgets the subject's
tolerance and keeps what it learned about how risky an action is (paper,
Propositions 1 and 4). Its numerics are ported from the research's Go
code and held to the research's golden fixtures.

What the research did not need, and this spec adds, is the operation of
the model for real decision points: actions from an open set of tools
and domains, time measured the way the model assumes, fitting that never
blocks a prediction, bounded memory, versions that can change without
corrupting a model, and a store that never holds an action's input.

## Design

### Time

The kernel's time axis counts cases, not seconds: the research's
forgetting scale, lambda = 90, is ninety decisions of that subject.
Limen assigns `t`, the ordinal of a case in its subject's history, when
it first sees the case's key (a prediction, or a decision that carries
the action), from a counter per (org, subject) incremented in one store
transaction. Two cases of one subject never share a `t`. Limen also
stamps the wall-clock receive time; it serves report windows and the
order of the review queue and never enters the model. A caller supplies
no time, except `ReplayRecord.at`, by which a replay orders history.

### The featurizer `features/1`

An action becomes two vectors: `phi_a`, what the action is (the kernel's
action block), and `phi_c`, where and how it runs (the context block).
Every group is scaled so that two actions that differ only in that group
are at squared distance at most 2, the distance of two one-hot vectors,
so no group dominates by its width. Strings are hashed with FNV-1a 32 bit
over their lowercase UTF-8 bytes, into the group's number of buckets.
Nothing in an action is an error for the featurizer: an unknown kind, an
unknown tool, a missing target or environment has a defined encoding.

`phi_a`, 56 dimensions:

| Group | Dims | Encoding |
|---|---|---|
| effect | 4 | one-hot over none, read, write, external |
| irreversibility | 1 | `reversible` false is 1, true is 0, absent is 0.5 |
| kind | 8 | one-hot of `hash(kind) mod 8` |
| name tokens | 32 | the tokens of `name`, and for a shell action the program of each command; a token is a maximal run of `[a-z0-9]` of length two or more; counts per `hash(token) mod 32`, scaled to unit length; zero when there is no token |
| program class | 8 | for a shell action, multi-hot over read, vcs_local, vcs_remote, network, delete, package, build_test, infra; zero otherwise |
| actor kind | 3 | one-hot over agent, service, person; zero when absent or another kind |

`phi_c`, 56 dimensions:

| Group | Dims | Encoding |
|---|---|---|
| target sensitivity | 1 | `target.sensitivity`; 0.5 when absent |
| target kind | 8 | one-hot of `hash(target.kind) mod 8`; zero when absent |
| environment | 4 | one-hot over host, sandbox, service, other; zero when absent |
| credentialed | 1 | `environment.credentialed` as 0 or 1; 0.5 when absent |
| destructive | 1 | 1 when an extractor finds a destructive pattern, else 0 |
| outside the workspace | 1 | 1 when a write or delete names a system or home path, else 0 |
| attributes | 16 | per attribute, bucket `hash(key) mod 16`: a boolean adds 1 or -1, a number `x` adds `x / (1 + abs(x))`, a string adds 1 to bucket `hash(key "=" value) mod 16` instead; each bucket clipped to [-1, 1] |
| signals | 24 | eight buckets by `hash(source) mod 8`, three slots each: the verdict's level (allow 0, flag 1/3, ask 2/3, block 1), 1 when a verdict is present, and the score; within a bucket the maximum of each slot |

**The shell extractor.** It applies to kind `tool` with a name, compared
case-insensitively, in `bash`, `shell`, `sh`, `exec_command`,
`run_shell_command`, `local_shell`, and reads the input's `command` (a
string, or an array of strings joined by spaces) or `cmd`. It splits the
command into segments on `&&`, `||`, `;`, `|` and newlines; in each
segment it skips leading `NAME=value` assignments and the wrappers
`sudo`, `env`, `time`, `nohup` and `command`, and takes the program's
base name and its first argument. It is a static, worst-case reading
before execution, not a parser: a chain is as dangerous as its most
dangerous segment, and an unrecognized program contributes its name
token and no class. Its rules seed from the research's `bashmap`
heuristics.

| Class | Programs (and subcommands) |
|---|---|
| read | `ls`, `cat`, `head`, `tail`, `less`, `more`, `grep`, `rg`, `find`, `fd`, `stat`, `wc`, `pwd`, `echo`, `printf`, `which`, `file`, `tree`; `git status`, `log`, `diff`, `show`, `blame` |
| vcs_local | `git add`, `commit`, `checkout`, `switch`, `restore`, `stash`, `merge`, `rebase`, `reset`, `tag`, `cherry-pick`, `branch` |
| vcs_remote | `git push`, `pull`, `fetch`, `clone`; `gh` |
| network | `curl`, `wget`, `ssh`, `scp`, `rsync`, `nc`, `ncat`, `telnet`, `ftp`, `sftp`, `http`, `https` |
| delete | `rm`, `rmdir`, `unlink`, `shred`, `truncate`; `git clean`; `find` with `-delete` |
| package | `apt`, `apt-get`, `yum`, `dnf`, `apk`, `brew`, `pip`, `pip3`, `pipx`, `npm`, `pnpm`, `yarn`, `bun` with `install`, `add` or `i`; `go install`, `go get`; `cargo install`, `cargo add`; `gem install` |
| build_test | `make`; `go build`, `test`, `vet`, `run`; `cargo build`, `test`, `run`; `npm test`, `npm run`; `pytest`, `jest`, `tox`, `mvn`, `gradle` |
| infra | `terraform`, `kubectl`, `helm`, `docker`, `podman`, `aws`, `gcloud`, `az`, `psql`, `mysql`, `mongo`, `mongosh`, `sqlite3`, `redis-cli` |

Destructive patterns, in any segment: `rm` with `-r`, `-R`,
`--recursive`, `-f` or `--force`; `git push` with `-f`, `--force`,
`--force-with-lease`, `--delete` or a `:ref` deletion; `git reset
--hard`; `git clean` with `-f`; `git branch -D`; `--no-verify`; `DROP
TABLE`, `DROP DATABASE`, `TRUNCATE`, `DELETE FROM` in any case; `mkfs`;
`dd` with `of=/dev/`; `chmod -R`, `chown -R`; a redirection to
`/dev/sd`; `kubectl delete`; `terraform destroy`; `docker rm -f`,
`docker system prune`.

Outside the workspace: an argument of `rm`, `mv`, `cp`, `tee`, `chmod`,
`chown`, `ln`, `sed -i` or a redirection target that is an absolute path
under `/etc`, `/usr`, `/bin`, `/sbin`, `/lib`, `/boot`, `/var`,
`/System` or `/Library`, or begins with `~` or `$HOME`, or contains
`..`.

Other kinds have no input extractor in `features/1`; their decision
points describe the action through `target`, `environment` and
`attributes`, and a later featurizer version adds extractors per kind.
The input is read once, at prediction time, and dropped; only the
vectors are kept.

### The model

The kernel is the research's additive kernel over `(phi_a, phi_c, t)`:

```
k = s_static k_a k_c + s_global k_t + s_inter k_a k_c k_t
k_a = exp(-|phi_a - phi_a'|^2 / (2 l_a^2)),  k_c likewise with l_c,
k_t = exp(-|t - t'| / lambda)
```

with `s_static` 1.6, `s_global` 1.0, `s_inter` 0.6, `l_a` 1.1, `l_c` 1.2
and `lambda` 90 cases. The static term keeps what was learned about an
action's risk; the global term is the subject's shared tolerance, which
every answer moves; the interaction lets one kind of action drift on its
own. The separable product kernel is kept only as the research baseline
and never serves. The hyperparameters are fixed per version and are not
selected by marginal likelihood, which in the research preferred
memories too long for rare risky actions; a change is a new version and
earns its way through [[007-evidence-gates]].

The posterior is the Laplace approximation of the GP-probit (Rasmussen
and Williams, Algorithms 3.1 and 3.2), with jitter 1e-6, at most 100
Newton steps and tolerance 1e-6. A prediction at `(phi, t)` has latent
mean `mu` and variance `s2`, and

```
approve = Phi(mu / sqrt(1 + s2)),   uncertainty = sqrt(s2)
```

A subject with no answers has `mu = 0`, so `approve` is one half and,
under every profile Limen accepts, the suggestion is `ask`.

The training set of a model is, for each answered case of the subject,
the case's features, its `t`, and its latest answer, keeping the 512
cases with the highest `t`. Answers are not weighted in the fit: under a
correctly specified model the posterior is not biased by the adaptive
choice of which actions a person saw (paper, Section 3.7). Weights belong
to error estimates, not to the posterior ([[008-ledger-and-estimation]]).

### Predicting

1. Look up the key. With the same digest, return the stored prediction;
   with a different one, `action_conflict`.
2. Assign `t` and the receive time.
3. Featurize, then drop the input.
4. Resolve the effective profile ([[002-contract]]).
5. Read the model's snapshot; with none, use the prior.
6. Compute `approve` and `uncertainty`, suggest
   `thresholds.Verdict(approve)`, and store the case with its digest,
   features and prediction in one transaction.

A prediction never fits. Against a snapshot of `n` training cases it
costs one kernel row, `O(n d)`, a dot product for the mean, and one
triangular solve for the variance, `O(n^2)`: about a quarter of a
million multiply-adds at `n` = 512. The budget is a 99th percentile of
5 milliseconds in process for a subject whose snapshot is in memory,
excluding store latency, and 50 milliseconds through `limend`.

### Fitting off the hot path

A snapshot is immutable: the training features and times, `alpha` (the
gradient of the log likelihood at the mode, so `mu = k* . alpha`), the
square roots of the likelihood curvature, and the Cholesky factor of
`B = I + W^(1/2) K W^(1/2)`. Predictions read it through an atomic
pointer and never wait for a fit.

A model is due when it has answers newer than its snapshot. A fitter
pool (default half the processors) fits due models, oldest first, at
most one fit in flight per model, after a one second debounce that
gathers answers arriving together. A fit at `n` = 512 is about a billion
floating-point operations, under a second on one core. The new snapshot
is written to the store and then swapped in. The target is a model no
more than five seconds behind its newest answer.

Snapshots are cached in memory under a least-recently-used budget,
1 GiB by default, about two and a half megabytes per model at the label
cap. An evicted model's next prediction loads its stored snapshot
(a single read, not a fit). Replicas share the store and fit the same
labels with the same arithmetic, so their snapshots agree once each has
fitted the same answers; until then two replicas may differ by the
answers one has not fitted, which the five-second target bounds.
*Amended 2026-11-01:* this holds once a store that more than one process
shares exists. Until then one process owns a ledger directory, and a
hosted `limend` runs as one replica.

### Versions

A prediction's source is `learned/<N>`. A version fixes the featurizer,
the kernel, its hyperparameters and the label cap; models are keyed by
(org, subject, version) and never mix versions.

| N | Featurizer | Kernel | Hyperparameters | Label cap |
|---|---|---|---|---|
| 1 | `features/1` | additive | 1.6, 1.0, 0.6; 1.1, 1.2; lambda 90 | 512 |

A new version re-featurizes stored cases from their stored actions. An
action's input is not stored, so for cases that predate the new version
the input-derived groups (program class, destructive, outside the
workspace, and the program tokens) take their absent encodings. During a
change the old version keeps serving while the new one fits beside it,
and the operator switches after a replay comparison
([[007-evidence-gates]]).

### The store

One interface with three implementations: memory (tests and the stub's
tier), file (a self-hosted single process), Postgres (hosted).

*Amended 2026-11-01:* two implementations are built, memory and file.
The file store is also the hosted store: a hosted `limend` keeps its
ledger directory on a persistent volume, with one replica, because one
process owns a directory. A store that more than one `limend` shares
replaces the Postgres store. It is planned on Storage
([Arca](https://github.com/latere-ai/arca)) after Limen is open source,
and is not built. The tables below are the records every store keeps;
the indexes are for a shared store.

| Table | Key | Columns |
|---|---|---|
| `subjects` | org, subject | `next_t` |
| `cases` | org, subject, action_id | `t`, `received_at`, `digest` (SHA-256 of the JSON of the action without its input, which is never kept and so cannot tell two actions apart later), `action` (without input), `features` (by featurizer version), `prediction`, `decision`, `decided_at` |
| `answers` | org, subject, action_id, seq | `approve`, `by`, `at`, `review` |
| `profiles` | org, kind, agent (empty when unscoped) | `costs`, `audit_rate`, `certify`, `updated_at` |
| `snapshots` | org, subject, version | `fitted_through_t`, `blob`, `created_at` |

An index on (org, subject, t) serves fitting and windows; a partial
index on cases whose decision is shown and that have no review serves the
review queue. The file store keeps an append-only, fsynced NDJSON log of
cases, decisions and answers per subject directory, a snapshot file per
version, and one profiles file per organization. `Forget` removes a
subject's rows from every table in one transaction, or its directory.

### What is ported and what is rewritten

From [`github.com/changkun/trustcalib` at tag
`v3`](https://github.com/changkun/trustcalib/tree/v3/go), under `go/`
(the Go code is no longer on that repository's main branch):

- **Ported:** `kernel` (both kernels, the RBF blocks, the time block),
  `gp` (`LaplaceGPC` fit and predict, the probit derivatives with the
  stable log CDF), and the golden fixtures `kernel_full`,
  `kernel_additive`, `laplace_fit`, `laplace_fit_additive`, `predict`,
  `predict_additive` and `logcdf`, into `engine/kernel` and `engine/gp`,
  with the fixtures in `engine/testdata/fixtures`.
- **Rewritten:** the gateway (it decides, and in Limen the decision
  point decides), the featurizers (the research's taxonomy refuses any
  tool outside eighteen synthetic ones), persistence (one subject's JSON
  state), and the audit counters (replaced by the ledger and
  `estimate/`). The `bashmap` rules seed the shell extractor. The
  gateway's `trajectory` fixture is not ported.

### Non-goals

Choosing which actions to show a person for the sake of learning (value
of information, or BALD, which the research found does not help);
selecting hyperparameters by marginal likelihood; sharing what one
person's answers taught with another person's model, which would need
its own spec and its own consent; routing an escalation to a particular
reviewer.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | `engine/kernel` and `engine/gp` reproduce each ported fixture within 1e-9 | parity tests over the fixtures |
| 2 | `features/1` produces a 56 and a 56 dimension vector for every valid action, including unknown kinds, unknown tools and missing parts, and never errors | table and fuzz tests in `engine/featurize` |
| 3 | The shell extractor assigns each class and each destructive pattern in the tables, judges a chain by its worst segment, and finds outside-workspace paths | a table test with one command per row |
| 4 | A subject with no answers predicts `approve` 0.5 and `ask` under the default profile and every accepted profile | the conformance suite and an engine test |
| 5 | `t` increases by one per new case of a subject, and no time a caller sends reaches the model | an engine test with a fake clock |
| 6 | A prediction never fits, and its 99th percentile at `n` = 512 with a warm snapshot is under 5 milliseconds on the reference runner | a test that counts fits and a benchmark |
| 7 | A model with new answers is refitted within five seconds and the new snapshot is used by the next prediction | an engine test with a fake clock |
| 8 | Memory for snapshots stays within the budget, except a single model larger than the whole budget, and an evicted model reloads its snapshot without fitting (*amended 2026-11-01*) | an engine test with a small budget |
| 9 | No store holds an action's input after the prediction returns | a store test that greps every row and file for a marker in the input |
| 10 | Models of different versions never share training data, and a version change leaves the old version serving | an engine test |
| 11 | `engine.Engine` passes `conformance.Run` with each store | [[006-conformance]] |

## Outcome

Recorded on 2026-11-01, after v0.3.0. The engine predicts, keeps the
ledger and fits off the hot path, in process and behind `limend`, and it
is in production behind hosted Topos. Every criterion has a test in the
tree. Two parts of the design are not built: a second model version
beside the first, and a store that more than one process shares. The
spec stays `testing` until they are built or moved to their own spec.

### What was built

| Piece | Where |
|---|---|
| the additive and product kernels | `engine/kernel` |
| the Laplace GP-probit, its explanation terms and parts | `engine/gp` |
| `features/1`, the shell reader and the labels | `engine/featurize` |
| the service: prediction, background fitting, the snapshot cache, replay, explanations | `engine/` |
| the store interface, the memory store, the directory store and the store contract tests | `engine/store`, `engine/store/file`, `engine/store/storetest` |
| the golden fixtures of the research code | `engine/testdata/fixtures` |

| # | Proof in the tree |
|---|---|
| 1 | `TestGolden` (kernel); `TestFitGolden`, `TestPredictGolden`, `TestLogCDFGolden` (gp), each within 1e-10 or tighter |
| 2 | `TestGroupLayout`, `TestEmptyAction`, `TestEncodings`, `TestGroupDistanceBound`, `FuzzOf` |
| 3 | `TestShellClasses`, `TestShellDestructive`, `TestShellOutsideWorkspace`, `TestShellPrograms`, `TestRedirection`, `TestShellDepthBound`, `FuzzReadShell` |
| 4 | `TestLearns`, `TestForgetDropsTheModel`, `TestPrior`, `TestCostsThresholds`; the conformance case `UnknownIsAnAsk` |
| 5 | `TestTimeCountsDecisions`; the store rules `NextT` and `NextTConcurrent` |
| 6 | `TestPredictionNeverFits`, on the fake clock of `testing/synctest`, and `TestWarmPredictionAt512` (`engine/fit_test.go`); `BenchmarkPredictWarm512` reports a 99th percentile of 2.7 ms on an Apple M5 Pro. The gate does not assert the 5 ms bound, because under the race detector a prediction takes about 63 ms |
| 7 | `TestBackgroundFitWithinFiveSeconds`, on the fake clock; also `TestBackgroundFitAndReload`. A fit takes no fake time, so the five seconds of wall time rest on the one second debounce and `BenchmarkFit512`, about 160 ms |
| 8 | `TestCacheStaysWithinTheBudget`, `TestEvictionReloads`; one model larger than the whole budget stays alone, because a prediction needs the model it reads |
| 9 | `TestNoFileHoldsTheInput` (`engine/store/file`), which searches every file of a ledger for a marker; also the store rule `InputNeverKept`, the conformance case `CasesNeverReturnInput`, `TestLabelKeepsNoInput` |
| 10 | partly: `TestVersionsKeepTheirSnapshots`, as far as one version is built: an engine never reads or changes the snapshot of another version. Two versions side by side are not built |
| 11 | `TestConformance`, `TestConformanceInline`, `TestEngineConformance` (directory store) |

### What diverges from the design as written

| What it said | What was built | Why |
|---|---|---|
| three stores, Postgres for hosted | memory and directory; the directory store on a volume is the hosted store | one process serves a ledger until a shared store exists (amended above) |
| the counter is incremented in one store transaction | `NextT` is its own store call, and the directory store rebuilds the counter from the largest `t` when it opens | the engine serializes the operations on one subject, so a store needs atomic single calls only |
| the receive time orders the review queue | the cases and the queue are in the order of `t` | `t` is given in the order of arrival |
| `answers` and `snapshots` tables | answers are kept inside their case; a snapshot is the fitted state with the count of answers it was fitted through | the records are per case and per subject; the count names a model ([[009-explanations]]) |
| cases | also keep `featurizer`, `label`, `labels` and `predicted_by` | added by [[009-explanations]] |
| a prediction never fits | true for `limend`, which fits in the background; `Options.Inline` fits in the caller's goroutine, and a replay's scratch engine uses it | a replay must learn each answer before its next prediction |
| fitters take the oldest model first | fits run first in, first out, as each model's debounce releases it | no order was implemented; the debounce releases a model about one second after its newest answer |
| the featurizer's encodings | the program-class group has unit length, the attribute group at most `1/sqrt 2`, the signal group at most 1; the shell reader also reads `sh -c`, `eval` and `find -exec`, and knows more wrappers, destructive forms and protected paths | recorded in the `engine/featurize` package documentation |

### What this leaves open

| Open | Why |
|---|---|
| a second model version that fits beside the first, and the operator's switch after a replay comparison | only `features/1` and version 1 exist; the cache in memory is keyed by organization and subject |
| a store that more than one `limend` shares | planned on Storage (Arca) after Limen is open source |
