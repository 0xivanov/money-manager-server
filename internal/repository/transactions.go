package repository

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"money-manager-server/internal/model"

	"github.com/jackc/pgx/v5/pgtype"
)

type TransactionFilter struct {
	From     time.Time
	To       time.Time
	Type     string
	Category string
}

func (r *Repository) ListTransactions(ctx context.Context, userID int, filter TransactionFilter) ([]model.Transaction, error) {
	query := `SELECT id,type,category,description,amount::text,currency,to_char(occurred_at,'YYYY-MM-DD'),
        source,status,excluded_from_budget,schedule_occurrence_id
        FROM transactions WHERE user_id=$1 AND occurred_at >= $2 AND occurred_at < $3`
	args := []any{userID, filter.From, filter.To}
	if filter.Type != "" {
		query += fmt.Sprintf(" AND type=$%d", len(args)+1)
		args = append(args, filter.Type)
	}
	if filter.Category != "" {
		query += fmt.Sprintf(" AND lower(category)=lower($%d)", len(args)+1)
		args = append(args, filter.Category)
	}
	query += " ORDER BY occurred_at DESC,id DESC"

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.Transaction, 0)
	for rows.Next() {
		transaction, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, transaction)
	}
	return out, rows.Err()
}

func (r *Repository) ExportTransactions(ctx context.Context, userID int, from, toExclusive time.Time, limit int) ([]model.Transaction, error) {
	rows, err := r.db.Query(ctx, `SELECT id,type,category,description,amount::text,currency,to_char(occurred_at,'YYYY-MM-DD'),
        source,status,excluded_from_budget,schedule_occurrence_id
        FROM transactions WHERE user_id=$1 AND occurred_at >= $2 AND occurred_at < $3
		ORDER BY occurred_at ASC,id ASC LIMIT $4`, userID, from, toExclusive, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.Transaction, 0)
	for rows.Next() {
		transaction, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, transaction)
	}
	return out, rows.Err()
}

func (r *Repository) CreateTransaction(ctx context.Context, userID int, request model.TransactionRequest) (model.Transaction, error) {
	row := r.db.QueryRow(ctx, `INSERT INTO transactions(
        user_id,type,category,description,amount,currency,occurred_at,source,status,excluded_from_budget
    ) VALUES($1,$2,$3,$4,$5,$6,$7,'manual','booked',$8)
        RETURNING id,type,category,description,amount::text,currency,to_char(occurred_at,'YYYY-MM-DD'),
            source,status,excluded_from_budget,schedule_occurrence_id`,
		userID, request.Type, request.Category, request.Description, request.Amount, request.Currency,
		request.OccurredAt, request.ExcludedFromBudget)
	return scanTransaction(row)
}

