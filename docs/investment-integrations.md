# Investment integration assessment

Reviewed on 2026-07-14. No broker or exchange account API is integrated yet. Public Kraken market data is used for BTC and ETH reference pricing.

## Current product behavior

Money Manager records the EUR value and exact time of a BTC or ETH buy or sell. Future execution times are rejected. The backend looks up a nearby BTC/EUR or ETH/EUR Kraken trade, stores the reference price and quote time, and calculates quantity. Portfolio quantity, cost basis, realized profit, and unrealized profit are derived from that trade ledger. Current valuation uses Kraken's public order-book midpoint, and the portfolio chart combines daily close data with the user's buys and sells. Completed chart days use `23:59:59Z`; the final point uses the exact current quote time. No API key is required.

The Kraken quote is a market reference and can differ from the user's actual Revolut X or other broker fill because of spread, fees, and execution timing. The audit export retains the entered EUR amount, derived quantity, reference unit price, provider, provider timestamp, fees, and execution timestamp.

New stock trades and investment plans are temporarily disabled. Existing stock data and generic ledger code remain in place for a future market-data provider, but stock positions are excluded from the supported crypto valuation instead of invalidating it.

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
