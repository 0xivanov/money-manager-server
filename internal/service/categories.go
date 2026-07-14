package service

import (
	"context"
	"errors"
	"fmt"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

func (s *Service) ListCategories(ctx context.Context, userID int, transactionType string) ([]model.Category, error) {
	transactionType, err := normalizeTransactionType(transactionType)
	if err != nil {
		return nil, err
	}
	if err := s.store.EnsureDefaultCategories(ctx, userID); err != nil {
		return nil, apperrors.Internal(fmt.Errorf("ensure default categories: %w", err))
	}
	categories, err := s.store.ListCategories(ctx, userID, transactionType)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list categories: %w", err))
	}
	return categories, nil
}

func (s *Service) CreateCategory(ctx context.Context, userID int, request model.CategoryRequest) (model.Category, error) {
	transactionType, err := normalizeTransactionType(request.Type)
	if err != nil {
		return model.Category{}, err
	}
	name, err := normalizeLimitedText(request.Name, "category name", maximumCategoryRunes, false)
	if err != nil {
		return model.Category{}, err
	}
	request.Type = transactionType
	request.Name = name

	category, err := s.store.CreateCategory(ctx, userID, request)
	if errors.Is(err, repository.ErrConflict) {
		return model.Category{}, apperrors.Conflict("category already exists")
	}
	if err != nil {
		return model.Category{}, apperrors.Internal(fmt.Errorf("create category: %w", err))
	}
	return category, nil
}

func (s *Service) DeleteCategory(ctx context.Context, userID, categoryID int) error {
	if err := validateID(categoryID); err != nil {
		return err
	}
	err := s.store.DeleteCategory(ctx, userID, categoryID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("custom category not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete category: %w", err))
	}
	return nil
}
