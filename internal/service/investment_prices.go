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

func (s *Service) SetManualInvestmentPrice(
	ctx context.Context,
	userID int,
	request model.InvestmentPriceRequest,
) (model.InvestmentPrice, error) {
	assetType, err := normalizeInvestmentAssetType(request.AssetType)
	if err != nil {
		return model.InvestmentPrice{}, err
	}
	symbol, err := normalizeInvestmentSymbol(request.Symbol)
	if err != nil {
		return model.InvestmentPrice{}, err
	}
	if err := validateCryptoSymbol(assetType, symbol); err != nil {
		return model.InvestmentPrice{}, err
	}
	if assetType == "crypto" {
		return model.InvestmentPrice{}, apperrors.Validation(
			"manual prices are unavailable for crypto because crypto prices are provided automatically",
		)
	}
	price, err := normalizeUnsignedDecimal(request.Price, "price", 12, 8, false)
	if err != nil {
		return model.InvestmentPrice{}, err
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = supportedCurrency
	}
	if currency != supportedCurrency {
		return model.InvestmentPrice{}, apperrors.Validation("currency must be EUR")
	}
	now := s.now().UTC()
	asOf := now.Truncate(time.Second)
	if strings.TrimSpace(request.AsOf) != "" {
		parsedAsOf, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(request.AsOf))
		if parseErr != nil {
			return model.InvestmentPrice{}, apperrors.Validation("as_of must use RFC3339 format")
		}
		parsedAsOf = parsedAsOf.UTC()
		if parsedAsOf.After(now) {
			return model.InvestmentPrice{}, apperrors.Validation("as_of cannot be in the future")
		}
		asOf = parsedAsOf.Truncate(time.Second)
	}
	request.AssetType, request.Symbol, request.Price, request.Currency = assetType, symbol, price, currency
	item, err := s.store.UpsertManualInvestmentPrice(ctx, userID, request, asOf)
	if errors.Is(err, repository.ErrNotFound) {
		return model.InvestmentPrice{}, apperrors.NotFound("add a trade for this asset before setting its price")
	}
	if err != nil {
		return model.InvestmentPrice{}, apperrors.Internal(fmt.Errorf("set manual investment price: %w", err))
	}
	return item, nil
}
