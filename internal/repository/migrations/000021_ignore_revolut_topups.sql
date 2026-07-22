INSERT INTO open_banking_transaction_suppressions(user_id,source_account_id,external_id)
SELECT transactions.user_id,transactions.source_account_id,transactions.external_id
FROM transactions
JOIN open_banking_accounts
  ON open_banking_accounts.id = transactions.source_account_id
JOIN open_banking_connections
  ON open_banking_connections.id = open_banking_accounts.connection_id
WHERE transactions.source = 'open_banking'
  AND transactions.source_account_id IS NOT NULL
  AND transactions.external_id IS NOT NULL
  AND lower(open_banking_connections.institution_name) LIKE '%revolut%'
  AND regexp_replace(
      upper(COALESCE(transactions.source_metadata->>'bank_transaction_code','')),
      '[^A-Z0-9]', '', 'g'
  ) IN ('TOPUP','CARDTOPUP','TOPUPRETURN','CARDTOPUPRETURN')
ON CONFLICT(user_id,external_id)
DO UPDATE SET source_account_id=EXCLUDED.source_account_id,deleted_at=now();

DELETE FROM transactions
USING open_banking_accounts,open_banking_connections
WHERE transactions.source = 'open_banking'
  AND transactions.source_account_id = open_banking_accounts.id
  AND open_banking_accounts.connection_id = open_banking_connections.id
  AND lower(open_banking_connections.institution_name) LIKE '%revolut%'
  AND regexp_replace(
      upper(COALESCE(transactions.source_metadata->>'bank_transaction_code','')),
      '[^A-Z0-9]', '', 'g'
  ) IN ('TOPUP','CARDTOPUP','TOPUPRETURN','CARDTOPUPRETURN');
