package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"money-manager-server/internal/model"
)

type OpenBankingAuthorizationRecord struct {
	ID              int
	UserID          int
	InstitutionName string
	Country         string
	PSUType         string
	ValidUntil      time.Time
}

type NewOpenBankingAuthorization struct {
	UserID          int
	StateHash       string
	InstitutionName string
	Country         string
	PSUType         string
	ValidUntil      time.Time
	ExpiresAt       time.Time
}

type NewOpenBankingConnection struct {
	AuthorizationID int
	UserID          int
	ProviderSession string
	InstitutionName string
	Country         string
	PSUType         string
	Status          string
	ValidUntil      time.Time
	Accounts        []NewOpenBankingAccount
}

type NewOpenBankingAccount struct {
	ProviderAccountID  string
	IdentificationHash string
	Name               string
	Details            string
	CashAccountType    string
	Product            string
	Currency           string
	DisplayIdentifier  string
	ProviderPayload    json.RawMessage
}

type OpenBankingConnectionRecord struct {
	Connection      model.OpenBankingConnection
	ProviderSession string
}

type OpenBankingAccountRecord struct {
	Account           model.OpenBankingAccount
	ProviderAccountID string
	ProviderPayload   json.RawMessage
}

func (r *Repository) ListOpenBankingProviderSessions(ctx context.Context, userID int) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT provider_session_id FROM open_banking_connections
		WHERE user_id=$1 ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		sessions = append(sessions, sessionID)
	}
	return sessions, rows.Err()
}

