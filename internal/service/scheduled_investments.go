package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/marketdata"
	"money-manager-server/internal/model"
	"money-manager-server/internal/recurrence"
	"money-manager-server/internal/repository"
)

const scheduledInvestmentPostingBatchSize = 100

var errScheduledInvestmentPostingTime = errors.New("scheduled investment posting time has not arrived")

func (s *Service) materializeDueInvestmentSchedules(ctx context.Context, now time.Time) (int, error) {
	schedules, err := s.store.ListActiveInvestmentSchedules(ctx)
	if err != nil {
		return 0, fmt.Errorf("list active investment schedules: %w", err)
	}
	materialized := 0
	for _, schedule := range schedules {
		today, err := scheduleLocalDate(now, schedule.Timezone)
		if err != nil {
			return materialized, fmt.Errorf("load investment schedule timezone: %w", err)
		}
		from, err := parseDate(schedule.StartDate, "start_date")
		if err != nil {
			return materialized, fmt.Errorf("parse stored investment schedule start date: %w", err)
		}
		if schedule.MaterializedThrough != "" {
			through, err := parseDate(schedule.MaterializedThrough, "materialized_through")
			if err != nil {
				return materialized, fmt.Errorf("parse stored investment materialization date: %w", err)
			}
			from = through.AddDate(0, 0, 1)
		}
		if from.After(today) {
			continue
		}
		rule, err := investmentRecurrenceRule(schedule)
		if err != nil {
			return materialized, fmt.Errorf("build investment recurrence: %w", err)
		}
		dates, err := recurrence.Occurrences(rule, from, today)
		if err != nil {
			return materialized, fmt.Errorf("generate investment occurrences: %w", err)
		}
		seeds := make([]repository.InvestmentScheduleOccurrenceSeed, 0, len(dates))
		for _, date := range dates {
			seeds = append(seeds, repository.InvestmentScheduleOccurrenceSeed{
				ScheduleID: schedule.ID, UserID: schedule.UserID, ScheduledFor: date,
			})
		}
		count, err := s.store.UpsertInvestmentScheduleOccurrences(ctx, seeds)
		if err != nil {
			return materialized, fmt.Errorf("store investment schedule occurrences: %w", err)
		}
		if err := s.store.MarkInvestmentScheduleMaterializedThrough(ctx, schedule.ID, today); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			return materialized, fmt.Errorf("mark investment schedule materialized: %w", err)
		}
		materialized += count
	}
	return materialized, nil
}

func (s *Service) postDueInvestmentScheduleOccurrences(ctx context.Context, now time.Time) (int, error) {
	occurrences, err := s.store.ListDueInvestmentScheduleOccurrences(
		ctx, now.UTC(), scheduledInvestmentPostingBatchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("list due investment schedule occurrences: %w", err)
	}
	posted := 0
	var postingErrors []error
	for _, occurrence := range occurrences {
		request, err := s.prepareScheduledInvestmentTrade(ctx, occurrence)
		if errors.Is(err, errScheduledInvestmentPostingTime) {
			continue
		}
		if err != nil {
			postingErrors = append(postingErrors, fmt.Errorf(
				"price investment schedule %d for %s: %w",
				occurrence.Schedule.ID, occurrence.ScheduledFor.Format("2006-01-02"), err,
			))
			continue
		}
		_, inserted, err := s.store.PostInvestmentScheduleOccurrence(ctx, occurrence.ID, request)
		if err != nil {
			postingErrors = append(postingErrors, fmt.Errorf(
				"post investment schedule occurrence %d: %w", occurrence.ID, err,
			))
			continue
		}
		if inserted {
			posted++
			s.invalidateInvestmentResponses(ctx, occurrence.Schedule.UserID)
		}
	}
	return posted, errors.Join(postingErrors...)
}

