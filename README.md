# Money Manager Server

Production-oriented Go API shared by the Money Manager Android and iOS applications.

## Capabilities

- HS256 JWT authentication with issuer, audience, issued-at, and expiration validation
- Transaction and category CRUD scoped to the authenticated user
- Strict EUR amount, category, date, and request validation
- Monthly summaries and date-range CSV export
- Account inspection and deletion through `/me`
- PostgreSQL-backed readiness, process liveness, and graceful shutdown
- Versioned migrations serialized by a PostgreSQL advisory lock
- Preserved quarantine records for legacy rows that cannot satisfy hardened constraints
- Structured access logs, request IDs, typed public errors, and auth rate limiting
- Stateless Linux ARM64 deployment across two Raspberry Pis

## Local development

Docker Compose supplies development-only database credentials and a non-production JWT secret:

```bash
docker compose up --build --wait
curl --fail http://localhost:8080/readyz
```

Stop the stack and remove local data:

```bash
docker compose down --volumes
```

To run the Go process directly, copy `.env.example`, export its values in your shell, start PostgreSQL, then run:

```bash
go run ./cmd/server
```

`DATABASE_URL` and `JWT_SECRET` are always required. The server refuses to start with a missing database URL or a JWT secret shorter than 32 bytes.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port. |
| `DATABASE_URL` | required | PostgreSQL URL. Production should use a private endpoint and TLS where available. |
| `JWT_SECRET` | required | JWT signing key, at least 32 bytes. |
| `JWT_ISSUER` | `money-manager-api` | Required token issuer. |
| `JWT_AUDIENCE` | `money-manager-mobile` | Required token audience. |
| `JWT_TTL` | `24h` | Access-token lifetime. |
| `JWT_LEGACY_ACCEPT_UNTIL` | unset | Optional absolute RFC3339 cutoff for correctly signed pre-hardening tokens without issuer/audience. The cutoff cannot be more than one `JWT_TTL` from process startup. |
| `DB_MAX_CONNS` | `10` | Maximum pgx pool connections per replica. |
| `DB_MIN_CONNS` | `2` | Minimum pgx pool connections per replica. |
| `DB_MAX_CONN_LIFETIME` | `30m` | Maximum connection lifetime. |
| `DB_MAX_CONN_IDLE_TIME` | `5m` | Maximum idle connection time. |
| `DB_HEALTH_CHECK_PERIOD` | `1m` | pgx pool health-check interval. |
| `STARTUP_TIMEOUT` | `30s` | Database connection timeout. |
| `MIGRATION_TIMEOUT` | `3m` | Separate deadline for advisory-lock acquisition and versioned migrations. |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful HTTP shutdown deadline. |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Request-header timeout. |
| `HTTP_READ_TIMEOUT` | `15s` | Full request-read timeout. |
| `HTTP_WRITE_TIMEOUT` | `30s` | Response-write timeout. |
| `HTTP_IDLE_TIMEOUT` | `1m` | Keep-alive idle timeout. |
| `REQUEST_BODY_LIMIT_BYTES` | `65536` | Maximum JSON body size. |
| `AUTH_RATE_LIMIT` | `10` | Auth requests allowed per client and route per window. |
| `AUTH_RATE_WINDOW` | `1m` | Auth rate-limit window. |
| `TRUSTED_PROXY_CIDRS` | unset | Comma-separated proxy network CIDRs allowed to supply forwarded client addresses. Forwarded headers are ignored by default. |
| `TRUSTED_PROXY_HOPS` | `0` | Exact number of trusted proxy hops, including the direct peer. Must be positive and configured together with proxy CIDRs. |
| `ENABLE_BANKING_APPLICATION_ID` | unset | Active Enable Banking sandbox or production application ID. Open banking stays disabled when unset. |
| `ENABLE_BANKING_PRIVATE_KEY` | unset | RSA private key PEM supplied directly, intended for production secret injection. Configure this or the path, never both. |
| `ENABLE_BANKING_PRIVATE_KEY_BASE64` | unset | Base64-encoded RSA private key PEM. This is the preferred one-line deployer secret. Configure only one private-key source. |
| `ENABLE_BANKING_PRIVATE_KEY_PATH` | unset | Path to the RSA private key PEM, convenient for direct local execution. |
| `ENABLE_BANKING_CALLBACK_URL` | required with credentials | Exact HTTPS callback URL registered for the Enable Banking application. HTTP is allowed only on localhost. |
| `ENABLE_BANKING_RESULT_REDIRECT_URL` | unset | Optional app deep link or web URL opened after the server completes the callback. Callback status is appended as query parameters. |
| `ENABLE_BANKING_CONSENT_DAYS` | `90` | Default requested consent lifetime. The server clamps it to the institution's reported maximum. |
| `ENABLE_BANKING_STATE_TTL` | `15m` | Lifetime of one-time authorization state. Allowed range is 1 minute to 1 hour. |
| `ENABLE_BANKING_REQUEST_TIMEOUT` | `20s` | Timeout for Enable Banking API calls. Maximum is 1 minute. |

