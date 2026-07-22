package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/config"
	"money-manager-server/internal/marketdata"
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

func TestCreateInvestmentTransferExcludesSpendingAndLinksRevolutXSchedule(t *testing.T) {
	scheduleID := 42
	store := &fakeStore{
		findCategory: func(context.Context, int, string, string) (string, error) {
			return "investment_transfer", nil
		},
		getInvestmentSchedule: func(context.Context, int, int) (model.InvestmentSchedule, error) {
			return model.InvestmentSchedule{ID: scheduleID, Broker: "revolut_x"}, nil
		},
		createTransaction: func(_ context.Context, _ int, request model.TransactionRequest) (model.Transaction, error) {
			if request.Purpose != "investment_transfer" || !request.ExcludedFromBudget {
				t.Fatalf("investment transfer was not normalized: %#v", request)
			}
			if request.InvestmentScheduleID == nil || *request.InvestmentScheduleID != scheduleID {
				t.Fatalf("investment schedule link = %#v", request.InvestmentScheduleID)
			}
			return model.Transaction{ID: 1, Purpose: request.Purpose}, nil
		},
	}
	service := testService(store)
	transaction, err := service.CreateTransaction(context.Background(), 1, model.TransactionRequest{
		Type: "expense", Category: "investment_transfer", Amount: "25", Currency: "EUR",
		OccurredAt: "2026-07-18", Purpose: "investment_transfer", InvestmentScheduleID: &scheduleID,
	})
	if err != nil || transaction.Purpose != "investment_transfer" {
		t.Fatalf("CreateTransaction() = %#v, %v", transaction, err)
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
			if transactionType == "expense" && name != "dining_out" {
				t.Fatalf("expense category name = %q", name)
			}
			if transactionType == "income" && name != "other" {
				t.Fatalf("income category name = %q", name)
			}
			return name, nil
		},
		importTransactions: func(_ context.Context, userID int, transactions []model.ImportedTransaction) (int, int, error) {
			if userID != 7 || len(transactions) != 2 {
				t.Fatalf("import user/rows = %d/%d", userID, len(transactions))
			}
			expense := transactions[0]
			if expense.Request.Type != "expense" || expense.Request.Category != "dining_out" || expense.Request.Amount != "12.34" || expense.Request.OccurredAt != "2026-07-11" || expense.Request.Description != "Coffee Shop" {
				t.Fatalf("expense = %#v", expense)
			}
			income := transactions[1]
			if income.Request.Type != "income" || income.Request.Category != "other" || income.Request.Amount != "50.00" {
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
		"TOPUP,Current,2026-07-12 09:30:00,2026-07-12 09:30:00,Top up by bank card,500,0,EUR,COMPLETED,650\n" +
		"TOPUP_RETURN,Current,2026-07-12 09:35:00,2026-07-12 09:35:00,Reverted top up,-500,0,EUR,COMPLETED,150\n" +
		"CARD_PAYMENT,Current,2026-07-12 10:00:00,,Pending Shop,-4,0,EUR,PENDING,146\n" +
		"CARD_PAYMENT,Current,2026-07-12 11:00:00,2026-07-12 11:00:00,Dollar Shop,-5,0,USD,COMPLETED,141\n"

	result, err := testService(store).ImportRevolutCSV(context.Background(), 7, []byte(csv))
	if err != nil || result != (model.ImportResult{Imported: 1, Skipped: 1, Ignored: 4}) {
		t.Fatalf("ImportRevolutCSV() = %#v, %v", result, err)
	}
}

func TestImportRevolutCSVTopUpRuleKeepsSalaryTransfers(t *testing.T) {
	store := &fakeStore{
		findCategory: func(_ context.Context, _ int, transactionType, name string) (string, error) {
			if transactionType != "income" || name != "salary" {
				t.Fatalf("category lookup = %q/%q", transactionType, name)
			}
			return name, nil
		},
		importTransactions: func(_ context.Context, _ int, transactions []model.ImportedTransaction) (int, int, error) {
			if len(transactions) != 1 || transactions[0].Request.Description != "Monthly salary" {
				t.Fatalf("transactions = %#v", transactions)
			}
			return 1, 0, nil
		},
	}
	csv := "Type,Started Date,Completed Date,Description,Amount,Currency,State\n" +
		"CARD_TOP_UP,2026-07-01 08:00:00,2026-07-01 08:00:00,Card top up,500,EUR,COMPLETED\n" +
		"TOPUP_RETURN,2026-07-01 08:30:00,2026-07-01 08:30:00,Top up return,-500,EUR,COMPLETED\n" +
		"TRANSFER,2026-07-01 09:00:00,2026-07-01 09:00:00,Monthly salary,3000,EUR,COMPLETED\n"

	result, err := testService(store).ImportRevolutCSV(context.Background(), 7, []byte(csv))
	if err != nil || result != (model.ImportResult{Imported: 1, Ignored: 2}) {
		t.Fatalf("ImportRevolutCSV() = %#v, %v", result, err)
	}
}

func TestImportRevolutCSVUsesMobileCategoryWithoutChangingFingerprint(t *testing.T) {
	var fingerprints []string
	var categories []string
	store := &fakeStore{
		findCategory: func(_ context.Context, _ int, transactionType, name string) (string, error) {
			if transactionType != "expense" || (name != "other" && name != "shopping") {
				t.Fatalf("category lookup = %q/%q", transactionType, name)
			}
			return name, nil
		},
		importTransactions: func(_ context.Context, _ int, transactions []model.ImportedTransaction) (int, int, error) {
			if len(transactions) != 1 {
				t.Fatalf("transactions = %#v", transactions)
			}
			fingerprints = append(fingerprints, transactions[0].Fingerprint)
			categories = append(categories, transactions[0].Request.Category)
			return 1, 0, nil
		},
	}
	baseHeader := "Type,Started Date,Completed Date,Description,Amount,Currency,State"
	baseRow := "CARD_PAYMENT,2026-07-11 10:00:00,2026-07-11 10:01:00,Unfamiliar Store,-12.34,EUR,COMPLETED"
	if _, err := testService(store).ImportRevolutCSV(context.Background(), 7, []byte(baseHeader+"\n"+baseRow+"\n")); err != nil {
		t.Fatalf("base import: %v", err)
	}
	annotated := baseHeader + ",Money Manager Category\n" + baseRow + ",shopping\n"
	if _, err := testService(store).ImportRevolutCSV(context.Background(), 7, []byte(annotated)); err != nil {
		t.Fatalf("annotated import: %v", err)
	}
	if len(fingerprints) != 2 || fingerprints[0] != fingerprints[1] {
		t.Fatalf("fingerprints = %#v", fingerprints)
	}
	if len(categories) != 2 || categories[0] != "other" || categories[1] != "shopping" {
		t.Fatalf("categories = %#v", categories)
	}
}

func TestImportRevolutCSVRejectsUnknownShape(t *testing.T) {
	_, err := testService(&fakeStore{}).ImportRevolutCSV(context.Background(), 1, []byte("date,value\n2026-07-01,1\n"))
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateTransactionScheduleNormalizesAndMaterializesMonthlyDates(t *testing.T) {
	now := time.Date(2026, 1, 30, 10, 0, 0, 0, time.UTC)
	var seeds []repository.ScheduleOccurrenceSeed
	var materializedThrough time.Time
	store := &fakeStore{
		findCategory: func(_ context.Context, userID int, transactionType, category string) (string, error) {
			if userID != 7 || transactionType != "expense" || category != "housing" {
				t.Fatalf("category lookup = %d, %q, %q", userID, transactionType, category)
			}
			return "Housing", nil
		},
		createTransactionSchedule: func(_ context.Context, userID int, request model.TransactionScheduleRequest) (model.TransactionSchedule, error) {
			if userID != 7 || request.Name != "Rent" || request.Amount != "1250.00" || request.Currency != "EUR" {
				t.Fatalf("normalized schedule = %d, %#v", userID, request)
			}
			if request.Frequency != "monthly" || request.FrequencyInterval != 1 || request.DayOfMonth == nil || *request.DayOfMonth != 31 {
				t.Fatalf("normalized recurrence = %#v", request)
			}
			return model.TransactionSchedule{
				ID: 11, UserID: userID, Type: request.Type, Name: request.Name, Category: request.Category,
				Description: request.Description, Amount: request.Amount, Currency: request.Currency,
				Frequency: request.Frequency, FrequencyInterval: request.FrequencyInterval,
				StartDate: request.StartDate, EndDate: request.EndDate, DayOfMonth: request.DayOfMonth,
				Timezone: request.Timezone, AutoPost: request.AutoPost, Status: "active",
			}, nil
		},
		upsertScheduleOccurrences: func(_ context.Context, value []repository.ScheduleOccurrenceSeed) (int, error) {
			seeds = append([]repository.ScheduleOccurrenceSeed(nil), value...)
			return len(value), nil
		},
		markScheduleMaterialized: func(_ context.Context, scheduleID int, through time.Time) error {
			if scheduleID != 11 {
				t.Fatalf("materialized schedule id = %d", scheduleID)
			}
			materializedThrough = through
			return nil
		},
		getTransactionSchedule: func(_ context.Context, userID, scheduleID int, _ time.Time) (model.TransactionSchedule, error) {
			return model.TransactionSchedule{ID: scheduleID, UserID: userID, Name: "Rent", Status: "active"}, nil
		},
	}
	service := testService(store)
	service.now = func() time.Time { return now }

	schedule, err := service.CreateTransactionSchedule(context.Background(), 7, model.TransactionScheduleRequest{
		Type: " Expense ", Name: " Rent ", Category: "housing", Amount: "1250",
		Frequency: "MONTHLY", StartDate: "2026-01-31", Timezone: "Europe/Sofia", AutoPost: true,
	})
	if err != nil || schedule.ID != 11 {
		t.Fatalf("CreateTransactionSchedule() = %#v, %v", schedule, err)
	}
	wantDates := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"}
	if len(seeds) != len(wantDates) {
		t.Fatalf("occurrence count = %d, seeds = %#v", len(seeds), seeds)
	}
	for index, want := range wantDates {
		if got := seeds[index].ScheduledFor.Format("2006-01-02"); got != want || seeds[index].UserID != 7 {
			t.Fatalf("occurrence %d = %q for user %d, want %q for user 7", index, got, seeds[index].UserID, want)
		}
	}
	if got := materializedThrough.Format("2006-01-02"); got != "2026-04-30" {
		t.Fatalf("materialized through = %q", got)
	}
}

func TestCreateTransactionScheduleRejectsInvalidRecurrence(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	dayOfWeek := 1
	tests := []model.TransactionScheduleRequest{
		{Type: "expense", Name: "Past", Category: "Housing", Amount: "1", Frequency: "monthly", StartDate: "2026-07-12"},
		{Type: "expense", Name: "Mixed", Category: "Housing", Amount: "1", Frequency: "monthly", StartDate: "2026-07-14", DayOfWeek: &dayOfWeek},
		{Type: "expense", Name: "Bad zone", Category: "Housing", Amount: "1", Frequency: "daily", StartDate: "2026-07-14", Timezone: "Mars/Olympus"},
	}
	for _, request := range tests {
		service := testService(&fakeStore{findCategory: func(context.Context, int, string, string) (string, error) {
			return "Housing", nil
		}})
		service.now = func() time.Time { return now }
		if _, err := service.CreateTransactionSchedule(context.Background(), 1, request); apperrors.KindOf(err) != apperrors.KindValidation {
			t.Errorf("request %#v error = %v", request, err)
		}
	}
}

func TestListTransactionScheduleOccurrencesDefaultsToPlanned(t *testing.T) {
	store := &fakeStore{
		listScheduleOccurrences: func(_ context.Context, userID int, filter repository.ScheduleOccurrenceFilter) ([]model.TransactionScheduleOccurrence, error) {
			if userID != 7 {
				t.Fatalf("user id = %d", userID)
			}
			if filter.Status != "planned" {
				t.Fatalf("status = %q, want planned", filter.Status)
			}
			return []model.TransactionScheduleOccurrence{}, nil
		},
	}

	_, err := testService(store).ListTransactionScheduleOccurrences(
		context.Background(), 7, "2026-07-15", "2026-10-13", 0, "",
	)
	if err != nil {
		t.Fatalf("ListTransactionScheduleOccurrences() error = %v", err)
	}
}

func TestListTransactionScheduleOccurrencesKeepsExplicitStatus(t *testing.T) {
	store := &fakeStore{
		listScheduleOccurrences: func(_ context.Context, _ int, filter repository.ScheduleOccurrenceFilter) ([]model.TransactionScheduleOccurrence, error) {
			if filter.Status != "skipped" {
				t.Fatalf("status = %q, want skipped", filter.Status)
			}
			return []model.TransactionScheduleOccurrence{}, nil
		},
	}

	_, err := testService(store).ListTransactionScheduleOccurrences(
		context.Background(), 7, "2026-07-15", "2026-10-13", 0, " SKIPPED ",
	)
	if err != nil {
		t.Fatalf("ListTransactionScheduleOccurrences() error = %v", err)
	}
}

func TestCalculateInvestmentPortfolioTracksAverageCostAndProfit(t *testing.T) {
	portfolio, err := calculateInvestmentPortfolio([]model.InvestmentTrade{
		{ID: 3, AssetType: "stock", Symbol: "ACME", AssetName: "Acme", Broker: "trading212", Side: "sell", Quantity: "1", PricePerUnit: "150", Fees: "2", OccurredAt: "2026-03-01"},
		{ID: 1, AssetType: "stock", Symbol: "ACME", AssetName: "Acme", Broker: "trading212", Side: "buy", Quantity: "2", PricePerUnit: "100", Fees: "2", OccurredAt: "2026-01-01"},
		{ID: 2, AssetType: "stock", Symbol: "ACME", AssetName: "Acme", Broker: "trading212", Side: "buy", Quantity: "1", PricePerUnit: "130", Fees: "1", OccurredAt: "2026-02-01"},
	}, []model.InvestmentPrice{{AssetType: "stock", Symbol: "ACME", Price: "160", AsOf: "2026-07-13T10:00:00Z"}})
	if err != nil {
		t.Fatalf("calculateInvestmentPortfolio() error = %v", err)
	}
	if len(portfolio.Positions) != 1 {
		t.Fatalf("positions = %#v", portfolio.Positions)
	}
	position := portfolio.Positions[0]
	if position.Quantity != "2" || position.AverageCost != "111.00000000" || position.InvestedAmount != "222.00" {
		t.Fatalf("position cost = %#v", position)
	}
	if position.CurrentValue != "320.00" || position.UnrealizedProfit != "98.00" || position.RealizedProfit != "37.00" {
		t.Fatalf("position profit = %#v", position)
	}
	if portfolio.CurrentValue != "320.00" || portfolio.UnrealizedProfit != "98.00" || portfolio.RealizedProfit != "37.00" || portfolio.MissingPrices != 0 {
		t.Fatalf("portfolio totals = %#v", portfolio)
	}
}

func TestCalculateInvestmentPortfolioDoesNotRequirePriceForClosedPosition(t *testing.T) {
	portfolio, err := calculateInvestmentPortfolio([]model.InvestmentTrade{
		{ID: 1, AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "revolut_x", Side: "buy", Quantity: "0.1", PricePerUnit: "50000", Fees: "1", OccurredAt: "2026-01-01"},
		{ID: 2, AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "revolut_x", Side: "sell", Quantity: "0.1", PricePerUnit: "60000", Fees: "1", OccurredAt: "2026-02-01"},
	}, nil)
	if err != nil {
		t.Fatalf("calculateInvestmentPortfolio() error = %v", err)
	}
	if portfolio.MissingPrices != 0 || portfolio.Positions[0].PriceStatus != "not_required" || portfolio.RealizedProfit != "998.00" {
		t.Fatalf("closed portfolio = %#v", portfolio)
	}
}

func TestInvestmentPortfolioKeepsLedgerWhenStockPricingIsUnavailable(t *testing.T) {
	store := &fakeStore{listInvestmentTrades: func(
		_ context.Context, userID int, _ repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		if userID != 7 {
			t.Fatalf("user ID = %d", userID)
		}
		return []model.InvestmentTrade{{
			ID: 1, AssetType: "stock", Symbol: "QDVE", AssetName: "iShares S&P 500 IT",
			Exchange: "XETRA", MarketCurrency: "EUR", Broker: "trading212", Side: "buy",
			Amount: "100.00", Quantity: "2", PricePerUnit: "50", Fees: "1.00",
			OccurredAt: "2026-07-17T07:05:10Z",
		}}, nil
	}}
	service := testService(store)
	service.stockMarketData = &fakeStockInvestmentMarketData{currentQuote: func(
		context.Context, marketdata.EquityInstrument, string,
	) (investmentMarketQuote, error) {
		return investmentMarketQuote{}, errors.New("provider quota exhausted")
	}}

	portfolio, err := service.InvestmentPortfolio(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if portfolio.InvestedAmount != "101.00" || portfolio.CurrentValue != "" ||
		portfolio.UnrealizedProfit != "" || portfolio.MissingPrices != 1 {
		t.Fatalf("portfolio = %#v", portfolio)
	}
	if len(portfolio.Positions) != 1 || portfolio.Positions[0].Symbol != "QDVE" ||
		portfolio.Positions[0].Quantity != "2" || portfolio.Positions[0].PriceStatus != "missing" {
		t.Fatalf("positions = %#v", portfolio.Positions)
	}
}

func TestInvestmentPortfolioKeepsStockLedgerWhenProviderIsNotConfigured(t *testing.T) {
	store := &fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		return []model.InvestmentTrade{{
			ID: 1, AssetType: "stock", Symbol: "4GLD", AssetName: "Xetra-Gold",
			Exchange: "XETRA", MarketCurrency: "EUR", Broker: "trading212", Side: "buy",
			Amount: "20.00", Quantity: "0.16673614", PricePerUnit: "119.95", Fees: "0",
			OccurredAt: "2026-06-17T07:05:06Z",
		}}, nil
	}}
	service := testService(store)
	service.stockMarketData = nil

	portfolio, err := service.InvestmentPortfolio(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if portfolio.InvestedAmount != "20.00" || portfolio.MissingPrices != 1 || len(portfolio.Positions) != 1 {
		t.Fatalf("portfolio = %#v", portfolio)
	}
}

func TestInvestmentDecimalAndIdentityValidation(t *testing.T) {
	if value, err := normalizeUnsignedDecimal("000.0100000000", "quantity", 18, 10, false); err != nil || value != "0.01" {
		t.Fatalf("normalized quantity = %q, %v", value, err)
	}
	for _, value := range []string{"0", "-1", "1.00000000001", "1e2", ".5"} {
		if _, err := normalizeUnsignedDecimal(value, "quantity", 18, 10, false); apperrors.KindOf(err) != apperrors.KindValidation {
			t.Errorf("quantity %q error = %v", value, err)
		}
	}
	if _, _, _, _, err := normalizeInvestmentIdentity("crypto", "SOL", "Solana", "revolut_x"); apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("unsupported crypto error = %v", err)
	}
	if _, _, _, _, err := normalizeInvestmentIdentity("stock", "AAPL", "Apple", "revolut_x"); apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("stock broker error = %v", err)
	}
	service := testService(&fakeStore{})
	service.now = func() time.Time { return time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC) }
	legacy, err := service.validateInvestmentTrade(model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "BTC", Broker: "manual", Side: "buy",
		Quantity: "0.002", PricePerUnit: "50000", OccurredAt: "2026-07-14",
	})
	if err != nil || legacy.Amount != "100.00" {
		t.Fatalf("legacy trade amount = %#v, %v", legacy, err)
	}
}

