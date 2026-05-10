package config

import "os"

type Config struct{ Port, DatabaseURL, JWTSecret string }

func Load() Config {
	return Config{Port: getenv("PORT", "8080"), DatabaseURL: getenv("DATABASE_URL", "postgres://money:money@localhost:5432/money_manager?sslmode=disable"), JWTSecret: getenv("JWT_SECRET", "dev-secret")}
}
func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
