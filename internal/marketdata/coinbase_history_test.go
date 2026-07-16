package marketdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoinbaseDailyHistoryPaginatesAndSortsLongRanges(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.AddDate(0, 0, 302)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/products/BTC-EUR/candles" || request.URL.Query().Get("granularity") != "86400" {
			t.Errorf("request = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		from, fromErr := time.Parse(time.RFC3339, request.URL.Query().Get("start"))
		through, throughErr := time.Parse(time.RFC3339, request.URL.Query().Get("end"))
		if fromErr != nil || throughErr != nil {
			t.Fatalf("range = %q to %q", request.URL.Query().Get("start"), request.URL.Query().Get("end"))
		}
		rows := make([][]any, 0)
		for day := through; !day.Before(from); day = day.AddDate(0, 0, -1) {
			rows = append(rows, []any{day.Unix(), "90.00", "110.00", "95.00", "100.50", "12.00"})
		}
		writeJSON(t, response, rows)
	}))
	defer server.Close()

	client, err := NewCoinbaseHistory(CoinbaseHistoryConfig{
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now },
		OperationTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	points, err := client.DailyHistory(context.Background(), " btc ", " eur ", start)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(points) != 302 {
		t.Fatalf("calls = %d, points = %d", calls.Load(), len(points))
	}
	if !points[0].AsOf.Equal(start) || !points[len(points)-1].AsOf.Equal(now.AddDate(0, 0, -1)) ||
		points[0].Close != "100.50" {
		t.Fatalf("history bounds = %#v to %#v", points[0], points[len(points)-1])
	}
}

func TestCoinbaseDailyHistoryRejectsUnsupportedPairBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	client, err := NewCoinbaseHistory(CoinbaseHistoryConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DailyHistory(context.Background(), "SOL", "EUR", time.Now().Add(-time.Hour)); err == nil || calls.Load() != 0 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}