func TestInvestmentTimestampsRejectAnyFutureInstant(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	service := testService(&fakeStore{})
	service.now = func() time.Time { return now }

	_, err := service.CreateInvestmentTrade(context.Background(), 7, model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "BTC", Broker: "manual", Side: "buy", Amount: "25",
		OccurredAt: now.Add(500 * time.Millisecond).Format(time.RFC3339Nano),
	})
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("future trade error = %v", err)
	}

	_, err = service.SetManualInvestmentPrice(context.Background(), 7, model.InvestmentPriceRequest{
		AssetType: "stock", Symbol: "ACME", Price: "10", AsOf: now.Add(500 * time.Millisecond).Format(time.RFC3339Nano),
	})
	if apperrors.KindOf(err) != apperrors.KindValidation {
		t.Fatalf("future manual price error = %v", err)
	}

	if _, err := service.validateInvestmentTrade(model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "ETH", Broker: "manual", Side: "buy", Amount: "25",
		OccurredAt: now.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("trade at current instant error = %v", err)
	}
}

func TestManualInvestmentPriceIsRestrictedToLegacyStocks(t *testing.T) {
	service := testService(&fakeStore{})

	_, err := service.SetManualInvestmentPrice(context.Background(), 7, model.InvestmentPriceRequest{
		AssetType: "crypto", Symbol: "BTC", Price: "50000",
	})
	if apperrors.KindOf(err) != apperrors.KindValidation || !strings.Contains(err.Error(), "provided automatically") {
		t.Fatalf("crypto manual price error = %v", err)
	}

	_, err = service.SetManualInvestmentPrice(context.Background(), 7, model.InvestmentPriceRequest{
		AssetType: "stock", Symbol: "ACME", Price: "10",
	})
	if apperrors.KindOf(err) != apperrors.KindNotFound {
		t.Fatalf("legacy stock manual price error = %v", err)
	}
}

