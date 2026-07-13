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

CREATE TEMP TABLE users_to_quarantine ON COMMIT DROP AS
WITH ranked AS (
    SELECT id,
           email,
           row_number() OVER (PARTITION BY lower(btrim(email)) ORDER BY id) AS normalized_rank
    FROM users
)
SELECT id,
       CASE
           WHEN btrim(email) = '' THEN 'email is empty after normalization'
           WHEN char_length(btrim(email)) > 254 THEN 'email exceeds 254 characters'
           WHEN strpos(email, chr(10)) > 0 OR strpos(email, chr(13)) > 0 OR strpos(email, chr(9)) > 0 THEN 'email contains control characters'
           ELSE 'case-insensitive duplicate email; lowest user id retained'
       END AS reason
FROM ranked
WHERE btrim(email) = ''
   OR char_length(btrim(email)) > 254
   OR strpos(email, chr(10)) > 0
   OR strpos(email, chr(13)) > 0
   OR strpos(email, chr(9)) > 0
   OR normalized_rank > 1;

INSERT INTO migration_quarantine(migration_version, source_table, source_id, reason, row_data)
SELECT 2, 'transactions', tx_row.id, 'parent user quarantined: ' || target.reason, to_jsonb(tx_row)
FROM transactions AS tx_row
JOIN users_to_quarantine AS target ON target.id = tx_row.user_id
ON CONFLICT DO NOTHING;

INSERT INTO migration_quarantine(migration_version, source_table, source_id, reason, row_data)
SELECT 2, 'categories', category.id, 'parent user quarantined: ' || target.reason, to_jsonb(category)
FROM categories AS category
JOIN users_to_quarantine AS target ON target.id = category.user_id
ON CONFLICT DO NOTHING;

INSERT INTO migration_quarantine(migration_version, source_table, source_id, reason, row_data)
SELECT 2, 'users', users.id, target.reason, to_jsonb(users)
FROM users
JOIN users_to_quarantine AS target ON target.id = users.id
ON CONFLICT DO NOTHING;

DELETE FROM users USING users_to_quarantine WHERE users.id = users_to_quarantine.id;

UPDATE users
SET email = lower(btrim(email)), updated_at = now()
WHERE email <> lower(btrim(email));

CREATE TEMP TABLE categories_to_quarantine ON COMMIT DROP AS
WITH ranked AS (
    SELECT id,
           type,
           name,
           active,
           row_number() OVER (
               PARTITION BY user_id, lower(btrim(type)), lower(btrim(name))
               ORDER BY active DESC, is_default DESC, id
           ) AS normalized_rank
    FROM categories
)
SELECT id,
       CASE
           WHEN lower(btrim(type)) NOT IN ('expense', 'income') THEN 'unsupported category type'
           WHEN char_length(btrim(name)) = 0 THEN 'category name is empty after normalization'
           WHEN char_length(btrim(name)) > 40 THEN 'category name exceeds 40 characters'
           ELSE 'duplicate active category after normalization; default or lowest id retained'
       END AS reason
FROM ranked
WHERE lower(btrim(type)) NOT IN ('expense', 'income')
   OR char_length(btrim(name)) = 0
   OR char_length(btrim(name)) > 40
   OR (active AND normalized_rank > 1);

INSERT INTO migration_quarantine(migration_version, source_table, source_id, reason, row_data)
SELECT 2, 'categories', category.id, target.reason, to_jsonb(category)
FROM categories AS category
JOIN categories_to_quarantine AS target ON target.id = category.id
ON CONFLICT DO NOTHING;

DELETE FROM categories USING categories_to_quarantine WHERE categories.id = categories_to_quarantine.id;

UPDATE categories
SET type = lower(btrim(type)), name = btrim(name), updated_at = now()
WHERE type <> lower(btrim(type)) OR name <> btrim(name);

UPDATE transactions
SET type = lower(btrim(type)),
    category = btrim(category),
    currency = upper(btrim(currency)),
    updated_at = now()
WHERE type <> lower(btrim(type))
   OR category <> btrim(category)
   OR currency <> upper(btrim(currency));

