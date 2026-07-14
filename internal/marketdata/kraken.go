package marketdata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL          = "https://api.kraken.com"
	maximumResponseSize     = 2 << 20
	maximumPostTradeRows    = 1000
	maximumPostTradePages   = 10
	maximumOrderBookLevels  = 10
	maximumDailyHistoryRows = 720
	maximumOHLCResponseRows = maximumDailyHistoryRows + 1
	defaultRequestInterval  = 2 * time.Second
	defaultOperationTimeout = 10 * time.Second
	currentQuoteCacheTTL    = 5 * time.Second
	dailyHistoryCacheTTL    = 15 * time.Minute
)

var (
	decimalPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
	quoteWindows   = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 5 * time.Minute}
)

type Client struct {
	baseURL                *url.URL
	httpClient             *http.Client
	now                    func() time.Time
	minimumRequestInterval time.Duration
	operationTimeout       time.Duration
	limiter                *requestLimiter

	cacheMu           sync.Mutex
	currentQuotes     map[string]cachedQuote
	currentQuoteCalls map[string]*quoteCall
	dailyHistories    map[string]cachedHistory
	dailyHistoryCalls map[string]*historyCall
}

var _ Provider = (*Client)(nil)

func New(config Config) (*Client, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Host == "" ||
		(parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") ||
		parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" || parsedBaseURL.User != nil {
		return nil, errors.New("market data base URL must be an absolute HTTP URL without credentials, query, or fragment")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
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
	minimumRequestInterval := config.MinimumRequestInterval
	if minimumRequestInterval == 0 {
		minimumRequestInterval = defaultRequestInterval
	} else if minimumRequestInterval < 0 {
		minimumRequestInterval = 0
	}
	operationTimeout := config.OperationTimeout
	if operationTimeout == 0 {
		operationTimeout = httpClientCopy.Timeout
		if operationTimeout <= 0 {
			operationTimeout = defaultOperationTimeout
		}
	}
	if operationTimeout < 0 {
		return nil, errors.New("market data operation timeout must be positive")
	}
	maximumQueuedRequests := config.MaximumQueuedRequests
	if maximumQueuedRequests == 0 {
		maximumQueuedRequests = defaultMaximumQueuedRequests
	}
	if maximumQueuedRequests < 1 {
		return nil, errors.New("market data maximum queued requests must be positive")
	}

	client := &Client{
		baseURL: parsedBaseURL, httpClient: &httpClientCopy, now: now,
		minimumRequestInterval: minimumRequestInterval, operationTimeout: operationTimeout,
		currentQuotes: make(map[string]cachedQuote), currentQuoteCalls: make(map[string]*quoteCall),
		dailyHistories: make(map[string]cachedHistory), dailyHistoryCalls: make(map[string]*historyCall),
	}
	client.limiter = newRequestLimiter(minimumRequestInterval, maximumQueuedRequests, now)
	return client, nil
}

func (c *Client) QuoteAt(
	ctx context.Context,
	symbol, currency string,
	at time.Time,
) (Quote, error) {
	symbol, currency, err := normalizePair(symbol, currency)
	if err != nil {
		return Quote{}, err
	}
	if at.IsZero() {
		return Quote{}, errors.New("market data quote time is required")
	}
	at = at.UTC()
	now := c.now().UTC()
	if at.After(now) {
		return Quote{}, errors.New("market data quote time cannot be in the future")
	}
	ctx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()

	for _, window := range quoteWindows {
		from := at.Add(-window)
		through := at.Add(window)
		if through.After(now) {
			through = now
		}
		quote, found, err := c.quoteInRange(ctx, symbol, currency, at, from, through)
		if err != nil {
			return Quote{}, err
		}
		if found {
			return quote, nil
		}
	}
	return Quote{}, fmt.Errorf("%w for %s/%s at %s", ErrQuoteUnavailable, symbol, currency, at.Format(time.RFC3339Nano))
}

func (c *Client) CurrentQuotes(
	ctx context.Context,
	symbols []string,
	currency string,
) ([]Quote, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "EUR" {
		return nil, fmt.Errorf("%w: */%s", ErrUnsupportedPair, currency)
	}
	if len(symbols) == 0 {
		return []Quote{}, nil
	}
	if len(symbols) > 2 {
		return nil, errors.New("at most two current market data symbols are supported")
	}

	seen := make(map[string]struct{}, len(symbols))
	normalized := make([]string, len(symbols))
	for index, symbol := range symbols {
		var err error
		normalized[index], _, err = normalizePair(symbol, currency)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[normalized[index]]; exists {
			return nil, fmt.Errorf("duplicate market data symbol %q", normalized[index])
		}
		seen[normalized[index]] = struct{}{}
	}
	ctx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()

	quotes := make([]Quote, 0, len(normalized))
	for _, symbol := range normalized {
		quote, err := c.currentQuoteWithCache(ctx, symbol, currency)
		if err != nil {
			return nil, err
		}
		quotes = append(quotes, quote)
	}
	return quotes, nil
}

func (c *Client) DailyHistory(
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
	since = since.UTC()
	if since.After(c.now().UTC()) {
		return nil, errors.New("market data history start time cannot be in the future")
	}
	ctx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()
	return c.dailyHistoryWithCache(ctx, symbol, currency, since)
}

func (c *Client) fetchDailyHistory(
	ctx context.Context,
	symbol, currency string,
	since time.Time,
) ([]DailyClose, error) {
	query := url.Values{
		"pair":     {symbol + currency},
		"interval": {"1440"},
		"since":    {strconv.FormatInt(since.Unix(), 10)},
	}
	var response struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := c.get(ctx, "/0/public/OHLC", query, &response); err != nil {
		return nil, err
	}

	var rawRows json.RawMessage
	for key, value := range response.Result {
		if key == "last" {
			continue
		}
		if rawRows != nil {
			return nil, errors.New("kraken returned multiple OHLC result sets")
		}
		rawRows = value
	}
	if rawRows == nil {
		return nil, errors.New("kraken returned no OHLC result set")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return nil, fmt.Errorf("decode kraken OHLC rows: %w", err)
	}
	if len(rows) > maximumOHLCResponseRows {
		return nil, errors.New("kraken returned too many OHLC rows")
	}

	points := make([]DailyClose, 0, len(rows))
	currentDay := utcDay(c.now())
	for _, rawRow := range rows {
		var fields []json.RawMessage
		if err := json.Unmarshal(rawRow, &fields); err != nil {
			return nil, fmt.Errorf("decode kraken OHLC row: %w", err)
		}
		if len(fields) < 5 || len(fields) > 16 {
			return nil, errors.New("kraken returned a malformed OHLC row")
		}
		unixSeconds, err := strconv.ParseInt(string(fields[0]), 10, 64)
		if err != nil || unixSeconds <= 0 {
			return nil, errors.New("kraken returned an invalid OHLC timestamp")
		}
		asOf := time.Unix(unixSeconds, 0).UTC()
		if asOf.After(c.now().UTC()) {
			return nil, errors.New("kraken returned a future OHLC timestamp")
		}
		var closePrice string
		if err := json.Unmarshal(fields[4], &closePrice); err != nil {
			return nil, errors.New("kraken returned a non-string OHLC close price")
		}
		if _, _, err := positiveDecimal(closePrice); err != nil {
			return nil, fmt.Errorf("kraken returned an invalid OHLC close price: %w", err)
		}
		// Kraken always appends the current, not-yet-committed candle to as
		// many as 720 completed OHLC samples. CurrentQuotes supplies the live
		// mark, so history only exposes completed daily closes.
		if asOf.Before(since) || !asOf.Before(currentDay) {
			continue
		}
		points = append(points, DailyClose{AsOf: asOf, Close: closePrice})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].AsOf.Before(points[j].AsOf) })
	for index := 1; index < len(points); index++ {
		if points[index].AsOf.Equal(points[index-1].AsOf) {
			return nil, errors.New("kraken returned duplicate OHLC timestamps")
		}
	}
	if len(points) > maximumDailyHistoryRows {
		return nil, errors.New("kraken returned too many completed OHLC rows")
	}
	return points, nil
}

