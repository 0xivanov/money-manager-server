package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuoteAtReturnsNearestTradeAndPrefersEarlierTie(t *testing.T) {
	target := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/0/public/PostTrade" {
			t.Errorf("path = %q", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("symbol") != "BTC/EUR" || query.Get("count") != "1000" ||
			query.Get("from_ts") != target.Add(-time.Second).Format(time.RFC3339Nano) ||
			query.Get("to_ts") != target.Add(time.Second).Format(time.RFC3339Nano) {
			t.Errorf("query = %#v", query)
		}
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"count": 3,
				"trades": []map[string]any{
					postTrade("BTC/EUR", "EUR", "101.20", target.Add(100*time.Millisecond)),
					postTrade("BTC/EUR", "EUR", "99.50", target.Add(-700*time.Millisecond)),
					postTrade("BTC/EUR", "EUR", "100.80", target.Add(-100*time.Millisecond)),
				},
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server, target.Add(time.Hour))
	quote, err := client.QuoteAt(context.Background(), " btc ", " eur ", target)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Symbol != "BTC" || quote.Currency != "EUR" || quote.Price != "100.80" ||
		quote.Provider != ProviderKraken || !quote.AsOf.Equal(target.Add(-100*time.Millisecond)) {
		t.Fatalf("quote = %#v", quote)
	}
}

func TestQuoteAtExpandsSearchWindow(t *testing.T) {
	target := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		query := request.URL.Query()
		switch call {
		case 1:
			if query.Get("from_ts") != target.Add(-time.Second).Format(time.RFC3339Nano) ||
				query.Get("to_ts") != target.Add(time.Second).Format(time.RFC3339Nano) {
				t.Errorf("first query = %#v", query)
			}
			writeJSON(t, response, map[string]any{
				"error": []string{}, "result": map[string]any{"count": 0, "trades": []any{}},
			})
		case 2:
			if query.Get("from_ts") != target.Add(-5*time.Second).Format(time.RFC3339Nano) ||
				query.Get("to_ts") != target.Add(5*time.Second).Format(time.RFC3339Nano) {
				t.Errorf("second query = %#v", query)
			}
			writeJSON(t, response, map[string]any{
				"error": []string{},
				"result": map[string]any{"count": 1, "trades": []map[string]any{
					postTrade("ETH/EUR", "EUR", "2100.25", target.Add(2*time.Second)),
				}},
			})
		default:
			t.Errorf("unexpected request %d", call)
		}
	}))
	defer server.Close()

	client := testClient(t, server, target.Add(time.Hour))
	quote, err := client.QuoteAt(context.Background(), "ETH", "EUR", target)
	if err != nil || quote.Price != "2100.25" || calls.Load() != 2 {
		t.Fatalf("quote = %#v, calls = %d, error = %v", quote, calls.Load(), err)
	}
}

func TestQuoteAtPaginatesFullTradeResponse(t *testing.T) {
	target := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lastOnFirstPage := target.Add(-500 * time.Millisecond)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			trades := make([]map[string]any, maximumPostTradeRows)
			for index := range trades {
				tradeTime := target.Add(-999*time.Millisecond + time.Duration(index)*time.Microsecond)
				if index == len(trades)-1 {
					tradeTime = lastOnFirstPage
				}
				trades[index] = postTrade("BTC/EUR", "EUR", "100.00", tradeTime)
			}
			writeJSON(t, response, map[string]any{
				"error": []string{},
				"result": map[string]any{
					"last_ts": lastOnFirstPage.Format(time.RFC3339Nano),
					"count":   maximumPostTradeRows,
					"trades":  trades,
				},
			})
			return
		}
		if call == 2 {
			if from := request.URL.Query().Get("from_ts"); from != lastOnFirstPage.Format(time.RFC3339Nano) {
				t.Errorf("continuation from_ts = %q", from)
			}
			writeJSON(t, response, map[string]any{
				"error": []string{},
				"result": map[string]any{"count": 1, "trades": []map[string]any{
					postTrade("BTC/EUR", "EUR", "101.75", target),
				}},
			})
			return
		}
		t.Errorf("unexpected request %d", call)
	}))
	defer server.Close()

	client := testClient(t, server, target.Add(time.Hour))
	quote, err := client.QuoteAt(context.Background(), "BTC", "EUR", target)
	if err != nil || quote.Price != "101.75" || !quote.AsOf.Equal(target) || calls.Load() != 2 {
		t.Fatalf("quote = %#v, calls = %d, error = %v", quote, calls.Load(), err)
	}
}

