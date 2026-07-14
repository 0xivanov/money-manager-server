package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestFCMClientAuthorizesAndSendsNotification(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			assertion := request.Form.Get("assertion")
			token, parseErr := jwt.Parse(assertion, func(_ *jwt.Token) (any, error) {
				return &privateKey.PublicKey, nil
			}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(server.URL+"/token"), jwt.WithTimeFunc(func() time.Time { return now }))
			if parseErr != nil || !token.Valid {
				t.Fatalf("OAuth assertion = %#v, %v", token, parseErr)
			}
			response.Header().Set("content-type", "application/json")
			_, _ = response.Write([]byte(`{"access_token":"access-token","expires_in":3600}`))
		case "/v1/projects/money-manager/messages:send":
			if request.Header.Get("authorization") != "Bearer access-token" {
				t.Fatalf("authorization = %q", request.Header.Get("authorization"))
			}
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			response.Header().Set("content-type", "application/json")
			_, _ = response.Write([]byte(`{"name":"projects/money-manager/messages/1"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := NewFCMClient(FCMConfig{
		ProjectID: "money-manager", ClientEmail: "push@example.com", PrivateKey: privateKey,
		HTTPClient: server.Client(), Now: func() time.Time { return now },
		TokenURL: server.URL + "/token", APIOrigin: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Send(context.Background(), Notification{
		DeviceToken: "registration-token", Title: "Budget alert", Body: "Food is at 80%",
		EventType: "budget_alert", Data: json.RawMessage(`{"budget_id":4}`),
	})
	if err != nil || result != (Result{}) {
		t.Fatalf("Send() = %#v, %v", result, err)
	}
}

func TestFCMClientDeactivatesUnregisteredToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/token") {
			_, _ = response.Write([]byte(`{"access_token":"access-token","expires_in":3600}`))
			return
		}
		response.WriteHeader(http.StatusNotFound)
		_, _ = response.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","message":"gone","details":[{"errorCode":"UNREGISTERED"}]}}`))
	}))
	defer server.Close()
	client, err := NewFCMClient(FCMConfig{
		ProjectID: "money-manager", ClientEmail: "push@example.com", PrivateKey: privateKey,
		HTTPClient: server.Client(), TokenURL: server.URL + "/token", APIOrigin: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Send(context.Background(), Notification{
		DeviceToken: "old-token", Title: "Alert", Body: "Body", EventType: "bank_spending",
	})
	if err == nil || !result.Permanent || !result.Deactivate {
		t.Fatalf("Send() = %#v, %v", result, err)
	}
}
