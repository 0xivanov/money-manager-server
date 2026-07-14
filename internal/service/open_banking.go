package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/config"
	"money-manager-server/internal/enablebanking"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

const (
	maximumInstitutionNameRunes = 160
	maximumOpenBankingTextRunes = 500
	maximumCallbackCodeBytes    = 4096
	maximumContinuationKeyBytes = 4096
	maximumOpenBankingSyncRows  = 5000
	maximumOpenBankingSyncPages = 100
	openBankingSyncBatchSize    = 10
	openBankingSyncInterval     = 6 * time.Hour
	openBankingSyncClaimTTL     = 5 * time.Minute
)

type openBankingClient interface {
	ListInstitutions(context.Context, string, string) ([]enablebanking.Institution, error)
	StartAuthorization(context.Context, enablebanking.StartAuthorizationRequest) (enablebanking.StartAuthorizationResponse, error)
	AuthorizeSession(context.Context, string) (enablebanking.AuthorizeSessionResponse, error)
	GetSession(context.Context, string) (enablebanking.Session, error)
	DeleteSession(context.Context, string, enablebanking.PSUHeaders) error
	AccountDetails(context.Context, string, enablebanking.PSUHeaders) (json.RawMessage, error)
	AccountBalances(context.Context, string, enablebanking.PSUHeaders) (json.RawMessage, error)
	AccountTransactions(context.Context, string, url.Values, enablebanking.PSUHeaders) (json.RawMessage, error)
}

type openBankingServiceConfig struct {
	callbackURL       string
	resultRedirectURL string
	consentDays       int
	stateTTL          time.Duration
}

func (s *Service) configureOpenBanking(cfg config.Config) {
	s.openBankingConfig = openBankingServiceConfig{
		callbackURL:       cfg.EnableBankingCallbackURL,
		resultRedirectURL: cfg.EnableBankingResultRedirectURL,
		consentDays:       cfg.EnableBankingConsentDays,
		stateTTL:          cfg.EnableBankingStateTTL,
	}
	if cfg.EnableBankingApplicationID == "" || cfg.EnableBankingPrivateKey == nil {
		return
	}
	client, err := enablebanking.New(enablebanking.Config{
		ApplicationID: cfg.EnableBankingApplicationID,
		PrivateKey:    cfg.EnableBankingPrivateKey,
		HTTPClient:    &http.Client{Timeout: cfg.EnableBankingRequestTimeout},
	})
	if err != nil {
		s.openBankingError = err
		return
	}
	s.openBanking = client
}

func (s *Service) ListOpenBankingInstitutions(ctx context.Context, country, psuType string) ([]model.OpenBankingInstitution, error) {
	client, err := s.requireOpenBanking()
	if err != nil {
		return nil, err
	}
	country, err = normalizeCountry(country)
	if err != nil {
		return nil, err
	}
	psuType, err = normalizePSUType(psuType, true)
	if err != nil {
		return nil, err
	}
	institutions, err := client.ListInstitutions(ctx, country, psuType)
	if err != nil {
		return nil, mapOpenBankingProviderError("list institutions", err)
	}
	result := make([]model.OpenBankingInstitution, 0, len(institutions))
	for _, institution := range institutions {
		methods := make([]model.OpenBankingAuthMethod, 0, len(institution.AuthMethods))
		for _, method := range institution.AuthMethods {
			methods = append(methods, model.OpenBankingAuthMethod{
				Name: method.Name, Title: method.Title, PSUType: method.PSUType,
				Approach: method.Approach, HiddenMethod: method.HiddenMethod,
			})
		}
		result = append(result, model.OpenBankingInstitution{
			Name: institution.Name, Country: institution.Country, Logo: institution.Logo,
			PSUTypes: institution.PSUTypes, AuthMethods: methods,
			MaximumConsentValidity: institution.MaximumConsentValidity,
			Beta:                   institution.Beta, BIC: institution.BIC,
			RequiredPSUHeaders: institution.RequiredPSUHeaders,
		})
	}
	return result, nil
}

