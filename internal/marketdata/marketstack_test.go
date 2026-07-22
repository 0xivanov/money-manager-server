package marketdata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMarketstackDailyHistoryMapsXetraAndReturnsEuroCloses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/eod" || request.URL.Query().Get("symbols") != "QDVE.DE" ||
			request.URL.Query().Get("access_key") != "secret-key" {
			t.Fatalf("request = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"pagination":{"count":2,"total":2},"data":[` +
			`{"symbol":"QDVE.DE","close":43.5,"date":"2026-07-17T00:00:00+0000"},` +
			`{"symbol":"QDVE.DE","close":42.25,"date":"2026-07-16T00:00:00+0000"}]}`))
	}))
	defer server.Close()
	client, err := NewMarketstack(MarketstackConfig{
		BaseURL: server.URL + "/v2", APIKey: "secret-key", FrankfurterURL: server.URL + "/v1",
		Now: func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	points, err := client.DailyHistory(context.Background(), EquityInstrument{
		Symbol: "qdve", Exchange: "xetra", MarketCurrency: "eur",
	}, "eur", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].Close != "42.25" || points[1].Close != "43.5" ||
		points[0].AsOf.Format("2006-01-02") != "2026-07-16" {
		t.Fatalf("points = %#v", points)
	}
}

func TestMarketstackDailyHistorySkipsRowsWithoutAUsableClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pagination":{"count":3,"total":3},"data":[` +
			`{"symbol":"MSTR","close":null,"date":"2026-07-15T00:00:00Z"},` +
			`{"symbol":"MSTR","close":0,"date":"2026-07-16T00:00:00Z"},` +
			`{"symbol":"MSTR","close":105.25,"date":"2026-07-17T00:00:00Z"}]}`))
	}))
	defer server.Close()
	client, err := NewMarketstack(MarketstackConfig{
		BaseURL: server.URL + "/v2", APIKey: "secret-key", FrankfurterURL: server.URL + "/v1",
		Now: func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	points, err := client.DailyHistory(context.Background(), EquityInstrument{
		Symbol: "MSTR", Exchange: "NASDAQ", MarketCurrency: "EUR",
	}, "EUR", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Close != "105.25" || points[0].AsOf.Format("2006-01-02") != "2026-07-17" {
		t.Fatalf("points = %#v", points)
	}
}

func TestMarketstackDailyHistoryConvertsNASDAQUSDToEUR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/eod":
			if request.URL.Query().Get("symbols") != "MSTR" {
				t.Fatalf("ticker = %q", request.URL.Query().Get("symbols"))
			}
			_, _ = w.Write([]byte(`{"pagination":{"count":2,"total":2},"data":[` +
				`{"symbol":"MSTR","close":100,"date":"2026-07-06T00:00:00Z"},` +
				`{"symbol":"MSTR","close":110,"date":"2026-07-07T00:00:00Z"}]}`))
		case "/v1/2026-06-24..2026-07-18":
			if request.URL.Query().Get("base") != "USD" || request.URL.Query().Get("symbols") != "EUR" {
				t.Fatalf("FX query = %s", request.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"rates":{"2026-07-03":{"EUR":0.85},"2026-07-07":{"EUR":0.86}}}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client, err := NewMarketstack(MarketstackConfig{
		BaseURL: server.URL + "/v2", APIKey: "secret-key", FrankfurterURL: server.URL + "/v1",
		Now: func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	points, err := client.DailyHistory(context.Background(), EquityInstrument{
		Symbol: "MSTR", Exchange: "NASDAQ", MarketCurrency: "USD",
	}, "EUR", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].Close != "85" || points[1].Close != "94.6" {
		t.Fatalf("converted points = %#v", points)
	}
}

func TestMarketstackNeverIncludesKeyInHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider unavailable", http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := NewMarketstack(MarketstackConfig{
		BaseURL: server.URL, APIKey: "do-not-leak", FrankfurterURL: server.URL,
		Now: func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.DailyHistory(context.Background(), EquityInstrument{
		Symbol: "MSTR", Exchange: "NASDAQ", MarketCurrency: "USD",
	}, "EUR", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || strings.Contains(err.Error(), "do-not-leak") || !strings.Contains(err.Error(), fmt.Sprint(http.StatusTooManyRequests)) {
		t.Fatalf("error = %v", err)
	}
}

func TestMarketstackRejectsUnsupportedExchange(t *testing.T) {
	client, err := NewMarketstack(MarketstackConfig{
		BaseURL: "https://example.com/v2", APIKey: "key", FrankfurterURL: "https://example.com/v1",
		Now: func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.DailyHistory(context.Background(), EquityInstrument{
		Symbol: "ACME", Exchange: "UNKNOWN", MarketCurrency: "EUR",
	}, "EUR", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(fmt.Sprint(err), ErrUnsupportedPair.Error()) {
		t.Fatalf("error = %v", err)
	}
}
