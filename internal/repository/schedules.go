package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"money-manager-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ScheduleOccurrenceSeed struct {
	ScheduleID   int
	UserID       int
	ScheduledFor time.Time
	Type         string
	Name         string
	Category     string
	Description  string
	Amount       string
	Currency     string
	AutoPost     bool
}

type ScheduleOccurrenceFilter struct {
	From       time.Time
	Through    time.Time
	ScheduleID int
	Status     string
}

const transactionScheduleSelect = `SELECT
	s.id,s.user_id,s.type,s.name,s.category,s.description,s.amount::text,s.currency,
	s.frequency,s.frequency_interval,to_char(s.start_date,'YYYY-MM-DD'),
	COALESCE(to_char(s.end_date,'YYYY-MM-DD'),''),s.day_of_week,s.day_of_month,
	s.timezone,s.auto_post,s.status,COALESCE(to_char(s.materialized_through,'YYYY-MM-DD'),''),
	COALESCE(to_char((
		SELECT min(o.scheduled_for)
		FROM transaction_schedule_occurrences o
		WHERE o.schedule_id=s.id AND o.status='planned'
		  AND o.scheduled_for >= ($2 AT TIME ZONE s.timezone)::date
	),'YYYY-MM-DD'),''),
	to_char(s.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	to_char(s.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
	FROM transaction_schedules s`

const transactionScheduleReturning = `id,user_id,type,name,category,description,amount::text,currency,
	frequency,frequency_interval,to_char(start_date,'YYYY-MM-DD'),
	COALESCE(to_char(end_date,'YYYY-MM-DD'),''),day_of_week,day_of_month,
	timezone,auto_post,status,COALESCE(to_char(materialized_through,'YYYY-MM-DD'),''),
	''::text,
	to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`

func (r *Repository) CreateTransactionSchedule(
	ctx context.Context,
	userID int,
	request model.TransactionScheduleRequest,
) (model.TransactionSchedule, error) {
	row := r.db.QueryRow(ctx, `INSERT INTO transaction_schedules(
		user_id,type,name,category,description,amount,currency,frequency,frequency_interval,
		start_date,end_date,day_of_week,day_of_month,timezone,auto_post
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')::date,$12,$13,$14,$15)
	RETURNING `+transactionScheduleReturning,
		userID, request.Type, request.Name, request.Category, request.Description, request.Amount,
		request.Currency, request.Frequency, request.FrequencyInterval, request.StartDate, request.EndDate,
		request.DayOfWeek, request.DayOfMonth, request.Timezone, request.AutoPost,
	)
	return scanTransactionSchedule(row)
}

func (r *Repository) ListTransactionSchedules(
	ctx context.Context,
	userID int,
	status string,
	now time.Time,
) ([]model.TransactionSchedule, error) {
	query := transactionScheduleSelect + ` WHERE s.user_id=$1`
	args := []any{userID, now}
	if status == "" {
		query += ` AND s.status <> 'archived'`
	} else {
		query += ` AND s.status=$3`
		args = append(args, status)
	}
	query += ` ORDER BY CASE s.status WHEN 'active' THEN 0 WHEN 'paused' THEN 1 ELSE 2 END,
		COALESCE((SELECT min(o.scheduled_for) FROM transaction_schedule_occurrences o
			WHERE o.schedule_id=s.id AND o.status='planned'), 'infinity'::date),s.id`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.TransactionSchedule, 0)
	for rows.Next() {
		item, err := scanTransactionSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) GetTransactionSchedule(
	ctx context.Context,
	userID, scheduleID int,
	now time.Time,
) (model.TransactionSchedule, error) {
	row := r.db.QueryRow(ctx, transactionScheduleSelect+` WHERE s.user_id=$1 AND s.id=$3`, userID, now, scheduleID)
	item, err := scanTransactionSchedule(row)
	return item, mapNotFound(err)
}