func (s *Service) StartOpenBankingAuthorization(
	ctx context.Context,
	userID int,
	request model.OpenBankingAuthorizationRequest,
) (model.OpenBankingAuthorization, error) {
	client, err := s.requireOpenBanking()
	if err != nil {
		return model.OpenBankingAuthorization{}, err
	}
	institutionName, err := normalizeInstitutionName(request.InstitutionName)
	if err != nil {
		return model.OpenBankingAuthorization{}, err
	}
	country, err := normalizeCountry(request.Country)
	if err != nil {
		return model.OpenBankingAuthorization{}, err
	}
	psuType, err := normalizePSUType(request.PSUType, true)
	if err != nil {
		return model.OpenBankingAuthorization{}, err
	}
	if psuType == "" {
		psuType = "personal"
	}
	language, err := normalizeOpenBankingLanguage(request.Language)
	if err != nil {
		return model.OpenBankingAuthorization{}, err
	}
	consentDays := request.ConsentDays
	if consentDays == 0 {
		consentDays = s.openBankingConfig.consentDays
	}
	if consentDays < 1 || consentDays > 180 {
		return model.OpenBankingAuthorization{}, apperrors.Validation("consent_days must be between 1 and 180")
	}

	institutions, err := client.ListInstitutions(ctx, country, psuType)
	if err != nil {
		return model.OpenBankingAuthorization{}, mapOpenBankingProviderError("verify institution", err)
	}
	var selected *enablebanking.Institution
	for index := range institutions {
		if strings.EqualFold(institutions[index].Name, institutionName) && institutions[index].Country == country {
			selected = &institutions[index]
			break
		}
	}
	if selected == nil {
		return model.OpenBankingAuthorization{}, apperrors.Validation("the selected institution is not available for this country and user type")
	}

	now := s.now().UTC()
	consentDuration := time.Duration(consentDays) * 24 * time.Hour
	if selected.MaximumConsentValidity > 0 {
		maximum := time.Duration(selected.MaximumConsentValidity) * time.Second
		if maximum < consentDuration {
			consentDuration = maximum
		}
	}
	validUntil := now.Add(consentDuration).Truncate(time.Second)
	state, stateHash, err := newOpenBankingState()
	if err != nil {
		return model.OpenBankingAuthorization{}, apperrors.Internal(fmt.Errorf("generate open banking state: %w", err))
	}
	expiresAt := now.Add(s.openBankingConfig.stateTTL).Truncate(time.Second)
	authorizationID, err := s.store.CreateOpenBankingAuthorization(ctx, repository.NewOpenBankingAuthorization{
		UserID: userID, StateHash: stateHash, InstitutionName: selected.Name,
		Country: country, PSUType: psuType, ValidUntil: validUntil, ExpiresAt: expiresAt,
	})
	if err != nil {
		return model.OpenBankingAuthorization{}, apperrors.Internal(fmt.Errorf("store open banking state: %w", err))
	}
	response, err := client.StartAuthorization(ctx, enablebanking.StartAuthorizationRequest{
		Access: enablebanking.Access{
			Balances: true, Transactions: true, ValidUntil: validUntil.Format(time.RFC3339),
		},
		Institution: enablebanking.ASPSP{Name: selected.Name, Country: country},
		State:       state, RedirectURL: s.openBankingConfig.callbackURL,
		PSUType: psuType, Language: language,
	})
	if err != nil {
		_ = s.store.FailOpenBankingAuthorization(ctx, authorizationID, "provider_error", truncateRunes(err.Error(), maximumOpenBankingTextRunes))
		return model.OpenBankingAuthorization{}, mapOpenBankingProviderError("start authorization", err)
	}
	if err := validateAuthorizationResponse(response); err != nil {
		_ = s.store.FailOpenBankingAuthorization(ctx, authorizationID, "invalid_provider_response", err.Error())
		return model.OpenBankingAuthorization{}, apperrors.Unavailable("bank connection is temporarily unavailable", err)
	}
	if err := s.store.SetOpenBankingAuthorizationProviderID(ctx, authorizationID, response.AuthorizationID); err != nil {
		return model.OpenBankingAuthorization{}, apperrors.Internal(fmt.Errorf("store authorization ID: %w", err))
	}
	return model.OpenBankingAuthorization{
		AuthorizationURL: response.URL,
		AuthorizationID:  response.AuthorizationID,
		ValidUntil:       validUntil.Format(time.RFC3339),
		ExpiresAt:        expiresAt.Format(time.RFC3339),
	}, nil
}

