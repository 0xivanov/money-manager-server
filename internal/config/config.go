package config

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"net/netip"
	"time"
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
	MarketDataRequestTimeout       time.Duration
	APNSKeyID                      string
	APNSTeamID                     string
	APNSBundleID                   string
	APNSPrivateKey                 *ecdsa.PrivateKey
	APNSRequestTimeout             time.Duration
	FCMProjectID                   string
	FCMClientEmail                 string
	FCMPrivateKey                  *rsa.PrivateKey
	FCMRequestTimeout              time.Duration
}
