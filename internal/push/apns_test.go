package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAPNSClientSignsAndSendsAlert(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/3/device/device-token" || request.Header.Get("apns-topic") != "org.moneymanager.ios" {
			t.Fatalf("request = %s, topic %q", request.URL.Path, request.Header.Get("apns-topic"))
		}
		rawToken := strings.TrimPrefix(request.Header.Get("authorization"), "bearer ")
		token, parseErr := jwt.Parse(rawToken, func(token *jwt.Token) (any, error) {
			return &privateKey.PublicKey, nil
		}, jwt.WithValidMethods([]string{"ES256"}))
		if parseErr != nil || !token.Valid || token.Header["kid"] != "KEYID12345" {
			t.Fatalf("provider token = %#v, %v", token, parseErr)
		}
		var payload map[string]any
		if decodeErr := json.NewDecoder(request.Body).Decode(&payload); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if payload["event_type"] != "bank_spending" {
			t.Fatalf("payload = %#v", payload)
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewAPNSClient(APNSConfig{
		KeyID: "KEYID12345", TeamID: "TEAMID1234", BundleID: "org.moneymanager.ios",
		PrivateKey: privateKey, HTTPClient: server.Client(), Now: func() time.Time { return now },
		ProductionOrigin: server.URL, SandboxOrigin: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Send(context.Background(), Notification{
		DeviceToken: "device-token", AppID: "org.moneymanager.ios", Environment: "production",
		Title: "New bank spending", Body: "Market · 12.50 EUR", EventType: "bank_spending",
		Data: json.RawMessage(`{"transaction_id":42}`),
	})
	if err != nil || result != (Result{}) {
		t.Fatalf("Send() = %#v, %v", result, err)
	}
}

func TestAPNSClientClassifiesInvalidDeviceAndRetriesExpiredToken(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			response.WriteHeader(http.StatusForbidden)
			_, _ = response.Write([]byte(`{"reason":"ExpiredProviderToken"}`))
			return
		}
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"reason":"BadDeviceToken"}`))
	}))
	defer server.Close()
	client, err := NewAPNSClient(APNSConfig{
		KeyID: "KEYID12345", TeamID: "TEAMID1234", BundleID: "org.moneymanager.ios",
		PrivateKey: privateKey, HTTPClient: server.Client(), ProductionOrigin: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Send(context.Background(), Notification{
		DeviceToken: "bad-token", AppID: "org.moneymanager.ios", Environment: "production",
		Title: "Alert", Body: "Body", EventType: "budget_alert",
	})
	if err == nil || !result.Permanent || !result.Deactivate || requests != 2 {
		t.Fatalf("Send() = %#v, %v, requests=%d", result, err, requests)
	}
}
