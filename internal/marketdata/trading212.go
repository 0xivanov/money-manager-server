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
	"strings"
	"sync"
	"time"
)

const (
	ProviderTrading212       = "trading212"
	defaultTrading212BaseURL = "https://live.trading212.com/api/v0"
	trading212MaxResponse    = 4 << 20
	trading212MaximumError   = 300
	trading212PositionTTL    = 5 * time.Minute
	trading212InstrumentTTL  = 24 * time.Hour
	trading212MaxOrderAge    = 7 * 24 * time.Hour
)

type Trading212Config struct {
	BaseURL          string
	APIKey           string
	APISecret        string
	HTTPClient       *http.Client
	Now              func() time.Time
	OperationTimeout time.Duration
}

type Trading212Client struct {
	baseURL          *url.URL
	apiKey           string
	apiSecret        string
	httpClient       *http.Client
	now              func() time.Time
	operationTimeout time.Duration

	cacheMu              sync.Mutex
	positions            []trading212Position
	positionsExpiresAt   time.Time
	instruments          []trading212Instrument
	instrumentsExpiresAt time.Time
}

type trading212Instrument struct {
	CurrencyCode string `json:"currencyCode"`
	Currency     string `json:"currency"`
	ISIN         string `json:"isin"`
	Name         string `json:"name"`
	Ticker       string `json:"ticker"`
}

func (i trading212Instrument) currency() string {
	if strings.TrimSpace(i.Currency) != "" {
		return strings.ToUpper(strings.TrimSpace(i.Currency))
	}
	return strings.ToUpper(strings.TrimSpace(i.CurrencyCode))
}

type trading212WalletImpact struct {
	Currency     string      `json:"currency"`
	CurrentValue json.Number `json:"currentValue"`
	NetValue     json.Number `json:"netValue"`
}

type trading212Position struct {
	CurrentPrice json.Number            `json:"currentPrice"`
	Instrument   trading212Instrument   `json:"instrument"`
	Quantity     json.Number            `json:"quantity"`
	WalletImpact trading212WalletImpact `json:"walletImpact"`
}

type trading212HistoricalOrder struct {
	Fill struct {
		FilledAt     string                 `json:"filledAt"`
		Price        json.Number            `json:"price"`
		Quantity     json.Number            `json:"quantity"`
		WalletImpact trading212WalletImpact `json:"walletImpact"`
	} `json:"fill"`
	Order struct {
		Instrument trading212Instrument `json:"instrument"`
		Ticker     string               `json:"ticker"`
	} `json:"order"`
}

type trading212HTTPError struct {
	status  int
	message string
}

func (e *trading212HTTPError) Error() string {
	return fmt.Sprintf("Trading 212 returned HTTP %d: %s", e.status, e.message)
}

func NewTrading212(config Trading212Config) (*Trading212Client, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultTrading212BaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Trading 212 base URL must be an absolute HTTP URL without credentials, query, or fragment")
	}
	apiKey := strings.TrimSpace(config.APIKey)
	apiSecret := strings.TrimSpace(config.APISecret)
	if apiKey == "" || apiSecret == "" {
		return nil, errors.New("Trading 212 API key and secret are required")
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
		return nil, errors.New("Trading 212 operation timeout must be positive")
	}
	return &Trading212Client{
		baseURL: parsed, apiKey: apiKey, apiSecret: apiSecret, httpClient: &httpClientCopy,
		now: now, operationTimeout: timeout,
	}, nil
}

func (c *Trading212Client) CurrentQuote(
	ctx context.Context, instrument EquityInstrument, currency string,
) (Quote, error) {
	instrument, currency, err := normalizeTrading212Request(instrument, currency)
	if err != nil {
		return Quote{}, err
	}
	positions, err := c.openPositions(ctx)
	if err != nil {
		return Quote{}, err
	}
	position, ok := findTrading212Position(positions, instrument)
	if !ok {
		return Quote{}, fmt.Errorf("%w for %s", ErrQuoteUnavailable, instrument.Symbol)
	}
	price, err := trading212PositionPrice(position, currency)
	if err != nil {
		return Quote{}, err
	}
	return Quote{
		Symbol: instrument.Symbol, Currency: currency, Price: price,
		Provider: ProviderTrading212, AsOf: c.now().UTC(),
	}, nil
}

