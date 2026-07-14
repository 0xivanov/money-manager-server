package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
)

const (
	productionAPNSOrigin = "https://api.push.apple.com"
	sandboxAPNSOrigin    = "https://api.sandbox.push.apple.com"
	maximumAPNSPayload   = 4096
)

type Notification struct {
	DeviceToken string
	AppID       string
	Environment string
	Title       string
	Body        string
	EventType   string
	Data        json.RawMessage
}

type Result struct {
	Permanent  bool
	Deactivate bool
}

type APNSConfig struct {
	KeyID            string
	TeamID           string
	BundleID         string
	PrivateKey       *ecdsa.PrivateKey
	HTTPClient       *http.Client
	Now              func() time.Time
	ProductionOrigin string
	SandboxOrigin    string
}

type APNSClient struct {
	keyID            string
	teamID           string
	bundleID         string
	privateKey       *ecdsa.PrivateKey
	httpClient       *http.Client
	now              func() time.Time
	productionOrigin string
	sandboxOrigin    string

	mu          sync.Mutex
	providerJWT string
	jwtIssuedAt time.Time
}

func NewAPNSClient(config APNSConfig) (*APNSClient, error) {
	if strings.TrimSpace(config.KeyID) == "" || strings.TrimSpace(config.TeamID) == "" ||
		strings.TrimSpace(config.BundleID) == "" || config.PrivateKey == nil {
		return nil, errors.New("complete APNs credentials are required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	productionOrigin := strings.TrimRight(config.ProductionOrigin, "/")
	if productionOrigin == "" {
		productionOrigin = productionAPNSOrigin
	}
	sandboxOrigin := strings.TrimRight(config.SandboxOrigin, "/")
	if sandboxOrigin == "" {
		sandboxOrigin = sandboxAPNSOrigin
	}
	return &APNSClient{
		keyID: strings.TrimSpace(config.KeyID), teamID: strings.TrimSpace(config.TeamID),
		bundleID: strings.TrimSpace(config.BundleID), privateKey: config.PrivateKey,
		httpClient: client, now: now, productionOrigin: productionOrigin, sandboxOrigin: sandboxOrigin,
	}, nil
}

func (c *APNSClient) Send(ctx context.Context, notification Notification) (Result, error) {
	if notification.AppID != c.bundleID {
		return Result{Permanent: true, Deactivate: true}, fmt.Errorf(
			"APNs topic mismatch: device uses %q", notification.AppID,
		)
	}
	payload, err := apnsPayload(notification)
	if err != nil {
		return Result{Permanent: true}, err
	}
	status, reason, err := c.send(ctx, notification, payload, false)
	if err == nil {
		return Result{}, nil
	}
	if status == http.StatusForbidden && reason == "ExpiredProviderToken" {
		c.invalidateProviderToken()
		status, reason, err = c.send(ctx, notification, payload, true)
		if err == nil {
			return Result{}, nil
		}
	}
	result := Result{}
	switch status {
	case http.StatusBadRequest:
		result.Permanent = true
		result.Deactivate = reason == "BadDeviceToken" || reason == "DeviceTokenNotForTopic"
	case http.StatusGone:
		result.Permanent = true
		result.Deactivate = true
	case http.StatusRequestEntityTooLarge:
		result.Permanent = true
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable:
		result.Permanent = false
	case http.StatusForbidden:
		result.Permanent = reason != "ExpiredProviderToken"
	default:
		result.Permanent = status >= 400 && status < 500
	}
	return result, err
}

func (c *APNSClient) send(
	ctx context.Context,
	notification Notification,
	payload []byte,
	forceToken bool,
) (int, string, error) {
	token, err := c.providerToken(forceToken)
	if err != nil {
		return 0, "", fmt.Errorf("create APNs provider token: %w", err)
	}
	origin := c.productionOrigin
	if notification.Environment == "sandbox" {
		origin = c.sandboxOrigin
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		origin+"/3/device/"+url.PathEscape(notification.DeviceToken),
		bytes.NewReader(payload),
	)
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("authorization", "bearer "+token)
	request.Header.Set("apns-topic", c.bundleID)
	request.Header.Set("apns-push-type", "alert")
	request.Header.Set("apns-priority", "10")
	request.Header.Set("content-type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("send APNs request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode, "", nil
	}
	var failure struct {
		Reason string `json:"reason"`
	}
	contents, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = json.Unmarshal(contents, &failure)
	reason := strings.TrimSpace(failure.Reason)
	if reason == "" {
		reason = http.StatusText(response.StatusCode)
	}
	return response.StatusCode, reason, fmt.Errorf("APNs rejected notification: %s", reason)
}

func (c *APNSClient) providerToken(force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	if !force && c.providerJWT != "" && now.Sub(c.jwtIssuedAt) < 50*time.Minute {
		return c.providerJWT, nil
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": c.teamID,
		"iat": now.Unix(),
	})
	token.Header["kid"] = c.keyID
	signed, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", err
	}
	c.providerJWT = signed
	c.jwtIssuedAt = now
	return signed, nil
}

func (c *APNSClient) invalidateProviderToken() {
	c.mu.Lock()
	c.providerJWT = ""
	c.jwtIssuedAt = time.Time{}
	c.mu.Unlock()
}

func apnsPayload(notification Notification) ([]byte, error) {
	data := notification.Data
	if len(data) == 0 || !json.Valid(data) {
		data = json.RawMessage(`{}`)
	}
	payload := struct {
		APS struct {
			Alert struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"alert"`
			Sound string `json:"sound"`
		} `json:"aps"`
		EventType string          `json:"event_type"`
		Data      json.RawMessage `json:"data"`
	}{EventType: notification.EventType, Data: data}
	payload.APS.Alert.Title = notification.Title
	payload.APS.Alert.Body = notification.Body
	payload.APS.Sound = "default"
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode APNs payload: %w", err)
	}
	if len(encoded) > maximumAPNSPayload {
		return nil, fmt.Errorf("APNs payload is %d bytes, maximum is %d", len(encoded), maximumAPNSPayload)
	}
	if !utf8.Valid(encoded) {
		return nil, errors.New("APNs payload is not valid UTF-8")
	}
	return encoded, nil
}
