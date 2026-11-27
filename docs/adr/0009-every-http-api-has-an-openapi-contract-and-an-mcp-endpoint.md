# 9. Every HTTP API has an OpenAPI contract and an MCP endpoint

Status: accepted.

This decision records how a project documents its HTTP API, and how an AI
client uses the same API.

## Context

Several projects serve HTTP endpoints that other programs call:

| Project | Endpoints for programs |
| --- | --- |
| gallery.keyforge.cards | `GET /api/cards` (JSON export), `GET /icons/...` |
| keyfordle | `GET /api/decks/public/...`, `POST /api/decks/public/record_keyfordle_completion/` |
| keyforge.cards | `GET /health` |
| keyforge-data (planned, keyforge-data ADR 0006) | cards, datasets, problems, later decks |

No project has an OpenAPI file, a docs page, or an MCP server. Each project
chooses its own error shape. For example, gallery.keyforge.cards sends
`{"error": "..."}`. The only documentation is in the code and in the
`AGENTS.md` files.

The user wants one standard for all projects, and wants the APIs to be easy
for AI clients to use. New services use Go (ADR 0008). The user plans to move
most TypeScript projects to Go.

All apps run on Cloud Run with a minimum of zero instances. The standard
must not add a separate service.

A Cloud Run front end returns its own 404 for `/healthz`. gallery.keyforge.cards
returns 404 for `/healthz` and 200 for `/health`.

## Decision

**An endpoint for programs is an API endpoint. A project with API endpoints
generates its OpenAPI contract from its Go code, and serves the contract, a
docs page, an `llms.txt` file, and an MCP endpoint.** Pages and Datastar
batches are not API endpoints.

Deno and TypeScript apps are out of scope until they move to Go.

### Paths

| Path | Content |
| --- | --- |
| `/api/...` | The API endpoints |
| `/openapi.json` | The OpenAPI 3.1 contract, generated from the code |
| `/docs` | The docs page, made from the contract |
| `/llms.txt` | A short text guide for AI clients, made from the contract, with links to `/openapi.json` and `/mcp` |
| `/mcp` | The MCP endpoint |
| `/health` | The health check. Do not use `/healthz`. |

### Contract

**Use huma v2 (`github.com/danielgtaylor/huma/v2`) with its `humago` adapter
on the standard `net/http` ServeMux.**

- Each API operation is a huma operation: an `operationId`, a summary, a
  description, and a Go input type and a Go output type. huma makes the
  OpenAPI 3.1 contract from these types and their struct tags. huma also
  validates the input against the same schema.
- The contract comes from the code, so it cannot drift from the code. There
  is no drift test.
- `/docs` uses the huma Scalar renderer (`DocsRendererScalar`).
- Pages and Datastar batches stay plain `http.HandlerFunc` routes on the same
  ServeMux. huma registers only the API routes.

The candidates:

| Library | Direction | OpenAPI | Router | Maturity | License | Result |
| --- | --- | --- | --- | --- | --- | --- |
| huma v2 (v2.41) | Code first | 3.1.0 | ServeMux (`humago`), chi, gin, echo, fiber, and others | Stable v2 since 2024, many releases | MIT | Chosen |
| fuego (v0.21) | Code first | 3.0 model (kin-openapi) | net/http | v0, API still changes | MIT | Not stable |
| swaggest/openapi-go (v0.2) | Code first, reflection only | 3.0 and 3.1 | None: the app connects routes and schemas by hand | v0 | MIT | Too much hand work |
| swaggo/swag | Comments | Swagger 2.0 | Any | Stable | MIT | No OpenAPI 3.1, and comments drift |
| ogen, oapi-codegen | Contract first | 3.0 and 3.1 | Generated | Stable | Apache 2.0 | The wrong direction: the user wants Go code as the source |

The lock-in of huma is the handler shape:
`func(ctx context.Context, in *Input) (*Output, error)`. The router does not
lock in, because `humago` uses the standard ServeMux. To leave huma, change
each API handler back to `http.HandlerFunc`. Pages do not change.

### Versions

- Make only additive changes to `/api/...`: new endpoints, new optional
  parameters, and new response fields.
- For a change that is not additive, add `/api/v2/...`. Keep the old path
  until no known client uses it.

### Errors

- Send errors as RFC 9457 problem details (`application/problem+json`) with
  `type`, `title`, `status`, and `detail`. huma sends this shape by default.
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

- Each app serves its own MCP endpoint at `/mcp`, with the Streamable HTTP
  transport, in stateless mode. Use the official Go SDK
  (`github.com/modelcontextprotocol/go-sdk`).
- Stateless mode keeps no session in the instance. An instance can stop
  between calls, so the endpoint works with a minimum of zero instances.
- **The MCP tools come from the OpenAPI operations.** An app marks an
  operation for MCP with an extension (`x-mcp: true`) in its huma operation.
  The shared `httpapi` module reads the generated contract, and makes one
  tool for each marked operation:
  - The tool name is the `operationId`.
  - The tool description is the operation description.
  - The tool input schema is the operation input schema (the path, query,
    and body parameters as one object).
  - The tool output schema is the response schema.
  - A tool call runs the operation in the same process, through the huma
    handler. There is no HTTP round trip and no second code path.
- What this saves: no tool code, no second schema, and no second validation
  in each app. A new API operation becomes a tool when the app marks it.
- What still needs hand work in each app:
  - Write the operation description for an AI reader: what the operation is
    for, what each parameter means, and an example. The docs page and the
    tool use the same text.
  - Choose which operations to mark. Mark only read-only operations that are
    useful to an AI client.
- Public read-only tools need no authentication. Tools that change data need
  authentication. Write a separate decision for those.
- Do not make one MCP gateway for all projects. A gateway is one more service
  to deploy, and it must change when a project changes.

### Shared code

- Put the shared HTTP code in a separate Go module in this repository:
  `github.com/dmikalova/project-standards/httpapi`, with its own `go.mod`.
  It holds:
  - The huma setup for a ServeMux, with the house defaults (OpenAPI 3.1,
    Scalar, problem details).
  - The rate limit middleware and the CORS middleware.
  - The `/llms.txt` handler, made from the contract.
  - The MCP adapter (the `/mcp` handler and the tools from the marked
    operations).
- The separate module keeps the CI dependencies of the root module out of
  the apps.

### Conformance

- Add `api: true` to `mklv.config.json` for a project with API endpoints.
- The check of such a project fails if the project does not import
  `httpapi`, or if the generated contract is not valid OpenAPI 3.1. The check
  gets the contract from a test that `httpapi` gives to each project.

## Consequences

- gallery.keyforge.cards changes its error shape from `{"error": "..."}` to
  problem details. Its JSON export is the first huma operation.
- keyfordle moves its `/api/decks/public/...` endpoints to huma operations.
  It keeps the paths, because the paths copy the Master Vault paths that its
  client expects.
- An AI client that supports remote MCP servers can use each app at
  `https://<app>/mcp`. A client without MCP reads `/llms.txt` and
  `/openapi.json`.
- The cost does not change. The MCP endpoint and the docs run in the existing
  Cloud Run service.
