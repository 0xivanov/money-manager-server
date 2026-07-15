package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

func TestInvestmentPortfolioCacheIsUserScopedAndUsesStrictTTL(t *testing.T) {
	listCalls := make(map[int]int)
	quoteCalls := 0
	store := &fakeStore{listInvestmentTrades: func(
		_ context.Context, userID int, _ repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		listCalls[userID]++
		return []model.InvestmentTrade{{
			ID: userID, AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "manual",
			Side: "buy", Amount: "100.00", Quantity: "0.002", PricePerUnit: "50000",
			Fees: "0", OccurredAt: "2026-07-01T10:00:00Z",
		}}, nil
	}}
	cache := newFakeInvestmentResponseCache()
	service := testService(store)
	service.investmentCache = cache
	service.marketData = &fakeInvestmentMarketData{currentQuotes: func(
		context.Context, []string, string,
	) (map[string]investmentMarketQuote, error) {
		quoteCalls++
		return map[string]investmentMarketQuote{"BTC": {
			Price: "60000", Provider: "kraken", AsOf: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		}}, nil
	}}

	first, err := service.InvestmentPortfolio(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.InvestmentPortfolio(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := service.InvestmentPortfolio(context.Background(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if first.CurrentValue != "120.00" || second.CurrentValue != first.CurrentValue ||
		len(second.Positions) != len(first.Positions) || otherUser.CurrentValue != "120.00" {
		t.Fatalf("portfolio results = %#v, %#v, %#v", first, second, otherUser)
	}
	if listCalls[7] != 1 || listCalls[8] != 1 || quoteCalls != 2 {
		t.Fatalf("uncached calls = lists %#v, quotes %d", listCalls, quoteCalls)
	}
	if len(cache.setTTLs) != 2 {
		t.Fatalf("cache writes = %d", len(cache.setTTLs))
	}
	for _, ttl := range cache.setTTLs {
		if ttl != 5*time.Minute {
			t.Fatalf("cache TTL = %s", ttl)
		}
	}
	if _, ok := cache.entries[investmentPortfolioCacheKey(7)]; !ok {
		t.Fatal("user 7 portfolio cache entry is missing")
	}
	if _, ok := cache.entries[investmentPortfolioCacheKey(8)]; !ok {
		t.Fatal("user 8 portfolio cache entry is missing")
	}
}

func TestInvestmentPortfolioHistoryCacheSeparatesNormalizedRanges(t *testing.T) {
	listCalls := 0
	service := testService(&fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		listCalls++
		return []model.InvestmentTrade{}, nil
	}})
	cache := newFakeInvestmentResponseCache()
	service.investmentCache = cache

	for _, rangeValue := range []string{"", "1Y", "1m", "1M"} {
		history, err := service.InvestmentPortfolioHistory(context.Background(), 7, rangeValue)
		if err != nil {
			t.Fatalf("range %q: %v", rangeValue, err)
		}
		if history.Range != strings.ToLower(firstNonEmptyForCacheTest(rangeValue, "1y")) {
			t.Fatalf("range %q returned %q", rangeValue, history.Range)
		}
	}
	if listCalls != 2 {
		t.Fatalf("history computations = %d, want 2", listCalls)
	}
	if len(cache.entries) != 2 {
		t.Fatalf("history cache entries = %#v", cache.entries)
	}
}

func TestInvestmentResponseCacheFailuresAreBoundedAndFailOpen(t *testing.T) {
	listCalls := 0
	service := testService(&fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		listCalls++
		return []model.InvestmentTrade{}, nil
	}})
	cache := newFakeInvestmentResponseCache()
	cache.getErr = errors.New("Redis unavailable")
	cache.setErr = errors.New("Redis unavailable")
	service.investmentCache = cache

	portfolio, err := service.InvestmentPortfolio(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(portfolio.Positions) != 0 || listCalls != 1 {
		t.Fatalf("portfolio = %#v, list calls = %d", portfolio, listCalls)
	}
	if !cache.getHadDeadline || !cache.setHadDeadline {
		t.Fatalf("cache deadlines = get %v, set %v", cache.getHadDeadline, cache.setHadDeadline)
	}
}

func TestInvestmentTradeWritesInvalidateOnlyAfterSuccess(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	cache := newFakeInvestmentResponseCache()
	service := testService(&fakeStore{
		createInvestmentTrade: func(
			context.Context, int, model.InvestmentTradeRequest,
		) (model.InvestmentTrade, error) {
			return model.InvestmentTrade{ID: 9}, nil
		},
		deleteInvestmentTrade: func(_ context.Context, _ int, tradeID int) error {
			if tradeID == 10 {
				return errors.New("database unavailable")
			}
			return nil
		},
	})
	service.investmentCache = cache
	service.now = func() time.Time { return now }
	service.marketData = &fakeInvestmentMarketData{quoteAt: func(
		context.Context, string, string, time.Time,
	) (investmentMarketQuote, error) {
		return investmentMarketQuote{Price: "50000", Provider: "kraken", AsOf: now}, nil
	}}

	_, err := service.CreateInvestmentTrade(context.Background(), 7, model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "BTC", Broker: "manual", Side: "buy", Amount: "100",
		OccurredAt: now.Add(-time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteInvestmentTrade(context.Background(), 8, 9); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteInvestmentTrade(context.Background(), 8, 10); err == nil {
		t.Fatal("failed delete returned nil")
	}
	if len(cache.invalidatedUsers) != 2 || cache.invalidatedUsers[0] != 7 || cache.invalidatedUsers[1] != 8 {
		t.Fatalf("invalidated users = %#v", cache.invalidatedUsers)
	}
}

type fakeInvestmentResponseCache struct {
	entries          map[string][]byte
	setTTLs          []time.Duration
	invalidatedUsers []int
	getErr           error
	setErr           error
	getHadDeadline   bool
	setHadDeadline   bool
}

func newFakeInvestmentResponseCache() *fakeInvestmentResponseCache {
	return &fakeInvestmentResponseCache{entries: make(map[string][]byte)}
}

func (c *fakeInvestmentResponseCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	_, c.getHadDeadline = ctx.Deadline()
	if c.getErr != nil {
		return nil, false, c.getErr
	}
	contents, ok := c.entries[key]
	return append([]byte(nil), contents...), ok, nil
}

func (c *fakeInvestmentResponseCache) Set(ctx context.Context, key string, contents []byte, ttl time.Duration) error {
	_, c.setHadDeadline = ctx.Deadline()
	if c.setErr != nil {
		return c.setErr
	}
	c.entries[key] = append([]byte(nil), contents...)
	c.setTTLs = append(c.setTTLs, ttl)
	return nil
}

func (c *fakeInvestmentResponseCache) InvalidateUser(_ context.Context, userID int) error {
	prefix := investmentCacheUserPrefix(userID)
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
	c.invalidatedUsers = append(c.invalidatedUsers, userID)
	return nil
}

func (*fakeInvestmentResponseCache) Close() error { return nil }

func firstNonEmptyForCacheTest(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
