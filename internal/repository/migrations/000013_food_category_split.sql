UPDATE categories
SET is_default = true,
    sort_order = CASE lower(btrim(name))
        WHEN 'groceries' THEN 0
        WHEN 'dining_out' THEN 1
        WHEN 'going_out' THEN 2
    END,
    updated_at = now()
WHERE type = 'expense'
  AND lower(btrim(name)) IN ('groceries', 'dining_out', 'going_out')
  AND active;

INSERT INTO categories(user_id, type, name, is_default, active, sort_order)
SELECT users.id, 'expense', category.name, true, true, category.sort_order
FROM users
CROSS JOIN (VALUES
    ('groceries', 0),
    ('dining_out', 1),
    ('going_out', 2)
) AS category(name, sort_order)
WHERE NOT EXISTS (
    SELECT 1
    FROM categories existing
    WHERE existing.user_id = users.id
      AND existing.type = 'expense'
      AND lower(btrim(existing.name)) = category.name
      AND existing.active
);

UPDATE categories
SET sort_order = CASE lower(btrim(name))
        WHEN 'transport' THEN 3
        WHEN 'housing' THEN 4
        WHEN 'utilities' THEN 5
        WHEN 'health' THEN 6
        WHEN 'entertainment' THEN 7
        WHEN 'shopping' THEN 8
        WHEN 'travel' THEN 9
        WHEN 'education' THEN 10
        WHEN 'beauty' THEN 11
        WHEN 'other' THEN 12
    END,
    updated_at = now()
WHERE type = 'expense'
  AND lower(btrim(name)) IN (
      'transport', 'housing', 'utilities', 'health', 'entertainment',
      'shopping', 'travel', 'education', 'beauty', 'other'
  )
  AND is_default
  AND active;

WITH food_transactions AS (
    SELECT id,
           CASE
               WHEN btrim(COALESCE(
                   source_metadata->>'merchant_category_code',
                   source_metadata->>'mcc',
                   ''
               )) ~ '^[0-9]{1,6}$'
               THEN btrim(COALESCE(
                   source_metadata->>'merchant_category_code',
                   source_metadata->>'mcc'
               ))::int
           END AS mcc,
           lower(regexp_replace(description, '[[:space:]]+', ' ', 'g')) AS classification_text
    FROM transactions
    WHERE type = 'expense'
      AND lower(btrim(category)) = 'food'
), classified AS (
    SELECT id,
           CASE
               WHEN classification_text LIKE ANY (ARRAY[
                   '%shisha%', '%hookah%', '%nightclub%', '%night club%', '%cocktail%',
                   '%lounge%', '%club entry%', '%club ticket%'
               ]) THEN 'going_out'
               WHEN mcc IN (5411, 5422, 5441, 5451, 5462, 5499) THEN 'groceries'
               WHEN mcc = 5813 THEN 'going_out'
               WHEN mcc IN (5811, 5812, 5814) THEN 'dining_out'
               WHEN classification_text LIKE ANY (ARRAY[
                   '%restaurant%', '%cafe%', '%coffee%', '%bakery%', '%takeaway%',
                   '%glovo%', '%wolt%', '%deliveroo%', '%mcdonald%', '%kfc%', '%happy bar%'
               ]) THEN 'dining_out'
               ELSE 'groceries'
           END AS category
    FROM food_transactions
)
UPDATE transactions AS transaction
SET category = classified.category,
    source_metadata = CASE
        WHEN lower(COALESCE(transaction.source_metadata->>'classified_category', '')) = 'food'
        THEN transaction.source_metadata || jsonb_build_object('classified_category', classified.category)
        ELSE transaction.source_metadata
    END,
    updated_at = now()
FROM classified
WHERE transaction.id = classified.id;

UPDATE budgets AS budget
SET category = 'groceries',
    updated_at = now()
WHERE lower(btrim(budget.category)) = 'food'
  AND (
      budget.status <> 'active'
      OR NOT EXISTS (
          SELECT 1
          FROM budgets existing
          WHERE existing.user_id = budget.user_id
            AND lower(btrim(existing.category)) = 'groceries'
            AND existing.period = budget.period
            AND existing.status = 'active'
      )
  );

UPDATE transaction_schedules
SET category = 'groceries',
    updated_at = now()
WHERE type = 'expense'
  AND lower(btrim(category)) = 'food';

UPDATE transaction_schedule_occurrences
SET category = 'groceries',
    updated_at = now()
WHERE type = 'expense'
  AND lower(btrim(category)) = 'food';

UPDATE categories
SET active = false,
    is_default = false,
    updated_at = now()
WHERE type = 'expense'
  AND lower(btrim(name)) = 'food'
  AND active;
