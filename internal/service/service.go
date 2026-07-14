package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/config"
	"money-manager-server/internal/model"
	"money-manager-server/internal/push"
	"money-manager-server/internal/repository"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	minimumPasswordBytes    = 8
	maximumPasswordBytes    = 72
	maximumEmailBytes       = 254
	maximumCategoryRunes    = 40
	maximumDescriptionRunes = 500
	supportedCurrency       = "EUR"
	maximumExportDays       = 366
	maximumExportRows       = 5000
	maximumImportRows       = 5000
)

type Store interface {
	Close()
	Ping(context.Context) error
	RegisterUser(context.Context, string, string) (model.User, error)
	FindUserByEmail(context.Context, string) (repository.UserWithPassword, error)
	GetUser(context.Context, int) (model.User, error)
	DeleteUser(context.Context, int) error
	EnsureDefaultCategories(context.Context, int) error
	ListCategories(context.Context, int, string) ([]model.Category, error)
	CreateCategory(context.Context, int, model.CategoryRequest) (model.Category, error)
	DeleteCategory(context.Context, int, int) error
	FindActiveCategoryName(context.Context, int, string, string) (string, error)
	ListTransactions(context.Context, int, repository.TransactionFilter) ([]model.Transaction, error)
	ExportTransactions(context.Context, int, time.Time, time.Time, int) ([]model.Transaction, error)
	CreateTransaction(context.Context, int, model.TransactionRequest) (model.Transaction, error)
	ImportTransactions(context.Context, int, []model.ImportedTransaction) (int, int, error)
	GetTransaction(context.Context, int, int) (model.Transaction, error)
	UpdateTransaction(context.Context, int, int, model.TransactionRequest) (model.Transaction, error)
	DeleteTransaction(context.Context, int, int) error
	Summary(context.Context, int, string, time.Time, time.Time) (model.Summary, error)
	CreateTransactionSchedule(context.Context, int, model.TransactionScheduleRequest) (model.TransactionSchedule, error)
	ListTransactionSchedules(context.Context, int, string, time.Time) ([]model.TransactionSchedule, error)
	GetTransactionSchedule(context.Context, int, int, time.Time) (model.TransactionSchedule, error)
	UpdateTransactionSchedule(context.Context, int, int, model.TransactionScheduleRequest, time.Time) (model.TransactionSchedule, error)
	SetTransactionScheduleStatus(context.Context, int, int, string) error
	ArchiveTransactionSchedule(context.Context, int, int) error
	ListActiveTransactionSchedules(context.Context) ([]model.TransactionSchedule, error)
	UpsertTransactionScheduleOccurrences(context.Context, []repository.ScheduleOccurrenceSeed) (int, error)
	MarkTransactionScheduleMaterializedThrough(context.Context, int, time.Time) error
	ListTransactionScheduleOccurrences(context.Context, int, repository.ScheduleOccurrenceFilter) ([]model.TransactionScheduleOccurrence, error)
	PostDueTransactionScheduleOccurrences(context.Context, time.Time, int) (int, error)
	QueueDueTransactionScheduleReminders(context.Context, time.Time, int) (int, error)
	ListBudgets(context.Context, int, time.Time, bool) ([]model.Budget, error)
	GetBudget(context.Context, int, int, time.Time) (model.Budget, error)
	CreateBudget(context.Context, int, model.BudgetRequest, time.Time) (model.Budget, error)
	UpdateBudget(context.Context, int, int, model.BudgetRequest, time.Time) (model.Budget, error)
	ArchiveBudget(context.Context, int, int) error
	QueueBudgetAlerts(context.Context, time.Time) (int, error)
	GetNotificationPreferences(context.Context, int) (model.NotificationPreferences, error)
	UpdateNotificationPreferences(context.Context, int, model.NotificationPreferences) (model.NotificationPreferences, error)
	RegisterPushDevice(context.Context, int, model.PushDeviceRequest) (model.PushDevice, error)
	DeactivatePushDevice(context.Context, int, int) error
	CreateInvestmentTrade(context.Context, int, model.InvestmentTradeRequest) (model.InvestmentTrade, error)
	ListInvestmentTrades(context.Context, int, repository.InvestmentTradeFilter) ([]model.InvestmentTrade, error)
	DeleteInvestmentTrade(context.Context, int, int) error
	InvestmentHoldingQuantity(context.Context, int, string, string, string) (string, error)
	ListInvestmentPrices(context.Context) ([]model.InvestmentPrice, error)
	UpsertManualInvestmentPrice(context.Context, int, model.InvestmentPriceRequest, time.Time) (model.InvestmentPrice, error)
	CreateInvestmentSchedule(context.Context, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error)
	ListInvestmentSchedules(context.Context, int, string) ([]model.InvestmentSchedule, error)
	GetInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error)
	UpdateInvestmentSchedule(context.Context, int, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error)
	SetInvestmentScheduleStatus(context.Context, int, int, string) error
	ArchiveInvestmentSchedule(context.Context, int, int) error
	ListActiveInvestmentSchedules(context.Context) ([]model.InvestmentSchedule, error)
	QueueInvestmentReminder(context.Context, model.InvestmentSchedule, time.Time) (bool, error)
	CreateOpenBankingAuthorization(context.Context, repository.NewOpenBankingAuthorization) (int, error)
	SetOpenBankingAuthorizationProviderID(context.Context, int, string) error
	ClaimOpenBankingAuthorization(context.Context, string, time.Time) (repository.OpenBankingAuthorizationRecord, error)
	FailOpenBankingAuthorization(context.Context, int, string, string) error
	StoreOpenBankingConnection(context.Context, repository.NewOpenBankingConnection) (int, error)
	ListOpenBankingConnections(context.Context, int) ([]model.OpenBankingConnection, error)
	GetOpenBankingConnection(context.Context, int, int) (repository.OpenBankingConnectionRecord, error)
	UpdateOpenBankingConnectionStatus(context.Context, int, int, string) error
	DeleteOpenBankingConnection(context.Context, int, int) error
	ListOpenBankingAccounts(context.Context, int) ([]model.OpenBankingAccount, error)
	GetOpenBankingAccount(context.Context, int, int) (repository.OpenBankingAccountRecord, error)
	ListOpenBankingProviderSessions(context.Context, int) ([]string, error)
	ImportOpenBankingTransactions(context.Context, int, int, []repository.OpenBankingTransactionSeed, time.Time) (model.OpenBankingSyncResult, error)
	ClaimOpenBankingAccountsForSync(context.Context, time.Time, time.Time, time.Time, int) ([]repository.OpenBankingSyncAccount, error)
	ReleaseOpenBankingSyncClaim(context.Context, int) error
	ClaimNotificationDeliveries(context.Context, time.Time, time.Time, []string, int) ([]repository.NotificationDelivery, error)
	CompleteNotificationDelivery(context.Context, int, bool, bool, bool, string, time.Time, time.Time) error
}

