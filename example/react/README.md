# GAG ReAct Agent Example

A single-user, single-session ReAct agent web server. Graph events are stored
in `react.db` using `modernc.org/sqlite`.

## Run

```bash
export OPENAI_API_KEY="..."
export OPENAI_MODEL="..."
export OPENAI_BASE_URL="..."

go run ./example/react
```

The server listens on `:8080`.

## Usage

Before sending the first message, run the graph so it reaches the user input
interrupt:

```bash
curl -X POST http://localhost:8080/run
```

Then send a message:

```bash
curl -N http://localhost:8080/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"Hello"}'
```

`/chat` streams graph events emitted during the run using SSE:

```text
event: model.delta
data: {"node_id":"react.model","chunk":{...}}
```

If a tool emits a `tool.review` interrupt, approve or reject it using the
request ID from that event:

```bash
curl -N http://localhost:8080/review \
  -H 'Content-Type: application/json' \
  -d '{"request_id":"add.review.<tool-call-id>","approved":true}'
```

`/review` resumes the graph and streams the remaining events using SSE.

Call `/chat` again for subsequent messages. The server restores the same
session from SQLite.
