UPDATE transactions
SET status = 'booked',
    updated_at = now()
WHERE status <> 'booked';

ALTER TABLE transactions
    DROP CONSTRAINT transactions_status_check;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_status_check
        CHECK (status = 'booked');
