package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/recurrence"
	"money-manager-server/internal/repository"
)

const (
	defaultScheduleTimezone    = "Europe/Sofia"
	maximumScheduleNameRunes   = 100
	maximumScheduleInterval    = 365
	maximumOccurrenceRangeDays = 366
	schedulePostingBatchSize   = 500
)

func (s *Service) CreateTransactionSchedule(
	ctx context.Context,
	userID int,
	request model.TransactionScheduleRequest,
) (model.TransactionSchedule, error) {
	normalized, today, err := s.validateTransactionSchedule(ctx, userID, request, nil)
	if err != nil {
		return model.TransactionSchedule{}, err
	}
	schedule, err := s.store.CreateTransactionSchedule(ctx, userID, normalized)
	if err != nil {
		return model.TransactionSchedule{}, apperrors.Internal(fmt.Errorf("create transaction schedule: %w", err))
	}
	if _, err := s.materializeTransactionSchedule(ctx, schedule, today); err != nil {
		return model.TransactionSchedule{}, err
	}
	return s.getTransactionSchedule(ctx, userID, schedule.ID)
}

func (s *Service) ListTransactionSchedules(
	ctx context.Context,
	userID int,
	status string,
) ([]model.TransactionSchedule, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && status != "active" && status != "paused" && status != "archived" {
		return nil, apperrors.Validation("status must be active, paused, or archived")
	}
	items, err := s.store.ListTransactionSchedules(ctx, userID, status, s.now().UTC())
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list transaction schedules: %w", err))
	}
	return items, nil
}

func (s *Service) GetTransactionSchedule(
	ctx context.Context,
	userID, scheduleID int,
) (model.TransactionSchedule, error) {
	if err := validateID(scheduleID); err != nil {
		return model.TransactionSchedule{}, err
	}
	return s.getTransactionSchedule(ctx, userID, scheduleID)
}

func (s *Service) getTransactionSchedule(
	ctx context.Context,
	userID, scheduleID int,
) (model.TransactionSchedule, error) {
	item, err := s.store.GetTransactionSchedule(ctx, userID, scheduleID, s.now().UTC())
	if errors.Is(err, repository.ErrNotFound) {
		return model.TransactionSchedule{}, apperrors.NotFound("transaction schedule not found")
	}
	if err != nil {
		return model.TransactionSchedule{}, apperrors.Internal(fmt.Errorf("get transaction schedule: %w", err))
	}
	return item, nil
}

func (s *Service) UpdateTransactionSchedule(
	ctx context.Context,
	userID, scheduleID int,
	request model.TransactionScheduleRequest,
) (model.TransactionSchedule, error) {
	if err := validateID(scheduleID); err != nil {
		return model.TransactionSchedule{}, err
	}
	existing, err := s.store.GetTransactionSchedule(ctx, userID, scheduleID, s.now().UTC())
	if errors.Is(err, repository.ErrNotFound) {
		return model.TransactionSchedule{}, apperrors.NotFound("transaction schedule not found")
	}
	if err != nil {
		return model.TransactionSchedule{}, apperrors.Internal(fmt.Errorf("get transaction schedule for update: %w", err))
	}
	if existing.Status == "archived" {
		return model.TransactionSchedule{}, apperrors.Conflict("archived transaction schedules cannot be edited")
	}
	normalized, today, err := s.validateTransactionSchedule(ctx, userID, request, &existing)
	if err != nil {
		return model.TransactionSchedule{}, err
	}
	updated, err := s.store.UpdateTransactionSchedule(ctx, userID, scheduleID, normalized, today)
	if errors.Is(err, repository.ErrNotFound) {
		return model.TransactionSchedule{}, apperrors.NotFound("transaction schedule not found")
	}
	if err != nil {
		return model.TransactionSchedule{}, apperrors.Internal(fmt.Errorf("update transaction schedule: %w", err))
	}
	if updated.Status == "active" {
		if _, err := s.materializeTransactionSchedule(ctx, updated, today); err != nil {
			return model.TransactionSchedule{}, err
		}
	}
	return s.getTransactionSchedule(ctx, userID, scheduleID)
}

