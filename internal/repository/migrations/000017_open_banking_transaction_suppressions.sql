CREATE TABLE open_banking_transaction_suppressions (
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_account_id BIGINT NOT NULL REFERENCES open_banking_accounts(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, source_account_id, external_id),
    CONSTRAINT open_banking_transaction_suppressions_external_id_length_check
        CHECK (char_length(external_id) BETWEEN 1 AND 500)
);
