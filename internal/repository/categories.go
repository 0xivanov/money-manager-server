package repository

import (
	"context"

	"money-manager-server/internal/model"
)

var defaultCategories = map[string][]string{
	"expense": {"groceries", "dining_out", "going_out", "transport", "housing", "utilities", "health", "entertainment", "shopping", "travel", "education", "beauty", "other"},
	"income":  {"salary", "freelance", "gift", "investment", "refund", "other"},
}

func (r *Repository) ListCategories(ctx context.Context, userID int, transactionType string) ([]model.Category, error) {
	rows, err := r.db.Query(ctx, `SELECT id,type,name,is_default
		FROM categories WHERE user_id=$1 AND type=$2 AND active
		ORDER BY sort_order ASC,name ASC`, userID, transactionType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.Category, 0)
	for rows.Next() {
		var category model.Category
		if err := rows.Scan(&category.ID, &category.Type, &category.Name, &category.IsDefault); err != nil {
			return nil, err
		}
		out = append(out, category)
	}
	return out, rows.Err()
}

func (r *Repository) CreateCategory(ctx context.Context, userID int, request model.CategoryRequest) (model.Category, error) {
	var category model.Category
	err := r.db.QueryRow(ctx, `INSERT INTO categories(user_id,type,name,is_default,active,sort_order)
		SELECT $1,$2,$3,false,true,COALESCE(MAX(sort_order),999)+1
		FROM categories WHERE user_id=$1 AND type=$2
		RETURNING id,type,name,is_default`, userID, request.Type, request.Name,
	).Scan(&category.ID, &category.Type, &category.Name, &category.IsDefault)
	return category, mapConflict(err)
}

func (r *Repository) DeleteCategory(ctx context.Context, userID, categoryID int) error {
	tag, err := r.db.Exec(ctx,
		"UPDATE categories SET active=false,updated_at=now() WHERE id=$1 AND user_id=$2 AND is_default=false AND active",
		categoryID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) FindActiveCategoryName(ctx context.Context, userID int, transactionType, name string) (string, error) {
	var canonicalName string
	err := r.db.QueryRow(ctx, `SELECT name FROM categories
		WHERE user_id=$1 AND type=$2 AND lower(name)=lower($3) AND active`,
		userID, transactionType, name,
	).Scan(&canonicalName)
	return canonicalName, mapNotFound(err)
}
