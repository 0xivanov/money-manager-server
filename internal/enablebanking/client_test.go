package enablebanking

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestClientSignsRequiredJWTAndSanitizesInstitutions(t *testing.T) {
	privateKey := testPrivateKey(t)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/aspsps" || request.URL.Query().Get("country") != "BG" || request.URL.Query().Get("psu_type") != "personal" {
			t.Fatalf("unexpected request URL: %s", request.URL.String())
		}
		rawToken := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		token, err := jwt.Parse(rawToken, func(token *jwt.Token) (any, error) {
			return &privateKey.PublicKey, nil
		}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithoutClaimsValidation())
		if err != nil || !token.Valid {
			t.Fatalf("JWT parse = %#v, %v", token, err)
		}
		claims := token.Claims.(jwt.MapClaims)
		if claims["iss"] != "enablebanking.com" || claims["aud"] != "api.enablebanking.com" {
			t.Fatalf("JWT claims = %#v", claims)
		}
		if token.Header["kid"] != "application-id" || token.Header["alg"] != "RS256" {
			t.Fatalf("JWT header = %#v", token.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"aspsps":[{"name":"Revolut","country":"BG","logo":"https://example.test/logo","psu_types":["personal"],"auth_methods":[{"name":"redirect","title":"App","psu_type":"personal","approach":"REDIRECT","hidden_method":false,"credentials":[{"name":"password"}]}],"maximum_consent_validity":15552000,"beta":false,"required_psu_headers":["Psu-Ip-Address"],"sandbox":{"users":[{"username":"secret-user","password":"secret-password"}]}}]}`))
	}))
	defer server.Close()

	client, err := New(Config{
		ApplicationID: "application-id", PrivateKey: privateKey,
		BaseURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	institutions, err := client.ListInstitutions(context.Background(), "BG", "personal")
	if err != nil || len(institutions) != 1 || institutions[0].Name != "Revolut" {
		t.Fatalf("institutions = %#v, %v", institutions, err)
	}
	encoded, err := json.Marshal(institutions)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-user") || strings.Contains(string(encoded), "secret-password") || strings.Contains(string(encoded), "credentials") {
		t.Fatalf("sanitized response leaked sandbox metadata: %s", encoded)
	}
}

func TestClientUsesTypedAuthorizationBodyAndForwardsPSUContext(t *testing.T) {
	privateKey := testPrivateKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/auth":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			aspsp := body["aspsp"].(map[string]any)
			if len(aspsp) != 2 || aspsp["name"] != "Revolut" || aspsp["country"] != "BG" {
				t.Fatalf("authorization ASPSP body = %#v", aspsp)
			}
			if body["redirect_url"] != "https://money.example/api/open-banking/callback" || body["state"] != "state" {
				t.Fatalf("authorization body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"url":"https://auth.enablebanking.com/start","authorization_id":"authorization-id"}`))
		case "/accounts/provider-account/transactions":
			if request.URL.Query().Get("date_from") != "2026-07-01" || request.Header.Get("Psu-Ip-Address") != "198.51.100.7" || request.Header.Get("Psu-User-Agent") != "MoneyManager/1" {
				t.Fatalf("transactions request = %s, headers = %#v", request.URL.String(), request.Header)
			}
			_, _ = w.Write([]byte(`{"transactions":[],"continuation_key":null}`))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{ApplicationID: "application-id", PrivateKey: privateKey, BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := client.StartAuthorization(context.Background(), StartAuthorizationRequest{
		Access:      Access{Balances: true, Transactions: true, ValidUntil: "2026-10-01T00:00:00Z"},
		Institution: ASPSP{Name: "Revolut", Country: "BG"}, State: "state",
		RedirectURL: "https://money.example/api/open-banking/callback", PSUType: "personal",
	})
	if err != nil || authorization.AuthorizationID != "authorization-id" {
		t.Fatalf("authorization = %#v, %v", authorization, err)
	}
	data, err := client.AccountTransactions(context.Background(), "provider-account", url.Values{"date_from": {"2026-07-01"}}, PSUHeaders{
		IPAddress: "198.51.100.7", UserAgent: "MoneyManager/1",
	})
	if err != nil || !json.Valid(data) {
		t.Fatalf("transactions = %s, %v", data, err)
	}
}

func TestClientReturnsStructuredProviderError(t *testing.T) {
	privateKey := testPrivateKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"ASPSP_RATE_LIMIT_EXCEEDED","message":"slow down"}`))
	}))
	defer server.Close()
	client, err := New(Config{ApplicationID: "application-id", PrivateKey: privateKey, BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListInstitutions(context.Background(), "BG", "personal")
	providerErr, ok := err.(*ProviderError)
	if !ok || providerErr.StatusCode != http.StatusTooManyRequests || providerErr.Code != "ASPSP_RATE_LIMIT_EXCEEDED" {
		t.Fatalf("provider error = %#v", err)
	}
}

func TestLiveCredentials(t *testing.T) {
	applicationID := os.Getenv("TEST_ENABLE_BANKING_APPLICATION_ID")
	privateKeyPath := os.Getenv("TEST_ENABLE_BANKING_PRIVATE_KEY_PATH")
	if applicationID == "" || privateKeyPath == "" {
		t.Skip("live Enable Banking credentials are not configured")
	}
	contents, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(contents)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{ApplicationID: applicationID, PrivateKey: privateKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListInstitutions(context.Background(), "BG", "personal"); err != nil {
		t.Fatalf("live Enable Banking credential check failed: %v", err)
	}
}

func testPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}
