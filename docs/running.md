# Running limend

`limend` supplies the [API](api.md) and keeps the ledger in a directory. A
decision point, for example Topos, sends actions, decisions, and answers to
`limend`. `limend` predicts from these records. It updates the model of
each person in the background, not while a decision waits.

At this time, `limend` supplies the API for one person, who has one bearer
token. A hosted installation checks tokens from its own issuers and uses
its own authorization endpoint.
[`specs/001-architecture.md`](../specs/001-architecture.md) describes a
hosted installation. You cannot configure one yet.

## Get limend

To build the two programs from a checkout of this repository, run:

```sh
make build
```

This command writes the server, `out/limend`, and the replay command,
`out/limen`. Each program contains the identifier of the commit that it
came from.

To run the server without a build first, run `go run ./cmd/limend`.

A release tag publishes the image `ghcr.io/latere-ai/limen:<tag>`. The
image has a signature, a bill of materials, and a provenance attestation.
The image runs `limend` as a user that is not root.

To see the build of the server, run `limend -version`. It shows the tag,
the commit, and the date.

## Configuration

`limend` reads its configuration from environment variables. It has no
flags other than `-version`.

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_TOKEN` | The bearer token that each request must have. You must set it. | None |
| `LIMEN_ADDR` | The address that the server listens on. | `127.0.0.1:7720`. In the image: `:7720`. |
| `LIMEN_DATA_DIR` | The ledger directory. | `$XDG_STATE_HOME/limen`, or `~/.local/state/limen`. In the image: `/var/lib/limen`. |
| `LIMEN_SUBJECT` | The person that the server answers to. This person can act for each person in the ledger. | `local` |

The token identifies the person in `LIMEN_SUBJECT`. The shared owner
policy decides what this person can do. This person owns the server, so
this person can do all operations. If a request has no token, or a
different token, `limend` refuses it with `unauthenticated`.

## The ledger directory

One process owns a ledger directory. While one process holds the
directory, Limen refuses a second process on it. This applies to a second
`limend`, and to `limen replay` with `--keep --store` set to the same
directory. On Linux and macOS, the lock stops when the process stops.
After a crash, you do not have to remove the lock.

Limen keeps the records of each person in a log. Limen only adds lines to
the log. It writes each line to the disk before it continues. If a crash
interrupts a write, Limen discards the incomplete line the next time that
it reads the log.

Limen writes each model snapshot and each cost profile as a complete file,
in one step. A crash cannot leave a part of such a file. Limen never
writes the input of an action. It writes only the features that it
calculates from the input.

To make a backup of the ledger, do these steps:

> **Warning:** Stop `limend` before you copy the ledger directory. A copy
> that you make while `limend` runs can be incomplete.

1. Stop `limend`.
2. Copy the ledger directory.
3. Start `limend` again.

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
2. It lets the requests in progress finish, for not more than ten seconds.
3. It updates the models with the records that arrived after the last
   update. After a restart, the predictions use these records.
4. It stops.

| Exit code | Meaning |
|---|---|
| 0 | `limend` stopped correctly. |
| 1 | `limend` failed. |
| 2 | The configuration has an error. |

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
