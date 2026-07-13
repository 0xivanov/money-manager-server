package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/config"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"

	"github.com/golang-jwt/jwt/v5"
)

func TestRegisterNormalizesEmailAndIssuesToken(t *testing.T) {
	store := &fakeStore{}
	store.registerUser = func(_ context.Context, email, passwordHash string) (model.User, error) {
		if email != "person@example.com" {
			t.Fatalf("normalized email = %q", email)
		}
		if passwordHash == "" || passwordHash == "correct horse" {
			t.Fatal("password was not hashed")
		}
		return model.User{ID: 42, Email: email}, nil
	}
	service := testService(store)

	response, err := service.Register(context.Background(), model.AuthRequest{
		Email: " Person@Example.COM ", Password: "correct horse",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if response.User.ID != 42 || response.Token == "" {
		t.Fatalf("unexpected response: %#v", response)
	}
	userID, err := service.ParseUserID(response.Token)
	if err != nil || userID != 42 {
		t.Fatalf("ParseUserID() = %d, %v", userID, err)
	}
}

func TestRegisterValidatesPasswordAndMapsConflict(t *testing.T) {
	service := testService(&fakeStore{})
	_, err := service.Register(context.Background(), model.AuthRequest{Email: "person@example.com", Password: "short"})
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("short password kind = %q, error = %v", apperrors.KindOf(err), err)
	}

	store := &fakeStore{registerUser: func(context.Context, string, string) (model.User, error) {
		return model.User{}, repository.ErrConflict
	}}
	service = testService(store)
	_, err = service.Register(context.Background(), model.AuthRequest{Email: "person@example.com", Password: "long enough"})
	if apperrors.KindOf(err) != apperrors.KindConflict {
		t.Fatalf("conflict kind = %q, error = %v", apperrors.KindOf(err), err)
	}
}

func TestParseUserIDRejectsWrongMethodAndAudience(t *testing.T) {
	service := testService(&fakeStore{})
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	service.legacyAcceptUntil = now.Add(time.Hour)
	claims := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "money-manager-api", Subject: "7", Audience: jwt.ClaimStrings{"wrong"},
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}}
	wrongMethod, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ParseUserID(wrongMethod); apperrors.KindOf(err) != apperrors.KindUnauthorized {
		t.Fatalf("wrong method error = %v", err)
	}
	wrongAudience, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ParseUserID(wrongAudience); apperrors.KindOf(err) != apperrors.KindUnauthorized {
		t.Fatalf("wrong audience error = %v", err)
	}
}

func TestParseUserIDAllowsBoundedLegacyTransition(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	service := testService(&fakeStore{})
	service.now = func() time.Time { return now }
	service.legacyAcceptUntil = now.Add(time.Hour)
	legacyClaims := tokenClaims{
		Email: "person@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "7", ExpiresAt: jwt.NewNumericDate(now.Add(30 * time.Minute)),
		},
	}
	legacyToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, legacyClaims).SignedString([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if userID, err := service.ParseUserID(legacyToken); err != nil || userID != 7 {
		t.Fatalf("legacy token user = %d, error = %v", userID, err)
	}

	service.now = func() time.Time { return now.Add(time.Hour + time.Second) }
	if _, err := service.ParseUserID(legacyToken); apperrors.KindOf(err) != apperrors.KindUnauthorized {
		t.Fatalf("legacy token after transition error = %v", err)
	}
}

func TestParseUserIDRejectsLegacyTokenExpiringAfterTransition(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	service := testService(&fakeStore{})
	service.now = func() time.Time { return now }
	service.legacyAcceptUntil = now.Add(time.Hour)
	claims := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject: "7", ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Hour)),
	}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ParseUserID(token); apperrors.KindOf(err) != apperrors.KindUnauthorized {
		t.Fatalf("overlong legacy token error = %v", err)
	}
}

