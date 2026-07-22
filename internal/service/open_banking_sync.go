package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

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
			seed, include := normalizeOpenBankingTransactionForInstitution(
				raw, today, account.Account.InstitutionName,
			)
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
	EntryReference       string          `json:"entry_reference"`
	MerchantCategoryCode json.RawMessage `json:"merchant_category_code"`
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
	return normalizeOpenBankingTransactionForInstitution(raw, today, "")
}

func normalizeOpenBankingTransactionForInstitution(
	raw json.RawMessage,
	today time.Time,
	institutionName string,
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
	if isRevolutTopUpTransaction(institutionName, transaction) {
		return repository.OpenBankingTransactionSeed{}, false
	}
	occurredAt := firstValidOpenBankingDate(
		transaction.BookingDate, transaction.TransactionDate, transaction.ValueDate,
	)
	if occurredAt.IsZero() || occurredAt.After(today) {
		return repository.OpenBankingTransactionSeed{}, false
	}
	status := openBankingTransactionStatus(transaction.Status)
	description := openBankingTransactionDescription(transaction, transactionType)
	merchantCategoryCode := openBankingMerchantCategoryCode(transaction.MerchantCategoryCode)
	classification := classifyOpenBankingTransaction(
		transactionType,
		merchantCategoryCode,
		openBankingTransactionClassificationText(transaction, description),
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
	metadata, err := json.Marshal(map[string]any{
		"provider_status":          strings.ToUpper(strings.TrimSpace(transaction.Status)),
		"entry_reference":          truncateBytes(strings.TrimSpace(transaction.EntryReference), 500),
		"merchant_category_code":   truncateBytes(merchantCategoryCode, 20),
		"bank_transaction_code":    truncateBytes(strings.TrimSpace(transaction.BankTransactionCode.Code), 40),
		"bank_transaction_subcode": truncateBytes(strings.TrimSpace(transaction.BankTransactionCode.SubCode), 40),
		"classification_source":    classification.Source,
		"classified_category":      classification.Category,
		"classified_type":          transactionType,
		"category_source":          classification.Source,
	})
	if err != nil {
		return repository.OpenBankingTransactionSeed{}, false
	}
	return repository.OpenBankingTransactionSeed{
		ExternalID: externalID, Type: transactionType, Category: classification.Category,
		Description: description, Amount: amount, Currency: currency,
		OccurredAt: occurredAt, Status: status, Metadata: metadata,
	}, true
}

func isRevolutTopUpTransaction(institutionName string, transaction enableBankingTransaction) bool {
	if !strings.Contains(strings.ToLower(strings.TrimSpace(institutionName)), "revolut") {
		return false
	}
	code := strings.ToUpper(strings.TrimSpace(transaction.BankTransactionCode.Code))
	code = strings.NewReplacer("-", "", "_", "", " ", "").Replace(code)
	switch code {
	case "TOPUP", "CARDTOPUP", "TOPUPRETURN", "CARDTOPUPRETURN":
		return true
	}
	text := strings.ToLower(openBankingTransactionClassificationText(transaction, ""))
	text = strings.Join(strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(text)), " ")
	for _, marker := range []string{
		"card top up",
		"card topup",
		"top up by bank card",
		"topup by bank card",
		"top up return",
		"topup return",
		"cash deposit by bank card",
		"cash deposit",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func openBankingMerchantCategoryCode(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return ""
		}
		value = strings.TrimSpace(decoded)
	} else {
		var decoded json.Number
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return ""
		}
		value = decoded.String()
	}
	if number, ok := new(big.Rat).SetString(value); ok && number.Denom().Cmp(big.NewInt(1)) == 0 && number.Num().IsInt64() {
		code := number.Num().Int64()
		if code >= 0 && code <= 9999 {
			return fmt.Sprintf("%04d", code)
		}
	}
	return value
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

func openBankingTransactionClassificationText(transaction enableBankingTransaction, description string) string {
	values := []string{description, transaction.Creditor.Name, transaction.Debtor.Name}
	values = append(values, transaction.RemittanceInformation...)
	values = append(values, transaction.Note, transaction.BankTransactionCode.Description)
	return strings.Join(values, " ")
}

func cloneURLValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, items := range values {
		clone[key] = append([]string(nil), items...)
	}
	return clone
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
