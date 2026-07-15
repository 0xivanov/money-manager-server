package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

const defaultInvestmentHistoryRange = "1y"

var investmentHistoryRangeDays = map[string]int{
	"1m": 30,
	"3m": 90,
	"1y": 365,
}

type historyLedgerPosition struct {
	quantity *big.Rat
	basis    *big.Rat
}

type timedInvestmentTrade struct {
	trade model.InvestmentTrade
	at    time.Time
}

func (s *Service) InvestmentPortfolioHistory(
	ctx context.Context,
	userID int,
	rangeValue string,
) (model.InvestmentPortfolioHistory, error) {
	rangeValue = strings.ToLower(strings.TrimSpace(rangeValue))
	if rangeValue == "" {
		rangeValue = defaultInvestmentHistoryRange
	}
	days, ok := investmentHistoryRangeDays[rangeValue]
	if !ok {
		return model.InvestmentPortfolioHistory{}, apperrors.Validation("range must be 1m, 3m, or 1y")
	}
	cacheKey := investmentPortfolioHistoryCacheKey(userID, rangeValue)
	var cached model.InvestmentPortfolioHistory
	if s.loadInvestmentResponse(ctx, cacheKey, &cached) {
		return cached, nil
	}
	result, err := s.investmentPortfolioHistory(ctx, userID, rangeValue, days)
	if err == nil {
		s.storeInvestmentResponse(ctx, cacheKey, result)
	}
	return result, err
}

func (s *Service) investmentPortfolioHistory(
	ctx context.Context,
	userID int,
	rangeValue string,
	days int,
) (model.InvestmentPortfolioHistory, error) {
	result := model.InvestmentPortfolioHistory{
		Points:   make([]model.InvestmentPortfolioHistoryPoint, 0, days+1),
		Currency: supportedCurrency, Range: rangeValue,
	}
	trades, err := s.store.ListInvestmentTrades(ctx, userID, repository.InvestmentTradeFilter{
		Limit: maximumInvestmentTradeRows + 1,
	})
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Internal(fmt.Errorf("list portfolio history trades: %w", err))
	}
	if len(trades) > maximumInvestmentTradeRows {
		return model.InvestmentPortfolioHistory{}, apperrors.Validation("portfolio contains more than 10000 trades")
	}
	supported, unsupportedPositions, err := supportedInvestmentTrades(trades)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Internal(err)
	}
	result.UnsupportedPositions = unsupportedPositions
	if len(supported) == 0 {
		return result, nil
	}
	if s.marketData == nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Unavailable(
			"crypto market pricing is temporarily unavailable", errors.New("market data client is not configured"),
		)
	}

	ordered, err := timedInvestmentTrades(supported)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Internal(err)
	}
	now := s.now().UTC().Truncate(time.Second)
	start := utcDay(now.AddDate(0, 0, -days))
	if firstDay := utcDay(ordered[0].at); firstDay.After(start) {
		start = firstDay
	}
	symbols := investmentSymbols(supported)
	history := make(map[string][]investmentMarketHistoryPoint, len(symbols))
	for _, symbol := range symbols {
		points, historyErr := s.marketData.DailyHistory(ctx, symbol, supportedCurrency, start.AddDate(0, 0, -1))
		if historyErr != nil {
			return model.InvestmentPortfolioHistory{}, apperrors.Unavailable("crypto price history is temporarily unavailable", historyErr)
		}
		sort.Slice(points, func(i, j int) bool { return points[i].AsOf.Before(points[j].AsOf) })
		history[symbol] = points
	}

	points, ledgers, err := calculateDailyInvestmentHistory(ordered, history, start, now)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Unavailable("crypto price history is incomplete", err)
	}
	result.Points = points
	openSymbols := openSymbolsFromHistoryLedgers(ledgers)
	currentPrices := make(map[string]*big.Rat, len(openSymbols))
	if len(openSymbols) > 0 {
		quotes, quoteErr := s.marketData.CurrentQuotes(ctx, openSymbols, supportedCurrency)
		if quoteErr != nil {
			return model.InvestmentPortfolioHistory{}, apperrors.Unavailable("crypto market pricing is temporarily unavailable", quoteErr)
		}
		for _, symbol := range openSymbols {
			quote, exists := quotes[symbol]
			price, valid := new(big.Rat).SetString(quote.Price)
			if !exists || !valid || price.Sign() <= 0 {
				return model.InvestmentPortfolioHistory{}, apperrors.Unavailable(
					"crypto market pricing returned an incomplete quote", fmt.Errorf("missing quote for %s", symbol),
				)
			}
			currentPrices[symbol] = price
		}
	}
	currentValue, currentBasis, err := valueHistoryLedgers(ledgers, currentPrices)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Internal(err)
	}
	if len(result.Points) > 0 && utcDay(parseHistoryPointTime(result.Points[len(result.Points)-1].AsOf)).Equal(utcDay(now)) {
		result.Points = result.Points[:len(result.Points)-1]
	}
	result.Points = append(result.Points, model.InvestmentPortfolioHistoryPoint{
		AsOf: now.Format(time.RFC3339), Value: formatRat(currentValue, 2),
		InvestedAmount: formatRat(currentBasis, 2),
	})
	return result, nil
}

func timedInvestmentTrades(trades []model.InvestmentTrade) ([]timedInvestmentTrade, error) {
	ordered := make([]timedInvestmentTrade, 0, len(trades))
	for _, trade := range trades {
		at, err := parseInvestmentOccurredAt(trade.OccurredAt)
		if err != nil {
			return nil, errors.New("stored investment trade contains an invalid timestamp")
		}
		ordered = append(ordered, timedInvestmentTrade{trade: trade, at: at})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].at.Equal(ordered[j].at) {
			return ordered[i].trade.ID < ordered[j].trade.ID
		}
		return ordered[i].at.Before(ordered[j].at)
	})
	return ordered, nil
}

