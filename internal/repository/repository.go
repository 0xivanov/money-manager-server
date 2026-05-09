package repository

import (
	"context"
	"fmt"
	"money-manager-server/internal/model"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ db *pgxpool.Pool }
func New(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) { return pgxpool.New(ctx, url) }
func Migrate(ctx context.Context, db *pgxpool.Pool) error { _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS users(id SERIAL PRIMARY KEY,email TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS transactions(id SERIAL PRIMARY KEY,user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,type TEXT NOT NULL,category TEXT NOT NULL,amount NUMERIC(14,2) NOT NULL,currency TEXT NOT NULL,occurred_at DATE NOT NULL);`); return err }

func (r *Repository) CreateUser(ctx context.Context, email, hash string) (int, error) { var id int; err := r.db.QueryRow(ctx, "INSERT INTO users(email,password_hash) VALUES($1,$2) RETURNING id", email, hash).Scan(&id); return id, err }
func (r *Repository) FindUser(ctx context.Context, email string) (int, string, error) { var id int; var h string; err := r.db.QueryRow(ctx, "SELECT id,password_hash FROM users WHERE email=$1", email).Scan(&id, &h); return id, h, err }

func (r *Repository) ListTransactions(ctx context.Context, userID int, month, typ, category string) ([]model.Transaction, error) {
	q := `SELECT id,type,category,to_char(amount,'FM999999999990.00'),currency,to_char(occurred_at,'YYYY-MM-DD') FROM transactions WHERE user_id=$1 AND to_char(occurred_at,'YYYY-MM')=$2`
	args := []any{userID, month}
	if typ != "" { q += fmt.Sprintf(" AND type=$%d", len(args)+1); args = append(args, typ) }
	if category != "" { q += fmt.Sprintf(" AND category=$%d", len(args)+1); args = append(args, category) }
	q += " ORDER BY occurred_at DESC,id DESC"
	rows, err := r.db.Query(ctx, q, args...); if err != nil { return nil, err }
	defer rows.Close()
	var out []model.Transaction
	for rows.Next() { var t model.Transaction; if err := rows.Scan(&t.ID, &t.Type, &t.Category, &t.Amount, &t.Currency, &t.OccurredAt); err != nil { return nil, err }; out = append(out, t) }
	return out, rows.Err()
}
func (r *Repository) CreateTransaction(ctx context.Context, userID int, tr model.TransactionRequest) (model.Transaction, error) { var t model.Transaction; err := r.db.QueryRow(ctx, `INSERT INTO transactions(user_id,type,category,amount,currency,occurred_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id,type,category,to_char(amount,'FM999999999990.00'),currency,to_char(occurred_at,'YYYY-MM-DD')`, userID, tr.Type, tr.Category, tr.Amount, strings.ToUpper(tr.Currency), tr.OccurredAt).Scan(&t.ID, &t.Type, &t.Category, &t.Amount, &t.Currency, &t.OccurredAt); return t, err }
func (r *Repository) UpdateTransaction(ctx context.Context, userID, id int, tr model.TransactionRequest) (model.Transaction, error) { var t model.Transaction; err := r.db.QueryRow(ctx, `UPDATE transactions SET type=$1,category=$2,amount=$3,currency=$4,occurred_at=$5 WHERE id=$6 AND user_id=$7 RETURNING id,type,category,to_char(amount,'FM999999999990.00'),currency,to_char(occurred_at,'YYYY-MM-DD')`, tr.Type, tr.Category, tr.Amount, strings.ToUpper(tr.Currency), tr.OccurredAt, id, userID).Scan(&t.ID, &t.Type, &t.Category, &t.Amount, &t.Currency, &t.OccurredAt); return t, err }
func (r *Repository) DeleteTransaction(ctx context.Context, userID, id int) error { _, err := r.db.Exec(ctx, "DELETE FROM transactions WHERE id=$1 AND user_id=$2", id, userID); return err }
func (r *Repository) Summary(ctx context.Context, userID int, month string) (model.Summary, error) { var s model.Summary; s.Month = month; s.Currency = "EUR"; err := r.db.QueryRow(ctx, `SELECT to_char(COALESCE(SUM(CASE WHEN type='income' THEN amount ELSE 0 END),0),'FM999999999990.00'),to_char(COALESCE(SUM(CASE WHEN type='expense' THEN amount ELSE 0 END),0),'FM999999999990.00'),COUNT(*) FROM transactions WHERE user_id=$1 AND to_char(occurred_at,'YYYY-MM')=$2`, userID, month).Scan(&s.Income, &s.Expense, &s.TransactionCount); s.Balance = calcBalance(s.Income, s.Expense); return s, err }
func calcBalance(i,e string) string { return i }