func TestCreateTransactionNormalizesFinancialFields(t *testing.T) {
	store := &fakeStore{
		findCategory: func(context.Context, int, string, string) (string, error) { return "Food", nil },
		createTransaction: func(_ context.Context, _ int, request model.TransactionRequest) (model.Transaction, error) {
			if request.Type != "expense" || request.Category != "Food" || request.Amount != "12.50" || request.Currency != "EUR" {
				t.Fatalf("unexpected normalized request: %#v", request)
			}
			if request.Description != "Lunch" || request.OccurredAt != "2026-07-11" {
				t.Fatalf("unexpected text/date fields: %#v", request)
			}
			return model.Transaction{ID: 1, Amount: request.Amount}, nil
		},
	}
	service := testService(store)
	transaction, err := service.CreateTransaction(context.Background(), 1, model.TransactionRequest{
		Type: " Expense ", Category: "food", Description: " Lunch ", Amount: "0012.5", OccurredAt: "2026-07-11",
	})
	if err != nil || transaction.ID != 1 {
		t.Fatalf("CreateTransaction() = %#v, %v", transaction, err)
	}
}

func TestCreateTransactionRejectsInvalidMoneyAndCategory(t *testing.T) {
	tests := []model.TransactionRequest{
		{Type: "expense", Category: "food", Amount: "0", Currency: "EUR", OccurredAt: "2026-07-11"},
		{Type: "expense", Category: "food", Amount: "1.001", Currency: "EUR", OccurredAt: "2026-07-11"},
		{Type: "expense", Category: "food", Amount: "1.00", Currency: "USD", OccurredAt: "2026-07-11"},
		{Type: "expense", Category: "food", Amount: "1.00", Currency: "EUR", OccurredAt: "2026-02-30"},
	}
	for _, request := range tests {
		service := testService(&fakeStore{findCategory: func(context.Context, int, string, string) (string, error) { return "food", nil }})
		if _, err := service.CreateTransaction(context.Background(), 1, request); apperrors.KindOf(err) != apperrors.KindValidation {
			t.Errorf("request %#v error = %v", request, err)
		}
	}

	service := testService(&fakeStore{findCategory: func(context.Context, int, string, string) (string, error) {
		return "", repository.ErrNotFound
	}})
	_, err := service.CreateTransaction(context.Background(), 1, model.TransactionRequest{
		Type: "expense", Category: "missing", Amount: "1.00", Currency: "EUR", OccurredAt: "2026-07-11",
	})
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("missing category error = %v", err)
	}
}

func TestListTransactionsRequiresValidMonth(t *testing.T) {
	service := testService(&fakeStore{})
	if _, err := service.ListTransactions(context.Background(), 1, "July", "", ""); apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("invalid month error = %v", err)
	}
}

func TestUpdateTransactionAllowsUnchangedArchivedCategory(t *testing.T) {
	store := &fakeStore{
		getTransaction: func(context.Context, int, int) (model.Transaction, error) {
			return model.Transaction{ID: 9, Type: "expense", Category: "Archived", Amount: "4.00", Currency: "EUR", OccurredAt: "2026-07-10"}, nil
		},
		findCategory: func(context.Context, int, string, string) (string, error) {
			t.Fatal("unchanged archived category must not be looked up as active")
			return "", repository.ErrNotFound
		},
		updateTransaction: func(_ context.Context, _ int, _ int, request model.TransactionRequest) (model.Transaction, error) {
			if request.Category != "Archived" || request.Amount != "5.00" {
				t.Fatalf("unexpected update request: %#v", request)
			}
			return model.Transaction{ID: 9, Category: request.Category, Amount: request.Amount}, nil
		},
	}
	service := testService(store)
	updated, err := service.UpdateTransaction(context.Background(), 1, 9, model.TransactionRequest{
		Type: "expense", Category: "archived", Amount: "5", Currency: "EUR", OccurredAt: "2026-07-10",
	})
	if err != nil || updated.Category != "Archived" {
		t.Fatalf("UpdateTransaction() = %#v, %v", updated, err)
	}
}