func investmentSymbols(trades []model.InvestmentTrade) []string {
	seen := make(map[string]bool)
	for _, trade := range trades {
		seen[trade.Symbol] = true
	}
	symbols := make([]string, 0, len(seen))
	for symbol := range seen {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	return symbols
}

func calculateDailyInvestmentHistory(
	trades []timedInvestmentTrade,
	history map[string][]investmentMarketHistoryPoint,
	start time.Time,
	now time.Time,
) ([]model.InvestmentPortfolioHistoryPoint, map[string]*historyLedgerPosition, error) {
	ledgers := make(map[string]*historyLedgerPosition)
	latestPrices := make(map[string]*big.Rat)
	priceIndexes := make(map[string]int)
	tradeIndex := 0
	points := make([]model.InvestmentPortfolioHistoryPoint, 0)
	for day := utcDay(start); !day.After(utcDay(now)); day = day.AddDate(0, 0, 1) {
		through := day.AddDate(0, 0, 1)
		if through.After(now) {
			through = now.Add(time.Nanosecond)
		}
		for tradeIndex < len(trades) && trades[tradeIndex].at.Before(through) {
			if err := applyHistoryTrade(ledgers, trades[tradeIndex].trade); err != nil {
				return nil, nil, err
			}
			tradeIndex++
		}
		for symbol, series := range history {
			index := priceIndexes[symbol]
			for index < len(series) && series[index].AsOf.Before(through) {
				price, ok := new(big.Rat).SetString(series[index].Price)
				if !ok || price.Sign() <= 0 {
					return nil, nil, fmt.Errorf("invalid %s history price", symbol)
				}
				latestPrices[symbol] = price
				index++
			}
			priceIndexes[symbol] = index
		}
		if len(ledgers) == 0 {
			continue
		}
		value, basis, err := valueHistoryLedgers(ledgers, latestPrices)
		if err != nil {
			return nil, nil, err
		}
		pointAsOf := through.Add(-time.Second)
		if day.Equal(utcDay(now)) {
			pointAsOf = now
		}
		points = append(points, model.InvestmentPortfolioHistoryPoint{
			AsOf: pointAsOf.Format(time.RFC3339), Value: formatRat(value, 2),
			InvestedAmount: formatRat(basis, 2),
		})
	}
	return points, ledgers, nil
}

func applyHistoryTrade(ledgers map[string]*historyLedgerPosition, trade model.InvestmentTrade) error {
	quantity, quantityOK := new(big.Rat).SetString(trade.Quantity)
	price, priceOK := new(big.Rat).SetString(trade.PricePerUnit)
	fees, feesOK := new(big.Rat).SetString(trade.Fees)
	if !quantityOK || !priceOK || !feesOK {
		return errors.New("stored investment trade contains invalid decimals")
	}
	amount := new(big.Rat).Mul(quantity, price)
	if strings.TrimSpace(trade.Amount) != "" {
		var amountOK bool
		amount, amountOK = new(big.Rat).SetString(trade.Amount)
		if !amountOK {
			return errors.New("stored investment trade contains an invalid amount")
		}
	}
	key := trade.AssetType + "\x00" + trade.Symbol + "\x00" + trade.Broker
	position := ledgers[key]
	if position == nil {
		position = &historyLedgerPosition{quantity: new(big.Rat), basis: new(big.Rat)}
		ledgers[key] = position
	}
	if trade.Side == "buy" {
		position.quantity.Add(position.quantity, quantity)
		position.basis.Add(position.basis, new(big.Rat).Add(amount, fees))
		return nil
	}
	if position.quantity.Sign() <= 0 || position.quantity.Cmp(quantity) < 0 {
		return errors.New("investment ledger contains a sale larger than its holding")
	}
	costRemoved := new(big.Rat).Mul(position.basis, new(big.Rat).Quo(quantity, position.quantity))
	position.basis.Sub(position.basis, costRemoved)
	position.quantity.Sub(position.quantity, quantity)
	if position.quantity.Sign() == 0 {
		position.basis.SetInt64(0)
	}
	return nil
}

func valueHistoryLedgers(
	ledgers map[string]*historyLedgerPosition,
	prices map[string]*big.Rat,
) (*big.Rat, *big.Rat, error) {
	value, basis := new(big.Rat), new(big.Rat)
	for key, position := range ledgers {
		basis.Add(basis, position.basis)
		if position.quantity.Sign() == 0 {
			continue
		}
		parts := strings.Split(key, "\x00")
		if len(parts) < 2 || prices[parts[1]] == nil {
			return nil, nil, fmt.Errorf("missing history price for %s", parts[1])
		}
		value.Add(value, new(big.Rat).Mul(position.quantity, prices[parts[1]]))
	}
	return value, basis, nil
}

func openSymbolsFromHistoryLedgers(ledgers map[string]*historyLedgerPosition) []string {
	seen := make(map[string]bool)
	for key, position := range ledgers {
		if position.quantity.Sign() <= 0 {
			continue
		}
		parts := strings.Split(key, "\x00")
		if len(parts) >= 2 {
			seen[parts[1]] = true
		}
	}
	symbols := make([]string, 0, len(seen))
	for symbol := range seen {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	return symbols
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func parseHistoryPointTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}
