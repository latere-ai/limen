# Run limend on your computer

This page is for a person who wants to try `limend` from a checkout of
this repository. It starts the server for one person, sends one action,
its decision, and its answer, and reads the ledger. `limend` needs no
other service and no database: it keeps the ledger in a directory.

You need bash, Go 1.27 or later, `git`, `curl`, and `jq`.

## Start the server

Get the code, and build the server and the replay command:

```sh
git clone https://github.com/latere-ai/limen.git && cd limen
make build
```

`make build` writes `out/limend` and `out/limen`. Start the server for
one person, with a ledger in a new directory. The last line waits until
the server answers:

```sh
export LIMEN_TOKEN=$(openssl rand -hex 16) LIMEN_DATA_DIR=$(mktemp -d)
./out/limend &
until curl -sf http://127.0.0.1:7720/v1/version > /dev/null; do sleep 0.2; done
```

`limend` writes the address, the ledger directory, and the model when it
starts. `GET /v1/version` needs no token. It gives the version of the
build.

## Send an action

A decision point makes three calls for each action. It gets a
prediction, it records its decision, and it sends the answer of the
person:

```sh
L=http://127.0.0.1:7720
H=(-H "Authorization: Bearer $LIMEN_TOKEN" -H 'Content-Type: application/json')
KEY='"org":"local","subject":"local","action_id":"deploy-1"'
curl -s "${H[@]}" $L/v1/predictions -d "{$KEY,\"kind\":\"tool\",\"name\":\"deploy\",
  \"effect\":\"external\",\"actor\":{\"kind\":\"agent\",\"id\":\"ci\",\"session\":\"s1\"}}" |
  jq '{approve, verdict, ask_kind}'
curl -s -w '%{http_code}\n' "${H[@]}" $L/v1/decisions -d "{$KEY,\"verdict\":\"ask\",\"review_probability\":1,\"source\":\"local\"}"
curl -s -w '%{http_code}\n' "${H[@]}" $L/v1/answers -d "{$KEY,\"approve\":true,\"details_opened\":true,\"surface\":\"prompt\"}"
```

Limen has no answers from this person, so the prediction is `ask`, with
the approval probability 0.5 and the `ask_kind` `unsure`. The decision
and the answer each give the status 204.

## Read the ledger

The case of the action now has its prediction, its decision, and its
answer:

```sh
curl -s "${H[@]}" "$L/v1/cases?org=local&subject=local" |
  jq -c '.entries[] | {action_id: .action.action_id, verdict: .decision.verdict, review: .answers[0].review}'
```

The output is one line. The answer is the review of the action, because
the decision showed the action to the person:

```
{"action_id":"deploy-1","verdict":"ask","review":true}
```

## Stop the server

```sh
kill %1
```

`limend` updates the models with the new answer, then stops. The ledger
stays in `$LIMEN_DATA_DIR`. If you start `limend` again with the same
directory, it continues from these records.

## Next

- [Integration](integrating.md) tells what a decision point sends, and
  how to start safely.
- [API](api.md) gives each route.
- [Configuration](configuration.md) gives each environment variable, and
  the hosted mode with tokens from your issuers.
- [Install limend](install.md) tells how to run `limend` for other
  persons.
