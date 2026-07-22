package marketdata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const ProviderKraken = "kraken"

type EquityInstrument struct {
	Symbol         string
	Exchange       string
	MarketCurrency string
}

var (
	ErrUnsupportedPair  = errors.New("unsupported market data pair")
	ErrQuoteUnavailable = errors.New("market data quote unavailable")
	ErrRequestQueueFull = errors.New("market data request queue is full")
)

// Provider supplies current and historical market prices without exposing a
// vendor-specific response shape to its consumers.
type Provider interface {
	QuoteAt(ctx context.Context, symbol, currency string, at time.Time) (Quote, error)
	CurrentQuotes(ctx context.Context, symbols []string, currency string) ([]Quote, error)
	DailyHistory(ctx context.Context, symbol, currency string, since time.Time) ([]DailyClose, error)
}

type DailyHistoryProvider interface {
	DailyHistory(ctx context.Context, symbol, currency string, since time.Time) ([]DailyClose, error)
}

type Quote struct {
	Symbol   string
	Currency string
	Price    string
	Provider string
	AsOf     time.Time
}

type DailyClose struct {
	AsOf  time.Time
	Close string
}

type Config struct {
	BaseURL                string
	HTTPClient             *http.Client
	Now                    func() time.Time
	MinimumRequestInterval time.Duration
	OperationTimeout       time.Duration
	MaximumQueuedRequests  int
}

type ProviderError struct {
	StatusCode int
	Errors     []string
}

func (e *ProviderError) Error() string {
	prefix := ProviderKraken + " returned an error"
	if e.StatusCode != 0 {
		prefix = fmt.Sprintf("%s returned HTTP %d", ProviderKraken, e.StatusCode)
	}
	if len(e.Errors) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(e.Errors, "; ")
}