func (s *Service) ImportRevolutCSV(ctx context.Context, userID int, contents []byte) (model.ImportResult, error) {
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(contents, []byte{0xEF, 0xBB, 0xBF})))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return model.ImportResult{}, apperrors.Validation("file must be a valid CSV")
	}
	if len(records) < 2 {
		return model.ImportResult{}, apperrors.Validation("CSV must contain a header and at least one transaction")
	}
	if len(records)-1 > maximumImportRows {
		return model.ImportResult{}, apperrors.Validation("CSV contains more than 5000 transactions")
	}

	headers := make(map[string]int, len(records[0]))
	for index, header := range records[0] {
		headers[normalizeCSVHeader(header)] = index
	}
	for _, required := range []string{"description", "amount", "currency"} {
		if _, ok := headers[required]; !ok {
			return model.ImportResult{}, apperrors.Validation("CSV is missing required Revolut columns")
		}
	}
	if _, completed := headers["completed date"]; !completed {
		if _, started := headers["started date"]; !started {
			return model.ImportResult{}, apperrors.Validation("CSV is missing required Revolut date columns")
		}
	}

	if err := s.store.EnsureDefaultCategories(ctx, userID); err != nil {
		return model.ImportResult{}, apperrors.Internal(fmt.Errorf("ensure default categories: %w", err))
	}
	imports := make([]model.ImportedTransaction, 0, len(records)-1)
	ignored := 0
	importCategories := make(map[string]string, 2)
	for rowIndex, record := range records[1:] {
		if len(record) == 1 && strings.TrimSpace(record[0]) == "" {
			ignored++
			continue
		}
		field := func(name string) string {
			index, ok := headers[name]
			if !ok || index >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[index])
		}
		state := strings.ToUpper(field("state"))
		if state != "" && state != "COMPLETED" {
			ignored++
			continue
		}
		currency := strings.ToUpper(field("currency"))
		if currency != supportedCurrency {
			ignored++
			continue
		}
		rawAmount := strings.ReplaceAll(field("amount"), ",", "")
		transactionType := "income"
		if strings.HasPrefix(rawAmount, "-") {
			transactionType = "expense"
			rawAmount = strings.TrimPrefix(rawAmount, "-")
		} else {
			rawAmount = strings.TrimPrefix(rawAmount, "+")
		}
		if isZeroCSVAmount(rawAmount) {
			ignored++
			continue
		}
		amount, amountErr := normalizeAmount(rawAmount)
		if amountErr != nil {
			return model.ImportResult{}, apperrors.Validation(fmt.Sprintf("row %d has an invalid amount", rowIndex+2))
		}
		date, dateErr := parseRevolutDate(firstNonEmpty(field("completed date"), field("started date")))
		if dateErr != nil {
			return model.ImportResult{}, apperrors.Validation(fmt.Sprintf("row %d has an invalid date", rowIndex+2))
		}
		description, descriptionErr := normalizeLimitedText(field("description"), "description", maximumDescriptionRunes, false)
		if descriptionErr != nil {
			return model.ImportResult{}, apperrors.Validation(fmt.Sprintf("row %d has an invalid description", rowIndex+2))
		}
		category, ok := importCategories[transactionType]
		if !ok {
			var categoryErr error
			category, categoryErr = s.store.FindActiveCategoryName(ctx, userID, transactionType, "other")
			if categoryErr != nil {
				return model.ImportResult{}, apperrors.Internal(fmt.Errorf("find import category: %w", categoryErr))
			}
			importCategories[transactionType] = category
		}
		hash := sha256.Sum256([]byte(strings.Join(record, "\x1f")))
		imports = append(imports, model.ImportedTransaction{
			Request: model.TransactionRequest{
				Type: transactionType, Category: category, Description: description,
				Amount: amount, Currency: currency, OccurredAt: date.Format("2006-01-02"),
			},
			Fingerprint: hex.EncodeToString(hash[:]),
		})
	}
	if len(imports) == 0 {
		return model.ImportResult{Ignored: ignored}, nil
	}
	imported, skipped, err := s.store.ImportTransactions(ctx, userID, imports)
	if err != nil {
		return model.ImportResult{}, apperrors.Internal(fmt.Errorf("import transactions: %w", err))
	}
	return model.ImportResult{Imported: imported, Skipped: skipped, Ignored: ignored}, nil
}

