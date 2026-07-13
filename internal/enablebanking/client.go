package enablebanking

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultBaseURL      = "https://api.enablebanking.com"
	maximumResponseSize = 8 << 20
)

type Config struct {
	ApplicationID string
	PrivateKey    *rsa.PrivateKey
	HTTPClient    *http.Client
	BaseURL       string
	Now           func() time.Time
}

type Client struct {
	applicationID string
	privateKey    *rsa.PrivateKey
	httpClient    *http.Client
	baseURL       *url.URL
	now           func() time.Time
}

type ProviderError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *ProviderError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("enable banking returned HTTP %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("enable banking returned HTTP %d", e.StatusCode)
}

type Institution struct {
	Name                   string       `json:"name"`
	Country                string       `json:"country"`
	Logo                   string       `json:"logo"`
	PSUTypes               []string     `json:"psu_types"`
	AuthMethods            []AuthMethod `json:"auth_methods"`
	MaximumConsentValidity int64        `json:"maximum_consent_validity"`
	Beta                   bool         `json:"beta"`
	BIC                    string       `json:"bic,omitempty"`
	RequiredPSUHeaders     []string     `json:"required_psu_headers,omitempty"`
}

type ASPSP struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

type AuthMethod struct {
	Name         string `json:"name"`
	Title        string `json:"title,omitempty"`
	PSUType      string `json:"psu_type"`
	Approach     string `json:"approach"`
	HiddenMethod bool   `json:"hidden_method"`
}

type StartAuthorizationRequest struct {
	Access      Access `json:"access"`
	Institution ASPSP  `json:"aspsp"`
	State       string `json:"state"`
	RedirectURL string `json:"redirect_url"`
	PSUType     string `json:"psu_type"`
	Language    string `json:"language,omitempty"`
}

type Access struct {
	Balances     bool   `json:"balances,omitempty"`
	Transactions bool   `json:"transactions,omitempty"`
	ValidUntil   string `json:"valid_until"`
}

type StartAuthorizationResponse struct {
	URL             string `json:"url"`
	AuthorizationID string `json:"authorization_id"`
}

type AuthorizeSessionResponse struct {
	SessionID string    `json:"session_id"`
	Accounts  []Account `json:"accounts"`
	ASPSP     ASPSP     `json:"aspsp"`
	PSUType   string    `json:"psu_type"`
	Access    Access    `json:"access"`
}

type Account struct {
	UID                string                `json:"uid,omitempty"`
	IdentificationHash string                `json:"identification_hash"`
	AccountID          AccountIdentification `json:"account_id,omitempty"`
	Name               string                `json:"name,omitempty"`
	Details            string                `json:"details,omitempty"`
	CashAccountType    string                `json:"cash_account_type"`
	Product            string                `json:"product,omitempty"`
	Currency           string                `json:"currency"`
	Raw                json.RawMessage       `json:"-"`
}

type AccountIdentification struct {
	IBAN  string                `json:"iban,omitempty"`
	Other GenericIdentification `json:"other,omitempty"`
}

type GenericIdentification struct {
	Identification string `json:"identification,omitempty"`
	SchemeName     string `json:"scheme_name,omitempty"`
}

func (a *Account) UnmarshalJSON(data []byte) error {
	type accountAlias Account
	var decoded accountAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*a = Account(decoded)
	a.Raw = append(a.Raw[:0], data...)
	return nil
}

type Session struct {
	Status   string   `json:"status"`
	Accounts []string `json:"accounts"`
	ASPSP    ASPSP    `json:"aspsp"`
	PSUType  string   `json:"psu_type"`
	Access   Access   `json:"access"`
}

type PSUHeaders struct {
	IPAddress      string
	UserAgent      string
	Referer        string
	Accept         string
	AcceptCharset  string
	AcceptEncoding string
	AcceptLanguage string
}

func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.ApplicationID) == "" {
		return nil, errors.New("enable banking application ID is required")
	}
	if config.PrivateKey == nil {
		return nil, errors.New("enable banking private key is required")
	}
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, errors.New("enable banking base URL must be absolute")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
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
	return &Client{
		applicationID: strings.TrimSpace(config.ApplicationID),
		privateKey:    config.PrivateKey,
		httpClient:    &httpClientCopy,
		baseURL:       parsedBaseURL,
		now:           now,
	}, nil
}

func (c *Client) ListInstitutions(ctx context.Context, country, psuType string) ([]Institution, error) {
	query := url.Values{}
	if country != "" {
		query.Set("country", country)
	}
	if psuType != "" {
		query.Set("psu_type", psuType)
	}
	var response struct {
		Institutions []Institution `json:"aspsps"`
	}
	if err := c.request(ctx, http.MethodGet, "/aspsps", query, nil, PSUHeaders{}, &response); err != nil {
		return nil, err
	}
	if response.Institutions == nil {
		response.Institutions = []Institution{}
	}
	return response.Institutions, nil
}

