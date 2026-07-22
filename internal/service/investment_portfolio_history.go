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
	"money-manager-server/internal/marketdata"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

const defaultInvestmentHistoryRange = "1y"

var investmentHistoryRangeDays = map[string]int{
	"1m":  30,
	"3m":  90,
	"1y":  365,
	"2y":  730,
	"5y":  1825,
	"max": 0,
}

const maximumInvestmentHistoryResponsePoints = 500

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
		return model.InvestmentPortfolioHistory{}, apperrors.Validation("range must be 1m, 3m, 1y, 2y, 5y, or max")
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
	capacity := days + 1
	if capacity <= 0 || capacity > maximumInvestmentHistoryResponsePoints {
		capacity = maximumInvestmentHistoryResponsePoints
	}
	result := model.InvestmentPortfolioHistory{
		Points:   make([]model.InvestmentPortfolioHistoryPoint, 0, capacity),
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
	supported, unsupportedPositions, err := supportedInvestmentTrades(trades, s.stockHistoryData != nil)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Internal(err)
	}
	result.UnsupportedPositions = unsupportedPositions
	if len(supported) == 0 {
		return result, nil
	}
	ordered, err := timedInvestmentTrades(supported)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Internal(err)
	}
	now := s.now().UTC().Truncate(time.Second)
	start := utcDay(ordered[0].at)
	if days > 0 {
		start = utcDay(now.AddDate(0, 0, -days))
	}
	if firstDay := utcDay(ordered[0].at); firstDay.After(start) {
		start = firstDay
	}
	instruments := uniqueInvestmentInstruments(supported)
	history := make(map[string][]investmentMarketHistoryPoint, len(instruments))
	availableInstruments := make([]model.InvestmentTrade, 0, len(instruments))
	unavailableStockInstruments := make(map[string]bool)
	for _, instrument := range instruments {
		points, historyErr := s.investmentDailyHistory(ctx, instrument, start.AddDate(0, 0, -1))
		if historyErr != nil {
			if instrument.AssetType == "stock" {
				key := investmentMarketKey(instrument)
				unavailableStockInstruments[key] = true
				openPositions, countErr := openInvestmentPositionCount(supported, instrument)
				if countErr != nil {
					return model.InvestmentPortfolioHistory{}, apperrors.Internal(countErr)
				}
				result.UnsupportedPositions += openPositions
				continue
			}
			return model.InvestmentPortfolioHistory{}, apperrors.Unavailable("portfolio price history is temporarily unavailable", historyErr)
		}
		sort.Slice(points, func(i, j int) bool { return points[i].AsOf.Before(points[j].AsOf) })
		history[investmentMarketKey(instrument)] = points
		availableInstruments = append(availableInstruments, instrument)
	}
	if len(unavailableStockInstruments) > 0 {
		ordered = availableTimedInvestmentTrades(ordered, unavailableStockInstruments)
		instruments = availableInstruments
		if len(ordered) == 0 {
			return result, nil
		}
		start = utcDay(ordered[0].at)
		if days > 0 {
			start = utcDay(now.AddDate(0, 0, -days))
		}
		if firstDay := utcDay(ordered[0].at); firstDay.After(start) {
			start = firstDay
		}
	}

	points, ledgers, err := calculateDailyInvestmentHistory(ordered, instruments, history, start, now)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Unavailable("portfolio price history is incomplete", err)
	}
	result.Points = points
	currentPrices := make(map[string]*big.Rat, len(instruments))
	for _, instrument := range instruments {
		if !historyLedgerIsOpen(ledgers, instrument) {
			continue
		}
		quote, quoteErr := s.currentInvestmentHistoryQuote(ctx, userID, instrument)
		price, valid := new(big.Rat).SetString(quote.Price)
		if quoteErr != nil || !valid || price.Sign() <= 0 {
			if latest := latestInvestmentHistoryPrice(history[investmentMarketKey(instrument)]); latest != nil {
				currentPrices[investmentMarketKey(instrument)] = latest
				continue
			}
			return model.InvestmentPortfolioHistory{}, apperrors.Unavailable("portfolio market pricing returned an incomplete quote", quoteErr)
		}
		currentPrices[investmentMarketKey(instrument)] = price
	}
	currentValue, currentBasis, currentHoldings, err := valueHistoryLedgers(ledgers, currentPrices)
	if err != nil {
		return model.InvestmentPortfolioHistory{}, apperrors.Internal(err)
	}
	if len(result.Points) > 0 && utcDay(parseHistoryPointTime(result.Points[len(result.Points)-1].AsOf)).Equal(utcDay(now)) {
		result.Points = result.Points[:len(result.Points)-1]
	}
	result.Points = append(result.Points, model.InvestmentPortfolioHistoryPoint{
		AsOf: now.Format(time.RFC3339), Value: formatRat(currentValue, 2),
		InvestedAmount: formatRat(currentBasis, 2),
		Holdings:       investmentHistoryHoldings(instruments, currentHoldings),
	})
	result.Points = sampleInvestmentHistoryPoints(result.Points, maximumInvestmentHistoryResponsePoints)
	return result, nil
}

