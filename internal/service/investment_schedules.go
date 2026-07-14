package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/recurrence"
	"money-manager-server/internal/repository"
)

func (s *Service) CreateInvestmentSchedule(
	ctx context.Context,
	userID int,
	request model.InvestmentScheduleRequest,
) (model.InvestmentSchedule, error) {
	normalized, err := s.validateInvestmentSchedule(request, nil)
	if err != nil {
		return model.InvestmentSchedule{}, err
	}
	item, err := s.store.CreateInvestmentSchedule(ctx, userID, normalized)
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("create investment schedule: %w", err))
	}
	return s.decorateInvestmentSchedule(item)
}

func (s *Service) ListInvestmentSchedules(ctx context.Context, userID int, status string) ([]model.InvestmentSchedule, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && status != "active" && status != "paused" && status != "archived" {
		return nil, apperrors.Validation("status must be active, paused, or archived")
	}
	items, err := s.store.ListInvestmentSchedules(ctx, userID, status)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list investment schedules: %w", err))
	}
	for index := range items {
		items[index], err = s.decorateInvestmentSchedule(items[index])
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Service) GetInvestmentSchedule(ctx context.Context, userID, scheduleID int) (model.InvestmentSchedule, error) {
	if err := validateID(scheduleID); err != nil {
		return model.InvestmentSchedule{}, err
	}
	item, err := s.store.GetInvestmentSchedule(ctx, userID, scheduleID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.InvestmentSchedule{}, apperrors.NotFound("investment schedule not found")
	}
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("get investment schedule: %w", err))
	}
	return s.decorateInvestmentSchedule(item)
}

func (s *Service) UpdateInvestmentSchedule(
	ctx context.Context,
	userID, scheduleID int,
	request model.InvestmentScheduleRequest,
) (model.InvestmentSchedule, error) {
	if err := validateID(scheduleID); err != nil {
		return model.InvestmentSchedule{}, err
	}
	existing, err := s.store.GetInvestmentSchedule(ctx, userID, scheduleID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.InvestmentSchedule{}, apperrors.NotFound("investment schedule not found")
	}
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("get investment schedule for update: %w", err))
	}
	if existing.Status == "archived" {
		return model.InvestmentSchedule{}, apperrors.Conflict("archived investment schedules cannot be edited")
	}
	normalized, err := s.validateInvestmentSchedule(request, &existing)
	if err != nil {
		return model.InvestmentSchedule{}, err
	}
	item, err := s.store.UpdateInvestmentSchedule(ctx, userID, scheduleID, normalized)
	if errors.Is(err, repository.ErrNotFound) {
		return model.InvestmentSchedule{}, apperrors.NotFound("investment schedule not found")
	}
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("update investment schedule: %w", err))
	}
	return s.decorateInvestmentSchedule(item)
}

func (s *Service) PauseInvestmentSchedule(ctx context.Context, userID, scheduleID int) (model.InvestmentSchedule, error) {
	return s.setInvestmentScheduleStatus(ctx, userID, scheduleID, "paused")
}

func (s *Service) ResumeInvestmentSchedule(ctx context.Context, userID, scheduleID int) (model.InvestmentSchedule, error) {
	return s.setInvestmentScheduleStatus(ctx, userID, scheduleID, "active")
}

func (s *Service) setInvestmentScheduleStatus(
	ctx context.Context,
	userID, scheduleID int,
	status string,
) (model.InvestmentSchedule, error) {
	if err := validateID(scheduleID); err != nil {
		return model.InvestmentSchedule{}, err
	}
	item, err := s.store.GetInvestmentSchedule(ctx, userID, scheduleID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.InvestmentSchedule{}, apperrors.NotFound("investment schedule not found")
	}
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("get investment schedule: %w", err))
	}
	if item.Status == "archived" {
		return model.InvestmentSchedule{}, apperrors.Conflict("archived investment schedules cannot change status")
	}
	if err := s.store.SetInvestmentScheduleStatus(ctx, userID, scheduleID, status); errors.Is(err, repository.ErrNotFound) {
		return model.InvestmentSchedule{}, apperrors.NotFound("investment schedule not found")
	} else if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("set investment schedule status: %w", err))
	}
	return s.GetInvestmentSchedule(ctx, userID, scheduleID)
}

