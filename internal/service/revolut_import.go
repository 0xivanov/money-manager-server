package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

const revolutImportCategoryHeader = "money manager category"

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
	importCategories := make(map[string]string, 16)
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
		if isRevolutTopUpCSVType(field("type")) {
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
		requestedCategory := strings.ToLower(field(revolutImportCategoryHeader))
		if requestedCategory == "" {
			requestedCategory = classifyOpenBankingTransaction(transactionType, "", description).Category
		}
		categoryCacheKey := transactionType + "\x00" + requestedCategory
		category, ok := importCategories[categoryCacheKey]
		if !ok {
			var categoryErr error
			category, categoryErr = s.store.FindActiveCategoryName(ctx, userID, transactionType, requestedCategory)
			if errors.Is(categoryErr, repository.ErrNotFound) {
				return model.ImportResult{}, apperrors.Validation(fmt.Sprintf("row %d uses an unavailable category", rowIndex+2))
			}
			if categoryErr != nil {
				return model.ImportResult{}, apperrors.Internal(fmt.Errorf("find import category: %w", categoryErr))
			}
			importCategories[categoryCacheKey] = category
		}
		fingerprintRecord := record
		if categoryIndex, hasCategory := headers[revolutImportCategoryHeader]; hasCategory && categoryIndex < len(record) {
			fingerprintRecord = make([]string, 0, len(record)-1)
			fingerprintRecord = append(fingerprintRecord, record[:categoryIndex]...)
			fingerprintRecord = append(fingerprintRecord, record[categoryIndex+1:]...)
		}
		hash := sha256.Sum256([]byte(strings.Join(fingerprintRecord, "\x1f")))
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

func isRevolutTopUpCSVType(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", " ", "_").Replace(value)
	value = strings.Join(strings.FieldsFunc(value, func(character rune) bool {
		return character == '_'
	}), "_")
	switch value {
	case "TOPUP", "TOP_UP", "CARD_TOPUP", "CARD_TOP_UP",
		"TOPUP_RETURN", "TOP_UP_RETURN", "CARD_TOPUP_RETURN", "CARD_TOP_UP_RETURN":
		return true
	default:
		return false
	}
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