func (s *Service) CompleteOpenBankingAuthorization(
	ctx context.Context,
	request model.OpenBankingCallbackRequest,
) (model.OpenBankingCallbackResult, error) {
	client, err := s.requireOpenBanking()
	if err != nil {
		return s.openBankingCallbackResult("failed", "Bank connection is not configured", 0, "not_configured"), err
	}
	stateBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(request.State))
	if err != nil || len(stateBytes) != 32 {
		return model.OpenBankingCallbackResult{}, apperrors.Validation("invalid or expired open banking state")
	}
	stateSum := sha256.Sum256([]byte(strings.TrimSpace(request.State)))
	authorization, err := s.store.ClaimOpenBankingAuthorization(ctx, hex.EncodeToString(stateSum[:]), s.now().UTC())
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return model.OpenBankingCallbackResult{}, apperrors.Validation("invalid, expired, or already used open banking state")
		}
		return model.OpenBankingCallbackResult{}, apperrors.Internal(fmt.Errorf("claim open banking state: %w", err))
	}

	providerError := truncateBytes(strings.TrimSpace(request.Error), 120)
	providerDescription := truncateRunes(strings.TrimSpace(request.ErrorDescription), maximumOpenBankingTextRunes)
	if providerError != "" {
		if err := s.store.FailOpenBankingAuthorization(ctx, authorization.ID, providerError, providerDescription); err != nil {
			return model.OpenBankingCallbackResult{}, apperrors.Internal(fmt.Errorf("record declined bank authorization: %w", err))
		}
		return s.openBankingCallbackResult("cancelled", "Bank connection was cancelled", 0, "authorization_cancelled"), nil
	}
	code := strings.TrimSpace(request.Code)
	if code == "" || len(code) > maximumCallbackCodeBytes {
		_ = s.store.FailOpenBankingAuthorization(ctx, authorization.ID, "missing_code", "authorization callback did not contain a valid code")
		return s.openBankingCallbackResult("failed", "Bank connection could not be completed", 0, "missing_code"), apperrors.Validation("authorization callback did not contain a valid code")
	}

	session, err := client.AuthorizeSession(ctx, code)
	if err != nil {
		_ = s.store.FailOpenBankingAuthorization(ctx, authorization.ID, "session_exchange_failed", truncateRunes(err.Error(), maximumOpenBankingTextRunes))
		return s.openBankingCallbackResult("failed", "Bank connection could not be completed", 0, "session_exchange_failed"), mapOpenBankingProviderError("authorize session", err)
	}
	if strings.TrimSpace(session.SessionID) == "" {
		err := errors.New("enable banking session response has no session_id")
		_ = s.store.FailOpenBankingAuthorization(ctx, authorization.ID, "invalid_provider_response", err.Error())
		return s.openBankingCallbackResult("failed", "Bank connection could not be completed", 0, "invalid_provider_response"), apperrors.Unavailable("bank connection is temporarily unavailable", err)
	}
	validUntil := authorization.ValidUntil
	if parsed, parseErr := time.Parse(time.RFC3339, session.Access.ValidUntil); parseErr == nil {
		validUntil = parsed.UTC()
	}
	accounts := make([]repository.NewOpenBankingAccount, 0, len(session.Accounts))
	for _, account := range session.Accounts {
		identificationHash := strings.TrimSpace(account.IdentificationHash)
		if identificationHash == "" {
			sum := sha256.Sum256(append([]byte(session.SessionID+"\x00"+account.UID+"\x00"), account.Raw...))
			identificationHash = "derived:" + hex.EncodeToString(sum[:])
		}
		accounts = append(accounts, repository.NewOpenBankingAccount{
			ProviderAccountID: account.UID, IdentificationHash: identificationHash,
			Name:              truncateRunes(account.Name, maximumInstitutionNameRunes),
			Details:           truncateRunes(account.Details, maximumOpenBankingTextRunes),
			CashAccountType:   truncateRunes(account.CashAccountType, 40),
			Product:           truncateRunes(account.Product, maximumInstitutionNameRunes),
			Currency:          truncateRunes(strings.ToUpper(account.Currency), 12),
			DisplayIdentifier: maskedAccountIdentifier(account), ProviderPayload: account.Raw,
		})
	}
	connectionID, err := s.store.StoreOpenBankingConnection(ctx, repository.NewOpenBankingConnection{
		AuthorizationID: authorization.ID, UserID: authorization.UserID,
		ProviderSession: session.SessionID, InstitutionName: authorization.InstitutionName,
		Country: authorization.Country, PSUType: authorization.PSUType,
		Status: "AUTHORIZED", ValidUntil: validUntil, Accounts: accounts,
	})
	if err != nil {
		_ = client.DeleteSession(context.Background(), session.SessionID, enablebanking.PSUHeaders{})
		_ = s.store.FailOpenBankingAuthorization(ctx, authorization.ID, "storage_failed", truncateRunes(err.Error(), maximumOpenBankingTextRunes))
		return s.openBankingCallbackResult("failed", "Bank connection could not be saved", 0, "storage_failed"), apperrors.Internal(fmt.Errorf("store authorized bank session: %w", err))
	}
	return s.openBankingCallbackResult("connected", "Bank account connected", connectionID, ""), nil
}

func (s *Service) ListOpenBankingConnections(ctx context.Context, userID int) ([]model.OpenBankingConnection, error) {
	if _, err := s.requireOpenBanking(); err != nil {
		return nil, err
	}
	connections, err := s.store.ListOpenBankingConnections(ctx, userID)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list open banking connections: %w", err))
	}
	return connections, nil
}