func isZeroCSVAmount(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, character := range value {
		if character != '0' && character != '.' {
			return false
		}
	}
	return true
}

func normalizeCSVHeader(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimPrefix(value, "\ufeff")), " "))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseRevolutDate(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05.000", time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("unsupported Revolut date")
}

type Service struct {
	store               Store
	secret              []byte
	issuer              string
	audience            string
	tokenTTL            time.Duration
	legacyAcceptUntil   time.Time
	now                 func() time.Time
	openBanking         openBankingClient
	openBankingConfig   openBankingServiceConfig
	openBankingError    error
	pushSenders         map[string]notificationSender
	pushPlatforms       []string
	pushError           error
	scheduleHorizonDays int
}

type notificationSender interface {
	Send(context.Context, push.Notification) (push.Result, error)
}

type tokenClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

func New(ctx context.Context, cfg config.Config) (*Service, error) {
	connectCtx, cancelConnect := context.WithTimeout(ctx, cfg.StartupTimeout)
	db, err := repository.Open(connectCtx, cfg.DatabaseURL, repository.Options{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   cfg.DBMaxConnLifetime,
		MaxConnIdleTime:   cfg.DBMaxConnIdleTime,
		HealthCheckPeriod: cfg.DBHealthCheckPeriod,
	})
	cancelConnect()
	if err != nil {
		return nil, err
	}
	migrationCtx, cancelMigration := context.WithTimeout(ctx, cfg.MigrationTimeout)
	defer cancelMigration()
	if err := repository.Migrate(migrationCtx, db); err != nil {
		db.Close()
		return nil, err
	}
	return NewWithStore(repository.New(db), cfg), nil
}

