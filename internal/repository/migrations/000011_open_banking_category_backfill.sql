-- The legacy importer automatically classified salary and refund income plus
-- several expense MCC groups, but did not record classification provenance.
-- Reconstruct that legacy result before deciding whether a non-other category
-- was a user correction. Explicit automatic provenance remains authoritative,
-- while explicit user provenance is normalized to the current override flags.
WITH legacy_candidates AS (
    SELECT id,
           type,
           lower(btrim(category)) AS current_category,
           lower(description) AS classification_text,
           COALESCE(source_metadata->>'category_source' = 'user_override', false) AS explicit_user_override,
           CASE
               WHEN btrim(COALESCE(
                   source_metadata->>'merchant_category_code',
                   source_metadata->>'mcc',
                   ''
               )) ~ '^[+-]?[0-9]+$'
               THEN btrim(COALESCE(
                   source_metadata->>'merchant_category_code',
                   source_metadata->>'mcc'
               ))::numeric
           END AS mcc
    FROM transactions
    WHERE source = 'open_banking'
      AND lower(btrim(category)) <> 'other'
      AND NOT (
          source_metadata @> '{"classification_override":true}'::jsonb
          OR source_metadata @> '{"type_override":true}'::jsonb
          OR source_metadata @> '{"category_override":true}'::jsonb
      )
      AND NOT (
          COALESCE(
              NULLIF(source_metadata->>'category_source', ''),
              NULLIF(source_metadata->>'classification_source', ''),
              ''
          ) IN ('mcc', 'expense_keyword', 'income_keyword')
          AND lower(COALESCE(source_metadata->>'classified_category', '')) = lower(category)
      )
), legacy_classified AS (
    SELECT id,
           current_category,
           explicit_user_override,
           CASE
               WHEN type = 'income' THEN
                   CASE
                       WHEN classification_text LIKE '%salary%'
                            OR classification_text LIKE '%payroll%' THEN 'salary'
                       WHEN classification_text LIKE '%refund%'
                            OR classification_text LIKE '%reversal%' THEN 'refund'
                       ELSE 'other'
                   END
               ELSE
                   CASE
                       WHEN mcc = 5411 OR mcc BETWEEN 5811 AND 5814 THEN 'food'
                       WHEN mcc IN (4111, 4121, 4131, 4789, 5541, 5542, 7523) THEN 'transport'
                       WHEN mcc IN (4900, 4814, 4899) THEN 'utilities'
                       WHEN mcc = 6513 THEN 'housing'
                       WHEN mcc = 5912 OR mcc BETWEEN 8011 AND 8099 THEN 'health'
                       WHEN mcc IN (7832, 7922) OR mcc BETWEEN 7991 AND 7999 THEN 'entertainment'
                       WHEN mcc BETWEEN 3000 AND 3999 OR mcc IN (4511, 4722, 7011) THEN 'travel'
                       WHEN mcc BETWEEN 5000 AND 5999 THEN 'shopping'
                       ELSE 'other'
                   END
           END AS legacy_category
    FROM legacy_candidates
)
UPDATE transactions AS transaction
SET source_metadata = transaction.source_metadata || jsonb_build_object(
        'classification_override', true,
        'category_override', true,
        'category_source', 'user_override'
    ),
    updated_at = now()
FROM legacy_classified
WHERE transaction.id = legacy_classified.id
  AND (
      legacy_classified.explicit_user_override
      OR legacy_classified.current_category <> legacy_classified.legacy_category
  );

