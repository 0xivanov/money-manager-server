# Investment integration assessment

Reviewed on 2026-07-13. No broker or exchange API is integrated yet.

## Current product behavior

Money Manager tracks manually entered BTC, ETH, and stock buys and sells. Portfolio quantity, cost basis, realized profit, and unrealized profit are derived from that trade ledger. Users can enter current prices, schedule investment reminders, and export an audit CSV. A missing market price is shown as missing instead of inventing a portfolio total.

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
5. Fetch prices through a deliberately selected, licensed market-data source or the provider's documented read-only market-data API.
6. Show sync time, incomplete data, missing prices, and reconciliation differences in both mobile apps.
7. Add audit logs, rate limits, provider health reporting, and an export that includes raw external IDs and fees.

Do not add order placement, cancellation, transfers, withdrawals, or automatic trading. Scheduled investments in Money Manager should remain reminders until a separate, explicit trading product and security review is approved.