func TestCreateInvestmentTradeDerivesQuantityFromEuroAmount(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	quotedAt := now.Add(-2 * time.Minute)
	var stored model.InvestmentTradeRequest
	store := &fakeStore{createInvestmentTrade: func(
		_ context.Context, userID int, request model.InvestmentTradeRequest,
	) (model.InvestmentTrade, error) {
		if userID != 7 {
			t.Fatalf("user ID = %d", userID)
		}
		stored = request
		return model.InvestmentTrade{
			ID: 9, AssetType: request.AssetType, Symbol: request.Symbol, Amount: request.Amount,
			Quantity: request.Quantity, PricePerUnit: request.PricePerUnit,
			PriceProvider: request.PriceProvider, PriceAsOf: request.PriceAsOf,
		}, nil
	}}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.marketData = &fakeInvestmentMarketData{quoteAt: func(
		_ context.Context, symbol, currency string, at time.Time,
	) (investmentMarketQuote, error) {
		if symbol != "BTC" || currency != "EUR" || !at.Equal(now.Add(-time.Hour)) {
			t.Fatalf("quote request = %s/%s at %s", symbol, currency, at)
		}
		return investmentMarketQuote{Price: "50000.00000", Provider: "kraken", AsOf: quotedAt}, nil
	}}
	trade, err := service.CreateInvestmentTrade(context.Background(), 7, model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "btc", AssetName: "ignored", Broker: "revolut_x",
		Side: "buy", Amount: "100", Fees: "1", Currency: "EUR",
		OccurredAt: now.Add(-time.Hour).Format(time.RFC3339), Notes: "Monthly buy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if trade.ID != 9 || stored.Amount != "100.00" || stored.Quantity != "0.002" || stored.PricePerUnit != "50000" {
		t.Fatalf("derived trade = %#v, stored = %#v", trade, stored)
	}
	if stored.PriceProvider != "kraken" || stored.PriceAsOf != quotedAt.Format(time.RFC3339) || stored.AssetName != "Bitcoin" {
		t.Fatalf("quote audit fields = %#v", stored)
	}
}

