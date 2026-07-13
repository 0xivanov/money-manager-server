ALTER TABLE transactions ADD COLUMN IF NOT EXISTS import_source TEXT;
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS import_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS transactions_user_import_fingerprint_idx
    ON transactions(user_id, import_source, import_fingerprint)
    WHERE import_source IS NOT NULL AND import_fingerprint IS NOT NULL;
