# Money Manager Server Initial Design Spec

Date: 16 May 2026
Project: `money-manager-server`
Audience: AI agents and engineers extending, debugging, or integrating the backend.

## Purpose

`money-manager-server` is a Go HTTP API for the Money Manager apps. It currently supports JWT auth, PostgreSQL-backed users, transactions, categories, monthly summaries, and CSV export. The README calls out Android compatibility, but the backend also has fields and endpoints expected by the iOS client that exists in the wider workspace.

This document describes the server as it exists in the current working tree on 16 May 2026. Several server files are modified relative to Git HEAD; this spec intentionally documents the current file contents, including those uncommitted changes.

## High-Level Shape

The server is deliberately small and layered:

- Entrypoint: `cmd/server/main.go`
- Runtime startup: `internal/app/app.go`
- Environment config: `internal/config/config.go`
- HTTP routes and response writing: `internal/router/router.go`
- Business rules: `internal/service/service.go`
- PostgreSQL access and migrations: `internal/repository/repository.go`
- JSON DTOs/domain structs: `internal/model/model.go`
- Container/dev setup: `Dockerfile`, `docker-compose.yml`, `.env.example`

There is no external web framework. Routing uses Go 1.22 `http.ServeMux` method/path patterns such as `GET /health` and `PUT /transactions/{id}`.

## Runtime Configuration

Configuration is loaded from environment variables in `internal/config/config.go`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PORT` | `8080` | TCP port for `http.ListenAndServe`. |
| `DATABASE_URL` | `postgres://money:money@localhost:5432/money_manager?sslmode=disable` | PostgreSQL connection string used by pgxpool. |
| `JWT_SECRET` | `dev-secret` | HMAC signing secret for JWTs. Must be changed in production. |

Docker Compose runs:

- `db`: `postgres:16-alpine`, database `money_manager`, user/password `money`/`money`, host port `5432`.
- `api`: built from local Dockerfile, exposes `8080`, points `DATABASE_URL` at the Compose DB service, and sets `JWT_SECRET=change-this-secret-in-production`.

Local quick start from README:

```bash
cp .env.example .env
docker compose up --build
curl http://localhost:8080/health
```

Expected health response is plain text `ok`.

## Dependencies

`go.mod` declares Go 1.22 and direct dependencies:

- `github.com/golang-jwt/jwt/v5` for JWT creation/parsing.
- `github.com/jackc/pgx/v5` for PostgreSQL and pgxpool.
- `golang.org/x/crypto` for bcrypt password hashing.

There are currently no tests in the server repo.

## Application Startup

`app.Run()` does the entire boot sequence:

1. Load config.
2. Create service with `service.New(context.Background(), cfg)`.
3. `service.New` opens PostgreSQL via `repository.Open`.
4. `service.New` runs `repository.Migrate`.
5. Build HTTP handler via `router.Build(svc)`.
6. Start `http.ListenAndServe(":"+cfg.Port, h)`.

If DB connection or migration fails, startup calls `log.Fatal`.

## Database Schema

Migrations are embedded SQL in `repository.Migrate`. They are idempotent enough for local/dev use but are not a formal migration system.

### `users`

```sql
CREATE TABLE IF NOT EXISTS users(
  id SERIAL PRIMARY KEY,
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL
);
```

Notes:

- Email uniqueness is enforced by the database.
- Passwords are stored as bcrypt hashes.
- Email is trimmed by service methods before register/login.
- There is no email format validation.

### `transactions`

```sql
CREATE TABLE IF NOT EXISTS transactions(
  id SERIAL PRIMARY KEY,
  user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  amount NUMERIC(14,2) NOT NULL,
  currency TEXT NOT NULL,
  occurred_at DATE NOT NULL
);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
```

Notes:

- `type` is intended to be `expense` or `income`, but transaction create/update routes currently call repository methods directly and do not validate type.
- `amount` is numeric with 2 decimal places.
- `amount` is serialized out as a string with exactly two decimal places using PostgreSQL `to_char(..., 'FM999999999990.00')`.
- `occurred_at` is a date only. Returned format is `YYYY-MM-DD`.
- `description` is present in the current backend model/API. Clients that do not send it get the Go zero value `""`.