func (r *Repository) UpdateTransactionSchedule(
	ctx context.Context,
	userID, scheduleID int,
	request model.TransactionScheduleRequest,
	today time.Time,
) (model.TransactionSchedule, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return model.TransactionSchedule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `UPDATE transaction_schedules SET
		type=$1,name=$2,category=$3,description=$4,amount=$5,currency=$6,
		frequency=$7,frequency_interval=$8,start_date=$9,end_date=NULLIF($10,'')::date,
		day_of_week=$11,day_of_month=$12,timezone=$13,auto_post=$14,
		materialized_through=$15::date-1,updated_at=now()
		WHERE id=$16 AND user_id=$17 AND status <> 'archived'
		RETURNING `+transactionScheduleReturning,
		request.Type, request.Name, request.Category, request.Description, request.Amount, request.Currency,
		request.Frequency, request.FrequencyInterval, request.StartDate, request.EndDate,
		request.DayOfWeek, request.DayOfMonth, request.Timezone, request.AutoPost,
		today, scheduleID, userID,
	)
	item, err := scanTransactionSchedule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.TransactionSchedule{}, ErrNotFound
	}
	if err != nil {
		return model.TransactionSchedule{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM transaction_schedule_occurrences
		WHERE schedule_id=$1 AND status='planned' AND scheduled_for >= $2::date`, scheduleID, today); err != nil {
		return model.TransactionSchedule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.TransactionSchedule{}, err
	}
	return item, nil
}

func (r *Repository) SetTransactionScheduleStatus(
	ctx context.Context,
	userID, scheduleID int,
	status string,
) error {
	tag, err := r.db.Exec(ctx, `UPDATE transaction_schedules
		SET status=$1,updated_at=now()
		WHERE id=$2 AND user_id=$3 AND status <> 'archived'`, status, scheduleID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ArchiveTransactionSchedule(ctx context.Context, userID, scheduleID int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE transaction_schedules
		SET status='archived',updated_at=now()
		WHERE id=$1 AND user_id=$2 AND status <> 'archived'`, scheduleID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE transaction_schedule_occurrences
		SET status='skipped',updated_at=now()
		WHERE schedule_id=$1 AND status='planned'`, scheduleID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListActiveTransactionSchedules(ctx context.Context) ([]model.TransactionSchedule, error) {
	rows, err := r.db.Query(ctx, `SELECT `+transactionScheduleReturning+`
		FROM transaction_schedules WHERE status='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.TransactionSchedule, 0)
	for rows.Next() {
		item, err := scanTransactionSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) UpsertTransactionScheduleOccurrences(
	ctx context.Context,
	seeds []ScheduleOccurrenceSeed,
) (int, error) {
	if len(seeds) == 0 {
		return 0, nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inserted := 0
	for _, seed := range seeds {
		tag, err := tx.Exec(ctx, `INSERT INTO transaction_schedule_occurrences(
			schedule_id,user_id,scheduled_for,type,name,category,description,amount,currency,auto_post
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT(schedule_id,scheduled_for) DO NOTHING`,
			seed.ScheduleID, seed.UserID, seed.ScheduledFor, seed.Type, seed.Name, seed.Category,
			seed.Description, seed.Amount, seed.Currency, seed.AutoPost,
		)
		if err != nil {
			return 0, err
		}
		inserted += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return inserted, nil
}

func (r *Repository) MarkTransactionScheduleMaterializedThrough(
	ctx context.Context,
	scheduleID int,
	through time.Time,
) error {
	tag, err := r.db.Exec(ctx, `UPDATE transaction_schedules
		SET materialized_through=GREATEST(COALESCE(materialized_through,$2::date),$2::date),updated_at=now()
		WHERE id=$1 AND status='active'`, scheduleID, through)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ListTransactionScheduleOccurrences(
	ctx context.Context,
	userID int,
	filter ScheduleOccurrenceFilter,
) ([]model.TransactionScheduleOccurrence, error) {
	query := `SELECT id,schedule_id,to_char(scheduled_for,'YYYY-MM-DD'),status,type,name,category,
		description,amount::text,currency,auto_post,transaction_id
		FROM transaction_schedule_occurrences
		WHERE user_id=$1 AND scheduled_for >= $2 AND scheduled_for <= $3`
	args := []any{userID, filter.From, filter.Through}
	if filter.ScheduleID > 0 {
		query += fmt.Sprintf(" AND schedule_id=$%d", len(args)+1)
		args = append(args, filter.ScheduleID)
	}
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status=$%d", len(args)+1)
		args = append(args, filter.Status)
	}
	query += ` ORDER BY scheduled_for,id`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.TransactionScheduleOccurrence, 0)
	for rows.Next() {
		var item model.TransactionScheduleOccurrence
		var transactionID pgtype.Int4
		if err := rows.Scan(
			&item.ID, &item.ScheduleID, &item.ScheduledFor, &item.Status, &item.Type,
			&item.Name, &item.Category, &item.Description, &item.Amount, &item.Currency,
			&item.AutoPost, &transactionID,
		); err != nil {
			return nil, err
		}
		if transactionID.Valid {
			value := int(transactionID.Int32)
			item.TransactionID = &value
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) PostDueTransactionScheduleOccurrences(
	ctx context.Context,
	now time.Time,
	limit int,
) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `SELECT o.id,o.user_id,o.type,o.name,o.category,o.description,
		o.amount::text,o.currency,to_char(o.scheduled_for,'YYYY-MM-DD')
		FROM transaction_schedule_occurrences o
		JOIN transaction_schedules s ON s.id=o.schedule_id
		WHERE o.status='planned' AND o.auto_post AND s.status='active'
		  AND o.scheduled_for <= ($1 AT TIME ZONE s.timezone)::date
		ORDER BY o.scheduled_for,o.id
		FOR UPDATE OF o SKIP LOCKED
		LIMIT $2`, now, limit)
	if err != nil {
		return 0, err
	}
	type dueOccurrence struct {
		ID, UserID                        int
		Type, Name, Category, Description string
		Amount, Currency, ScheduledFor    string
	}
	due := make([]dueOccurrence, 0)
	for rows.Next() {
		var item dueOccurrence
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.Type, &item.Name, &item.Category,
			&item.Description, &item.Amount, &item.Currency, &item.ScheduledFor,
		); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, item := range due {
		description := item.Description
		if description == "" {
			description = item.Name
		}
		var transactionID int
		if err := tx.QueryRow(ctx, `INSERT INTO transactions(
			user_id,type,category,description,amount,currency,occurred_at,source,status,schedule_occurrence_id
		) VALUES($1,$2,$3,$4,$5,$6,$7,'schedule','booked',$8)
		ON CONFLICT(schedule_occurrence_id) WHERE schedule_occurrence_id IS NOT NULL
		DO UPDATE SET schedule_occurrence_id=EXCLUDED.schedule_occurrence_id
		RETURNING id`,
			item.UserID, item.Type, item.Category, description, item.Amount, item.Currency,
			item.ScheduledFor, item.ID,
		).Scan(&transactionID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE transaction_schedule_occurrences
			SET status='posted',transaction_id=$1,posted_at=now(),updated_at=now()
			WHERE id=$2`, transactionID, item.ID); err != nil {
			return 0, err
		}
		title := "Scheduled income added"
		if item.Type == "expense" {
			title = "Scheduled expense added"
		}
		payload, err := json.Marshal(map[string]any{
			"schedule_occurrence_id": item.ID,
			"transaction_id":         transactionID,
			"type":                   item.Type,
			"scheduled_for":          item.ScheduledFor,
		})
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO notification_outbox(
			user_id,event_type,event_key,title,body,payload
		)
		SELECT $1,'scheduled_transaction_posted',$2,$3,$4,$5
		WHERE COALESCE((
			SELECT scheduled_money FROM notification_preferences WHERE user_id=$1
		),true)
		ON CONFLICT(event_key) DO NOTHING`,
			item.UserID, fmt.Sprintf("schedule-occurrence:%d:posted", item.ID), title,
			fmt.Sprintf("%s · %s %s", item.Name, item.Amount, item.Currency), payload,
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(due), nil
}

func (r *Repository) QueueDueTransactionScheduleReminders(
	ctx context.Context,
	now time.Time,
	limit int,
) (int, error) {
	tag, err := r.db.Exec(ctx, `WITH due AS (
		SELECT occurrence.id,occurrence.user_id,occurrence.type,occurrence.name,
			occurrence.amount::text,occurrence.currency,
			to_char(occurrence.scheduled_for,'YYYY-MM-DD') AS scheduled_for
		FROM transaction_schedule_occurrences occurrence
		JOIN transaction_schedules schedule ON schedule.id=occurrence.schedule_id
		WHERE occurrence.status='planned' AND NOT occurrence.auto_post AND schedule.status='active'
		  AND occurrence.scheduled_for <= ($1 AT TIME ZONE schedule.timezone)::date
		ORDER BY occurrence.scheduled_for,occurrence.id
		LIMIT $2
	)
	INSERT INTO notification_outbox(user_id,event_type,event_key,title,body,payload)
	SELECT due.user_id,'scheduled_transaction_due',
		'schedule-occurrence:'||due.id::text||':due',
		CASE WHEN due.type='income' THEN 'Scheduled income due' ELSE 'Scheduled expense due' END,
		due.name||' · '||due.amount||' '||due.currency,
		jsonb_build_object('schedule_occurrence_id',due.id,'type',due.type,'scheduled_for',due.scheduled_for)
	FROM due
	WHERE COALESCE((SELECT scheduled_money FROM notification_preferences
		WHERE user_id=due.user_id),true)
	ON CONFLICT(event_key) DO NOTHING`, now, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func scanTransactionSchedule(row rowScanner) (model.TransactionSchedule, error) {
	var item model.TransactionSchedule
	var dayOfWeek, dayOfMonth pgtype.Int2
	err := row.Scan(
		&item.ID, &item.UserID, &item.Type, &item.Name, &item.Category, &item.Description, &item.Amount,
		&item.Currency, &item.Frequency, &item.FrequencyInterval, &item.StartDate, &item.EndDate,
		&dayOfWeek, &dayOfMonth, &item.Timezone, &item.AutoPost, &item.Status,
		&item.MaterializedThrough, &item.NextOccurrenceDate, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return model.TransactionSchedule{}, err
	}
	if dayOfWeek.Valid {
		value := int(dayOfWeek.Int16)
		item.DayOfWeek = &value
	}
	if dayOfMonth.Valid {
		value := int(dayOfMonth.Int16)
		item.DayOfMonth = &value
	}
	return item, nil
}
