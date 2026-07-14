package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/enablebanking"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

func TestStartOpenBankingAuthorizationBindsStateAndClampsConsent(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	var providerRequest enablebanking.StartAuthorizationRequest
	client := &fakeOpenBankingClient{
		institutions: []enablebanking.Institution{{
			Name: "Revolut", Country: "BG", PSUTypes: []string{"personal"},
			MaximumConsentValidity: int64((30 * 24 * time.Hour) / time.Second),
		}},
		startAuthorization: func(_ context.Context, request enablebanking.StartAuthorizationRequest) (enablebanking.StartAuthorizationResponse, error) {
			providerRequest = request
			return enablebanking.StartAuthorizationResponse{
				URL: "https://auth.enablebanking.com/start", AuthorizationID: "provider-authorization",
			}, nil
		},
	}
	var stored repository.NewOpenBankingAuthorization
	store := &fakeStore{
		createOpenBankingAuthorization: func(_ context.Context, record repository.NewOpenBankingAuthorization) (int, error) {
			stored = record
			return 17, nil
		},
		setOpenBankingProviderID: func(_ context.Context, id int, providerID string) error {
			if id != 17 || providerID != "provider-authorization" {
				t.Fatalf("provider ID update = %d, %q", id, providerID)
			}
			return nil
		},
	}
	service := openBankingTestService(store, client, now)

	response, err := service.StartOpenBankingAuthorization(context.Background(), 7, model.OpenBankingAuthorizationRequest{
		InstitutionName: " revolut ", Country: "bg", PSUType: "personal", ConsentDays: 90, Language: "EN",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.AuthorizationID != "provider-authorization" || providerRequest.RedirectURL != "https://money.example/api/open-banking/callback" {
		t.Fatalf("authorization response/request = %#v / %#v", response, providerRequest)
	}
	if providerRequest.Access.ValidUntil != now.Add(30*24*time.Hour).Format(time.RFC3339) || providerRequest.Language != "en" {
		t.Fatalf("provider access = %#v", providerRequest.Access)
	}
	if stored.UserID != 7 || stored.StateHash == providerRequest.State || len(stored.StateHash) != 64 || stored.ExpiresAt != now.Add(15*time.Minute) {
		t.Fatalf("stored authorization = %#v", stored)
	}
	sum := sha256.Sum256([]byte(providerRequest.State))
	if stored.StateHash != hex.EncodeToString(sum[:]) {
		t.Fatal("stored state hash does not match generated state")
	}
}

func TestCompleteOpenBankingAuthorizationStoresOwnedSessionAndAccounts(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	state := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	stateSum := sha256.Sum256([]byte(state))
	client := &fakeOpenBankingClient{
		authorizeSession: func(_ context.Context, code string) (enablebanking.AuthorizeSessionResponse, error) {
			if code != "authorization-code" {
				t.Fatalf("authorization code = %q", code)
			}
			return enablebanking.AuthorizeSessionResponse{
				SessionID: "provider-session",
				Access:    enablebanking.Access{ValidUntil: "2026-10-01T00:00:00Z"},
				Accounts: []enablebanking.Account{{
					UID: "provider-account", IdentificationHash: "stable-account-hash",
					AccountID: enablebanking.AccountIdentification{IBAN: "BG80REV01234567890123"},
					Name:      "Everyday", CashAccountType: "CACC", Currency: "EUR",
					Raw: json.RawMessage(`{"uid":"provider-account","currency":"EUR"}`),
				}},
			}, nil
		},
	}
	var stored repository.NewOpenBankingConnection
	store := &fakeStore{
		claimOpenBankingAuthorization: func(_ context.Context, hash string, claimedAt time.Time) (repository.OpenBankingAuthorizationRecord, error) {
			if hash != hex.EncodeToString(stateSum[:]) || claimedAt != now {
				t.Fatalf("claim = %q at %s", hash, claimedAt)
			}
			return repository.OpenBankingAuthorizationRecord{
				ID: 3, UserID: 7, InstitutionName: "Revolut", Country: "BG", PSUType: "personal",
				ValidUntil: now.Add(30 * 24 * time.Hour),
			}, nil
		},
		storeOpenBankingConnection: func(_ context.Context, record repository.NewOpenBankingConnection) (int, error) {
			stored = record
			return 29, nil
		},
	}
	service := openBankingTestService(store, client, now)
	result, err := service.CompleteOpenBankingAuthorization(context.Background(), model.OpenBankingCallbackRequest{
		State: state, Code: "authorization-code",
	})
	if err != nil || result.Status != "connected" || result.ConnectionID != 29 {
		t.Fatalf("callback result = %#v, %v", result, err)
	}
	if stored.UserID != 7 || stored.ProviderSession != "provider-session" || len(stored.Accounts) != 1 {
		t.Fatalf("stored connection = %#v", stored)
	}
	account := stored.Accounts[0]
	if account.ProviderAccountID != "provider-account" || account.DisplayIdentifier != "•••• 0123" || account.IdentificationHash != "stable-account-hash" {
		t.Fatalf("stored account = %#v", account)
	}
	parsedRedirect, parseErr := url.Parse(result.RedirectURL)
	if parseErr != nil || parsedRedirect.Query().Get("status") != "connected" || parsedRedirect.Query().Get("connection_id") != "29" {
		t.Fatalf("callback redirect = %q, %v", result.RedirectURL, parseErr)
	}
}

func TestCompleteOpenBankingAuthorizationBoundsDetachedSessionCleanup(t *testing.T) {
	state := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	cleanupCalled := false
	client := &fakeOpenBankingClient{
		authorizeSession: func(context.Context, string) (enablebanking.AuthorizeSessionResponse, error) {
			return enablebanking.AuthorizeSessionResponse{SessionID: "provider-session"}, nil
		},
		deleteSession: func(ctx context.Context, sessionID string, _ enablebanking.PSUHeaders) error {
			cleanupCalled = true
			if sessionID != "provider-session" {
				t.Fatalf("session ID = %q", sessionID)
			}
			if ctx.Err() != nil {
				t.Fatalf("cleanup inherited request cancellation: %v", ctx.Err())
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Second {
				t.Fatalf("cleanup deadline = %s, present=%v", deadline, ok)
			}
			return nil
		},
	}
	store := &fakeStore{
		claimOpenBankingAuthorization: func(context.Context, string, time.Time) (repository.OpenBankingAuthorizationRecord, error) {
			return repository.OpenBankingAuthorizationRecord{ID: 3, UserID: 7}, nil
		},
		storeOpenBankingConnection: func(context.Context, repository.NewOpenBankingConnection) (int, error) {
			return 0, errors.New("database unavailable")
		},
	}
	service := openBankingTestService(store, client, time.Now().UTC())
	service.openBankingConfig.requestTimeout = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.CompleteOpenBankingAuthorization(ctx, model.OpenBankingCallbackRequest{
		State: state, Code: "authorization-code",
	})
	if err == nil || !cleanupCalled {
		t.Fatalf("cleanup called=%v, error=%v", cleanupCalled, err)
	}
}

func TestCompleteOpenBankingAuthorizationConsumesCancelledStateWithoutSessionExchange(t *testing.T) {
	state := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	failed := false
	store := &fakeStore{
		claimOpenBankingAuthorization: func(context.Context, string, time.Time) (repository.OpenBankingAuthorizationRecord, error) {
			return repository.OpenBankingAuthorizationRecord{ID: 8}, nil
		},
		failOpenBankingAuthorization: func(_ context.Context, id int, code, description string) error {
			failed = id == 8 && code == "access_denied" && description == "Cancelled by user"
			return nil
		},
	}
	service := openBankingTestService(store, &fakeOpenBankingClient{}, time.Now().UTC())
	result, err := service.CompleteOpenBankingAuthorization(context.Background(), model.OpenBankingCallbackRequest{
		State: state, Error: "access_denied", ErrorDescription: "Cancelled by user",
	})
	if err != nil || result.Status != "cancelled" || !failed {
		t.Fatalf("cancel callback = %#v, failed=%v, err=%v", result, failed, err)
	}
}

func TestOpenBankingTransactionsRequireOwnedAccountAndValidateProviderQuery(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	var providerQuery url.Values
	client := &fakeOpenBankingClient{
		accountTransactions: func(_ context.Context, accountID string, query url.Values, headers enablebanking.PSUHeaders) (json.RawMessage, error) {
			if accountID != "provider-account" || headers.IPAddress != "198.51.100.7" {
				t.Fatalf("provider account/context = %q / %#v", accountID, headers)
			}
			providerQuery = query
			return json.RawMessage(`{"transactions":[]}`), nil
		},
	}
	store := &fakeStore{getOpenBankingAccount: func(_ context.Context, userID, accountID int) (repository.OpenBankingAccountRecord, error) {
		if userID != 7 || accountID != 11 {
			t.Fatalf("ownership lookup = %d/%d", userID, accountID)
		}
		return repository.OpenBankingAccountRecord{ProviderAccountID: "provider-account"}, nil
	}}
	service := openBankingTestService(store, client, now)
	response, err := service.GetOpenBankingAccountTransactions(
		context.Background(), 7, 11, "2026-07-01", "2026-07-13", "next", "book", "default",
		model.OpenBankingPSUContext{IPAddress: "198.51.100.7"},
	)
	if err != nil || !json.Valid(response) || providerQuery.Get("transaction_status") != "BOOK" || providerQuery.Get("continuation_key") != "next" {
		t.Fatalf("transactions = %s, query=%v, err=%v", response, providerQuery, err)
	}
	_, err = service.GetOpenBankingAccountTransactions(context.Background(), 7, 11, "", "2026-07-13", "", "", "", model.OpenBankingPSUContext{})
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("date_to without date_from error = %v", err)
	}
}

func TestSyncOpenBankingAccountPaginatesNormalizesAndPersists(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	requests := 0
	client := &fakeOpenBankingClient{accountTransactions: func(
		_ context.Context,
		accountID string,
		query url.Values,
		headers enablebanking.PSUHeaders,
	) (json.RawMessage, error) {
		requests++
		if accountID != "provider-account" || headers.IPAddress != "198.51.100.9" {
			t.Fatalf("provider account/headers = %q / %#v", accountID, headers)
		}
		if query.Get("date_from") != "2026-04-14" || query.Get("date_to") != "2026-07-13" {
			t.Fatalf("sync range = %v", query)
		}
		if requests == 1 {
			if query.Get("continuation_key") != "" {
				t.Fatalf("first continuation = %q", query.Get("continuation_key"))
			}
			return json.RawMessage(`{"transactions":[{
				"entry_reference":"expense-1","merchant_category_code":"5411",
				"transaction_amount":{"currency":"EUR","amount":"42.8"},
				"credit_debit_indicator":"DBIT","status":"BOOK","booking_date":"2026-07-12",
				"creditor":{"name":"Fresh Market"}
			}],"continuation_key":"next-page"}`), nil
		}
		if query.Get("continuation_key") != "next-page" {
			t.Fatalf("second continuation = %q", query.Get("continuation_key"))
		}
		return json.RawMessage(`{"transactions":[{
			"transaction_id":"income-1","transaction_amount":{"currency":"EUR","amount":"3200"},
			"credit_debit_indicator":"CRDT","status":"BOOK","transaction_date":"2026-07-10",
			"debtor":{"name":"Monthly salary"}
		},{
			"transaction_id":"ignored-usd","transaction_amount":{"currency":"USD","amount":"12"},
			"credit_debit_indicator":"DBIT","status":"BOOK","booking_date":"2026-07-11"
		}]}`), nil
	}}
	var stored []repository.OpenBankingTransactionSeed
	store := &fakeStore{
		getOpenBankingAccount: func(context.Context, int, int) (repository.OpenBankingAccountRecord, error) {
			return repository.OpenBankingAccountRecord{ProviderAccountID: "provider-account"}, nil
		},
		importOpenBankingTransactions: func(
			_ context.Context,
			userID int,
			accountID int,
			seeds []repository.OpenBankingTransactionSeed,
			syncedAt time.Time,
		) (model.OpenBankingSyncResult, error) {
			if userID != 7 || accountID != 11 || syncedAt != now {
				t.Fatalf("store sync context = %d/%d at %s", userID, accountID, syncedAt)
			}
			stored = seeds
			return model.OpenBankingSyncResult{Imported: len(seeds)}, nil
		},
	}
	service := openBankingTestService(store, client, now)
	result, err := service.SyncOpenBankingAccount(
		context.Background(), 7, 11, "", "", model.OpenBankingPSUContext{IPAddress: "198.51.100.9"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || result.Fetched != 3 || result.Imported != 2 || result.Ignored != 1 || len(stored) != 2 {
		t.Fatalf("sync result = %#v, requests=%d, stored=%#v", result, requests, stored)
	}
	if stored[0].Type != "expense" || stored[0].Category != "food" || stored[0].Amount != "42.80" || stored[0].Description != "Fresh Market" {
		t.Fatalf("expense seed = %#v", stored[0])
	}
	if stored[1].Type != "income" || stored[1].Category != "salary" || stored[1].Amount != "3200.00" {
		t.Fatalf("income seed = %#v", stored[1])
	}
}

func TestNormalizeOpenBankingTransactionPrefersStableEntryReference(t *testing.T) {
	today := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	transaction := func(detailID string) repository.OpenBankingTransactionSeed {
		raw := json.RawMessage(`{
			"entry_reference":"stable-reference",
			"transaction_id":"` + detailID + `",
			"transaction_amount":{"currency":"EUR","amount":"12.50"},
			"credit_debit_indicator":"DBIT",
			"status":"BOOK",
			"booking_date":"2026-07-12",
			"creditor":{"name":"Market"}
		}`)
		item, ok := normalizeOpenBankingTransaction(raw, today)
		if !ok {
			t.Fatalf("transaction %q was ignored", detailID)
		}
		return item
	}
	first := transaction("temporary-detail-id-1")
	second := transaction("temporary-detail-id-2")
	if first.ExternalID != "stable-reference" || second.ExternalID != first.ExternalID {
		t.Fatalf("external IDs = %q and %q", first.ExternalID, second.ExternalID)
	}
}

func TestNormalizeOpenBankingTransactionAcceptsStringAndNumericMCC(t *testing.T) {
	today := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	for _, merchantCategoryCode := range []string{`"5411"`, `5411`} {
		raw := json.RawMessage(`{
			"entry_reference":"food-purchase",
			"merchant_category_code":` + merchantCategoryCode + `,
			"transaction_amount":{"currency":"EUR","amount":"18.90"},
			"credit_debit_indicator":"DBIT",
			"status":"BOOK",
			"booking_date":"2026-07-12",
			"creditor":{"name":"Unknown merchant"}
		}`)
		item, ok := normalizeOpenBankingTransaction(raw, today)
		if !ok || item.Category != "food" {
			t.Fatalf("MCC %s normalized transaction = %#v, included=%v", merchantCategoryCode, item, ok)
		}
		var metadata map[string]any
		if err := json.Unmarshal(item.Metadata, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata["merchant_category_code"] != "5411" ||
			metadata["classification_source"] != openBankingCategorySourceMCC ||
			metadata["category_source"] != openBankingCategorySourceMCC {
			t.Fatalf("MCC %s metadata = %#v", merchantCategoryCode, metadata)
		}
	}
}

func TestNormalizeOpenBankingTransactionUsesRevolutFriendlyKeywordFallbacks(t *testing.T) {
	today := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name             string
		providerFields   string
		indicator        string
		expectedCategory string
		expectedSource   string
	}{
		{
			name: "expense merchant hidden in remittance",
			providerFields: `"creditor":{"name":"Revolut"},
				"remittance_information":["Card payment to LIDL Bulgaria"]`,
			indicator:        "DBIT",
			expectedCategory: "food",
			expectedSource:   openBankingCategorySourceExpenseKeyword,
		},
		{
			name:             "expense merchant name",
			providerFields:   `"creditor":{"name":"BOLT.EU ride"}`,
			indicator:        "DBIT",
			expectedCategory: "transport",
			expectedSource:   openBankingCategorySourceExpenseKeyword,
		},
		{
			name:             "income description",
			providerFields:   `"debtor":{"name":"ACME client invoice 1042"}`,
			indicator:        "CRDT",
			expectedCategory: "freelance",
			expectedSource:   openBankingCategorySourceIncomeKeyword,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{
				"entry_reference":"keyword-transaction",
				"transaction_amount":{"currency":"EUR","amount":"18.90"},
				"credit_debit_indicator":"` + test.indicator + `",
				"status":"BOOK",
				"booking_date":"2026-07-12",
				` + test.providerFields + `
			}`)
			item, ok := normalizeOpenBankingTransaction(raw, today)
			if !ok || item.Category != test.expectedCategory {
				t.Fatalf("normalized transaction = %#v, included=%v", item, ok)
			}
			var metadata map[string]any
			if err := json.Unmarshal(item.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata["classification_source"] != test.expectedSource ||
				metadata["classified_category"] != test.expectedCategory {
				t.Fatalf("classification metadata = %#v", metadata)
			}
		})
	}
}

func TestOpenBankingCategoryClassifierPrefersMCCBeforeKeywords(t *testing.T) {
	classification := classifyOpenBankingTransaction("expense", "5541", "LIDL supermarket")
	if classification.Category != "transport" || classification.Source != openBankingCategorySourceMCC {
		t.Fatalf("classification = %#v", classification)
	}
}

func TestOpenBankingBackgroundMaintenanceClaimsOnceAndContinuesAfterFailure(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	var released []int
	store := &fakeStore{
		claimOpenBankingAccountsForSync: func(
			_ context.Context, claimedAt, nextAttempt, claimUntil time.Time, limit int,
		) ([]repository.OpenBankingSyncAccount, error) {
			if !claimedAt.Equal(now) || !nextAttempt.Equal(now.Add(6*time.Hour)) ||
				!claimUntil.Equal(now.Add(5*time.Minute)) || limit != 10 {
				t.Fatalf("claim window = %s, %s, %s, %d", claimedAt, nextAttempt, claimUntil, limit)
			}
			return []repository.OpenBankingSyncAccount{{UserID: 1, AccountID: 10}, {UserID: 2, AccountID: 20}}, nil
		},
		getOpenBankingAccount: func(_ context.Context, userID, accountID int) (repository.OpenBankingAccountRecord, error) {
			return repository.OpenBankingAccountRecord{
				Account:           model.OpenBankingAccount{ID: accountID},
				ProviderAccountID: fmt.Sprintf("provider-%d", userID),
			}, nil
		},
		importOpenBankingTransactions: func(
			_ context.Context, userID, accountID int, _ []repository.OpenBankingTransactionSeed, _ time.Time,
		) (model.OpenBankingSyncResult, error) {
			if userID != 1 || accountID != 10 {
				t.Fatalf("unexpected persisted account %d/%d", userID, accountID)
			}
			return model.OpenBankingSyncResult{Imported: 1, Notifications: 1}, nil
		},
		releaseOpenBankingSyncClaim: func(_ context.Context, accountID int) error {
			released = append(released, accountID)
			return nil
		},
	}
	client := &fakeOpenBankingClient{
		accountTransactions: func(_ context.Context, accountID string, _ url.Values, headers enablebanking.PSUHeaders) (json.RawMessage, error) {
			if headers != (enablebanking.PSUHeaders{}) {
				t.Fatalf("background sync sent PSU headers: %#v", headers)
			}
			if accountID == "provider-2" {
				return nil, errors.New("provider unavailable")
			}
			return json.RawMessage(`{"transactions":[]}`), nil
		},
	}
	service := openBankingTestService(store, client, now)
	result, err := service.RunOpenBankingSyncMaintenance(context.Background())
	if err == nil {
		t.Fatal("maintenance error = nil")
	}
	if result.Claimed != 2 || result.Succeeded != 1 || result.Failed != 1 ||
		result.Imported != 1 || result.Notifications != 1 {
		t.Fatalf("maintenance result = %#v", result)
	}
	if len(released) != 1 || released[0] != 20 {
		t.Fatalf("released claims = %#v", released)
	}
}

func TestOpenBankingRequiresConfiguration(t *testing.T) {
	service := testService(&fakeStore{})
	_, err := service.ListOpenBankingInstitutions(context.Background(), "BG", "personal")
	if apperrors.KindOf(err) != apperrors.KindUnavailable {
		t.Fatalf("unconfigured error = %v", err)
	}
}

func TestDeleteMeRevokesProviderSessionsBeforeDeletingUser(t *testing.T) {
	deletedUser := false
	store := &fakeStore{
		listOpenBankingProviderSessions: func(_ context.Context, userID int) ([]string, error) {
			if userID != 7 {
				t.Fatalf("session lookup user = %d", userID)
			}
			return []string{"session-one", "session-two"}, nil
		},
		deleteUser: func(_ context.Context, userID int) error {
			deletedUser = userID == 7
			return nil
		},
	}
	var revoked []string
	client := &fakeOpenBankingClient{deleteSession: func(_ context.Context, sessionID string, _ enablebanking.PSUHeaders) error {
		revoked = append(revoked, sessionID)
		return nil
	}}
	service := openBankingTestService(store, client, time.Now().UTC())
	if err := service.DeleteMe(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if !deletedUser || len(revoked) != 2 || revoked[0] != "session-one" || revoked[1] != "session-two" {
		t.Fatalf("deleted=%v, revoked=%v", deletedUser, revoked)
	}
}

func openBankingTestService(store Store, client openBankingClient, now time.Time) *Service {
	service := testService(store)
	service.openBanking = client
	service.now = func() time.Time { return now }
	service.openBankingConfig = openBankingServiceConfig{
		callbackURL:       "https://money.example/api/open-banking/callback",
		resultRedirectURL: "moneymanager://open-banking",
		consentDays:       90,
		stateTTL:          15 * time.Minute,
	}
	return service
}

type fakeOpenBankingClient struct {
	institutions        []enablebanking.Institution
	listError           error
	startAuthorization  func(context.Context, enablebanking.StartAuthorizationRequest) (enablebanking.StartAuthorizationResponse, error)
	authorizeSession    func(context.Context, string) (enablebanking.AuthorizeSessionResponse, error)
	accountTransactions func(context.Context, string, url.Values, enablebanking.PSUHeaders) (json.RawMessage, error)
	deleteSession       func(context.Context, string, enablebanking.PSUHeaders) error
}

func (f *fakeOpenBankingClient) ListInstitutions(context.Context, string, string) ([]enablebanking.Institution, error) {
	return f.institutions, f.listError
}
func (f *fakeOpenBankingClient) StartAuthorization(ctx context.Context, request enablebanking.StartAuthorizationRequest) (enablebanking.StartAuthorizationResponse, error) {
	if f.startAuthorization == nil {
		return enablebanking.StartAuthorizationResponse{}, errors.New("unexpected StartAuthorization call")
	}
	return f.startAuthorization(ctx, request)
}
func (f *fakeOpenBankingClient) AuthorizeSession(ctx context.Context, code string) (enablebanking.AuthorizeSessionResponse, error) {
	if f.authorizeSession == nil {
		return enablebanking.AuthorizeSessionResponse{}, errors.New("unexpected AuthorizeSession call")
	}
	return f.authorizeSession(ctx, code)
}
func (*fakeOpenBankingClient) GetSession(context.Context, string) (enablebanking.Session, error) {
	return enablebanking.Session{}, errors.New("unexpected GetSession call")
}
func (f *fakeOpenBankingClient) DeleteSession(ctx context.Context, sessionID string, headers enablebanking.PSUHeaders) error {
	if f.deleteSession != nil {
		return f.deleteSession(ctx, sessionID, headers)
	}
	return nil
}
func (*fakeOpenBankingClient) AccountDetails(context.Context, string, enablebanking.PSUHeaders) (json.RawMessage, error) {
	return nil, errors.New("unexpected AccountDetails call")
}
func (*fakeOpenBankingClient) AccountBalances(context.Context, string, enablebanking.PSUHeaders) (json.RawMessage, error) {
	return nil, errors.New("unexpected AccountBalances call")
}
func (f *fakeOpenBankingClient) AccountTransactions(ctx context.Context, accountID string, query url.Values, headers enablebanking.PSUHeaders) (json.RawMessage, error) {
	if f.accountTransactions == nil {
		return nil, errors.New("unexpected AccountTransactions call")
	}
	return f.accountTransactions(ctx, accountID, query, headers)
}
