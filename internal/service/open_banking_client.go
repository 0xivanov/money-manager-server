package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/config"
	"money-manager-server/internal/enablebanking"
	"money-manager-server/internal/repository"
)

const (
	maximumInstitutionNameRunes      = 160
	maximumOpenBankingTextRunes      = 500
	maximumCallbackCodeBytes         = 4096
	maximumContinuationKeyBytes      = 4096
	maximumOpenBankingSyncRows       = 5000
	maximumOpenBankingSyncPages      = 100
	openBankingSyncBatchSize         = 10
	openBankingSyncInterval          = 6 * time.Hour
	openBankingSyncClaimTTL          = 5 * time.Minute
	defaultOpenBankingRequestTimeout = 20 * time.Second
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
	requestTimeout    time.Duration
}

func (s *Service) configureOpenBanking(cfg config.Config) {
	requestTimeout := cfg.EnableBankingRequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultOpenBankingRequestTimeout
	}
	s.openBankingConfig = openBankingServiceConfig{
		callbackURL:       cfg.EnableBankingCallbackURL,
		resultRedirectURL: cfg.EnableBankingResultRedirectURL,
		consentDays:       cfg.EnableBankingConsentDays,
		stateTTL:          cfg.EnableBankingStateTTL,
		requestTimeout:    requestTimeout,
	}
	if cfg.EnableBankingApplicationID == "" || cfg.EnableBankingPrivateKey == nil {
		return
	}
	client, err := enablebanking.New(enablebanking.Config{
		ApplicationID: cfg.EnableBankingApplicationID,
		PrivateKey:    cfg.EnableBankingPrivateKey,
		HTTPClient:    &http.Client{Timeout: requestTimeout},
	})
	if err != nil {
		s.openBankingError = err
		return
	}
	s.openBanking = client
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

func (s *Service) openBankingCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.openBankingConfig.requestTimeout
	if timeout <= 0 {
		timeout = defaultOpenBankingRequestTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
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
