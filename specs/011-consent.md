---
title: "Consent: the reach of an action, no allow beyond what the person allowed, the weight of an answer, and a calibration report"
status: in-progress
track: core
depends_on: [002-contract.md, 004-engine.md, 006-conformance.md, 008-ledger-and-estimation.md, 009-explanations.md, 010-organizations.md]
affects: [action.go, answer.go, prediction.go, profile.go, explanation.go, reliability.go, replay.go, reach.go, estimate/, engine/, engine/featurize/, engine/gp/, engine/store/, stub/, conformance/, server/, client/, api/openapi.yaml, importer/topos/, cmd/limend/, docs/]
effort: large
created: 2026-11-02
updated: 2026-11-02
author: changkun
---

# Consent

## Overview

Limen learns from the answers of a person. Its main inference is this:
the person allowed this type of action before, so the person allows it
now. Two kinds of evidence show where this inference fails.

First, an approval does not always cover the next action of the same
type. Anthropic measured its auto mode classifier on 52 real actions
that went past what the person asked for. The classifier missed 17% of
them. Anthropic writes: "In the majority of misses we examined, the
classifier correctly identifies the danger. What it gets wrong is
whether a real consent signal in the session actually covers this
action."

In the example of Anthropic, the user said "clean up the PR", and that
does not name a force push. A similar action had an approval before.
Anthropic notes that one approval is not a pattern.

Second, most answers are habit. Users of Claude Code approve 97% of
permission prompts (Anthropic, August 2026). In a study with 1,053
testers, people stopped about 17% of dangerous commands early in a
session. After 50 or more prompts, they stopped about 5%. A model that
learns from these answers learns the habit, not the consent. The paper
says the same: "A gateway calibrated to a rubber-stamping supervisor
learns to rubber-stamp" (section Limitations).

A third result is about what a person sees when Limen asks. In a study
of 50 analysts, a coin flip set the option that the agent of each
analyst favored. The analysts chose that option 86% of the time. Of
the analysts, 77% repeated the arguments of the agent, and 15% said
that the agent was the source. A display of the approval probability
of Limen before the person answers can make the person follow Limen.

The engine today shows the problem on one action type. These numbers
are measurements on Limen 234161e with the kernel of [[004-engine]]:

| Measurement | Result |
|---|---|
| evidence ratio after one allow | 0.54, so a `split` ask exists from the first answer |
| one allow of `publish`, Balanced profile (allow above 0.65) | 0.729: the next `publish` runs without a person |
| allows before an `allow`, Careful profile (allow above 0.90) | 7: 0.5, 0.729, 0.805, 0.845, 0.87, 0.887, 0.899, then 0.908 |
| 40 allows, Strict profile (allow above 0.98) | 0.962: never an `allow` |
| about 90 decisions with no answer | the next `publish` is `unsure` again (evidence ratio 0.30) |
| allow, allow, deny | 0.61 with evidence ratio 0.84: a `split` ask from answers that disagree |

The label `publish` does not tell a person if the action made an app
public or updated a preview. The model does not know it either.

This spec adds five parts:

1. **Scope.** A closed set of facts about the reach of an action. The
   facts are undo, the boundaries it crosses, the class of its target,
   and first contact. Limen reads some facts from the input. The
   decision point sends the others.
2. **The cover rule.** Limen never suggests `allow` for an action that
   the approvals of the person do not cover. An approval covers an
   action of the same family whose scope is within its scope. Limen
   also needs enough approvals from enough different sessions.
3. **The weight of an answer.** An answer carries how the person gave
   it. It tells the time to answer and where the person answered. It
   tells if the person opened the details, and if Limen showed its
   suggestion first. A quick approval with no details does not count
   toward the cover rule.
4. **A calibration report.** For each range of approval probability,
   the report compares what Limen stated with the rate that the reviews
   measured.
5. **Asks without a lean.** For an `ask`, Limen states no probability
   in its reason. A client does not show the lean of Limen before the
   person answers.