func TestCreateInvestmentTradeDoesNotInsertWithoutMarketPrice(t *testing.T) {
	inserted := false
	store := &fakeStore{createInvestmentTrade: func(
		context.Context, int, model.InvestmentTradeRequest,
	) (model.InvestmentTrade, error) {
		inserted = true
		return model.InvestmentTrade{}, nil
	}}
	service := testService(store)
	service.marketData = &fakeInvestmentMarketData{quoteAt: func(
		context.Context, string, string, time.Time,
	) (investmentMarketQuote, error) {
		return investmentMarketQuote{}, errors.New("provider unavailable")
	}}
	_, err := service.CreateInvestmentTrade(context.Background(), 7, model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "BTC", Broker: "manual", Side: "buy", Amount: "25",
		OccurredAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	})
	if apperrors.KindOf(err) != apperrors.KindUnavailable || inserted {
		t.Fatalf("error = %v, inserted = %v", err, inserted)
	}
}

func TestCreateStockTradeUsesTrading212ListingAndCurrency(t *testing.T) {
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)
	var stored model.InvestmentTradeRequest
	store := &fakeStore{createInvestmentTrade: func(
		_ context.Context, _ int, request model.InvestmentTradeRequest,
	) (model.InvestmentTrade, error) {
		stored = request
		return model.InvestmentTrade{ID: 12, AssetType: request.AssetType, Symbol: request.Symbol}, nil
	}}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.stockMarketData = &fakeStockInvestmentMarketData{quoteAt: func(
		_ context.Context, instrument marketdata.EquityInstrument, currency string, at time.Time,
	) (investmentMarketQuote, error) {
		if instrument.Symbol != "AAPL" || instrument.Exchange != "NASDAQ" || instrument.MarketCurrency != "USD" || currency != "EUR" {
			t.Fatalf("instrument = %#v, currency = %s", instrument, currency)
		}
		return investmentMarketQuote{Price: "200", Provider: "trading212", AsOf: at}, nil
	}}
	_, err := service.CreateInvestmentTrade(context.Background(), 7, model.InvestmentTradeRequest{
		AssetType: "stock", Symbol: "aapl", AssetName: "Apple", Exchange: "nasdaq",
		MarketCurrency: "usd", Broker: "trading212", Side: "buy", Amount: "100",
		OccurredAt: now.Add(-time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Exchange != "NASDAQ" || stored.MarketCurrency != "USD" || stored.Quantity != "0.5" || stored.PriceProvider != "trading212" {
		t.Fatalf("stored = %#v", stored)
	}
}

func TestCreateStockTradeRejectsNonTrading212Owner(t *testing.T) {
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)
	inserted := false
	store := &fakeStore{createInvestmentTrade: func(
		context.Context, int, model.InvestmentTradeRequest,
	) (model.InvestmentTrade, error) {
		inserted = true
		return model.InvestmentTrade{}, nil
	}}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.stockMarketData = &fakeStockInvestmentMarketData{quoteAt: func(
		context.Context, marketdata.EquityInstrument, string, time.Time,
	) (investmentMarketQuote, error) {
		t.Fatal("non-owner request reached Trading 212")
		return investmentMarketQuote{}, nil
	}}

	_, err := service.CreateInvestmentTrade(context.Background(), 8, model.InvestmentTradeRequest{
		AssetType: "stock", Symbol: "AAPL", AssetName: "Apple", Exchange: "NASDAQ",
		MarketCurrency: "USD", Broker: "trading212", Side: "buy", Amount: "100",
		OccurredAt: now.Add(-time.Hour).Format(time.RFC3339),
	})
	if apperrors.KindOf(err) != apperrors.KindUnavailable || inserted ||
		!strings.Contains(err.Error(), "not available for this account") {
		t.Fatalf("error = %v, inserted = %v", err, inserted)
	}
}

func TestInvestmentPortfolioDoesNotUseTrading212ForNonOwner(t *testing.T) {
	store := &fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		return []model.InvestmentTrade{{
			ID: 1, AssetType: "stock", Symbol: "QDVE", AssetName: "iShares S&P 500 IT",
			Exchange: "XETRA", MarketCurrency: "EUR", Broker: "trading212", Side: "buy",
			Amount: "100.00", Quantity: "2", PricePerUnit: "50", Fees: "0",
			OccurredAt: "2026-07-16T10:00:00Z",
		}}, nil
	}}
	service := testService(store)
	service.stockMarketData = &fakeStockInvestmentMarketData{currentQuote: func(
		context.Context, marketdata.EquityInstrument, string,
	) (investmentMarketQuote, error) {
		t.Fatal("non-owner request reached Trading 212")
		return investmentMarketQuote{}, nil
	}}

	portfolio, err := service.InvestmentPortfolio(context.Background(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(portfolio.Positions) != 1 || portfolio.Positions[0].PriceStatus != "missing" || portfolio.MissingPrices != 1 {
		t.Fatalf("portfolio = %#v", portfolio)
	}
}

func TestDeleteInvestmentTradeMapsAtomicRepositoryResult(t *testing.T) {
	for _, test := range []struct {
		name string
		repo error
		kind apperrors.Kind
	}{
		{name: "deleted", kind: ""},
		{name: "missing", repo: repository.ErrNotFound, kind: apperrors.KindNotFound},
		{name: "depended on", repo: repository.ErrConflict, kind: apperrors.KindConflict},
		{name: "database error", repo: errors.New("database unavailable"), kind: apperrors.KindInternal},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			service := testService(&fakeStore{deleteInvestmentTrade: func(
				_ context.Context, userID, tradeID int,
			) error {
				called = true
				if userID != 7 || tradeID != 9 {
					t.Fatalf("delete arguments = %d/%d", userID, tradeID)
				}
				return test.repo
			}})
			err := service.DeleteInvestmentTrade(context.Background(), 7, 9)
			if !called {
				t.Fatal("repository delete was not called")
			}
			if test.kind == "" {
				if err != nil {
					t.Fatalf("delete error = %v", err)
				}
				return
			}
			if apperrors.KindOf(err) != test.kind {
				t.Fatalf("delete error kind = %q, want %q: %v", apperrors.KindOf(err), test.kind, err)
			}
		})
	}
}

