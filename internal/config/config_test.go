package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresSecretsAndParsesOverrides(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("JWT_TTL", "2h")
	t.Setenv("DB_MAX_CONNS", "12")
	t.Setenv("DB_MIN_CONNS", "3")
	t.Setenv("REQUEST_BODY_LIMIT_BYTES", "4096")
	t.Setenv("MIGRATION_TIMEOUT", "4m")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.42.0.0/16, 10.8.0.0/24")
	t.Setenv("TRUSTED_PROXY_HOPS", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.JWTTTL != 2*time.Hour || cfg.DBMaxConns != 12 || cfg.DBMinConns != 3 {
		t.Fatalf("unexpected parsed config: %#v", cfg)
	}
	if cfg.RequestBodyLimit != 4096 || cfg.MigrationTimeout != 4*time.Minute || len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyHops != 1 {
		t.Fatalf("unexpected HTTP config: %#v", cfg)
	}
}

func TestLoadDoesNotTrustProxyHeadersByDefault(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.TrustedProxyCIDRs) != 0 || cfg.TrustedProxyHops != 0 {
		t.Fatalf("proxy trust must default off: %#v", cfg)
	}
}

func TestLoadRejectsUnsafeLegacyWindowAndIncompleteProxyTrust(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("JWT_TTL", "1h")
	t.Setenv("JWT_LEGACY_ACCEPT_UNTIL", time.Now().UTC().Add(2*time.Hour).Format(time.RFC3339))
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.42.0.0/16")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "one JWT_TTL") || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("unexpected safety validation error: %v", err)
	}
}

func TestLoadRejectsMissingAndUnsafeValues(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("JWT_SECRET", "short")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected an error")
	}
	message := err.Error()
	if !strings.Contains(message, "DATABASE_URL is required") || !strings.Contains(message, "JWT_SECRET must be at least 32 bytes") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("JWT_TTL", "tomorrow")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_TTL") {
		t.Fatalf("expected JWT_TTL error, got %v", err)
	}
}

func TestLoadKeepsOpenBankingOptionalAndReturnsKeyErrors(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	if _, err := Load(); err != nil {
		t.Fatalf("optional open banking configuration failed: %v", err)
	}

	t.Setenv("ENABLE_BANKING_APPLICATION_ID", "application-id")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("partial credential error = %v", err)
	}

	t.Setenv("ENABLE_BANKING_PRIVATE_KEY_PATH", "/definitely/missing/private-key.pem")
	t.Setenv("ENABLE_BANKING_CALLBACK_URL", "https://money.example/api/open-banking/callback")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ENABLE_BANKING_PRIVATE_KEY_PATH") {
		t.Fatalf("private key read error = %v", err)
	}
}

func TestLoadAcceptsBase64OpenBankingKey(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encodedPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	t.Setenv("ENABLE_BANKING_APPLICATION_ID", "application-id")
	t.Setenv("ENABLE_BANKING_PRIVATE_KEY_BASE64", base64.StdEncoding.EncodeToString(encodedPEM))
	t.Setenv("ENABLE_BANKING_CALLBACK_URL", "https://money.example/api/open-banking/callback")

	cfg, err := Load()
	if err != nil || cfg.EnableBankingPrivateKey == nil || cfg.EnableBankingApplicationID != "application-id" {
		t.Fatalf("base64 open banking configuration = %#v, %v", cfg, err)
	}
}

func TestLoadAcceptsBase64APNSKeyAndRejectsPartialConfiguration(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("APNS_KEY_ID", "ABCDEFGHIJ")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("partial APNs configuration error = %v", err)
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encodedPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	t.Setenv("APNS_TEAM_ID", "KLMNOPQRST")
	t.Setenv("APNS_BUNDLE_ID", "org.moneymanager.ios")
	t.Setenv("APNS_PRIVATE_KEY_BASE64", base64.StdEncoding.EncodeToString(encodedPEM))

	cfg, err := Load()
	if err != nil || cfg.APNSPrivateKey == nil || cfg.APNSBundleID != "org.moneymanager.ios" {
		t.Fatalf("base64 APNs configuration = %#v, %v", cfg, err)
	}
}

func TestLoadAcceptsBase64FCMServiceAccount(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://money:money@localhost/money")
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	serviceAccount := `{"project_id":"money-manager","client_email":"push@money-manager.iam.gserviceaccount.com","private_key":` +
		strconv.Quote(string(privateKeyPEM)) + `}`
	t.Setenv("FCM_SERVICE_ACCOUNT_BASE64", base64.StdEncoding.EncodeToString([]byte(serviceAccount)))
	cfg, err := Load()
	if err != nil || cfg.FCMPrivateKey == nil || cfg.FCMProjectID != "money-manager" {
		t.Fatalf("base64 FCM configuration = %#v, %v", cfg, err)
	}
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"PORT", "DATABASE_URL", "JWT_SECRET", "JWT_ISSUER", "JWT_AUDIENCE", "JWT_TTL", "JWT_LEGACY_ACCEPT_UNTIL",
		"DB_MAX_CONNS", "DB_MIN_CONNS", "DB_MAX_CONN_LIFETIME", "DB_MAX_CONN_IDLE_TIME",
		"DB_HEALTH_CHECK_PERIOD", "STARTUP_TIMEOUT", "MIGRATION_TIMEOUT", "SHUTDOWN_TIMEOUT", "HTTP_READ_HEADER_TIMEOUT",
		"HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "REQUEST_BODY_LIMIT_BYTES",
		"AUTH_RATE_LIMIT", "AUTH_RATE_WINDOW", "TRUSTED_PROXY_CIDRS", "TRUSTED_PROXY_HOPS",
		"ENABLE_BANKING_APPLICATION_ID", "ENABLE_BANKING_PRIVATE_KEY", "ENABLE_BANKING_PRIVATE_KEY_BASE64",
		"ENABLE_BANKING_PRIVATE_KEY_PATH", "ENABLE_BANKING_CALLBACK_URL",
		"ENABLE_BANKING_RESULT_REDIRECT_URL", "ENABLE_BANKING_CONSENT_DAYS", "ENABLE_BANKING_STATE_TTL",
		"ENABLE_BANKING_REQUEST_TIMEOUT",
		"MARKET_DATA_REQUEST_TIMEOUT",
		"APNS_KEY_ID", "APNS_TEAM_ID", "APNS_BUNDLE_ID", "APNS_PRIVATE_KEY_BASE64", "APNS_REQUEST_TIMEOUT",
		"FCM_SERVICE_ACCOUNT_BASE64", "FCM_REQUEST_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
}
