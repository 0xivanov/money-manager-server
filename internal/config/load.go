package config

import (
	"os"
	"strings"
	"time"
)

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

	keySources, err := readEnableBankingKeySources()
	if err != nil {
		return Config{}, err
	}
	if err := loadAPNSCredentials(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadFCMCredentials(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.EnableBankingPrivateKey, err = keySources.load(); err != nil {
		return Config{}, err
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
	if cfg.APNSRequestTimeout, err = durationEnv("APNS_REQUEST_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.FCMRequestTimeout, err = durationEnv("FCM_REQUEST_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