func TestInvestmentPortfolioUsesLiveCryptoQuotesAndExcludesStocks(t *testing.T) {
	store := &fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		return []model.InvestmentTrade{
			{ID: 1, AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "manual", Side: "buy", Amount: "100.00", Quantity: "0.002", PricePerUnit: "50000", Fees: "0", OccurredAt: "2026-07-01T10:00:00Z"},
			{ID: 2, AssetType: "stock", Symbol: "AAPL", AssetName: "Apple", Broker: "trading212", Side: "buy", Amount: "50.00", Quantity: "0.25", PricePerUnit: "200", Fees: "0", OccurredAt: "2026-07-01T10:00:00Z"},
		}, nil
	}}
	service := testService(store)
	service.marketData = &fakeInvestmentMarketData{currentQuotes: func(
		_ context.Context, symbols []string, currency string,
	) (map[string]investmentMarketQuote, error) {
		if len(symbols) != 1 || symbols[0] != "BTC" || currency != "EUR" {
			t.Fatalf("current quote request = %#v/%s", symbols, currency)
		}
		return map[string]investmentMarketQuote{"BTC": {
			Price: "60000", Provider: "kraken", AsOf: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
		}}, nil
	}}
	portfolio, err := service.InvestmentPortfolio(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(portfolio.Positions) != 1 || portfolio.Positions[0].Symbol != "BTC" ||
		portfolio.CurrentValue != "120.00" || portfolio.InvestedAmount != "100.00" ||
		portfolio.UnsupportedPositions != 1 || portfolio.Positions[0].PriceProvider != "kraken" {
		t.Fatalf("portfolio = %#v", portfolio)
	}
}

func TestInvestmentPortfolioHistoryValuesDailyHoldingsAndCurrentPrice(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		return []model.InvestmentTrade{
			{ID: 2, AssetType: "crypto", Symbol: "BTC", Broker: "manual", Side: "sell", Amount: "30.00", Quantity: "0.0005", PricePerUnit: "60000", Fees: "0", OccurredAt: "2026-07-13T10:00:00Z"},
			{ID: 1, AssetType: "crypto", Symbol: "BTC", Broker: "manual", Side: "buy", Amount: "100.00", Quantity: "0.002", PricePerUnit: "50000", Fees: "0", OccurredAt: "2026-07-12T10:00:00Z"},
		}, nil
	}}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.marketData = &fakeInvestmentMarketData{
		dailyHistory: func(context.Context, string, string, time.Time) ([]investmentMarketHistoryPoint, error) {
			return []investmentMarketHistoryPoint{
				{Price: "49000", AsOf: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)},
				{Price: "50000", AsOf: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)},
				{Price: "60000", AsOf: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)},
				{Price: "70000", AsOf: time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)},
			}, nil
		},
		currentQuotes: func(context.Context, []string, string) (map[string]investmentMarketQuote, error) {
			return map[string]investmentMarketQuote{"BTC": {Price: "72000", Provider: "kraken", AsOf: now}}, nil
		},
	}
	history, err := service.InvestmentPortfolioHistory(context.Background(), 7, "1y")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Points) != 3 || history.Points[0].Value != "100.00" ||
		history.Points[1].Value != "90.00" || history.Points[2].Value != "108.00" ||
		history.Points[2].InvestedAmount != "75.00" ||
		history.Points[0].AsOf != "2026-07-12T23:59:59Z" ||
		history.Points[1].AsOf != "2026-07-13T23:59:59Z" ||
		history.Points[2].AsOf != now.Format(time.RFC3339) {
		t.Fatalf("history = %#v", history)
	}
	if len(history.Points[2].Holdings) != 1 || history.Points[2].Holdings[0].Symbol != "BTC" ||
		history.Points[2].Holdings[0].Value != "108.00" {
		t.Fatalf("holding history = %#v", history.Points[2].Holdings)
	}
}

func TestInvestmentPortfolioHistoryIncludesAndCachesMarketstackStocks(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	var cached []model.InvestmentMarketHistoryPrice
	store := &fakeStore{
		listInvestmentTrades: func(context.Context, int, repository.InvestmentTradeFilter) ([]model.InvestmentTrade, error) {
			return []model.InvestmentTrade{{
				ID: 1, AssetType: "stock", Symbol: "QDVE", AssetName: "iShares S&P 500 IT",
				Exchange: "XETRA", MarketCurrency: "EUR", Broker: "trading212", Side: "buy",
				Amount: "100.00", Quantity: "2", PricePerUnit: "50", Fees: "0",
				OccurredAt: "2026-07-16T10:00:00Z",
			}}, nil
		},
		listInvestmentMarketHistory: func(
			context.Context, string, string, string, string, time.Time,
		) ([]model.InvestmentMarketHistoryPrice, error) {
			return append([]model.InvestmentMarketHistoryPrice(nil), cached...), nil
		},
		upsertInvestmentMarketHistory: func(_ context.Context, prices []model.InvestmentMarketHistoryPrice) error {
			cached = append(cached, prices...)
			return nil
		},
	}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.stockHistoryData = &fakeStockInvestmentHistory{dailyHistory: func(
		_ context.Context, instrument marketdata.EquityInstrument, currency string, since time.Time,
	) ([]investmentMarketHistoryPoint, error) {
		if instrument.Symbol != "QDVE" || instrument.Exchange != "XETRA" || currency != "EUR" ||
			since.Format("2006-01-02") != "2026-07-15" {
			t.Fatalf("history request = %#v/%s/%s", instrument, currency, since)
		}
		return []investmentMarketHistoryPoint{
			{Price: "50", AsOf: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)},
			{Price: "52", AsOf: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)},
		}, nil
	}}
	service.stockMarketData = &fakeStockInvestmentMarketData{currentQuote: func(
		context.Context, marketdata.EquityInstrument, string,
	) (investmentMarketQuote, error) {
		return investmentMarketQuote{Price: "53", Provider: "trading212", AsOf: now}, nil
	}}

	history, err := service.InvestmentPortfolioHistory(context.Background(), 7, "1y")
	if err != nil {
		t.Fatal(err)
	}
	if history.UnsupportedPositions != 0 || len(history.Points) != 3 || len(cached) != 2 {
		t.Fatalf("history/cache = %#v/%#v", history, cached)
	}
	last := history.Points[len(history.Points)-1]
	if last.Value != "106.00" || len(last.Holdings) != 1 || last.Holdings[0].Symbol != "QDVE" ||
		last.Holdings[0].Value != "106.00" {
		t.Fatalf("last history point = %#v", last)
	}
}

