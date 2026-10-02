# Running limend

`limend` serves the [API](api.md) over a ledger kept in a directory. A
decision point such as Topos sends it actions, decisions and answers;
`limend` predicts from them and refits each person's model in the
background, off the decision path.

Today it serves one person, who holds one bearer token. A hosted
installation, which verifies tokens from its own issuers and asks its own
authorization endpoint, is described in
[`specs/001-architecture.md`](../specs/001-architecture.md) and not yet
configurable.

## Getting it

From a checkout, `make build` writes `out/limend` and the replay command
`out/limen`, each stamped with the commit it was built from. `go run
./cmd/limend` runs the server without building it first.

A release tag publishes the image `ghcr.io/latere-ai/limen:<tag>`, signed,
with a bill of materials and a provenance attestation. The image runs
`limend` as a non-root user.

`limend -version` prints the build: the tag, the commit and the date.

## Configuration

`limend` reads its environment and takes no flags but `-version`.

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_TOKEN` | the bearer token every request must carry; required | none |
| `LIMEN_ADDR` | the address to listen on | `127.0.0.1:7720`; `:7720` in the image |
| `LIMEN_DATA_DIR` | the ledger's directory | `$XDG_STATE_HOME/limen`, or `~/.local/state/limen`; `/var/lib/limen` in the image |
| `LIMEN_SUBJECT` | the person the server answers to, who may act for every subject in it | `local` |

The token stands for `LIMEN_SUBJECT`, and the shared owner policy decides
what it may do: everything, since that person owns the server. A request
without the token, or with another, is refused with `unauthenticated`.

## The ledger

One process owns a directory: a second `limend`, or a `limen replay
--keep` over the same directory, is refused while the first holds it. On
Linux and macOS the lock is released when the process exits, so a crash
leaves nothing to clean up.

Each person's records are an append-only log, synced on every write; a
write cut short by a crash is dropped when the log is next read. Model
snapshots and cost profiles are written whole and atomically. The input
of an action is never written: only the features read from it are.

To back the ledger up, copy the directory while `limend` is stopped.

## Starting and stopping

```sh
export LIMEN_TOKEN=$(openssl rand -hex 16)
limend
```

On an interrupt or a termination signal, `limend` stops accepting
requests, lets those in flight finish for up to ten seconds, fits what
arrived since the last fit so a restart predicts from it, and exits. It
exits 0 when stopped, 1 when it fails, and 2 on a configuration error.

## In a container

```sh
docker run -d --name limen \
  -e LIMEN_TOKEN="$LIMEN_TOKEN" \
  -p 127.0.0.1:7720:7720 \
  -v limen:/var/lib/limen \
  ghcr.io/latere-ai/limen:<tag>
```

Inside the container `limend` listens on every interface, so publish the
port on loopback unless something else on the network is meant to reach
it. The ledger directory belongs to the image's user, so a named volume
mounted there is writable without further setup. `docker stop` sends the
termination signal, and the final fit runs before the container exits.
