# Responses WebSocket

Modelserver accepts authenticated WebSocket upgrades at `GET /v1/responses`.
WebSocket turns use the **`openai_responses_websocket`** request kind, while
`POST /v1/responses` uses **`openai_responses`** for HTTP/SSE. The two kinds
match routes independently and can select different upstream groups for the
same model, project and client.

Migration `076_responses_websocket_request_kind.sql` registers the new kind.
It does not change existing routes. In the routing dashboard, create a route
with `openai_responses_websocket` and select a group containing native `openai`
or `codex` upstreams. Select both kinds explicitly if one route should serve
both transports. An HTTP-only route never acts as a WebSocket fallback.

This implementation follows the local OpenAI Codex source:

- `codex-rs/core/src/client.rs`: handshake headers, prewarming and connection reuse.
- `codex-rs/codex-api/src/common.rs`: flat `response.create` payloads.
- `codex-rs/codex-api/src/endpoint/responses_websocket.rs`: terminal events and errors.

For a Codex custom provider, enable WebSockets in its existing provider entry:

```toml
[model_providers.modelserver]
name = "modelserver"
base_url = "https://YOUR_MODELSERVER/v1"
env_key = "MODELSERVER_API_KEY"
wire_api = "responses"
supports_websockets = true
```

Clients authenticate the upgrade using the same bearer API key or OAuth token
as HTTP requests. The first message supplies the model:

```json
{"type":"response.create","model":"gpt-5","input":[{"role":"user","content":"Hello"}],"store":false}
```

Responses arrive as JSON text messages. After a terminal response, another
`response.create` can use its `previous_response_id` with incremental input.
`generate:false` prewarms, tool calls, encrypted reasoning and `client_metadata`
are forwarded to the upstream. The connection uses Codex's
`OpenAI-Beta: responses_websockets=2026-02-06` handshake.

Each turn repeats authentication, model permissions, session requirements,
subscription eligibility and rate-limit checks, and records its own usage
under `request_kind: openai_responses_websocket`. Request filters and the
routing matrix expose this kind separately from HTTP Responses.
Upstream model mapping, OAuth refresh on handshake failure, outbound proxy
settings, request timeouts and HTTP logging use the existing pipeline. Request
metadata includes `transport: websocket`; stored response bodies use SSE so
existing log viewers can read them.

Connections retain the selected upstream for continuation state. Change models
by opening a new connection. Connections last at most one hour; disconnected or
expired connections require a new connection, with full input when an upstream
cannot resolve `previous_response_id`. Requests already sent upstream are never
automatically replayed after a stream failure.

HTTP-only providers such as native ModelHub, Bedrock and Vertex are not converted
to WebSockets. Without a matching WebSocket route, the request returns an error
with status 404. A WebSocket route with no capable upstream returns an error with
status 426 in the WebSocket error envelope; use HTTP for those routes. Codex's
immediate HTTP fallback is triggered by a 426 during the upgrade, so configure
`supports_websockets = false` for an HTTP-only route. A reverse proxy in front
of modelserver must forward WebSocket upgrades and allow long-lived connections.

Focused protocol tests (use in-memory connections, no listener or live API):

```sh
go test -race ./internal/proxy -run '^TestResponsesWebsocket' -count=1
```
