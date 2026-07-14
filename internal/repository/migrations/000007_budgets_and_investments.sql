CREATE TABLE budgets (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    amount NUMERIC(14,2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'EUR',
    period TEXT NOT NULL DEFAULT 'monthly',
    warning_threshold SMALLINT NOT NULL DEFAULT 80,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT budgets_name_length_check CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    CONSTRAINT budgets_category_length_check CHECK (char_length(category) <= 40),
    CONSTRAINT budgets_amount_check CHECK (amount > 0 AND amount <= 999999999999.99),
    CONSTRAINT budgets_currency_check CHECK (currency = 'EUR'),
    CONSTRAINT budgets_period_check CHECK (period IN ('weekly', 'monthly')),
    CONSTRAINT budgets_warning_threshold_check CHECK (warning_threshold BETWEEN 1 AND 100),
    CONSTRAINT budgets_status_check CHECK (status IN ('active', 'archived'))
);

CREATE UNIQUE INDEX budgets_active_scope_idx
    ON budgets(user_id, lower(category), period)
    WHERE status = 'active';
CREATE INDEX budgets_user_status_idx ON budgets(user_id, status, id);

CREATE TABLE budget_alerts (
    id BIGSERIAL PRIMARY KEY,
    budget_id BIGINT NOT NULL REFERENCES budgets(id) ON DELETE CASCADE,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start DATE NOT NULL,
    alert_level SMALLINT NOT NULL,
    spent_amount NUMERIC(14,2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (budget_id, period_start, alert_level),
    CONSTRAINT budget_alerts_level_check CHECK (alert_level BETWEEN 1 AND 100),
    CONSTRAINT budget_alerts_spent_check CHECK (spent_amount >= 0)
);

CREATE INDEX budget_alerts_user_created_idx ON budget_alerts(user_id, created_at DESC);

CREATE TABLE investment_trades (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    asset_type TEXT NOT NULL,
    symbol TEXT NOT NULL,
    asset_name TEXT NOT NULL,
    broker TEXT NOT NULL,
    side TEXT NOT NULL,
    quantity NUMERIC(28,10) NOT NULL,
    price_per_unit NUMERIC(20,8) NOT NULL,
    fees NUMERIC(14,2) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'EUR',
    occurred_at DATE NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT investment_trades_asset_type_check CHECK (asset_type IN ('crypto', 'stock')),
    CONSTRAINT investment_trades_symbol_length_check CHECK (char_length(symbol) BETWEEN 1 AND 20),
    CONSTRAINT investment_trades_asset_name_length_check CHECK (char_length(btrim(asset_name)) BETWEEN 1 AND 100),
    CONSTRAINT investment_trades_broker_check CHECK (broker IN ('manual', 'revolut_x', 'trading212')),
    CONSTRAINT investment_trades_side_check CHECK (side IN ('buy', 'sell')),
    CONSTRAINT investment_trades_quantity_check CHECK (quantity > 0),
    CONSTRAINT investment_trades_price_check CHECK (price_per_unit > 0),
    CONSTRAINT investment_trades_fees_check CHECK (fees >= 0),
    CONSTRAINT investment_trades_currency_check CHECK (currency = 'EUR'),
    CONSTRAINT investment_trades_notes_length_check CHECK (char_length(notes) <= 500)
);

CREATE INDEX investment_trades_user_date_idx ON investment_trades(user_id, occurred_at DESC, id DESC);
CREATE INDEX investment_trades_position_idx ON investment_trades(user_id, asset_type, symbol, broker, occurred_at, id);

CREATE TABLE investment_prices (
    asset_type TEXT NOT NULL,
    symbol TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'EUR',
    price NUMERIC(20,8) NOT NULL,
    provider TEXT NOT NULL DEFAULT 'manual',
    as_of TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_type, symbol, currency),
    CONSTRAINT investment_prices_asset_type_check CHECK (asset_type IN ('crypto', 'stock')),
    CONSTRAINT investment_prices_symbol_length_check CHECK (char_length(symbol) BETWEEN 1 AND 20),
    CONSTRAINT investment_prices_currency_check CHECK (currency = 'EUR'),
    CONSTRAINT investment_prices_price_check CHECK (price > 0),
    CONSTRAINT investment_prices_provider_length_check CHECK (char_length(provider) BETWEEN 1 AND 100)
);

CREATE TABLE investment_schedules (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    asset_type TEXT NOT NULL,
    symbol TEXT NOT NULL,
    asset_name TEXT NOT NULL,
    broker TEXT NOT NULL,
    amount NUMERIC(14,2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'EUR',
    frequency TEXT NOT NULL,
    frequency_interval SMALLINT NOT NULL DEFAULT 1,
    start_date DATE NOT NULL,
    end_date DATE,
    day_of_week SMALLINT,
    day_of_month SMALLINT,
    timezone TEXT NOT NULL DEFAULT 'Europe/Sofia',
    status TEXT NOT NULL DEFAULT 'active',
    last_notified_on DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT investment_schedules_asset_type_check CHECK (asset_type IN ('crypto', 'stock')),
    CONSTRAINT investment_schedules_symbol_length_check CHECK (char_length(symbol) BETWEEN 1 AND 20),
    CONSTRAINT investment_schedules_asset_name_length_check CHECK (char_length(btrim(asset_name)) BETWEEN 1 AND 100),
    CONSTRAINT investment_schedules_broker_check CHECK (broker IN ('manual', 'revolut_x', 'trading212')),
    CONSTRAINT investment_schedules_amount_check CHECK (amount > 0 AND amount <= 999999999999.99),
    CONSTRAINT investment_schedules_currency_check CHECK (currency = 'EUR'),
    CONSTRAINT investment_schedules_frequency_check CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    CONSTRAINT investment_schedules_frequency_interval_check CHECK (frequency_interval BETWEEN 1 AND 365),
    CONSTRAINT investment_schedules_date_range_check CHECK (end_date IS NULL OR end_date >= start_date),
    CONSTRAINT investment_schedules_timezone_length_check CHECK (char_length(timezone) BETWEEN 1 AND 100),
    CONSTRAINT investment_schedules_status_check CHECK (status IN ('active', 'paused', 'archived')),
    CONSTRAINT investment_schedules_frequency_fields_check CHECK (
        (frequency = 'daily' AND day_of_week IS NULL AND day_of_month IS NULL) OR
        (frequency = 'weekly' AND day_of_week BETWEEN 1 AND 7 AND day_of_month IS NULL) OR
        (frequency = 'monthly' AND day_of_month BETWEEN 1 AND 31 AND day_of_week IS NULL)
    )
);

CREATE INDEX investment_schedules_user_status_idx ON investment_schedules(user_id, status, id);
CREATE INDEX investment_schedules_active_idx ON investment_schedules(id) WHERE status = 'active';