func (c *Trading212Client) QuoteAt(
	ctx context.Context, instrument EquityInstrument, currency string, at time.Time,
) (Quote, error) {
	instrument, currency, err := normalizeTrading212Request(instrument, currency)
	if err != nil {
		return Quote{}, err
	}
	now := c.now().UTC()
	if at.IsZero() || at.After(now) {
		return Quote{}, errors.New("equity quote time must be present and not be in the future")
	}
	ticker, err := c.resolveTicker(ctx, instrument)
	if err != nil {
		return Quote{}, err
	}
	var response struct {
		Items []trading212HistoricalOrder `json:"items"`
	}
	query := url.Values{"ticker": {ticker}, "limit": {"50"}}
	if err := c.get(ctx, "/equity/history/orders", query, &response); err != nil {
		return Quote{}, err
	}
	order, filledAt, ok := closestTrading212Fill(response.Items, ticker, at.UTC())
	if !ok || absoluteDuration(filledAt.Sub(at.UTC())) > trading212MaxOrderAge {
		return Quote{}, fmt.Errorf("%w near %s for %s", ErrQuoteUnavailable, at.UTC().Format(time.RFC3339), instrument.Symbol)
	}
	price, err := trading212FillPrice(order, currency)
	if err != nil {
		return Quote{}, err
	}
	return Quote{
		Symbol: instrument.Symbol, Currency: currency, Price: price,
		Provider: ProviderTrading212, AsOf: filledAt,
	}, nil
}

func (c *Trading212Client) openPositions(ctx context.Context) ([]trading212Position, error) {
	c.cacheMu.Lock()
	if c.now().Before(c.positionsExpiresAt) {
		positions := append([]trading212Position(nil), c.positions...)
		c.cacheMu.Unlock()
		return positions, nil
	}
	c.cacheMu.Unlock()
	var positions []trading212Position
	if err := c.get(ctx, "/equity/positions", nil, &positions); err != nil {
		return nil, err
	}
	c.cacheMu.Lock()
	c.positions = append([]trading212Position(nil), positions...)
	c.positionsExpiresAt = c.now().Add(trading212PositionTTL)
	c.cacheMu.Unlock()
	return positions, nil
}

func (c *Trading212Client) availableInstruments(ctx context.Context) ([]trading212Instrument, error) {
	c.cacheMu.Lock()
	if c.now().Before(c.instrumentsExpiresAt) {
		instruments := append([]trading212Instrument(nil), c.instruments...)
		c.cacheMu.Unlock()
		return instruments, nil
	}
	c.cacheMu.Unlock()
	var instruments []trading212Instrument
	if err := c.get(ctx, "/equity/metadata/instruments", nil, &instruments); err != nil {
		return nil, err
	}
	c.cacheMu.Lock()
	c.instruments = append([]trading212Instrument(nil), instruments...)
	c.instrumentsExpiresAt = c.now().Add(trading212InstrumentTTL)
	c.cacheMu.Unlock()
	return instruments, nil
}