func TestUpdateTransactionValidatesChangedCategory(t *testing.T) {
	store := &fakeStore{
		getTransaction: func(context.Context, int, int) (model.Transaction, error) {
			return model.Transaction{ID: 9, Type: "expense", Category: "Archived"}, nil
		},
		findCategory: func(context.Context, int, string, string) (string, error) {
			return "", repository.ErrNotFound
		},
	}
	service := testService(store)
	_, err := service.UpdateTransaction(context.Background(), 1, 9, model.TransactionRequest{
		Type: "expense", Category: "replacement", Amount: "5.00", Currency: "EUR", OccurredAt: "2026-07-10",
	})
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("changed category error = %v", err)
	}
}

func TestExportTransactionsIsBounded(t *testing.T) {
	service := testService(&fakeStore{})
	if _, err := service.ExportTransactions(context.Background(), 1, "2025-01-01", "2026-01-02"); apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("oversized range error = %v", err)
	}

	store := &fakeStore{exportTransactions: func(context.Context, int, time.Time, time.Time, int) ([]model.Transaction, error) {
		return make([]model.Transaction, maximumExportRows+1), nil
	}}
	service = testService(store)
	if _, err := service.ExportTransactions(context.Background(), 1, "2026-01-01", "2026-01-31"); apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("oversized row count error = %v", err)
	}
}

func TestImportRevolutCSVNormalizesAndIgnoresUnsupportedRows(t *testing.T) {
	store := &fakeStore{
		findCategory: func(_ context.Context, _ int, transactionType, name string) (string, error) {
			if name != "other" {
				t.Fatalf("category name = %q", name)
			}
			return "other", nil
		},
		importTransactions: func(_ context.Context, userID int, transactions []model.ImportedTransaction) (int, int, error) {
			if userID != 7 || len(transactions) != 2 {
				t.Fatalf("import user/rows = %d/%d", userID, len(transactions))
			}
			expense := transactions[0]
			if expense.Request.Type != "expense" || expense.Request.Amount != "12.34" || expense.Request.OccurredAt != "2026-07-11" || expense.Request.Description != "Coffee Shop" {
				t.Fatalf("expense = %#v", expense)
			}
			income := transactions[1]
			if income.Request.Type != "income" || income.Request.Amount != "50.00" {
				t.Fatalf("income = %#v", income)
			}
			if expense.Fingerprint == "" || income.Fingerprint == "" || expense.Fingerprint == income.Fingerprint {
				t.Fatal("missing or duplicate fingerprints")
			}
			return 1, 1, nil
		},
	}
	csv := "Type,Product,Started Date,Completed Date,Description,Amount,Fee,Currency,State,Balance\n" +
		"CARD_PAYMENT,Current,2026-07-11 10:00:00,2026-07-11 10:01:00,Coffee Shop,-12.34,0,EUR,COMPLETED,100\n" +
		"TRANSFER,Current,2026-07-12 09:00:00,2026-07-12 09:00:00,Friend,50,0,EUR,COMPLETED,150\n" +
		"CARD_PAYMENT,Current,2026-07-12 10:00:00,,Pending Shop,-4,0,EUR,PENDING,146\n" +
		"CARD_PAYMENT,Current,2026-07-12 11:00:00,2026-07-12 11:00:00,Dollar Shop,-5,0,USD,COMPLETED,141\n"

	result, err := testService(store).ImportRevolutCSV(context.Background(), 7, []byte(csv))
	if err != nil || result != (model.ImportResult{Imported: 1, Skipped: 1, Ignored: 2}) {
		t.Fatalf("ImportRevolutCSV() = %#v, %v", result, err)
	}
}

func TestImportRevolutCSVRejectsUnknownShape(t *testing.T) {
	_, err := testService(&fakeStore{}).ImportRevolutCSV(context.Background(), 1, []byte("date,value\n2026-07-01,1\n"))
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("error = %v", err)
	}
}

func testService(store Store) *Service {
	cfg := config.Config{
		JWTSecret: strings.Repeat("s", 32), JWTIssuer: "money-manager-api",
		JWTAudience: "money-manager-mobile", JWTTTL: time.Hour,
	}
	return NewWithStore(store, cfg)
}

