package marketdata

import (
	"context"
	"strconv"
	"time"
)

type cachedQuote struct {
	quote     Quote
	expiresAt time.Time
}

type quoteCall struct {
	done  chan struct{}
	quote Quote
	err   error
}

type cachedHistory struct {
	points    []DailyClose
	expiresAt time.Time
}

type historyCall struct {
	done   chan struct{}
	points []DailyClose
	err    error
}

func (c *Client) currentQuoteWithCache(ctx context.Context, symbol, currency string) (Quote, error) {
	if err := ctx.Err(); err != nil {
		return Quote{}, err
	}
	key := symbol + "/" + currency
	now := c.now().UTC()

	c.cacheMu.Lock()
	if cached, ok := c.currentQuotes[key]; ok && now.Before(cached.expiresAt) {
		c.cacheMu.Unlock()
		if err := ctx.Err(); err != nil {
			return Quote{}, err
		}
		return cached.quote, nil
	}
	if call, ok := c.currentQuoteCalls[key]; ok {
		c.cacheMu.Unlock()
		select {
		case <-ctx.Done():
			return Quote{}, ctx.Err()
		case <-call.done:
			if err := ctx.Err(); err != nil {
				return Quote{}, err
			}
			return call.quote, call.err
		}
	}
	call := &quoteCall{done: make(chan struct{})}
	c.currentQuoteCalls[key] = call
	c.cacheMu.Unlock()

	quote, err := c.fetchCurrentQuote(ctx, symbol, currency)

	c.cacheMu.Lock()
	call.quote, call.err = quote, err
	if err == nil {
		c.currentQuotes[key] = cachedQuote{quote: quote, expiresAt: c.now().UTC().Add(currentQuoteCacheTTL)}
	}
	delete(c.currentQuoteCalls, key)
	close(call.done)
	c.cacheMu.Unlock()
	return quote, err
}

func (c *Client) dailyHistoryWithCache(
	ctx context.Context,
	symbol, currency string,
	since time.Time,
) ([]DailyClose, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := symbol + "/" + currency + "/" + strconv.FormatInt(since.UnixNano(), 10)
	now := c.now().UTC()

	c.cacheMu.Lock()
	if cached, ok := c.dailyHistories[key]; ok && now.Before(cached.expiresAt) {
		points := cloneDailyCloses(cached.points)
		c.cacheMu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return points, nil
	}
	if call, ok := c.dailyHistoryCalls[key]; ok {
		c.cacheMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return cloneDailyCloses(call.points), call.err
		}
	}
	call := &historyCall{done: make(chan struct{})}
	c.dailyHistoryCalls[key] = call
	c.cacheMu.Unlock()

	points, err := c.fetchDailyHistory(ctx, symbol, currency, since)

	c.cacheMu.Lock()
	call.points, call.err = cloneDailyCloses(points), err
	if err == nil {
		expiresAt := c.now().UTC().Add(dailyHistoryCacheTTL)
		nextDay := utcDay(c.now()).AddDate(0, 0, 1)
		if nextDay.Before(expiresAt) {
			expiresAt = nextDay
		}
		c.dailyHistories[key] = cachedHistory{
			points: cloneDailyCloses(points), expiresAt: expiresAt,
		}
	}
	delete(c.dailyHistoryCalls, key)
	close(call.done)
	c.cacheMu.Unlock()
	return cloneDailyCloses(points), err
}

func cloneDailyCloses(points []DailyClose) []DailyClose {
	if points == nil {
		return nil
	}
	result := make([]DailyClose, len(points))
	copy(result, points)
	return result
}
