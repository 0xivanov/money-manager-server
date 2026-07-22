# Investment integration assessment

Reviewed on 2026-07-19. Public Kraken market data is used for BTC and ETH, Trading 212 read-only account data supplies stock holdings and current values, and Marketstack supplies daily stock and ETF history when configured.

## Current product behavior

Money Manager records the EUR value and exact time of a BTC or ETH buy or sell. Future execution times are rejected. The backend looks up a nearby BTC/EUR or ETH/EUR Kraken trade, stores the reference price and quote time, and calculates quantity. Portfolio quantity, cost basis, realized profit, and unrealized profit are derived from that trade ledger. Current valuation uses Kraken's public order-book midpoint, and the portfolio chart combines daily close data with the user's buys and sells. Completed chart days use `23:59:59Z`; the final point uses the exact current quote time. No API key is required.

The Kraken quote is a market reference and can differ from the user's actual Revolut X or other broker fill because of spread, fees, and execution timing. The audit export retains the entered EUR amount, derived quantity, reference unit price, provider, provider timestamp, fees, and execution timestamp.

Stock and ETF trades use the account's own Trading 212 fills and open positions when `TRADING212_API_KEY` and `TRADING212_API_SECRET` are configured. Listings retain their native market currency, while Trading 212 wallet-impact values provide the EUR account-currency values used by the portfolio. Marketstack end-of-day prices supply historical valuation when `MARKETSTACK_API_KEY` is configured. USD closes are converted to EUR using Frankfurter's ECB-backed daily rates. If Trading 212 is not configured or a position cannot be matched, the stock ledger remains visible with a missing-price status and does not invalidate crypto valuation.

Trading 212 credentials are account-specific rather than application-wide. `TRADING212_OWNER_USER_ID` is therefore required with the key pair, and the service checks the authenticated user ID before every Trading 212 quote or fill lookup. Other users can use shared public Marketstack history, but cannot invoke the configured Trading 212 account.

## Trading 212 read-only account data

The API key and secret are sent only from the backend using HTTP Basic authentication and are never exposed to mobile clients. Request errors are scrubbed so neither credential can reach logs or client responses. The integration makes GET requests only to `/equity/positions`, `/equity/metadata/instruments`, and `/equity/history/orders`. It has no order-placement code. Create the Trading 212 key with portfolio, account data, and history read permissions only, then restrict it to the backend nodes' public egress IP addresses.

Open positions are cached for five minutes and instrument metadata for 24 hours. Historical trade creation uses the closest account fill within seven days. Trading 212 does not provide general daily price history, so Marketstack supplies chart history for supported NASDAQ and Xetra listings. Completed stock closes are persisted in PostgreSQL and refreshed in small overlapping windows instead of being fetched for every chart request. The portfolio history response includes both aggregate totals and a per-holding value breakdown for the mobile chart.

## Marketstack stock history

The backend calls only Marketstack's GET `/v2/eod` endpoint. The API key is sent from the backend and is scrubbed from provider errors. Current stock quantities, fills, and live account values continue to come from Trading 212. Marketstack is used only for historical closes.

The current listing map uses the native portfolio identity:

- `MSTR` on NASDAQ maps to `MSTR` and is converted from USD to EUR.
- `QDVE`, `VGWE`, and `4GLD` on Xetra map to their `.DE` Marketstack symbols and remain in EUR.

The backend persists converted daily prices in `investment_market_history`. A chart request reads the shared database cache first. It backfills a missing range or refreshes a stale tail with a seven-day overlap, then stores the new closes. If Marketstack is temporarily unavailable and cached history exists, the cached series remains usable.

`PUT /investments/prices` is deprecated and retained only for legacy stock records so future stock-provider work does not require a schema rollback. Crypto requests are rejected because BTC and ETH valuation always uses automatic Kraken prices and the manual price table is not consulted for crypto.

## Kraken public market data

Official documentation:

- <https://docs.kraken.com/api/docs/rest-api/get-post-trade>
- <https://docs.kraken.com/api/docs/rest-api/get-pre-trade>
- <https://docs.kraken.com/api/docs/rest-api/get-ohlc-data>
- <https://support.kraken.com/articles/206548367-what-are-the-api-rate-limits->

The integration is read-only and calls only public endpoints:

- post-trade data for the reference price nearest a recorded execution time;
- pre-trade order-book data for a current midpoint;
- daily OHLC data for the one-year portfolio chart.

Historical lookups expand through bounded time windows when no trade exists at the exact second. A missing historical quote fails the write instead of silently substituting a current price. Provider responses are size-bounded and validated before any price is persisted. Each backend replica spaces Kraken request admissions by at least two seconds. With the current two-replica deployment, that bounds the long-run aggregate admission rate to one request per second. This is not a distributed lock, so two replicas can still admit requests at the same instant. A bounded, cancellation-aware queue prevents local overload, five-second current-quote caching coalesces dashboard requests, and completed daily history is cached for up to 15 minutes without crossing a UTC day boundary.

## Revolut X

Official documentation: <https://developer.revolut.com/docs/api/revolut-x-crypto-exchange>

The official Revolut X API supports mapped retail accounts, Ed25519 request signing, balances, instrument configuration, public market data, historical orders, private fills, candles, and order placement. The useful read-only scope for Money Manager is:

- balances for reconciliation;
- private fills for an authoritative buy and sell ledger;
- instrument metadata and public prices for valuation.

Order creation and cancellation must remain disabled. A compromised trading credential could otherwise cause direct financial loss. The private signing key must be encrypted and stored only on the server. It must never be embedded in either mobile application.

## Trading 212

Official documentation: <https://docs.trading212.com/api/historical-events/requestreport>

Trading 212 exposes a beta Public API v0 for Invest and Stocks ISA accounts. It uses an API key and secret over HTTP Basic authentication, has separate demo and live origins, and exposes positions, account data, historical events, report generation, and trading operations.

The useful read-only scope for Money Manager is:

- positions for reconciliation;
- historical events or generated reports for buys, sells, dividends, and fees;
- account currency and metadata.

The integration should begin against the demo environment. Trading endpoints must not be called, and credentials should be rejected if they grant capabilities beyond the documented read-only setup where the provider supports scoping.

## Recommended implementation order

1. Add encrypted, server-only integration credentials with explicit user consent, revocation, and deletion.
2. Build read-only connection tests for each provider.
3. Import fills and historical events into a separate provider staging table.
4. Normalize provider events into the existing investment trade ledger with stable external IDs and idempotent reconciliation.
5. Reconcile imported fill prices against the stored Kraken reference quote without replacing the authoritative provider fill.
6. Show sync time, incomplete data, missing prices, and reconciliation differences in both mobile apps.
7. Add audit logs, rate limits, provider health reporting, and an export that includes raw external IDs and fees.

Do not add order placement, cancellation, transfers, withdrawals, or automatic trading. Scheduled investments in Money Manager should remain reminders until a separate, explicit trading product and security review is approved.
