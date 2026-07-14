package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/recurrence"
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

func (s *Service) InvestmentPortfolio(ctx context.Context, userID int) (model.InvestmentPortfolio, error) {
	trades, err := s.store.ListInvestmentTrades(ctx, userID, repository.InvestmentTradeFilter{Limit: maximumInvestmentTradeRows + 1})
	if err != nil {
		return model.InvestmentPortfolio{}, apperrors.Internal(fmt.Errorf("list portfolio trades: %w", err))
	}
	if len(trades) > maximumInvestmentTradeRows {
		return model.InvestmentPortfolio{}, apperrors.Validation("portfolio contains more than 10000 trades")
	}
	prices, err := s.store.ListInvestmentPrices(ctx)
	if err != nil {
		return model.InvestmentPortfolio{}, apperrors.Internal(fmt.Errorf("list investment prices: %w", err))
	}
	return calculateInvestmentPortfolio(trades, prices)
}

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
	asOf := s.now().UTC().Truncate(time.Second)
	if strings.TrimSpace(request.AsOf) != "" {
		asOf, err = time.Parse(time.RFC3339, strings.TrimSpace(request.AsOf))
		if err != nil {
			return model.InvestmentPrice{}, apperrors.Validation("as_of must use RFC3339 format")
		}
		asOf = asOf.UTC().Truncate(time.Second)
		if asOf.After(s.now().UTC().Add(5 * time.Minute)) {
			return model.InvestmentPrice{}, apperrors.Validation("as_of cannot be in the future")
		}
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

func (s *Service) validateInvestmentTrade(request model.InvestmentTradeRequest) (model.InvestmentTradeRequest, error) {
	assetType, symbol, assetName, broker, err := normalizeInvestmentIdentity(
		request.AssetType, request.Symbol, request.AssetName, request.Broker,
	)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	side := strings.ToLower(strings.TrimSpace(request.Side))
	if side != "buy" && side != "sell" {
		return model.InvestmentTradeRequest{}, apperrors.Validation("side must be buy or sell")
	}
	quantity, err := normalizeUnsignedDecimal(request.Quantity, "quantity", 18, 10, false)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	price, err := normalizeUnsignedDecimal(request.PricePerUnit, "price_per_unit", 12, 8, false)
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
	date, err := parseDate(request.OccurredAt, "occurred_at")
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	today, err := scheduleLocalDate(s.now(), defaultScheduleTimezone)
	if err != nil {
		return model.InvestmentTradeRequest{}, apperrors.Internal(fmt.Errorf("load default timezone: %w", err))
	}
	if date.After(today) {
		return model.InvestmentTradeRequest{}, apperrors.Validation("occurred_at cannot be in the future")
	}
	notes, err := normalizeLimitedText(request.Notes, "notes", maximumDescriptionRunes, true)
	if err != nil {
		return model.InvestmentTradeRequest{}, err
	}
	return model.InvestmentTradeRequest{
		AssetType: assetType, Symbol: symbol, AssetName: assetName, Broker: broker, Side: side,
		Quantity: quantity, PricePerUnit: price, Fees: fees, Currency: currency,
		OccurredAt: date.Format("2006-01-02"), Notes: notes,
	}, nil
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

type investmentLedgerPosition struct {
	assetType, symbol, assetName, broker string
	quantity, basis, realized            *big.Rat
}

func calculateInvestmentPortfolio(trades []model.InvestmentTrade, prices []model.InvestmentPrice) (model.InvestmentPortfolio, error) {
	ordered := append([]model.InvestmentTrade(nil), trades...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].OccurredAt == ordered[j].OccurredAt {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].OccurredAt < ordered[j].OccurredAt
	})
	ledgers := make(map[string]*investmentLedgerPosition)
	keys := make([]string, 0)
	for _, trade := range ordered {
		key := trade.AssetType + "\x00" + trade.Symbol + "\x00" + trade.Broker
		position := ledgers[key]
		if position == nil {
			position = &investmentLedgerPosition{
				assetType: trade.AssetType, symbol: trade.Symbol, assetName: trade.AssetName, broker: trade.Broker,
				quantity: new(big.Rat), basis: new(big.Rat), realized: new(big.Rat),
			}
			ledgers[key] = position
			keys = append(keys, key)
		}
		quantity, quantityOK := new(big.Rat).SetString(trade.Quantity)
		price, priceOK := new(big.Rat).SetString(trade.PricePerUnit)
		fees, feesOK := new(big.Rat).SetString(trade.Fees)
		if !quantityOK || !priceOK || !feesOK {
			return model.InvestmentPortfolio{}, apperrors.Internal(errors.New("stored investment trade contains invalid decimals"))
		}
		gross := new(big.Rat).Mul(quantity, price)
		if trade.Side == "buy" {
			position.quantity.Add(position.quantity, quantity)
			position.basis.Add(position.basis, new(big.Rat).Add(gross, fees))
			continue
		}
		if position.quantity.Cmp(quantity) < 0 || position.quantity.Sign() == 0 {
			return model.InvestmentPortfolio{}, apperrors.Internal(errors.New("investment ledger contains a sale larger than its holding"))
		}
		costRemoved := new(big.Rat).Mul(position.basis, new(big.Rat).Quo(quantity, position.quantity))
		proceeds := new(big.Rat).Sub(gross, fees)
		position.realized.Add(position.realized, new(big.Rat).Sub(proceeds, costRemoved))
		position.basis.Sub(position.basis, costRemoved)
		position.quantity.Sub(position.quantity, quantity)
		if position.quantity.Sign() == 0 {
			position.basis.SetInt64(0)
		}
	}
	priceMap := make(map[string]model.InvestmentPrice, len(prices))
	for _, price := range prices {
		priceMap[price.AssetType+"\x00"+price.Symbol] = price
	}
	sort.Strings(keys)
	portfolio := model.InvestmentPortfolio{
		Positions: make([]model.InvestmentPosition, 0, len(keys)), Currency: supportedCurrency,
		InvestedAmount: "0.00", CurrentValue: "0.00", UnrealizedProfit: "0.00", RealizedProfit: "0.00",
	}
	totalBasis, totalValue, totalRealized := new(big.Rat), new(big.Rat), new(big.Rat)
	for _, key := range keys {
		ledger := ledgers[key]
		average := new(big.Rat)
		if ledger.quantity.Sign() > 0 {
			average.Quo(ledger.basis, ledger.quantity)
		}
		position := model.InvestmentPosition{
			AssetType: ledger.assetType, Symbol: ledger.symbol, AssetName: ledger.assetName,
			Broker: ledger.broker, Quantity: formatRatTrimmed(ledger.quantity, 10),
			AverageCost: formatRat(average, 8), InvestedAmount: formatRat(ledger.basis, 2),
			RealizedProfit: formatRat(ledger.realized, 2), Currency: supportedCurrency, PriceStatus: "missing",
		}
		totalBasis.Add(totalBasis, ledger.basis)
		totalRealized.Add(totalRealized, ledger.realized)
		if ledger.quantity.Sign() == 0 {
			position.PriceStatus = "not_required"
		} else if price, ok := priceMap[ledger.assetType+"\x00"+ledger.symbol]; ok {
			currentPrice, valid := new(big.Rat).SetString(price.Price)
			if !valid {
				return model.InvestmentPortfolio{}, apperrors.Internal(errors.New("stored investment price contains an invalid decimal"))
			}
			value := new(big.Rat).Mul(ledger.quantity, currentPrice)
			unrealized := new(big.Rat).Sub(value, ledger.basis)
			percentage := new(big.Rat)
			if ledger.basis.Sign() != 0 {
				percentage.Mul(new(big.Rat).Quo(unrealized, ledger.basis), big.NewRat(100, 1))
			}
			position.CurrentPrice = formatRat(currentPrice, 8)
			position.CurrentValue = formatRat(value, 2)
			position.UnrealizedProfit = formatRat(unrealized, 2)
			position.UnrealizedPct = formatRat(percentage, 2)
			position.PriceAsOf = price.AsOf
			position.PriceStatus = "available"
			totalValue.Add(totalValue, value)
		} else {
			portfolio.MissingPrices++
		}
		portfolio.Positions = append(portfolio.Positions, position)
	}
	portfolio.InvestedAmount = formatRat(totalBasis, 2)
	portfolio.RealizedProfit = formatRat(totalRealized, 2)
	if portfolio.MissingPrices == 0 {
		portfolio.CurrentValue = formatRat(totalValue, 2)
		portfolio.UnrealizedProfit = formatRat(new(big.Rat).Sub(totalValue, totalBasis), 2)
	} else {
		portfolio.CurrentValue = ""
		portfolio.UnrealizedProfit = ""
	}
	return portfolio, nil
}

