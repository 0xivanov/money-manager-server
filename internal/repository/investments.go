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

type InvestmentScheduleOccurrenceSeed struct {
	ScheduleID   int
	UserID       int
	ScheduledFor time.Time
}

type DueInvestmentScheduleOccurrence struct {
	ID           int
	ScheduledFor time.Time
	Schedule     model.InvestmentSchedule
}

func (r *Repository) CreateInvestmentTrade(ctx context.Context, userID int, request model.InvestmentTradeRequest) (model.InvestmentTrade, error) {
	if request.MarketCurrency == "" {
		request.MarketCurrency = "EUR"
	}
	if request.AssetType == "stock" && request.Exchange == "" {
		request.Exchange = "UNKNOWN"
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return model.InvestmentTrade{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := investmentPositionLockKey(userID, request.AssetType, request.Symbol, request.Exchange, request.Broker)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return model.InvestmentTrade{}, err
	}
	if request.Side == "sell" {
		var validLedger bool
		err := tx.QueryRow(ctx, `WITH entries AS (
			SELECT occurred_at,id::bigint AS sequence,
				CASE side WHEN 'buy' THEN quantity ELSE -quantity END AS delta
			FROM investment_trades
			WHERE user_id=$1 AND asset_type=$2 AND symbol=$3 AND exchange=$4 AND broker=$5
			UNION ALL
			SELECT $6::timestamptz,9223372036854775807::bigint,-$7::numeric
		), balances AS (
			SELECT sum(delta) OVER (ORDER BY occurred_at,sequence ROWS UNBOUNDED PRECEDING) AS quantity
			FROM entries
		)
		SELECT COALESCE(bool_and(quantity >= 0),true) FROM balances`,
			userID, request.AssetType, request.Symbol, request.Exchange, request.Broker, request.OccurredAt, request.Quantity,
		).Scan(&validLedger)
		if err != nil {
			return model.InvestmentTrade{}, err
		}
		if !validLedger {
			return model.InvestmentTrade{}, ErrConflict
		}
	}
	row := tx.QueryRow(ctx, `INSERT INTO investment_trades(
		user_id,asset_type,symbol,asset_name,exchange,market_currency,broker,side,amount,quantity,price_per_unit,
		price_provider,price_as_of,fees,currency,occurred_at,notes
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING id,asset_type,symbol,asset_name,exchange,market_currency,broker,side,amount::text,quantity::text,
			price_per_unit::text,price_provider,
			to_char(price_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),fees::text,
			currency,to_char(occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),notes,
			investment_schedule_occurrence_id,
			to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		userID, request.AssetType, request.Symbol, request.AssetName, request.Exchange, request.MarketCurrency,
		request.Broker, request.Side, request.Amount, request.Quantity, request.PricePerUnit,
		request.PriceProvider, request.PriceAsOf, request.Fees, request.Currency, request.OccurredAt, request.Notes)
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
	query := `SELECT id,asset_type,symbol,asset_name,exchange,market_currency,broker,side,amount::text,quantity::text,
		price_per_unit::text,price_provider,
		to_char(price_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),fees::text,
		currency,to_char(occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),notes,
		investment_schedule_occurrence_id,
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

	var assetType, symbol, exchange, broker string
	err = tx.QueryRow(ctx, `SELECT asset_type,symbol,exchange,broker
		FROM investment_trades WHERE id=$1 AND user_id=$2`, tradeID, userID,
	).Scan(&assetType, &symbol, &exchange, &broker)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	lockKey := investmentPositionLockKey(userID, assetType, symbol, exchange, broker)
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
		WHERE user_id=$1 AND asset_type=$2 AND symbol=$3 AND exchange=$4 AND broker=$5
	)
	SELECT COALESCE(bool_and(quantity >= 0),true) FROM balances`,
		userID, assetType, symbol, exchange, broker,
	).Scan(&validLedger)
	if err != nil {
		return err
	}
	if !validLedger {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func investmentPositionLockKey(userID int, assetType, symbol, exchange, broker string) string {
	return fmt.Sprintf("%d:%s:%s:%s:%s", userID, assetType, symbol, exchange, broker)
}

func (r *Repository) InvestmentHoldingQuantity(ctx context.Context, userID int, assetType, symbol, exchange, broker string) (string, error) {
	var quantity string
	err := r.db.QueryRow(ctx, `SELECT COALESCE(sum(CASE side WHEN 'buy' THEN quantity ELSE -quantity END),0)::text
		FROM investment_trades WHERE user_id=$1 AND asset_type=$2 AND symbol=$3 AND exchange=$4 AND broker=$5`,
		userID, assetType, symbol, exchange, broker).Scan(&quantity)
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

func (r *Repository) ListInvestmentMarketHistory(
	ctx context.Context,
	assetType, symbol, exchange, currency string,
	since time.Time,
) ([]model.InvestmentMarketHistoryPrice, error) {
	rows, err := r.db.Query(ctx, `SELECT asset_type,symbol,exchange,currency,price::text,provider,as_of
		FROM investment_market_history
		WHERE asset_type=$1 AND symbol=$2 AND exchange=$3 AND currency=$4 AND as_of >= $5::date
		ORDER BY as_of`, assetType, symbol, exchange, currency, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.InvestmentMarketHistoryPrice, 0)
	for rows.Next() {
		var item model.InvestmentMarketHistoryPrice
		if err := rows.Scan(
			&item.AssetType, &item.Symbol, &item.Exchange, &item.Currency,
			&item.Price, &item.Provider, &item.AsOf,
		); err != nil {
			return nil, err
		}
		item.AsOf = item.AsOf.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) UpsertInvestmentMarketHistory(
	ctx context.Context,
	prices []model.InvestmentMarketHistoryPrice,
) error {
	if len(prices) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, price := range prices {
		batch.Queue(`INSERT INTO investment_market_history(
			asset_type,symbol,exchange,currency,as_of,price,provider
		) VALUES($1,$2,$3,$4,$5::date,$6,$7)
		ON CONFLICT(asset_type,symbol,exchange,currency,as_of) DO UPDATE SET
			price=EXCLUDED.price,provider=EXCLUDED.provider,fetched_at=now()`,
			price.AssetType, price.Symbol, price.Exchange, price.Currency,
			price.AsOf.UTC(), price.Price, price.Provider)
	}
	results := r.db.SendBatch(ctx, batch)
	for range prices {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
}

const investmentScheduleSelect = `SELECT id,user_id,asset_type,symbol,asset_name,exchange,market_currency,broker,amount::text,currency,
	frequency,frequency_interval,to_char(start_date,'YYYY-MM-DD'),COALESCE(to_char(end_date,'YYYY-MM-DD'),''),
	day_of_week,day_of_month,timezone,status,COALESCE(to_char(last_notified_on,'YYYY-MM-DD'),''),
	COALESCE(to_char(materialized_through,'YYYY-MM-DD'),''),COALESCE(to_char(last_posted_on,'YYYY-MM-DD'),''),
	to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
	FROM investment_schedules`

func (r *Repository) CreateInvestmentSchedule(ctx context.Context, userID int, request model.InvestmentScheduleRequest) (model.InvestmentSchedule, error) {
	request = investmentScheduleMarketDefaults(request)
	row := r.db.QueryRow(ctx, `INSERT INTO investment_schedules(
		user_id,asset_type,symbol,asset_name,exchange,market_currency,broker,amount,currency,frequency,frequency_interval,
		start_date,end_date,day_of_week,day_of_month,timezone
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,'')::date,$14,$15,$16)
		RETURNING id,user_id,asset_type,symbol,asset_name,exchange,market_currency,broker,amount::text,currency,
			frequency,frequency_interval,to_char(start_date,'YYYY-MM-DD'),COALESCE(to_char(end_date,'YYYY-MM-DD'),''),
			day_of_week,day_of_month,timezone,status,COALESCE(to_char(last_notified_on,'YYYY-MM-DD'),''),
			COALESCE(to_char(materialized_through,'YYYY-MM-DD'),''),COALESCE(to_char(last_posted_on,'YYYY-MM-DD'),''),
			to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		userID, request.AssetType, request.Symbol, request.AssetName, request.Exchange, request.MarketCurrency,
		request.Broker, request.Amount, request.Currency, request.Frequency, request.FrequencyInterval,
		request.StartDate, request.EndDate, request.DayOfWeek, request.DayOfMonth, request.Timezone)
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
	request = investmentScheduleMarketDefaults(request)
	row := r.db.QueryRow(ctx, `UPDATE investment_schedules SET
		asset_type=$1,symbol=$2,asset_name=$3,exchange=$4,market_currency=$5,broker=$6,amount=$7,currency=$8,
		frequency=$9,frequency_interval=$10,start_date=$11,end_date=NULLIF($12,'')::date,
		day_of_week=$13,day_of_month=$14,timezone=$15,last_notified_on=NULL,updated_at=now()
		WHERE id=$16 AND user_id=$17 AND status <> 'archived'
		RETURNING id,user_id,asset_type,symbol,asset_name,exchange,market_currency,broker,amount::text,currency,
			frequency,frequency_interval,to_char(start_date,'YYYY-MM-DD'),COALESCE(to_char(end_date,'YYYY-MM-DD'),''),
			day_of_week,day_of_month,timezone,status,COALESCE(to_char(last_notified_on,'YYYY-MM-DD'),''),
			COALESCE(to_char(materialized_through,'YYYY-MM-DD'),''),COALESCE(to_char(last_posted_on,'YYYY-MM-DD'),''),
			to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		request.AssetType, request.Symbol, request.AssetName, request.Exchange, request.MarketCurrency,
		request.Broker, request.Amount, request.Currency, request.Frequency, request.FrequencyInterval,
		request.StartDate, request.EndDate, request.DayOfWeek, request.DayOfMonth, request.Timezone, scheduleID, userID)
	item, err := scanInvestmentSchedule(row)
	return item, mapNotFound(err)
}

func investmentScheduleMarketDefaults(request model.InvestmentScheduleRequest) model.InvestmentScheduleRequest {
	if request.MarketCurrency == "" {
		request.MarketCurrency = "EUR"
	}
	if request.AssetType == "stock" && request.Exchange == "" {
		request.Exchange = "UNKNOWN"
	}
	return request
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

func (r *Repository) UpsertInvestmentScheduleOccurrences(
	ctx context.Context,
	seeds []InvestmentScheduleOccurrenceSeed,
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
		tag, err := tx.Exec(ctx, `INSERT INTO investment_schedule_occurrences(schedule_id,user_id,scheduled_for)
			VALUES($1,$2,$3::date) ON CONFLICT(schedule_id,scheduled_for) DO NOTHING`,
			seed.ScheduleID, seed.UserID, seed.ScheduledFor.UTC())
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

func (r *Repository) MarkInvestmentScheduleMaterializedThrough(
	ctx context.Context,
	scheduleID int,
	through time.Time,
) error {
	tag, err := r.db.Exec(ctx, `UPDATE investment_schedules
		SET materialized_through=$1::date,updated_at=now()
		WHERE id=$2 AND status='active'`, through.UTC(), scheduleID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ListDueInvestmentScheduleOccurrences(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]DueInvestmentScheduleOccurrence, error) {
	rows, err := r.db.Query(ctx, `SELECT occurrence.id,occurrence.scheduled_for,
		schedule.id,schedule.user_id,schedule.asset_type,schedule.symbol,schedule.asset_name,
		schedule.exchange,schedule.market_currency,schedule.broker,schedule.amount::text,schedule.currency,
		schedule.frequency,schedule.frequency_interval,to_char(schedule.start_date,'YYYY-MM-DD'),
		COALESCE(to_char(schedule.end_date,'YYYY-MM-DD'),''),schedule.day_of_week,schedule.day_of_month,
		schedule.timezone,schedule.status,COALESCE(to_char(schedule.last_notified_on,'YYYY-MM-DD'),''),
		COALESCE(to_char(schedule.materialized_through,'YYYY-MM-DD'),''),
		COALESCE(to_char(schedule.last_posted_on,'YYYY-MM-DD'),''),
		to_char(schedule.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(schedule.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM investment_schedule_occurrences occurrence
		JOIN investment_schedules schedule ON schedule.id=occurrence.schedule_id
		WHERE occurrence.status='planned' AND schedule.status='active'
		  AND occurrence.scheduled_for <= ($1 AT TIME ZONE schedule.timezone)::date
		ORDER BY occurrence.scheduled_for,occurrence.id LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DueInvestmentScheduleOccurrence, 0)
	for rows.Next() {
		var item DueInvestmentScheduleOccurrence
		var dayOfWeek, dayOfMonth pgtype.Int2
		if err := rows.Scan(
			&item.ID, &item.ScheduledFor,
			&item.Schedule.ID, &item.Schedule.UserID, &item.Schedule.AssetType, &item.Schedule.Symbol,
			&item.Schedule.AssetName, &item.Schedule.Exchange, &item.Schedule.MarketCurrency,
			&item.Schedule.Broker, &item.Schedule.Amount, &item.Schedule.Currency,
			&item.Schedule.Frequency, &item.Schedule.FrequencyInterval, &item.Schedule.StartDate,
			&item.Schedule.EndDate, &dayOfWeek, &dayOfMonth, &item.Schedule.Timezone,
			&item.Schedule.Status, &item.Schedule.LastNotifiedOn, &item.Schedule.MaterializedThrough,
			&item.Schedule.LastPostedOn, &item.Schedule.CreatedAt, &item.Schedule.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if dayOfWeek.Valid {
			value := int(dayOfWeek.Int16)
			item.Schedule.DayOfWeek = &value
		}
		if dayOfMonth.Valid {
			value := int(dayOfMonth.Int16)
			item.Schedule.DayOfMonth = &value
		}
		item.ScheduledFor = time.Date(
			item.ScheduledFor.Year(), item.ScheduledFor.Month(), item.ScheduledFor.Day(), 0, 0, 0, 0, time.UTC,
		)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) PostInvestmentScheduleOccurrence(
	ctx context.Context,
	occurrenceID int,
	request model.InvestmentTradeRequest,
) (model.InvestmentTrade, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return model.InvestmentTrade{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, scheduleID int
	var scheduledFor time.Time
	err = tx.QueryRow(ctx, `SELECT occurrence.user_id,occurrence.schedule_id,occurrence.scheduled_for
		FROM investment_schedule_occurrences occurrence
		JOIN investment_schedules schedule ON schedule.id=occurrence.schedule_id
		WHERE occurrence.id=$1 AND occurrence.status='planned' AND schedule.status='active'
		FOR UPDATE OF occurrence`, occurrenceID).Scan(&userID, &scheduleID, &scheduledFor)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.InvestmentTrade{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return model.InvestmentTrade{}, false, err
	}
	lockKey := investmentPositionLockKey(userID, request.AssetType, request.Symbol, request.Exchange, request.Broker)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return model.InvestmentTrade{}, false, err
	}
	row := tx.QueryRow(ctx, `INSERT INTO investment_trades(
		user_id,asset_type,symbol,asset_name,exchange,market_currency,broker,side,amount,quantity,price_per_unit,
		price_provider,price_as_of,fees,currency,occurred_at,notes,investment_schedule_occurrence_id
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
	RETURNING id,asset_type,symbol,asset_name,exchange,market_currency,broker,side,amount::text,quantity::text,
		price_per_unit::text,price_provider,
		to_char(price_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),fees::text,
		currency,to_char(occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),notes,
		investment_schedule_occurrence_id,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		userID, request.AssetType, request.Symbol, request.AssetName, request.Exchange, request.MarketCurrency,
		request.Broker, request.Side, request.Amount, request.Quantity, request.PricePerUnit,
		request.PriceProvider, request.PriceAsOf, request.Fees, request.Currency, request.OccurredAt,
		request.Notes, occurrenceID)
	item, err := scanInvestmentTrade(row)
	if err != nil {
		return model.InvestmentTrade{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE investment_schedule_occurrences SET status='posted',updated_at=now()
		WHERE id=$1`, occurrenceID); err != nil {
		return model.InvestmentTrade{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE investment_schedules
		SET last_posted_on=GREATEST(COALESCE(last_posted_on,$1::date),$1::date),updated_at=now()
		WHERE id=$2`, scheduledFor.UTC(), scheduleID); err != nil {
		return model.InvestmentTrade{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notification_outbox(user_id,event_type,event_key,title,body,payload)
		SELECT $1,'scheduled_investment_posted',$2,'Scheduled investment recorded',$3,
			jsonb_build_object('investment_schedule_id',$4::bigint,
				'investment_schedule_occurrence_id',$5::bigint,'investment_trade_id',$6::bigint,
				'scheduled_for',$7::text,'symbol',$8::text)
		WHERE COALESCE((SELECT investment_reminders FROM notification_preferences WHERE user_id=$1),true)
		ON CONFLICT(event_key) DO NOTHING`, userID,
		fmt.Sprintf("investment-schedule-occurrence:%d:posted", occurrenceID),
		request.AssetName+" · "+request.Amount+" "+request.Currency,
		scheduleID, occurrenceID, item.ID, scheduledFor.Format("2006-01-02"), request.Symbol); err != nil {
		return model.InvestmentTrade{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.InvestmentTrade{}, false, err
	}
	return item, true, nil
}

func scanInvestmentTrade(row rowScanner) (model.InvestmentTrade, error) {
	var item model.InvestmentTrade
	var scheduleOccurrenceID pgtype.Int8
	err := row.Scan(&item.ID, &item.AssetType, &item.Symbol, &item.AssetName, &item.Exchange,
		&item.MarketCurrency, &item.Broker,
		&item.Side, &item.Amount, &item.Quantity, &item.PricePerUnit, &item.PriceProvider,
		&item.PriceAsOf, &item.Fees, &item.Currency, &item.OccurredAt, &item.Notes, &scheduleOccurrenceID,
		&item.CreatedAt, &item.UpdatedAt)
	if err == nil && scheduleOccurrenceID.Valid {
		value := int(scheduleOccurrenceID.Int64)
		item.ScheduleOccurrenceID = &value
	}
	return item, err
}

func scanInvestmentSchedule(row rowScanner) (model.InvestmentSchedule, error) {
	var item model.InvestmentSchedule
	var dayOfWeek, dayOfMonth pgtype.Int2
	err := row.Scan(&item.ID, &item.UserID, &item.AssetType, &item.Symbol, &item.AssetName,
		&item.Exchange, &item.MarketCurrency, &item.Broker, &item.Amount, &item.Currency,
		&item.Frequency, &item.FrequencyInterval,
		&item.StartDate, &item.EndDate, &dayOfWeek, &dayOfMonth, &item.Timezone, &item.Status,
		&item.LastNotifiedOn, &item.MaterializedThrough, &item.LastPostedOn,
		&item.CreatedAt, &item.UpdatedAt)
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