func (s *Service) DeleteInvestmentSchedule(ctx context.Context, userID, scheduleID int) error {
	if err := validateID(scheduleID); err != nil {
		return err
	}
	err := s.store.ArchiveInvestmentSchedule(ctx, userID, scheduleID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("investment schedule not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("archive investment schedule: %w", err))
	}
	return nil
}

func (s *Service) validateInvestmentSchedule(
	request model.InvestmentScheduleRequest,
	existing *model.InvestmentSchedule,
) (model.InvestmentScheduleRequest, error) {
	assetType, symbol, assetName, broker, err := normalizeInvestmentIdentity(
		request.AssetType, request.Symbol, request.AssetName, request.Broker,
	)
	if err != nil {
		return model.InvestmentScheduleRequest{}, err
	}
	amount, err := normalizeAmount(request.Amount)
	if err != nil {
		return model.InvestmentScheduleRequest{}, err
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = supportedCurrency
	}
	if currency != supportedCurrency {
		return model.InvestmentScheduleRequest{}, apperrors.Validation("currency must be EUR")
	}
	timezone := strings.TrimSpace(request.Timezone)
	if timezone == "" {
		timezone = defaultScheduleTimezone
	}
	today, err := scheduleLocalDate(s.now(), timezone)
	if err != nil || len(timezone) > 100 {
		return model.InvestmentScheduleRequest{}, apperrors.Validation("timezone must be a valid IANA timezone of 100 characters or less")
	}
	start, err := parseDate(request.StartDate, "start_date")
	if err != nil {
		return model.InvestmentScheduleRequest{}, err
	}
	if start.Before(today) && (existing == nil || request.StartDate != existing.StartDate) {
		return model.InvestmentScheduleRequest{}, apperrors.Validation("start_date cannot be in the past")
	}
	endDate := ""
	if strings.TrimSpace(request.EndDate) != "" {
		end, err := parseDate(request.EndDate, "end_date")
		if err != nil {
			return model.InvestmentScheduleRequest{}, err
		}
		if end.Before(start) {
			return model.InvestmentScheduleRequest{}, apperrors.Validation("end_date must be on or after start_date")
		}
		endDate = end.Format("2006-01-02")
	}
	frequency := strings.ToLower(strings.TrimSpace(request.Frequency))
	if frequency != "daily" && frequency != "weekly" && frequency != "monthly" {
		return model.InvestmentScheduleRequest{}, apperrors.Validation("frequency must be daily, weekly, or monthly")
	}
	interval := request.FrequencyInterval
	if interval == 0 {
		interval = 1
	}
	if interval < 1 || interval > maximumScheduleInterval {
		return model.InvestmentScheduleRequest{}, apperrors.Validation("frequency_interval must be between 1 and 365")
	}
	dayOfWeek, dayOfMonth := request.DayOfWeek, request.DayOfMonth
	switch frequency {
	case "daily":
		if dayOfWeek != nil || dayOfMonth != nil {
			return model.InvestmentScheduleRequest{}, apperrors.Validation("daily schedules cannot set day_of_week or day_of_month")
		}
	case "weekly":
		if dayOfMonth != nil {
			return model.InvestmentScheduleRequest{}, apperrors.Validation("weekly schedules cannot set day_of_month")
		}
		if dayOfWeek == nil {
			value := isoWeekday(start)
			dayOfWeek = &value
		}
		if *dayOfWeek < 1 || *dayOfWeek > 7 {
			return model.InvestmentScheduleRequest{}, apperrors.Validation("day_of_week must be between 1 and 7")
		}
	case "monthly":
		if dayOfWeek != nil {
			return model.InvestmentScheduleRequest{}, apperrors.Validation("monthly schedules cannot set day_of_week")
		}
		if dayOfMonth == nil {
			value := start.Day()
			dayOfMonth = &value
		}
		if *dayOfMonth < 1 || *dayOfMonth > 31 {
			return model.InvestmentScheduleRequest{}, apperrors.Validation("day_of_month must be between 1 and 31")
		}
	}
	return model.InvestmentScheduleRequest{
		AssetType: assetType, Symbol: symbol, AssetName: assetName, Broker: broker,
		Amount: amount, Currency: currency, Frequency: frequency, FrequencyInterval: interval,
		StartDate: start.Format("2006-01-02"), EndDate: endDate, DayOfWeek: dayOfWeek,
		DayOfMonth: dayOfMonth, Timezone: timezone,
	}, nil
}

func (s *Service) decorateInvestmentSchedule(item model.InvestmentSchedule) (model.InvestmentSchedule, error) {
	if item.Status != "active" {
		return item, nil
	}
	today, err := scheduleLocalDate(s.now(), item.Timezone)
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("load investment schedule timezone: %w", err))
	}
	from := today
	if item.LastNotifiedOn == today.Format("2006-01-02") {
		from = from.AddDate(0, 0, 1)
	}
	rule, err := investmentRecurrenceRule(item)
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("build investment recurrence: %w", err))
	}
	dates, err := recurrence.Occurrences(rule, from, from.AddDate(5, 0, 0))
	if err != nil {
		return model.InvestmentSchedule{}, apperrors.Internal(fmt.Errorf("calculate next investment occurrence: %w", err))
	}
	if len(dates) > 0 {
		item.NextOccurrence = dates[0].Format("2006-01-02")
	}
	return item, nil
}

func (s *Service) queueInvestmentReminders(ctx context.Context, now time.Time) (int, error) {
	items, err := s.store.ListActiveInvestmentSchedules(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, item := range items {
		today, err := scheduleLocalDate(now, item.Timezone)
		if err != nil {
			return queued, err
		}
		if item.LastNotifiedOn == today.Format("2006-01-02") {
			continue
		}
		rule, err := investmentRecurrenceRule(item)
		if err != nil {
			return queued, err
		}
		dates, err := recurrence.Occurrences(rule, today, today)
		if err != nil {
			return queued, err
		}
		if len(dates) == 0 {
			continue
		}
		inserted, err := s.store.QueueInvestmentReminder(ctx, item, today)
		if err != nil {
			return queued, err
		}
		if inserted {
			queued++
		}
	}
	return queued, nil
}