-- Backfill legacy open-banking transactions that still use the fallback
-- category. This is a SQL snapshot of the deterministic MCC and keyword rules
-- used by the sync service when this migration was introduced.
WITH candidates AS (
    SELECT id,
           type,
           lower(regexp_replace(description, '[[:space:]]+', ' ', 'g')) AS classification_text,
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
           END AS mcc
    FROM transactions
    WHERE source = 'open_banking'
      AND lower(btrim(category)) = 'other'
      AND NOT (
          source_metadata @> '{"classification_override":true}'::jsonb
          OR source_metadata @> '{"type_override":true}'::jsonb
          OR source_metadata @> '{"category_override":true}'::jsonb
      )
), mcc_categories AS (
    SELECT candidates.*,
           CASE
               WHEN mcc IN (5411, 5422, 5441, 5451, 5462, 5499)
                    OR mcc BETWEEN 5811 AND 5814 THEN 'food'
               WHEN mcc IN (4011, 4111, 4112, 4121, 4131, 4789, 5541, 5542, 7523)
                    THEN 'transport'
               WHEN mcc IN (4900, 4814, 4899) THEN 'utilities'
               WHEN mcc = 6513 THEN 'housing'
               WHEN mcc = 5912 OR mcc BETWEEN 8011 AND 8099 THEN 'health'
               WHEN mcc IN (7832, 7922) OR mcc BETWEEN 7991 AND 7999 THEN 'entertainment'
               WHEN mcc BETWEEN 3000 AND 3999 OR mcc IN (4511, 4722, 7011) THEN 'travel'
               WHEN mcc IN (8211, 8220, 8241, 8244, 8249, 8299) THEN 'education'
               WHEN mcc BETWEEN 5000 AND 5999 THEN 'shopping'
           END AS mcc_category
    FROM candidates
), keyword_categories AS (
    SELECT mcc_categories.*,
           CASE
               WHEN type = 'income' THEN
                   CASE
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%salary%', '%payroll%', '%monthly wage%', '%wages%'
                       ]) THEN 'salary'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%freelance%', '%contractor payment%', '%client invoice%'
                       ]) THEN 'freelance'
                       WHEN classification_text LIKE '%gift%' THEN 'gift'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%dividend%', '%interest payment%', '%investment return%'
                       ]) THEN 'investment'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%refund%', '%reversal%', '%cashback%', '%chargeback%', '%reimbursement%'
                       ]) THEN 'refund'
                   END
               ELSE
                   CASE
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%lidl%', '%kaufland%', '%billa%', '%fantastico%', '%t-market%',
                           '%supermarket%', '%grocery%', '%restaurant%', '%cafe%', '%coffee%',
                           '%bakery%', '%takeaway%', '%glovo%', '%wolt%', '%deliveroo%',
                           '%mcdonald%', '%kfc%'
                       ]) THEN 'food'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%uber%', '%bolt%', '%taxi%', '%metro%', '%subway%', '%bus ticket%',
                           '%tram%', '%railway%', '%train ticket%', '%parking%', '%fuel%',
                           '%petrol%', '%gas station%', '%shell%', '%omv%'
                       ]) THEN 'transport'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%rent payment%', '%monthly rent%', '%landlord%', '%mortgage%'
                       ]) THEN 'housing'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%electricity%', '%electric bill%', '%water bill%', '%heating%',
                           '%utility%', '%internet bill%', '%broadband%', '%mobile bill%',
                           '%phone bill%', '%telecom%', '%vivacom%', '%yettel%'
                       ]) THEN 'utilities'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%pharmacy%', '%apteka%', '%drugstore%', '%hospital%', '%clinic%',
                           '%doctor%', '%dentist%', '%dental%', '%medical%'
                       ]) THEN 'health'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%netflix%', '%spotify%', '%cinema%', '%movie theatre%',
                           '%movie theater%', '%concert%', '%steam games%', '%playstation%', '%xbox%'
                       ]) THEN 'entertainment'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%airbnb%', '%booking.com%', '%hotel%', '%hostel%', '%airline%',
                           '%flight%', '%ryanair%', '%wizz air%', '%easyjet%'
                       ]) THEN 'travel'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%university%', '%school fee%', '%tuition%', '%online course%',
                           '%udemy%', '%coursera%'
                       ]) THEN 'education'
                       WHEN classification_text LIKE ANY (ARRAY[
                           '%amazon%', '%ebay%', '%etsy%', '%shopping mall%', '%retail%',
                           '%clothing%', '%fashion%', '%zara%', '%ikea%', '%dm drogerie%'
                       ]) THEN 'shopping'
                   END
           END AS keyword_category
    FROM mcc_categories
), classified AS (
    SELECT id,
           type,
           CASE
               WHEN type = 'income' THEN keyword_category
               ELSE COALESCE(mcc_category, keyword_category)
           END AS classified_category,
           CASE
               WHEN type = 'income' AND keyword_category IS NOT NULL THEN 'income_keyword'
               WHEN type <> 'income' AND mcc_category IS NOT NULL THEN 'mcc'
               WHEN type <> 'income' AND keyword_category IS NOT NULL THEN 'expense_keyword'
           END AS classification_source
    FROM keyword_categories
)
UPDATE transactions AS transaction
SET category = classified.classified_category,
    source_metadata = transaction.source_metadata || jsonb_build_object(
        'classification_source', classified.classification_source,
        'classified_category', classified.classified_category,
        'classified_type', classified.type,
        'category_source', classified.classification_source
    ),
    updated_at = now()
FROM classified
WHERE transaction.id = classified.id
  AND classified.classified_category IS NOT NULL;
