package config

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type enableBankingKeySources struct {
	path   string
	pem    string
	base64 string
}

func readEnableBankingKeySources() (enableBankingKeySources, error) {
	sources := enableBankingKeySources{
		path:   strings.TrimSpace(os.Getenv("ENABLE_BANKING_PRIVATE_KEY_PATH")),
		pem:    strings.TrimSpace(os.Getenv("ENABLE_BANKING_PRIVATE_KEY")),
		base64: strings.TrimSpace(os.Getenv("ENABLE_BANKING_PRIVATE_KEY_BASE64")),
	}
	configured := 0
	for _, value := range []string{sources.path, sources.pem, sources.base64} {
		if value != "" {
			configured++
		}
	}
	if configured > 1 {
		return enableBankingKeySources{}, errors.New("configure only one Enable Banking private key source")
	}
	return sources, nil
}

func (s enableBankingKeySources) load() (*rsa.PrivateKey, error) {
	if s.base64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(s.base64)
		if err != nil {
			return nil, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY_BASE64: decode base64: %w", err)
		}
		key, err := parsePrivateKey(decoded)
		if err != nil {
			return nil, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY_BASE64: %w", err)
		}
		return key, nil
	}
	if s.pem != "" {
		key, err := parsePrivateKey([]byte(s.pem))
		if err != nil {
			return nil, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY: %w", err)
		}
		return key, nil
	}
	if s.path != "" {
		key, err := loadPrivateKey(s.path)
		if err != nil {
			return nil, fmt.Errorf("ENABLE_BANKING_PRIVATE_KEY_PATH: %w", err)
		}
		return key, nil
	}
	return nil, nil
}

func loadAPNSCredentials(cfg *Config) error {
	keyBase64 := strings.TrimSpace(os.Getenv("APNS_PRIVATE_KEY_BASE64"))
	if keyBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(keyBase64)
		if err != nil {
			return fmt.Errorf("APNS_PRIVATE_KEY_BASE64: decode base64: %w", err)
		}
		cfg.APNSPrivateKey, err = jwt.ParseECPrivateKeyFromPEM(decoded)
		if err != nil {
			return fmt.Errorf("APNS_PRIVATE_KEY_BASE64: parse EC private key: %w", err)
		}
	}
	cfg.APNSKeyID = strings.TrimSpace(os.Getenv("APNS_KEY_ID"))
	cfg.APNSTeamID = strings.TrimSpace(os.Getenv("APNS_TEAM_ID"))
	cfg.APNSBundleID = strings.TrimSpace(os.Getenv("APNS_BUNDLE_ID"))
	return nil
}

func loadFCMCredentials(cfg *Config) error {
	serviceAccountBase64 := strings.TrimSpace(os.Getenv("FCM_SERVICE_ACCOUNT_BASE64"))
	if serviceAccountBase64 == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(serviceAccountBase64)
	if err != nil {
		return fmt.Errorf("FCM_SERVICE_ACCOUNT_BASE64: decode base64: %w", err)
	}
	var account struct {
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if err := json.Unmarshal(decoded, &account); err != nil {
		return fmt.Errorf("FCM_SERVICE_ACCOUNT_BASE64: decode service account JSON: %w", err)
	}
	cfg.FCMProjectID = strings.TrimSpace(account.ProjectID)
	cfg.FCMClientEmail = strings.TrimSpace(account.ClientEmail)
	cfg.FCMPrivateKey, err = jwt.ParseRSAPrivateKeyFromPEM([]byte(account.PrivateKey))
	if err != nil {
		return fmt.Errorf("FCM_SERVICE_ACCOUNT_BASE64: parse RSA private key: %w", err)
	}
	if cfg.FCMProjectID == "" || cfg.FCMClientEmail == "" {
		return errors.New("FCM_SERVICE_ACCOUNT_BASE64 must contain project_id and client_email")
	}
	return nil
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
