DELETE FROM open_banking_transaction_suppressions duplicate
USING open_banking_transaction_suppressions retained
WHERE duplicate.user_id = retained.user_id
  AND duplicate.external_id = retained.external_id
  AND duplicate.ctid > retained.ctid;

ALTER TABLE open_banking_transaction_suppressions
    DROP CONSTRAINT IF EXISTS open_banking_transaction_suppressions_pkey,
    DROP CONSTRAINT IF EXISTS open_banking_transaction_suppressions_source_account_id_fkey;

ALTER TABLE open_banking_transaction_suppressions
    ALTER COLUMN source_account_id DROP NOT NULL,
    ADD CONSTRAINT open_banking_transaction_suppressions_pkey
        PRIMARY KEY (user_id, external_id),
    ADD CONSTRAINT open_banking_transaction_suppressions_source_account_id_fkey
        FOREIGN KEY (source_account_id) REFERENCES open_banking_accounts(id) ON DELETE SET NULL;

CREATE INDEX open_banking_transaction_suppressions_account_idx
    ON open_banking_transaction_suppressions(source_account_id)
    WHERE source_account_id IS NOT NULL;