func NewWithStore(store Store, cfg config.Config) *Service {
	result := &Service{
		store:               store,
		secret:              []byte(cfg.JWTSecret),
		issuer:              cfg.JWTIssuer,
		audience:            cfg.JWTAudience,
		tokenTTL:            cfg.JWTTTL,
		legacyAcceptUntil:   cfg.JWTLegacyAcceptUntil,
		now:                 time.Now,
		scheduleHorizonDays: 90,
	}
	result.configureOpenBanking(cfg)
	result.configurePush(cfg)
	return result
}

func (s *Service) configurePush(cfg config.Config) {
	s.pushSenders = make(map[string]notificationSender)
	if cfg.APNSPrivateKey != nil {
		client, err := push.NewAPNSClient(push.APNSConfig{
			KeyID: cfg.APNSKeyID, TeamID: cfg.APNSTeamID, BundleID: cfg.APNSBundleID,
			PrivateKey: cfg.APNSPrivateKey, HTTPClient: &http.Client{Timeout: cfg.APNSRequestTimeout},
		})
		if err != nil {
			s.pushError = errors.Join(s.pushError, err)
		} else {
			s.pushSenders["ios"] = client
			s.pushPlatforms = append(s.pushPlatforms, "ios")
		}
	}
	if cfg.FCMPrivateKey != nil {
		client, err := push.NewFCMClient(push.FCMConfig{
			ProjectID: cfg.FCMProjectID, ClientEmail: cfg.FCMClientEmail,
			PrivateKey: cfg.FCMPrivateKey, HTTPClient: &http.Client{Timeout: cfg.FCMRequestTimeout},
		})
		if err != nil {
			s.pushError = errors.Join(s.pushError, err)
		} else {
			s.pushSenders["android"] = client
			s.pushPlatforms = append(s.pushPlatforms, "android")
		}
	}
}

func (s *Service) Close() { s.store.Close() }

func (s *Service) Ready(ctx context.Context) error { return s.store.Ping(ctx) }

func (s *Service) Register(ctx context.Context, request model.AuthRequest) (model.AuthResponse, error) {
	email, err := normalizeEmail(request.Email)
	if err != nil {
		return model.AuthResponse{}, err
	}
	if err := validatePassword(request.Password); err != nil {
		return model.AuthResponse{}, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("hash password: %w", err))
	}
	user, err := s.store.RegisterUser(ctx, email, string(passwordHash))
	if errors.Is(err, repository.ErrConflict) {
		return model.AuthResponse{}, apperrors.Conflict("email is already registered")
	}
	if err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("register user: %w", err))
	}
	token, err := s.issueToken(user)
	if err != nil {
		return model.AuthResponse{}, err
	}
	return model.AuthResponse{Token: token, User: user}, nil
}

func (s *Service) Login(ctx context.Context, request model.AuthRequest) (model.AuthResponse, error) {
	email, err := normalizeLoginEmail(request.Email)
	if err != nil {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}
	if request.Password == "" || len([]byte(request.Password)) > maximumPasswordBytes {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}

	record, err := s.store.FindUserByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}
	if err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("find user: %w", err))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(request.Password)); err != nil {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}
	if err := s.store.EnsureDefaultCategories(ctx, record.User.ID); err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("ensure default categories: %w", err))
	}
	token, err := s.issueToken(record.User)
	if err != nil {
		return model.AuthResponse{}, err
	}
	return model.AuthResponse{Token: token, User: record.User}, nil
}

func (s *Service) GetMe(ctx context.Context, userID int) (model.User, error) {
	if err := validateID(userID); err != nil {
		return model.User{}, err
	}
	user, err := s.store.GetUser(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.User{}, apperrors.NotFound("user not found")
	}
	if err != nil {
		return model.User{}, apperrors.Internal(fmt.Errorf("get user: %w", err))
	}
	return user, nil
}

