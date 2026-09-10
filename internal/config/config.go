package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv          string
	HTTPAddr        string
	BaseURL         string
	DatabaseURL     string
	SessionSecret   string
	SessionTTL      time.Duration
	BookingHoldTTL  time.Duration
	CSRFSecure      bool
	SMTPHost        string
	SMTPPort        int
	SMTPFrom        string
	MidtransServer  string
	MidtransClient  string
	MidtransIsProd  bool
	AdminEmail     string
	AdminPassword  string
	CSEmail        string
	CSPassword     string
}

func Load() (Config, error) {
	cfg := Config{
		AppEnv:         getenv("APP_ENV", "development"),
		HTTPAddr:       getenv("HTTP_ADDR", ":8080"),
		BaseURL:        getenv("BASE_URL", "http://localhost:8080"),
		DatabaseURL:    getenv("DATABASE_URL", "postgres://spa:spa@localhost:5432/spa?sslmode=disable"),
		SessionSecret:  getenv("SESSION_SECRET", "dev-session-secret-change-me"),
		SessionTTL:     durationEnv("SESSION_TTL", 168*time.Hour),
		BookingHoldTTL: durationEnv("BOOKING_HOLD_TTL", 15*time.Minute),
		CSRFSecure:     getenv("APP_ENV", "development") == "production",
		SMTPHost:       getenv("SMTP_HOST", "localhost"),
		SMTPPort:       intEnv("SMTP_PORT", 1025),
		SMTPFrom:       getenv("SMTP_FROM", "bookings@spa.local"),
		MidtransServer: getenv("MIDTRANS_SERVER_KEY", ""),
		MidtransClient: getenv("MIDTRANS_CLIENT_KEY", ""),
		MidtransIsProd: getenv("MIDTRANS_IS_PRODUCTION", "false") == "true",
		AdminEmail:     getenv("ADMIN_EMAIL", "admin@spa.local"),
		AdminPassword:  getenv("ADMIN_PASSWORD", "admin12345"),
		CSEmail:        getenv("CS_EMAIL", "cs@spa.local"),
		CSPassword:     getenv("CS_PASSWORD", "cs12345678"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.SessionSecret == "" {
		return cfg, fmt.Errorf("SESSION_SECRET is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
