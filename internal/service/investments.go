package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

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
	if s.marketData == nil {
		return model.InvestmentTrade{}, apperrors.Unavailable(
			"crypto market pricing is temporarily unavailable", errors.New("market data client is not configured"),
		)
	}
	occurredAt, err := time.Parse(time.RFC3339, normalized.OccurredAt)
	if err != nil {
		return model.InvestmentTrade{}, apperrors.Internal(fmt.Errorf("parse normalized investment timestamp: %w", err))
	}
	quote, err := s.marketData.QuoteAt(ctx, normalized.Symbol, normalized.Currency, occurredAt)
	if err != nil {
		return model.InvestmentTrade{}, apperrors.Unavailable("crypto market pricing is temporarily unavailable", err)
	}
	price, err := normalizeUnsignedDecimal(quote.Price, "market price", 12, 8, false)
	if err != nil {
		return model.InvestmentTrade{}, apperrors.Unavailable("crypto market pricing returned an invalid price", err)
	}
	provider, err := normalizeLimitedText(quote.Provider, "market price provider", 100, false)
	if err != nil || quote.AsOf.IsZero() {
		return model.InvestmentTrade{}, apperrors.Unavailable(
			"crypto market pricing returned an invalid quote", errors.New("market quote audit data is missing"),
		)
	}
	amountNumber, _ := new(big.Rat).SetString(normalized.Amount)
	priceNumber, _ := new(big.Rat).SetString(price)
	quantityNumber := new(big.Rat).Quo(amountNumber, priceNumber)
	quantity := formatRatTrimmed(quantityNumber, 18)
	if quantity == "0" {
		return model.InvestmentTrade{}, apperrors.Validation("amount is too small for the selected asset")
	}
	normalized.Quantity = quantity
	normalized.PricePerUnit = price
	normalized.PriceProvider = provider
	normalized.PriceAsOf = quote.AsOf.UTC().Truncate(time.Second).Format(time.RFC3339)
	if normalized.Side == "sell" {
		fees, _ := new(big.Rat).SetString(normalized.Fees)
		if fees.Cmp(amountNumber) >= 0 {
			return model.InvestmentTrade{}, apperrors.Validation("sell fees must be less than the sold amount")
		}
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
	if errors.Is(err, repository.ErrConflict) {
		return model.InvestmentTrade{}, apperrors.Validation("sell amount cannot exceed the holding available at that time")
	}
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
		filter.Through = filter.Through.AddDate(0, 0, 1)
	}
	if !filter.From.IsZero() && !filter.Through.IsZero() && !filter.Through.After(filter.From) {
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
	err := s.store.DeleteInvestmentTrade(ctx, userID, tradeID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("investment trade not found")
	}
	if errors.Is(err, repository.ErrConflict) {
		return apperrors.Conflict("this trade cannot be deleted because a later sale depends on it")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete investment trade: %w", err))
	}
	return nil
}