### `categories`

```sql
CREATE TABLE IF NOT EXISTS categories(
  id SERIAL PRIMARY KEY,
  user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT false,
  active BOOLEAN NOT NULL DEFAULT true,
  sort_order INT NOT NULL DEFAULT 1000
);
ALTER TABLE categories ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 1000;
CREATE UNIQUE INDEX IF NOT EXISTS categories_user_type_name_active_idx
  ON categories(user_id,type,lower(name))
  WHERE active;
```

Notes:

- Categories are user-scoped.
- Default categories are inserted per user during register, login, and category listing.
- Custom categories are soft-deleted by setting `active=false`.
- Default categories cannot be deleted through `DeleteCategory` because the SQL requires `is_default=false`.
- Active category names are unique per user, type, and case-insensitive name.

Default expense categories, in insertion/sort order:

`food`, `transport`, `housing`, `utilities`, `health`, `entertainment`, `shopping`, `travel`, `education`, `other`

Default income categories:

`salary`, `freelance`, `gift`, `investment`, `refund`, `other`

## Data Models and JSON Contract

Models live in `internal/model/model.go`.

### Auth

Request:

```json
{
  "email": "user@example.com",
  "password": "secret"
}
```

Response:

```json
{
  "token": "jwt",
  "user": {
    "id": 1,
    "email": "user@example.com"
  }
}
```

### Transaction

Response object:

```json
{
  "id": 1,
  "type": "expense",
  "category": "food",
  "description": "",
  "amount": "12.50",
  "currency": "EUR",
  "occurred_at": "2026-05-16"
}
```

Create/update request:

```json
{
  "type": "expense",
  "category": "food",
  "description": "Lunch",
  "amount": "12.50",
  "currency": "EUR",
  "occurred_at": "2026-05-16"
}
```

Important client compatibility note: some clients may omit `description`. That is acceptable because Go decodes it as `""` and DB schema has `NOT NULL DEFAULT ''`. Some clients may also omit `currency`; router defaults missing currency to `EUR` before create/update.

### Category

Response object:

```json
{
  "id": 10,
  "type": "expense",
  "name": "food",
  "is_default": true
}
```

Create request:

```json
{
  "type": "expense",
  "name": "groceries"
}
```

### Summary

Response object:

```json
{
  "month": "2026-05",
  "income": "1000.00",
  "expense": "200.00",
  "balance": "800.00",
  "currency": "EUR",
  "transaction_count": 4
}
```

`balance` is calculated in Go as income minus expense using `math/big.Rat`, then rendered with two decimals.

## Authentication and Authorization

Auth uses JWT Bearer tokens.

- Tokens are signed with HS256.
- Claims include:
  - `sub`: user ID as a string.
  - `email`: email address.
  - `exp`: current time plus 24 hours.
- Protected routes require `Authorization: Bearer <token>`.
- Missing/invalid bearer tokens result in status `401` with no JSON body.
- `ParseUserID` parses the token and extracts `sub`.

Security caveat for future agents: `jwt.Parse` is called with a key function but does not explicitly check the signing method. Consider adding an HS256 method check before production hardening.

## Error Handling

`router.write` centralizes JSON responses for most endpoints:

- On success, it sets `Content-Type: application/json`, writes the supplied status code, and JSON-encodes non-nil values.
- On error, it always writes HTTP `400` and response body `{"error":"..."}`.

Exceptions:

- Auth failures in `authUser` write bare `401`.
- Delete success writes bare `204`.
- CSV export sets `Content-Type: text/csv`; if CSV writing fails after headers/body may have begun, it attempts to write JSON error but the response may already be partially committed.

Current error status granularity is coarse. Most service/repository failures become `400`, including DB issues.

## API Endpoints

### Health

`GET /health`

Returns plain text:

```text
ok
```

### Register

`POST /auth/register`

Body: `AuthRequest`.

Behavior:

1. Trim email.
2. Require non-empty email and password.
3. Hash password with bcrypt default cost.
4. Insert user.
5. Ensure default categories for the new user.
6. Return JWT and user.

Success status: `201`.

Known errors:

- `email and password are required`
- `email is already registered`

### Login

`POST /auth/login`

Body: `AuthRequest`.

Behavior:

1. Trim email.
2. Require non-empty email and password.
3. Find user by email.
4. Compare bcrypt password.
5. Ensure default categories exist for the user.
6. Return JWT and user.

Success status: `200`.

Known errors:

- `email and password are required`
- `invalid credentials`

### List Categories

`GET /categories?type=expense`

Protected: yes.

Query:

- `type`: required in practice; must be `expense` or `income`.

Behavior:

1. Validate transaction type.
2. Ensure default categories.
3. Return active categories for the user and type, ordered by `sort_order ASC, name ASC`.

Success status: `200`.

### Create Category

`POST /categories`

Protected: yes.

Body: `CategoryRequest`.

Validation:

- `type` trimmed and must be `expense` or `income`.
- `name` trimmed and required.
- `name` must be 40 characters or less.

Behavior:

- Inserts as `is_default=false`, `active=true`.
- Unique conflict returns `category already exists`.

Success status: `201`.

### Delete Category

`DELETE /categories/{id}`

Protected: yes.

Behavior:

- Soft-deletes an active custom category belonging to the user.
- Does not delete default categories.
- Does not update existing transactions that reference the category name.

Success status: `204`.

Known error:

- `custom category not found`

### List Transactions

`GET /transactions?month=2026-05&type=expense&category=food`

Protected: yes.

Query:

- `month`: expected `YYYY-MM`. No explicit validation before SQL filtering.
- `type`: optional. If present, exact-match filter.
- `category`: optional. If present, exact-match filter.

Behavior:

- Queries transactions for the authenticated user where `to_char(occurred_at,'YYYY-MM') = month`.
- Applies optional type/category filters.
- Sorts by `occurred_at DESC, id DESC`.

Success status: `200`.

### Create Transaction

`POST /transactions`

Protected: yes.

Body: `TransactionRequest`.

Behavior:

- If `currency` is empty, router sets it to `EUR`.
- Inserts transaction for authenticated user.
- Uppercases currency before storing.
- Returns created transaction.

Success status: currently `200`, not `201`.

Validation caveat:

- There is no service-level validation for transaction type, category existence, positive amount, amount format, currency format, or date format. Invalid values may be rejected by PostgreSQL or accepted depending on the field.

### Update Transaction

`PUT /transactions/{id}`

Protected: yes.

Body: `TransactionRequest`.

Behavior:

- If `currency` is empty, router sets it to `EUR`.
- Updates only a transaction owned by the authenticated user.
- Uppercases currency before storing.
- Returns updated transaction.

Success status: `200`.

Missing-row caveat:

- If no row matches `id` and `user_id`, `QueryRow.Scan` returns `pgx.ErrNoRows`, which currently becomes HTTP `400` with the raw error text.

### Delete Transaction

`DELETE /transactions/{id}`

Protected: yes.

Behavior:

- Deletes transaction by `id` and authenticated `user_id`.
- Does not currently check affected row count.

Success status: `204`.

### Transaction Summary

`GET /transactions/summary?month=2026-05`

Protected: yes.

Query:

- `month`: expected `YYYY-MM`.

Behavior:

- Sums income and expense separately for the authenticated user and month.
- Counts all transactions for that month.
- Sets `currency` to hardcoded `EUR`.
- Calculates `balance = income - expense`.

Success status: `200`.

Multi-currency caveat:

- Summary ignores per-transaction currencies and always reports EUR. If true multi-currency support is added, this endpoint needs redesign.

### Export Transactions CSV

`GET /transactions/export?from=2026-05-01&to=2026-05-16`

Protected: yes.

Query:

- `from`: required `YYYY-MM-DD`.
- `to`: required `YYYY-MM-DD`.

Validation:

- Both dates must parse using Go layout `2006-01-02`.
- `from` must be before or equal to `to`.

Behavior:

- Queries user transactions where `occurred_at >= from::date AND occurred_at <= to::date`.
- Sorts by `occurred_at ASC, id ASC`.
- Returns CSV, not JSON.
- Headers:
  - `Content-Type: text/csv`
  - `Content-Disposition: attachment; filename="money-manager-<from>-to-<to>.csv"`

