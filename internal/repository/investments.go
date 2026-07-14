package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"money-manager-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type InvestmentTradeFilter struct {
	From      time.Time
	Through   time.Time
	AssetType string
	Symbol    string
	Broker    string
	Limit     int
}

func (r *Repository) CreateInvestmentTrade(ctx context.Context, userID int, request model.InvestmentTradeRequest) (model.InvestmentTrade, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return model.InvestmentTrade{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := investmentPositionLockKey(userID, request.AssetType, request.Symbol, request.Broker)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return model.InvestmentTrade{}, err
	}
	if request.Side == "sell" {
		var validLedger bool
		err := tx.QueryRow(ctx, `WITH entries AS (
			SELECT occurred_at,id::bigint AS sequence,
				CASE side WHEN 'buy' THEN quantity ELSE -quantity END AS delta
			FROM investment_trades
			WHERE user_id=$1 AND asset_type=$2 AND symbol=$3 AND broker=$4
			UNION ALL
			SELECT $5::timestamptz,9223372036854775807::bigint,-$6::numeric
		), balances AS (
			SELECT sum(delta) OVER (ORDER BY occurred_at,sequence ROWS UNBOUNDED PRECEDING) AS quantity
			FROM entries
		)
		SELECT COALESCE(bool_and(quantity >= 0),true) FROM balances`,
			userID, request.AssetType, request.Symbol, request.Broker, request.OccurredAt, request.Quantity,
		).Scan(&validLedger)
		if err != nil {
			return model.InvestmentTrade{}, err
		}
		if !validLedger {
			return model.InvestmentTrade{}, ErrConflict
		}
	}
	row := tx.QueryRow(ctx, `INSERT INTO investment_trades(
		user_id,asset_type,symbol,asset_name,broker,side,amount,quantity,price_per_unit,
		price_provider,price_as_of,fees,currency,occurred_at,notes
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	RETURNING id,asset_type,symbol,asset_name,broker,side,amount::text,quantity::text,
		price_per_unit::text,price_provider,
		to_char(price_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),fees::text,
		currency,to_char(occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),notes,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		userID, request.AssetType, request.Symbol, request.AssetName, request.Broker, request.Side,
		request.Amount, request.Quantity, request.PricePerUnit, request.PriceProvider, request.PriceAsOf,
		request.Fees, request.Currency, request.OccurredAt, request.Notes)
	item, err := scanInvestmentTrade(row)
	if err != nil {
		return model.InvestmentTrade{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.InvestmentTrade{}, err
	}
	return item, nil
}

func (r *Repository) ListInvestmentTrades(ctx context.Context, userID int, filter InvestmentTradeFilter) ([]model.InvestmentTrade, error) {
	query := `SELECT id,asset_type,symbol,asset_name,broker,side,amount::text,quantity::text,
		price_per_unit::text,price_provider,
		to_char(price_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),fees::text,
		currency,to_char(occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),notes,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM investment_trades WHERE user_id=$1`
	args := []any{userID}
	if !filter.From.IsZero() {
		query += fmt.Sprintf(" AND occurred_at >= $%d", len(args)+1)
		args = append(args, filter.From)
	}
	if !filter.Through.IsZero() {
		query += fmt.Sprintf(" AND occurred_at < $%d", len(args)+1)
		args = append(args, filter.Through)
	}
	if filter.AssetType != "" {
		query += fmt.Sprintf(" AND asset_type=$%d", len(args)+1)
		args = append(args, filter.AssetType)
	}
	if filter.Symbol != "" {
		query += fmt.Sprintf(" AND symbol=$%d", len(args)+1)
		args = append(args, filter.Symbol)
	}
	if filter.Broker != "" {
		query += fmt.Sprintf(" AND broker=$%d", len(args)+1)
		args = append(args, filter.Broker)
	}
	query += ` ORDER BY occurred_at DESC,id DESC`
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, filter.Limit)
	}
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.InvestmentTrade, 0)
	for rows.Next() {
		item, err := scanInvestmentTrade(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) DeleteInvestmentTrade(ctx context.Context, userID, tradeID int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var assetType, symbol, broker string
	err = tx.QueryRow(ctx, `SELECT asset_type,symbol,broker
		FROM investment_trades WHERE id=$1 AND user_id=$2`, tradeID, userID,
	).Scan(&assetType, &symbol, &broker)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	lockKey := investmentPositionLockKey(userID, assetType, symbol, broker)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM investment_trades WHERE id=$1 AND user_id=$2`, tradeID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	var validLedger bool
	err = tx.QueryRow(ctx, `WITH balances AS (
		SELECT sum(CASE side WHEN 'buy' THEN quantity ELSE -quantity END)
			OVER (ORDER BY occurred_at,id ROWS UNBOUNDED PRECEDING) AS quantity
		FROM investment_trades
		WHERE user_id=$1 AND asset_type=$2 AND symbol=$3 AND broker=$4
	)
	SELECT COALESCE(bool_and(quantity >= 0),true) FROM balances`,
		userID, assetType, symbol, broker,
	).Scan(&validLedger)
	if err != nil {
		return err
	}
	if !validLedger {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func investmentPositionLockKey(userID int, assetType, symbol, broker string) string {
	return fmt.Sprintf("%d:%s:%s:%s", userID, assetType, symbol, broker)
}

func (r *Repository) InvestmentHoldingQuantity(ctx context.Context, userID int, assetType, symbol, broker string) (string, error) {
	var quantity string
	err := r.db.QueryRow(ctx, `SELECT COALESCE(sum(CASE side WHEN 'buy' THEN quantity ELSE -quantity END),0)::text
		FROM investment_trades WHERE user_id=$1 AND asset_type=$2 AND symbol=$3 AND broker=$4`,
		userID, assetType, symbol, broker).Scan(&quantity)
	return quantity, err
}

func (r *Repository) ListInvestmentPrices(ctx context.Context) ([]model.InvestmentPrice, error) {
	rows, err := r.db.Query(ctx, `SELECT asset_type,symbol,currency,price::text,provider,
		to_char(as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM investment_prices ORDER BY asset_type,symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.InvestmentPrice, 0)
	for rows.Next() {
		var item model.InvestmentPrice
		if err := rows.Scan(&item.AssetType, &item.Symbol, &item.Currency, &item.Price, &item.Provider, &item.AsOf); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) UpsertManualInvestmentPrice(
	ctx context.Context,
	userID int,
	request model.InvestmentPriceRequest,
	asOf time.Time,
) (model.InvestmentPrice, error) {
	var item model.InvestmentPrice
	err := r.db.QueryRow(ctx, `INSERT INTO investment_prices(asset_type,symbol,currency,price,provider,as_of)
		SELECT $2,$3,$4,$5,'manual',$6
		WHERE EXISTS(SELECT 1 FROM investment_trades
			WHERE user_id=$1 AND asset_type=$2 AND symbol=$3)
		ON CONFLICT(asset_type,symbol,currency) DO UPDATE SET
			price=EXCLUDED.price,provider='manual',as_of=EXCLUDED.as_of,updated_at=now()
		RETURNING asset_type,symbol,currency,price::text,provider,
			to_char(as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		userID, request.AssetType, request.Symbol, request.Currency, request.Price, asOf,
	).Scan(&item.AssetType, &item.Symbol, &item.Currency, &item.Price, &item.Provider, &item.AsOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.InvestmentPrice{}, ErrNotFound
	}
	return item, err
}

const investmentScheduleSelect = `SELECT id,user_id,asset_type,symbol,asset_name,broker,amount::text,currency,
	frequency,frequency_interval,to_char(start_date,'YYYY-MM-DD'),COALESCE(to_char(end_date,'YYYY-MM-DD'),''),
	day_of_week,day_of_month,timezone,status,COALESCE(to_char(last_notified_on,'YYYY-MM-DD'),''),
	to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
	FROM investment_schedules`

func (r *Repository) CreateInvestmentSchedule(ctx context.Context, userID int, request model.InvestmentScheduleRequest) (model.InvestmentSchedule, error) {
	row := r.db.QueryRow(ctx, `INSERT INTO investment_schedules(
		user_id,asset_type,symbol,asset_name,broker,amount,currency,frequency,frequency_interval,
		start_date,end_date,day_of_week,day_of_month,timezone
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')::date,$12,$13,$14)
	RETURNING id,user_id,asset_type,symbol,asset_name,broker,amount::text,currency,
		frequency,frequency_interval,to_char(start_date,'YYYY-MM-DD'),COALESCE(to_char(end_date,'YYYY-MM-DD'),''),
		day_of_week,day_of_month,timezone,status,COALESCE(to_char(last_notified_on,'YYYY-MM-DD'),''),
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		userID, request.AssetType, request.Symbol, request.AssetName, request.Broker, request.Amount,
		request.Currency, request.Frequency, request.FrequencyInterval, request.StartDate, request.EndDate,
		request.DayOfWeek, request.DayOfMonth, request.Timezone)
	return scanInvestmentSchedule(row)
}

func (r *Repository) ListInvestmentSchedules(ctx context.Context, userID int, status string) ([]model.InvestmentSchedule, error) {
	query := investmentScheduleSelect + ` WHERE user_id=$1`
	args := []any{userID}
	if status == "" {
		query += ` AND status <> 'archived'`
	} else {
		query += ` AND status=$2`
		args = append(args, status)
	}
	query += ` ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'paused' THEN 1 ELSE 2 END,id`
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.InvestmentSchedule, 0)
	for rows.Next() {
		item, err := scanInvestmentSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetInvestmentSchedule(ctx context.Context, userID, scheduleID int) (model.InvestmentSchedule, error) {
	item, err := scanInvestmentSchedule(r.db.QueryRow(ctx, investmentScheduleSelect+` WHERE user_id=$1 AND id=$2`, userID, scheduleID))
	return item, mapNotFound(err)
}

func (r *Repository) UpdateInvestmentSchedule(
	ctx context.Context,
	userID, scheduleID int,
	request model.InvestmentScheduleRequest,
) (model.InvestmentSchedule, error) {
	row := r.db.QueryRow(ctx, `UPDATE investment_schedules SET
		asset_type=$1,symbol=$2,asset_name=$3,broker=$4,amount=$5,currency=$6,
		frequency=$7,frequency_interval=$8,start_date=$9,end_date=NULLIF($10,'')::date,
		day_of_week=$11,day_of_month=$12,timezone=$13,last_notified_on=NULL,updated_at=now()
		WHERE id=$14 AND user_id=$15 AND status <> 'archived'
		RETURNING id,user_id,asset_type,symbol,asset_name,broker,amount::text,currency,
		frequency,frequency_interval,to_char(start_date,'YYYY-MM-DD'),COALESCE(to_char(end_date,'YYYY-MM-DD'),''),
		day_of_week,day_of_month,timezone,status,COALESCE(to_char(last_notified_on,'YYYY-MM-DD'),''),
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		request.AssetType, request.Symbol, request.AssetName, request.Broker, request.Amount, request.Currency,
		request.Frequency, request.FrequencyInterval, request.StartDate, request.EndDate,
		request.DayOfWeek, request.DayOfMonth, request.Timezone, scheduleID, userID)
	item, err := scanInvestmentSchedule(row)
	return item, mapNotFound(err)
}

func (r *Repository) SetInvestmentScheduleStatus(ctx context.Context, userID, scheduleID int, status string) error {
	tag, err := r.db.Exec(ctx, `UPDATE investment_schedules SET status=$1,updated_at=now()
		WHERE id=$2 AND user_id=$3 AND status <> 'archived'`, status, scheduleID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ArchiveInvestmentSchedule(ctx context.Context, userID, scheduleID int) error {
	tag, err := r.db.Exec(ctx, `UPDATE investment_schedules SET status='archived',updated_at=now()
		WHERE id=$1 AND user_id=$2 AND status <> 'archived'`, scheduleID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ListActiveInvestmentSchedules(ctx context.Context) ([]model.InvestmentSchedule, error) {
	rows, err := r.db.Query(ctx, investmentScheduleSelect+` WHERE status='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.InvestmentSchedule, 0)
	for rows.Next() {
		item, err := scanInvestmentSchedule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) QueueInvestmentReminder(ctx context.Context, schedule model.InvestmentSchedule, date time.Time) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE investment_schedules SET last_notified_on=$1,updated_at=now()
		WHERE id=$2 AND status='active' AND (last_notified_on IS NULL OR last_notified_on < $1)`, date, schedule.ID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `INSERT INTO notification_outbox(user_id,event_type,event_key,title,body,payload)
		SELECT $1,'investment_reminder',$2,'Investment plan due',$3,
			jsonb_build_object('investment_schedule_id',$4,'scheduled_for',$5::text,'symbol',$6)
		WHERE COALESCE((SELECT investment_reminders FROM notification_preferences WHERE user_id=$1),true)
		ON CONFLICT(event_key) DO NOTHING`, schedule.UserID,
		fmt.Sprintf("investment-schedule:%d:%s", schedule.ID, date.Format("2006-01-02")),
		schedule.AssetName+" · "+schedule.Amount+" "+schedule.Currency,
		schedule.ID, date.Format("2006-01-02"), schedule.Symbol)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func scanInvestmentTrade(row rowScanner) (model.InvestmentTrade, error) {
	var item model.InvestmentTrade
	err := row.Scan(&item.ID, &item.AssetType, &item.Symbol, &item.AssetName, &item.Broker,
		&item.Side, &item.Amount, &item.Quantity, &item.PricePerUnit, &item.PriceProvider,
		&item.PriceAsOf, &item.Fees, &item.Currency, &item.OccurredAt, &item.Notes,
		&item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanInvestmentSchedule(row rowScanner) (model.InvestmentSchedule, error) {
	var item model.InvestmentSchedule
	var dayOfWeek, dayOfMonth pgtype.Int2
	err := row.Scan(&item.ID, &item.UserID, &item.AssetType, &item.Symbol, &item.AssetName,
		&item.Broker, &item.Amount, &item.Currency, &item.Frequency, &item.FrequencyInterval,
		&item.StartDate, &item.EndDate, &dayOfWeek, &dayOfMonth, &item.Timezone, &item.Status,
		&item.LastNotifiedOn, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return model.InvestmentSchedule{}, err
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