func (s *Service) PauseTransactionSchedule(ctx context.Context, userID, scheduleID int) (model.TransactionSchedule, error) {
	return s.setTransactionSchedulePaused(ctx, userID, scheduleID, true)
}

func (s *Service) ResumeTransactionSchedule(ctx context.Context, userID, scheduleID int) (model.TransactionSchedule, error) {
	return s.setTransactionSchedulePaused(ctx, userID, scheduleID, false)
}

func (s *Service) setTransactionSchedulePaused(
	ctx context.Context,
	userID, scheduleID int,
	paused bool,
) (model.TransactionSchedule, error) {
	if err := validateID(scheduleID); err != nil {
		return model.TransactionSchedule{}, err
	}
	item, err := s.store.GetTransactionSchedule(ctx, userID, scheduleID, s.now().UTC())
	if errors.Is(err, repository.ErrNotFound) {
		return model.TransactionSchedule{}, apperrors.NotFound("transaction schedule not found")
	}
	if err != nil {
		return model.TransactionSchedule{}, apperrors.Internal(fmt.Errorf("get transaction schedule: %w", err))
	}
	if item.Status == "archived" {
		return model.TransactionSchedule{}, apperrors.Conflict("archived transaction schedules cannot change status")
	}
	status := "active"
	if paused {
		status = "paused"
	}
	if err := s.store.SetTransactionScheduleStatus(ctx, userID, scheduleID, status); errors.Is(err, repository.ErrNotFound) {
		return model.TransactionSchedule{}, apperrors.NotFound("transaction schedule not found")
	} else if err != nil {
		return model.TransactionSchedule{}, apperrors.Internal(fmt.Errorf("set transaction schedule status: %w", err))
	}
	if !paused {
		item.Status = "active"
		today, err := scheduleLocalDate(s.now(), item.Timezone)
		if err != nil {
			return model.TransactionSchedule{}, apperrors.Internal(err)
		}
		if _, err := s.materializeTransactionSchedule(ctx, item, today); err != nil {
			return model.TransactionSchedule{}, err
		}
	}
	return s.getTransactionSchedule(ctx, userID, scheduleID)
}

func (s *Service) DeleteTransactionSchedule(ctx context.Context, userID, scheduleID int) error {
	if err := validateID(scheduleID); err != nil {
		return err
	}
	err := s.store.ArchiveTransactionSchedule(ctx, userID, scheduleID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("transaction schedule not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("archive transaction schedule: %w", err))
	}
	return nil
}

func (s *Service) ListTransactionScheduleOccurrences(
	ctx context.Context,
	userID int,
	fromString, throughString string,
	scheduleID int,
	status string,
) ([]model.TransactionScheduleOccurrence, error) {
	today, err := scheduleLocalDate(s.now(), defaultScheduleTimezone)
	if err != nil {
		return nil, apperrors.Internal(err)
	}
	from := today
	if strings.TrimSpace(fromString) != "" {
		from, err = parseDate(fromString, "from")
		if err != nil {
			return nil, err
		}
	}
	through := from.AddDate(0, 0, 30)
	if strings.TrimSpace(throughString) != "" {
		through, err = parseDate(throughString, "through")
		if err != nil {
			return nil, err
		}
	}
	if through.Before(from) {
		return nil, apperrors.Validation("through must be on or after from")
	}
	if int(through.Sub(from).Hours()/24)+1 > maximumOccurrenceRangeDays {
		return nil, apperrors.Validation("occurrence date range must be 366 days or less")
	}
	if scheduleID < 0 {
		return nil, apperrors.Validation("schedule_id must be a positive integer")
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "planned"
	}
	if status != "planned" && status != "posted" && status != "skipped" {
		return nil, apperrors.Validation("status must be planned, posted, or skipped")
	}
	items, err := s.store.ListTransactionScheduleOccurrences(ctx, userID, repository.ScheduleOccurrenceFilter{
		From: from, Through: through, ScheduleID: scheduleID, Status: status,
	})
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list transaction schedule occurrences: %w", err))
	}
	return items, nil
}

