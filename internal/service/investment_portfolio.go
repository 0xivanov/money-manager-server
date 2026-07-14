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

type investmentLedgerPosition struct {
	assetType, symbol, assetName, broker string
	quantity, basis, realized            *big.Rat
}

func (s *Service) InvestmentPortfolio(ctx context.Context, userID int) (model.InvestmentPortfolio, error) {
	trades, err := s.store.ListInvestmentTrades(ctx, userID, repository.InvestmentTradeFilter{Limit: maximumInvestmentTradeRows + 1})
	if err != nil {
		return model.InvestmentPortfolio{}, apperrors.Internal(fmt.Errorf("list portfolio trades: %w", err))
	}
	if len(trades) > maximumInvestmentTradeRows {
		return model.InvestmentPortfolio{}, apperrors.Validation("portfolio contains more than 10000 trades")
	}
	supportedTrades, unsupportedPositions, err := supportedInvestmentTrades(trades)
	if err != nil {
		return model.InvestmentPortfolio{}, apperrors.Internal(err)
	}
	symbols, err := openInvestmentSymbols(supportedTrades)
	if err != nil {
		return model.InvestmentPortfolio{}, apperrors.Internal(err)
	}
	prices := make([]model.InvestmentPrice, 0, len(symbols))
	if len(symbols) > 0 {
		if s.marketData == nil {
			return model.InvestmentPortfolio{}, apperrors.Unavailable(
				"crypto market pricing is temporarily unavailable", errors.New("market data client is not configured"),
			)
		}
		quotes, quoteErr := s.marketData.CurrentQuotes(ctx, symbols, supportedCurrency)
		if quoteErr != nil {
			return model.InvestmentPortfolio{}, apperrors.Unavailable("crypto market pricing is temporarily unavailable", quoteErr)
		}
		for _, symbol := range symbols {
			quote, ok := quotes[symbol]
			if !ok || quote.AsOf.IsZero() || strings.TrimSpace(quote.Price) == "" {
				return model.InvestmentPortfolio{}, apperrors.Unavailable(
					"crypto market pricing returned an incomplete quote", fmt.Errorf("missing quote for %s", symbol),
				)
			}
			prices = append(prices, model.InvestmentPrice{
				AssetType: "crypto", Symbol: symbol, Currency: supportedCurrency,
				Price: quote.Price, Provider: quote.Provider,
				AsOf: quote.AsOf.UTC().Truncate(time.Second).Format(time.RFC3339),
			})
		}
	}
	portfolio, err := calculateInvestmentPortfolio(supportedTrades, prices)
	if err != nil {
		return model.InvestmentPortfolio{}, err
	}
	portfolio.UnsupportedPositions = unsupportedPositions
	return portfolio, nil
}

func supportedInvestmentTrades(trades []model.InvestmentTrade) ([]model.InvestmentTrade, int, error) {
	supported := make([]model.InvestmentTrade, 0, len(trades))
	unsupportedHoldings := make(map[string]*big.Rat)
	for _, trade := range trades {
		if trade.AssetType == "crypto" && (trade.Symbol == "BTC" || trade.Symbol == "ETH") {
			supported = append(supported, trade)
			continue
		}
		quantity, ok := new(big.Rat).SetString(trade.Quantity)
		if !ok {
			return nil, 0, errors.New("stored investment trade contains an invalid quantity")
		}
		key := trade.AssetType + "\x00" + trade.Symbol + "\x00" + trade.Broker
		if unsupportedHoldings[key] == nil {
			unsupportedHoldings[key] = new(big.Rat)
		}
		if trade.Side == "buy" {
			unsupportedHoldings[key].Add(unsupportedHoldings[key], quantity)
		} else {
			unsupportedHoldings[key].Sub(unsupportedHoldings[key], quantity)
		}
	}
	unsupportedPositions := 0
	for _, quantity := range unsupportedHoldings {
		if quantity.Sign() > 0 {
			unsupportedPositions++
		}
	}
	return supported, unsupportedPositions, nil
}

func openInvestmentSymbols(trades []model.InvestmentTrade) ([]string, error) {
	holdings := make(map[string]*big.Rat)
	for _, trade := range trades {
		quantity, ok := new(big.Rat).SetString(trade.Quantity)
		if !ok {
			return nil, errors.New("stored investment trade contains an invalid quantity")
		}
		if holdings[trade.Symbol] == nil {
			holdings[trade.Symbol] = new(big.Rat)
		}
		if trade.Side == "buy" {
			holdings[trade.Symbol].Add(holdings[trade.Symbol], quantity)
		} else {
			holdings[trade.Symbol].Sub(holdings[trade.Symbol], quantity)
		}
	}
	symbols := make([]string, 0, len(holdings))
	for symbol, quantity := range holdings {
		if quantity.Sign() > 0 {
			symbols = append(symbols, symbol)
		}
	}
	sort.Strings(symbols)
	return symbols, nil
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
		if strings.TrimSpace(trade.Amount) != "" {
			storedAmount, amountOK := new(big.Rat).SetString(trade.Amount)
			if !amountOK {
				return model.InvestmentPortfolio{}, apperrors.Internal(errors.New("stored investment trade contains an invalid amount"))
			}
			gross = storedAmount
		}
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
			Broker: ledger.broker, Quantity: formatRatTrimmed(ledger.quantity, 18),
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
			position.PriceProvider = price.Provider
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
