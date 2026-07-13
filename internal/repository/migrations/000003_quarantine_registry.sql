CREATE TABLE IF NOT EXISTS migration_quarantine (
    id BIGSERIAL PRIMARY KEY,
    migration_version BIGINT NOT NULL,
    source_table TEXT NOT NULL,
    source_id BIGINT NOT NULL,
    reason TEXT NOT NULL,
    row_data JSONB NOT NULL,
    quarantined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (migration_version, source_table, source_id)
);

COMMENT ON TABLE migration_quarantine IS
    'Original rows removed by compatibility migrations. Inspect and remediate manually before deleting quarantine records.';
