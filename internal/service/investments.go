package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

const (
	maximumInvestmentAssetNameRunes = 100
	maximumInvestmentSymbolBytes    = 20
	maximumInvestmentTradeRows      = 10000
)

var investmentSymbolPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.\-]{0,19}$`)

func (s *Service) CreateInvestmentTrade(
	ctx context.Context,
	userID int,
	request model.InvestmentTradeRequest,
) (model.InvestmentTrade, error) {
	normalized, err := s.validateInvestmentTrade(request)
	if err != nil {
		return model.InvestmentTrade{}, err
	}
	if normalized.Side == "sell" {
		holding, err := s.store.InvestmentHoldingQuantity(ctx, userID, normalized.AssetType, normalized.Symbol, normalized.Broker)
		if err != nil {
			return model.InvestmentTrade{}, apperrors.Internal(fmt.Errorf("get investment holding: %w", err))
		}
		available, ok := new(big.Rat).SetString(holding)
		if !ok {
			return model.InvestmentTrade{}, apperrors.Internal(errors.New("stored investment holding is invalid"))
		}
		quantity, _ := new(big.Rat).SetString(normalized.Quantity)
		if available.Cmp(quantity) < 0 {
			return model.InvestmentTrade{}, apperrors.Validation("sell quantity cannot exceed the current holding for this broker")
		}
	}
	item, err := s.store.CreateInvestmentTrade(ctx, userID, normalized)
	if err != nil {
		return model.InvestmentTrade{}, apperrors.Internal(fmt.Errorf("create investment trade: %w", err))
	}
	return item, nil
}

func (s *Service) ListInvestmentTrades(
	ctx context.Context,
	userID int,
	fromString, throughString, assetType, symbol, broker string,
) ([]model.InvestmentTrade, error) {
	filter := repository.InvestmentTradeFilter{Limit: maximumInvestmentTradeRows + 1}
	var err error
	if strings.TrimSpace(fromString) != "" {
		filter.From, err = parseDate(fromString, "from")
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(throughString) != "" {
		filter.Through, err = parseDate(throughString, "through")
		if err != nil {
			return nil, err
		}
	}
	if !filter.From.IsZero() && !filter.Through.IsZero() && filter.Through.Before(filter.From) {
		return nil, apperrors.Validation("through must be on or after from")
	}
	if strings.TrimSpace(assetType) != "" {
		filter.AssetType, err = normalizeInvestmentAssetType(assetType)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(symbol) != "" {
		filter.Symbol, err = normalizeInvestmentSymbol(symbol)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(broker) != "" {
		filter.Broker, err = normalizeInvestmentBroker(broker, filter.AssetType)
		if err != nil {
			return nil, err
		}
	}
	items, err := s.store.ListInvestmentTrades(ctx, userID, filter)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list investment trades: %w", err))
	}
	if len(items) > maximumInvestmentTradeRows {
		return nil, apperrors.Validation("investment history contains more than 10000 trades; narrow the date range")
	}
	return items, nil
}

func (s *Service) DeleteInvestmentTrade(ctx context.Context, userID, tradeID int) error {
	if err := validateID(tradeID); err != nil {
		return err
	}
	trades, err := s.store.ListInvestmentTrades(ctx, userID, repository.InvestmentTradeFilter{Limit: maximumInvestmentTradeRows + 1})
	if err != nil {
		return apperrors.Internal(fmt.Errorf("validate investment trade deletion: %w", err))
	}
	found := false
	remaining := make([]model.InvestmentTrade, 0, len(trades)-1)
	for _, trade := range trades {
		if trade.ID == tradeID {
			found = true
			continue
		}
		remaining = append(remaining, trade)
	}
	if !found {
		return apperrors.NotFound("investment trade not found")
	}
	if err := validateInvestmentLedgerNeverNegative(remaining); err != nil {
		return apperrors.Conflict("this trade cannot be deleted because a later sale depends on it")
	}
	err = s.store.DeleteInvestmentTrade(ctx, userID, tradeID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("investment trade not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete investment trade: %w", err))
	}
	return nil
}