func TestInvestmentPortfolioHistoryKeepsAvailableHoldingsWhenStockHistoryIsUnavailable(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		return []model.InvestmentTrade{
			{ID: 1, AssetType: "stock", Symbol: "WDI", AssetName: "Wirecard", Exchange: "XETRA", MarketCurrency: "EUR", Broker: "trading212", Side: "buy", Amount: "1.00", Quantity: "1", PricePerUnit: "1", Fees: "0", OccurredAt: "2026-07-10T10:00:00Z"},
			{ID: 2, AssetType: "stock", Symbol: "WDI", AssetName: "Wirecard", Exchange: "XETRA", MarketCurrency: "EUR", Broker: "trading212", Side: "sell", Amount: "0.01", Quantity: "1", PricePerUnit: "0.01", Fees: "0", OccurredAt: "2026-07-11T10:00:00Z"},
			{ID: 3, AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "manual", Side: "buy", Amount: "100.00", Quantity: "0.002", PricePerUnit: "50000", Fees: "0", OccurredAt: "2026-07-12T10:00:00Z"},
			{ID: 4, AssetType: "stock", Symbol: "QDVE", AssetName: "iShares S&P 500 IT", Exchange: "XETRA", MarketCurrency: "EUR", Broker: "trading212", Side: "buy", Amount: "50.00", Quantity: "1", PricePerUnit: "50", Fees: "0", OccurredAt: "2026-07-12T11:00:00Z"},
		}, nil
	}}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.marketData = &fakeInvestmentMarketData{
		dailyHistory: func(context.Context, string, string, time.Time) ([]investmentMarketHistoryPoint, error) {
			return []investmentMarketHistoryPoint{
				{Price: "50000", AsOf: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)},
				{Price: "51000", AsOf: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)},
			}, nil
		},
		currentQuotes: func(context.Context, []string, string) (map[string]investmentMarketQuote, error) {
			return map[string]investmentMarketQuote{"BTC": {Price: "52000", Provider: "kraken", AsOf: now}}, nil
		},
	}
	service.stockHistoryData = &fakeStockInvestmentHistory{dailyHistory: func(
		context.Context, marketdata.EquityInstrument, string, time.Time,
	) ([]investmentMarketHistoryPoint, error) {
		return nil, errors.New("listing has no usable history")
	}}

	history, err := service.InvestmentPortfolioHistory(context.Background(), 7, "1y")
	if err != nil {
		t.Fatal(err)
	}
	if history.UnsupportedPositions != 1 || len(history.Points) != 3 {
		t.Fatalf("history = %#v", history)
	}
	last := history.Points[len(history.Points)-1]
	if last.Value != "104.00" || len(last.Holdings) != 1 || last.Holdings[0].Symbol != "BTC" {
		t.Fatalf("last history point = %#v", last)
	}
}

func TestInvestmentPortfolioMaxHistoryStartsAtFirstTradeAndCapsResponse(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	firstTrade := time.Date(2021, 5, 8, 10, 0, 0, 0, time.UTC)
	var requestedSince time.Time
	store := &fakeStore{listInvestmentTrades: func(
		context.Context, int, repository.InvestmentTradeFilter,
	) ([]model.InvestmentTrade, error) {
		return []model.InvestmentTrade{{
			ID: 1, AssetType: "crypto", Symbol: "BTC", Broker: "manual", Side: "buy",
			Amount: "100.00", Quantity: "0.002", PricePerUnit: "50000", Fees: "0",
			OccurredAt: firstTrade.Format(time.RFC3339),
		}}, nil
	}}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.marketData = &fakeInvestmentMarketData{
		dailyHistory: func(_ context.Context, _ string, _ string, since time.Time) ([]investmentMarketHistoryPoint, error) {
			requestedSince = since
			return []investmentMarketHistoryPoint{{Price: "50000", AsOf: since}}, nil
		},
		currentQuotes: func(context.Context, []string, string) (map[string]investmentMarketQuote, error) {
			return map[string]investmentMarketQuote{"BTC": {Price: "60000", Provider: "kraken", AsOf: now}}, nil
		},
	}

	history, err := service.InvestmentPortfolioHistory(context.Background(), 7, "MAX")
	if err != nil {
		t.Fatal(err)
	}
	if history.Range != "max" || len(history.Points) != maximumInvestmentHistoryResponsePoints {
		t.Fatalf("history range/count = %q/%d", history.Range, len(history.Points))
	}
	wantSince := time.Date(2021, 5, 7, 0, 0, 0, 0, time.UTC)
	if !requestedSince.Equal(wantSince) {
		t.Fatalf("history since = %s, want %s", requestedSince, wantSince)
	}
	if history.Points[0].AsOf != "2021-05-08T23:59:59Z" || history.Points[len(history.Points)-1].AsOf != now.Format(time.RFC3339) {
		t.Fatalf("history bounds = %q to %q", history.Points[0].AsOf, history.Points[len(history.Points)-1].AsOf)
	}
}

func TestSampleInvestmentHistoryPointsKeepsBounds(t *testing.T) {
	points := make([]model.InvestmentPortfolioHistoryPoint, 1200)
	for index := range points {
		points[index] = model.InvestmentPortfolioHistoryPoint{AsOf: fmt.Sprintf("point-%04d", index)}
	}
	sampled := sampleInvestmentHistoryPoints(points, 500)
	if len(sampled) != 500 || sampled[0].AsOf != points[0].AsOf || sampled[len(sampled)-1].AsOf != points[len(points)-1].AsOf {
		t.Fatalf("sampled bounds/count = %d, %q to %q", len(sampled), sampled[0].AsOf, sampled[len(sampled)-1].AsOf)
	}
}

type fakeInvestmentMarketData struct {
	quoteAt       func(context.Context, string, string, time.Time) (investmentMarketQuote, error)
	currentQuotes func(context.Context, []string, string) (map[string]investmentMarketQuote, error)
	dailyHistory  func(context.Context, string, string, time.Time) ([]investmentMarketHistoryPoint, error)
}

type fakeStockInvestmentMarketData struct {
	quoteAt      func(context.Context, marketdata.EquityInstrument, string, time.Time) (investmentMarketQuote, error)
	currentQuote func(context.Context, marketdata.EquityInstrument, string) (investmentMarketQuote, error)
}

type fakeStockInvestmentHistory struct {
	dailyHistory func(context.Context, marketdata.EquityInstrument, string, time.Time) ([]investmentMarketHistoryPoint, error)
}

func (f *fakeStockInvestmentHistory) DailyHistory(
	ctx context.Context, instrument marketdata.EquityInstrument, currency string, since time.Time,
) ([]investmentMarketHistoryPoint, error) {
	return f.dailyHistory(ctx, instrument, currency, since)
}

func (f *fakeStockInvestmentMarketData) QuoteAt(ctx context.Context, instrument marketdata.EquityInstrument, currency string, at time.Time) (investmentMarketQuote, error) {
	return f.quoteAt(ctx, instrument, currency, at)
}

