package service

import (
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

func (s *Service) validateInvestmentTrade(request model.InvestmentTradeRequest) (model.InvestmentTradeRequest, error) {
	assetType, symbol, assetName, broker, err := normalizeInvestmentIdentity(
		request.AssetType, request.Symbol, request.AssetName, request.Broker,
	)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	if assetType != "crypto" {
		return model.InvestmentTradeRequest{}, apperrors.Validation("stock investments are temporarily unavailable")
	}
	side := strings.ToLower(strings.TrimSpace(request.Side))
	if side != "buy" && side != "sell" {
		return model.InvestmentTradeRequest{}, apperrors.Validation("side must be buy or sell")
	}
	amountValue := request.Amount
	if strings.TrimSpace(amountValue) == "" &&
		(strings.TrimSpace(request.Quantity) != "" || strings.TrimSpace(request.PricePerUnit) != "") {
		legacyQuantity, quantityErr := normalizeUnsignedDecimal(request.Quantity, "quantity", 18, 18, false)
		if quantityErr != nil {
			return model.InvestmentTradeRequest{}, quantityErr
		}
		legacyPrice, priceErr := normalizeUnsignedDecimal(request.PricePerUnit, "price_per_unit", 12, 8, false)
		if priceErr != nil {
			return model.InvestmentTradeRequest{}, priceErr
		}
		quantityNumber, _ := new(big.Rat).SetString(legacyQuantity)
		priceNumber, _ := new(big.Rat).SetString(legacyPrice)
		amountValue = new(big.Rat).Mul(quantityNumber, priceNumber).FloatString(2)
	}
	amount, err := normalizeAmount(amountValue)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	fees := strings.TrimSpace(request.Fees)
	if fees == "" {
		fees = "0.00"
	} else {
		fees, err = normalizeUnsignedDecimal(fees, "fees", 12, 2, true)
		if err != nil {
			return model.InvestmentTradeRequest{}, err
		}
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = supportedCurrency
	}
	if currency != supportedCurrency {
		return model.InvestmentTradeRequest{}, apperrors.Validation("currency must be EUR")
	}
	occurredAt, err := parseInvestmentOccurredAt(request.OccurredAt)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	if occurredAt.After(s.now().UTC()) {
		return model.InvestmentTradeRequest{}, apperrors.Validation("occurred_at cannot be in the future")
	}
	notes, err := normalizeLimitedText(request.Notes, "notes", maximumDescriptionRunes, true)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	return model.InvestmentTradeRequest{
		AssetType: assetType, Symbol: symbol, AssetName: assetName, Broker: broker, Side: side,
		Amount: amount, Fees: fees, Currency: currency,
		OccurredAt: occurredAt.UTC().Truncate(time.Second).Format(time.RFC3339), Notes: notes,
	}, nil
}

func parseInvestmentOccurredAt(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if timestamp, err := time.Parse(time.RFC3339, value); err == nil {
		return timestamp.UTC(), nil
	}
	if date, err := time.Parse("2006-01-02", value); err == nil && date.Format("2006-01-02") == value {
		return date.UTC(), nil
	}
	return time.Time{}, apperrors.Validation("occurred_at must use RFC3339 format")
}

func normalizeInvestmentIdentity(assetTypeValue, symbolValue, assetNameValue, brokerValue string) (string, string, string, string, error) {
	assetType, err := normalizeInvestmentAssetType(assetTypeValue)
	if err != nil {
		return "", "", "", "", err
	}
	symbol, err := normalizeInvestmentSymbol(symbolValue)
	if err != nil {
		return "", "", "", "", err
	}
	if err := validateCryptoSymbol(assetType, symbol); err != nil {
		return "", "", "", "", err
	}
	assetName := strings.TrimSpace(assetNameValue)
	if assetType == "crypto" {
		if symbol == "BTC" {
			assetName = "Bitcoin"
		} else {
			assetName = "Ethereum"
		}
	}
	assetName, err = normalizeLimitedText(assetName, "asset_name", maximumInvestmentAssetNameRunes, false)
	if err != nil {
		return "", "", "", "", err
	}
	broker, err := normalizeInvestmentBroker(brokerValue, assetType)
	if err != nil {
		return "", "", "", "", err
	}
	return assetType, symbol, assetName, broker, nil
}

func normalizeInvestmentAssetType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "crypto" && value != "stock" {
		return "", apperrors.Validation("asset_type must be crypto or stock")
	}
	return value, nil
}

func normalizeInvestmentSymbol(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) > maximumInvestmentSymbolBytes || !investmentSymbolPattern.MatchString(value) {
		return "", apperrors.Validation("symbol must contain 1 to 20 uppercase letters, numbers, dots, or hyphens")
	}
	return value, nil
}

func validateCryptoSymbol(assetType, symbol string) error {
	if assetType == "crypto" && symbol != "BTC" && symbol != "ETH" {
		return apperrors.Validation("crypto symbol must be BTC or ETH")
	}
	return nil
}

func normalizeInvestmentBroker(value, assetType string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "manual"
	}
	if value != "manual" && value != "revolut_x" && value != "trading212" {
		return "", apperrors.Validation("broker must be manual, revolut_x, or trading212")
	}
	if assetType == "crypto" && value == "trading212" {
		return "", apperrors.Validation("trading212 cannot be used for crypto holdings")
	}
	if assetType == "stock" && value == "revolut_x" {
		return "", apperrors.Validation("revolut_x cannot be used for stock holdings")
	}
	return value, nil
}

func normalizeUnsignedDecimal(value, field string, maximumIntegerDigits, maximumFractionDigits int, allowZero bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return "", apperrors.Validation(field + " must be a positive decimal number")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return "", apperrors.Validation(field + " must be a positive decimal number")
	}
	for _, part := range parts {
		for _, character := range part {
			if character < '0' || character > '9' {
				return "", apperrors.Validation(field + " must be a positive decimal number")
			}
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > maximumFractionDigits {
			return "", apperrors.Validation(fmt.Sprintf("%s must have at most %d decimal places", field, maximumFractionDigits))
		}
	}
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	if len(integer) > maximumIntegerDigits {
		return "", apperrors.Validation(field + " is too large")
	}
	fraction = strings.TrimRight(fraction, "0")
	result := integer
	if fraction != "" {
		result += "." + fraction
	}
	number, ok := new(big.Rat).SetString(result)
	if !ok || (!allowZero && number.Sign() == 0) {
		return "", apperrors.Validation(field + " must be greater than zero")
	}
	if allowZero && number.Sign() < 0 {
		return "", apperrors.Validation(field + " cannot be negative")
	}
	return result, nil
}
