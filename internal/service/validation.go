package service

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"money-manager-server/internal/apperrors"
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