CSV columns:

```csv
occurred_at,type,category,description,amount,currency
```

## Repository Query Details

Repository methods use positional SQL parameters for dynamic filters. `ListTransactions` constructs SQL by appending conditions with generated `$N` placeholders, not by interpolating untrusted values directly. This is good; keep that pattern if adding filters.

Formatting conventions:

- Monetary output is formatted in SQL using `to_char(amount,'FM999999999990.00')`.
- Dates are formatted in SQL using `to_char(occurred_at,'YYYY-MM-DD')`.
- Month filtering uses `to_char(occurred_at,'YYYY-MM')=$2`; this is simple but not index-friendly if the dataset grows.

## Client Expectations

The known mobile clients expect:

- Base URL locally:
  - Android emulator: `http://10.0.2.2:8080`
  - iOS simulator/local development: often `http://localhost:8080`
- Bearer token auth after login/register.
- Amounts as strings, not numbers.
- Dates as `YYYY-MM-DD`.
- Month keys as `YYYY-MM`.
- Transaction type values exactly `expense` and `income`.
- Default currency behavior of `EUR` when omitted.
- Category names as stable string identifiers in transactions.

The iOS app currently known in the wider workspace defines transaction models without `description`; Swift `Codable` decoding can fail if the server returns unexpected required fields only when the client struct lacks matching fields? In Swift, extra JSON keys are ignored, so returning `description` is compatible. Sending requests without `description` is also compatible with this backend because the request struct defaults to an empty Go string.

## Current Design Tradeoffs and Risks

- No tests exist. Add at least repository/service/router tests before larger behavioral changes.
- Embedded SQL migrations are convenient but limited. For production, move to versioned migrations.
- Error handling always maps most errors to HTTP `400`; future agents may want typed errors and more precise status codes.
- Transaction create/update bypass the service layer and therefore skip validation. This is the largest correctness gap.
- JWT parser should verify expected signing method.
- `JWT_SECRET` has a safe Compose placeholder but config has a weak default `dev-secret`.
- Summary is EUR-only despite transaction currency being stored.
- Category deletion soft-deletes the category but leaves old transaction category strings as-is. This is acceptable if transaction categories are historical labels, but not if category records become normalized foreign keys.
- `DELETE /transactions/{id}` succeeds even when no row existed.
- JSON decode errors are ignored in router handlers. Malformed JSON may become empty request structs and then fail validation inconsistently.
- `month` query values are not validated.
- Database pool uses pgx defaults; no custom pool sizing or health checks beyond Compose DB health.

## Suggested Extension Priorities

For future AI agents, good next steps are:

1. Add router/service tests around auth, categories, transaction CRUD, summary, and CSV export.
2. Move transaction create/update/delete behind service methods with validation and consistent not-found behavior.
3. Add request JSON decode error handling.
4. Introduce typed service errors and map them to `400`, `401`, `404`, `409`, and `500` appropriately.
5. Add versioned DB migrations if schema changes continue.
6. Decide whether categories remain denormalized strings on transactions or become foreign-keyed records.
7. Revisit multi-currency design before adding non-EUR UI flows.

## Useful Manual Smoke Commands

Run services:

```bash
docker compose up --build
```

Health:

```bash
curl http://localhost:8080/health
```

Register:

```bash
curl -s http://localhost:8080/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"agent@example.com","password":"password"}'
```

List expense categories, replacing `$TOKEN`:

```bash
curl -s 'http://localhost:8080/categories?type=expense' \
  -H "Authorization: Bearer $TOKEN"
```

Create transaction:

```bash
curl -s http://localhost:8080/transactions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"type":"expense","category":"food","description":"Lunch","amount":"12.50","currency":"EUR","occurred_at":"2026-05-16"}'
```

Summary:

```bash
curl -s 'http://localhost:8080/transactions/summary?month=2026-05' \
  -H "Authorization: Bearer $TOKEN"
```

CSV export:

```bash
curl -s 'http://localhost:8080/transactions/export?from=2026-05-01&to=2026-05-16' \
  -H "Authorization: Bearer $TOKEN"
```
