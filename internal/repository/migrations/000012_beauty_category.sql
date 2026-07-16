UPDATE categories
SET is_default = true,
    sort_order = 9,
    updated_at = now()
WHERE type = 'expense'
  AND lower(btrim(name)) = 'beauty'
  AND active;

-- Some upgraded legacy databases contain explicitly assigned category IDs,
-- so align the serial sequence before inserting defaults for existing users.
SELECT setval(
    pg_get_serial_sequence('categories', 'id'),
    GREATEST(COALESCE((SELECT MAX(id) FROM categories), 0) + 1, 1),
    false
);

INSERT INTO categories(user_id, type, name, is_default, active, sort_order)
SELECT users.id, 'expense', 'beauty', true, true, 9
FROM users
WHERE NOT EXISTS (
    SELECT 1
    FROM categories
    WHERE categories.user_id = users.id
      AND categories.type = 'expense'
      AND lower(btrim(categories.name)) = 'beauty'
      AND categories.active
);

UPDATE categories
SET sort_order = 10,
    updated_at = now()
WHERE type = 'expense'
  AND lower(btrim(name)) = 'other'
  AND is_default
  AND active;
