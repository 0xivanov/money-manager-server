package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderMarketstack          = "marketstack"
	defaultMarketstackBaseURL    = "https://api.marketstack.com/v2"
	defaultFrankfurterBaseURL    = "https://api.frankfurter.dev/v1"
	marketstackMaximumResponse   = 8 << 20
	marketstackPageSize          = 1000
	marketstackMaximumPages      = 20
	marketstackMaximumErrorBytes = 300
)

type MarketstackConfig struct {
	BaseURL          string
	APIKey           string
	FrankfurterURL   string
	HTTPClient       *http.Client
	Now              func() time.Time
	OperationTimeout time.Duration
}

// MarketstackClient reads end-of-day equity prices. Prices from USD listings
// are converted to EUR using Frankfurter's ECB-backed daily exchange rates.
type MarketstackClient struct {
	baseURL          *url.URL
	apiKey           string
	frankfurterURL   *url.URL
	httpClient       *http.Client
	now              func() time.Time
	operationTimeout time.Duration
}

type marketstackPage struct {
	Pagination struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
		Count  int `json:"count"`
		Total  int `json:"total"`
	} `json:"pagination"`
	Data []struct {
		Close  json.Number `json:"close"`
		Date   string      `json:"date"`
		Symbol string      `json:"symbol"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func NewMarketstack(config MarketstackConfig) (*MarketstackClient, error) {
	baseURL, err := parseProviderBaseURL(config.BaseURL, defaultMarketstackBaseURL, "Marketstack")
	if err != nil {
		return nil, err
	}
	frankfurterURL, err := parseProviderBaseURL(config.FrankfurterURL, defaultFrankfurterBaseURL, "Frankfurter")
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return nil, errors.New("Marketstack API key is required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	httpClientCopy := *httpClient
	if httpClientCopy.CheckRedirect == nil {
		httpClientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	timeout := config.OperationTimeout
	if timeout == 0 {
		timeout = httpClientCopy.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
	}
	if timeout < 0 {
		return nil, errors.New("Marketstack operation timeout must be positive")
	}
	return &MarketstackClient{
		baseURL: baseURL, apiKey: apiKey, frankfurterURL: frankfurterURL,
		httpClient: &httpClientCopy, now: now, operationTimeout: timeout,
	}, nil
}

func (c *MarketstackClient) DailyHistory(
	ctx context.Context,
	instrument EquityInstrument,
	currency string,
	since time.Time,
) ([]DailyClose, error) {
	instrument.Symbol = strings.ToUpper(strings.TrimSpace(instrument.Symbol))
	instrument.Exchange = strings.ToUpper(strings.TrimSpace(instrument.Exchange))
	instrument.MarketCurrency = strings.ToUpper(strings.TrimSpace(instrument.MarketCurrency))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if instrument.Symbol == "" || instrument.Exchange == "" || instrument.MarketCurrency == "" || currency == "" {
		return nil, errors.New("Marketstack instrument, exchange, market currency, and target currency are required")
	}
	if since.IsZero() {
		return nil, errors.New("Marketstack history start time is required")
	}
	now := c.now().UTC()
	if since.After(now) {
		return nil, errors.New("Marketstack history start time cannot be in the future")
	}
	if instrument.MarketCurrency != currency && !(instrument.MarketCurrency == "USD" && currency == "EUR") {
		return nil, fmt.Errorf("%w: Marketstack currency conversion %s/%s", ErrUnsupportedPair, instrument.MarketCurrency, currency)
	}
	ticker, err := marketstackTicker(instrument)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()
	rows, err := c.endOfDayRows(ctx, ticker, since.UTC(), now)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w for %s", ErrQuoteUnavailable, instrument.Symbol)
	}
	if instrument.MarketCurrency == currency {
		return marketstackRowsToCloses(rows, nil)
	}
	rates, err := c.frankfurterRates(ctx, instrument.MarketCurrency, currency, since.UTC().AddDate(0, 0, -7), now)
	if err != nil {
		return nil, err
	}
	return marketstackRowsToCloses(rows, rates)
}

type marketstackRow struct {
	at    time.Time
	close *big.Rat
}

func (c *MarketstackClient) endOfDayRows(ctx context.Context, ticker string, since, through time.Time) ([]marketstackRow, error) {
	rows := make([]marketstackRow, 0, 512)
	for offset, pageNumber := 0, 0; ; pageNumber++ {
		if pageNumber >= marketstackMaximumPages {
			return nil, errors.New("Marketstack history pagination exceeded its safety limit")
		}
		query := url.Values{
			"access_key": {c.apiKey},
			"symbols":    {ticker},
			"date_from":  {since.Format("2006-01-02")},
			"date_to":    {through.Format("2006-01-02")},
			"sort":       {"ASC"},
			"limit":      {strconv.Itoa(marketstackPageSize)},
			"offset":     {strconv.Itoa(offset)},
		}
		var page marketstackPage
		if err := c.getJSON(ctx, c.baseURL, "/eod", query, &page, "Marketstack"); err != nil {
			return nil, err
		}
		if page.Error != nil {
			return nil, fmt.Errorf("Marketstack returned %s: %s", strings.TrimSpace(page.Error.Code), c.scrub(strings.TrimSpace(page.Error.Message)))
		}
		for _, item := range page.Data {
			at, err := parseMarketstackDate(item.Date)
			if err != nil {
				return nil, errors.New("Marketstack returned an invalid date")
			}
			price, ok := new(big.Rat).SetString(item.Close.String())
			if !ok || price.Sign() <= 0 {
				// Marketstack can include placeholder rows with a null close for
				// non-trading days. They are not prices and should not invalidate
				// the otherwise usable series.
				continue
			}
			rows = append(rows, marketstackRow{at: at, close: price})
		}
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			break
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	return deduplicateMarketstackRows(rows), nil
}

func (c *MarketstackClient) frankfurterRates(
	ctx context.Context, base, target string, since, through time.Time,
) (map[time.Time]*big.Rat, error) {
	path := "/" + since.Format("2006-01-02") + ".." + through.Format("2006-01-02")
	query := url.Values{"base": {base}, "symbols": {target}}
	var response struct {
		Rates map[string]map[string]json.Number `json:"rates"`
	}
	if err := c.getJSON(ctx, c.frankfurterURL, path, query, &response, "Frankfurter"); err != nil {
		return nil, err
	}
	rates := make(map[time.Time]*big.Rat, len(response.Rates))
	for dateValue, currencies := range response.Rates {
		date, err := time.Parse("2006-01-02", dateValue)
		if err != nil {
			return nil, errors.New("Frankfurter returned an invalid date")
		}
		rate, ok := new(big.Rat).SetString(currencies[target].String())
		if !ok || rate.Sign() <= 0 {
			return nil, errors.New("Frankfurter returned an invalid exchange rate")
		}
		rates[date.UTC()] = rate
	}
	if len(rates) == 0 {
		return nil, errors.New("Frankfurter returned no exchange rates")
	}
	return rates, nil
}

func (c *MarketstackClient) getJSON(
	ctx context.Context, baseURL *url.URL, path string, query url.Values, target any, provider string,
) error {
	endpoint := *baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create %s request: %w", provider, err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return fmt.Errorf("execute %s request: %w", provider, urlError.Err)
		}
		return fmt.Errorf("execute %s request: request failed", provider)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, marketstackMaximumResponse+1))
	if err != nil {
		return fmt.Errorf("read %s response: %w", provider, err)
	}
	if len(body) > marketstackMaximumResponse {
		return fmt.Errorf("%s response exceeds size limit", provider)
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		message = c.scrub(message)
		if len(message) > marketstackMaximumErrorBytes {
			message = message[:marketstackMaximumErrorBytes]
		}
		return fmt.Errorf("%s returned HTTP %d: %s", provider, response.StatusCode, message)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode %s response: %w", provider, err)
	}
	return nil
}

func (c *MarketstackClient) scrub(value string) string {
	if c.apiKey == "" {
		return value
	}
	return strings.ReplaceAll(value, c.apiKey, "[redacted]")
}

func parseProviderBaseURL(value, fallback, provider string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s base URL must be an absolute HTTP URL without credentials, query, or fragment", provider)
	}
	return parsed, nil
}

func marketstackTicker(instrument EquityInstrument) (string, error) {
	switch instrument.Exchange {
	case "NASDAQ", "XNAS":
		return instrument.Symbol, nil
	case "XETRA", "XETR":
		if strings.HasSuffix(instrument.Symbol, ".DE") {
			return instrument.Symbol, nil
		}
		return instrument.Symbol + ".DE", nil
	default:
		return "", fmt.Errorf("%w: Marketstack exchange %s", ErrUnsupportedPair, instrument.Exchange)
	}
}

func parseMarketstackDate(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700", "2006-01-02"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, errors.New("invalid Marketstack date")
}

func marketstackRowsToCloses(rows []marketstackRow, rates map[time.Time]*big.Rat) ([]DailyClose, error) {
	result := make([]DailyClose, 0, len(rows))
	var rateDates []time.Time
	if rates != nil {
		rateDates = make([]time.Time, 0, len(rates))
		for date := range rates {
			rateDates = append(rateDates, date)
		}
		sort.Slice(rateDates, func(i, j int) bool { return rateDates[i].Before(rateDates[j]) })
	}
	for _, row := range rows {
		price := new(big.Rat).Set(row.close)
		if rates != nil {
			rate := latestRateAtOrBefore(rates, rateDates, row.at)
			if rate == nil {
				return nil, fmt.Errorf("missing exchange rate for %s", row.at.Format("2006-01-02"))
			}
			price.Mul(price, rate)
		}
		result = append(result, DailyClose{AsOf: row.at, Close: formatMarketstackDecimal(price)})
	}
	return result, nil
}

func latestRateAtOrBefore(rates map[time.Time]*big.Rat, dates []time.Time, at time.Time) *big.Rat {
	index := sort.Search(len(dates), func(index int) bool { return dates[index].After(at) })
	if index == 0 {
		return nil
	}
	return rates[dates[index-1]]
}

func deduplicateMarketstackRows(rows []marketstackRow) []marketstackRow {
	if len(rows) < 2 {
		return rows
	}
	result := rows[:0]
	for _, row := range rows {
		if len(result) > 0 && result[len(result)-1].at.Equal(row.at) {
			result[len(result)-1] = row
			continue
		}
		result = append(result, row)
	}
	return result
}

func formatMarketstackDecimal(value *big.Rat) string {
	formatted := value.FloatString(8)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}
