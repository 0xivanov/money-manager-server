package config

import (
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const minimumJWTSecretBytes = 32

type Config struct {
	Port                           string
	DatabaseURL                    string
	JWTSecret                      string
	JWTIssuer                      string
	JWTAudience                    string
	JWTTTL                         time.Duration
	JWTLegacyAcceptUntil           time.Time
	DBMaxConns                     int32
	DBMinConns                     int32
	DBMaxConnLifetime              time.Duration
	DBMaxConnIdleTime              time.Duration
	DBHealthCheckPeriod            time.Duration
	StartupTimeout                 time.Duration
	MigrationTimeout               time.Duration
	ShutdownTimeout                time.Duration
	HTTPReadHeaderTimeout          time.Duration
	HTTPReadTimeout                time.Duration
	HTTPWriteTimeout               time.Duration
	HTTPIdleTimeout                time.Duration
	RequestBodyLimit               int64
	AuthRateLimit                  int
	AuthRateWindow                 time.Duration
	TrustedProxyCIDRs              []netip.Prefix
	TrustedProxyHops               int
	EnableBankingApplicationID     string
	EnableBankingPrivateKey        *rsa.PrivateKey
	EnableBankingCallbackURL       string
	EnableBankingResultRedirectURL string
	EnableBankingConsentDays       int
	EnableBankingStateTTL          time.Duration
	EnableBankingRequestTimeout    time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Port:                           envOrDefault("PORT", "8080"),
		DatabaseURL:                    strings.TrimSpace(os.Getenv("DATABASE_URL")),
		JWTSecret:                      os.Getenv("JWT_SECRET"),
		JWTIssuer:                      envOrDefault("JWT_ISSUER", "money-manager-api"),
		JWTAudience:                    envOrDefault("JWT_AUDIENCE", "money-manager-mobile"),
		EnableBankingApplicationID:     strings.TrimSpace(os.Getenv("ENABLE_BANKING_APPLICATION_ID")),
		EnableBankingCallbackURL:       strings.TrimSpace(os.Getenv("ENABLE_BANKING_CALLBACK_URL")),
		EnableBankingResultRedirectURL: strings.TrimSpace(os.Getenv("ENABLE_BANKING_RESULT_REDIRECT_URL")),
	}

	var err error

	keyPath := strings.TrimSpace(os.Getenv("ENABLE_BANKING_PRIVATE_KEY_PATH"))
	keyPEM := strings.TrimSpace(os.Getenv("ENABLE_BANKING_PRIVATE_KEY"))
	keyBase64 := strings.TrimSpace(os.Getenv("ENABLE_BANKING_PRIVATE_KEY_BASE64"))
	configuredKeySources := 0
	for _, value := range []string{keyPath, keyPEM, keyBase64} {
		if value != "" {
			configuredKeySources++
		}
	}
	if configuredKeySources > 1 {
		return Config{}, errors.New("configure only one Enable Banking private key source")
	}
	if keyBase64 != "" {
		decoded, decodeErr := base64.StdEncoding.DecodeString(keyBase64)
		if decodeErr != nil {
			return Config{}, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY_BASE64: decode base64: %w", decodeErr)
		}
		cfg.EnableBankingPrivateKey, err = parsePrivateKey(decoded)
		if err != nil {
			return Config{}, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY_BASE64: %w", err)
		}
	} else if keyPEM != "" {
		cfg.EnableBankingPrivateKey, err = parsePrivateKey([]byte(keyPEM))
		if err != nil {
			return Config{}, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY: %w", err)
		}
	} else if keyPath != "" {
		cfg.EnableBankingPrivateKey, err = loadPrivateKey(keyPath)
		if err != nil {
			return Config{}, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY_PATH: %w", err)
		}
	}

	if cfg.JWTTTL, err = durationEnv("JWT_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.JWTLegacyAcceptUntil, err = optionalTimeEnv("JWT_LEGACY_ACCEPT_UNTIL"); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxConns, err = int32Env("DB_MAX_CONNS", 10); err != nil {
		return Config{}, err
	}
	if cfg.DBMinConns, err = int32Env("DB_MIN_CONNS", 2); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxConnLifetime, err = durationEnv("DB_MAX_CONN_LIFETIME", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxConnIdleTime, err = durationEnv("DB_MAX_CONN_IDLE_TIME", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.DBHealthCheckPeriod, err = durationEnv("DB_HEALTH_CHECK_PERIOD", time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.StartupTimeout, err = durationEnv("STARTUP_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MigrationTimeout, err = durationEnv("MIGRATION_TIMEOUT", 3*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationEnv("SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.HTTPReadHeaderTimeout, err = durationEnv("HTTP_READ_HEADER_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.HTTPReadTimeout, err = durationEnv("HTTP_READ_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.HTTPWriteTimeout, err = durationEnv("HTTP_WRITE_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.HTTPIdleTimeout, err = durationEnv("HTTP_IDLE_TIMEOUT", time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.RequestBodyLimit, err = int64Env("REQUEST_BODY_LIMIT_BYTES", 64*1024); err != nil {
		return Config{}, err
	}
	if cfg.AuthRateLimit, err = intEnv("AUTH_RATE_LIMIT", 10); err != nil {
		return Config{}, err
	}
	if cfg.AuthRateWindow, err = durationEnv("AUTH_RATE_WINDOW", time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.TrustedProxyCIDRs, err = prefixListEnv("TRUSTED_PROXY_CIDRS"); err != nil {
		return Config{}, err
	}
	if cfg.TrustedProxyHops, err = intEnv("TRUSTED_PROXY_HOPS", 0); err != nil {
		return Config{}, err
	}
	if cfg.EnableBankingConsentDays, err = intEnv("ENABLE_BANKING_CONSENT_DAYS", 90); err != nil {
		return Config{}, err
	}
	if cfg.EnableBankingStateTTL, err = durationEnv("ENABLE_BANKING_STATE_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.EnableBankingRequestTimeout, err = durationEnv("ENABLE_BANKING_REQUEST_TIMEOUT", 20*time.Second); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

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
		"DB_MAX_CONN_LIFETIME":     c.DBMaxConnLifetime,
		"DB_MAX_CONN_IDLE_TIME":    c.DBMaxConnIdleTime,
		"DB_HEALTH_CHECK_PERIOD":   c.DBHealthCheckPeriod,
		"STARTUP_TIMEOUT":          c.StartupTimeout,
		"MIGRATION_TIMEOUT":        c.MigrationTimeout,
		"SHUTDOWN_TIMEOUT":         c.ShutdownTimeout,
		"HTTP_READ_HEADER_TIMEOUT": c.HTTPReadHeaderTimeout,
		"HTTP_READ_TIMEOUT":        c.HTTPReadTimeout,
		"HTTP_WRITE_TIMEOUT":       c.HTTPWriteTimeout,
		"HTTP_IDLE_TIMEOUT":        c.HTTPIdleTimeout,
		"AUTH_RATE_WINDOW":         c.AuthRateWindow,
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

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", name, err)
	}
	return parsed, nil
}

func intEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func int32Env(name string, fallback int32) (int32, error) {
	value, err := int64Env(name, int64(fallback))
	if err != nil {
		return 0, err
	}
	if value < -1<<31 || value > 1<<31-1 {
		return 0, fmt.Errorf("%s is outside the supported range", name)
	}
	return int32(value), nil
}

func int64Env(name string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func optionalTimeEnv(name string) (time.Time, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must use RFC3339: %w", name, err)
	}
	return parsed.UTC(), nil
}

func prefixListEnv(name string) ([]netip.Prefix, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("%s contains an invalid CIDR %q: %w", name, strings.TrimSpace(part), err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read PEM file: %w", err)
	}

	return parsePrivateKey(pemBytes)
}

func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA private key: %w", err)
	}

	return privateKey, nil
}