func TestQuoteAtRejectsMalformedAndProviderErrors(t *testing.T) {
	target := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		statusCode int
		body       string
		check      func(*testing.T, error)
	}{
		{
			name: "provider error in successful response", statusCode: http.StatusOK,
			body: `{"error":["EQuery:Unknown asset pair"],"result":{}}`,
			check: func(t *testing.T, err error) {
				var providerError *ProviderError
				if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusOK ||
					len(providerError.Errors) != 1 || providerError.Errors[0] != "EQuery:Unknown asset pair" {
					t.Fatalf("error = %#v", err)
				}
			},
		},
		{
			name: "HTTP error", statusCode: http.StatusTooManyRequests,
			body: `{"error":["EAPI:Rate limit exceeded"]}`,
			check: func(t *testing.T, err error) {
				var providerError *ProviderError
				if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusTooManyRequests {
					t.Fatalf("error = %#v", err)
				}
			},
		},
		{
			name: "invalid decimal", statusCode: http.StatusOK,
			body: fmt.Sprintf(`{"error":[],"result":{"count":1,"trades":[{"symbol":"BTC/EUR","quote_asset":"EUR","price":"NaN","trade_ts":%q}]}}`, target.Format(time.RFC3339Nano)),
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "invalid trade price") {
					t.Fatalf("error = %#v", err)
				}
			},
		},
		{
			name: "invalid JSON", statusCode: http.StatusOK, body: `{"error":`,
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "decode kraken response") {
					t.Fatalf("error = %#v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.statusCode)
				_, _ = response.Write([]byte(test.body))
			}))
			defer server.Close()
			client := testClient(t, server, target.Add(time.Hour))
			_, err := client.QuoteAt(context.Background(), "BTC", "EUR", target)
			test.check(t, err)
		})
	}
}

func TestCurrentQuotesUseExactBestBidAskMidpoints(t *testing.T) {
	now := time.Date(2026, 7, 14, 18, 30, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		symbol := request.URL.Query().Get("symbol")
		if request.URL.Path != "/0/public/PreTrade" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var bids, asks []map[string]any
		switch symbol {
		case "BTC/EUR":
			bids = []map[string]any{{"price": "99.90"}, {"price": "100.10"}}
			asks = []map[string]any{{"price": "100.50"}, {"price": "100.30"}}
		case "ETH/EUR":
			bids = []map[string]any{{"price": "2000.01"}}
			asks = []map[string]any{{"price": "2000.02"}}
		default:
			t.Errorf("symbol = %q", symbol)
		}
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"symbol": symbol, "quote_asset": "EUR", "bids": bids, "asks": asks,
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server, now)
	quotes, err := client.CurrentQuotes(context.Background(), []string{" btc ", "ETH"}, " eur ")
	if err != nil {
		t.Fatal(err)
	}
	if len(quotes) != 2 || quotes[0].Price != "100.2" || quotes[1].Price != "2000.015" ||
		!quotes[0].AsOf.Equal(now) || !quotes[1].AsOf.Equal(now) || calls.Load() != 2 {
		t.Fatalf("quotes = %#v, calls = %d", quotes, calls.Load())
	}
}

func TestDailyHistoryParsesFiltersAndSortsOHLCCloses(t *testing.T) {
	since := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/0/public/OHLC" {
			t.Errorf("path = %q", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("pair") != "BTCEUR" || query.Get("interval") != "1440" ||
			query.Get("since") != fmt.Sprint(since.Unix()) {
			t.Errorf("query = %#v", query)
		}
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"XXBTZEUR": []any{
					ohlcRow(since.Add(48*time.Hour), "53000.20"),
					ohlcRow(since.Add(-24*time.Hour), "50000.00"),
					ohlcRow(since, "51000.10"),
				},
				"last": since.Add(72 * time.Hour).Unix(),
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server, now)
	points, err := client.DailyHistory(context.Background(), "BTC", "EUR", since)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || !points[0].AsOf.Equal(since) || points[0].Close != "51000.10" ||
		!points[1].AsOf.Equal(since.Add(48*time.Hour)) || points[1].Close != "53000.20" {
		t.Fatalf("points = %#v", points)
	}
}

func TestDailyHistoryDropsCurrentCandleFromMaximumResponse(t *testing.T) {
	now := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	since := utcDay(now).AddDate(0, 0, -maximumDailyHistoryRows)
	rows := make([]any, 0, maximumOHLCResponseRows)
	for day := 0; day <= maximumDailyHistoryRows; day++ {
		rows = append(rows, ohlcRow(since.AddDate(0, 0, day), "50000.00"))
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(t, response, map[string]any{
			"error":  []string{},
			"result": map[string]any{"XXBTZEUR": rows, "last": now.Unix()},
		})
	}))
	defer server.Close()

	client := testClient(t, server, now)
	points, err := client.DailyHistory(context.Background(), "BTC", "EUR", since)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != maximumDailyHistoryRows || !points[0].AsOf.Equal(since) ||
		!points[len(points)-1].AsOf.Equal(utcDay(now).AddDate(0, 0, -1)) {
		t.Fatalf("history bounds = %d, %s to %s", len(points), points[0].AsOf, points[len(points)-1].AsOf)
	}
}

func TestClientRejectsUnsupportedPairsWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	client := testClient(t, server, time.Now())

	_, err := client.CurrentQuotes(context.Background(), []string{"SOL"}, "EUR")
	if !errors.Is(err, ErrUnsupportedPair) || calls.Load() != 0 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
	_, err = client.DailyHistory(context.Background(), "BTC", "USD", time.Now().Add(-time.Hour))
	if !errors.Is(err, ErrUnsupportedPair) || calls.Load() != 0 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}

func TestQuoteAtReturnsUnavailableAfterBoundedWindows(t *testing.T) {
	target := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(t, response, map[string]any{
			"error": []string{}, "result": map[string]any{"count": 0, "trades": []any{}},
		})
	}))
	defer server.Close()
	client := testClient(t, server, target.Add(time.Hour))

	_, err := client.QuoteAt(context.Background(), "BTC", "EUR", target)
	if !errors.Is(err, ErrQuoteUnavailable) || calls.Load() != int32(len(quoteWindows)) {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(strings.Repeat("x", maximumResponseSize+1)))
	}))
	defer server.Close()
	client := testClient(t, server, time.Now())

	_, err := client.CurrentQuotes(context.Background(), []string{"BTC"}, "EUR")
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("error = %v", err)
	}
}

func TestRateLimitWaitHonorsContextCancellation(t *testing.T) {
	now := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		symbol := request.URL.Query().Get("symbol")
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"symbol": symbol, "quote_asset": "EUR",
				"bids": []map[string]any{{"price": "100"}},
				"asks": []map[string]any{{"price": "102"}},
			},
		})
	}))
	defer server.Close()
	client, err := New(Config{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now },
		MinimumRequestInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CurrentQuotes(context.Background(), []string{"BTC"}, "EUR"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.CurrentQuotes(ctx, []string{"ETH"}, "EUR")
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}

func TestOperationTimeoutIncludesRateLimitQueueWait(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		symbol := request.URL.Query().Get("symbol")
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"symbol": symbol, "quote_asset": "EUR",
				"bids": []map[string]any{{"price": "100"}},
				"asks": []map[string]any{{"price": "102"}},
			},
		})
	}))
	defer server.Close()
	client, err := New(Config{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: time.Now,
		MinimumRequestInterval: time.Hour, OperationTimeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CurrentQuotes(context.Background(), []string{"BTC"}, "EUR"); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, err = client.CurrentQuotes(context.Background(), []string{"ETH"}, "EUR")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("operation exceeded its overall deadline: %s", elapsed)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
}

func TestRequestLimiterBoundsQueueAndCancellationDoesNotReserveSlots(t *testing.T) {
	const interval = 80 * time.Millisecond
	limiter := newRequestLimiter(interval, 1, time.Now)
	if err := limiter.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstAdmission := time.Now()

	activeContext, cancelActive := context.WithCancel(context.Background())
	active := limiterRequest{ctx: activeContext, granted: make(chan error, 1)}
	limiter.queue <- active
	deadline := time.Now().Add(time.Second)
	for len(limiter.queue) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(limiter.queue) != 0 {
		t.Fatal("limiter worker did not consume the active request")
	}

	queuedContext, cancelQueued := context.WithCancel(context.Background())
	queued := limiterRequest{ctx: queuedContext, granted: make(chan error, 1)}
	limiter.queue <- queued
	if err := limiter.wait(context.Background()); !errors.Is(err, ErrRequestQueueFull) {
		t.Fatalf("saturated limiter error = %v", err)
	}

	cancelActive()
	cancelQueued()
	for name, request := range map[string]limiterRequest{"active": active, "queued": queued} {
		select {
		case err := <-request.granted:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s request error = %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for canceled %s request", name)
		}
	}

	remaining := time.Until(firstAdmission.Add(interval + 10*time.Millisecond))
	if remaining > 0 {
		time.Sleep(remaining)
	}
	nextContext, cancelNext := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelNext()
	if err := limiter.wait(nextContext); err != nil {
		t.Fatalf("canceled requests reserved future slots: %v", err)
	}
}

func TestCurrentQuoteCacheCoalescesAndExpires(t *testing.T) {
	now := time.Date(2026, 7, 14, 18, 30, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		price := strconv.Itoa(100 + int(call))
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"symbol": request.URL.Query().Get("symbol"), "quote_asset": "EUR",
				"bids": []map[string]any{{"price": price}},
				"asks": []map[string]any{{"price": price}},
			},
		})
	}))
	defer server.Close()
	client, err := New(Config{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now },
		MinimumRequestInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := client.CurrentQuotes(context.Background(), []string{"BTC"}, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CurrentQuotes(context.Background(), []string{"BTC"}, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || first[0] != second[0] || first[0].Price != "101" {
		t.Fatalf("cached quotes = %#v then %#v, calls = %d", first, second, calls.Load())
	}

	now = now.Add(currentQuoteCacheTTL)
	third, err := client.CurrentQuotes(context.Background(), []string{"BTC"}, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || third[0].Price != "102" {
		t.Fatalf("refreshed quote = %#v, calls = %d", third, calls.Load())
	}
}

func TestCurrentQuoteCacheCoalescesConcurrentRequests(t *testing.T) {
	now := time.Date(2026, 7, 14, 18, 30, 0, 0, time.UTC)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		enteredOnce.Do(func() { close(entered) })
		<-release
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"symbol": request.URL.Query().Get("symbol"), "quote_asset": "EUR",
				"bids": []map[string]any{{"price": "100"}},
				"asks": []map[string]any{{"price": "102"}},
			},
		})
	}))
	defer server.Close()
	client := testClient(t, server, now)

	const requestCount = 12
	results := make([][]Quote, requestCount)
	errorsFound := make([]error, requestCount)
	var waitGroup sync.WaitGroup
	for index := range requestCount {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			results[index], errorsFound[index] = client.CurrentQuotes(context.Background(), []string{"BTC"}, "EUR")
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	close(release)
	waitGroup.Wait()
	for index, err := range errorsFound {
		if err != nil || len(results[index]) != 1 || results[index][0].Price != "101" {
			t.Fatalf("result %d = %#v, error = %v", index, results[index], err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
}

func TestDailyHistoryCacheCoalescesAndReturnsCopies(t *testing.T) {
	since := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		enteredOnce.Do(func() { close(entered) })
		<-release
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"XXBTZEUR": []any{ohlcRow(since, "51000.10")}, "last": since.AddDate(0, 0, 1).Unix(),
			},
		})
	}))
	defer server.Close()
	client := testClient(t, server, now)

	const requestCount = 12
	results := make([][]DailyClose, requestCount)
	errorsFound := make([]error, requestCount)
	var waitGroup sync.WaitGroup
	for index := range requestCount {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			results[index], errorsFound[index] = client.DailyHistory(context.Background(), "BTC", "EUR", since)
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	close(release)
	waitGroup.Wait()
	for index, err := range errorsFound {
		if err != nil || len(results[index]) != 1 || results[index][0].Close != "51000.10" {
			t.Fatalf("result %d = %#v, error = %v", index, results[index], err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}

	results[0][0].Close = "mutated"
	cached, err := client.DailyHistory(context.Background(), "BTC", "EUR", since)
	if err != nil {
		t.Fatal(err)
	}
	if cached[0].Close != "51000.10" || calls.Load() != 1 {
		t.Fatalf("cached history = %#v, calls = %d", cached, calls.Load())
	}
}

func TestDailyHistoryCacheDoesNotCrossUTCDateBoundary(t *testing.T) {
	since := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 14, 23, 59, 59, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(t, response, map[string]any{
			"error": []string{},
			"result": map[string]any{
				"XXBTZEUR": []any{ohlcRow(since, "51000.10")}, "last": since.AddDate(0, 0, 1).Unix(),
			},
		})
	}))
	defer server.Close()
	client, err := New(Config{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now },
		MinimumRequestInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DailyHistory(context.Background(), "BTC", "EUR", since); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := client.DailyHistory(context.Background(), "BTC", "EUR", since); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want refresh after UTC date boundary", calls.Load())
	}
}

func testClient(t *testing.T, server *httptest.Server, now time.Time) *Client {
	t.Helper()
	client, err := New(Config{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now },
		MinimumRequestInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func postTrade(symbol, quoteAsset, price string, tradeTime time.Time) map[string]any {
	return map[string]any{
		"symbol": symbol, "quote_asset": quoteAsset, "price": price,
		"trade_ts": tradeTime.Format(time.RFC3339Nano),
	}
}

func ohlcRow(at time.Time, closePrice string) []any {
	return []any{at.Unix(), "1", "2", "0.5", closePrice, "1.5", "10", 5}
}

func writeJSON(t *testing.T, response http.ResponseWriter, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