func (s *Service) GetOpenBankingConnection(ctx context.Context, userID, connectionID int) (model.OpenBankingConnection, error) {
	client, err := s.requireOpenBanking()
	if err != nil {
		return model.OpenBankingConnection{}, err
	}
	record, err := s.store.GetOpenBankingConnection(ctx, userID, connectionID)
	if err != nil {
		return model.OpenBankingConnection{}, mapOpenBankingRepositoryNotFound(err, "bank connection not found")
	}
	session, err := client.GetSession(ctx, record.ProviderSession)
	if err != nil {
		return model.OpenBankingConnection{}, mapOpenBankingProviderError("get session", err)
	}
	if session.Status != "" && session.Status != record.Connection.Status {
		if err := s.store.UpdateOpenBankingConnectionStatus(ctx, userID, connectionID, session.Status); err != nil {
			return model.OpenBankingConnection{}, apperrors.Internal(fmt.Errorf("update bank connection status: %w", err))
		}
		record.Connection.Status = session.Status
		record.Connection.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	}
	return record.Connection, nil
}

func (s *Service) DeleteOpenBankingConnection(ctx context.Context, userID, connectionID int, psu model.OpenBankingPSUContext) error {
	client, err := s.requireOpenBanking()
	if err != nil {
		return err
	}
	record, err := s.store.GetOpenBankingConnection(ctx, userID, connectionID)
	if err != nil {
		return mapOpenBankingRepositoryNotFound(err, "bank connection not found")
	}
	if err := client.DeleteSession(ctx, record.ProviderSession, providerPSUHeaders(psu)); err != nil && !providerSessionAlreadyGone(err) {
		return mapOpenBankingProviderError("delete session", err)
	}
	if err := s.store.DeleteOpenBankingConnection(ctx, userID, connectionID); err != nil {
		return mapOpenBankingRepositoryNotFound(err, "bank connection not found")
	}
	return nil
}

func (s *Service) ListOpenBankingAccounts(ctx context.Context, userID int) ([]model.OpenBankingAccount, error) {
	if _, err := s.requireOpenBanking(); err != nil {
		return nil, err
	}
	accounts, err := s.store.ListOpenBankingAccounts(ctx, userID)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list open banking accounts: %w", err))
	}
	return accounts, nil
}

func (s *Service) GetOpenBankingAccountDetails(ctx context.Context, userID, accountID int, psu model.OpenBankingPSUContext) (model.OpenBankingProviderData, error) {
	client, account, err := s.openBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	if account.ProviderAccountID == "" {
		return account.ProviderPayload, nil
	}
	response, err := client.AccountDetails(ctx, account.ProviderAccountID, providerPSUHeaders(psu))
	if err != nil {
		return nil, mapOpenBankingProviderError("get account details", err)
	}
	return response, nil
}

func (s *Service) GetOpenBankingAccountBalances(ctx context.Context, userID, accountID int, psu model.OpenBankingPSUContext) (model.OpenBankingProviderData, error) {
	client, account, err := s.openBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	if account.ProviderAccountID == "" {
		return nil, apperrors.NotFound("balances are not available for this account")
	}
	response, err := client.AccountBalances(ctx, account.ProviderAccountID, providerPSUHeaders(psu))
	if err != nil {
		return nil, mapOpenBankingProviderError("get account balances", err)
	}
	return response, nil
}

func (s *Service) GetOpenBankingAccountTransactions(
	ctx context.Context,
	userID int,
	accountID int,
	dateFrom string,
	dateTo string,
	continuationKey string,
	transactionStatus string,
	strategy string,
	psu model.OpenBankingPSUContext,
) (model.OpenBankingProviderData, error) {
	client, account, err := s.openBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	if account.ProviderAccountID == "" {
		return nil, apperrors.NotFound("transactions are not available for this account")
	}
	query, err := openBankingTransactionQuery(dateFrom, dateTo, continuationKey, transactionStatus, strategy, s.now().UTC())
	if err != nil {
		return nil, err
	}
	response, err := client.AccountTransactions(ctx, account.ProviderAccountID, query, providerPSUHeaders(psu))
	if err != nil {
		return nil, mapOpenBankingProviderError("get account transactions", err)
	}
	return response, nil
}