func (f *fakeStockInvestmentMarketData) CurrentQuote(ctx context.Context, instrument marketdata.EquityInstrument, currency string) (investmentMarketQuote, error) {
	if f.currentQuote == nil {
		return investmentMarketQuote{}, errors.New("unexpected stock CurrentQuote call")
	}
	return f.currentQuote(ctx, instrument, currency)
}

func (f *fakeInvestmentMarketData) QuoteAt(
	ctx context.Context, symbol, currency string, at time.Time,
) (investmentMarketQuote, error) {
	if f.quoteAt == nil {
		return investmentMarketQuote{}, errors.New("unexpected QuoteAt call")
	}
	return f.quoteAt(ctx, symbol, currency, at)
}

func (f *fakeInvestmentMarketData) CurrentQuotes(
	ctx context.Context, symbols []string, currency string,
) (map[string]investmentMarketQuote, error) {
	if f.currentQuotes == nil {
		return nil, errors.New("unexpected CurrentQuotes call")
	}
	return f.currentQuotes(ctx, symbols, currency)
}

func (f *fakeInvestmentMarketData) DailyHistory(
	ctx context.Context, symbol, currency string, since time.Time,
) ([]investmentMarketHistoryPoint, error) {
	if f.dailyHistory == nil {
		return nil, errors.New("unexpected DailyHistory call")
	}
	return f.dailyHistory(ctx, symbol, currency, since)
}

