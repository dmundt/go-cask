# cas-api example

A standalone CAS HTTP API server (`/api/cas/v1`) implementing the contract in
`.github/instructions/cas-api.instructions.md` (R-01…R-14): content-addressed
store with dedup, streaming uploads/downloads, bearer-token role auth
(viewer/operator/admin), IP-based rate limiting, metadata, list, verify, GC,
stats, and a self-served OpenAPI document.

The companion **public client SDK** is the root `client/` package
(`github.com/dmundt/go-cask/client`) — this example demonstrates it; it does
not contain it.

## Run

```text
# terminal 1 — server (tokens: viewer=viewer, operator=operator, admin=admin)
go run ./examples/cas-api/server -store ./objects -bind 127.0.0.1:8080

# terminal 2 — demo round-trip via the SDK
go run ./examples/cas-api/demo -api http://127.0.0.1:8080 -token operator -file ./README.md

# explore the API
curl -H "Authorization: Bearer operator" http://127.0.0.1:8080/api/cas/v1/stats
curl -H "Authorization: Bearer viewer" http://127.0.0.1:8080/api/cas/v1/openapi.yaml
```

## Layout

```text
server/     the CAS API server (main + routes + rate limiter + tests)
demo/       a demo CLI that round-trips a file through the server via client/
client/     (repo root) the public SDK the demo uses
```

## Acceptance criteria

- `client.Put` → `client.Get` returns identical bytes ✓ (server + client tests)
- a viewer-role token gets 403 on `DELETE` ✓ (role-matrix test)
- large payloads stream without buffering ✓ (4 MiB round-trip test)
- `GET /api/cas/v1/openapi.yaml` is served and matches the routes ✓