func (r *Repository) ImportTransactions(ctx context.Context, userID int, transactions []model.ImportedTransaction) (int, int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	imported, skipped := 0, 0
	for _, transaction := range transactions {
		request := transaction.Request
		tag, err := tx.Exec(ctx, `INSERT INTO transactions(
            user_id,type,category,description,amount,currency,occurred_at,import_source,import_fingerprint,source,status
        ) VALUES($1,$2,$3,$4,$5,$6,$7,'revolut',$8,'import','booked')
		ON CONFLICT (user_id,import_source,import_fingerprint)
		WHERE import_source IS NOT NULL AND import_fingerprint IS NOT NULL DO NOTHING`,
			userID, request.Type, request.Category, request.Description, request.Amount,
			request.Currency, request.OccurredAt, transaction.Fingerprint)
		if err != nil {
			return 0, 0, err
		}
		if tag.RowsAffected() == 1 {
			imported++
		} else {
			skipped++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return imported, skipped, nil
}

func (r *Repository) GetTransaction(ctx context.Context, userID, transactionID int) (model.Transaction, error) {
	row := r.db.QueryRow(ctx, `SELECT id,type,category,description,amount::text,currency,to_char(occurred_at,'YYYY-MM-DD'),
        source,status,excluded_from_budget,schedule_occurrence_id
        FROM transactions WHERE id=$1 AND user_id=$2`, transactionID, userID)
	transaction, err := scanTransaction(row)
	return transaction, mapNotFound(err)
}

func (r *Repository) UpdateTransaction(ctx context.Context, userID, transactionID int, request model.TransactionRequest) (model.Transaction, error) {
	row := r.db.QueryRow(ctx, `UPDATE transactions
        SET type=$1,category=$2,description=$3,amount=$4,currency=$5,occurred_at=$6,
            excluded_from_budget=$7,updated_at=now()
        WHERE id=$8 AND user_id=$9
        RETURNING id,type,category,description,amount::text,currency,to_char(occurred_at,'YYYY-MM-DD'),
            source,status,excluded_from_budget,schedule_occurrence_id`,
		request.Type, request.Category, request.Description, request.Amount, request.Currency,
		request.OccurredAt, request.ExcludedFromBudget, transactionID, userID)
	transaction, err := scanTransaction(row)
	return transaction, mapNotFound(err)
}

func (r *Repository) DeleteTransaction(ctx context.Context, userID, transactionID int) error {
	tag, err := r.db.Exec(ctx, "DELETE FROM transactions WHERE id=$1 AND user_id=$2", transactionID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) Summary(ctx context.Context, userID int, month string, from, to time.Time) (model.Summary, error) {
	summary := model.Summary{Month: month, Currency: "EUR"}
	var rawIncome, rawExpense string
	err := r.db.QueryRow(ctx, `SELECT
		COALESCE(SUM(amount) FILTER (WHERE type='income'),0)::text,
		COALESCE(SUM(amount) FILTER (WHERE type='expense'),0)::text,
		COUNT(*)
        FROM transactions WHERE user_id=$1 AND occurred_at >= $2 AND occurred_at < $3 AND status='booked'`,
		userID, from, to,
	).Scan(&rawIncome, &rawExpense, &summary.TransactionCount)
	if err != nil {
		return model.Summary{}, err
	}
	if summary.Income, err = decimalWithTwoPlaces(rawIncome); err != nil {
		return model.Summary{}, fmt.Errorf("format income aggregate: %w", err)
	}
	if summary.Expense, err = decimalWithTwoPlaces(rawExpense); err != nil {
		return model.Summary{}, fmt.Errorf("format expense aggregate: %w", err)
	}
	if summary.Balance, err = calculateBalance(summary.Income, summary.Expense); err != nil {
		return model.Summary{}, err
	}
	return summary, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTransaction(row rowScanner) (model.Transaction, error) {
	var transaction model.Transaction
	var scheduleOccurrenceID pgtype.Int8
	err := row.Scan(
		&transaction.ID,
		&transaction.Type,
		&transaction.Category,
		&transaction.Description,
		&transaction.Amount,
		&transaction.Currency,
		&transaction.OccurredAt,
		&transaction.Source,
		&transaction.Status,
		&transaction.ExcludedFromBudget,
		&scheduleOccurrenceID,
	)
	if scheduleOccurrenceID.Valid {
		value := int(scheduleOccurrenceID.Int64)
		transaction.ScheduleOccurrenceID = &value
	}
	return transaction, err
}

func calculateBalance(incomeString, expenseString string) (string, error) {
	income, ok := new(big.Rat).SetString(incomeString)
	if !ok {
		return "", fmt.Errorf("invalid income decimal %q", incomeString)
	}
	expense, ok := new(big.Rat).SetString(expenseString)
	if !ok {
		return "", fmt.Errorf("invalid expense decimal %q", expenseString)
	}
	return new(big.Rat).Sub(income, expense).FloatString(2), nil
}

func decimalWithTwoPlaces(value string) (string, error) {
	decimal, ok := new(big.Rat).SetString(value)
	if !ok {
		return "", fmt.Errorf("invalid decimal %q", value)
	}
	return decimal.FloatString(2), nil
}
