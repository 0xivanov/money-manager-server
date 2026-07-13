package repository

import (
	"context"
	"fmt"
	"strings"

	"money-manager-server/internal/model"

	"github.com/jackc/pgx/v5"
)

type UserWithPassword struct {
	User         model.User
	PasswordHash string
}

func (r *Repository) RegisterUser(ctx context.Context, email, passwordHash string) (model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return model.User{}, fmt.Errorf("begin user registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var user model.User
	err = tx.QueryRow(ctx,
		"INSERT INTO users(email,password_hash) VALUES($1,$2) RETURNING id,email",
		email, passwordHash,
	).Scan(&user.ID, &user.Email)
	if err != nil {
		return model.User{}, mapConflict(err)
	}
	if err := seedDefaultCategories(ctx, tx, user.ID); err != nil {
		return model.User{}, fmt.Errorf("seed default categories: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, fmt.Errorf("commit user registration: %w", err)
	}
	return user, nil
}

func (r *Repository) FindUserByEmail(ctx context.Context, email string) (UserWithPassword, error) {
	var record UserWithPassword
	err := r.db.QueryRow(ctx,
		"SELECT id,email,password_hash FROM users WHERE lower(email)=lower($1)",
		email,
	).Scan(&record.User.ID, &record.User.Email, &record.PasswordHash)
	return record, mapNotFound(err)
}

func (r *Repository) GetUser(ctx context.Context, userID int) (model.User, error) {
	var user model.User
	err := r.db.QueryRow(ctx, "SELECT id,email FROM users WHERE id=$1", userID).Scan(&user.ID, &user.Email)
	return user, mapNotFound(err)
}

func (r *Repository) DeleteUser(ctx context.Context, userID int) error {
	tag, err := r.db.Exec(ctx, "DELETE FROM users WHERE id=$1", userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) EnsureDefaultCategories(ctx context.Context, userID int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin category seed: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := seedDefaultCategories(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func seedDefaultCategories(ctx context.Context, tx pgx.Tx, userID int) error {
	for transactionType, names := range defaultCategories {
		for sortOrder, name := range names {
			if _, err := tx.Exec(ctx, `INSERT INTO categories(user_id,type,name,is_default,active,sort_order)
				VALUES($1,$2,$3,true,true,$4) ON CONFLICT DO NOTHING`,
				userID, transactionType, name, sortOrder,
			); err != nil {
				return err
			}
		}
	}
	return nil
}