func (s *Service) RunScheduledTransactionMaintenance(ctx context.Context) (model.ScheduleMaintenanceResult, error) {
	schedules, err := s.store.ListActiveTransactionSchedules(ctx)
	if err != nil {
		return model.ScheduleMaintenanceResult{}, apperrors.Internal(fmt.Errorf("list active transaction schedules: %w", err))
	}
	result := model.ScheduleMaintenanceResult{}
	now := s.now().UTC()
	for _, schedule := range schedules {
		today, err := scheduleLocalDate(now, schedule.Timezone)
		if err != nil {
			return model.ScheduleMaintenanceResult{}, apperrors.Internal(fmt.Errorf("load schedule timezone: %w", err))
		}
		count, err := s.materializeTransactionSchedule(ctx, schedule, today)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return model.ScheduleMaintenanceResult{}, err
		}
		result.Materialized += count
	}
	posted, err := s.store.PostDueTransactionScheduleOccurrences(ctx, now, schedulePostingBatchSize)
	if err != nil {
		return model.ScheduleMaintenanceResult{}, apperrors.Internal(fmt.Errorf("post due transaction schedule occurrences: %w", err))
	}
	result.Posted = posted
	scheduleReminders, err := s.store.QueueDueTransactionScheduleReminders(ctx, now, schedulePostingBatchSize)
	if err != nil {
		return model.ScheduleMaintenanceResult{}, apperrors.Internal(fmt.Errorf("queue scheduled money reminders: %w", err))
	}
	result.ScheduleReminders = scheduleReminders
	budgetAlerts, err := s.queueBudgetAlerts(ctx, now)
	if err != nil {
		return model.ScheduleMaintenanceResult{}, apperrors.Internal(fmt.Errorf("queue budget alerts: %w", err))
	}
	result.BudgetAlerts = budgetAlerts
	investmentReminders, err := s.queueInvestmentReminders(ctx, now)
	if err != nil {
		return model.ScheduleMaintenanceResult{}, apperrors.Internal(fmt.Errorf("queue investment reminders: %w", err))
	}
	result.InvestmentReminders = investmentReminders
	return result, nil
}

func (s *Service) materializeTransactionSchedule(
	ctx context.Context,
	schedule model.TransactionSchedule,
	today time.Time,
) (int, error) {
	from, err := parseDate(schedule.StartDate, "start_date")
	if err != nil {
		return 0, apperrors.Internal(fmt.Errorf("parse stored schedule start date: %w", err))
	}
	if schedule.MaterializedThrough != "" {
		materializedThrough, err := parseDate(schedule.MaterializedThrough, "materialized_through")
		if err != nil {
			return 0, apperrors.Internal(fmt.Errorf("parse stored materialization date: %w", err))
		}
		from = materializedThrough.AddDate(0, 0, 1)
	}
	if from.Before(today) {
		from = today
	}
	through := today.AddDate(0, 0, s.scheduleHorizonDays)
	if from.After(through) {
		return 0, nil
	}
	rule, err := recurrenceRule(schedule)
	if err != nil {
		return 0, apperrors.Internal(fmt.Errorf("build recurrence rule: %w", err))
	}
	dates, err := recurrence.Occurrences(rule, from, through)
	if err != nil {
		return 0, apperrors.Internal(fmt.Errorf("generate transaction schedule occurrences: %w", err))
	}
	seeds := make([]repository.ScheduleOccurrenceSeed, 0, len(dates))
	for _, date := range dates {
		seeds = append(seeds, repository.ScheduleOccurrenceSeed{
			ScheduleID: schedule.ID, UserID: schedule.UserID, ScheduledFor: date,
			Type: schedule.Type, Name: schedule.Name, Category: schedule.Category,
			Description: schedule.Description, Amount: schedule.Amount, Currency: schedule.Currency,
			AutoPost: schedule.AutoPost,
		})
	}
	inserted, err := s.store.UpsertTransactionScheduleOccurrences(ctx, seeds)
	if err != nil {
		return 0, apperrors.Internal(fmt.Errorf("store transaction schedule occurrences: %w", err))
	}
	if err := s.store.MarkTransactionScheduleMaterializedThrough(ctx, schedule.ID, through); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return inserted, repository.ErrNotFound
		}
		return 0, apperrors.Internal(fmt.Errorf("mark transaction schedule materialized: %w", err))
	}
	return inserted, nil
}