func (s *Service) prepareScheduledInvestmentTrade(
	ctx context.Context,
	occurrence repository.DueInvestmentScheduleOccurrence,
) (model.InvestmentTradeRequest, error) {
	schedule := occurrence.Schedule
	occurredAt, err := scheduledInvestmentTimestamp(occurrence.ScheduledFor, schedule.Timezone)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	if occurredAt.After(s.now().UTC()) {
		return model.InvestmentTradeRequest{}, errScheduledInvestmentPostingTime
	}
	request, err := s.validateInvestmentTrade(model.InvestmentTradeRequest{
		AssetType: schedule.AssetType, Symbol: schedule.Symbol, AssetName: schedule.AssetName,
		Exchange: schedule.Exchange, MarketCurrency: schedule.MarketCurrency, Broker: schedule.Broker,
		Side: "buy", Amount: schedule.Amount, Fees: "0", Currency: schedule.Currency,
		OccurredAt: occurredAt.Format(time.RFC3339), Notes: "Scheduled investment",
	})
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	quote, err := s.scheduledInvestmentQuote(ctx, schedule, occurredAt)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	price, err := normalizeUnsignedDecimal(quote.Price, "market price", 12, 8, false)
	if err != nil {
		return model.InvestmentTradeRequest{}, fmt.Errorf("market pricing returned an invalid price: %w", err)
	}
	provider, err := normalizeLimitedText(quote.Provider, "market price provider", 100, false)
	if err != nil || quote.AsOf.IsZero() {
		return model.InvestmentTradeRequest{}, errors.New("market quote audit data is missing")
	}
	amount, _ := new(big.Rat).SetString(request.Amount)
	unitPrice, _ := new(big.Rat).SetString(price)
	quantity := formatRatTrimmed(new(big.Rat).Quo(amount, unitPrice), 18)
	if quantity == "0" {
		return model.InvestmentTradeRequest{}, apperrors.Validation("amount is too small for the selected asset")
	}
	request.Quantity = quantity
	request.PricePerUnit = price
	request.PriceProvider = provider
	request.PriceAsOf = quote.AsOf.UTC().Truncate(time.Second).Format(time.RFC3339)
	return request, nil
}

func (s *Service) scheduledInvestmentQuote(
	ctx context.Context,
	schedule model.InvestmentSchedule,
	at time.Time,
) (investmentMarketQuote, error) {
	if schedule.AssetType == "crypto" {
		if s.marketData == nil {
			return investmentMarketQuote{}, errors.New("crypto market data client is not configured")
		}
		return s.marketData.QuoteAt(ctx, schedule.Symbol, schedule.Currency, at)
	}
	if s.stockHistoryData == nil {
		return investmentMarketQuote{}, errors.New("stock history market data client is not configured")
	}
	points, err := s.cachedStockDailyHistory(ctx, model.InvestmentTrade{
		AssetType: schedule.AssetType, Symbol: schedule.Symbol, AssetName: schedule.AssetName,
		Exchange: schedule.Exchange, MarketCurrency: schedule.MarketCurrency,
	}, at.AddDate(0, 0, -10))
	if err != nil {
		return investmentMarketQuote{}, err
	}
	through := time.Date(at.Year(), at.Month(), at.Day(), 23, 59, 59, 0, time.UTC)
	for index := len(points) - 1; index >= 0; index-- {
		if points[index].AsOf.After(through) {
			continue
		}
		provider := points[index].Provider
		if provider == "" {
			provider = marketdata.ProviderMarketstack
		}
		return investmentMarketQuote{Price: points[index].Price, Provider: provider, AsOf: points[index].AsOf}, nil
	}
	return investmentMarketQuote{}, errors.New("no stock closing price is available on or before the scheduled date")
}

func scheduledInvestmentTimestamp(date time.Time, timezone string) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	localMidnight := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location).UTC()
	utcMidnight := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	if localMidnight.Before(utcMidnight) {
		return utcMidnight, nil
	}
	return localMidnight, nil
}