func (s *Service) DeleteMe(ctx context.Context, userID int) error {
	if err := validateID(userID); err != nil {
		return err
	}
	sessions, err := s.store.ListOpenBankingProviderSessions(ctx, userID)
	if err != nil {
		return apperrors.Internal(fmt.Errorf("list bank sessions before user deletion: %w", err))
	}
	if len(sessions) > 0 {
		client, err := s.requireOpenBanking()
		if err != nil {
			return err
		}
		for _, sessionID := range sessions {
			if err := client.DeleteSession(ctx, sessionID, enableBankingEmptyPSUHeaders()); err != nil && !providerSessionAlreadyGone(err) {
				return mapOpenBankingProviderError("revoke session before user deletion", err)
			}
		}
	}
	err = s.store.DeleteUser(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("user not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete user: %w", err))
	}
	return nil
}

func (s *Service) ListCategories(ctx context.Context, userID int, transactionType string) ([]model.Category, error) {
	transactionType, err := normalizeTransactionType(transactionType)
	if err != nil {
		return nil, err
	}
	if err := s.store.EnsureDefaultCategories(ctx, userID); err != nil {
		return nil, apperrors.Internal(fmt.Errorf("ensure default categories: %w", err))
	}
	categories, err := s.store.ListCategories(ctx, userID, transactionType)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list categories: %w", err))
	}
	return categories, nil
}

func (s *Service) CreateCategory(ctx context.Context, userID int, request model.CategoryRequest) (model.Category, error) {
	transactionType, err := normalizeTransactionType(request.Type)
	if err != nil {
		return model.Category{}, err
	}
	name, err := normalizeLimitedText(request.Name, "category name", maximumCategoryRunes, false)
	if err != nil {
		return model.Category{}, err
	}
	request.Type = transactionType
	request.Name = name

	category, err := s.store.CreateCategory(ctx, userID, request)
	if errors.Is(err, repository.ErrConflict) {
		return model.Category{}, apperrors.Conflict("category already exists")
	}
	if err != nil {
		return model.Category{}, apperrors.Internal(fmt.Errorf("create category: %w", err))
	}
	return category, nil
}

func (s *Service) DeleteCategory(ctx context.Context, userID, categoryID int) error {
	if err := validateID(categoryID); err != nil {
		return err
	}
	err := s.store.DeleteCategory(ctx, userID, categoryID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("custom category not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete category: %w", err))
	}
	return nil
}

func (s *Service) ListTransactions(ctx context.Context, userID int, month, transactionType, category string) ([]model.Transaction, error) {
	monthKey, from, to, err := parseMonth(month)
	if err != nil {
		return nil, err
	}
	_ = monthKey
	if strings.TrimSpace(transactionType) != "" {
		transactionType, err = normalizeTransactionType(transactionType)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(category) != "" {
		category, err = normalizeLimitedText(category, "category", maximumCategoryRunes, false)
		if err != nil {
			return nil, err
		}
	}
	transactions, err := s.store.ListTransactions(ctx, userID, repository.TransactionFilter{
		From: from, To: to, Type: transactionType, Category: category,
	})
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list transactions: %w", err))
	}
	return transactions, nil
}

func (s *Service) CreateTransaction(ctx context.Context, userID int, request model.TransactionRequest) (model.Transaction, error) {
	normalized, err := s.validateTransaction(ctx, userID, request, nil)
	if err != nil {
		return model.Transaction{}, err
	}
	transaction, err := s.store.CreateTransaction(ctx, userID, normalized)
	if err != nil {
		return model.Transaction{}, apperrors.Internal(fmt.Errorf("create transaction: %w", err))
	}
	return transaction, nil
}

func (s *Service) UpdateTransaction(ctx context.Context, userID, transactionID int, request model.TransactionRequest) (model.Transaction, error) {
	if err := validateID(transactionID); err != nil {
		return model.Transaction{}, err
	}
	existing, err := s.store.GetTransaction(ctx, userID, transactionID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Transaction{}, apperrors.NotFound("transaction not found")
	}
	if err != nil {
		return model.Transaction{}, apperrors.Internal(fmt.Errorf("get transaction for update: %w", err))
	}
	normalized, err := s.validateTransaction(ctx, userID, request, &existing)
	if err != nil {
		return model.Transaction{}, err
	}
	transaction, err := s.store.UpdateTransaction(ctx, userID, transactionID, normalized)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Transaction{}, apperrors.NotFound("transaction not found")
	}
	if err != nil {
		return model.Transaction{}, apperrors.Internal(fmt.Errorf("update transaction: %w", err))
	}
	return transaction, nil
}

