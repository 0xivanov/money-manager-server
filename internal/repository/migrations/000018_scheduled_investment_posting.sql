ALTER TABLE investment_schedules
    ADD COLUMN materialized_through DATE,
    ADD COLUMN last_posted_on DATE;

CREATE TABLE investment_schedule_occurrences (
    id BIGSERIAL PRIMARY KEY,
    schedule_id BIGINT NOT NULL REFERENCES investment_schedules(id) ON DELETE CASCADE,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scheduled_for DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'planned',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (schedule_id, scheduled_for),
    CONSTRAINT investment_schedule_occurrences_status_check CHECK (status IN ('planned', 'posted'))
);

CREATE INDEX investment_schedule_occurrences_due_idx
    ON investment_schedule_occurrences(scheduled_for, id)
    WHERE status = 'planned';

ALTER TABLE investment_trades
    ADD COLUMN investment_schedule_occurrence_id BIGINT
        REFERENCES investment_schedule_occurrences(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX investment_trades_schedule_occurrence_idx
    ON investment_trades(investment_schedule_occurrence_id)
    WHERE investment_schedule_occurrence_id IS NOT NULL;