func testService(store Store) *Service {
	cfg := config.Config{
		JWTSecret: strings.Repeat("s", 32), JWTIssuer: "money-manager-api",
		JWTAudience: "money-manager-mobile", JWTTTL: time.Hour, Trading212OwnerUserID: 7,
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
	createTransactionSchedule       func(context.Context, int, model.TransactionScheduleRequest) (model.TransactionSchedule, error)
	getTransactionSchedule          func(context.Context, int, int, time.Time) (model.TransactionSchedule, error)
	upsertScheduleOccurrences       func(context.Context, []repository.ScheduleOccurrenceSeed) (int, error)
	markScheduleMaterialized        func(context.Context, int, time.Time) error
	listScheduleOccurrences         func(context.Context, int, repository.ScheduleOccurrenceFilter) ([]model.TransactionScheduleOccurrence, error)
	createOpenBankingAuthorization  func(context.Context, repository.NewOpenBankingAuthorization) (int, error)
	setOpenBankingProviderID        func(context.Context, int, string) error
	claimOpenBankingAuthorization   func(context.Context, string, time.Time) (repository.OpenBankingAuthorizationRecord, error)
	failOpenBankingAuthorization    func(context.Context, int, string, string) error
	storeOpenBankingConnection      func(context.Context, repository.NewOpenBankingConnection) (int, error)
	getOpenBankingConnection        func(context.Context, int, int) (repository.OpenBankingConnectionRecord, error)
	getOpenBankingAccount           func(context.Context, int, int) (repository.OpenBankingAccountRecord, error)
	listOpenBankingProviderSessions func(context.Context, int) ([]string, error)
	importOpenBankingTransactions   func(context.Context, int, int, []repository.OpenBankingTransactionSeed, time.Time) (model.OpenBankingSyncResult, error)
	claimOpenBankingAccountsForSync func(context.Context, time.Time, time.Time, time.Time, int) ([]repository.OpenBankingSyncAccount, error)
	releaseOpenBankingSyncClaim     func(context.Context, int) error
	claimNotificationDeliveries     func(context.Context, time.Time, time.Time, time.Time, []string, int) ([]repository.NotificationDelivery, error)
	completeNotificationDelivery    func(context.Context, int, bool, bool, bool, string, time.Time, time.Time) error
	deleteUser                      func(context.Context, int) error
	createInvestmentTrade           func(context.Context, int, model.InvestmentTradeRequest) (model.InvestmentTrade, error)
	getInvestmentSchedule           func(context.Context, int, int) (model.InvestmentSchedule, error)
	listInvestmentTrades            func(context.Context, int, repository.InvestmentTradeFilter) ([]model.InvestmentTrade, error)
	deleteInvestmentTrade           func(context.Context, int, int) error
	investmentHoldingQuantity       func(context.Context, int, string, string, string) (string, error)
	listInvestmentMarketHistory     func(context.Context, string, string, string, string, time.Time) ([]model.InvestmentMarketHistoryPrice, error)
	upsertInvestmentMarketHistory   func(context.Context, []model.InvestmentMarketHistoryPrice) error
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
func (f *fakeStore) CreateTransactionSchedule(ctx context.Context, userID int, request model.TransactionScheduleRequest) (model.TransactionSchedule, error) {
	if f.createTransactionSchedule != nil {
		return f.createTransactionSchedule(ctx, userID, request)
	}
	return model.TransactionSchedule{}, errors.New("unexpected CreateTransactionSchedule call")
}
func (*fakeStore) ListTransactionSchedules(context.Context, int, string, time.Time) ([]model.TransactionSchedule, error) {
	return []model.TransactionSchedule{}, nil
}
func (f *fakeStore) GetTransactionSchedule(ctx context.Context, userID, scheduleID int, now time.Time) (model.TransactionSchedule, error) {
	if f.getTransactionSchedule != nil {
		return f.getTransactionSchedule(ctx, userID, scheduleID, now)
	}
	return model.TransactionSchedule{}, repository.ErrNotFound
}
func (*fakeStore) UpdateTransactionSchedule(context.Context, int, int, model.TransactionScheduleRequest, time.Time) (model.TransactionSchedule, error) {
	return model.TransactionSchedule{}, repository.ErrNotFound
}
func (*fakeStore) SetTransactionScheduleStatus(context.Context, int, int, string) error {
	return repository.ErrNotFound
}
func (*fakeStore) ArchiveTransactionSchedule(context.Context, int, int) error {
	return repository.ErrNotFound
}
func (*fakeStore) ListActiveTransactionSchedules(context.Context) ([]model.TransactionSchedule, error) {
	return []model.TransactionSchedule{}, nil
}
func (f *fakeStore) UpsertTransactionScheduleOccurrences(ctx context.Context, seeds []repository.ScheduleOccurrenceSeed) (int, error) {
	if f.upsertScheduleOccurrences != nil {
		return f.upsertScheduleOccurrences(ctx, seeds)
	}
	return 0, nil
}
func (f *fakeStore) MarkTransactionScheduleMaterializedThrough(ctx context.Context, scheduleID int, through time.Time) error {
	if f.markScheduleMaterialized != nil {
		return f.markScheduleMaterialized(ctx, scheduleID, through)
	}
	return nil
}
func (f *fakeStore) ListTransactionScheduleOccurrences(ctx context.Context, userID int, filter repository.ScheduleOccurrenceFilter) ([]model.TransactionScheduleOccurrence, error) {
	if f.listScheduleOccurrences != nil {
		return f.listScheduleOccurrences(ctx, userID, filter)
	}
	return []model.TransactionScheduleOccurrence{}, nil
}
func (*fakeStore) PostDueTransactionScheduleOccurrences(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (*fakeStore) QueueDueTransactionScheduleReminders(context.Context, time.Time, int) (int, error) {
	return 0, nil
}
func (*fakeStore) ListBudgets(context.Context, int, time.Time, bool) ([]model.Budget, error) {
	return []model.Budget{}, nil
}
func (*fakeStore) GetBudget(context.Context, int, int, time.Time) (model.Budget, error) {
	return model.Budget{}, repository.ErrNotFound
}
func (*fakeStore) CreateBudget(context.Context, int, model.BudgetRequest, time.Time) (model.Budget, error) {
	return model.Budget{}, nil
}
func (*fakeStore) UpdateBudget(context.Context, int, int, model.BudgetRequest, time.Time) (model.Budget, error) {
	return model.Budget{}, repository.ErrNotFound
}
func (*fakeStore) ArchiveBudget(context.Context, int, int) error             { return repository.ErrNotFound }
func (*fakeStore) QueueBudgetAlerts(context.Context, time.Time) (int, error) { return 0, nil }
func (*fakeStore) GetNotificationPreferences(context.Context, int) (model.NotificationPreferences, error) {
	return model.NotificationPreferences{Timezone: defaultScheduleTimezone}, nil
}
func (*fakeStore) UpdateNotificationPreferences(context.Context, int, model.NotificationPreferences) (model.NotificationPreferences, error) {
	return model.NotificationPreferences{}, nil
}
func (*fakeStore) RegisterPushDevice(context.Context, int, model.PushDeviceRequest) (model.PushDevice, error) {
	return model.PushDevice{ID: 1}, nil
}
func (*fakeStore) DeactivatePushDevice(context.Context, int, int) error {
	return repository.ErrNotFound
}

func (f *fakeStore) CreateInvestmentTrade(ctx context.Context, userID int, request model.InvestmentTradeRequest) (model.InvestmentTrade, error) {
	if f.createInvestmentTrade != nil {
		return f.createInvestmentTrade(ctx, userID, request)
	}
	return model.InvestmentTrade{}, nil
}
func (f *fakeStore) ListInvestmentTrades(ctx context.Context, userID int, filter repository.InvestmentTradeFilter) ([]model.InvestmentTrade, error) {
	if f.listInvestmentTrades != nil {
		return f.listInvestmentTrades(ctx, userID, filter)
	}
	return []model.InvestmentTrade{}, nil
}
func (f *fakeStore) DeleteInvestmentTrade(ctx context.Context, userID, tradeID int) error {
	if f.deleteInvestmentTrade != nil {
		return f.deleteInvestmentTrade(ctx, userID, tradeID)
	}
	return repository.ErrNotFound
}
func (f *fakeStore) InvestmentHoldingQuantity(ctx context.Context, userID int, assetType, symbol, exchange, broker string) (string, error) {
	if f.investmentHoldingQuantity != nil {
		return f.investmentHoldingQuantity(ctx, userID, assetType, symbol, broker)
	}
	return "0", nil
}
func (*fakeStore) ListInvestmentPrices(context.Context) ([]model.InvestmentPrice, error) {
	return []model.InvestmentPrice{}, nil
}
func (*fakeStore) UpsertManualInvestmentPrice(context.Context, int, model.InvestmentPriceRequest, time.Time) (model.InvestmentPrice, error) {
	return model.InvestmentPrice{}, repository.ErrNotFound
}
func (f *fakeStore) ListInvestmentMarketHistory(
	ctx context.Context, assetType, symbol, exchange, currency string, since time.Time,
) ([]model.InvestmentMarketHistoryPrice, error) {
	if f.listInvestmentMarketHistory != nil {
		return f.listInvestmentMarketHistory(ctx, assetType, symbol, exchange, currency, since)
	}
	return []model.InvestmentMarketHistoryPrice{}, nil
}
func (f *fakeStore) UpsertInvestmentMarketHistory(
	ctx context.Context, prices []model.InvestmentMarketHistoryPrice,
) error {
	if f.upsertInvestmentMarketHistory != nil {
		return f.upsertInvestmentMarketHistory(ctx, prices)
	}
	return nil
}
func (*fakeStore) CreateInvestmentSchedule(context.Context, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error) {
	return model.InvestmentSchedule{}, nil
}
func (*fakeStore) ListInvestmentSchedules(context.Context, int, string) ([]model.InvestmentSchedule, error) {
	return []model.InvestmentSchedule{}, nil
}

func (f *fakeStore) GetInvestmentSchedule(ctx context.Context, userID, scheduleID int) (model.InvestmentSchedule, error) {
	if f.getInvestmentSchedule != nil {
		return f.getInvestmentSchedule(ctx, userID, scheduleID)
	}
	return model.InvestmentSchedule{}, repository.ErrNotFound
}
func (*fakeStore) UpdateInvestmentSchedule(context.Context, int, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error) {
	return model.InvestmentSchedule{}, repository.ErrNotFound
}
func (*fakeStore) SetInvestmentScheduleStatus(context.Context, int, int, string) error {
	return repository.ErrNotFound
}
func (*fakeStore) ArchiveInvestmentSchedule(context.Context, int, int) error {
	return repository.ErrNotFound
}
func (*fakeStore) ListActiveInvestmentSchedules(context.Context) ([]model.InvestmentSchedule, error) {
	return []model.InvestmentSchedule{}, nil
}
func (*fakeStore) QueueInvestmentReminder(context.Context, model.InvestmentSchedule, time.Time) (bool, error) {
	return false, nil
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
func (f *fakeStore) ImportOpenBankingTransactions(
	ctx context.Context,
	userID int,
	accountID int,
	transactions []repository.OpenBankingTransactionSeed,
	syncedAt time.Time,
) (model.OpenBankingSyncResult, error) {
	if f.importOpenBankingTransactions != nil {
		return f.importOpenBankingTransactions(ctx, userID, accountID, transactions, syncedAt)
	}
	return model.OpenBankingSyncResult{}, errors.New("unexpected ImportOpenBankingTransactions call")
}

func (f *fakeStore) ClaimOpenBankingAccountsForSync(
	ctx context.Context,
	now time.Time,
	nextAttempt time.Time,
	claimUntil time.Time,
	limit int,
) ([]repository.OpenBankingSyncAccount, error) {
	if f.claimOpenBankingAccountsForSync != nil {
		return f.claimOpenBankingAccountsForSync(ctx, now, nextAttempt, claimUntil, limit)
	}
	return []repository.OpenBankingSyncAccount{}, nil
}

func (f *fakeStore) ReleaseOpenBankingSyncClaim(ctx context.Context, accountID int) error {
	if f.releaseOpenBankingSyncClaim != nil {
		return f.releaseOpenBankingSyncClaim(ctx, accountID)
	}
	return nil
}

func (f *fakeStore) ClaimNotificationDeliveries(
	ctx context.Context, now, staleBefore, expiredBefore time.Time, platforms []string, limit int,
) ([]repository.NotificationDelivery, error) {
	if f.claimNotificationDeliveries != nil {
		return f.claimNotificationDeliveries(ctx, now, staleBefore, expiredBefore, platforms, limit)
	}
	return []repository.NotificationDelivery{}, nil
}

func (f *fakeStore) CompleteNotificationDelivery(
	ctx context.Context,
	deliveryID int,
	success, permanent, deactivate bool,
	errorMessage string,
	retryAt, now time.Time,
) error {
	if f.completeNotificationDelivery != nil {
		return f.completeNotificationDelivery(
			ctx, deliveryID, success, permanent, deactivate, errorMessage, retryAt, now,
		)
	}
	return nil
}
