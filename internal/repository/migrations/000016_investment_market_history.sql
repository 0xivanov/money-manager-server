CREATE TABLE investment_market_history (
    asset_type TEXT NOT NULL CHECK (asset_type = 'stock'),
    symbol TEXT NOT NULL CHECK (char_length(symbol) BETWEEN 1 AND 20),
    exchange TEXT NOT NULL CHECK (char_length(exchange) BETWEEN 1 AND 20),
    currency CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    as_of DATE NOT NULL,
    price NUMERIC(30,10) NOT NULL CHECK (price > 0),
    provider TEXT NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 40),
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_type, symbol, exchange, currency, as_of)
);

CREATE INDEX investment_market_history_latest_idx
    ON investment_market_history(asset_type, symbol, exchange, currency, as_of DESC);