func (c *Client) quoteInRange(
	ctx context.Context,
	symbol, currency string,
	target, from, through time.Time,
) (Quote, bool, error) {
	cursor := from
	var best Quote
	var bestDistance time.Duration
	found := false

	for page := 0; page < maximumPostTradePages; page++ {
		query := url.Values{
			"symbol":  {symbol + "/" + currency},
			"from_ts": {cursor.Format(time.RFC3339Nano)},
			"to_ts":   {through.Format(time.RFC3339Nano)},
			"count":   {strconv.Itoa(maximumPostTradeRows)},
		}
		var response postTradeResponse
		if err := c.get(ctx, "/0/public/PostTrade", query, &response); err != nil {
			return Quote{}, false, err
		}
		if response.Result.Count < 0 || response.Result.Count > maximumPostTradeRows ||
			len(response.Result.Trades) > maximumPostTradeRows || response.Result.Count != len(response.Result.Trades) {
			return Quote{}, false, errors.New("kraken returned an invalid post-trade result size")
		}

		for _, trade := range response.Result.Trades {
			if trade.Symbol != symbol+"/"+currency || trade.QuoteAsset != currency {
				return Quote{}, false, errors.New("kraken returned a mismatched post-trade pair")
			}
			if _, _, err := positiveDecimal(trade.Price); err != nil {
				return Quote{}, false, fmt.Errorf("kraken returned an invalid trade price: %w", err)
			}
			tradeTime, err := time.Parse(time.RFC3339Nano, trade.TradeTime)
			if err != nil {
				return Quote{}, false, errors.New("kraken returned an invalid trade timestamp")
			}
			tradeTime = tradeTime.UTC()
			if tradeTime.Before(from) || tradeTime.After(through) {
				return Quote{}, false, errors.New("kraken returned a trade outside the requested range")
			}
			distance := absoluteDuration(tradeTime.Sub(target))
			if !found || distance < bestDistance ||
				(distance == bestDistance && tradeTime.Before(best.AsOf)) {
				best = Quote{Symbol: symbol, Currency: currency, Price: trade.Price, Provider: ProviderKraken, AsOf: tradeTime}
				bestDistance = distance
				found = true
			}
		}

		if response.Result.Count < maximumPostTradeRows {
			return best, found, nil
		}
		lastTime, err := time.Parse(time.RFC3339Nano, response.Result.LastTime)
		if err != nil {
			return Quote{}, false, errors.New("kraken returned an invalid post-trade continuation timestamp")
		}
		lastTime = lastTime.UTC()
		if !lastTime.After(cursor) {
			return Quote{}, false, errors.New("kraken returned a non-advancing post-trade continuation timestamp")
		}
		if !lastTime.Before(through) {
			return best, found, nil
		}
		cursor = lastTime
	}
	return Quote{}, false, errors.New("kraken post-trade pagination exceeded its safety limit")
}

