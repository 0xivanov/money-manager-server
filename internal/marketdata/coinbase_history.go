package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCoinbaseHistoryBaseURL = "https://api.exchange.coinbase.com"
	coinbaseHistoryPageDays       = 300
	maximumCoinbaseHistoryPages   = 25
)

type CoinbaseHistoryConfig struct {
	BaseURL          string
	HTTPClient       *http.Client
	Now              func() time.Time
	OperationTimeout time.Duration
}

type CoinbaseHistoryClient struct {
	baseURL          *url.URL
	httpClient       *http.Client
	now              func() time.Time
	operationTimeout time.Duration
}

var _ DailyHistoryProvider = (*CoinbaseHistoryClient)(nil)

func NewCoinbaseHistory(config CoinbaseHistoryConfig) (*CoinbaseHistoryClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultCoinbaseHistoryBaseURL
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Host == "" ||
		(parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") ||
		parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" || parsedBaseURL.User != nil {
		return nil, errors.New("Coinbase history base URL must be an absolute HTTP URL without credentials, query, or fragment")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultOperationTimeout}
	}
	httpClientCopy := *httpClient
	if httpClientCopy.CheckRedirect == nil {
		httpClientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	operationTimeout := config.OperationTimeout
	if operationTimeout == 0 {
		operationTimeout = httpClientCopy.Timeout
		if operationTimeout <= 0 {
			operationTimeout = defaultOperationTimeout
		}
	}
	if operationTimeout < 0 {
		return nil, errors.New("Coinbase history operation timeout must be positive")
	}

	return &CoinbaseHistoryClient{
		baseURL: parsedBaseURL, httpClient: &httpClientCopy,
		now: now, operationTimeout: operationTimeout,
	}, nil
}

func (c *CoinbaseHistoryClient) DailyHistory(
	ctx context.Context,
	symbol, currency string,
	since time.Time,
) ([]DailyClose, error) {
	symbol, currency, err := normalizePair(symbol, currency)
	if err != nil {
		return nil, err
	}
	if since.IsZero() {
		return nil, errors.New("market data history start time is required")
	}
	if since.After(c.now().UTC()) {
		return nil, errors.New("market data history start time cannot be in the future")
	}

	ctx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()
	start := utcDay(since)
	lastCompletedDay := utcDay(c.now()).AddDate(0, 0, -1)
	if lastCompletedDay.Before(start) {
		return []DailyClose{}, nil
	}

	points := make([]DailyClose, 0)
	seen := make(map[int64]bool)
	cursor := start
	for page := 0; !cursor.After(lastCompletedDay); page++ {
		if page >= maximumCoinbaseHistoryPages {
			return nil, errors.New("Coinbase history pagination exceeded its safety limit")
		}
		through := cursor.AddDate(0, 0, coinbaseHistoryPageDays-1)
		if through.After(lastCompletedDay) {
			through = lastCompletedDay
		}
		pagePoints, err := c.fetchDailyHistoryPage(ctx, symbol, currency, cursor, through)
		if err != nil {
			return nil, err
		}
		for _, point := range pagePoints {
			if point.AsOf.Before(start) || point.AsOf.After(lastCompletedDay) || seen[point.AsOf.Unix()] {
				continue
			}
			seen[point.AsOf.Unix()] = true
			points = append(points, point)
		}
		cursor = through.AddDate(0, 0, 1)
	}

	sort.Slice(points, func(i, j int) bool { return points[i].AsOf.Before(points[j].AsOf) })
	return points, nil
}

func (c *CoinbaseHistoryClient) fetchDailyHistoryPage(
	ctx context.Context,
	symbol, currency string,
	from, through time.Time,
) ([]DailyClose, error) {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + "/products/" + symbol + "-" + currency + "/candles"
	query := url.Values{
		"granularity": {"86400"},
		"start":       {from.Format(time.RFC3339)},
		"end":         {through.Format(time.RFC3339)},
	}
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Coinbase history request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "money-manager/1.0")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute Coinbase history request: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read Coinbase history response: %w", err)
	}
	if len(contents) > maximumResponseSize {
		return nil, errors.New("Coinbase history response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Coinbase history returned HTTP %d", response.StatusCode)
	}

	var rows [][]json.RawMessage
	if err := json.Unmarshal(contents, &rows); err != nil {
		return nil, fmt.Errorf("decode Coinbase history rows: %w", err)
	}
	if len(rows) > coinbaseHistoryPageDays+1 {
		return nil, errors.New("Coinbase history returned too many rows")
	}
	points := make([]DailyClose, 0, len(rows))
	for _, fields := range rows {
		if len(fields) < 5 || len(fields) > 6 {
			return nil, errors.New("Coinbase history returned a malformed candle")
		}
		unixSeconds, err := strconv.ParseInt(strings.Trim(string(fields[0]), `"`), 10, 64)
		if err != nil || unixSeconds <= 0 {
			return nil, errors.New("Coinbase history returned an invalid timestamp")
		}
		asOf := time.Unix(unixSeconds, 0).UTC()
		closePrice := strings.Trim(string(fields[4]), `"`)
		if _, _, err := positiveDecimal(closePrice); err != nil {
			return nil, fmt.Errorf("Coinbase history returned an invalid close price: %w", err)
		}
		points = append(points, DailyClose{AsOf: asOf, Close: closePrice})
	}
	return points, nil
}
