UPDATE transactions
SET category = 'other',
    excluded_from_budget = false,
    purpose = 'spending',
    investment_schedule_id = NULL,
    source_metadata = source_metadata - 'purpose_override',
    updated_at = now()
WHERE purpose = 'investment_transfer'
   OR lower(btrim(category)) = 'investment_transfer'
   OR investment_schedule_id IS NOT NULL;

DELETE FROM categories
WHERE type = 'expense' AND lower(btrim(name)) = 'investment_transfer';

DROP INDEX IF EXISTS transactions_user_purpose_period_idx;
DROP INDEX IF EXISTS transactions_investment_schedule_idx;

ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS transactions_purpose_check;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_purpose_check
        CHECK (purpose = 'spending'),
    ADD CONSTRAINT transactions_investment_schedule_disabled_check
        CHECK (investment_schedule_id IS NULL);