func (c *Client) fetchCurrentQuote(ctx context.Context, symbol, currency string) (Quote, error) {
	query := url.Values{"symbol": {symbol + "/" + currency}}
	var response preTradeResponse
	if err := c.get(ctx, "/0/public/PreTrade", query, &response); err != nil {
		return Quote{}, err
	}
	if response.Result.Symbol != symbol+"/"+currency || response.Result.QuoteAsset != currency {
		return Quote{}, errors.New("kraken returned a mismatched pre-trade pair")
	}
	bestBid, bidScale, err := bestPrice(response.Result.Bids, true)
	if err != nil {
		return Quote{}, fmt.Errorf("validate kraken bids: %w", err)
	}
	bestAsk, askScale, err := bestPrice(response.Result.Asks, false)
	if err != nil {
		return Quote{}, fmt.Errorf("validate kraken asks: %w", err)
	}
	if bestBid.Cmp(bestAsk) > 0 {
		return Quote{}, errors.New("kraken returned a crossed order book")
	}
	midpoint := new(big.Rat).Add(bestBid, bestAsk)
	midpoint.Quo(midpoint, big.NewRat(2, 1))
	scale := max(bidScale, askScale) + 1
	price := normalizeDecimal(midpoint.FloatString(scale))
	return Quote{
		Symbol: symbol, Currency: currency, Price: price,
		Provider: ProviderKraken, AsOf: c.now().UTC(),
	}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, destination any) error {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + path
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return fmt.Errorf("create kraken request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if c.limiter != nil {
		if err := c.limiter.wait(ctx); err != nil {
			return fmt.Errorf("wait to execute kraken request: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("wait to execute kraken request: %w", err)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("execute kraken request: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseSize+1))
	if err != nil {
		return fmt.Errorf("read kraken response: %w", err)
	}
	if len(contents) > maximumResponseSize {
		return errors.New("kraken response exceeds size limit")
	}

	providerErrors := parseKrakenErrors(contents)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &ProviderError{StatusCode: response.StatusCode, Errors: providerErrors}
	}
	var metadata struct {
		Errors []string `json:"error"`
	}
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return fmt.Errorf("decode kraken response: %w", err)
	}
	if len(metadata.Errors) > 0 {
		return &ProviderError{StatusCode: response.StatusCode, Errors: cleanedErrors(metadata.Errors)}
	}
	if destination == nil {
		return nil
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		return fmt.Errorf("decode kraken response: %w", err)
	}
	return nil
}

