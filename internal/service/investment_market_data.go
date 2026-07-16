package service

import (
	"context"
	"net/http"
	"time"

	"money-manager-server/internal/config"
	"money-manager-server/internal/marketdata"
)

type investmentMarketQuote struct {
	Price    string
	Provider string
	AsOf     time.Time
}

type investmentMarketHistoryPoint struct {
	Price string
	AsOf  time.Time
}

type investmentMarketDataClient interface {
	QuoteAt(context.Context, string, string, time.Time) (investmentMarketQuote, error)
	CurrentQuotes(context.Context, []string, string) (map[string]investmentMarketQuote, error)
	DailyHistory(context.Context, string, string, time.Time) ([]investmentMarketHistoryPoint, error)
}

type krakenInvestmentMarketData struct {
	provider            marketdata.Provider
	longHistoryProvider marketdata.DailyHistoryProvider
	now                 func() time.Time
}

func (s *Service) configureInvestmentMarketData(cfg config.Config) {
	timeout := cfg.MarketDataRequestTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client, err := marketdata.New(marketdata.Config{
		HTTPClient:       &http.Client{Timeout: timeout},
		Now:              s.now,
		OperationTimeout: timeout,
	})
	if err != nil {
		return
	}
	longHistoryClient, historyErr := marketdata.NewCoinbaseHistory(marketdata.CoinbaseHistoryConfig{
		HTTPClient:       &http.Client{Timeout: timeout},
		Now:              s.now,
		OperationTimeout: timeout,
	})
	if historyErr != nil {
		longHistoryClient = nil
	}
	s.marketData = &krakenInvestmentMarketData{
		provider: client, longHistoryProvider: longHistoryClient, now: s.now,
	}
}

func (k *krakenInvestmentMarketData) QuoteAt(
	ctx context.Context,
	symbol string,
	currency string,
	at time.Time,
) (investmentMarketQuote, error) {
	quote, err := k.provider.QuoteAt(ctx, symbol, currency, at)
	if err != nil {
		return investmentMarketQuote{}, err
	}
	return investmentMarketQuote{Price: quote.Price, Provider: quote.Provider, AsOf: quote.AsOf}, nil
}

func (k *krakenInvestmentMarketData) CurrentQuotes(
	ctx context.Context,
	symbols []string,
	currency string,
) (map[string]investmentMarketQuote, error) {
	quotes, err := k.provider.CurrentQuotes(ctx, symbols, currency)
	if err != nil {
		return nil, err
	}
	result := make(map[string]investmentMarketQuote, len(quotes))
	for _, quote := range quotes {
		result[quote.Symbol] = investmentMarketQuote{
			Price: quote.Price, Provider: quote.Provider, AsOf: quote.AsOf,
		}
	}
	return result, nil
}

func (k *krakenInvestmentMarketData) DailyHistory(
	ctx context.Context,
	symbol string,
	currency string,
	since time.Time,
) ([]investmentMarketHistoryPoint, error) {
	historyProvider := marketdata.DailyHistoryProvider(k.provider)
	if k.longHistoryProvider != nil && since.Before(k.now().UTC().AddDate(0, 0, -700)) {
		historyProvider = k.longHistoryProvider
	}
	points, err := historyProvider.DailyHistory(ctx, symbol, currency, since)
	if err != nil {
		return nil, err
	}
	result := make([]investmentMarketHistoryPoint, 0, len(points))
	for _, point := range points {
		result = append(result, investmentMarketHistoryPoint{Price: point.Close, AsOf: point.AsOf})
	}
	return result, nil
}