func (c *Trading212Client) resolveTicker(ctx context.Context, requested EquityInstrument) (string, error) {
	instruments, err := c.availableInstruments(ctx)
	if err != nil {
		return "", err
	}
	candidates := make([]trading212Instrument, 0, 2)
	for _, instrument := range instruments {
		if trading212TickerMatches(instrument.Ticker, requested.Symbol) && instrument.currency() == requested.MarketCurrency {
			candidates = append(candidates, instrument)
		}
	}
	if len(candidates) > 1 {
		filtered := candidates[:0]
		for _, candidate := range candidates {
			if trading212ExchangeMatches(candidate.Ticker, requested.Exchange) {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("%w: Trading 212 instrument mapping for %s/%s is not unique", ErrUnsupportedPair, requested.Symbol, requested.Exchange)
	}
	return candidates[0].Ticker, nil
}

func findTrading212Position(positions []trading212Position, requested EquityInstrument) (trading212Position, bool) {
	candidates := make([]trading212Position, 0, 2)
	for _, position := range positions {
		if trading212TickerMatches(position.Instrument.Ticker, requested.Symbol) &&
			position.Instrument.currency() == requested.MarketCurrency {
			candidates = append(candidates, position)
		}
	}
	if len(candidates) > 1 {
		filtered := candidates[:0]
		for _, candidate := range candidates {
			if trading212ExchangeMatches(candidate.Instrument.Ticker, requested.Exchange) {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	if len(candidates) != 1 {
		return trading212Position{}, false
	}
	return candidates[0], true
}

func trading212PositionPrice(position trading212Position, currency string) (string, error) {
	if position.Instrument.currency() == currency {
		price, ok := new(big.Rat).SetString(position.CurrentPrice.String())
		if ok && price.Sign() > 0 {
			return formatTrading212Decimal(price), nil
		}
	}
	quantity, quantityOK := new(big.Rat).SetString(position.Quantity.String())
	currentValue, valueOK := new(big.Rat).SetString(position.WalletImpact.CurrentValue.String())
	if strings.EqualFold(position.WalletImpact.Currency, currency) && quantityOK && valueOK && quantity.Sign() != 0 {
		return formatTrading212Decimal(new(big.Rat).Quo(absRat(currentValue), absRat(quantity))), nil
	}
	return "", errors.New("Trading 212 position does not contain a usable price in the requested currency")
}

func trading212FillPrice(order trading212HistoricalOrder, currency string) (string, error) {
	if order.Order.Instrument.currency() == currency {
		price, ok := new(big.Rat).SetString(order.Fill.Price.String())
		if ok && price.Sign() > 0 {
			return formatTrading212Decimal(price), nil
		}
	}
	quantity, quantityOK := new(big.Rat).SetString(order.Fill.Quantity.String())
	netValue, valueOK := new(big.Rat).SetString(order.Fill.WalletImpact.NetValue.String())
	if strings.EqualFold(order.Fill.WalletImpact.Currency, currency) && quantityOK && valueOK && quantity.Sign() != 0 {
		return formatTrading212Decimal(new(big.Rat).Quo(absRat(netValue), absRat(quantity))), nil
	}
	return "", errors.New("Trading 212 order does not contain a usable fill price in the requested currency")
}

func closestTrading212Fill(items []trading212HistoricalOrder, ticker string, at time.Time) (trading212HistoricalOrder, time.Time, bool) {
	type candidate struct {
		order trading212HistoricalOrder
		at    time.Time
	}
	candidates := make([]candidate, 0, len(items))
	for _, item := range items {
		itemTicker := item.Order.Ticker
		if itemTicker == "" {
			itemTicker = item.Order.Instrument.Ticker
		}
		filledAt, err := time.Parse(time.RFC3339Nano, item.Fill.FilledAt)
		if err == nil && strings.EqualFold(itemTicker, ticker) {
			candidates = append(candidates, candidate{order: item, at: filledAt.UTC()})
		}
	}
	if len(candidates) == 0 {
		return trading212HistoricalOrder{}, time.Time{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return absoluteDuration(candidates[i].at.Sub(at)) < absoluteDuration(candidates[j].at.Sub(at))
	})
	return candidates[0].order, candidates[0].at, true
}

func normalizeTrading212Request(instrument EquityInstrument, currency string) (EquityInstrument, string, error) {
	instrument.Symbol = strings.ToUpper(strings.TrimSpace(instrument.Symbol))
	instrument.Exchange = strings.ToUpper(strings.TrimSpace(instrument.Exchange))
	instrument.MarketCurrency = strings.ToUpper(strings.TrimSpace(instrument.MarketCurrency))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if instrument.Symbol == "" || instrument.Exchange == "" || len(instrument.MarketCurrency) != 3 || len(currency) != 3 {
		return EquityInstrument{}, "", errors.New("equity symbol, exchange, market currency, and quote currency are required")
	}
	return instrument, currency, nil
}

func trading212TickerMatches(ticker, symbol string) bool {
	base := strings.Split(strings.ToUpper(strings.TrimSpace(ticker)), "_")[0]
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if base == symbol {
		return true
	}
	return strings.HasSuffix(base, "D") && strings.TrimSuffix(base, "D") == symbol
}

func trading212ExchangeMatches(ticker, exchange string) bool {
	ticker = strings.ToUpper(ticker)
	switch strings.ToUpper(exchange) {
	case "XETRA":
		return strings.Contains(ticker, "_XETR_") || strings.Contains(ticker, "_DE_")
	case "NASDAQ", "NYSE":
		return strings.Contains(ticker, "_US_")
	default:
		return true
	}
}

func formatTrading212Decimal(value *big.Rat) string {
	formatted := value.FloatString(12)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" || formatted == "-0" {
		return "0"
	}
	return formatted
}

func absRat(value *big.Rat) *big.Rat {
	result := new(big.Rat).Set(value)
	if result.Sign() < 0 {
		result.Neg(result)
	}
	return result
}

func (c *Trading212Client) get(ctx context.Context, path string, query url.Values, target any) error {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	requestURL.RawQuery = query.Encode()
	requestContext := ctx
	cancel := func() {}
	if c.operationTimeout > 0 {
		requestContext, cancel = context.WithTimeout(ctx, c.operationTimeout)
	}
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return errors.New("create Trading 212 request")
	}
	request.SetBasicAuth(c.apiKey, c.apiSecret)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("Trading 212 request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, trading212MaxResponse+1))
	if err != nil {
		return fmt.Errorf("read Trading 212 response: %w", err)
	}
	if len(body) > trading212MaxResponse {
		return errors.New("Trading 212 response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &trading212HTTPError{status: response.StatusCode, message: c.safeErrorMessage(body)}
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode Trading 212 response: %w", err)
	}
	return nil
}

func (c *Trading212Client) safeErrorMessage(body []byte) string {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = "request failed"
	}
	message = strings.ReplaceAll(message, c.apiKey, "[redacted]")
	message = strings.ReplaceAll(message, c.apiSecret, "[redacted]")
	if len(message) > trading212MaximumError {
		message = message[:trading212MaximumError]
	}
	return message
}
