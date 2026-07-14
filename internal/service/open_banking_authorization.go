package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/enablebanking"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

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
		cleanupCtx, cancelCleanup := s.openBankingCleanupContext(ctx)
		_ = client.DeleteSession(cleanupCtx, session.SessionID, enablebanking.PSUHeaders{})
		cancelCleanup()
		_ = s.store.FailOpenBankingAuthorization(ctx, authorization.ID, "storage_failed", truncateRunes(err.Error(), maximumOpenBankingTextRunes))
		return s.openBankingCallbackResult("failed", "Bank connection could not be saved", 0, "storage_failed"), apperrors.Internal(fmt.Errorf("store authorized bank session: %w", err))
	}
	return s.openBankingCallbackResult("connected", "Bank account connected", connectionID, ""), nil
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
