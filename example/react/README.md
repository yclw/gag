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

Control requests return immediately with `202 Accepted`. Observe the session
from any number of clients using the long-lived SSE endpoint:

```bash
curl -N http://localhost:8080/events
```

Each connection first receives a `session.snapshot` containing the persisted
graph checkpoint, followed by output already emitted by the active execution
and then all new events in real time.

Then send a message:

```bash
curl http://localhost:8080/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"Hello"}'
```

Events are streamed by `/events`:

```text
event: model.delta
data: {"node_id":"react.model","chunk":{...}}
```

If a tool emits a `tool.review` interrupt, approve or reject it using the
request ID from that event:

```bash
curl http://localhost:8080/review \
  -H 'Content-Type: application/json' \
  -d '{"request_id":"add.review.<tool-call-id>","approved":true}'
```

Cancel the active execution with:

```bash
curl -X POST http://localhost:8080/cancel
```

If no execution is active, `/cancel` returns `202 Accepted` and runs the graph
with an already cancelled context so nodes can settle at the next interrupt.
If an execution is active, it cancels that context and returns `204 No
Content`. Only one execution may run at a time; concurrent `/run`, `/chat`,
and `/review` requests return `409 Conflict`.

Call `/chat` again for subsequent messages. The server restores the same
session from SQLite.
