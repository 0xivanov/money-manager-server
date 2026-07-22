ALTER TABLE transactions
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'spending',
    ADD COLUMN investment_schedule_id BIGINT REFERENCES investment_schedules(id) ON DELETE SET NULL;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_purpose_check
    CHECK (purpose IN ('spending', 'investment_transfer'));

UPDATE transactions
SET purpose = 'investment_transfer', excluded_from_budget = true
WHERE type = 'expense' AND lower(btrim(category)) = 'investment_transfer';

INSERT INTO categories(user_id, type, name, is_default, active, sort_order)
SELECT users.id, 'expense', 'investment_transfer', true, true, 120
FROM users
ON CONFLICT DO NOTHING;

UPDATE categories
SET is_default = true, active = true, updated_at = now()
WHERE type = 'expense' AND lower(name) = 'investment_transfer';

CREATE INDEX transactions_user_purpose_period_idx
    ON transactions(user_id, purpose, occurred_at DESC);

CREATE INDEX transactions_investment_schedule_idx
    ON transactions(investment_schedule_id, occurred_at DESC)
    WHERE investment_schedule_id IS NOT NULL;