func (s *Service) DeleteTransaction(ctx context.Context, userID, transactionID int) error {
	if err := validateID(transactionID); err != nil {
		return err
	}
	err := s.store.DeleteTransaction(ctx, userID, transactionID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("transaction not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete transaction: %w", err))
	}
	return nil
}

func (s *Service) Summary(ctx context.Context, userID int, month string) (model.Summary, error) {
	monthKey, from, to, err := parseMonth(month)
	if err != nil {
		return model.Summary{}, err
	}
	summary, err := s.store.Summary(ctx, userID, monthKey, from, to)
	if err != nil {
		return model.Summary{}, apperrors.Internal(fmt.Errorf("summarize transactions: %w", err))
	}
	return summary, nil
}

func (s *Service) ExportTransactions(ctx context.Context, userID int, fromString, toString string) ([]model.Transaction, error) {
	from, err := parseDate(fromString, "from")
	if err != nil {
		return nil, err
	}
	to, err := parseDate(toString, "to")
	if err != nil {
		return nil, err
	}
	if from.After(to) {
		return nil, apperrors.Validation("from must be before or equal to to")
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > maximumExportDays {
		return nil, apperrors.Validation("export date range must be 366 days or less")
	}
	transactions, err := s.store.ExportTransactions(ctx, userID, from, to.AddDate(0, 0, 1), maximumExportRows+1)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("export transactions: %w", err))
	}
	if len(transactions) > maximumExportRows {
		return nil, apperrors.Validation("export contains more than 5000 transactions; narrow the date range")
	}
	return transactions, nil
}

func (s *Service) issueToken(user model.User) (string, error) {
	now := s.now().UTC()
	claims := tokenClaims{
		Email: user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   strconv.Itoa(user.ID),
			Audience:  jwt.ClaimStrings{s.audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(s.tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", apperrors.Internal(fmt.Errorf("sign access token: %w", err))
	}
	return token, nil
}

func (s *Service) ParseUserID(rawToken string) (int, error) {
	claims, err := s.parseCurrentToken(rawToken)
	if err == nil {
		return userIDFromClaims(claims)
	}
	if s.legacyAcceptUntil.IsZero() || !s.now().UTC().Before(s.legacyAcceptUntil) {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	}
	legacyClaims, legacyErr := s.parseLegacyToken(rawToken)
	if legacyErr != nil || legacyClaims.Issuer != "" || len(legacyClaims.Audience) != 0 ||
		legacyClaims.ExpiresAt == nil || legacyClaims.ExpiresAt.Time.After(s.legacyAcceptUntil) {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	}
	return userIDFromClaims(legacyClaims)
}

func (s *Service) parseCurrentToken(rawToken string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid current token")
	}
	return claims, nil
}

func (s *Service) parseLegacyToken(rawToken string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid legacy token")
	}
	return claims, nil
}

func userIDFromClaims(claims *tokenClaims) (int, error) {
	userID, err := strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	}
	return userID, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (int, error) {
	userID, err := s.ParseUserID(rawToken)
	if err != nil {
		return 0, err
	}
	if _, err := s.store.GetUser(ctx, userID); errors.Is(err, repository.ErrNotFound) {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	} else if err != nil {
		return 0, apperrors.Internal(fmt.Errorf("authenticate user: %w", err))
	}
	return userID, nil
}

func (s *Service) validateTransaction(ctx context.Context, userID int, request model.TransactionRequest, existing *model.Transaction) (model.TransactionRequest, error) {
	transactionType, err := normalizeTransactionType(request.Type)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	category, err := normalizeLimitedText(request.Category, "category", maximumCategoryRunes, false)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	canonicalCategory := ""
	if existing != nil && transactionType == existing.Type && strings.EqualFold(category, existing.Category) {
		canonicalCategory = existing.Category
	} else {
		canonicalCategory, err = s.store.FindActiveCategoryName(ctx, userID, transactionType, category)
		if errors.Is(err, repository.ErrNotFound) {
			return model.TransactionRequest{}, apperrors.Validation("category must be active and match the transaction type")
		}
		if err != nil {
			return model.TransactionRequest{}, apperrors.Internal(fmt.Errorf("validate category: %w", err))
		}
	}
	amount, err := normalizeAmount(request.Amount)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = supportedCurrency
	}
	if currency != supportedCurrency {
		return model.TransactionRequest{}, apperrors.Validation("currency must be EUR")
	}
	date, err := parseDate(request.OccurredAt, "occurred_at")
	if err != nil {
		return model.TransactionRequest{}, err
	}
	description, err := normalizeLimitedText(request.Description, "description", maximumDescriptionRunes, true)
	if err != nil {
		return model.TransactionRequest{}, err
	}
	return model.TransactionRequest{
		Type: transactionType, Category: canonicalCategory, Description: description,
		Amount: amount, Currency: currency, OccurredAt: date.Format("2006-01-02"),
		ExcludedFromBudget: request.ExcludedFromBudget,
	}, nil
}

func normalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" {
		return "", apperrors.Validation("email is required")
	}
	if len([]byte(email)) > maximumEmailBytes || strings.ContainsAny(email, "\r\n\t") {
		return "", apperrors.Validation("email is invalid")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email || strings.Count(email, "@") != 1 {
		return "", apperrors.Validation("email is invalid")
	}
	localPart, domain, _ := strings.Cut(email, "@")
	if localPart == "" || domain == "" || len([]byte(localPart)) > 64 {
		return "", apperrors.Validation("email is invalid")
	}
	return email, nil
}

func normalizeLoginEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" || len([]byte(email)) > maximumEmailBytes || strings.ContainsAny(email, "\r\n\t") {
		return "", apperrors.Validation("email is invalid")
	}
	return email, nil
}

func validatePassword(password string) error {
	length := len([]byte(password))
	if length < minimumPasswordBytes || length > maximumPasswordBytes {
		return apperrors.Validation("password must be between 8 and 72 bytes")
	}
	return nil
}

func normalizeTransactionType(value string) (string, error) {
	transactionType := strings.ToLower(strings.TrimSpace(value))
	if transactionType != "expense" && transactionType != "income" {
		return "", apperrors.Validation("type must be expense or income")
	}
	return transactionType, nil
}

func normalizeLimitedText(value, field string, maximumRunes int, allowEmpty bool) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) {
		return "", apperrors.Validation(field + " must be valid UTF-8")
	}
	length := utf8.RuneCountInString(value)
	if !allowEmpty && length == 0 {
		return "", apperrors.Validation(field + " is required")
	}
	if length > maximumRunes {
		return "", apperrors.Validation(fmt.Sprintf("%s must be %d characters or less", field, maximumRunes))
	}
	return value, nil
}

func normalizeAmount(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") || strings.Count(value, ".") > 1 {
		return "", apperrors.Validation("amount must be a positive decimal with at most 2 decimal places")
	}
	whole, fraction, hasFraction := strings.Cut(value, ".")
	if whole == "" {
		return "", apperrors.Validation("amount must be a positive decimal with at most 2 decimal places")
	}
	for _, part := range []string{whole, fraction} {
		for _, character := range part {
			if character < '0' || character > '9' {
				return "", apperrors.Validation("amount must be a positive decimal with at most 2 decimal places")
			}
		}
	}
	if hasFraction && (len(fraction) == 0 || len(fraction) > 2) {
		return "", apperrors.Validation("amount must be a positive decimal with at most 2 decimal places")
	}
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	if len(whole) > 12 {
		return "", apperrors.Validation("amount must be at most 999999999999.99")
	}
	if !hasFraction {
		fraction = "00"
	} else if len(fraction) == 1 {
		fraction += "0"
	}
	if whole == "0" && fraction == "00" {
		return "", apperrors.Validation("amount must be greater than 0")
	}
	return whole + "." + fraction, nil
}

func parseDate(value, field string) (time.Time, error) {
	value = strings.TrimSpace(value)
	date, err := time.Parse("2006-01-02", value)
	if err != nil || date.Format("2006-01-02") != value {
		return time.Time{}, apperrors.Validation(field + " must use YYYY-MM-DD")
	}
	return date, nil
}

func parseMonth(value string) (string, time.Time, time.Time, error) {
	value = strings.TrimSpace(value)
	month, err := time.Parse("2006-01", value)
	if err != nil || month.Format("2006-01") != value {
		return "", time.Time{}, time.Time{}, apperrors.Validation("month must use YYYY-MM")
	}
	return value, month, month.AddDate(0, 1, 0), nil
}

func validateID(id int) error {
	if id <= 0 {
		return apperrors.Validation("id must be a positive integer")
	}
	return nil
}
