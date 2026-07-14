package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

const maximumBudgetNameRunes = 100

func (s *Service) ListBudgets(ctx context.Context, userID int, includeArchived bool) ([]model.Budget, error) {
	today, err := scheduleLocalDate(s.now(), defaultScheduleTimezone)
	if err != nil {
		return nil, apperrors.Internal(err)
	}
	items, err := s.store.ListBudgets(ctx, userID, today, includeArchived)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list budgets: %w", err))
	}
	return items, nil
}

func (s *Service) GetBudget(ctx context.Context, userID, budgetID int) (model.Budget, error) {
	if err := validateID(budgetID); err != nil {
		return model.Budget{}, err
	}
	today, err := scheduleLocalDate(s.now(), defaultScheduleTimezone)
	if err != nil {
		return model.Budget{}, apperrors.Internal(err)
	}
	item, err := s.store.GetBudget(ctx, userID, budgetID, today)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Budget{}, apperrors.NotFound("budget not found")
	}
	if err != nil {
		return model.Budget{}, apperrors.Internal(fmt.Errorf("get budget: %w", err))
	}
	return item, nil
}

func (s *Service) CreateBudget(ctx context.Context, userID int, request model.BudgetRequest) (model.Budget, error) {
	normalized, err := s.validateBudget(ctx, userID, request, nil)
	if err != nil {
		return model.Budget{}, err
	}
	today, _ := scheduleLocalDate(s.now(), defaultScheduleTimezone)
	item, err := s.store.CreateBudget(ctx, userID, normalized, today)
	if errors.Is(err, repository.ErrConflict) {
		return model.Budget{}, apperrors.Conflict("an active budget already exists for this category and period")
	}
	if err != nil {
		return model.Budget{}, apperrors.Internal(fmt.Errorf("create budget: %w", err))
	}
	return item, nil
}

func (s *Service) UpdateBudget(ctx context.Context, userID, budgetID int, request model.BudgetRequest) (model.Budget, error) {
	if err := validateID(budgetID); err != nil {
		return model.Budget{}, err
	}
	existing, err := s.GetBudget(ctx, userID, budgetID)
	if err != nil {
		return model.Budget{}, err
	}
	if existing.Status != "active" {
		return model.Budget{}, apperrors.Conflict("archived budgets cannot be edited")
	}
	normalized, err := s.validateBudget(ctx, userID, request, &existing)
	if err != nil {
		return model.Budget{}, err
	}
	today, _ := scheduleLocalDate(s.now(), defaultScheduleTimezone)
	item, err := s.store.UpdateBudget(ctx, userID, budgetID, normalized, today)
	if errors.Is(err, repository.ErrConflict) {
		return model.Budget{}, apperrors.Conflict("an active budget already exists for this category and period")
	}
	if errors.Is(err, repository.ErrNotFound) {
		return model.Budget{}, apperrors.NotFound("budget not found")
	}
	if err != nil {
		return model.Budget{}, apperrors.Internal(fmt.Errorf("update budget: %w", err))
	}
	return item, nil
}

func (s *Service) DeleteBudget(ctx context.Context, userID, budgetID int) error {
	if err := validateID(budgetID); err != nil {
		return err
	}
	err := s.store.ArchiveBudget(ctx, userID, budgetID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("budget not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("archive budget: %w", err))
	}
	return nil
}

func (s *Service) validateBudget(
	ctx context.Context,
	userID int,
	request model.BudgetRequest,
	existing *model.Budget,
) (model.BudgetRequest, error) {
	name, err := normalizeLimitedText(request.Name, "name", maximumBudgetNameRunes, false)
	if err != nil {
		return model.BudgetRequest{}, err
	}
	category := strings.TrimSpace(request.Category)
	if category != "" {
		category, err = normalizeLimitedText(category, "category", maximumCategoryRunes, false)
		if err != nil {
			return model.BudgetRequest{}, err
		}
		if existing != nil && strings.EqualFold(category, existing.Category) {
			category = existing.Category
		} else {
			category, err = s.store.FindActiveCategoryName(ctx, userID, "expense", category)
			if errors.Is(err, repository.ErrNotFound) {
				return model.BudgetRequest{}, apperrors.Validation("category must be an active expense category")
			}
			if err != nil {
				return model.BudgetRequest{}, apperrors.Internal(fmt.Errorf("validate budget category: %w", err))
			}
		}
	}
	amount, err := normalizeAmount(request.Amount)
	if err != nil {
		return model.BudgetRequest{}, err
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = supportedCurrency
	}
	if currency != supportedCurrency {
		return model.BudgetRequest{}, apperrors.Validation("currency must be EUR")
	}
	period := strings.ToLower(strings.TrimSpace(request.Period))
	if period != "weekly" && period != "monthly" {
		return model.BudgetRequest{}, apperrors.Validation("period must be weekly or monthly")
	}
	threshold := request.WarningThreshold
	if threshold == 0 {
		threshold = 80
	}
	if threshold < 1 || threshold > 100 {
		return model.BudgetRequest{}, apperrors.Validation("warning_threshold must be between 1 and 100")
	}
	return model.BudgetRequest{
		Name: name, Category: category, Amount: amount, Currency: currency,
		Period: period, WarningThreshold: threshold,
	}, nil
}

func (s *Service) queueBudgetAlerts(ctx context.Context, now time.Time) (int, error) {
	today, err := scheduleLocalDate(now, defaultScheduleTimezone)
	if err != nil {
		return 0, err
	}
	return s.store.QueueBudgetAlerts(ctx, today)
}
