# Install limend

This page is for the operator of `limend`. It tells how to get the
server and verify it, where it keeps the ledger, how to start, stop,
check, and upgrade it, and what a production installation must have.
[Configuration](configuration.md) gives each environment variable.
[Run limend on your computer](local.md) starts the server from a
checkout.

## Get limend

Each release on the
[releases page](https://github.com/latere-ai/limen/releases) attaches
these files:

- `limend_linux_amd64` and `limend_linux_arm64`, the server for Linux.
- `checksums.txt`, the SHA-256 checksums of the files, and
  `checksums.txt.sigstore.json`, its signature.
- `sbom-limen.spdx.json`, the bill of materials of the image.

A release also publishes the image `ghcr.io/latere-ai/limen:<tag>`. The
image has a signature, a bill of materials, and a provenance attestation.
The image runs `limend` as a user that is not root.

To install the two programs with Go 1.27 or newer, run:

```sh
go install latere.ai/x/limen/cmd/limend@latest
go install latere.ai/x/limen/cmd/limen@latest
```

A program from `go install` shows the version `dev`.

To build the two programs from a checkout of this repository, run:

```sh
make build
```

This command writes the server, `out/limend`, and the replay command,
`out/limen`. Each program contains the identifier of the commit that it
came from.

To run the server without a build first, run `go run ./cmd/limend`.

To see the build of the server, run `limend -version`. It shows the tag,
the commit, and the date.

Before you run a release, verify it.
[Verify a release](../SECURITY.md#verifying-a-release) gives the commands
that check the signature of `checksums.txt`, the checksums of the files,
and the signature and the provenance attestation of the image.

## Start and stop limend

To start the server, do these steps:

1. Make a secret token:

   ```sh
   export LIMEN_TOKEN=$(openssl rand -hex 16)
   ```

2. Start the server:

   ```sh
   limend
   ```

To stop the server, send an interrupt (Ctrl-C) or a termination signal.
`limend` then does these steps:

1. It stops the acceptance of new requests.
2. It lets the requests in progress finish.
3. It updates the models with the records that arrived after the last
   update. After a restart, the predictions use these records.
4. It stops.

Steps 2 and 3 together have ten seconds. If the models are not updated
in that time, `limend` stops with exit code 1. The records are not lost:
the next start learns them.

| Exit code | Meaning |
|---|---|
| 0 | `limend` stopped correctly. |
| 1 | `limend` failed. |
| 2 | The configuration has an error. |

[Values that stop a start](configuration.md#values-that-stop-a-start)
gives each cause of exit codes 1 and 2 when `limend` starts.

## Check that limend runs

`limend` has no health route. Each route needs a bearer token. To check
the server, use one of these methods:

- Open a TCP connection to the port of `limend`. `limend` opens the port
  after it opens the ledger directory and reads the key set of each
  issuer. If it cannot read a key set, it stops.
- Send a request with a token, for example
  `GET /v1/profiles?org=<org>`. Status 200 tells you that the server
  answers and can read the ledger.

In Kubernetes, use a `tcpSocket` probe for readiness and for liveness.

## Run limend in a container

> **Warning:** In the container, `limend` listens on all network
> interfaces. If you publish the port on an address other than loopback
> (`127.0.0.1`), other computers can send requests to `limend`. Publish
> the port on loopback, unless other computers must connect to `limend`.

1. Set `LIMEN_TOKEN` as in [Start and stop limend](#start-and-stop-limend).
2. Start the container:

   ```sh
   docker run -d --name limen \
     -e LIMEN_TOKEN="$LIMEN_TOKEN" \
     -p 127.0.0.1:7720:7720 \
     -v limen:/var/lib/limen \
     ghcr.io/latere-ai/limen:<tag>
   ```

The user of the image owns the ledger directory. If you attach a named
volume at this directory, `limend` can write to it. You do not have to
change its permissions.

`docker stop` sends the termination signal. `limend` updates the models
before the container stops.
## The ledger directory

One process owns a ledger directory. While one process holds the
directory, Limen refuses a second process on it. This applies to a second
`limend`, and to `limen replay` and `limen compare` with `--store` set to
the same directory. On Linux and macOS, the lock stops when the process
stops.
After a crash, you do not have to remove the lock.

Limen keeps the records of each person in a log. Limen only adds lines to
the log. It writes each line to the disk before it continues. If a crash
interrupts a write, Limen discards the incomplete line the next time that
it reads the log.

Limen writes each model snapshot, each cost profile, and each kept week
of a report of an organization as a complete file, in one step. A crash
cannot leave a part of such a file. The kept weeks are in the `reports`
directory. A forget of a person does not remove them, because they
contain no record of a person. Limen never
writes the input of an action. It writes only the features that it
calculates from the input.

To make a backup of the ledger, do these steps:

> **Warning:** Stop `limend` before you copy the ledger directory. A copy
> that you make while `limend` runs can be incomplete.

1. Stop `limend`.
2. Copy the ledger directory.
3. Start `limend` again.

## Logs and telemetry

`limend` writes its messages to the standard output and the standard
error. It writes a log line for each response with a status of 500 or
more, with the details for a developer. `limend` sends no telemetry. It
ignores the `OTEL_*` environment variables.

## Upgrade limend

Before you upgrade, read the section of each release in the
[changelog](../CHANGELOG.md), from your version to the new version. Each
section names each incompatible change and each step that an operator
must do.

To upgrade, do these steps:

1. Stop `limend`. It updates the models before it stops.
2. Copy the ledger directory, as in
   [The ledger directory](#the-ledger-directory).
3. Replace the binary, or set the image to the new tag.
4. Start `limend`.
5. Run `limend -version`, or send `GET /v1/version`, and examine the
   version.

Only one process can use the ledger directory. Because of this, the old
process must stop before the new process starts. In Kubernetes, use the
`Recreate` strategy for the Deployment.

## Switch the model

`limend` uses one model version for all predictions: `learned/1` or
`learned/2` ([Theory](theory.md#model-versions)). `learned/1` is the
default. `learned/2` also reads the scope of an action, and it gives
less weight to a quick approval. Before you change the model, compare
the two versions on your ledger.

To compare the two versions, do these steps:

1. Copy the ledger directory.
2. On a system other than Linux or macOS, remove the file `LOCK` from the
   copy.
3. Run the model comparison on the copy:

   ```sh
   limen compare ledger --store /path/to/the/copy
   ```

You do not have to stop `limend` before the copy. The model comparison
discards an incomplete last line of a log, and it changes nothing in the
ledger of `limend`.

`limen compare` replays the cases of the ledger through each version, in
the order of time. Each version learns an answer before the next
prediction. The model comparison keeps nothing. It reads the features
and the scope that each case kept, because the ledger does not keep the
input. A ledger with thousands of answers can take some minutes.

The model comparison gives these numbers for all persons, and for each
person:

- The verdicts that each version suggests, with the kind of each `ask`.
- The actions where the verdict is different, from the verdict of
  `learned/1` to the verdict of `learned/2`.
- The allows of `learned/1` that `learned/2` does not give, and the
  allows of `learned/2` that `learned/1` does not give.
- The verdicts of `learned/2` that permit more than the verdicts of
  `learned/1`. The model comparison lists each one, and tells if the
  cover rule holds it.
- The calibration error of each version, from the
  [calibration report](integrating.md#the-calibration-report) of its
  replayed predictions with the reviews of the ledger. A bin counts only
  if its reviews have an effective size of 20 or more. If no bin counts,
  the calibration error is not known.
- The Brier score of each version: the mean of the squared difference
  between the approval probability and the answer. A lower score is
  better. The answers are only the answers to the actions that a person
  saw.
- The number of recorded predictions whose verdict `learned/1` suggests
  again.

To compare the two versions on the sessions of Topos, use
`limen compare topos`. To compare them on a file of records, use
`limen compare file`. The two commands read the same sources as
`limen replay`. Add `--json` to get the numbers as JSON.

> **Warning:** Before you change the model, read each verdict of
> `learned/2` that permits more. If the cover rule does not hold a
> verdict, examine its case. Do not change the model if you cannot
> explain the verdict.

To change the model to `learned/2`, do these steps:

1. Set `LIMEN_MODEL` to `learned/2`:

   ```sh
   export LIMEN_MODEL=learned/2
   ```

2. Restart `limend`.
3. Find the line `The model is learned/2.` in the output of `limend`.

After the change, the first prediction for each person starts the fit of
`learned/2` from the cases in the ledger. Until the fit is complete,
Limen predicts from the prior for that person: the approval probability
is 0.5, and the suggestion is `ask`. The fit takes approximately one
second. A prediction never waits for a fit.

To go back to `learned/1`, do these steps:

1. Remove `LIMEN_MODEL`, or set it to `learned/1`.
2. Restart `limend`.

The fitted models of `learned/1` stay in the ledger directory. Each case
keeps the features of `learned/1`. `learned/1` then learns the answers
that came after the change, in the background.

## What a production installation must have

- **The hosted mode.** Set `LIMEN_OIDC_ISSUERS` and `LIMEN_OIDC_AUDIENCE`
  ([Hosted](configuration.md#hosted)). A token for one person, in
  `LIMEN_TOKEN`, lets each holder of the token act for each person in the
  ledger.
- **A decision about permissions.** Set `LIMEN_AUTHORIZER_URL` to your
  authorization endpoint ([Writing an authorization endpoint](authorizer.md)),
  or use the owner policy. With the owner policy, put in `LIMEN_ADMINS`
  only the service that runs your decision point.
- **TLS in front of `limend`.** `limend` supplies plain HTTP only. Put a
  proxy or an ingress that terminates TLS in front of it.
- **One replica, with the ledger on a persistent volume.** One process
  owns a ledger directory. Use a volume that one node mounts at a time,
  and stop the old process before the new process starts.
- **Backups of the ledger.** Stop `limend`, copy the ledger directory,
  and start `limend` again ([The ledger directory](#the-ledger-directory)).
- **A limit in front of `limend`.** `limend` does not limit the rate of
  requests, and it does not limit the time that a client takes to send a
  request body. A proxy in front can limit both
  ([What Limen does not defend](security.md#what-limen-does-not-defend)).
- **A model cache below the memory limit.** If the container has a
  memory limit, set `LIMEN_MODEL_CACHE_BYTES` to half of it or less
  ([Both modes](configuration.md#both-modes)). The default, 1 GiB, is
  more than many limits, and a container that goes above its limit is
  stopped.
- **Probes on the port.** Use a TCP probe for readiness and for liveness
  ([Check that limend runs](#check-that-limend-runs)).
