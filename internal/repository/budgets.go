package repository

import (
	"context"
	"strconv"
	"time"

	"money-manager-server/internal/model"
)

const budgetSelect = `WITH selected AS (
	SELECT b.*,
		CASE b.period
			WHEN 'weekly' THEN date_trunc('week',$2::date)::date
			ELSE date_trunc('month',$2::date)::date
		END AS period_start
	FROM budgets b
	WHERE b.user_id=$1
), calculated AS (
	SELECT selected.*,
		CASE selected.period
			WHEN 'weekly' THEN selected.period_start + 6
			ELSE (selected.period_start + INTERVAL '1 month - 1 day')::date
		END AS period_end,
		COALESCE((
			SELECT sum(t.amount)
			FROM transactions t
			WHERE t.user_id=selected.user_id AND t.type='expense' AND t.status='booked'
				AND NOT t.excluded_from_budget
				AND t.occurred_at >= selected.period_start
				AND t.occurred_at < CASE selected.period
					WHEN 'weekly' THEN selected.period_start + 7
					ELSE (selected.period_start + INTERVAL '1 month')::date
				END
				AND (selected.category='' OR lower(t.category)=lower(selected.category))
		),0) AS spent
	FROM selected
)
SELECT id,name,category,amount::text,currency,period,warning_threshold,status,
	to_char(period_start,'YYYY-MM-DD'),to_char(period_end,'YYYY-MM-DD'),spent::text,
	GREATEST(amount-spent,0)::text,round((spent/amount)*100,1)::text,
	CASE WHEN spent >= amount THEN 'exceeded'
		WHEN spent >= amount*warning_threshold/100.0 THEN 'approaching'
		ELSE 'safe' END,
	to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
FROM calculated`

