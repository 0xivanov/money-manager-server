package router

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

func TestHealthEndpointsSeparateLivenessAndReadiness(t *testing.T) {
	api := &fakeAPI{readyError: errors.New("database unavailable")}
	handler := testHandler(api, Options{})

	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if live.Code != http.StatusOK || live.Body.String() != "ok" {
		t.Fatalf("live response = %d %q", live.Code, live.Body.String())
	}
	for _, path := range []string{"/readyz", "/health"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "database") {
			t.Fatalf("%s response = %d %q", path, response.Code, response.Body.String())
		}
	}
}

func TestStrictJSONRejectsUnknownTrailingAndOversizedBodies(t *testing.T) {
	handler := testHandler(&fakeAPI{}, Options{RequestBodyLimit: 64})
	tests := []string{
		`{"email":"person@example.com","password":"password","extra":true}`,
		`{"email":"person@example.com","password":"password"}{}`,
		`{"email":"person@example.com","password":"` + strings.Repeat("x", 100) + `"}`,
	}
	for _, body := range tests {
		request := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %q status = %d, response = %s", body, response.Code, response.Body.String())
		}
		if response.Header().Get("X-Request-ID") == "" {
			t.Error("response has no request ID")
		}
	}
}

func TestInternalErrorsAreNotLeaked(t *testing.T) {
	api := &fakeAPI{registerError: apperrors.Internal(errors.New("users_password_hash secret detail"))}
	handler := testHandler(api, Options{})
	request := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(
		`{"email":"person@example.com","password":"password"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "password_hash") || !strings.Contains(response.Body.String(), "internal server error") {
		t.Fatalf("unsafe response body: %s", response.Body.String())
	}
}

func TestAuthenticationAndMeEndpoint(t *testing.T) {
	api := &fakeAPI{user: model.User{ID: 7, Email: "person@example.com"}}
	handler := testHandler(api, Options{})

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/me", nil))
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized response = %d, headers = %#v", unauthorized.Code, unauthorized.Header())
	}

	request := httptest.NewRequest(http.MethodGet, "/me", nil)
	request.Header.Set("Authorization", "bearer valid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "person@example.com") {
		t.Fatalf("me response = %d %s", response.Code, response.Body.String())
	}
}

func TestAuthRateLimitReturns429(t *testing.T) {
	handler := testHandler(&fakeAPI{}, Options{AuthRateLimit: 1, AuthRateWindow: time.Minute})
	for attempt := 1; attempt <= 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(
			`{"email":"person@example.com","password":"password"}`,
		))
		request.RemoteAddr = "192.0.2.5:12345"
		request.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(attempt))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if attempt == 1 && response.Code != http.StatusOK {
			t.Fatalf("first attempt status = %d: %s", response.Code, response.Body.String())
		}
		if attempt == 2 {
			if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
				t.Fatalf("limited response = %d, headers = %#v", response.Code, response.Header())
			}
		}
	}

	otherAccount := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(
		`{"email":"other@example.com","password":"password"}`,
	))
	otherAccount.RemoteAddr = "192.0.2.5:12345"
	otherAccount.Header.Set("Content-Type", "application/json")
	otherResponse := httptest.NewRecorder()
	handler.ServeHTTP(otherResponse, otherAccount)
	if otherResponse.Code != http.StatusOK {
		t.Fatalf("shared proxy limited a different account: status = %d, body = %s", otherResponse.Code, otherResponse.Body.String())
	}
}

func TestClientIPOnlyTrustsConfiguredProxyChain(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.42.0.9:12345"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 10.42.0.8")
	request.Header.Set("X-Real-IP", "203.0.113.99")

	if got := clientIP(request, nil, 0); got != "10.42.0.9" {
		t.Fatalf("default client IP = %q", got)
	}
	unrelated := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	if got := clientIP(request, unrelated, 1); got != "10.42.0.9" {
		t.Fatalf("untrusted peer client IP = %q", got)
	}
	trusted := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	if got := clientIP(request, trusted, 2); got != "198.51.100.7" {
		t.Fatalf("two-hop client IP = %q", got)
	}

	request.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.10")
	if got := clientIP(request, trusted, 2); got != "10.42.0.9" {
		t.Fatalf("untrusted intermediate proxy client IP = %q", got)
	}
}

func TestOpenBankingEndpointsRequireAuthentication(t *testing.T) {
	handler := testHandler(&fakeAPI{openBankingInstitutions: []model.OpenBankingInstitution{{Name: "Revolut", Country: "BG"}}}, Options{})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/open-banking/banks?country=BG", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized banks response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/open-banking/banks?country=BG", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Revolut") {
		t.Fatalf("banks response = %d %s", response.Code, response.Body.String())
	}

	create := httptest.NewRequest(http.MethodPost, "/api/open-banking/authorizations", strings.NewReader(
		`{"institution_name":"Revolut","country":"BG","psu_type":"personal"}`,
	))
	create.Header.Set("Authorization", "Bearer valid")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), "auth.enablebanking.com") {
		t.Fatalf("authorization response = %d %s", created.Code, created.Body.String())
	}
}

func TestOpenBankingCallbackIsPublicAndUsesSafeCompletionPageOrRedirect(t *testing.T) {
	pageHandler := testHandler(&fakeAPI{openBankingCallback: model.OpenBankingCallbackResult{
		Status: "connected", Message: "Bank account connected",
	}}, Options{})
	page := httptest.NewRecorder()
	pageHandler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/api/open-banking/callback?state=s&code=c", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(page.Body.String(), "Bank account connected") {
		t.Fatalf("callback page = %d, headers=%#v, body=%s", page.Code, page.Header(), page.Body.String())
	}

	redirectHandler := testHandler(&fakeAPI{openBankingCallback: model.OpenBankingCallbackResult{
		Status: "connected", RedirectURL: "moneymanager://open-banking?status=connected",
	}}, Options{})
	redirect := httptest.NewRecorder()
	redirectHandler.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/api/open-banking/callback?state=s&code=c", nil))
	if redirect.Code != http.StatusSeeOther || redirect.Header().Get("Location") != "moneymanager://open-banking?status=connected" {
		t.Fatalf("callback redirect = %d, location=%q", redirect.Code, redirect.Header().Get("Location"))
	}
}

func TestOpenBankingProviderJSONIsNotDoubleEncoded(t *testing.T) {
	handler := testHandler(&fakeAPI{}, Options{})
	request := httptest.NewRequest(http.MethodGet, "/api/open-banking/accounts/4/balances", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"balances":[]}` {
		t.Fatalf("balances response = %d %s", response.Code, response.Body.String())
	}
}

func testHandler(api API, options Options) http.Handler {
	options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return Build(api, options)
}

type fakeAPI struct {
	readyError              error
	registerError           error
	user                    model.User
	openBankingInstitutions []model.OpenBankingInstitution
	openBankingCallback     model.OpenBankingCallbackResult
	openBankingCallbackErr  error
}

func (f *fakeAPI) Ready(context.Context) error { return f.readyError }
func (f *fakeAPI) Register(context.Context, model.AuthRequest) (model.AuthResponse, error) {
	if f.registerError != nil {
		return model.AuthResponse{}, f.registerError
	}
	return model.AuthResponse{Token: "token", User: model.User{ID: 1, Email: "person@example.com"}}, nil
}
func (*fakeAPI) Login(context.Context, model.AuthRequest) (model.AuthResponse, error) {
	return model.AuthResponse{Token: "token", User: model.User{ID: 1, Email: "person@example.com"}}, nil
}
func (*fakeAPI) Authenticate(_ context.Context, token string) (int, error) {
	if token == "valid" {
		return 7, nil
	}
	return 0, apperrors.Unauthorized("invalid or expired access token")
}
func (f *fakeAPI) GetMe(context.Context, int) (model.User, error) { return f.user, nil }
func (*fakeAPI) DeleteMe(context.Context, int) error              { return nil }
func (*fakeAPI) ListCategories(context.Context, int, string) ([]model.Category, error) {
	return []model.Category{}, nil
}
func (*fakeAPI) CreateCategory(context.Context, int, model.CategoryRequest) (model.Category, error) {
	return model.Category{}, nil
}
func (*fakeAPI) DeleteCategory(context.Context, int, int) error { return nil }
func (*fakeAPI) ListTransactions(context.Context, int, string, string, string) ([]model.Transaction, error) {
	return []model.Transaction{}, nil
}
func (*fakeAPI) ExportTransactions(context.Context, int, string, string) ([]model.Transaction, error) {
	return []model.Transaction{}, nil
}
func (*fakeAPI) Summary(context.Context, int, string) (model.Summary, error) {
	return model.Summary{}, nil
}
func (*fakeAPI) CreateTransaction(context.Context, int, model.TransactionRequest) (model.Transaction, error) {
	return model.Transaction{}, nil
}
func (*fakeAPI) UpdateTransaction(context.Context, int, int, model.TransactionRequest) (model.Transaction, error) {
	return model.Transaction{}, nil
}
func (*fakeAPI) DeleteTransaction(context.Context, int, int) error { return nil }
func (*fakeAPI) ImportRevolutCSV(context.Context, int, []byte) (model.ImportResult, error) {
	return model.ImportResult{}, nil
}
func (f *fakeAPI) ListOpenBankingInstitutions(context.Context, string, string) ([]model.OpenBankingInstitution, error) {
	if f.openBankingInstitutions == nil {
		return []model.OpenBankingInstitution{}, nil
	}
	return f.openBankingInstitutions, nil
}
func (*fakeAPI) StartOpenBankingAuthorization(context.Context, int, model.OpenBankingAuthorizationRequest) (model.OpenBankingAuthorization, error) {
	return model.OpenBankingAuthorization{AuthorizationURL: "https://auth.enablebanking.com/start"}, nil
}
func (f *fakeAPI) CompleteOpenBankingAuthorization(context.Context, model.OpenBankingCallbackRequest) (model.OpenBankingCallbackResult, error) {
	return f.openBankingCallback, f.openBankingCallbackErr
}
func (*fakeAPI) ListOpenBankingConnections(context.Context, int) ([]model.OpenBankingConnection, error) {
	return []model.OpenBankingConnection{}, nil
}
func (*fakeAPI) GetOpenBankingConnection(context.Context, int, int) (model.OpenBankingConnection, error) {
	return model.OpenBankingConnection{}, nil
}
func (*fakeAPI) DeleteOpenBankingConnection(context.Context, int, int, model.OpenBankingPSUContext) error {
	return nil
}
func (*fakeAPI) ListOpenBankingAccounts(context.Context, int) ([]model.OpenBankingAccount, error) {
	return []model.OpenBankingAccount{}, nil
}
func (*fakeAPI) GetOpenBankingAccountDetails(context.Context, int, int, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error) {
	return model.OpenBankingProviderData(`{}`), nil
}
func (*fakeAPI) GetOpenBankingAccountBalances(context.Context, int, int, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error) {
	return model.OpenBankingProviderData(`{"balances":[]}`), nil
}
func (*fakeAPI) GetOpenBankingAccountTransactions(context.Context, int, int, string, string, string, string, string, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error) {
	return model.OpenBankingProviderData(`{"transactions":[]}`), nil
}
