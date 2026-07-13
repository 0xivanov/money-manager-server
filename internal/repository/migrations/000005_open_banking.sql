CREATE TABLE open_banking_authorizations (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state_hash CHAR(64) UNIQUE NOT NULL,
    institution_name TEXT NOT NULL,
    country CHAR(2) NOT NULL,
    psu_type TEXT NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    authorization_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    error_code TEXT,
    error_description TEXT,
    connection_id BIGINT,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT open_banking_authorizations_country_check CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT open_banking_authorizations_psu_type_check CHECK (psu_type IN ('personal', 'business')),
    CONSTRAINT open_banking_authorizations_status_check CHECK (status IN ('pending', 'processing', 'completed', 'failed'))
);

CREATE TABLE open_banking_connections (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_session_id TEXT UNIQUE NOT NULL,
    institution_name TEXT NOT NULL,
    country CHAR(2) NOT NULL,
    psu_type TEXT NOT NULL,
    status TEXT NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT open_banking_connections_country_check CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT open_banking_connections_psu_type_check CHECK (psu_type IN ('personal', 'business'))
);

CREATE TABLE open_banking_accounts (
    id BIGSERIAL PRIMARY KEY,
    connection_id BIGINT NOT NULL REFERENCES open_banking_connections(id) ON DELETE CASCADE,
    provider_account_id TEXT,
    identification_hash TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    details TEXT NOT NULL DEFAULT '',
    cash_account_type TEXT NOT NULL,
    product TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL,
    display_identifier TEXT NOT NULL DEFAULT '',
    provider_payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, identification_hash)
);

ALTER TABLE open_banking_authorizations
    ADD CONSTRAINT open_banking_authorizations_connection_fk
    FOREIGN KEY (connection_id) REFERENCES open_banking_connections(id) ON DELETE SET NULL;

CREATE INDEX open_banking_authorizations_pending_idx
    ON open_banking_authorizations(state_hash, expires_at)
    WHERE status = 'pending' AND consumed_at IS NULL;
CREATE INDEX open_banking_connections_user_idx
    ON open_banking_connections(user_id, created_at DESC);
CREATE INDEX open_banking_accounts_connection_idx
    ON open_banking_accounts(connection_id, id);