Parts 1 to 5 change no kernel feature and no fit. They apply to model
version 1. A second model version, `learned/2`, adds the scope to the
features and the weight of an answer to the likelihood
([features/2 and learned/2](#features2-and-learned2)).

This spec replaces the interim rule on Limen main, `LIMEN_MIN_ALLOWS`
and `LIMEN_MIN_SESSIONS`. That rule gives no `allow` until the person
allowed the same label 3 times in 2 different sessions. The cover rule keeps the two
settings with the same names and defaults. It counts by family and
scope in place of the label, and it counts only approvals that have
weight.

## Design

### Scope: the reach of an action

#### The dimensions

The scope of an action has four dimensions. Each dimension has a fixed
list of values in an order, from the least reach to the most reach.
Each dimension also has the value `unknown`.

| Dimension | Values, in order | Meaning |
|---|---|---|
| `undo` | `none`, `reversible`, `recoverable`, `irreversible` | `none`: the action changes nothing. `reversible`: the change can be undone in the workspace with no loss, for example an edit of a tracked file or a local commit. `recoverable`: a copy or a second action can undo the change. Examples are a push of new commits and an update of a preview. `irreversible`: no copy brings it back, or other people already saw it. Examples are a delete with no copy, a force push that drops commits, a sent message, and a payment |
| `crosses` | one boundary each: `machine`, `remote`, `network`, `organization`, `public`, each `no` or `yes` | `machine`: the action changes the machine outside the workspace, for example a home or system path. `remote`: the action reaches a system other than the machine. `network`: it reaches a host outside the network of the session. `organization`: it reaches a party that the organization does not own. `public`: it makes something readable by anyone |
| `target_class` | `scratch`, `own`, `shared`, `primary` | the class of what the action changes. `scratch`: a disposable thing. `own`: a thing that this session or person made, for example the branch of the session or a preview. `shared`: a thing that other people use, for example another branch. `primary`: the default or protected branch, production, a live release |
| `contact` | `repeat`, `first` | `first`: no earlier approved case of the person has the same target |

The five examples of the owner map to these dimensions:

| Example | `undo` | `crosses` | `target_class` |
|---|---|---|---|
| a read | `none` | | |
| a reversible write | `reversible` | | |
| an irreversible delete | `irreversible` | | |
| a force push that rewrites history | `irreversible` | `remote` | the class of the ref |
| a public exposure | `irreversible` | `remote`, `public` | |

`network`, `organization` and `public` each make `remote` true. Limen
sets `remote` to `yes` when one of them is `yes`.

#### The order, and `unknown`

The order of each dimension is a partial order. For values `v0 < v1 <
... < vk` of one dimension:

- `vi` is within `vj` if `i <= j`.
- `v0` is within `unknown`, and `unknown` is within `vk`.
- `unknown` is within `unknown`.
- No other pair is in the order. A middle value and `unknown` are not
  comparable.

For a boundary of `crosses`, `no` is `v0` and `yes` is `vk`.

The dimensions of the order are `undo`, each boundary of `crosses`,
and `target_class`. A scope `s` is within a scope `a` if, in each of these
dimensions, the value of `s` is within the value of `a`. A scope `s` exceeds `a` if `s` is not within
`a`. `contact` is not in the order, because each target was a first
contact at its first approval. `contact` is a feature of the model and
a word of the label.

With this order, an unknown value covers only an unknown value or the
least value. An unknown value is covered only by an unknown value or
the greatest value. A dimension that a decision point never sends is
`unknown` in each case, so its actions cover each other.

#### Who knows what

Limen reads a dimension from the input of the action where a fixed
table can read it. The decision point sends a dimension that only it
knows. A dimension that nobody reads or sends is `unknown`.

| Dimension | Limen reads it from | The decision point sends it as |
|---|---|---|
| `undo` | the shell table below, `effect` and `reversible` | `scope.undo`, and `reversible` as today |
| `crosses.machine` | the outside-workspace rule of the shell reader | `scope.crosses.machine` |
| `crosses.remote` | the shell table, and `effect` `external` | `scope.crosses.remote` |
| `crosses.network` | the shell table, bounded by the hold `egress` | `scope.crosses.network` |
| `crosses.organization` | nothing | `scope.crosses.organization` |
| `crosses.public` | the shell table of publishing commands, and `environment.repositories` for a `git push` | `scope.crosses.public` |
| `target_class` | the refspec of `git push` | `target.class` |
| `contact` | the stored targets of earlier approved cases | `scope.contact` |

For a dimension that both Limen and the decision point give, the value
is the greater of the two. If the two values are not comparable, the
value is the greatest value of the dimension. A decision point can make
the reading of Limen wider. It cannot make it narrower, because the
agent writes the input and the reading of the input is a worst case.

A hold is the one way to make a reading narrower. A hold is a limit
that the environment enforces, so the reach cannot occur:

| Hold | What the environment enforces | The bound |
|---|---|---|
| `egress` | no host outside the network of the session is reachable without its own approval | `crosses.network` is `no` |
| `protected_refs` | the git host refuses a push to a protected branch | a `target_class` of `primary` from a `git push` is `unknown` |
| `checkpoints` | the decision point keeps a copy of the workspace that can restore it | if no boundary of `crosses` is `yes` or `unknown`, `undo` is at most `recoverable` |

Limen makes the scope in this order:

1. Limen reads each dimension that it can read from the input and the
   fields of the action.
2. Each hold bounds that reading. `egress` and `protected_refs` bound
   the reading of their dimension. `checkpoints` bounds the reading of
   `undo`, and its test reads `crosses` after step 3.
3. Each claim of the decision point is merged with the bounded reading:
   the greater of the two, or the greatest value if they are not
   comparable. A dimension that only one side gives has the value of that
   side. A dimension that no side gives is `unknown`.
4. `remote` is `yes` if `network`, `organization` or `public` is `yes`.

A hold bounds the reading of the input, because the reading is a worst
case. A hold does not bound a claim, because a claim is what the decision
point knows. "At most `recoverable`" makes `irreversible` and `unknown`
into `recoverable`. A claim without a word, or a boundary without a key,
is no claim. A claim of `unknown`, or of a word outside the vocabulary, is
a claim of `unknown`, so it can only make the scope wider.

Limen reads `contact` from the `target` of the action if the target has
an `id`: `first` if no case of the person whose latest answer is an
approval has a target with the same `kind` and `id`, and `repeat` in
other cases. The `id` is a field of the decision point, kept with the
action as before. It never goes into the scope.

#### The shell table of `features/2`

The shell reader of [[004-engine]] gives the program and subcommand of
each segment. `features/2` reads the scope of each segment from this
table. The scope of a chain is the greatest value of its segments in
each dimension, as with the classes of `features/1`.

| Segment | `undo` | `crosses` |
|---|---|---|
| class `read` | `none` | |
| class `build_test` | `reversible` | |
| class `vcs_local` | `reversible` | |
| `git restore`, `git checkout --`, `git stash drop`, `git stash clear` | `irreversible` | |
| `git pull`, `git fetch`, `git clone` | `reversible` | `remote` |
| `git push` | `recoverable` | `remote`, and `public` from `environment.repositories` |
| `git push` with a force or delete flag (the destructive patterns of `features/1`) | `irreversible` | `remote`, and `public` from `environment.repositories` |
| `gh pr merge` | `recoverable` | `remote`, and `target_class` `primary` |
| `gh repo create --public`, `gh repo edit --visibility public` | `irreversible` | `remote`, `public` |
| `gh repo delete`, `gh release delete` | `irreversible` | `remote` |
| other `gh` | `unknown` | `remote` |
| `curl`, `wget` with no flag that sends data | `none` | `remote`, `network` `unknown` |
| `curl`, `wget` with `-d`, `--data`, `-F`, `--form`, `-T`, `--upload-file`, or `-X` other than `GET` or `HEAD` | `irreversible` | `remote`, `network` `unknown` |
| `ssh`, `scp`, `rsync`, `nc`, `ncat`, `telnet`, `ftp`, `sftp` | `irreversible` | `remote`, `network` `unknown` |
| class `delete` | `irreversible` | |
| class `package` | `recoverable` | `remote` |
| `npm publish`, `pnpm publish`, `yarn publish`, `cargo publish`, `gem push`, `twine upload` | `irreversible` | `remote`, `public` |
| `docker push`, `podman push` | `irreversible` | `remote`, `public` `unknown` |
| class `infra` with a destructive pattern | `irreversible` | `remote` |
| other class `infra` | `unknown` | `remote` `unknown` |
| a segment that no table names | `unknown` | each boundary `unknown` |

A boundary that a named row of the table leaves blank is `no` for that
segment. Only the last row, a segment that no table names, makes each
boundary `unknown`. The outside-workspace rule of `features/1` sets
`crosses.machine` to `yes`. If no segment sets it and no segment is
unknown, it is `no`. Limen reads `crosses.organization` from no row: a
shell action has it only from a claim.

The table takes the reading that finds more danger where it leaves a
choice open:

- A segment with a destructive pattern of `features/1` is
  `irreversible`, whatever its row. For example, `git reset --hard` is
  `irreversible`, not `reversible` as its class `vcs_local` is.
- `git checkout` is in the first row only with the argument `--`.
- `wget` with `--post-data`, `--post-file` or `--method` is a `wget` that
  sends data.
- A segment of class `network` with no row of its own, such as `http`,
  has `undo` `unknown`, `crosses.remote` `yes` and `crosses.network`
  `unknown`.
- A segment that a table of labels names but whose row is not in this
  table, such as a `git` subcommand that no table names, is a segment
  that no table names.

The greatest value of the segments uses the order of the dimensions: two values that
are not comparable give the greatest value of the dimension. For
example, a `go test` chained to a segment that no table names has `undo`
`irreversible`.

`crosses.public` of a `git push` is `yes` if `environment.repositories`
is `public`, `no` if it is `private`, and `unknown` in other cases.

`target_class` of a `git push` comes from its refspec. A destination named
`main`, `master`, `trunk`, `production`, `prod`, `stable`, `release`, or
`release/` and more, with or without `refs/heads/`, is `primary`. Any
other named destination is `unknown`. No refspec, or `HEAD`, is
`unknown`. `--all` and `--mirror` push each branch, the default branch
too, so they are `primary`. The name of a ref never goes into the scope.
Only its class does.

For an action that is not a shell action, Limen reads `undo` and
`crosses.remote` from the fields of the action. `effect` `none` or
`read` gives `undo` `none`. `reversible` true gives `reversible`, and
false gives `irreversible`. `effect` `external` gives `crosses.remote`
`yes`, and `effect` `write` gives `no`, because a write stays in the
system that runs it ([[001-architecture]]).

#### The contract fields

Three optional fields carry the claims of a decision point. Each value
is a word from the tables above. A value outside the vocabulary is
`unknown`, and it is not an error, so a newer decision point works with
an older Limen.

| Field | Type | Meaning |
|---|---|---|
| `scope.undo` | string | a value of `undo` |
| `scope.crosses` | object of boolean | for each boundary that the decision point knows, `true` for `yes` and `false` for `no`. An absent key is `unknown` |
| `scope.contact` | string | `first` or `repeat` |
| `target.class` | string | a value of `target_class` |
| `environment.holds` | array of string | the holds of the environment |
| `environment.repositories` | string | the visibility of the repositories that the session pushes to: `private`, `public`, or `mixed` |

The scope is a typed field, not an attribute. An attribute is an open
fact that the featurizer hashes. The scope is a closed vocabulary that
the cover rule compares. The decision point fills each field with
words from these tables only. It never puts a path, a host, a branch
name, or other text of the input in them.

A prediction gives the scope that Limen used, as `scope`. It has the
members `undo`, `crosses`, `target_class`, `contact` and `target_kind`.
Each dimension has a value, `unknown` included. `undo`, `target_class`
and `contact` are words. `crosses` is an object of boolean as in the
claim: `true` for `yes`, `false` for `no`, and no key for `unknown`. The
decision point can then see what Limen read, and show it to the person.
The case keeps the same scope. A `FailClosed` prediction has no
`scope`.

`target_kind` names the type of the target without its name. It is the
`kind` of the `target` that the decision point sent. For a shell
action with no `target`, it is the first row of this table that a
segment matches. With no match, it is absent.

| `target_kind` | Segments |
|---|---|
| `ref` | `git push`, `git branch`, `gh pr merge` |
| `repository` | other `gh repo` commands |
| `package` | the publishing commands of the shell table |
| `host` | the network programs |
| `path` | class `delete`, and the outside-workspace rule |

#### The input is still never stored

Invariant 5 of [[001-architecture]] stays. Limen reads the scope from
the input at prediction time and keeps only the words of the tables.
The refspec, the URL and each path are dropped with the input. Limen
does not keep a hash of an input string. A hash of a host or a path can
be found again from a list of names.

### Labels `labels/2`

A label of `labels/2` is the label of `labels/1` and a phrase from the
scope. The phrase tells the person the reach of the action:
`publish: make public` and `publish: update` are two different labels.

The phrase is the first one or two rows of this table that apply, in
the order of the table:

| Row | When | Phrase |
|---|---|---|
| 1 | `crosses.public` `yes` and `undo` `irreversible` | `make public` |
| 2 | `crosses.public` `yes` | `public` |
| 3 | `crosses.organization` `yes` | `outside the organization` |
| 4 | `crosses.network` `yes` | `outside the session network` |
| 5 | `target_class` `primary` | `default branch` for `target_kind` `ref`, `production` for `app`, `deploy` or `service`, and `protected target` for other kinds |
| 6 | `crosses.machine` `yes` | `outside the workspace` |
| 7 | `undo` `irreversible` | `cannot undo` |
| 8 | `contact` `first` | `first time` |

Row 1 holds rows 2 and 7: if row 1 applies, rows 2 and 7 do not. If no
row from 1 to 8 applies, the phrase is `read` for `undo` `none` and
`update` for other values, `unknown` included. A label of another kind
is `<labels/1 label>: <phrase>`. A shell label adds a phrase only from
rows 1 to 8, as `, <phrase>` after its segments, because its segments
already name the action.

| Action | `labels/1` | `labels/2` |
|---|---|---|
| `publish` that changes access to anyone with the link | `publish` | `publish: make public` |
| `publish` of a preview | `publish` | `publish: update` |
| `publish` of a release, the app private | `publish` | `publish: production` |
| `git push --force origin main` | `bash: git push --force` | `bash: git push --force, default branch, cannot undo` |
| `rm -rf ~/.cache` | `bash: rm -r` | `bash: rm -r, outside the workspace, cannot undo` |
| `go test ./...` | `bash: go test` | `bash: go test` |

The **family** of an action is its type for the cover rule. For a
tool, it is the name of the tool. For another kind, it is `<kind>
<name>`. For a shell action, it is each segment word of its label, such
as `git push` or `rm -r`. A shell segment that no table names has the
family `other command`. A chain belongs to the family of each of its
segments. A label names at most two segments, so the case keeps its
families beside its label. `git push --force` is a family of its own,
because its label word is not `git push`.

A case from before `labels/2` keeps its `labels/1` label. The cover
rule reads its scope from the migration table and its families from its
label ([migration](#migration-of-stored-cases)).

### The cover rule

#### What covers an action

An **approval that covers** an action `x` is a case `c` of the same
person in the window of the model, with these properties:

1. The latest answer of `c` is an approval.
2. The weight of that answer is above zero
   ([the weight of an answer](#the-weight-of-an-answer)).
3. `c` and `x` share a family. For a chain, each family of `x` needs
   its own approvals that cover it.
4. The scope of `x` is within the scope of `c`.

`x` is **covered** if two conditions are true for each family of `x`.
The covering approvals have a total weight of `min_weight` or more.
They come from `min_sessions` or more different sessions. The session of a case is
its `actor.session`. A case with no session counts as the session of
its receive date, one session for each UTC day.

| Setting | Default | Where |
|---|---|---|
| `min_weight` | 3 | `LIMEN_MIN_ALLOWS`, and the `consent` of a profile |
| `min_sessions` | 2 | `LIMEN_MIN_SESSIONS`, and the `consent` of a profile |

The variables set the default for an installation, and `0` turns a
setting off, for tests. A profile can set greater values and not smaller
ones: the setting that applies is the greater of the installation and
the profile. The meet of two profiles ([[010-organizations]]) takes the
greater value of each. A value of `0`, or no value, is the setting of
the installation. A value below 0 is refused with `invalid_profile`.
With the default weights of this spec, a considered approval counts 1,
so the defaults are the interim rule.

The window of the cover rule is the window of the model: the 512 most
recent answered cases. Cover has no other decay. An approval leaves the
cover when it leaves the window, as it leaves the fit.

A prediction, an explanation and a learned entry keep the field
`minimum` of the interim rule in the API, because the name of a field
never changes. Limen no longer sets it, and `cover` takes its place.

#### The cap on `allow`

The cap applies after the model:

1. The model gives `approve`, `uncertainty` and the evidence ratio, as
   in [[004-engine]].
2. The thresholds give the verdict, as in [[002-contract]].
3. If the verdict is `allow` and the action is not covered, the verdict
   is `ask`.

The cap changes only the verdict. `approve`, `uncertainty` and the
evidence ratio stay the numbers of the model. The cap can change
`allow` to `ask` and nothing else, so it never makes a suggestion more
permissive. There is no cap on `block`. A denial does not come from a
habit of approval. A false block costs one more question, not an
action that the person did not accept.

A capped `ask` has one of two new values of `ask_kind`:

| `ask_kind` | When | Meaning for the person |
|---|---|---|
| `wider` | for a family of `x`, no approval of the family, of any weight, has a scope that the scope of `x` is within | the person never allowed an action of this reach |
| `few` | in each family of `x`, approvals have such a scope, but their weight or their sessions are below the settings | the person allowed this reach, but not with enough considered answers or not in enough sessions |

A decision point that reads `ask_kind` treats an unknown value as
`unsure`, which is what Topos does today. A decision point that keeps
an ask for the person keeps `wider`, because it rests on the answers of
the person ([[005-adapters]]).

#### Why this is correct for the model

Proposition 2 gives the verdict of least expected cost among `allow`,
`ask` and `block`. The cap removes `allow` from the choices where the
person gave no consent to the reach of the action. Limen then takes
the verdict of least expected cost among the choices that are left.
The thresholds, the costs and the arithmetic of Proposition 2 do not
change.

The paper measured the problem that the cap solves (section
Generalization to never-labelled actions). A force push to production
infrastructure never had an answer. The kernel models allowed some of
its occurrences, because it was similar to approved actions. The
per-cell learner allowed none, because it asked about each cell that it
did not see.

Where the scope is new, the cap makes Limen ask like the per-cell
learner. Where the scope is covered, the kernel still transfers
approval. The paper names a prior that makes `f` decrease with
the risk attributes as a remedy (section Correlated generalization). The
cap is a strict version of that remedy over the scope.

#### What the prediction and the explanation carry

A prediction carries `cover`. A `FailClosed` prediction carries none.

| Field | Meaning |
|---|---|
| `cover.covered` | if the action is covered |
| `cover.weight` | the total weight of the covering approvals, for the family with the least weight. If a family misses a setting, only the families that miss one are compared |
| `cover.sessions` | the number of different sessions of the covering approvals with weight, for that family |
| `cover.min_weight`, `cover.min_sessions` | the settings that applied |

An explanation also carries `cover.nearest`: up to three approved cases
of the family whose scope `x` exceeds, each with `action_id`, `label`
and `at`, closest first. Closest is the fewest dimensions in which `x`
exceeds the case, and then the most recent. `at` is the time of the
latest answer of the case. A client can then say "You allowed
`publish: update`. This is `publish: make public`."

`learned` ([[009-explanations]]) applies the cap to the verdict of each
entry. A preview applies the cap with the `cover` recorded on each
prediction, and it does not compute the cover again.

The reason of a capped `ask` names the scope and no probability:

| `ask_kind` | Reason |
|---|---|
| `wider` | "This person never allowed an action of this reach: `<label>`. Limen suggests ask." |
| `few` | "This person allowed actions of this reach. Limen needs `<w>` considered approvals from `<m>` sessions before it suggests allow. Limen suggests ask." |

### The weight of an answer

#### What an answer carries

An answer gets six optional fields. The decision point relays them from
the client of the person. No field holds text.

| Field | Type | Meaning |
|---|---|---|
| `answer_ms` | integer, 0 to 86,400,000 | milliseconds from the moment the question was visible to the person to the answer, measured by the client |
| `waited_ms` | integer, 0 or more | milliseconds from the decision that showed the action to the answer, measured by the decision point |
| `details_opened` | boolean | true if the person opened the details of the action before the answer. The details are the full input, the change and the target. False if the client offered them and the person did not open them |
| `suggestion_shown` | boolean | true if the client showed the suggestion of Limen (the verdict, the approval probability, the reason, or an explanation) before the answer |
| `surface` | string | where the person answered: `prompt`, `notification`, `queue`, `bulk`, or `rule` |
| `remembered` | boolean | true if the answer also made a standing rule for later actions |

| `surface` | Where the person answered |
|---|---|
| `prompt` | a question in the conversation while the action waits |
| `notification` | a system notification |
| `queue` | a list of waiting questions or of reviews |
| `bulk` | one gesture that answers more than one action |
| `rule` | a standing rule, not the person at that time |

A decision point sends the decision of a rule as a decision, not as an
answer. If it sends it as an answer, `surface` is `rule`.

An `answer_ms` outside its range, or a `waited_ms` below 0, is refused
with `invalid_answer`. A `surface` outside the list is no `surface`, and
it is not an error, so a newer client works with an older Limen. Limen
keeps the six fields with the answer, as the decision point sent them,
and a recorded answer shows them.

A replay record carries the same six fields, and `answered_at`, the time
of the answer. A replay then records the answer at `answered_at`. A
record without `answered_at` has its answer at `at`, the time of the
decision, so the time that Limen measures is 0. The importer of Topos
logs sets `answered_at` from the time of the confirmation.

Limen also measures its own time from the recorded decision to the
answer, as `decided_at` to `at`. The **time** of an answer is the
smallest of `answer_ms`, `waited_ms` and that time. A client cannot
make an answer look slower than the decision point and Limen measured
it. If none of the three is known, the rule "too quick" does not
apply.

The **position** of an answer is the number of shown cases of the same
person that have a review in the same session before it. Limen counts
it from its records. A review is before the answer if its time is
earlier, or if the times are equal and its case is earlier. The answer
of the case itself is not counted.

#### The weight policy `weights/1`

The weight of an answer is a number from 0 to 1. Limen calculates it
when it reads the answer, from the stored fields and the policy. It
does not store it. The policy has a version, `weights/1`, and a change
of the policy is a change to this spec.

A denial has weight 1, except a denial with `surface` `rule`, which has
weight 0. A habit gives approvals, not denials, so a quick denial is
still evidence. An approval has weight 0 if one of these rules applies,
and weight 1 if none applies:

| Rule | When | Why |
|---|---|---|
| a standing rule | `surface` is `rule` | the person did not answer at that time |
| one gesture for many | `surface` is `bulk` and `details_opened` is not true | one gesture is not a decision about each action |
| too quick | the time is less than the floor, and `details_opened` is not true | the person did not have time to read the action |
| it followed the lean | `suggestion_shown` is true, the shown `approve` was above 0.5, and `details_opened` is not true | the answer can be the suggestion of Limen, not the view of the person |
| late in a long session | the position is the fatigue count or more, and `details_opened` is not true | the share of dangerous actions that people stop falls with each prompt |

| Setting | Default | Variable |
|---|---|---|
| the floor | 2,000 ms | `LIMEN_ANSWER_FLOOR_MS` |
| the fatigue count | 50 | `LIMEN_ANSWER_FATIGUE` |

The 50 comes from the 17% and 5% measurement above, which compared the
start of a session with 50 or more prompts.

The fatigue rule has a cost. After 50 answered prompts in one session,
the approvals of that session stop building the cover. The sessions
that ask most then earn an `allow` most slowly. The cover needs
approvals from only `min_sessions` different sessions, so the early
answers of other sessions can still build it. An answer with no new
fields has the time that Limen measured, no `details_opened`, and a
position. So a quick approval from before this spec has weight 0,
and a slower one has weight 1.

An answer is **considered** if its weight is 1.

#### Where the weight applies

In model version 1, the weight applies to the cover rule only. The fit
of `learned/1` does not change: each answer trains the model, as in
[[004-engine]]. A quick approval still moves `approve`, but it cannot
open the cover. The cap then holds the suggestion at `ask`.

In model version 2, the weight also applies to the likelihood. That is
the section [features/2 and learned/2](#features2-and-learned2).

The answer to an audit gets the weight that the policy gives it, as any
answer does. In each estimate, the same answer counts with its full
weight `1/p`, and the policy weight does not apply there.

#### The estimates do not use the weight

The estimates of [[008-ledger-and-estimation]] do not read the weight.
A review counts with the weight `1/p` of its review probability and
with no other weight. The weight of an answer depends on the answer. An
estimate that used it would no longer be a Horvitz-Thompson estimate
over the reviews (Proposition 8). Its value would move toward the
answers that the person gave slowly.

The cover rule changes which actions a person sees. It does not change
the probability with which a person sees them. The decision point fixes
that probability before the action runs, after the prediction and its
cap. So the estimates stay unbiased.

The certificate keeps its claim and its arithmetic. It gets one count,
`considered`: how many audits in its clean run are considered answers.
A reader can then see if the certificate rests on quick answers.

### features/2 and learned/2

#### The features

`features/2` is `features/1` with the scope in place of three of its
groups. Each new group obeys the rule of [[004-engine]]: two actions
that differ only in that group are at squared distance 2 or less. Each
boundary is its own group, so each boundary that one action crosses and
the other does not adds a squared distance of 2.

| Block | Group | Dims | Encoding |
|---|---|---|---|
| A | effect | 4 | as `features/1` |
| A | undo | 3 | thermometer over `reversible`, `recoverable`, `irreversible`, each step `sqrt(2/3)`. `unknown` is half of each step |
| A | kind, name tokens, program class, actor kind | 8, 32, 8, 3 | as `features/1` |
| A | each boundary of `crosses` | 1 each, 5 | `no` 0, `unknown` `sqrt(2)/2`, `yes` `sqrt(2)` |
| C | target sensitivity, target kind | 1, 8 | as `features/1` |
| C | target class | 3 | thermometer over `own`, `shared`, `primary`, each step `sqrt(2/3)`. `unknown` is half of each step |
| C | environment, credentialed | 4, 1 | as `features/1` |
| C | holds | 3 | one each for `egress`, `protected_refs`, `checkpoints`: 1 if held, else 0 |
| C | contact | 1 | `repeat` 0, `unknown` `sqrt(2)/2`, `first` `sqrt(2)` |
| C | attributes, signals | 16, 24 | as `features/1` |

`A` has 63 dimensions and `C` has 61. `features/2` drops the
irreversibility group of `A` and the destructive and outside-workspace
groups of `C`, because `undo` and `crosses.machine` hold them.

With the kernel of [[004-engine]], one boundary that differs multiplies
`k_a` by about 0.44. `publish: update` and `publish: make public`
differ in `undo` and `crosses.public`, and `k_a` falls to about 0.33.
One approval of the first now says less about the second.

#### The likelihood with weights

`learned/2` fits the likelihood with the weight `w_i` of each answer:

```
log p(y | f) = sum_i w_i log Phi(s_i f_i),   s_i = +1 for an approval, -1 for a denial
```

The gradient of answer `i` is `w_i` times the gradient of
[[004-engine]], and its curvature is `w_i` times the curvature. The
curvature stays zero or more, so the log likelihood stays concave and
the Newton steps of the Laplace method converge as before. An answer
with weight 0 adds nothing to the fit, and the fit leaves it out. The
explanation still sums exactly: `mu(x) = sum_i k(x_i, x) g_i`, with the
weighted gradient `g_i` ([[009-explanations]]). With each weight 1,
`engine/gp` gives the golden fixtures of the research within 1e-9.

A weight in a likelihood is a tempered likelihood. It is not in the
paper. As a remedy, the paper names a model in which the noise of an
answer grows with the number of asks (section Limitations). A tempered
likelihood is a version of that remedy that keeps the Laplace method
exact. It
must pass gate 1 of [[007-evidence-gates]] before it serves.

`weights/2`, for `learned/2`, gives weights between 0 and 1 in place
of 0. The rule "a standing rule" stays 0. "One gesture for many" and
"too quick" give 0.2. "It followed the lean" and "late in a long
session" multiply the weight by 0.5. The cover rule keeps `weights/1`.

#### Versions

| N | Featurizer | Labels | Kernel | Likelihood | Weights | Label cap |
|---|---|---|---|---|---|---|
| 1 | `features/1` | `labels/2` from this spec | additive, as [[004-engine]] | probit | none in the fit, `weights/1` in the cover | 512 |
| 2 | `features/2` | `labels/2` | additive, the same hyperparameters | probit with weights | `weights/2` in the fit, `weights/1` in the cover | 512 |

#### Migration of stored cases

A case keeps its action without its input, its `features/1` vectors,
its `labels/1` label, its decision, and its answers with their times.
Limen reads the scope of an old case from these records:

| Dimension | Read from |
|---|---|
| `undo` | the classes and the destructive flag of the stored `features/1` vector, the words of the label (`git push --force`, `rm -r`), `effect` and `reversible`. `unknown` where the table needs the input |
| `crosses.machine` | the outside-workspace flag of the stored `features/1` vector |
| `crosses.remote` | the classes of the stored vector: `vcs_remote`, `network` and `infra` give `yes`, and a case with only `read`, `vcs_local` and `build_test` gives `no` |
| `crosses.network`, `crosses.organization`, `crosses.public`, `target_class`, `contact` | `unknown` |

The cover rule of wave 1 reads the scope of an old case from this
table, and its families from the segment words of its `labels/1` label.
An old case keeps its `labels/1` label. The weight of an old answer uses
the time from `decided_at` to `at`, so a quick old approval does not
count toward the cover.

A decision point can start to send a dimension that it did not send
before. Old approvals have `unknown` in that dimension. They then cover
only new actions at the least value of that dimension. The cover
builds again from new answers, and the person sees more asks until it
does.

`unknown` is the correct value for a lost reading. An old approval with
an unknown reach covers only actions of unknown or least reach. It
never covers a new action that is known to be public or to reach
production.

`learned/2` needs a model of each person under the new features.
[[004-engine]] keys snapshots by organization, person and version, so
the snapshots of version 1 stay in the store. Two ways are possible:

1. **A refit.** The new release fits version 2 from the stored cases
   at the first prediction of each person, and serves it. To go back,
   the operator deploys the release before, which reads the snapshots of
   version 1. Before the release, a replay of the ledger compares the
   two versions. It puts the would-be verdicts of version 2 beside the
   recorded predictions of version 1 (gate 1 of [[007-evidence-gates]]).
2. **Two versions side by side.** This is criterion 10 of
   [[004-engine]]. Version 1 serves while version 2 fits beside it.
   The operator changes the version after the comparison. The cache of
   models is then keyed by version too, and each answer causes two
   fits.

With either way, the new release keeps the `features/1` vectors of
each new case beside its `features/2` vectors. The release before can
then still fit version 1.

The owner decided the refit (decision 1). The cover rule and the cap
do not depend on it, because they apply to version 1. The ledger is days old, so a replay compares on all
of it. A second fit of each answer and the cache keyed by version are
then not necessary.

### The calibration report

A person must not read Limen as surer than the reviews show. The
calibration report compares the approval probability that Limen stated
with the approval rate that the reviews measured.

`GET /v1/calibration?org&subject&from&to` answers it for the
predictions of Limen in the window. It asks `reliability.read` on the
`Reliability` resource, bound to the subject, as the reliability route
does ([[003-http-api]]).

The report has ten bins of `approve`: `[0, 0.1)`, `[0.1, 0.2)`, and so
on to `[0.9, 1]`. For each bin, it reads the cases whose prediction was
in the bin:

| Field | Meaning |
|---|---|
| `from`, `to` | the edges of the bin |
| `cases` | the predictions in the bin |
| `covered` | the share of them whose review probability was above zero |
| `stated` | the mean `approve` of the covered cases |
| `observed` | the approval rate of the covered cases, from their reviews, each with weight `1/p`, as the estimate of [[008-ledger-and-estimation]] |
| `interval` | the Wilson 95% interval of `observed` at the Kish effective size |
| `reviews`, `considered` | the reviews, and how many of them are considered answers |
| `state` | `calibrated`, `overconfident`, `underconfident`, or `not_estimable` |

`state` is `not_estimable` if the Kish effective size is below 20, and
then `observed` is null. Null is not zero. Otherwise, `state` is
`calibrated` if `stated` is in the interval. It is `overconfident` if
`stated` is outside the interval and further from 0.5 than `observed`.
It is `underconfident` in the other case.

The report also gives `error` and `estimable`. They read only the
bins that are not `not_estimable`. `error` is the mean distance between
`stated` and `observed` over those bins, each bin weighted by its
`cases`. `estimable` is the share of all cases that are in those bins.

The bins in the ask range get reviews from each `ask`. The bins above
the allow threshold and below the block threshold get reviews only from
audits, so their states need an audit rate above zero. The bins above
the allow threshold are the bins of the allows. Their `observed` is
one minus the false-allow rate of those allows. The certificate tests
that rate against its claim.

**The rule for clients.** A client shows `approve` as a number, or in
words that state a probability such as "likely", only if two conditions
are true. The bin of the prediction is `calibrated` in the report of the
person for the last 90 days. For a bin above the allow threshold, the
certificate of `learned` also holds. In all other cases the client
states no probability.

An explanation carries `calibration`, the state of the bin of its
prediction in that report. A client then does not ask for the report
each time.

### Asks without a lean

The reason of an `ask` states no probability and no direction. A reason
of a prediction is for the person who reads the record, and the person
reads it when Limen asks.

| `ask_kind` | Reason |
|---|---|
| `unsure` | "Limen has few answers from this person about actions like this. Limen suggests ask." |
| `split` | "The answers of this person about actions like this do not agree. Limen suggests ask." |
| `short` | "The answers of this person about actions like this agree, but they are not enough yet to settle this action. Limen suggests ask." |
| `wider`, `few` | the reasons of the cover rule above |

An `ask` for a person with no answers keeps its reason: "Limen has no
answers from this person yet. It suggests ask." The `few` reason holds
the two settings, which are counts and not probabilities. No reason of
an `ask` holds a probability or a decimal number.

The reasons of `allow` and `block` keep their probabilities. Nobody is
asked about them, and they are records.

**The rule for clients.** When an action waits for the person, the
client shows the action, its label, and its scope. For `wider`, it can
also show the label of the nearest approval. These are facts about the
action, not a lean. The client does not show `approve`, the reason, or
an explanation before the person answers. The study of the analysts
found that people follow an opinion that they see first, and that they
repeat its reasons.

**The decision on recommendations** (owner, 2026-11-02). A client can
show a recommendation before the answer, such as a mark on one button,
only where the `calibration` of the explanation is `calibrated` for the
bin of the prediction. The calibration report comes in wave 2, so in
wave 1 an explanation has no `calibration`, and no client shows a
recommendation. A client that shows one sends `suggestion_shown` true.
An approval that follows a shown lean (`approve` above 0.5) without
details has weight 0 under `weights/1`, so it never builds the cover.
A recommendation can then help the person, but it cannot teach Limen to
run an action alone.

After the answer, or if the person asks why, the client shows both
sides of the explanation. It shows the evidence for approval and the
evidence for denial, never one side alone. A client that shows any part
of the suggestion before the answer sends `suggestion_shown` true.

### Asks that a decision point keeps

The owner decided this on 2026-11-02 (decision 7). Before it, a `split`
ask could rest on answers that agree ([[009-explanations]], amendment of
2026-11-02). One allow gives an evidence ratio of 0.54, so the second
action of a type was a `split` ask. In the `learned` mode of Topos, a
`split` ask goes to the person, so each approval made the next action
wait for the person, until the probability passed the threshold.

An `ask` in the ask band with an evidence ratio of 0.5 or more now has
one of two kinds:

| `ask_kind` | When |
|---|---|
| `split` | the evidence ratio is 0.5 or more, and in a family of the action, the latest answers of the answered cases in the window hold an approval and a denial |
| `short` | the evidence ratio is 0.5 or more, and in each family of the action, the latest answers of the answered cases in the window agree. A family with no answered case agrees |

A decision point keeps `split` and `wider` for the person, and lets
its rules decide `unsure`, `short` and `few`. An older decision point
reads `short` as `unsure`. The cut 0.5 stays a measure of evidence. It
is no longer the test of consent, because the cover rule is that test.
The weight of an answer does not change the kind: a quick denial and a
considered approval disagree.

### What not to do

| Do not | Why |
|---|---|
| accept free text in an answer, such as a note or the pattern of a standing rule | it can carry the words of the agent or a secret, and later code could read it as an instruction |
| read the messages of the agent, its reasoning, or the output of a tool | an agent can argue a check into a bad verdict. Anthropic found that its classifier did better without them |
| read the task of a subagent | the agent writes it, and it is text |
| infer consent from the message of the person, such as "clean up the PR" | that needs a language model that reads text. Limen learns consent from answers only. A judge of the decision point can send its view as a signal |
| use the weight of an answer in an estimate or a certificate | the estimates are Horvitz-Thompson over the reviews. A weight that depends on the answer biases them |
| let a decision point make the reading of the input narrower | the agent writes the input, so its reading is a worst case. Only a hold that the environment enforces is narrower |
| keep a hash of a host, a path or a branch name | a list of names finds the string again |
| compare scopes across families | one allow of a wide action would then cover each narrower action of any type |
| put the time of an answer into the features | it describes the answer, not the action, and the model predicts the answer for the action |
| show the lean of Limen before the person answers | the person follows a lean that is shown first |
| choose which actions to ask about for the sake of learning | still a non-goal of [[004-engine]] |

### Terms

These terms go into `docs/terms.md` with the build of each part:

| Term | Meaning | In the API |
|---|---|---|
| scope | The reach of an action. It tells if the action can be undone, the boundaries it crosses, the class of its target, and if it is a first contact. | `scope` |
| hold | A limit that the environment of an action enforces, so a reach cannot occur. | `holds` |
| family | The type of an action for the cover rule: the tool, or the program and subcommand of a shell segment. | |
| cover | The approvals of a person that have a scope that the scope of an action is within, in the same family. | `cover` |
| covered | An action whose cover has enough weight from enough different sessions. | `cover.covered` |
| wider | An `ask` for an action whose scope exceeds the scope of each approval of its family. | `ask_kind` |
| few | An `ask` for an action that approvals cover, but not with enough weight or sessions. | `ask_kind` |
| short | An `ask` on evidence whose answers agree, but are not enough yet to give `allow` or `block`. | `ask_kind` |
| weight of an answer | A number from 0 to 1 that tells how much an answer counts for the model and the cover. | |
| considered answer | An answer with weight 1. | `considered` |
| calibration report | The approval probabilities that Limen stated, compared with the approval rates that reviews measured. | `Calibration` |

### What other specs get

| Spec | Change |
|---|---|
| [[002-contract]] | the new fields of `Action`, `Target`, `Environment`, `Answer`, `RecordedAnswer`, `ReplayRecord`, `Prediction`, `Explanation`, `LearnedEntry`, `Certificate` and `Profile`. The verdict of a prediction is the verdict of the thresholds after the cap |
| [[003-http-api]] | the new fields, and `GET /v1/calibration` |
| [[004-engine]] | the scope on each case, `features/2`, `learned/2`, the likelihood with weights, and the decision between a refit and criterion 10 |
| [[006-conformance]] | `PredictionFollowsTheProfile` compares the verdict with the thresholds after the cap. New cases test the cap, the weights and the calibration report |
| [[008-ledger-and-estimation]] | the `considered` count of the certificate, and the rule that no estimate reads the weight |
| [[009-explanations]] | `cover` and `calibration` on an explanation, and the reasons of an `ask`. The cap applies to the verdict that an explanation calculates again, to `learned`, and to previews. `split` and `short` (decision 7) |
| [[010-organizations]] | the `consent` of a profile and its meet |

The cover rule is shared arithmetic, like the estimates. `estimate/`
holds it, the engine and the stub both use it, and the stub reads the
scope with `engine/featurize`, which has no numerical dependency. The
engine and the stub then give the same verdicts and covers for the same
ledger.

## Decisions

The owner approved this design on 2026-11-02 and took each
recommendation, with decision 7 and decision 10 as below.

| # | Decision | Taken |
|---|---|---|
| 1 | `learned/2`: a refit, or two versions side by side (criterion 10 of [[004-engine]]) | a refit, after a replay comparison. The cap ships before it, on version 1 |
| 2 | Cover: only in the window of the model, or with its own decay | the window of the model, with no other decay |
| 3 | The weight of an answer in the fit of `learned/2`: 0 or 1 as in the cover, or `weights/2` with values between | `weights/2` in the fit, and 0 or 1 in the cover |
| 4 | An answer with no new fields: its weight from the time Limen measured, or weight 1 | the time Limen measured, so a quick old approval does not open the cover |
| 5 | The floor and the fatigue count | 2,000 ms and 50, and measure them again from the first month of answer times |
| 6 | The certificate: count only considered audits in its clean run | not now. The certificate counts them in `considered`. Decide again when clients send the new fields |
| 7 | `split` and `short` | `short` is an ask in the ask band at an evidence ratio of 0.5 or more whose family answers agree. `split` needs an approval and a denial in the family. Decision points let the rules decide `short`, as `unsure` and `few`, and keep `split` and `wider` for the person ([Asks that a decision point keeps](#asks-that-a-decision-point-keeps)) |
| 8 | The cut 0.5 of the evidence ratio | keep it as a measure of evidence. The cover rule is the test of consent |
| 9 | Can a covered `allow` make an action run that the risk score of the decision point would ask about | yes, only if covered, and never above the ceiling of the rules. This is a setting of the decision point ([[005-adapters]]). In Topos, `TOPOS_DECISIONS_LOOSEN=covered` is the default |
| 10 | Recommendations before the answer | only where the `calibration` of the bin is `calibrated`, and an approval that follows a shown lean without details has weight 0 ([the decision on recommendations](#asks-without-a-lean)) |

## Build waves

| Wave | Limen | Proof |
|---|---|---|
| 0 | the interim rule on main: 3 allows of one label in 2 sessions | done outside this spec |
| 1 | the scope of each case and the scope of old cases, `labels/2`, and the cover rule with its cap on version 1. Also the answer fields, `weights/1`, `considered` on the certificate, the reasons without a lean, `cover`, and `split` and `short` | criteria 1 to 15, 20 for the scope of old cases, 22 and 23 |
| 2 | the calibration report, and `calibration` on an explanation | criteria 16 and 17 |
| 3 | `features/2`, `learned/2` with weights, the refit, after a replay comparison | criteria 18, 19 and 21 |

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | The reader gives the scope of each row of the shell table. A chain gets the greatest value of its segments in each dimension | a table test in `engine/featurize` with one command for each row |
| 2 | A refspec with a name of the table gives `primary`. Other names and no refspec give `unknown`. No ref name appears in the scope | a table test, and a test that searches the stored case for the ref name |
| 3 | A claim makes a dimension wider and never narrower. Two values that are not comparable give the greatest value. Each hold bounds its dimension as its table says | a table test over pairs of a reading and a claim |
| 4 | A value outside the vocabulary of a scope field is `unknown`, and it is not an error | a fuzz test of the scope fields |
| 5 | The order of scopes is a partial order: reflexive, antisymmetric and transitive over each dimension with `unknown` | a property test over all triples of values |
| 6 | No stored label or scope holds a string of the input or of `target.id` | a store test that searches every label and scope for markers in the input and in `target.id` |
| 7 | `labels/2` gives each label of the example table, and two actions that differ only in scope have different labels | a featurize test |
| 8 | An action is covered if and only if, in each family, its covering approvals have `min_weight` and `min_sessions`. A case with no session counts as its UTC day | an `estimate/` table test |
| 9 | The cap changes `allow` to `ask` for an action that is not covered. It gives `wider` or `few` by their definitions. It changes no number of the prediction | an engine test and a conformance case |
| 10 | Under the Balanced profile, after one allow of `publish: update`, the next `publish: update` is `few`. A `publish: make public` is `wider` | an engine test that repeats the measurement of the Overview |
| 11 | The engine, the stub and the client give the same verdict, `ask_kind` and `cover` for the same ledger | the conformance suite |
| 12 | `min_weight` and `min_sessions` of a profile below 1 are refused, a profile cannot go below the installation, and the meet takes the greater value | profile tests and a conformance case |
| 13 | Each rule of `weights/1` gives weight 0 to an approval. A denial has weight 1, except from a `rule`. The time is the smallest of the three times | an `estimate/` table test |
| 14 | The reason of an `ask` holds no number | an engine test over each `ask_kind` |
| 15 | No estimate and no certificate changes when the weights change. Only `considered` changes | an `estimate/` test that runs the reports with two weight policies |
| 16 | The calibration report gives the HT approval rate in each bin, null below an effective size of 20, and each state by its definition | an `estimate/` test against values calculated by hand, and a seeded Monte Carlo test that a calibrated model is `calibrated` in each estimable bin |
| 17 | `GET /v1/calibration` asks `reliability.read` with the subject-bound resource, and `api/openapi.yaml` describes it | server tests and the `api` tests |
| 18 | `features/2` gives 63 and 61 dimensions for each valid action, each group obeys the distance bound, and it never errors | table and fuzz tests in `engine/featurize` |
| 19 | With each weight 1, the fit gives the golden fixtures within 1e-9. With weights, the contributions sum to the mean within 1e-9. An answer with weight 0 changes nothing | `engine/gp` tests |
| 20 | An old case gets the scope of the migration table. An old approval of unknown reach never covers a new action of known greatest reach | an engine test over a ledger written by the release before |
| 21 | A replay of the ledger gives the would-be verdicts of version 2 beside the recorded predictions of version 1 before version 2 serves | the replay command and the record of the deployment |
| 22 | `split` needs an approval and a denial in the family, `short` is agreeing answers, and an older client reads `short` as `unsure` | an engine test and a decision point test |
| 23 | A replay records an answer at `answered_at` with its six fields, and the importer of Topos logs sets `answered_at` from the confirmation | an engine test and an importer test |
