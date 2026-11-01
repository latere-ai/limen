# Running limend

`limend` supplies the [API](api.md) and keeps the ledger in a directory. A
decision point, for example Topos, sends actions, decisions, and answers to
`limend`. `limend` predicts from these records. It updates the model of
each person in the background, not while a decision waits.

`limend` runs in one of two modes:

- **One person.** One bearer token identifies one person. Use this mode
  on your own computer.
- **Hosted.** Each caller sends a token from an issuer that you trust.
  `limend` reads the subject of the caller from the token. The owner
  policy or your authorization endpoint decides what the caller can do.

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
flags other than `-version`. Set `LIMEN_TOKEN` for one person, or
`LIMEN_OIDC_ISSUERS` for a hosted server. Do not set both.

These variables apply in both modes:

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_ADDR` | The address that the server listens on. | `127.0.0.1:7720`. In the image: `:7720`. |
| `LIMEN_DATA_DIR` | The ledger directory. | `$XDG_STATE_HOME/limen`, or `~/.local/state/limen`. In the image: `/var/lib/limen`. |

### One person

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_TOKEN` | The bearer token that each request must have. | None |
| `LIMEN_SUBJECT` | The person that the server answers to. This person can act for each person in the ledger. | `local` |

The token identifies the person in `LIMEN_SUBJECT`. The shared owner
policy decides what this person can do. This person owns the server, so
this person can do all operations. If a request has no token, or a
different token, `limend` refuses it with `unauthenticated`.

### Hosted

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_OIDC_ISSUERS` | The issuers that `limend` accepts tokens from, separated by commas. | None |
| `LIMEN_OIDC_AUDIENCE` | The audiences that a token for `limend` carries, separated by commas. A token must carry one of them. You must set it. | None |
| `LIMEN_ADMINS` | The subjects that can do all operations, separated by commas. Each subject is the issuer and the `sub` of a token, with a pipe between them. Use it only without `LIMEN_AUTHORIZER_URL`. | None |
| `LIMEN_AUTHORIZER_URL` | The authorization endpoint. If you set it, the endpoint decides each request. | None |
| `LIMEN_AUTHORIZER_TOKEN`, `LIMEN_AUTHORIZER_TOKEN_FILE` | The bearer token for the authorization endpoint, or a file that contains it. Set one of them with `LIMEN_AUTHORIZER_URL`. | None |

When it starts, `limend` reads the key set of each issuer. If it cannot
read a key set, it stops with exit code 1. A token must not be older than
one day, and it must carry the time when it was issued.

Without an authorization endpoint, the shared owner policy decides:

- A person can do the operations for their own subject: predict, decide,
  answer, list cases, read reports, and forget their data.
- A subject in `LIMEN_ADMINS` can do all operations for all persons. Use
  it for the service that runs the decision point, for example Topos.
- Only an admin can read or save cost profiles and send replays.

The [API](api.md#permissions) lists the permission that each route needs
from an authorization endpoint.

> **Warning:** A subject in `LIMEN_ADMINS` can read and change the ledger
> of each person. Add only the service that runs your decision point.

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
