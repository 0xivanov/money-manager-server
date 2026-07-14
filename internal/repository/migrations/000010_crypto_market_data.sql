ALTER TABLE investment_trades
    ADD COLUMN amount NUMERIC,
    ADD COLUMN price_provider TEXT,
    ADD COLUMN price_as_of TIMESTAMPTZ;

UPDATE investment_trades
SET amount = trim_scale(quantity * price_per_unit),
    price_provider = 'legacy_manual',
    price_as_of = occurred_at::timestamp AT TIME ZONE 'UTC';

ALTER TABLE investment_trades
    ALTER COLUMN amount SET NOT NULL,
    ALTER COLUMN price_provider SET NOT NULL,
    ALTER COLUMN price_as_of SET NOT NULL,
    ALTER COLUMN quantity TYPE NUMERIC(38,18),
    ALTER COLUMN occurred_at TYPE TIMESTAMPTZ
        USING occurred_at::timestamp AT TIME ZONE 'UTC';

-- Keep inserts from the pre-market-data binary working during a rolling deploy
-- and after a binary rollback. New binaries provide these fields explicitly.
CREATE FUNCTION investment_trades_fill_legacy_market_data()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.amount IS NULL THEN
        NEW.amount := trim_scale(NEW.quantity * NEW.price_per_unit);
    END IF;
    IF NEW.price_provider IS NULL THEN
        NEW.price_provider := 'legacy_manual';
    END IF;
    IF NEW.price_as_of IS NULL THEN
        NEW.price_as_of := NEW.occurred_at;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER investment_trades_fill_legacy_market_data_before_insert
BEFORE INSERT ON investment_trades
FOR EACH ROW
EXECUTE FUNCTION investment_trades_fill_legacy_market_data();

ALTER TABLE investment_trades
    ADD CONSTRAINT investment_trades_amount_check
        CHECK (amount > 0 AND (price_provider = 'legacy_manual' OR amount <= 999999999999.99)),
    ADD CONSTRAINT investment_trades_price_provider_length_check
        CHECK (char_length(btrim(price_provider)) BETWEEN 1 AND 100);

DROP INDEX investment_trades_user_date_idx;
CREATE INDEX investment_trades_user_date_idx
    ON investment_trades(user_id, occurred_at DESC, id DESC);

DROP INDEX investment_trades_position_idx;
CREATE INDEX investment_trades_position_idx
    ON investment_trades(user_id, asset_type, symbol, broker, occurred_at, id);