type postTradeResponse struct {
	Result struct {
		LastTime string `json:"last_ts"`
		Count    int    `json:"count"`
		Trades   []struct {
			Price      string `json:"price"`
			Symbol     string `json:"symbol"`
			QuoteAsset string `json:"quote_asset"`
			TradeTime  string `json:"trade_ts"`
		} `json:"trades"`
	} `json:"result"`
}

type orderBookLevel struct {
	Price string `json:"price"`
}

type preTradeResponse struct {
	Result struct {
		Symbol     string           `json:"symbol"`
		QuoteAsset string           `json:"quote_asset"`
		Bids       []orderBookLevel `json:"bids"`
		Asks       []orderBookLevel `json:"asks"`
	} `json:"result"`
}

func normalizePair(symbol, currency string) (string, string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "EUR" || (symbol != "BTC" && symbol != "ETH") {
		return "", "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPair, symbol, currency)
	}
	return symbol, currency, nil
}

func bestPrice(levels []orderBookLevel, highest bool) (*big.Rat, int, error) {
	if len(levels) == 0 || len(levels) > maximumOrderBookLevels {
		return nil, 0, errors.New("invalid order book level count")
	}
	var best *big.Rat
	bestScale := 0
	for _, level := range levels {
		price, scale, err := positiveDecimal(level.Price)
		if err != nil {
			return nil, 0, err
		}
		if best == nil || (highest && price.Cmp(best) > 0) || (!highest && price.Cmp(best) < 0) {
			best = price
			bestScale = scale
		}
	}
	return best, bestScale, nil
}

func positiveDecimal(value string) (*big.Rat, int, error) {
	if !decimalPattern.MatchString(value) {
		return nil, 0, errors.New("price is not a decimal number")
	}
	price, ok := new(big.Rat).SetString(value)
	if !ok || price.Sign() <= 0 {
		return nil, 0, errors.New("price must be greater than zero")
	}
	scale := 0
	if decimal := strings.IndexByte(value, '.'); decimal >= 0 {
		scale = len(value) - decimal - 1
	}
	return price, scale, nil
}

func normalizeDecimal(value string) string {
	if strings.Contains(value, ".") {
		value = strings.TrimRight(value, "0")
		value = strings.TrimRight(value, ".")
	}
	return value
}

func parseKrakenErrors(contents []byte) []string {
	var response struct {
		Errors []string `json:"error"`
	}
	if json.Unmarshal(bytes.TrimSpace(contents), &response) != nil {
		return nil
	}
	return cleanedErrors(response.Errors)
}

func cleanedErrors(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