func (s *Service) SyncOpenBankingAccount(
	ctx context.Context,
	userID int,
	accountID int,
	dateFrom string,
	dateTo string,
	psu model.OpenBankingPSUContext,
) (model.OpenBankingSyncResult, error) {
	client, account, err := s.openBankingAccount(ctx, userID, accountID)
	if err != nil {
		return model.OpenBankingSyncResult{}, err
	}
	if account.ProviderAccountID == "" {
		return model.OpenBankingSyncResult{}, apperrors.NotFound("transactions are not available for this account")
	}
	today := s.now().UTC().Truncate(24 * time.Hour)
	if strings.TrimSpace(dateFrom) == "" {
		from := today.AddDate(0, 0, -90)
		if lastSync, parseErr := time.Parse(time.RFC3339, account.Account.LastSyncedAt); parseErr == nil {
			from = lastSync.UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
		}
		dateFrom = from.Format("2006-01-02")
	}
	if strings.TrimSpace(dateTo) == "" {
		dateTo = today.Format("2006-01-02")
	}
	baseQuery, err := openBankingTransactionQuery(dateFrom, dateTo, "", "", "default", s.now().UTC())
	if err != nil {
		return model.OpenBankingSyncResult{}, err
	}
	from, _ := time.Parse("2006-01-02", dateFrom)
	through, _ := time.Parse("2006-01-02", dateTo)
	if through.Sub(from) > 366*24*time.Hour {
		return model.OpenBankingSyncResult{}, apperrors.Validation("bank sync date range cannot exceed 366 days")
	}

	seeds := make([]repository.OpenBankingTransactionSeed, 0)
	result := model.OpenBankingSyncResult{}
	continuationKey := ""
	seenContinuations := make(map[string]bool)
	for pageNumber := 0; pageNumber < maximumOpenBankingSyncPages; pageNumber++ {
		query := cloneURLValues(baseQuery)
		if continuationKey != "" {
			query.Set("continuation_key", continuationKey)
		}
		response, requestErr := client.AccountTransactions(
			ctx, account.ProviderAccountID, query, providerPSUHeaders(psu),
		)
		if requestErr != nil {
			return model.OpenBankingSyncResult{}, mapOpenBankingProviderError("sync account transactions", requestErr)
		}
		var page enableBankingTransactionsPage
		if err := json.Unmarshal(response, &page); err != nil {
			return model.OpenBankingSyncResult{}, apperrors.Unavailable(
				"bank data provider returned an invalid transaction response", err,
			)
		}
		result.Fetched += len(page.Transactions)
		if result.Fetched > maximumOpenBankingSyncRows {
			return model.OpenBankingSyncResult{}, apperrors.Unavailable(
				"bank data provider returned too many transactions", errors.New("sync row limit exceeded"),
			)
		}
		for _, raw := range page.Transactions {
			seed, include := normalizeOpenBankingTransaction(raw, today)
			if !include {
				result.Ignored++
				continue
			}
			seeds = append(seeds, seed)
		}
		continuationKey = strings.TrimSpace(page.ContinuationKey)
		if continuationKey == "" {
			break
		}
		if len(continuationKey) > maximumContinuationKeyBytes || seenContinuations[continuationKey] {
			return model.OpenBankingSyncResult{}, apperrors.Unavailable(
				"bank data provider returned an invalid continuation key", errors.New("invalid pagination state"),
			)
		}
		seenContinuations[continuationKey] = true
		if pageNumber == maximumOpenBankingSyncPages-1 {
			return model.OpenBankingSyncResult{}, apperrors.Unavailable(
				"bank transaction sync exceeded the page limit", errors.New("sync page limit exceeded"),
			)
		}
	}
	stored, err := s.store.ImportOpenBankingTransactions(
		ctx, userID, accountID, seeds, s.now().UTC().Truncate(time.Second),
	)
	if err != nil {
		return model.OpenBankingSyncResult{}, mapOpenBankingRepositoryNotFound(err, "bank account not found")
	}
	result.Imported = stored.Imported
	result.Updated = stored.Updated
	result.Unchanged = stored.Unchanged
	result.Notifications = stored.Notifications
	return result, nil
}

func (s *Service) RunOpenBankingSyncMaintenance(ctx context.Context) (model.OpenBankingMaintenanceResult, error) {
	if s.openBanking == nil {
		return model.OpenBankingMaintenanceResult{}, nil
	}
	now := s.now().UTC().Truncate(time.Second)
	accounts, err := s.store.ClaimOpenBankingAccountsForSync(
		ctx,
		now,
		now.Add(openBankingSyncInterval),
		now.Add(openBankingSyncClaimTTL),
		openBankingSyncBatchSize,
	)
	if err != nil {
		return model.OpenBankingMaintenanceResult{}, apperrors.Internal(
			fmt.Errorf("claim open banking accounts for sync: %w", err),
		)
	}
	result := model.OpenBankingMaintenanceResult{Claimed: len(accounts)}
	var syncErrors []error
	for _, account := range accounts {
		synced, syncErr := s.SyncOpenBankingAccount(
			ctx, account.UserID, account.AccountID, "", "", model.OpenBankingPSUContext{},
		)
		if syncErr != nil {
			result.Failed++
			syncErrors = append(syncErrors, fmt.Errorf("sync account %d: %w", account.AccountID, syncErr))
			if releaseErr := s.store.ReleaseOpenBankingSyncClaim(ctx, account.AccountID); releaseErr != nil {
				syncErrors = append(syncErrors, fmt.Errorf("release account %d sync claim: %w", account.AccountID, releaseErr))
			}
			continue
		}
		result.Succeeded++
		result.Imported += synced.Imported
		result.Updated += synced.Updated
		result.Notifications += synced.Notifications
	}
	if len(syncErrors) > 0 {
		return result, errors.Join(syncErrors...)
	}
	return result, nil
}