type fakeStore struct {
	registerUser                    func(context.Context, string, string) (model.User, error)
	findCategory                    func(context.Context, int, string, string) (string, error)
	createTransaction               func(context.Context, int, model.TransactionRequest) (model.Transaction, error)
	getTransaction                  func(context.Context, int, int) (model.Transaction, error)
	updateTransaction               func(context.Context, int, int, model.TransactionRequest) (model.Transaction, error)
	exportTransactions              func(context.Context, int, time.Time, time.Time, int) ([]model.Transaction, error)
	importTransactions              func(context.Context, int, []model.ImportedTransaction) (int, int, error)
	createOpenBankingAuthorization  func(context.Context, repository.NewOpenBankingAuthorization) (int, error)
	setOpenBankingProviderID        func(context.Context, int, string) error
	claimOpenBankingAuthorization   func(context.Context, string, time.Time) (repository.OpenBankingAuthorizationRecord, error)
	failOpenBankingAuthorization    func(context.Context, int, string, string) error
	storeOpenBankingConnection      func(context.Context, repository.NewOpenBankingConnection) (int, error)
	getOpenBankingConnection        func(context.Context, int, int) (repository.OpenBankingConnectionRecord, error)
	getOpenBankingAccount           func(context.Context, int, int) (repository.OpenBankingAccountRecord, error)
	listOpenBankingProviderSessions func(context.Context, int) ([]string, error)
	deleteUser                      func(context.Context, int) error
}

func (f *fakeStore) ImportTransactions(ctx context.Context, userID int, transactions []model.ImportedTransaction) (int, int, error) {
	if f.importTransactions != nil {
		return f.importTransactions(ctx, userID, transactions)
	}
	return 0, 0, errors.New("unexpected ImportTransactions call")
}

func (*fakeStore) Close()                     {}
func (*fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) RegisterUser(ctx context.Context, email, hash string) (model.User, error) {
	if f.registerUser != nil {
		return f.registerUser(ctx, email, hash)
	}
	return model.User{}, errors.New("unexpected RegisterUser call")
}
func (*fakeStore) FindUserByEmail(context.Context, string) (repository.UserWithPassword, error) {
	return repository.UserWithPassword{}, repository.ErrNotFound
}
func (*fakeStore) GetUser(context.Context, int) (model.User, error) {
	return model.User{}, repository.ErrNotFound
}
func (f *fakeStore) DeleteUser(ctx context.Context, userID int) error {
	if f.deleteUser != nil {
		return f.deleteUser(ctx, userID)
	}
	return repository.ErrNotFound
}
func (*fakeStore) EnsureDefaultCategories(context.Context, int) error { return nil }
func (*fakeStore) ListCategories(context.Context, int, string) ([]model.Category, error) {
	return []model.Category{}, nil
}
func (*fakeStore) CreateCategory(context.Context, int, model.CategoryRequest) (model.Category, error) {
	return model.Category{}, nil
}
func (*fakeStore) DeleteCategory(context.Context, int, int) error { return nil }
func (f *fakeStore) FindActiveCategoryName(ctx context.Context, userID int, transactionType, name string) (string, error) {
	if f.findCategory != nil {
		return f.findCategory(ctx, userID, transactionType, name)
	}
	return "", repository.ErrNotFound
}
func (*fakeStore) ListTransactions(context.Context, int, repository.TransactionFilter) ([]model.Transaction, error) {
	return []model.Transaction{}, nil
}

