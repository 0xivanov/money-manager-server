ALTER TABLE open_banking_accounts
    ADD COLUMN last_synced_at TIMESTAMPTZ,
    ADD COLUMN next_sync_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN sync_claimed_until TIMESTAMPTZ;

ALTER TABLE transactions DROP CONSTRAINT transactions_status_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check
    CHECK (status IN ('pending', 'booked', 'cancelled'));

CREATE INDEX open_banking_accounts_sync_idx
    ON open_banking_accounts(next_sync_at, id)
    WHERE provider_account_id IS NOT NULL;

CREATE INDEX transactions_open_banking_account_date_idx
    ON transactions(source_account_id, occurred_at DESC, id DESC)
    WHERE source = 'open_banking';
