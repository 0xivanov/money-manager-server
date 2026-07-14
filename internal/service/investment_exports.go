package service

import (
	"context"
	"fmt"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

func (s *Service) ExportInvestmentTrades(ctx context.Context, userID int, fromString, throughString string) ([]model.InvestmentTrade, error) {
	from, err := parseDate(fromString, "from")
	if err != nil {
		return nil, err
	}
	through, err := parseDate(throughString, "through")
	if err != nil {
		return nil, err
	}
	if through.Before(from) {
		return nil, apperrors.Validation("through must be on or after from")
	}
	if int(through.Sub(from).Hours()/24)+1 > maximumExportDays {
		return nil, apperrors.Validation("export date range must be 366 days or less")
	}
	items, err := s.store.ListInvestmentTrades(ctx, userID, repository.InvestmentTradeFilter{
		From: from, Through: through, Limit: maximumExportRows + 1,
	})
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("export investment trades: %w", err))
	}
	if len(items) > maximumExportRows {
		return nil, apperrors.Validation("export contains more than 5000 trades; narrow the date range")
	}
	return items, nil
}
