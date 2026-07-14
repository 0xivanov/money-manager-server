package push

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	googleOAuthTokenURL = "https://oauth2.googleapis.com/token"
	fcmAPIOrigin        = "https://fcm.googleapis.com"
	fcmMessagingScope   = "https://www.googleapis.com/auth/firebase.messaging"
)

type FCMConfig struct {
	ProjectID   string
	ClientEmail string
	PrivateKey  *rsa.PrivateKey
	HTTPClient  *http.Client
	Now         func() time.Time
	TokenURL    string
	APIOrigin   string
}

type FCMClient struct {
	projectID   string
	clientEmail string
	privateKey  *rsa.PrivateKey
	httpClient  *http.Client
	now         func() time.Time
	tokenURL    string
	apiOrigin   string

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func NewFCMClient(config FCMConfig) (*FCMClient, error) {
	if strings.TrimSpace(config.ProjectID) == "" || strings.TrimSpace(config.ClientEmail) == "" || config.PrivateKey == nil {
		return nil, errors.New("complete FCM service account credentials are required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	tokenURL := strings.TrimSpace(config.TokenURL)
	if tokenURL == "" {
		tokenURL = googleOAuthTokenURL
	}
	apiOrigin := strings.TrimRight(config.APIOrigin, "/")
	if apiOrigin == "" {
		apiOrigin = fcmAPIOrigin
	}
	return &FCMClient{
		projectID: strings.TrimSpace(config.ProjectID), clientEmail: strings.TrimSpace(config.ClientEmail),
		privateKey: config.PrivateKey, httpClient: httpClient, now: now,
		tokenURL: tokenURL, apiOrigin: apiOrigin,
	}, nil
}

func (c *FCMClient) Send(ctx context.Context, notification Notification) (Result, error) {
	status, errorCode, err := c.send(ctx, notification, false)
	if err == nil {
		return Result{}, nil
	}
	if status == http.StatusUnauthorized {
		c.invalidateAccessToken()
		status, errorCode, err = c.send(ctx, notification, true)
		if err == nil {
			return Result{}, nil
		}
	}
	result := Result{}
	switch status {
	case http.StatusBadRequest:
		result.Permanent = true
	case http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		result.Permanent = false
	case http.StatusForbidden, http.StatusNotFound:
		result.Permanent = true
	default:
		result.Permanent = status >= 400 && status < 500
	}
	if errorCode == "UNREGISTERED" || errorCode == "SENDER_ID_MISMATCH" {
		result.Permanent = true
		result.Deactivate = true
	}
	return result, err
}

func (c *FCMClient) send(
	ctx context.Context,
	notification Notification,
	forceToken bool,
) (int, string, error) {
	accessToken, err := c.getAccessToken(ctx, forceToken)
	if err != nil {
		return 0, "", fmt.Errorf("authorize FCM request: %w", err)
	}
	dataPayload := "{}"
	if len(notification.Data) > 0 && json.Valid(notification.Data) {
		dataPayload = string(notification.Data)
	}
	payload := struct {
		Message struct {
			Token        string `json:"token"`
			Notification struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"notification"`
			Data map[string]string `json:"data"`
		} `json:"message"`
	}{}
	payload.Message.Token = notification.DeviceToken
	payload.Message.Notification.Title = notification.Title
	payload.Message.Notification.Body = notification.Body
	payload.Message.Data = map[string]string{
		"event_type": notification.EventType,
		"payload":    dataPayload,
		"title":      notification.Title,
		"body":       notification.Body,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, "", fmt.Errorf("encode FCM payload: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.apiOrigin+"/v1/projects/"+url.PathEscape(c.projectID)+"/messages:send",
		bytes.NewReader(encoded),
	)
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("authorization", "Bearer "+accessToken)
	request.Header.Set("content-type", "application/json; charset=utf-8")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("send FCM request: %w", err)
	}
	defer response.Body.Close()
	contents, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.StatusCode, "", nil
	}
	var failure struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(contents, &failure)
	errorCode := failure.Error.Status
	for _, detail := range failure.Error.Details {
		if detail.ErrorCode != "" {
			errorCode = detail.ErrorCode
			break
		}
	}
	message := strings.TrimSpace(failure.Error.Message)
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return response.StatusCode, errorCode, fmt.Errorf("FCM rejected notification: %s", message)
}

func (c *FCMClient) getAccessToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	if !force && c.accessToken != "" && now.Before(c.tokenExpiry.Add(-5*time.Minute)) {
		return c.accessToken, nil
	}
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   c.clientEmail,
		"scope": fcmMessagingScope,
		"aud":   c.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	signed, err := assertion.SignedString(c.privateKey)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signed},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request OAuth token: %w", err)
	}
	defer response.Body.Close()
	contents, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OAuth token endpoint returned %d", response.StatusCode)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   any    `json:"expires_in"`
	}
	if err := json.Unmarshal(contents, &tokenResponse); err != nil || strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return "", errors.New("OAuth token endpoint returned an invalid response")
	}
	expiresIn := int64(3600)
	switch value := tokenResponse.ExpiresIn.(type) {
	case float64:
		expiresIn = int64(value)
	case string:
		if parsed, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil {
			expiresIn = parsed
		}
	}
	if expiresIn < 60 {
		return "", errors.New("OAuth token endpoint returned an invalid expiry")
	}
	c.accessToken = strings.TrimSpace(tokenResponse.AccessToken)
	c.tokenExpiry = now.Add(time.Duration(expiresIn) * time.Second)
	return c.accessToken, nil
}

func (c *FCMClient) invalidateAccessToken() {
	c.mu.Lock()
	c.accessToken = ""
	c.tokenExpiry = time.Time{}
	c.mu.Unlock()
}