The sandbox and production applications use the same Enable Banking API origin. Select the environment by supplying the application ID and private key that belong to that environment. Never embed either private key in an iOS or Android build.

For direct local execution, use the downloaded sandbox key file:

```bash
export ENABLE_BANKING_APPLICATION_ID="your-sandbox-application-id"
export ENABLE_BANKING_PRIVATE_KEY_PATH="/absolute/path/to/sandbox-private-key.pem"
export ENABLE_BANKING_CALLBACK_URL="http://localhost:8080/api/open-banking/callback"
go run ./cmd/server
```

Docker Compose accepts either the PEM through `ENABLE_BANKING_PRIVATE_KEY` or its one-line base64 encoding through `ENABLE_BANKING_PRIVATE_KEY_BASE64`. Export one of them before running `docker compose up`; the private key is not checked into the repository.

### Legacy JWT transition

The hardened token contract normally rejects tokens without issuer and audience claims. For one rollout only, set `JWT_LEGACY_ACCEPT_UNTIL` to a fixed timestamp no later than one access-token TTL after cutover. During that window the server accepts only HS256 tokens signed by the current secret that:

- omit both issuer and audience;
- contain a valid positive subject and required expiration;
- expire no later than the configured cutoff.

The cutoff is absolute and does not move when the process restarts. Remove the setting after it passes. Tokens with an incorrect issuer or audience never fall back to legacy validation.

### Trusted proxy addresses

Forwarded client-IP headers are ignored unless both the direct peer and every declared intermediate hop match `TRUSTED_PROXY_CIDRS`, and the chain has exactly enough entries for `TRUSTED_PROXY_HOPS`. Authentication limits are keyed by a privacy-preserving hash of the normalized account identifier plus the verified network address. This prevents an ingress address shared by all clients from turning one abusive account into a global lockout, while still preventing spoofed `X-Forwarded-For` values from bypassing a per-account limit.

## HTTP contract

Health:

- `GET /livez`: process liveness, no dependency checks
- `GET /readyz`: PostgreSQL-backed readiness
- `GET /health`: readiness-compatible alias retained for mobile and existing tooling

Authentication and account:

- `POST /auth/register`
- `POST /auth/login`
- `GET /me`
- `DELETE /me`

Categories:

- `GET /categories?type=expense`
- `POST /categories`
- `DELETE /categories/{id}`

Transactions:

- `GET /transactions?month=2026-07&type=expense&category=food`
- `POST /transactions`
- `PUT /transactions/{id}`
- `DELETE /transactions/{id}`
- `GET /transactions/summary?month=2026-07`
- `GET /transactions/export?from=2026-07-01&to=2026-07-31`
- `POST /transactions/import/revolut` with a `text/csv` Revolut account statement body

Open banking:

- `GET /api/open-banking/banks?country=BG&psu_type=personal`
- `POST /api/open-banking/authorizations`
- `GET /api/open-banking/callback?state=...&code=...`, public callback registered with Enable Banking
- `GET /api/open-banking/connections`
- `GET /api/open-banking/connections/{id}`
- `DELETE /api/open-banking/connections/{id}`
- `GET /api/open-banking/accounts`
- `GET /api/open-banking/accounts/{id}/details`
- `GET /api/open-banking/accounts/{id}/balances`
- `GET /api/open-banking/accounts/{id}/transactions?date_from=2026-07-01&date_to=2026-07-31&transaction_status=BOOK&strategy=default`

All open-banking endpoints except the callback require a Money Manager access token. Institution responses are reduced to UI-safe metadata, so Enable Banking sandbox usernames and passwords are never returned. Provider session and account IDs stay server-side behind local user-owned IDs.

Start a Revolut consent flow after selecting the exact institution name returned by the banks endpoint:

```json
{
  "institution_name": "Revolut",
  "country": "BG",
  "psu_type": "personal",
  "consent_days": 90,
  "language": "en"
}
```

The response contains an `authorization_url` that the mobile app opens in the system browser. Enable Banking returns to the configured callback. The server validates and consumes the state once, exchanges the code for a session, saves the authorized accounts, and then renders a completion page or redirects to `ENABLE_BANKING_RESULT_REDIRECT_URL`.

Linking a Revolut account manually in the Enable Banking control panel only activates or whitelists that account for restricted production use. It does not create an API session. Each Money Manager user must still complete the authorization flow above. A restricted production application returns data only for accounts already linked in the control panel; unrestricted access requires Enable Banking production activation.

CSV exports are limited to an inclusive 366-day range and 5,000 transactions. Requests over either limit return HTTP 400 and must be narrowed. This keeps the pre-encoded CSV response below a predictable memory bound.