func (s *Service) investmentDailyHistory(
	ctx context.Context,
	instrument model.InvestmentTrade,
	since time.Time,
) ([]investmentMarketHistoryPoint, error) {
	if instrument.AssetType == "crypto" {
		if s.marketData == nil {
			return nil, errors.New("crypto market data client is not configured")
		}
		return s.marketData.DailyHistory(ctx, instrument.Symbol, supportedCurrency, since)
	}
	if s.stockHistoryData == nil {
		return nil, errors.New("stock history market data client is not configured")
	}
	return s.cachedStockDailyHistory(ctx, instrument, since)
}

func (s *Service) cachedStockDailyHistory(
	ctx context.Context,
	instrument model.InvestmentTrade,
	since time.Time,
) ([]investmentMarketHistoryPoint, error) {
	since = utcDay(since)
	cached, err := s.store.ListInvestmentMarketHistory(
		ctx, "stock", instrument.Symbol, instrument.Exchange, supportedCurrency, since,
	)
	if err != nil {
		return nil, fmt.Errorf("list cached stock history: %w", err)
	}
	needsBackfill := len(cached) == 0 || cached[0].AsOf.After(since.AddDate(0, 0, 7))
	staleBefore := utcDay(s.now().UTC()).AddDate(0, 0, -4)
	needsRefresh := len(cached) == 0 || cached[len(cached)-1].AsOf.Before(staleBefore)
	if !needsBackfill && !needsRefresh {
		return storedInvestmentHistoryPoints(cached), nil
	}
	fetchSince := since
	if !needsBackfill && len(cached) > 0 {
		fetchSince = cached[len(cached)-1].AsOf.AddDate(0, 0, -7)
	}
	fetched, fetchErr := s.stockHistoryData.DailyHistory(ctx, marketdata.EquityInstrument{
		Symbol: instrument.Symbol, Exchange: instrument.Exchange, MarketCurrency: instrument.MarketCurrency,
	}, supportedCurrency, fetchSince)
	if fetchErr != nil {
		if len(cached) > 0 {
			return storedInvestmentHistoryPoints(cached), nil
		}
		return nil, fetchErr
	}
	stored := make([]model.InvestmentMarketHistoryPrice, 0, len(fetched))
	for _, point := range fetched {
		stored = append(stored, model.InvestmentMarketHistoryPrice{
			AssetType: "stock", Symbol: instrument.Symbol, Exchange: instrument.Exchange,
			Currency: supportedCurrency, Price: point.Price, Provider: marketdata.ProviderMarketstack,
			AsOf: utcDay(point.AsOf),
		})
	}
	if err := s.store.UpsertInvestmentMarketHistory(ctx, stored); err != nil {
		return nil, fmt.Errorf("cache stock history: %w", err)
	}
	return mergeInvestmentHistory(cached, fetched, since), nil
}

func (s *Service) currentInvestmentHistoryQuote(
	ctx context.Context,
	userID int,
	instrument model.InvestmentTrade,
) (investmentMarketQuote, error) {
	if instrument.AssetType == "crypto" {
		if s.marketData == nil {
			return investmentMarketQuote{}, errors.New("crypto market data client is not configured")
		}
		quotes, err := s.marketData.CurrentQuotes(ctx, []string{instrument.Symbol}, supportedCurrency)
		return quotes[instrument.Symbol], err
	}
	if !s.canUseTrading212(userID) {
		return investmentMarketQuote{}, errors.New("Trading 212 integration is not available for this account")
	}
	return s.stockMarketData.CurrentQuote(ctx, marketdata.EquityInstrument{
		Symbol: instrument.Symbol, Exchange: instrument.Exchange, MarketCurrency: instrument.MarketCurrency,
	}, supportedCurrency)
}

func storedInvestmentHistoryPoints(prices []model.InvestmentMarketHistoryPrice) []investmentMarketHistoryPoint {
	result := make([]investmentMarketHistoryPoint, 0, len(prices))
	for _, price := range prices {
		result = append(result, investmentMarketHistoryPoint{
			Price: price.Price, Provider: price.Provider, AsOf: utcDay(price.AsOf),
		})
	}
	return result
}

func mergeInvestmentHistory(
	cached []model.InvestmentMarketHistoryPrice,
	fetched []investmentMarketHistoryPoint,
	since time.Time,
) []investmentMarketHistoryPoint {
	byDay := make(map[time.Time]investmentMarketHistoryPoint, len(cached)+len(fetched))
	for _, point := range storedInvestmentHistoryPoints(cached) {
		byDay[utcDay(point.AsOf)] = point
	}
	for _, point := range fetched {
		point.AsOf = utcDay(point.AsOf)
		byDay[point.AsOf] = point
	}
	result := make([]investmentMarketHistoryPoint, 0, len(byDay))
	for day, point := range byDay {
		if !day.Before(since) {
			result = append(result, point)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AsOf.Before(result[j].AsOf) })
	return result
}

func latestInvestmentHistoryPrice(points []investmentMarketHistoryPoint) *big.Rat {
	if len(points) == 0 {
		return nil
	}
	price, ok := new(big.Rat).SetString(points[len(points)-1].Price)
	if !ok || price.Sign() <= 0 {
		return nil
	}
	return price
}