func recurrenceRule(schedule model.TransactionSchedule) (recurrence.Rule, error) {
	start, err := time.Parse("2006-01-02", schedule.StartDate)
	if err != nil {
		return recurrence.Rule{}, err
	}
	var end *time.Time
	if schedule.EndDate != "" {
		value, err := time.Parse("2006-01-02", schedule.EndDate)
		if err != nil {
			return recurrence.Rule{}, err
		}
		end = &value
	}
	rule := recurrence.Rule{
		Frequency: schedule.Frequency, Interval: schedule.FrequencyInterval,
		StartDate: start, EndDate: end,
	}
	if schedule.DayOfWeek != nil {
		rule.DayOfWeek = *schedule.DayOfWeek
	}
	if schedule.DayOfMonth != nil {
		rule.DayOfMonth = *schedule.DayOfMonth
	}
	return rule, nil
}

func (s *Service) validateTransactionSchedule(
	ctx context.Context,
	userID int,
	request model.TransactionScheduleRequest,
	existing *model.TransactionSchedule,
) (model.TransactionScheduleRequest, time.Time, error) {
	transactionType, err := normalizeTransactionType(request.Type)
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, err
	}
	name, err := normalizeLimitedText(request.Name, "name", maximumScheduleNameRunes, false)
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, err
	}
	category, err := normalizeLimitedText(request.Category, "category", maximumCategoryRunes, false)
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, err
	}
	canonicalCategory := ""
	if existing != nil && transactionType == existing.Type && strings.EqualFold(category, existing.Category) {
		canonicalCategory = existing.Category
	} else {
		canonicalCategory, err = s.store.FindActiveCategoryName(ctx, userID, transactionType, category)
		if errors.Is(err, repository.ErrNotFound) {
			return model.TransactionScheduleRequest{}, time.Time{}, apperrors.Validation("category must be active and match the schedule type")
		}
		if err != nil {
			return model.TransactionScheduleRequest{}, time.Time{}, apperrors.Internal(fmt.Errorf("validate schedule category: %w", err))
		}
	}
	description, err := normalizeLimitedText(request.Description, "description", maximumDescriptionRunes, true)
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, err
	}
	amount, err := normalizeAmount(request.Amount)
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, err
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = supportedCurrency
	}
	if currency != supportedCurrency {
		return model.TransactionScheduleRequest{}, time.Time{}, apperrors.Validation("currency must be EUR")
	}
	timezone := strings.TrimSpace(request.Timezone)
	if timezone == "" {
		timezone = defaultScheduleTimezone
	}
	today, err := scheduleLocalDate(s.now(), timezone)
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, apperrors.Validation("timezone must be a valid IANA timezone")
	}
	start, err := parseDate(request.StartDate, "start_date")
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, err
	}
	if start.Before(today) && (existing == nil || request.StartDate != existing.StartDate) {
		return model.TransactionScheduleRequest{}, time.Time{}, apperrors.Validation("start_date cannot be in the past")
	}
	endDate := ""
	if strings.TrimSpace(request.EndDate) != "" {
		end, err := parseDate(request.EndDate, "end_date")
		if err != nil {
			return model.TransactionScheduleRequest{}, time.Time{}, err
		}
		if end.Before(start) {
			return model.TransactionScheduleRequest{}, time.Time{}, apperrors.Validation("end_date must be on or after start_date")
		}
		endDate = end.Format("2006-01-02")
	}
	recurrence, err := normalizeScheduleRecurrence(
		start,
		request.Frequency,
		request.FrequencyInterval,
		request.DayOfWeek,
		request.DayOfMonth,
	)
	if err != nil {
		return model.TransactionScheduleRequest{}, time.Time{}, err
	}
	return model.TransactionScheduleRequest{
		Type: transactionType, Name: name, Category: canonicalCategory, Description: description,
		Amount: amount, Currency: currency, Frequency: recurrence.frequency, FrequencyInterval: recurrence.interval,
		StartDate: start.Format("2006-01-02"), EndDate: endDate, DayOfWeek: recurrence.dayOfWeek,
		DayOfMonth: recurrence.dayOfMonth, Timezone: timezone, AutoPost: request.AutoPost,
	}, today, nil
}
