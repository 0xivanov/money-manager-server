CREATE TABLE notification_deliveries (
    id BIGSERIAL PRIMARY KEY,
    notification_id BIGINT NOT NULL REFERENCES notification_outbox(id) ON DELETE CASCADE,
    device_id BIGINT NOT NULL REFERENCES push_devices(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT,
    locked_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(notification_id, device_id),
    CONSTRAINT notification_deliveries_status_check CHECK (status IN ('pending', 'processing', 'sent', 'dead')),
    CONSTRAINT notification_deliveries_attempts_check CHECK (attempts >= 0)
);

CREATE INDEX notification_deliveries_pending_idx
    ON notification_deliveries(available_at, id)
    WHERE status = 'pending';