Revolut imports accept up to 2 MiB and 5,000 rows. Completed EUR rows are imported into the `other` expense or income category according to the amount sign. Pending, reverted, zero-value, and non-EUR rows are ignored. A stable source fingerprint makes overlapping or repeated statement imports idempotent.

Protected endpoints require:

```text
Authorization: Bearer <token>
```

JSON bodies are bounded and strict. Unknown fields, trailing JSON values, malformed content, and non-JSON content types return HTTP 400. Errors use:

```json
{
  "error": "safe public message",
  "request_id": "request correlation ID"
}
```

The API maps validation, authentication, missing resources, conflicts, rate limits, provider availability, and internal failures to `400`, `401`, `404`, `409`, `429`, `503`, and `500`. Internal database and provider details are logged server-side but never returned to clients.

## Financial validation

- Types are exactly `expense` or `income` after normalization.
- Currency is EUR. Missing currency is normalized to EUR for client compatibility.
- Amounts must be positive, have at most two decimal places, and not exceed `999999999999.99`.
- Dates use `YYYY-MM-DD`; month filters use `YYYY-MM`.
- A transaction category must be active, owned by the user, and match the transaction type.
- Category names are limited to 40 Unicode characters.
- Descriptions are limited to 500 Unicode characters.
- Passwords are 8 to 72 bytes because bcrypt has a 72-byte input limit.

## Migrations

SQL migrations live in `internal/repository/migrations` and are embedded into the server binary. Startup:

1. Connects to PostgreSQL with a bounded context.
2. Acquires a database advisory lock.
3. Uses the separate `MIGRATION_TIMEOUT`, allowing schema work more time than ordinary startup connection checks.
4. Normalizes compatible legacy values and copies incompatible users, categories, and transactions into `migration_quarantine` before removing them from active tables.
5. Applies each unapplied migration in its own transaction.
6. Records the version in `schema_migrations`.
7. Releases the advisory lock before serving traffic.

This allows two replicas to start concurrently without racing schema changes. Compatible casing and whitespace are normalized. Values that cannot be converted safely, including non-EUR money, are preserved as original JSON with an explicit reason instead of being silently converted or blocking deployment.

Inspect quarantined records after an upgrade:

```sql
SELECT migration_version, source_table, source_id, reason, row_data, quarantined_at
FROM migration_quarantine
ORDER BY quarantined_at, source_table, source_id;
```

Remediation is intentionally manual because merging duplicate accounts and converting currencies require product decisions. Export and back up quarantine records before restoring corrected rows, and retain them until the restore has been verified.

## Verification

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Set `TEST_DATABASE_URL` to a disposable PostgreSQL database to enable the isolated-schema repository integration test.

Build ARM64:

```bash
docker buildx build --platform linux/arm64 -t money-manager-server:test .
```

## Production image and deployment

Build and push a versioned ARM64 image, then inspect its manifest:

```bash
VERSION=v0.1.0
docker buildx build \
  --platform linux/arm64 \
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --tag "ghcr.io/0xivanov/money-manager-server:$VERSION" \
  --push .
docker buildx imagetools inspect "ghcr.io/0xivanov/money-manager-server:$VERSION"
```

Before production, replace the version tag in `deployer.yml` with the published image digest when possible. The GHCR package must be public because the current deployer does not inject private-registry pull credentials.

The checked-in `deployer.yml` targets:

- App: `money-manager-api`
- Route: `https://money.0xivanov.dev`
- Two resilient Linux ARM64 replicas
- One pod per Raspberry Pi
- PostgreSQL-backed `/readyz`
- Encrypted deployer secrets for the database, JWT signing, and Enable Banking production credentials

For a new app, submit once to create the deployer app record, set secrets interactively, then submit again:

```bash
deployer deploy --file deployer.yml --dry-run
deployer deploy --file deployer.yml
deployer secrets set money-manager-api DATABASE_URL
deployer secrets set money-manager-api JWT_SECRET
deployer secrets set money-manager-api ENABLE_BANKING_APPLICATION_ID
deployer secrets set money-manager-api ENABLE_BANKING_PRIVATE_KEY_BASE64
deployer secrets set money-manager-api ENABLE_BANKING_CALLBACK_URL
deployer deploy --file deployer.yml
deployer status money-manager-api
deployer logs --tail 50 money-manager-api
deployer routes list
```

For `ENABLE_BANKING_PRIVATE_KEY_BASE64`, encode the production PEM as one line with `base64 < prod.pem | tr -d '\n'`, then paste that value into the deployer's hidden prompt. Set `ENABLE_BANKING_CALLBACK_URL` to `https://money.0xivanov.dev/api/open-banking/callback` and register that exact URL in the production Enable Banking application before rollout.

The PostgreSQL service is intentionally external to the stateless API deployment. It must be reachable from both worker nodes, restricted to the private network, backed up off-host, and restore-tested before release.
