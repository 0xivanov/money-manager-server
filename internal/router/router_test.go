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

func TestTransactionScheduleEndpointsRequireAuthenticationAndReturnState(t *testing.T) {
	handler := testHandler(&fakeAPI{}, Options{})

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/schedules", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized schedules response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	create := httptest.NewRequest(http.MethodPost, "/schedules", strings.NewReader(
		`{"type":"expense","name":"Rent","category":"Housing","amount":"1200","frequency":"monthly","start_date":"2026-08-01","auto_post":true}`,
	))
	create.Header.Set("Authorization", "Bearer valid")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"name":"Rent"`) {
		t.Fatalf("create schedule response = %d %s", created.Code, created.Body.String())
	}

	pause := httptest.NewRequest(http.MethodPost, "/schedules/9/pause", nil)
	pause.Header.Set("Authorization", "Bearer valid")
	paused := httptest.NewRecorder()
	handler.ServeHTTP(paused, pause)
	if paused.Code != http.StatusOK || !strings.Contains(paused.Body.String(), `"status":"paused"`) {
		t.Fatalf("pause schedule response = %d %s", paused.Code, paused.Body.String())
	}

	occurrences := httptest.NewRequest(http.MethodGet, "/schedule-occurrences?from=2026-08-01&through=2026-08-31", nil)
	occurrences.Header.Set("Authorization", "Bearer valid")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, occurrences)
	if listed.Code != http.StatusOK || strings.TrimSpace(listed.Body.String()) != "[]" {
		t.Fatalf("schedule occurrences response = %d %s", listed.Code, listed.Body.String())
	}
}

func TestProtectedRouteInventoryRequiresAuthentication(t *testing.T) {
	handler := testHandler(&fakeAPI{}, Options{})
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/me"},
		{http.MethodDelete, "/me"},
		{http.MethodGet, "/categories"},
		{http.MethodPost, "/categories"},
		{http.MethodDelete, "/categories/1"},
		{http.MethodGet, "/transactions"},
		{http.MethodGet, "/transactions/export"},
		{http.MethodPost, "/transactions/import/revolut"},
		{http.MethodGet, "/transactions/summary"},
		{http.MethodPost, "/transactions"},
		{http.MethodPut, "/transactions/1"},
		{http.MethodDelete, "/transactions/1"},
		{http.MethodGet, "/schedules"},
		{http.MethodPost, "/schedules"},
		{http.MethodGet, "/schedules/1"},
		{http.MethodPut, "/schedules/1"},
		{http.MethodPost, "/schedules/1/pause"},
		{http.MethodPost, "/schedules/1/resume"},
		{http.MethodDelete, "/schedules/1"},
		{http.MethodGet, "/schedule-occurrences"},
		{http.MethodGet, "/budgets"},
		{http.MethodPost, "/budgets"},
		{http.MethodGet, "/budgets/1"},
		{http.MethodPut, "/budgets/1"},
		{http.MethodDelete, "/budgets/1"},
		{http.MethodGet, "/notification-preferences"},
		{http.MethodPut, "/notification-preferences"},
		{http.MethodPost, "/push-devices"},
		{http.MethodDelete, "/push-devices/1"},
		{http.MethodGet, "/investments/portfolio"},
		{http.MethodGet, "/investments/trades"},
		{http.MethodPost, "/investments/trades"},
		{http.MethodDelete, "/investments/trades/1"},
		{http.MethodPut, "/investments/prices"},
		{http.MethodGet, "/investments/export"},
		{http.MethodGet, "/investment-schedules"},
		{http.MethodPost, "/investment-schedules"},
		{http.MethodGet, "/investment-schedules/1"},
		{http.MethodPut, "/investment-schedules/1"},
		{http.MethodPost, "/investment-schedules/1/pause"},
		{http.MethodPost, "/investment-schedules/1/resume"},
		{http.MethodDelete, "/investment-schedules/1"},
		{http.MethodGet, "/api/open-banking/banks"},
		{http.MethodPost, "/api/open-banking/authorizations"},
		{http.MethodGet, "/api/open-banking/connections"},
		{http.MethodGet, "/api/open-banking/connections/1"},
		{http.MethodDelete, "/api/open-banking/connections/1"},
		{http.MethodGet, "/api/open-banking/accounts"},
		{http.MethodGet, "/api/open-banking/accounts/1/details"},
		{http.MethodGet, "/api/open-banking/accounts/1/balances"},
		{http.MethodGet, "/api/open-banking/accounts/1/transactions"},
		{http.MethodPost, "/api/open-banking/accounts/1/sync"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}

	unsupported := httptest.NewRecorder()
	handler.ServeHTTP(unsupported, httptest.NewRequest(http.MethodPatch, "/budgets", nil))
	if unsupported.Code != http.StatusNotFound {
		t.Fatalf("unsupported method status = %d, body = %s", unsupported.Code, unsupported.Body.String())
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
func (*fakeAPI) ListTransactionSchedules(context.Context, int, string) ([]model.TransactionSchedule, error) {
	return []model.TransactionSchedule{}, nil
}
func (*fakeAPI) CreateTransactionSchedule(context.Context, int, model.TransactionScheduleRequest) (model.TransactionSchedule, error) {
	return model.TransactionSchedule{ID: 9, Name: "Rent", Status: "active"}, nil
}
func (*fakeAPI) GetTransactionSchedule(context.Context, int, int) (model.TransactionSchedule, error) {
	return model.TransactionSchedule{ID: 9, Name: "Rent", Status: "active"}, nil
}
func (*fakeAPI) UpdateTransactionSchedule(context.Context, int, int, model.TransactionScheduleRequest) (model.TransactionSchedule, error) {
	return model.TransactionSchedule{ID: 9, Name: "Rent", Status: "active"}, nil
}
func (*fakeAPI) PauseTransactionSchedule(context.Context, int, int) (model.TransactionSchedule, error) {
	return model.TransactionSchedule{ID: 9, Name: "Rent", Status: "paused"}, nil
}
func (*fakeAPI) ResumeTransactionSchedule(context.Context, int, int) (model.TransactionSchedule, error) {
	return model.TransactionSchedule{ID: 9, Name: "Rent", Status: "active"}, nil
}
func (*fakeAPI) DeleteTransactionSchedule(context.Context, int, int) error { return nil }
func (*fakeAPI) ListTransactionScheduleOccurrences(context.Context, int, string, string, int, string) ([]model.TransactionScheduleOccurrence, error) {
	return []model.TransactionScheduleOccurrence{}, nil
}
func (*fakeAPI) ListBudgets(context.Context, int, bool) ([]model.Budget, error) {
	return []model.Budget{}, nil
}
func (*fakeAPI) GetBudget(context.Context, int, int) (model.Budget, error) {
	return model.Budget{ID: 1, Name: "Food"}, nil
}
func (*fakeAPI) CreateBudget(context.Context, int, model.BudgetRequest) (model.Budget, error) {
	return model.Budget{ID: 1, Name: "Food"}, nil
}
func (*fakeAPI) UpdateBudget(context.Context, int, int, model.BudgetRequest) (model.Budget, error) {
	return model.Budget{ID: 1, Name: "Food"}, nil
}
func (*fakeAPI) DeleteBudget(context.Context, int, int) error { return nil }
func (*fakeAPI) GetNotificationPreferences(context.Context, int) (model.NotificationPreferences, error) {
	return model.NotificationPreferences{Timezone: "Europe/Sofia"}, nil
}
func (*fakeAPI) UpdateNotificationPreferences(context.Context, int, model.NotificationPreferences) (model.NotificationPreferences, error) {
	return model.NotificationPreferences{Timezone: "Europe/Sofia"}, nil
}
func (*fakeAPI) RegisterPushDevice(context.Context, int, model.PushDeviceRequest) (model.PushDevice, error) {
	return model.PushDevice{ID: 1, Platform: "ios"}, nil
}
func (*fakeAPI) DeletePushDevice(context.Context, int, int) error { return nil }
func (*fakeAPI) CreateInvestmentTrade(context.Context, int, model.InvestmentTradeRequest) (model.InvestmentTrade, error) {
	return model.InvestmentTrade{ID: 1, Symbol: "BTC"}, nil
}
func (*fakeAPI) ListInvestmentTrades(context.Context, int, string, string, string, string, string) ([]model.InvestmentTrade, error) {
	return []model.InvestmentTrade{}, nil
}
func (*fakeAPI) DeleteInvestmentTrade(context.Context, int, int) error { return nil }
func (*fakeAPI) InvestmentPortfolio(context.Context, int) (model.InvestmentPortfolio, error) {
	return model.InvestmentPortfolio{Positions: []model.InvestmentPosition{}, Currency: "EUR"}, nil
}
func (*fakeAPI) SetManualInvestmentPrice(context.Context, int, model.InvestmentPriceRequest) (model.InvestmentPrice, error) {
	return model.InvestmentPrice{Symbol: "BTC", Price: "1.00"}, nil
}
func (*fakeAPI) ExportInvestmentTrades(context.Context, int, string, string) ([]model.InvestmentTrade, error) {
	return []model.InvestmentTrade{}, nil
}
func (*fakeAPI) ListInvestmentSchedules(context.Context, int, string) ([]model.InvestmentSchedule, error) {
	return []model.InvestmentSchedule{}, nil
}
func (*fakeAPI) CreateInvestmentSchedule(context.Context, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error) {
	return model.InvestmentSchedule{ID: 1, Symbol: "BTC", Status: "active"}, nil
}
func (*fakeAPI) GetInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error) {
	return model.InvestmentSchedule{ID: 1, Symbol: "BTC", Status: "active"}, nil
}
func (*fakeAPI) UpdateInvestmentSchedule(context.Context, int, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error) {
	return model.InvestmentSchedule{ID: 1, Symbol: "BTC", Status: "active"}, nil
}
func (*fakeAPI) PauseInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error) {
	return model.InvestmentSchedule{ID: 1, Symbol: "BTC", Status: "paused"}, nil
}
func (*fakeAPI) ResumeInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error) {
	return model.InvestmentSchedule{ID: 1, Symbol: "BTC", Status: "active"}, nil
}
func (*fakeAPI) DeleteInvestmentSchedule(context.Context, int, int) error { return nil }
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
func (*fakeAPI) SyncOpenBankingAccount(context.Context, int, int, string, string, model.OpenBankingPSUContext) (model.OpenBankingSyncResult, error) {
	return model.OpenBankingSyncResult{}, nil
}