func validateInvestmentLedgerNeverNegative(trades []model.InvestmentTrade) error {
	ordered := append([]model.InvestmentTrade(nil), trades...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].OccurredAt == ordered[j].OccurredAt {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].OccurredAt < ordered[j].OccurredAt
	})
	holdings := make(map[string]*big.Rat)
	for _, trade := range ordered {
		key := trade.AssetType + "\x00" + trade.Symbol + "\x00" + trade.Broker
		quantity, ok := new(big.Rat).SetString(trade.Quantity)
		if !ok {
			return errors.New("invalid quantity")
		}
		if holdings[key] == nil {
			holdings[key] = new(big.Rat)
		}
		if trade.Side == "buy" {
			holdings[key].Add(holdings[key], quantity)
		} else {
			holdings[key].Sub(holdings[key], quantity)
			if holdings[key].Sign() < 0 {
				return errors.New("negative holding")
			}
		}
	}
	return nil
}

func formatRat(value *big.Rat, decimals int) string {
	return value.FloatString(decimals)
}

func formatRatTrimmed(value *big.Rat, decimals int) string {
	result := value.FloatString(decimals)
	result = strings.TrimRight(result, "0")
	result = strings.TrimRight(result, ".")
	if result == "" || result == "-0" {
		return "0"
	}
	return result
}

func investmentRecurrenceRule(schedule model.InvestmentSchedule) (recurrence.Rule, error) {
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
