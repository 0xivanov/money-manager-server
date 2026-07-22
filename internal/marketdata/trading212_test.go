package marketdata

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTrading212CurrentQuoteUsesReadOnlyPositionsAndCaches(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.Method != http.MethodGet || request.URL.Path != "/api/v0/equity/positions" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		wantAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("read-key:read-secret"))
		if request.Header.Get("Authorization") != wantAuthorization {
			t.Fatalf("authorization header = %q", request.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(writer, `[{"currentPrice":95.35,"instrument":{"currency":"USD","isin":"US5949724083","name":"Strategy","ticker":"MSTR_US_EQ"},"quantity":0.5,"walletImpact":{"currency":"EUR","currentValue":41.75}}]`)
	}))
	defer server.Close()

	client, err := NewTrading212(Trading212Config{
		BaseURL: server.URL + "/api/v0", APIKey: "read-key", APISecret: "read-secret",
		HTTPClient: server.Client(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		quote, quoteErr := client.CurrentQuote(context.Background(), EquityInstrument{
			Symbol: "MSTR", Exchange: "NASDAQ", MarketCurrency: "USD",
		}, "EUR")
		if quoteErr != nil {
			t.Fatal(quoteErr)
		}
		if quote.Price != "83.5" || quote.Provider != ProviderTrading212 || !quote.AsOf.Equal(now) {
			t.Fatalf("quote = %#v", quote)
		}
	}
	if calls != 1 {
		t.Fatalf("position calls = %d", calls)
	}
}

func TestTrading212QuoteAtUsesClosestAccountFill(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v0/equity/metadata/instruments":
			_, _ = fmt.Fprint(writer, `[{"currencyCode":"EUR","isin":"IE00B3WJKG14","name":"iShares S&P 500 Information Technology Sector","ticker":"QDVEd_XETR_EQ"}]`)
		case "/api/v0/equity/history/orders":
			if request.URL.Query().Get("ticker") != "QDVEd_XETR_EQ" || request.URL.Query().Get("limit") != "50" {
				t.Fatalf("query = %s", request.URL.RawQuery)
			}
			_, _ = fmt.Fprint(writer, `{"items":[
				{"fill":{"filledAt":"2026-07-17T07:05:10Z","price":43.325,"quantity":2.30813618,"walletImpact":{"currency":"EUR","netValue":100}},"order":{"instrument":{"currency":"EUR","ticker":"QDVEd_XETR_EQ"},"ticker":"QDVEd_XETR_EQ"}},
				{"fill":{"filledAt":"2026-06-17T07:05:04Z","price":40,"quantity":2,"walletImpact":{"currency":"EUR","netValue":80}},"order":{"instrument":{"currency":"EUR","ticker":"QDVEd_XETR_EQ"},"ticker":"QDVEd_XETR_EQ"}}
			]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewTrading212(Trading212Config{
		BaseURL: server.URL + "/api/v0", APIKey: "read-key", APISecret: "read-secret",
		HTTPClient: server.Client(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	quote, err := client.QuoteAt(context.Background(), EquityInstrument{
		Symbol: "QDVE", Exchange: "XETRA", MarketCurrency: "EUR",
	}, "EUR", time.Date(2026, 7, 17, 7, 5, 12, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if quote.Price != "43.325" || quote.AsOf.Format(time.RFC3339) != "2026-07-17T07:05:10Z" {
		t.Fatalf("quote = %#v", quote)
	}
}

func TestTrading212ErrorsRedactCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "read-key and read-secret are invalid", http.StatusUnauthorized)
	}))
	defer server.Close()
	client, err := NewTrading212(Trading212Config{
		BaseURL: server.URL, APIKey: "read-key", APISecret: "read-secret", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CurrentQuote(context.Background(), EquityInstrument{
		Symbol: "MSTR", Exchange: "NASDAQ", MarketCurrency: "USD",
	}, "EUR")
	if err == nil || strings.Contains(err.Error(), "read-key") || strings.Contains(err.Error(), "read-secret") ||
		!strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error = %v", err)
	}
}