func (c *Client) StartAuthorization(ctx context.Context, request StartAuthorizationRequest) (StartAuthorizationResponse, error) {
	var response StartAuthorizationResponse
	err := c.request(ctx, http.MethodPost, "/auth", nil, request, PSUHeaders{}, &response)
	return response, err
}

func (c *Client) AuthorizeSession(ctx context.Context, code string) (AuthorizeSessionResponse, error) {
	var response AuthorizeSessionResponse
	err := c.request(ctx, http.MethodPost, "/sessions", nil, map[string]string{"code": code}, PSUHeaders{}, &response)
	return response, err
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (Session, error) {
	var response Session
	err := c.request(ctx, http.MethodGet, "/sessions/"+url.PathEscape(sessionID), nil, nil, PSUHeaders{}, &response)
	return response, err
}

func (c *Client) DeleteSession(ctx context.Context, sessionID string, headers PSUHeaders) error {
	var response json.RawMessage
	return c.request(ctx, http.MethodDelete, "/sessions/"+url.PathEscape(sessionID), nil, nil, headers, &response)
}

func (c *Client) AccountDetails(ctx context.Context, accountID string, headers PSUHeaders) (json.RawMessage, error) {
	return c.accountRequest(ctx, accountID, "details", nil, headers)
}

func (c *Client) AccountBalances(ctx context.Context, accountID string, headers PSUHeaders) (json.RawMessage, error) {
	return c.accountRequest(ctx, accountID, "balances", nil, headers)
}

func (c *Client) AccountTransactions(ctx context.Context, accountID string, query url.Values, headers PSUHeaders) (json.RawMessage, error) {
	return c.accountRequest(ctx, accountID, "transactions", query, headers)
}

func (c *Client) accountRequest(ctx context.Context, accountID, resource string, query url.Values, headers PSUHeaders) (json.RawMessage, error) {
	var response json.RawMessage
	path := "/accounts/" + url.PathEscape(accountID) + "/" + resource
	err := c.request(ctx, http.MethodGet, path, query, nil, headers, &response)
	return response, err
}

func (c *Client) request(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	body any,
	psuHeaders PSUHeaders,
	destination any,
) error {
	var encodedBody io.Reader
	if body != nil {
		contents, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode enable banking request: %w", err)
		}
		encodedBody = bytes.NewReader(contents)
	}
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + path
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), encodedBody)
	if err != nil {
		return fmt.Errorf("create enable banking request: %w", err)
	}
	token, err := c.signToken()
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	setPSUHeaders(request.Header, psuHeaders)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("execute enable banking request: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseSize+1))
	if err != nil {
		return fmt.Errorf("read enable banking response: %w", err)
	}
	if len(contents) > maximumResponseSize {
		return errors.New("enable banking response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return parseProviderError(response.StatusCode, contents)
	}
	if destination == nil || len(bytes.TrimSpace(contents)) == 0 {
		return nil
	}
	if raw, ok := destination.(*json.RawMessage); ok {
		if !json.Valid(contents) {
			return errors.New("enable banking returned invalid JSON")
		}
		*raw = append((*raw)[:0], contents...)
		return nil
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		return fmt.Errorf("decode enable banking response: %w", err)
	}
	return nil
}

func (c *Client) signToken() (string, error) {
	now := c.now().UTC()
	claims := jwt.MapClaims{
		"iss": "enablebanking.com",
		"aud": "api.enablebanking.com",
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = c.applicationID
	signed, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign enable banking JWT: %w", err)
	}
	return signed, nil
}

func setPSUHeaders(headers http.Header, values PSUHeaders) {
	for name, value := range map[string]string{
		"Psu-Ip-Address":      values.IPAddress,
		"Psu-User-Agent":      values.UserAgent,
		"Psu-Referer":         values.Referer,
		"Psu-Accept":          values.Accept,
		"Psu-Accept-Charset":  values.AcceptCharset,
		"Psu-Accept-Encoding": values.AcceptEncoding,
		"Psu-Accept-Language": values.AcceptLanguage,
	} {
		if strings.TrimSpace(value) != "" {
			headers.Set(name, value)
		}
	}
}

func parseProviderError(statusCode int, contents []byte) error {
	var payload struct {
		Code             string `json:"code"`
		Error            string `json:"error"`
		Message          string `json:"message"`
		Description      string `json:"description"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(contents, &payload)
	code := firstNonEmpty(payload.Code, payload.Error)
	message := firstNonEmpty(payload.Message, payload.Description, payload.ErrorDescription)
	return &ProviderError{StatusCode: statusCode, Code: code, Message: message}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