type enableBankingTransactionsPage struct {
	Transactions    []json.RawMessage `json:"transactions"`
	ContinuationKey string            `json:"continuation_key"`
}

type enableBankingTransaction struct {
	EntryReference       string `json:"entry_reference"`
	MerchantCategoryCode string `json:"merchant_category_code"`
	TransactionAmount    struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	} `json:"transaction_amount"`
	CreditDebitIndicator string `json:"credit_debit_indicator"`
	Status               string `json:"status"`
	BookingDate          string `json:"booking_date"`
	TransactionDate      string `json:"transaction_date"`
	ValueDate            string `json:"value_date"`
	Creditor             struct {
		Name string `json:"name"`
	} `json:"creditor"`
	Debtor struct {
		Name string `json:"name"`
	} `json:"debtor"`
	BankTransactionCode struct {
		Description string `json:"description"`
		Code        string `json:"code"`
		SubCode     string `json:"sub_code"`
	} `json:"bank_transaction_code"`
	RemittanceInformation []string `json:"remittance_information"`
	Note                  string   `json:"note"`
	TransactionID         string   `json:"transaction_id"`
}

func normalizeOpenBankingTransaction(
	raw json.RawMessage,
	today time.Time,
) (repository.OpenBankingTransactionSeed, bool) {
	var transaction enableBankingTransaction
	if len(raw) == 0 || json.Unmarshal(raw, &transaction) != nil {
		return repository.OpenBankingTransactionSeed{}, false
	}
	currency := strings.ToUpper(strings.TrimSpace(transaction.TransactionAmount.Currency))
	if currency != supportedCurrency {
		return repository.OpenBankingTransactionSeed{}, false
	}
	rawAmount := strings.TrimSpace(transaction.TransactionAmount.Amount)
	negative := strings.HasPrefix(rawAmount, "-")
	rawAmount = strings.TrimPrefix(strings.TrimPrefix(rawAmount, "+"), "-")
	amountNumber, ok := new(big.Rat).SetString(rawAmount)
	if !ok || amountNumber.Sign() <= 0 || amountNumber.Cmp(big.NewRat(99999999999999, 100)) > 0 {
		return repository.OpenBankingTransactionSeed{}, false
	}
	amount := amountNumber.FloatString(2)

	indicator := strings.ToUpper(strings.TrimSpace(transaction.CreditDebitIndicator))
	transactionType := ""
	switch indicator {
	case "DBIT":
		transactionType = "expense"
	case "CRDT":
		transactionType = "income"
	default:
		if negative {
			transactionType = "expense"
		} else {
			return repository.OpenBankingTransactionSeed{}, false
		}
	}
	occurredAt := firstValidOpenBankingDate(
		transaction.BookingDate, transaction.TransactionDate, transaction.ValueDate,
	)
	if occurredAt.IsZero() || occurredAt.After(today) {
		return repository.OpenBankingTransactionSeed{}, false
	}
	status := openBankingTransactionStatus(transaction.Status)
	description := openBankingTransactionDescription(transaction, transactionType)
	category := openBankingTransactionCategory(
		transactionType, transaction.MerchantCategoryCode, description,
	)
	// Enable Banking documents entry_reference as stable across transaction-list
	// retrievals. transaction_id is only a detail lookup key and may change.
	externalID := strings.TrimSpace(transaction.EntryReference)
	if externalID == "" {
		externalID = strings.TrimSpace(transaction.TransactionID)
	}
	if externalID == "" {
		sum := sha256.Sum256(raw)
		externalID = "derived:" + hex.EncodeToString(sum[:])
	}
	if len(externalID) > 500 {
		sum := sha256.Sum256([]byte(externalID))
		externalID = "hashed:" + hex.EncodeToString(sum[:])
	}
	metadata, err := json.Marshal(map[string]string{
		"provider_status":          strings.ToUpper(strings.TrimSpace(transaction.Status)),
		"entry_reference":          truncateBytes(strings.TrimSpace(transaction.EntryReference), 500),
		"merchant_category_code":   truncateBytes(strings.TrimSpace(transaction.MerchantCategoryCode), 20),
		"bank_transaction_code":    truncateBytes(strings.TrimSpace(transaction.BankTransactionCode.Code), 40),
		"bank_transaction_subcode": truncateBytes(strings.TrimSpace(transaction.BankTransactionCode.SubCode), 40),
	})
	if err != nil {
		return repository.OpenBankingTransactionSeed{}, false
	}
	return repository.OpenBankingTransactionSeed{
		ExternalID: externalID, Type: transactionType, Category: category,
		Description: description, Amount: amount, Currency: currency,
		OccurredAt: occurredAt, Status: status, Metadata: metadata,
	}, true
}