func sampleInvestmentHistoryPoints(
	points []model.InvestmentPortfolioHistoryPoint,
	limit int,
) []model.InvestmentPortfolioHistoryPoint {
	if limit < 2 || len(points) <= limit {
		return points
	}
	lastIndex := len(points) - 1
	step := float64(lastIndex) / float64(limit-1)
	result := make([]model.InvestmentPortfolioHistoryPoint, 0, limit)
	previousIndex := -1
	for sampleIndex := 0; sampleIndex < limit; sampleIndex++ {
		index := int(float64(sampleIndex)*step + 0.5)
		if sampleIndex == limit-1 {
			index = lastIndex
		}
		if index == previousIndex {
			continue
		}
		result = append(result, points[index])
		previousIndex = index
	}
	return result
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

func uniqueInvestmentInstruments(trades []model.InvestmentTrade) []model.InvestmentTrade {
	items := make(map[string]model.InvestmentTrade)
	for _, trade := range trades {
		items[investmentMarketKey(trade)] = trade
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]model.InvestmentTrade, 0, len(keys))
	for _, key := range keys {
		result = append(result, items[key])
	}
	return result
}

func investmentMarketKey(trade model.InvestmentTrade) string {
	return trade.AssetType + "\x00" + trade.Symbol + "\x00" + trade.Exchange
}

func availableTimedInvestmentTrades(
	trades []timedInvestmentTrade,
	unavailable map[string]bool,
) []timedInvestmentTrade {
	result := make([]timedInvestmentTrade, 0, len(trades))
	for _, trade := range trades {
		if !unavailable[investmentMarketKey(trade.trade)] {
			result = append(result, trade)
		}
	}
	return result
}

func openInvestmentPositionCount(trades []model.InvestmentTrade, instrument model.InvestmentTrade) (int, error) {
	quantities := make(map[string]*big.Rat)
	key := investmentMarketKey(instrument)
	for _, trade := range trades {
		if investmentMarketKey(trade) != key {
			continue
		}
		quantity, ok := new(big.Rat).SetString(trade.Quantity)
		if !ok {
			return 0, errors.New("stored investment trade contains an invalid quantity")
		}
		if quantities[trade.Broker] == nil {
			quantities[trade.Broker] = new(big.Rat)
		}
		if trade.Side == "buy" {
			quantities[trade.Broker].Add(quantities[trade.Broker], quantity)
		} else {
			quantities[trade.Broker].Sub(quantities[trade.Broker], quantity)
		}
	}
	count := 0
	for _, quantity := range quantities {
		if quantity.Sign() > 0 {
			count++
		}
	}
	return count, nil
}

func historyLedgerIsOpen(ledgers map[string]*historyLedgerPosition, instrument model.InvestmentTrade) bool {
	prefix := investmentMarketKey(instrument) + "\x00"
	for key, position := range ledgers {
		if strings.HasPrefix(key, prefix) && position.quantity.Sign() > 0 {
			return true
		}
	}
	return false
}

func calculateDailyInvestmentHistory(
	trades []timedInvestmentTrade,
	instruments []model.InvestmentTrade,
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
		value, basis, holdingValues, err := valueHistoryLedgers(ledgers, latestPrices)
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
			Holdings:       investmentHistoryHoldings(instruments, holdingValues),
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
	key := investmentMarketKey(trade) + "\x00" + trade.Broker
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
) (*big.Rat, *big.Rat, map[string]*big.Rat, error) {
	value, basis := new(big.Rat), new(big.Rat)
	holdings := make(map[string]*big.Rat)
	for key, position := range ledgers {
		basis.Add(basis, position.basis)
		if position.quantity.Sign() == 0 {
			continue
		}
		parts := strings.Split(key, "\x00")
		if len(parts) < 3 || prices[strings.Join(parts[:3], "\x00")] == nil {
			return nil, nil, nil, fmt.Errorf("missing history price for %s", parts[1])
		}
		instrumentKey := strings.Join(parts[:3], "\x00")
		positionValue := new(big.Rat).Mul(position.quantity, prices[instrumentKey])
		value.Add(value, positionValue)
		if holdings[instrumentKey] == nil {
			holdings[instrumentKey] = new(big.Rat)
		}
		holdings[instrumentKey].Add(holdings[instrumentKey], positionValue)
	}
	return value, basis, holdings, nil
}

func investmentHistoryHoldings(
	instruments []model.InvestmentTrade,
	values map[string]*big.Rat,
) []model.InvestmentPortfolioHistoryHolding {
	result := make([]model.InvestmentPortfolioHistoryHolding, 0, len(instruments))
	for _, instrument := range instruments {
		value := values[investmentMarketKey(instrument)]
		if value == nil {
			value = new(big.Rat)
		}
		result = append(result, model.InvestmentPortfolioHistoryHolding{
			AssetType: instrument.AssetType, Symbol: instrument.Symbol,
			AssetName: instrument.AssetName, Exchange: instrument.Exchange,
			Value: formatRat(value, 2),
		})
	}
	return result
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