func (r *Repository) ListBudgets(ctx context.Context, userID int, reference time.Time, includeArchived bool) ([]model.Budget, error) {
	query := budgetSelect
	if includeArchived {
		query += ` WHERE status IN ('active','archived')`
	} else {
		query += ` WHERE status='active'`
	}
	query += ` ORDER BY CASE WHEN category='' THEN 0 ELSE 1 END,name,id`
	rows, err := r.db.Query(ctx, query, userID, reference)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Budget, 0)
	for rows.Next() {
		item, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetBudget(ctx context.Context, userID, budgetID int, reference time.Time) (model.Budget, error) {
	item, err := scanBudget(r.db.QueryRow(ctx, budgetSelect+` WHERE id=$3`, userID, reference, budgetID))
	return item, mapNotFound(err)
}

func (r *Repository) CreateBudget(ctx context.Context, userID int, request model.BudgetRequest, reference time.Time) (model.Budget, error) {
	var id int
	err := r.db.QueryRow(ctx, `INSERT INTO budgets(user_id,name,category,amount,currency,period,warning_threshold)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, userID, request.Name, request.Category,
		request.Amount, request.Currency, request.Period, request.WarningThreshold).Scan(&id)
	if mapped := mapConflict(err); mapped == ErrConflict {
		return model.Budget{}, ErrConflict
	}
	if err != nil {
		return model.Budget{}, err
	}
	return r.GetBudget(ctx, userID, id, reference)
}

func (r *Repository) UpdateBudget(ctx context.Context, userID, budgetID int, request model.BudgetRequest, reference time.Time) (model.Budget, error) {
	tag, err := r.db.Exec(ctx, `UPDATE budgets SET name=$1,category=$2,amount=$3,currency=$4,
		period=$5,warning_threshold=$6,updated_at=now()
		WHERE id=$7 AND user_id=$8 AND status='active'`, request.Name, request.Category,
		request.Amount, request.Currency, request.Period, request.WarningThreshold, budgetID, userID)
	if mapped := mapConflict(err); mapped == ErrConflict {
		return model.Budget{}, ErrConflict
	}
	if err != nil {
		return model.Budget{}, err
	}
	if tag.RowsAffected() == 0 {
		return model.Budget{}, ErrNotFound
	}
	return r.GetBudget(ctx, userID, budgetID, reference)
}

func (r *Repository) ArchiveBudget(ctx context.Context, userID, budgetID int) error {
	tag, err := r.db.Exec(ctx, `UPDATE budgets SET status='archived',updated_at=now()
		WHERE id=$1 AND user_id=$2 AND status='active'`, budgetID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) QueueBudgetAlerts(ctx context.Context, reference time.Time) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `WITH active AS (
		SELECT b.*,
			CASE b.period WHEN 'weekly' THEN date_trunc('week',$1::date)::date
				ELSE date_trunc('month',$1::date)::date END AS period_start
		FROM budgets b WHERE b.status='active'
	), spending AS (
		SELECT active.*,
			COALESCE((SELECT sum(t.amount) FROM transactions t
				WHERE t.user_id=active.user_id AND t.type='expense' AND t.status='booked'
					AND NOT t.excluded_from_budget
					AND t.occurred_at >= active.period_start
					AND t.occurred_at < CASE active.period WHEN 'weekly' THEN active.period_start+7
						ELSE (active.period_start+INTERVAL '1 month')::date END
					AND (active.category='' OR lower(t.category)=lower(active.category))),0) AS spent
		FROM active
	), candidates AS (
		SELECT spending.*,level
		FROM spending CROSS JOIN LATERAL (
			SELECT warning_threshold AS level WHERE spent >= amount*warning_threshold/100.0
			UNION SELECT 100 WHERE spent >= amount
		) levels
	), inserted AS (
		INSERT INTO budget_alerts(budget_id,user_id,period_start,alert_level,spent_amount)
		SELECT id,user_id,period_start,level,spent FROM candidates
		ON CONFLICT(budget_id,period_start,alert_level) DO NOTHING
		RETURNING budget_id,user_id,period_start,alert_level,spent_amount
	)
	SELECT inserted.budget_id,inserted.user_id,to_char(inserted.period_start,'YYYY-MM-DD'),
		inserted.alert_level,inserted.spent_amount::text,b.name,b.amount::text,b.currency
	FROM inserted JOIN budgets b ON b.id=inserted.budget_id`, reference)
	if err != nil {
		return 0, err
	}
	type alert struct {
		budgetID, userID, level  int
		periodStart, spent, name string
		amount, currency         string
	}
	alerts := make([]alert, 0)
	for rows.Next() {
		var item alert
		if err := rows.Scan(&item.budgetID, &item.userID, &item.periodStart, &item.level,
			&item.spent, &item.name, &item.amount, &item.currency); err != nil {
			rows.Close()
			return 0, err
		}
		alerts = append(alerts, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range alerts {
		title := "Budget is approaching its limit"
		if item.level == 100 {
			title = "Budget limit reached"
		}
		_, err := tx.Exec(ctx, `INSERT INTO notification_outbox(user_id,event_type,event_key,title,body,payload)
			SELECT $1,'budget_alert',$2,$3,$4,jsonb_build_object(
				'budget_id',$5::bigint,'period_start',$6::text,
				'alert_level',$7::integer,'spent_amount',$8::numeric)
			WHERE COALESCE((SELECT budget_alerts FROM notification_preferences WHERE user_id=$1),true)
			ON CONFLICT(event_key) DO NOTHING`, item.userID,
			"budget:"+strconv.Itoa(item.budgetID)+":"+item.periodStart+":"+strconv.Itoa(item.level), title,
			item.name+" · "+item.spent+" of "+item.amount+" "+item.currency,
			item.budgetID, item.periodStart, item.level, item.spent)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(alerts), nil
}

func scanBudget(row rowScanner) (model.Budget, error) {
	var item model.Budget
	err := row.Scan(&item.ID, &item.Name, &item.Category, &item.Amount, &item.Currency,
		&item.Period, &item.WarningThreshold, &item.Status, &item.PeriodStart, &item.PeriodEnd,
		&item.SpentAmount, &item.RemainingAmount, &item.ProgressPercent, &item.AlertLevel,
		&item.CreatedAt, &item.UpdatedAt)
	return item, err
}
