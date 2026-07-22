ALTER TABLE investment_trades
    ADD COLUMN exchange TEXT NOT NULL DEFAULT '',
    ADD COLUMN market_currency TEXT NOT NULL DEFAULT 'EUR';

ALTER TABLE investment_schedules
    ADD COLUMN exchange TEXT NOT NULL DEFAULT '',
    ADD COLUMN market_currency TEXT NOT NULL DEFAULT 'EUR';

UPDATE investment_trades
SET exchange = 'UNKNOWN'
WHERE asset_type = 'stock' AND exchange = '';

UPDATE investment_schedules
SET exchange = 'UNKNOWN'
WHERE asset_type = 'stock' AND exchange = '';

ALTER TABLE investment_trades
    ADD CONSTRAINT investment_trades_exchange_check CHECK (
        (asset_type = 'crypto' AND exchange = '') OR
        (asset_type = 'stock' AND char_length(btrim(exchange)) BETWEEN 1 AND 20)
    ),
    ADD CONSTRAINT investment_trades_market_currency_check CHECK (
        market_currency ~ '^[A-Z]{3}$'
    );

ALTER TABLE investment_schedules
    ADD CONSTRAINT investment_schedules_exchange_check CHECK (
        (asset_type = 'crypto' AND exchange = '') OR
        (asset_type = 'stock' AND char_length(btrim(exchange)) BETWEEN 1 AND 20)
    ),
    ADD CONSTRAINT investment_schedules_market_currency_check CHECK (
        market_currency ~ '^[A-Z]{3}$'
    );

DROP INDEX investment_trades_position_idx;
CREATE INDEX investment_trades_position_idx
    ON investment_trades(user_id, asset_type, symbol, exchange, broker, occurred_at, id);