func (f *fakeStore) ExportTransactions(ctx context.Context, userID int, from, to time.Time, limit int) ([]model.Transaction, error) {
	if f.exportTransactions != nil {
		return f.exportTransactions(ctx, userID, from, to, limit)
	}
	return []model.Transaction{}, nil
}
func (f *fakeStore) CreateTransaction(ctx context.Context, userID int, request model.TransactionRequest) (model.Transaction, error) {
	if f.createTransaction != nil {
		return f.createTransaction(ctx, userID, request)
	}
	return model.Transaction{}, errors.New("unexpected CreateTransaction call")
}
func (f *fakeStore) GetTransaction(ctx context.Context, userID, transactionID int) (model.Transaction, error) {
	if f.getTransaction != nil {
		return f.getTransaction(ctx, userID, transactionID)
	}
	return model.Transaction{}, repository.ErrNotFound
}
func (f *fakeStore) UpdateTransaction(ctx context.Context, userID, transactionID int, request model.TransactionRequest) (model.Transaction, error) {
	if f.updateTransaction != nil {
		return f.updateTransaction(ctx, userID, transactionID, request)
	}
	return model.Transaction{}, nil
}
func (*fakeStore) DeleteTransaction(context.Context, int, int) error { return nil }
func (*fakeStore) Summary(context.Context, int, string, time.Time, time.Time) (model.Summary, error) {
	return model.Summary{}, nil
}
func (f *fakeStore) CreateOpenBankingAuthorization(ctx context.Context, record repository.NewOpenBankingAuthorization) (int, error) {
	if f.createOpenBankingAuthorization != nil {
		return f.createOpenBankingAuthorization(ctx, record)
	}
	return 0, errors.New("unexpected CreateOpenBankingAuthorization call")
}
func (f *fakeStore) SetOpenBankingAuthorizationProviderID(ctx context.Context, authorizationID int, providerID string) error {
	if f.setOpenBankingProviderID != nil {
		return f.setOpenBankingProviderID(ctx, authorizationID, providerID)
	}
	return errors.New("unexpected SetOpenBankingAuthorizationProviderID call")
}
func (f *fakeStore) ClaimOpenBankingAuthorization(ctx context.Context, stateHash string, now time.Time) (repository.OpenBankingAuthorizationRecord, error) {
	if f.claimOpenBankingAuthorization != nil {
		return f.claimOpenBankingAuthorization(ctx, stateHash, now)
	}
	return repository.OpenBankingAuthorizationRecord{}, repository.ErrNotFound
}
func (f *fakeStore) FailOpenBankingAuthorization(ctx context.Context, authorizationID int, code, description string) error {
	if f.failOpenBankingAuthorization != nil {
		return f.failOpenBankingAuthorization(ctx, authorizationID, code, description)
	}
	return nil
}
func (f *fakeStore) StoreOpenBankingConnection(ctx context.Context, record repository.NewOpenBankingConnection) (int, error) {
	if f.storeOpenBankingConnection != nil {
		return f.storeOpenBankingConnection(ctx, record)
	}
	return 0, errors.New("unexpected StoreOpenBankingConnection call")
}
func (*fakeStore) ListOpenBankingConnections(context.Context, int) ([]model.OpenBankingConnection, error) {
	return []model.OpenBankingConnection{}, nil
}
func (f *fakeStore) GetOpenBankingConnection(ctx context.Context, userID, connectionID int) (repository.OpenBankingConnectionRecord, error) {
	if f.getOpenBankingConnection != nil {
		return f.getOpenBankingConnection(ctx, userID, connectionID)
	}
	return repository.OpenBankingConnectionRecord{}, repository.ErrNotFound
}
func (*fakeStore) UpdateOpenBankingConnectionStatus(context.Context, int, int, string) error {
	return nil
}
func (*fakeStore) DeleteOpenBankingConnection(context.Context, int, int) error { return nil }
func (*fakeStore) ListOpenBankingAccounts(context.Context, int) ([]model.OpenBankingAccount, error) {
	return []model.OpenBankingAccount{}, nil
}
func (f *fakeStore) GetOpenBankingAccount(ctx context.Context, userID, accountID int) (repository.OpenBankingAccountRecord, error) {
	if f.getOpenBankingAccount != nil {
		return f.getOpenBankingAccount(ctx, userID, accountID)
	}
	return repository.OpenBankingAccountRecord{}, repository.ErrNotFound
}
func (f *fakeStore) ListOpenBankingProviderSessions(ctx context.Context, userID int) ([]string, error) {
	if f.listOpenBankingProviderSessions != nil {
		return f.listOpenBankingProviderSessions(ctx, userID)
	}
	return []string{}, nil
}
