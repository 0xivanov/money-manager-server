package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c Config) Validate() error {
	return c.validateAt(time.Now().UTC())
}

func (c Config) validateAt(now time.Time) error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len([]byte(c.JWTSecret)) < minimumJWTSecretBytes {
		errs = append(errs, fmt.Errorf("JWT_SECRET must be at least %d bytes", minimumJWTSecretBytes))
	}
	if strings.TrimSpace(c.JWTIssuer) == "" {
		errs = append(errs, errors.New("JWT_ISSUER is required"))
	}
	if strings.TrimSpace(c.JWTAudience) == "" {
		errs = append(errs, errors.New("JWT_AUDIENCE is required"))
	}
	if c.JWTTTL <= 0 {
		errs = append(errs, errors.New("JWT_TTL must be positive"))
	}
	if !c.JWTLegacyAcceptUntil.IsZero() && c.JWTTTL > 0 && c.JWTLegacyAcceptUntil.After(now.Add(c.JWTTTL)) {
		errs = append(errs, errors.New("JWT_LEGACY_ACCEPT_UNTIL must be no later than one JWT_TTL from startup"))
	}
	if c.DBMaxConns < 1 {
		errs = append(errs, errors.New("DB_MAX_CONNS must be at least 1"))
	}
	if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
		errs = append(errs, errors.New("DB_MIN_CONNS must be between 0 and DB_MAX_CONNS"))
	}
	for name, value := range map[string]time.Duration{
		"DB_MAX_CONN_LIFETIME":        c.DBMaxConnLifetime,
		"DB_MAX_CONN_IDLE_TIME":       c.DBMaxConnIdleTime,
		"DB_HEALTH_CHECK_PERIOD":      c.DBHealthCheckPeriod,
		"STARTUP_TIMEOUT":             c.StartupTimeout,
		"MIGRATION_TIMEOUT":           c.MigrationTimeout,
		"SHUTDOWN_TIMEOUT":            c.ShutdownTimeout,
		"HTTP_READ_HEADER_TIMEOUT":    c.HTTPReadHeaderTimeout,
		"HTTP_READ_TIMEOUT":           c.HTTPReadTimeout,
		"HTTP_WRITE_TIMEOUT":          c.HTTPWriteTimeout,
		"HTTP_IDLE_TIMEOUT":           c.HTTPIdleTimeout,
		"AUTH_RATE_WINDOW":            c.AuthRateWindow,
		"MARKET_DATA_REQUEST_TIMEOUT": c.MarketDataRequestTimeout,
	} {
		if value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", name))
		}
	}
	if c.RequestBodyLimit < 1024 {
		errs = append(errs, errors.New("REQUEST_BODY_LIMIT_BYTES must be at least 1024"))
	}
	if c.AuthRateLimit < 1 {
		errs = append(errs, errors.New("AUTH_RATE_LIMIT must be at least 1"))
	}
	if c.MarketDataRequestTimeout > time.Minute {
		errs = append(errs, errors.New("MARKET_DATA_REQUEST_TIMEOUT must be no more than 1m"))
	}
	if c.TrustedProxyHops < 0 {
		errs = append(errs, errors.New("TRUSTED_PROXY_HOPS cannot be negative"))
	}
	if (len(c.TrustedProxyCIDRs) == 0) != (c.TrustedProxyHops == 0) {
		errs = append(errs, errors.New("TRUSTED_PROXY_CIDRS and a positive TRUSTED_PROXY_HOPS must be configured together"))
	}
	credentialsConfigured := strings.TrimSpace(c.EnableBankingApplicationID) != "" || c.EnableBankingPrivateKey != nil
	if (strings.TrimSpace(c.EnableBankingApplicationID) == "") != (c.EnableBankingPrivateKey == nil) {
		errs = append(errs, errors.New("ENABLE_BANKING_APPLICATION_ID and an Enable Banking private key must be configured together"))
	}
	if credentialsConfigured {
		if strings.TrimSpace(c.EnableBankingCallbackURL) == "" {
			errs = append(errs, errors.New("ENABLE_BANKING_CALLBACK_URL is required when Enable Banking is configured"))
		} else if err := validateCallbackURL(c.EnableBankingCallbackURL); err != nil {
			errs = append(errs, err)
		}
	}
	if c.EnableBankingResultRedirectURL != "" {
		parsed, err := url.Parse(c.EnableBankingResultRedirectURL)
		if err != nil || parsed.Scheme == "" || parsed.User != nil || parsed.Fragment != "" {
			errs = append(errs, errors.New("ENABLE_BANKING_RESULT_REDIRECT_URL must be an absolute URL without credentials or a fragment"))
		}
	}
	if c.EnableBankingConsentDays < 1 || c.EnableBankingConsentDays > 180 {
		errs = append(errs, errors.New("ENABLE_BANKING_CONSENT_DAYS must be between 1 and 180"))
	}
	if c.EnableBankingStateTTL < time.Minute || c.EnableBankingStateTTL > time.Hour {
		errs = append(errs, errors.New("ENABLE_BANKING_STATE_TTL must be between 1m and 1h"))
	}
	if c.EnableBankingRequestTimeout <= 0 || c.EnableBankingRequestTimeout > time.Minute {
		errs = append(errs, errors.New("ENABLE_BANKING_REQUEST_TIMEOUT must be positive and no more than 1m"))
	}
	apnsConfiguredValues := 0
	for _, configured := range []bool{
		c.APNSKeyID != "", c.APNSTeamID != "", c.APNSBundleID != "", c.APNSPrivateKey != nil,
	} {
		if configured {
			apnsConfiguredValues++
		}
	}
	if apnsConfiguredValues != 0 && apnsConfiguredValues != 4 {
		errs = append(errs, errors.New("APNS_KEY_ID, APNS_TEAM_ID, APNS_BUNDLE_ID, and APNS_PRIVATE_KEY_BASE64 must be configured together"))
	}
	if c.APNSRequestTimeout <= 0 || c.APNSRequestTimeout > time.Minute {
		errs = append(errs, errors.New("APNS_REQUEST_TIMEOUT must be positive and no more than 1m"))
	}
	if c.FCMRequestTimeout <= 0 || c.FCMRequestTimeout > time.Minute {
		errs = append(errs, errors.New("FCM_REQUEST_TIMEOUT must be positive and no more than 1m"))
	}
	if port, err := strconv.Atoi(c.Port); err != nil || port < 1 || port > 65535 {
		errs = append(errs, errors.New("PORT must be between 1 and 65535"))
	}
	return errors.Join(errs...)
}

func validateCallbackURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("ENABLE_BANKING_CALLBACK_URL must be an absolute HTTP URL without credentials or a fragment")
	}
	if parsed.Scheme == "http" {
		host := strings.ToLower(parsed.Hostname())
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return errors.New("ENABLE_BANKING_CALLBACK_URL must use HTTPS outside localhost")
		}
	}
	return nil
}
