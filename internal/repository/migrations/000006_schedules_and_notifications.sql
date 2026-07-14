ALTER TABLE transactions ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE transactions ADD COLUMN status TEXT NOT NULL DEFAULT 'booked';
ALTER TABLE transactions ADD COLUMN excluded_from_budget BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE transactions ADD COLUMN source_account_id BIGINT REFERENCES open_banking_accounts(id) ON DELETE SET NULL;
ALTER TABLE transactions ADD COLUMN external_id TEXT;
ALTER TABLE transactions ADD COLUMN source_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE transactions
SET source = 'import'
WHERE import_source IS NOT NULL;

ALTER TABLE transactions ADD CONSTRAINT transactions_source_check
    CHECK (source IN ('manual', 'import', 'schedule', 'open_banking'));
ALTER TABLE transactions ADD CONSTRAINT transactions_status_check
    CHECK (status IN ('pending', 'booked'));
ALTER TABLE transactions ADD CONSTRAINT transactions_external_id_length_check
    CHECK (external_id IS NULL OR char_length(external_id) BETWEEN 1 AND 500);

CREATE TABLE transaction_schedules (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    category TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    amount NUMERIC(14,2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'EUR',
    frequency TEXT NOT NULL,
    frequency_interval SMALLINT NOT NULL DEFAULT 1,
    start_date DATE NOT NULL,
    end_date DATE,
    day_of_week SMALLINT,
    day_of_month SMALLINT,
    timezone TEXT NOT NULL DEFAULT 'Europe/Sofia',
    auto_post BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'active',
    materialized_through DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT transaction_schedules_type_check CHECK (type IN ('expense', 'income')),
    CONSTRAINT transaction_schedules_name_length_check CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    CONSTRAINT transaction_schedules_category_length_check CHECK (char_length(btrim(category)) BETWEEN 1 AND 40),
    CONSTRAINT transaction_schedules_description_length_check CHECK (char_length(description) <= 500),
    CONSTRAINT transaction_schedules_amount_check CHECK (amount > 0 AND amount <= 999999999999.99),
    CONSTRAINT transaction_schedules_currency_check CHECK (currency = 'EUR'),
    CONSTRAINT transaction_schedules_frequency_check CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    CONSTRAINT transaction_schedules_frequency_interval_check CHECK (frequency_interval BETWEEN 1 AND 365),
    CONSTRAINT transaction_schedules_date_range_check CHECK (end_date IS NULL OR end_date >= start_date),
    CONSTRAINT transaction_schedules_timezone_length_check CHECK (char_length(timezone) BETWEEN 1 AND 100),
    CONSTRAINT transaction_schedules_status_check CHECK (status IN ('active', 'paused', 'archived')),
    CONSTRAINT transaction_schedules_frequency_fields_check CHECK (
        (frequency = 'daily' AND day_of_week IS NULL AND day_of_month IS NULL) OR
        (frequency = 'weekly' AND day_of_week BETWEEN 1 AND 7 AND day_of_month IS NULL) OR
        (frequency = 'monthly' AND day_of_month BETWEEN 1 AND 31 AND day_of_week IS NULL)
    )
);

CREATE TABLE transaction_schedule_occurrences (
    id BIGSERIAL PRIMARY KEY,
    schedule_id BIGINT NOT NULL REFERENCES transaction_schedules(id) ON DELETE CASCADE,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scheduled_for DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'planned',
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    category TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    amount NUMERIC(14,2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'EUR',
    auto_post BOOLEAN NOT NULL,
    transaction_id INT REFERENCES transactions(id) ON DELETE SET NULL,
    posted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (schedule_id, scheduled_for),
    CONSTRAINT transaction_schedule_occurrences_status_check CHECK (status IN ('planned', 'posted', 'skipped')),
    CONSTRAINT transaction_schedule_occurrences_type_check CHECK (type IN ('expense', 'income')),
    CONSTRAINT transaction_schedule_occurrences_name_length_check CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    CONSTRAINT transaction_schedule_occurrences_category_length_check CHECK (char_length(btrim(category)) BETWEEN 1 AND 40),
    CONSTRAINT transaction_schedule_occurrences_description_length_check CHECK (char_length(description) <= 500),
    CONSTRAINT transaction_schedule_occurrences_amount_check CHECK (amount > 0 AND amount <= 999999999999.99),
    CONSTRAINT transaction_schedule_occurrences_currency_check CHECK (currency = 'EUR')
);

ALTER TABLE transactions ADD COLUMN schedule_occurrence_id BIGINT
    REFERENCES transaction_schedule_occurrences(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX transactions_schedule_occurrence_idx
    ON transactions(schedule_occurrence_id)
    WHERE schedule_occurrence_id IS NOT NULL;
CREATE UNIQUE INDEX transactions_open_banking_external_idx
    ON transactions(user_id, source_account_id, external_id)
    WHERE source = 'open_banking' AND source_account_id IS NOT NULL AND external_id IS NOT NULL;
CREATE INDEX transactions_user_budget_period_idx
    ON transactions(user_id, occurred_at, category)
    WHERE type = 'expense' AND status = 'booked' AND NOT excluded_from_budget;
CREATE INDEX transaction_schedules_user_status_idx
    ON transaction_schedules(user_id, status, created_at DESC);
CREATE INDEX transaction_schedules_active_materialization_idx
    ON transaction_schedules(materialized_through, id)
    WHERE status = 'active';
CREATE INDEX transaction_schedule_occurrences_user_date_idx
    ON transaction_schedule_occurrences(user_id, scheduled_for, id);
CREATE INDEX transaction_schedule_occurrences_due_idx
    ON transaction_schedule_occurrences(scheduled_for, id)
    WHERE status = 'planned' AND auto_post;

CREATE TABLE notification_preferences (
    user_id INT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    bank_spending BOOLEAN NOT NULL DEFAULT true,
    budget_alerts BOOLEAN NOT NULL DEFAULT true,
    scheduled_money BOOLEAN NOT NULL DEFAULT true,
    investment_reminders BOOLEAN NOT NULL DEFAULT true,
    quiet_hours_start TIME,
    quiet_hours_end TIME,
    timezone TEXT NOT NULL DEFAULT 'Europe/Sofia',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT notification_preferences_timezone_length_check CHECK (char_length(timezone) BETWEEN 1 AND 100),
    CONSTRAINT notification_preferences_quiet_hours_check CHECK (
        (quiet_hours_start IS NULL AND quiet_hours_end IS NULL) OR
        (quiet_hours_start IS NOT NULL AND quiet_hours_end IS NOT NULL)
    )
);

CREATE TABLE push_devices (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform TEXT NOT NULL,
    device_token TEXT NOT NULL,
    app_id TEXT NOT NULL,
    environment TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (platform, device_token),
    CONSTRAINT push_devices_platform_check CHECK (platform IN ('ios', 'android')),
    CONSTRAINT push_devices_environment_check CHECK (environment IN ('sandbox', 'production')),
    CONSTRAINT push_devices_token_length_check CHECK (char_length(device_token) BETWEEN 16 AND 4096),
    CONSTRAINT push_devices_app_id_length_check CHECK (char_length(app_id) BETWEEN 1 AND 255)
);

CREATE INDEX push_devices_user_active_idx
    ON push_devices(user_id, platform, id)
    WHERE active;

CREATE TABLE notification_outbox (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    event_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending',
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT,
    locked_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT notification_outbox_event_type_length_check CHECK (char_length(event_type) BETWEEN 1 AND 100),
    CONSTRAINT notification_outbox_event_key_length_check CHECK (char_length(event_key) BETWEEN 1 AND 255),
    CONSTRAINT notification_outbox_title_length_check CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT notification_outbox_body_length_check CHECK (char_length(body) BETWEEN 1 AND 500),
    CONSTRAINT notification_outbox_status_check CHECK (status IN ('pending', 'processing', 'sent', 'dead')),
    CONSTRAINT notification_outbox_attempts_check CHECK (attempts >= 0)
);

CREATE INDEX notification_outbox_pending_idx
    ON notification_outbox(available_at, id)
    WHERE status = 'pending';