CREATE TEMP TABLE transactions_to_quarantine ON COMMIT DROP AS
SELECT id,
       CASE
           WHEN type NOT IN ('expense', 'income') THEN 'unsupported transaction type'
           WHEN amount <= 0 THEN 'transaction amount is not positive'
           WHEN amount > 999999999999.99 THEN 'transaction amount exceeds supported maximum'
           WHEN currency <> 'EUR' THEN 'unsupported legacy currency; no implicit conversion performed'
           WHEN char_length(category) = 0 THEN 'transaction category is empty after normalization'
           WHEN char_length(category) > 40 THEN 'transaction category exceeds 40 characters'
           ELSE 'transaction description exceeds 500 characters'
       END AS reason
FROM transactions
WHERE type NOT IN ('expense', 'income')
   OR amount <= 0
   OR amount > 999999999999.99
   OR currency <> 'EUR'
   OR char_length(category) = 0
   OR char_length(category) > 40
   OR char_length(description) > 500;

INSERT INTO migration_quarantine(migration_version, source_table, source_id, reason, row_data)
SELECT 2, 'transactions', tx_row.id, target.reason, to_jsonb(tx_row)
FROM transactions AS tx_row
JOIN transactions_to_quarantine AS target ON target.id = tx_row.id
ON CONFLICT DO NOTHING;

DELETE FROM transactions USING transactions_to_quarantine WHERE transactions.id = transactions_to_quarantine.id;

DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_email_normalized_check' AND conrelid = 'users'::regclass) THEN
        ALTER TABLE users ADD CONSTRAINT users_email_normalized_check
            CHECK (email = lower(btrim(email)) AND char_length(email) BETWEEN 1 AND 254) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'categories_type_check' AND conrelid = 'categories'::regclass) THEN
        ALTER TABLE categories ADD CONSTRAINT categories_type_check
            CHECK (type IN ('expense', 'income')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'categories_name_length_check' AND conrelid = 'categories'::regclass) THEN
        ALTER TABLE categories ADD CONSTRAINT categories_name_length_check
            CHECK (char_length(btrim(name)) BETWEEN 1 AND 40) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'transactions_type_check' AND conrelid = 'transactions'::regclass) THEN
        ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
            CHECK (type IN ('expense', 'income')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'transactions_amount_check' AND conrelid = 'transactions'::regclass) THEN
        ALTER TABLE transactions ADD CONSTRAINT transactions_amount_check
            CHECK (amount > 0 AND amount <= 999999999999.99) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'transactions_currency_check' AND conrelid = 'transactions'::regclass) THEN
        ALTER TABLE transactions ADD CONSTRAINT transactions_currency_check
            CHECK (currency = 'EUR') NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'transactions_category_length_check' AND conrelid = 'transactions'::regclass) THEN
        ALTER TABLE transactions ADD CONSTRAINT transactions_category_length_check
            CHECK (char_length(btrim(category)) BETWEEN 1 AND 40) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'transactions_description_length_check' AND conrelid = 'transactions'::regclass) THEN
        ALTER TABLE transactions ADD CONSTRAINT transactions_description_length_check
            CHECK (char_length(description) <= 500) NOT VALID;
    END IF;
END
$migration$;

ALTER TABLE users VALIDATE CONSTRAINT users_email_normalized_check;
ALTER TABLE categories VALIDATE CONSTRAINT categories_type_check;
ALTER TABLE categories VALIDATE CONSTRAINT categories_name_length_check;
ALTER TABLE transactions VALIDATE CONSTRAINT transactions_type_check;
ALTER TABLE transactions VALIDATE CONSTRAINT transactions_amount_check;
ALTER TABLE transactions VALIDATE CONSTRAINT transactions_currency_check;
ALTER TABLE transactions VALIDATE CONSTRAINT transactions_category_length_check;
ALTER TABLE transactions VALIDATE CONSTRAINT transactions_description_length_check;

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_idx ON users(lower(email));
CREATE UNIQUE INDEX IF NOT EXISTS categories_user_type_name_active_idx
    ON categories(user_id, type, lower(name)) WHERE active;
CREATE INDEX IF NOT EXISTS transactions_user_occurred_id_idx
    ON transactions(user_id, occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS transactions_user_type_occurred_idx
    ON transactions(user_id, type, occurred_at DESC);