func (r *Repository) CreateOpenBankingAuthorization(ctx context.Context, record NewOpenBankingAuthorization) (int, error) {
	var id int
	err := r.db.QueryRow(ctx, `INSERT INTO open_banking_authorizations(
		user_id,state_hash,institution_name,country,psu_type,valid_until,expires_at
	) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		record.UserID, record.StateHash, record.InstitutionName, record.Country,
		record.PSUType, record.ValidUntil, record.ExpiresAt,
	).Scan(&id)
	return id, mapConflict(err)
}

func (r *Repository) SetOpenBankingAuthorizationProviderID(ctx context.Context, authorizationID int, providerID string) error {
	tag, err := r.db.Exec(ctx, `UPDATE open_banking_authorizations
		SET authorization_id=$1,updated_at=now() WHERE id=$2 AND status='pending'`, providerID, authorizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ClaimOpenBankingAuthorization(ctx context.Context, stateHash string, now time.Time) (OpenBankingAuthorizationRecord, error) {
	var record OpenBankingAuthorizationRecord
	err := r.db.QueryRow(ctx, `UPDATE open_banking_authorizations
		SET status='processing',consumed_at=$2,updated_at=$2
		WHERE state_hash=$1 AND status='pending' AND consumed_at IS NULL AND expires_at > $2
		RETURNING id,user_id,institution_name,country,psu_type,valid_until`, stateHash, now,
	).Scan(&record.ID, &record.UserID, &record.InstitutionName, &record.Country, &record.PSUType, &record.ValidUntil)
	return record, mapNotFound(err)
}

func (r *Repository) FailOpenBankingAuthorization(ctx context.Context, authorizationID int, code, description string) error {
	tag, err := r.db.Exec(ctx, `UPDATE open_banking_authorizations
		SET status='failed',error_code=$1,error_description=$2,updated_at=now()
		WHERE id=$3 AND status IN ('pending','processing')`, code, description, authorizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) StoreOpenBankingConnection(ctx context.Context, record NewOpenBankingConnection) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin open banking connection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var connectionID int
	err = tx.QueryRow(ctx, `INSERT INTO open_banking_connections(
		user_id,provider_session_id,institution_name,country,psu_type,status,valid_until
	) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		record.UserID, record.ProviderSession, record.InstitutionName, record.Country,
		record.PSUType, record.Status, record.ValidUntil,
	).Scan(&connectionID)
	if err != nil {
		return 0, mapConflict(err)
	}
	for _, account := range record.Accounts {
		payload := account.ProviderPayload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		_, err := tx.Exec(ctx, `INSERT INTO open_banking_accounts(
			connection_id,provider_account_id,identification_hash,name,details,cash_account_type,
			product,currency,display_identifier,provider_payload
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			connectionID, nullableString(account.ProviderAccountID), account.IdentificationHash,
			account.Name, account.Details, account.CashAccountType, account.Product,
			account.Currency, account.DisplayIdentifier, payload,
		)
		if err != nil {
			return 0, fmt.Errorf("store open banking account: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE open_banking_authorizations
		SET status='completed',connection_id=$1,updated_at=now()
		WHERE id=$2 AND user_id=$3 AND status='processing'`, connectionID, record.AuthorizationID, record.UserID)
	if err != nil {
		return 0, fmt.Errorf("complete open banking authorization: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit open banking connection: %w", err)
	}
	return connectionID, nil
}

func (r *Repository) ListOpenBankingConnections(ctx context.Context, userID int) ([]model.OpenBankingConnection, error) {
	rows, err := r.db.Query(ctx, `SELECT c.id,c.institution_name,c.country,c.psu_type,c.status,
		to_char(c.valid_until AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),count(a.id),
		to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(c.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM open_banking_connections c
		LEFT JOIN open_banking_accounts a ON a.connection_id=c.id
		WHERE c.user_id=$1
		GROUP BY c.id ORDER BY c.created_at DESC,c.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := make([]model.OpenBankingConnection, 0)
	for rows.Next() {
		var connection model.OpenBankingConnection
		if err := rows.Scan(
			&connection.ID, &connection.InstitutionName, &connection.Country, &connection.PSUType,
			&connection.Status, &connection.ValidUntil, &connection.AccountCount,
			&connection.CreatedAt, &connection.UpdatedAt,
		); err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

func (r *Repository) GetOpenBankingConnection(ctx context.Context, userID, connectionID int) (OpenBankingConnectionRecord, error) {
	var record OpenBankingConnectionRecord
	err := r.db.QueryRow(ctx, `SELECT c.id,c.institution_name,c.country,c.psu_type,c.status,
		to_char(c.valid_until AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		(SELECT count(*) FROM open_banking_accounts a WHERE a.connection_id=c.id),
		to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(c.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),c.provider_session_id
		FROM open_banking_connections c WHERE c.id=$1 AND c.user_id=$2`, connectionID, userID,
	).Scan(
		&record.Connection.ID, &record.Connection.InstitutionName, &record.Connection.Country,
		&record.Connection.PSUType, &record.Connection.Status, &record.Connection.ValidUntil,
		&record.Connection.AccountCount, &record.Connection.CreatedAt, &record.Connection.UpdatedAt,
		&record.ProviderSession,
	)
	return record, mapNotFound(err)
}

func (r *Repository) UpdateOpenBankingConnectionStatus(ctx context.Context, userID, connectionID int, status string) error {
	tag, err := r.db.Exec(ctx, `UPDATE open_banking_connections SET status=$1,updated_at=now()
		WHERE id=$2 AND user_id=$3`, status, connectionID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) DeleteOpenBankingConnection(ctx context.Context, userID, connectionID int) error {
	tag, err := r.db.Exec(ctx, "DELETE FROM open_banking_connections WHERE id=$1 AND user_id=$2", connectionID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ListOpenBankingAccounts(ctx context.Context, userID int) ([]model.OpenBankingAccount, error) {
	rows, err := r.db.Query(ctx, `SELECT a.id,a.connection_id,c.institution_name,c.country,a.name,a.details,
		a.cash_account_type,a.product,a.currency,a.display_identifier,a.identification_hash,
		(a.provider_account_id IS NOT NULL)
		FROM open_banking_accounts a
		JOIN open_banking_connections c ON c.id=a.connection_id
		WHERE c.user_id=$1 ORDER BY c.created_at DESC,a.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]model.OpenBankingAccount, 0)
	for rows.Next() {
		var account model.OpenBankingAccount
		if err := rows.Scan(
			&account.ID, &account.ConnectionID, &account.InstitutionName, &account.Country,
			&account.Name, &account.Details, &account.CashAccountType, &account.Product,
			&account.Currency, &account.DisplayIdentifier, &account.IdentificationHash,
			&account.CanFetchData,
		); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (r *Repository) GetOpenBankingAccount(ctx context.Context, userID, accountID int) (OpenBankingAccountRecord, error) {
	var record OpenBankingAccountRecord
	err := r.db.QueryRow(ctx, `SELECT a.id,a.connection_id,c.institution_name,c.country,a.name,a.details,
		a.cash_account_type,a.product,a.currency,a.display_identifier,a.identification_hash,
		(a.provider_account_id IS NOT NULL),COALESCE(a.provider_account_id,''),a.provider_payload
		FROM open_banking_accounts a
		JOIN open_banking_connections c ON c.id=a.connection_id
		WHERE a.id=$1 AND c.user_id=$2`, accountID, userID,
	).Scan(
		&record.Account.ID, &record.Account.ConnectionID, &record.Account.InstitutionName,
		&record.Account.Country, &record.Account.Name, &record.Account.Details,
		&record.Account.CashAccountType, &record.Account.Product, &record.Account.Currency,
		&record.Account.DisplayIdentifier, &record.Account.IdentificationHash,
		&record.Account.CanFetchData, &record.ProviderAccountID, &record.ProviderPayload,
	)
	return record, mapNotFound(err)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
