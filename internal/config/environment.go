package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

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