func firstValidOpenBankingDate(values ...string) time.Time {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) >= 10 {
			value = value[:10]
		}
		if date, err := time.Parse("2006-01-02", value); err == nil {
			return date
		}
	}
	return time.Time{}
}

func openBankingTransactionStatus(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "BOOK":
		return "booked"
	case "CNCL", "RJCT":
		return "cancelled"
	default:
		return "pending"
	}
}

func openBankingTransactionDescription(transaction enableBankingTransaction, transactionType string) string {
	candidates := []string{}
	if transactionType == "expense" {
		candidates = append(candidates, transaction.Creditor.Name)
	} else {
		candidates = append(candidates, transaction.Debtor.Name)
	}
	candidates = append(candidates, transaction.RemittanceInformation...)
	candidates = append(candidates, transaction.Note, transaction.BankTransactionCode.Description)
	for _, candidate := range candidates {
		if value := truncateRunes(candidate, maximumDescriptionRunes); value != "" {
			return value
		}
	}
	return "Bank transaction"
}

func openBankingTransactionCategory(transactionType, mcc, description string) string {
	if transactionType == "income" {
		lower := strings.ToLower(description)
		if strings.Contains(lower, "salary") || strings.Contains(lower, "payroll") {
			return "salary"
		}
		if strings.Contains(lower, "refund") || strings.Contains(lower, "reversal") {
			return "refund"
		}
		return "other"
	}
	code, _ := strconv.Atoi(strings.TrimSpace(mcc))
	switch {
	case code == 5411 || code >= 5811 && code <= 5814:
		return "food"
	case code == 4111 || code == 4121 || code == 4131 || code == 4789 ||
		code == 5541 || code == 5542 || code == 7523:
		return "transport"
	case code == 4900 || code == 4814 || code == 4899:
		return "utilities"
	case code == 6513:
		return "housing"
	case code == 5912 || code >= 8011 && code <= 8099:
		return "health"
	case code == 7832 || code == 7922 || code >= 7991 && code <= 7999:
		return "entertainment"
	case code >= 3000 && code <= 3999 || code == 4511 || code == 4722 || code == 7011:
		return "travel"
	case code >= 5000 && code <= 5999:
		return "shopping"
	default:
		return "other"
	}
}

func cloneURLValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, items := range values {
		clone[key] = append([]string(nil), items...)
	}
	return clone
}

func (s *Service) openBankingAccount(ctx context.Context, userID, accountID int) (openBankingClient, repository.OpenBankingAccountRecord, error) {
	client, err := s.requireOpenBanking()
	if err != nil {
		return nil, repository.OpenBankingAccountRecord{}, err
	}
	account, err := s.store.GetOpenBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, repository.OpenBankingAccountRecord{}, mapOpenBankingRepositoryNotFound(err, "bank account not found")
	}
	return client, account, nil
}

func (s *Service) requireOpenBanking() (openBankingClient, error) {
	if s.openBanking != nil {
		return s.openBanking, nil
	}
	cause := s.openBankingError
	if cause == nil {
		cause = errors.New("enable banking credentials are not configured")
	}
	return nil, apperrors.Unavailable("open banking is not configured", cause)
}

func (s *Service) openBankingCallbackResult(status, message string, connectionID int, errorCode string) model.OpenBankingCallbackResult {
	result := model.OpenBankingCallbackResult{Status: status, Message: message, ConnectionID: connectionID}
	if s.openBankingConfig.resultRedirectURL == "" {
		return result
	}
	parsed, err := url.Parse(s.openBankingConfig.resultRedirectURL)
	if err != nil {
		return result
	}
	query := parsed.Query()
	query.Set("status", status)
	if connectionID > 0 {
		query.Set("connection_id", fmt.Sprintf("%d", connectionID))
	}
	if errorCode != "" {
		query.Set("error", errorCode)
	}
	parsed.RawQuery = query.Encode()
	result.RedirectURL = parsed.String()
	return result
}

func newOpenBankingState() (string, string, error) {
	contents := make([]byte, 32)
	if _, err := rand.Read(contents); err != nil {
		return "", "", err
	}
	state := base64.RawURLEncoding.EncodeToString(contents)
	sum := sha256.Sum256([]byte(state))
	return state, hex.EncodeToString(sum[:]), nil
}

func normalizeCountry(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 2 {
		return "", apperrors.Validation("country must be a two-letter ISO 3166-1 code")
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return "", apperrors.Validation("country must be a two-letter ISO 3166-1 code")
		}
	}
	return value, nil
}

func normalizePSUType(value string, optional bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" && optional {
		return "", nil
	}
	if value != "personal" && value != "business" {
		return "", apperrors.Validation("psu_type must be personal or business")
	}
	return value, nil
}

