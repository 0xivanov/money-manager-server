package repository

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"money-manager-server/internal/model"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) { return pgxpool.New(ctx, url) }
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS users(id SERIAL PRIMARY KEY,email TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS transactions(id SERIAL PRIMARY KEY,user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,type TEXT NOT NULL,category TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',amount NUMERIC(14,2) NOT NULL,currency TEXT NOT NULL,occurred_at DATE NOT NULL);
CREATE TABLE IF NOT EXISTS categories(id SERIAL PRIMARY KEY,user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,type TEXT NOT NULL,name TEXT NOT NULL,is_default BOOLEAN NOT NULL DEFAULT false,active BOOLEAN NOT NULL DEFAULT true,sort_order INT NOT NULL DEFAULT 1000);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 1000;
CREATE UNIQUE INDEX IF NOT EXISTS categories_user_type_name_active_idx ON categories(user_id,type,lower(name)) WHERE active;`)
	return err
}

func (r *Repository) CreateUser(ctx context.Context, email, hash string) (int, error) {
	var id int
	err := r.db.QueryRow(ctx, "INSERT INTO users(email,password_hash) VALUES($1,$2) RETURNING id", email, hash).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, errors.New("email is already registered")
	}
	return id, err
}
func (r *Repository) FindUser(ctx context.Context, email string) (int, string, error) {
	var id int
	var h string
	err := r.db.QueryRow(ctx, "SELECT id,password_hash FROM users WHERE email=$1", email).Scan(&id, &h)
	return id, h, err
}

func (r *Repository) EnsureDefaultCategories(ctx context.Context, userID int) error {
	for typ, categories := range defaultCategories {
		for index, name := range categories {
			_, err := r.db.Exec(ctx, `INSERT INTO categories(user_id,type,name,is_default,active,sort_order)
SELECT $1,$2,$3,true,true,$4
WHERE NOT EXISTS (
	SELECT 1 FROM categories WHERE user_id=$1 AND type=$2 AND lower(name)=lower($3) AND active
)`, userID, typ, name, index)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repository) ListCategories(ctx context.Context, userID int, typ string) ([]model.Category, error) {
	rows, err := r.db.Query(ctx, `SELECT id,type,name,is_default FROM categories WHERE user_id=$1 AND type=$2 AND active ORDER BY sort_order ASC,name ASC`, userID, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Category{}
	for rows.Next() {
		var c model.Category
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.IsDefault); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repository) CreateCategory(ctx context.Context, userID int, req model.CategoryRequest) (model.Category, error) {
	var c model.Category
	err := r.db.QueryRow(ctx, `INSERT INTO categories(user_id,type,name,is_default,active) VALUES($1,$2,$3,false,true) RETURNING id,type,name,is_default`, userID, req.Type, req.Name).Scan(&c.ID, &c.Type, &c.Name, &c.IsDefault)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return model.Category{}, errors.New("category already exists")
	}
	return c, err
}

func (r *Repository) DeleteCategory(ctx context.Context, userID, id int) error {
	tag, err := r.db.Exec(ctx, "UPDATE categories SET active=false WHERE id=$1 AND user_id=$2 AND is_default=false AND active", id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("custom category not found")
	}
	return nil
}

func (r *Repository) ListTransactions(ctx context.Context, userID int, month, typ, category string) ([]model.Transaction, error) {
	q := `SELECT id,type,category,description,to_char(amount,'FM999999999990.00'),currency,to_char(occurred_at,'YYYY-MM-DD') FROM transactions WHERE user_id=$1 AND to_char(occurred_at,'YYYY-MM')=$2`
	args := []any{userID, month}
	if typ != "" {
		q += fmt.Sprintf(" AND type=$%d", len(args)+1)
		args = append(args, typ)
	}
	if category != "" {
		q += fmt.Sprintf(" AND category=$%d", len(args)+1)
		args = append(args, category)
	}
	q += " ORDER BY occurred_at DESC,id DESC"
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Transaction{}
	for rows.Next() {
		var t model.Transaction
		if err := rows.Scan(&t.ID, &t.Type, &t.Category, &t.Description, &t.Amount, &t.Currency, &t.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) ExportTransactions(ctx context.Context, userID int, from, to string) ([]model.Transaction, error) {
	rows, err := r.db.Query(ctx, `SELECT id,type,category,description,to_char(amount,'FM999999999990.00'),currency,to_char(occurred_at,'YYYY-MM-DD') FROM transactions WHERE user_id=$1 AND occurred_at >= $2::date AND occurred_at <= $3::date ORDER BY occurred_at ASC,id ASC`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Transaction{}
	for rows.Next() {
		var t model.Transaction
		if err := rows.Scan(&t.ID, &t.Type, &t.Category, &t.Description, &t.Amount, &t.Currency, &t.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *Repository) CreateTransaction(ctx context.Context, userID int, tr model.TransactionRequest) (model.Transaction, error) {
	var t model.Transaction
	err := r.db.QueryRow(ctx, `INSERT INTO transactions(user_id,type,category,description,amount,currency,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,type,category,description,to_char(amount,'FM999999999990.00'),currency,to_char(occurred_at,'YYYY-MM-DD')`, userID, tr.Type, tr.Category, tr.Description, tr.Amount, strings.ToUpper(tr.Currency), tr.OccurredAt).Scan(&t.ID, &t.Type, &t.Category, &t.Description, &t.Amount, &t.Currency, &t.OccurredAt)
	return t, err
}
func (r *Repository) UpdateTransaction(ctx context.Context, userID, id int, tr model.TransactionRequest) (model.Transaction, error) {
	var t model.Transaction
	err := r.db.QueryRow(ctx, `UPDATE transactions SET type=$1,category=$2,description=$3,amount=$4,currency=$5,occurred_at=$6 WHERE id=$7 AND user_id=$8 RETURNING id,type,category,description,to_char(amount,'FM999999999990.00'),currency,to_char(occurred_at,'YYYY-MM-DD')`, tr.Type, tr.Category, tr.Description, tr.Amount, strings.ToUpper(tr.Currency), tr.OccurredAt, id, userID).Scan(&t.ID, &t.Type, &t.Category, &t.Description, &t.Amount, &t.Currency, &t.OccurredAt)
	return t, err
}
func (r *Repository) DeleteTransaction(ctx context.Context, userID, id int) error {
	_, err := r.db.Exec(ctx, "DELETE FROM transactions WHERE id=$1 AND user_id=$2", id, userID)
	return err
}
func (r *Repository) Summary(ctx context.Context, userID int, month string) (model.Summary, error) {
	var s model.Summary
	s.Month = month
	s.Currency = "EUR"
	err := r.db.QueryRow(ctx, `SELECT to_char(COALESCE(SUM(CASE WHEN type='income' THEN amount ELSE 0 END),0),'FM999999999990.00'),to_char(COALESCE(SUM(CASE WHEN type='expense' THEN amount ELSE 0 END),0),'FM999999999990.00'),COUNT(*) FROM transactions WHERE user_id=$1 AND to_char(occurred_at,'YYYY-MM')=$2`, userID, month).Scan(&s.Income, &s.Expense, &s.TransactionCount)
	s.Balance = calcBalance(s.Income, s.Expense)
	return s, err
}
func calcBalance(i, e string) string {
	income, ok := new(big.Rat).SetString(i)
	if !ok {
		income = new(big.Rat)
	}
	expense, ok := new(big.Rat).SetString(e)
	if !ok {
		expense = new(big.Rat)
	}
	return new(big.Rat).Sub(income, expense).FloatString(2)
}

var defaultCategories = map[string][]string{
	"expense": {
		"food",
		"transport",
		"housing",
		"utilities",
		"health",
		"entertainment",
		"shopping",
		"travel",
		"education",
		"other",
	},
	"income": {
		"salary",
		"freelance",
		"gift",
		"investment",
		"refund",
		"other",
	},
}

func DefaultCategoriesForType(typ string) []string {
	out := append([]string(nil), defaultCategories[typ]...)
	sort.Strings(out)
	return out
}
