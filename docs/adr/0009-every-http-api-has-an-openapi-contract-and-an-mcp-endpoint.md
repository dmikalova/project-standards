# 9. Every HTTP API has an OpenAPI contract and an MCP endpoint

Status: proposed.

This decision records how a project documents its HTTP API, and how an AI
client uses the same API.

## Context

Several projects serve HTTP endpoints that other programs call:

| Project | Endpoints for programs |
| --- | --- |
| keyforge-card-search | `GET /api/cards` (JSON export), `GET /icons/...` |
| keyfordle | `GET /api/decks/public/...`, `POST /api/decks/public/record_keyfordle_completion/` |
| keyforge.cards | `GET /health` |
| keyforge-data (planned, keyforge-data ADR 0006) | cards, datasets, problems, later decks |

No project has an OpenAPI file, a docs page, or an MCP server. Each project
chooses its own error shape. For example, keyforge-card-search sends
`{"error": "..."}`. The only documentation is in the code and in the
`AGENTS.md` files.

The user wants one standard for all projects, and wants the APIs to be easy
for AI clients to use.

All apps run on Cloud Run with a minimum of zero instances. The standard
must not add an always-on service.

A Cloud Run front end returns its own 404 for `/healthz`. search.keyforge.cards
returns 404 for `/healthz` and 200 for `/health`.

## Decision

**An endpoint for programs is an API endpoint. A project with API endpoints
has an OpenAPI contract, a docs page, an `llms.txt` file, and an MCP
endpoint.** Pages and Datastar batches are not API endpoints.

### Paths

| Path | Content |
| --- | --- |
| `/api/...` | The API endpoints |
| `/openapi.json` | The OpenAPI 3.1 contract |
| `/docs` | The docs page, made from the contract |
| `/llms.txt` | A short text guide for AI clients, with links to `/openapi.json` and `/mcp` |
| `/mcp` | The MCP endpoint |
| `/health` | The health check. Do not use `/healthz`. |

### Contract

- Write the contract by hand in `api/openapi.yaml`. Embed it in the binary
  and serve it as JSON at `/openapi.json`.
- Do not generate the contract from Go code, and do not generate Go code from
  the contract. A generator adds a framework to each project. A hand-written
  contract with a drift test is smaller.
- Add a drift test in each project. The test compares the routes of the HTTP
  mux with the paths in the contract. A route without a path fails the test,
  and a path without a route fails the test.
- `/docs` renders the contract with a pinned Scalar API Reference bundle. The
  page has no server code.

### Versions

- Make only additive changes to `/api/...`: new endpoints, new optional
  parameters, and new response fields.
- For a change that is not additive, add `/api/v2/...`. Keep the old path
  until no known client uses it.

### Errors

- Send errors as RFC 9457 problem details (`application/problem+json`) with
  `type`, `title`, `status`, and `detail`.
- For a query error, add `position` (the character offset).

### Rate limits

- Limit each client IP with an in-process token bucket. Cloud Run sends the
  client IP in `X-Forwarded-For`.
- Send `429` with `Retry-After` when a client goes over the limit.
- The limit applies to `/api/...` and `/mcp`.
- The limit lives in the instance memory. A new instance starts with full
  buckets. That is sufficient for public read-only data.

### CORS

- Send `Access-Control-Allow-Origin: *` on public `GET` endpoints.
- Do not send CORS headers on endpoints that change data.

### MCP

- Each project serves its own MCP endpoint at `/mcp`, with the Streamable
  HTTP transport, in stateless mode. Use the official Go SDK
  (`github.com/modelcontextprotocol/go-sdk`).
- Stateless mode keeps no session in the instance. An instance can stop
  between calls, so the endpoint works with a minimum of zero instances.
- Each MCP tool calls the same Go function as its API endpoint. A tool has no
  logic of its own.
- Public read-only tools need no authentication. Tools that change data need
  authentication. Write a separate decision for those.
- Do not make one MCP gateway for all projects. A gateway is one more service
  to deploy, and it must change when a project changes.

### Shared code

- Put the shared HTTP code in a separate Go module in this repository:
  `github.com/dmikalova/project-standards/httpapi`, with its own `go.mod`.
  It holds the problem details writer, the rate limit middleware, the CORS
  middleware, the `/openapi.json`, `/docs`, and `/llms.txt` handlers, and the
  drift test helper.
- The separate module keeps the CI dependencies of the root module out of
  the apps.

### Conformance

- Add `api: true` to `mklv.config.json` for a project with API endpoints.
- The check of such a project fails if `api/openapi.yaml` is missing or is
  not valid OpenAPI 3.1, or if `llms.txt` is missing.

## Consequences

- keyforge-card-search changes its error shape from `{"error": "..."}` to
  problem details. Its JSON export is the first endpoint to get a contract.
- keyfordle documents its `/api/decks/public/...` endpoints. It keeps the
  paths, because the paths copy the Master Vault paths that its client
  expects.
- An AI client that supports remote MCP servers can use each app at
  `https://<app>/mcp`. A client without MCP reads `/llms.txt` and
  `/openapi.json`.
- The cost does not change. The MCP endpoint and the docs run in the existing
  Cloud Run service.