func normalizeInstitutionName(value string) (string, error) {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || utf8.RuneCountInString(value) > maximumInstitutionNameRunes {
		return "", apperrors.Validation("institution_name must contain between 1 and 160 characters")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", apperrors.Validation("institution_name contains unsupported characters")
		}
	}
	return value, nil
}

func normalizeOpenBankingLanguage(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if len(value) != 2 || value[0] < 'a' || value[0] > 'z' || value[1] < 'a' || value[1] > 'z' {
		return "", apperrors.Validation("language must be a two-letter ISO 639-1 code")
	}
	return value, nil
}

func validateAuthorizationResponse(response enablebanking.StartAuthorizationResponse) error {
	parsed, err := url.Parse(response.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("enable banking returned an invalid authorization URL")
	}
	if strings.TrimSpace(response.AuthorizationID) == "" {
		return errors.New("enable banking returned no authorization ID")
	}
	return nil
}

func maskedAccountIdentifier(account enablebanking.Account) string {
	identifier := strings.TrimSpace(account.AccountID.IBAN)
	if identifier == "" {
		identifier = strings.TrimSpace(account.AccountID.Other.Identification)
	}
	identifier = strings.ReplaceAll(identifier, " ", "")
	if identifier == "" {
		return ""
	}
	runes := []rune(identifier)
	if len(runes) > 4 {
		runes = runes[len(runes)-4:]
	}
	return "•••• " + string(runes)
}

func openBankingTransactionQuery(dateFrom, dateTo, continuationKey, transactionStatus, strategy string, now time.Time) (url.Values, error) {
	query := url.Values{}
	var from time.Time
	var err error
	if dateFrom != "" {
		from, err = time.Parse("2006-01-02", dateFrom)
		if err != nil {
			return nil, apperrors.Validation("date_from must use YYYY-MM-DD")
		}
		if from.After(now) {
			return nil, apperrors.Validation("date_from cannot be in the future")
		}
		query.Set("date_from", dateFrom)
	}
	if dateTo != "" {
		if from.IsZero() {
			return nil, apperrors.Validation("date_from is required when date_to is provided")
		}
		to, parseErr := time.Parse("2006-01-02", dateTo)
		if parseErr != nil {
			return nil, apperrors.Validation("date_to must use YYYY-MM-DD")
		}
		if to.Before(from) {
			return nil, apperrors.Validation("date_to cannot be before date_from")
		}
		if to.After(now) {
			return nil, apperrors.Validation("date_to cannot be in the future")
		}
		query.Set("date_to", dateTo)
	}
	continuationKey = strings.TrimSpace(continuationKey)
	if len(continuationKey) > maximumContinuationKeyBytes {
		return nil, apperrors.Validation("continuation_key is too long")
	}
	if continuationKey != "" {
		query.Set("continuation_key", continuationKey)
	}
	transactionStatus = strings.ToUpper(strings.TrimSpace(transactionStatus))
	if transactionStatus != "" {
		valid := map[string]bool{"BOOK": true, "CNCL": true, "HOLD": true, "OTHR": true, "PDNG": true, "RJCT": true, "SCHD": true}
		if !valid[transactionStatus] {
			return nil, apperrors.Validation("transaction_status is not supported")
		}
		query.Set("transaction_status", transactionStatus)
	}
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if strategy != "" {
		if strategy != "default" && strategy != "longest" {
			return nil, apperrors.Validation("strategy must be default or longest")
		}
		query.Set("strategy", strategy)
	}
	return query, nil
}

func providerPSUHeaders(psu model.OpenBankingPSUContext) enablebanking.PSUHeaders {
	return enablebanking.PSUHeaders{
		IPAddress: psu.IPAddress, UserAgent: psu.UserAgent, Referer: psu.Referer,
		Accept: psu.Accept, AcceptCharset: psu.AcceptCharset, AcceptEncoding: psu.AcceptEncoding,
		AcceptLanguage: psu.AcceptLanguage,
	}
}

func enableBankingEmptyPSUHeaders() enablebanking.PSUHeaders { return enablebanking.PSUHeaders{} }

func providerSessionAlreadyGone(err error) bool {
	var providerErr *enablebanking.ProviderError
	if !errors.As(err, &providerErr) {
		return false
	}
	if providerErr.StatusCode == http.StatusNotFound {
		return true
	}
	switch providerErr.Code {
	case "CLOSED_SESSION", "EXPIRED_SESSION", "SESSION_DOES_NOT_EXIST":
		return true
	default:
		return false
	}
}

func mapOpenBankingProviderError(operation string, err error) error {
	return apperrors.Unavailable("bank data provider is temporarily unavailable", fmt.Errorf("%s: %w", operation, err))
}

func mapOpenBankingRepositoryNotFound(err error, message string) error {
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound(message)
	}
	return apperrors.Internal(err)
}

func truncateRunes(value string, maximum int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maximum {
		runes = runes[:maximum]
	}
	return string(runes)
}

func truncateBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
