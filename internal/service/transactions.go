package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

func (s *Service) ListTransactions(ctx context.Context, userID int, month, transactionType, category string) ([]model.Transaction, error) {
	_, from, to, err := parseMonth(month)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(transactionType) != "" {
		transactionType, err = normalizeTransactionType(transactionType)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(category) != "" {
		category, err = normalizeLimitedText(category, "category", maximumCategoryRunes, false)
		if err != nil {
			return nil, err
		}
	}
	transactions, err := s.store.ListTransactions(ctx, userID, repository.TransactionFilter{
		From: from, To: to, Type: transactionType, Category: category,
	})
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list transactions: %w", err))
	}
	return transactions, nil
}

func (s *Service) CreateTransaction(ctx context.Context, userID int, request model.TransactionRequest) (model.Transaction, error) {
	normalized, err := s.validateTransaction(ctx, userID, request, nil)
	if err != nil {
		return model.Transaction{}, err
	}
	transaction, err := s.store.CreateTransaction(ctx, userID, normalized)
	if err != nil {
		return model.Transaction{}, apperrors.Internal(fmt.Errorf("create transaction: %w", err))
	}
	return transaction, nil
}

func (s *Service) UpdateTransaction(ctx context.Context, userID, transactionID int, request model.TransactionRequest) (model.Transaction, error) {
	if err := validateID(transactionID); err != nil {
		return model.Transaction{}, err
	}
	existing, err := s.store.GetTransaction(ctx, userID, transactionID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Transaction{}, apperrors.NotFound("transaction not found")
	}
	if err != nil {
		return model.Transaction{}, apperrors.Internal(fmt.Errorf("get transaction for update: %w", err))
	}
	normalized, err := s.validateTransaction(ctx, userID, request, &existing)
	if err != nil {
		return model.Transaction{}, err
	}
	transaction, err := s.store.UpdateTransaction(ctx, userID, transactionID, normalized)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Transaction{}, apperrors.NotFound("transaction not found")
	}
	if err != nil {
		return model.Transaction{}, apperrors.Internal(fmt.Errorf("update transaction: %w", err))
	}
	return transaction, nil
}

func (s *Service) DeleteTransaction(ctx context.Context, userID, transactionID int) error {
	if err := validateID(transactionID); err != nil {
		return err
	}
	err := s.store.DeleteTransaction(ctx, userID, transactionID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("transaction not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete transaction: %w", err))
	}
	return nil
}

func (s *Service) Summary(ctx context.Context, userID int, month string) (model.Summary, error) {
	monthKey, from, to, err := parseMonth(month)
	if err != nil {
		return model.Summary{}, err
	}
	summary, err := s.store.Summary(ctx, userID, monthKey, from, to)
	if err != nil {
		return model.Summary{}, apperrors.Internal(fmt.Errorf("summarize transactions: %w", err))
	}
	return summary, nil
}

func (s *Service) ExportTransactions(ctx context.Context, userID int, fromString, toString string) ([]model.Transaction, error) {
	from, err := parseDate(fromString, "from")
	if err != nil {
		return nil, err
	}
	to, err := parseDate(toString, "to")
	if err != nil {
		return nil, err
	}
	if from.After(to) {
		return nil, apperrors.Validation("from must be before or equal to to")
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > maximumExportDays {
		return nil, apperrors.Validation("export date range must be 366 days or less")
	}
	transactions, err := s.store.ExportTransactions(ctx, userID, from, to.AddDate(0, 0, 1), maximumExportRows+1)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("export transactions: %w", err))
	}
	if len(transactions) > maximumExportRows {
		return nil, apperrors.Validation("export contains more than 5000 transactions; narrow the date range")
	}
	return transactions, nil
}

func (s *Service) validateTransaction(ctx context.Context, userID int, request model.TransactionRequest, existing *model.Transaction) (model.TransactionRequest, error) {
	transactionType, err := normalizeTransactionType(request.Type)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	category, err := normalizeLimitedText(request.Category, "category", maximumCategoryRunes, false)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	canonicalCategory := ""
	if existing != nil && transactionType == existing.Type && strings.EqualFold(category, existing.Category) {
		canonicalCategory = existing.Category
	} else {
		canonicalCategory, err = s.store.FindActiveCategoryName(ctx, userID, transactionType, category)
		if errors.Is(err, repository.ErrNotFound) {
			return model.TransactionRequest{}, apperrors.Validation("category must be active and match the transaction type")
		}
		if err != nil {
			return model.TransactionRequest{}, apperrors.Internal(fmt.Errorf("validate category: %w", err))
		}
	}
	amount, err := normalizeAmount(request.Amount)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = supportedCurrency
	}
	if currency != supportedCurrency {
		return model.TransactionRequest{}, apperrors.Validation("currency must be EUR")
	}
	date, err := parseDate(request.OccurredAt, "occurred_at")
	if err != nil {
		return model.TransactionRequest{}, err
	}
	description, err := normalizeLimitedText(request.Description, "description", maximumDescriptionRunes, true)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	return model.TransactionRequest{
		Type: transactionType, Category: canonicalCategory, Description: description,
		Amount: amount, Currency: currency, OccurredAt: date.Format("2006-01-02"),
		ExcludedFromBudget: request.ExcludedFromBudget,
	}, nil
}
