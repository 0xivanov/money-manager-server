package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"money-manager-server/internal/model"

	"github.com/jackc/pgx/v5"
)

type OpenBankingTransactionSeed struct {
	ExternalID  string
	Type        string
	Category    string
	Description string
	Amount      string
	Currency    string
	OccurredAt  time.Time
	Metadata    json.RawMessage
}

func (r *Repository) ClaimOpenBankingAccountsForSync(
	ctx context.Context,
	now time.Time,
	nextAttempt time.Time,
	claimUntil time.Time,
	limit int,
) ([]OpenBankingSyncAccount, error) {
	rows, err := r.db.Query(ctx, `WITH due AS (
		SELECT a.id,c.user_id
		FROM open_banking_accounts a
		JOIN open_banking_connections c ON c.id=a.connection_id
		WHERE a.provider_account_id IS NOT NULL
		  AND a.next_sync_at <= $1
		  AND (a.sync_claimed_until IS NULL OR a.sync_claimed_until <= $1)
		  AND c.status IN ('AUTHORIZED','VALID','READY')
		  AND c.valid_until > $1
		ORDER BY a.next_sync_at,a.id
		FOR UPDATE OF a SKIP LOCKED
		LIMIT $4
	)
	UPDATE open_banking_accounts a
	SET next_sync_at=$2,sync_claimed_until=$3,updated_at=now()
	FROM due
	WHERE a.id=due.id
	RETURNING due.user_id,a.id`, now, nextAttempt, claimUntil, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]OpenBankingSyncAccount, 0)
	for rows.Next() {
		var account OpenBankingSyncAccount
		if err := rows.Scan(&account.UserID, &account.AccountID); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (r *Repository) ReleaseOpenBankingSyncClaim(ctx context.Context, accountID int) error {
	_, err := r.db.Exec(ctx, `UPDATE open_banking_accounts
		SET sync_claimed_until=NULL,updated_at=now() WHERE id=$1`, accountID)
	return err
}

func (r *Repository) ImportOpenBankingTransactions(
	ctx context.Context,
	userID int,
	accountID int,
	transactions []OpenBankingTransactionSeed,
	syncedAt time.Time,
) (model.OpenBankingSyncResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return model.OpenBankingSyncResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ownerID int
	var initialSync bool
	var institutionName, accountName string
	err = tx.QueryRow(ctx, `SELECT c.user_id,a.last_synced_at IS NULL,c.institution_name,a.name
		FROM open_banking_accounts a
		JOIN open_banking_connections c ON c.id=a.connection_id
		WHERE a.id=$1 FOR UPDATE`, accountID,
	).Scan(&ownerID, &initialSync, &institutionName, &accountName)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ownerID != userID) {
		return model.OpenBankingSyncResult{}, ErrNotFound
	}
	if err != nil {
		return model.OpenBankingSyncResult{}, err
	}

	result := model.OpenBankingSyncResult{}
	for _, item := range transactions {
		var suppressed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM open_banking_transaction_suppressions
			WHERE user_id=$1 AND external_id=$2
		)`, userID, item.ExternalID).Scan(&suppressed); err != nil {
			return model.OpenBankingSyncResult{}, err
		}
		if suppressed {
			continue
		}
		metadata := item.Metadata
		if len(metadata) == 0 {
			metadata = json.RawMessage(`{}`)
		}
		effectiveType := item.Type
		effectiveCategory := item.Category
		effectiveMetadata := metadata
		var transactionID int
		err = tx.QueryRow(ctx, `INSERT INTO transactions(
			user_id,type,category,description,amount,currency,occurred_at,source,status,
			source_account_id,external_id,source_metadata
		) VALUES($1,$2,$3,$4,$5,$6,$7,'open_banking','booked',$8,$9,$10)
		ON CONFLICT(user_id,source_account_id,external_id)
		WHERE source='open_banking' AND source_account_id IS NOT NULL AND external_id IS NOT NULL
		DO NOTHING RETURNING id`, userID, effectiveType, effectiveCategory, item.Description,
			item.Amount, item.Currency, item.OccurredAt, accountID, item.ExternalID, effectiveMetadata,
		).Scan(&transactionID)
		inserted := err == nil
		if errors.Is(err, pgx.ErrNoRows) {
			var currentType, currentCategory, currentDescription string
			var classificationOverride, typeOverride, categoryOverride bool
			err = tx.QueryRow(ctx, `SELECT id,type,category,description,
					source_metadata @> '{"classification_override":true}'::jsonb,
					source_metadata @> '{"type_override":true}'::jsonb,
					source_metadata @> '{"category_override":true}'::jsonb
				FROM transactions
				WHERE user_id=$1 AND source='open_banking' AND source_account_id=$2 AND external_id=$3
				FOR UPDATE`, userID, accountID, item.ExternalID,
			).Scan(
				&transactionID, &currentType, &currentCategory,
				&currentDescription, &classificationOverride, &typeOverride, &categoryOverride,
			)
			if err != nil {
				return model.OpenBankingSyncResult{}, err
			}
			classificationOverride = classificationOverride || typeOverride || categoryOverride
			if classificationOverride {
				effectiveType = currentType
				effectiveCategory = currentCategory
				effectiveMetadata, err = openBankingOverrideMetadata(metadata, typeOverride, categoryOverride)
				if err != nil {
					return model.OpenBankingSyncResult{}, err
				}
			}
			effectiveDescription := preserveUserClarification(item.Description, currentDescription)
			tag, updateErr := tx.Exec(ctx, `UPDATE transactions SET
				type=$1,category=$2,description=$3,amount=$4,currency=$5,occurred_at=$6,
				status='booked',source_metadata=$7,updated_at=now()
				WHERE id=$8 AND (type,category,description,amount,currency,occurred_at,source_metadata)
				IS DISTINCT FROM ($1,$2,$3,$4::numeric,$5,$6::date,$7::jsonb)`,
				effectiveType, effectiveCategory, effectiveDescription, item.Amount, item.Currency,
				item.OccurredAt, effectiveMetadata, transactionID)
			if updateErr != nil {
				return model.OpenBankingSyncResult{}, updateErr
			}
			if tag.RowsAffected() > 0 {
				result.Updated++
			} else {
				result.Unchanged++
			}
		} else if err != nil {
			return model.OpenBankingSyncResult{}, err
		} else {
			result.Imported++
		}

		if initialSync || effectiveType != "expense" || !inserted {
			continue
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"transaction_id": transactionID,
			"account_id":     accountID,
			"amount":         item.Amount,
			"currency":       item.Currency,
			"merchant":       item.Description,
		})
		if marshalErr != nil {
			return model.OpenBankingSyncResult{}, marshalErr
		}
		sum := sha256.Sum256([]byte(item.ExternalID))
		eventKey := fmt.Sprintf("open-banking:%d:%s:transaction", accountID, hex.EncodeToString(sum[:]))
		title := "New bank spending"
		bodyAccount := accountName
		if bodyAccount == "" {
			bodyAccount = institutionName
		}
		body := fmt.Sprintf("%s · %s %s", item.Description, item.Amount, item.Currency)
		if bodyAccount != "" {
			body = fmt.Sprintf("%s · %s", body, bodyAccount)
		}
		tag, insertErr := tx.Exec(ctx, `INSERT INTO notification_outbox(
			user_id,event_type,event_key,title,body,payload
		)
		SELECT $1,'bank_spending',$2,$3,$4,$5
		WHERE COALESCE((SELECT bank_spending FROM notification_preferences WHERE user_id=$1),true)
		ON CONFLICT(event_key) DO NOTHING`, userID, eventKey, title, body, payload)
		if insertErr != nil {
			return model.OpenBankingSyncResult{}, insertErr
		}
		if tag.RowsAffected() > 0 {
			result.Notifications++
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE open_banking_accounts
		SET last_synced_at=$1::timestamptz,next_sync_at=$1::timestamptz+interval '6 hours',
			sync_claimed_until=NULL,updated_at=now()
		WHERE id=$2`, syncedAt, accountID); err != nil {
		return model.OpenBankingSyncResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.OpenBankingSyncResult{}, err
	}
	return result, nil
}

func preserveUserClarification(bankDescription, currentDescription string) string {
	const marker = "User clarification:"
	markerIndex := strings.Index(strings.ToLower(currentDescription), strings.ToLower(marker))
	if markerIndex < 0 {
		return bankDescription
	}
	note := strings.TrimSpace(currentDescription[markerIndex+len(marker):])
	if note == "" {
		return bankDescription
	}
	bankDescription = strings.TrimSpace(bankDescription)
	if bankDescription == "" {
		return marker + " " + note
	}
	return bankDescription + "\n" + marker + " " + note
}

func openBankingOverrideMetadata(metadata json.RawMessage, typeOverride, categoryOverride bool) (json.RawMessage, error) {
	values := make(map[string]any)
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &values); err != nil {
			return nil, fmt.Errorf("decode open banking source metadata: %w", err)
		}
	}
	if values == nil {
		values = make(map[string]any)
	}
	values["classification_override"] = true
	values["category_source"] = "user_override"
	if typeOverride {
		values["type_override"] = true
	}
	if categoryOverride {
		values["category_override"] = true
	}
	result, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode open banking source metadata: %w", err)
	}
	return result, nil
}
