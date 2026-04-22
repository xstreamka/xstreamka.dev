package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	ListenAddr string

	// PostgreSQL
	DBHost string
	DBPort string
	DBUser string
	DBPass string
	DBName string

	// Робокасса
	RobokassaLogin string
	RobokassaPass1 string
	RobokassaPass2 string
	RobokassaTest  bool
	RobokassaAlgo  string // "md5" или "sha256"

	// Общий секрет для подписи между pay-service и внешними сервисами (VPN-панель и т.д.)
	WebhookSecret string

	// URL лендинга (для шаблонов)
	SiteURL string

	// Admin UI
	AdminUser         string
	AdminPassword     string // plaintext fallback (для миграции)
	AdminPasswordHash string // bcrypt, предпочтительный вариант
	SessionSecret     string // HMAC-ключ для session-cookie (>= 32 байт)
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		ListenAddr: getEnv("LISTEN_ADDR", ":8090"),

		DBHost: getEnv("DB_HOST", "127.0.0.1"),
		DBPort: getEnv("DB_PORT", "5432"),
		DBUser: getEnv("DB_USER", "payservice"),
		DBPass: getEnv("DB_PASS", ""),
		DBName: getEnv("DB_NAME", "payservice"),

		RobokassaLogin: getEnv("ROBOKASSA_LOGIN", ""),
		RobokassaPass1: getEnv("ROBOKASSA_PASS1", ""),
		RobokassaPass2: getEnv("ROBOKASSA_PASS2", ""),
		RobokassaTest:  getEnv("ROBOKASSA_TEST_MODE", "false") == "true",
		RobokassaAlgo:  getEnv("ROBOKASSA_HASH_ALGO", "md5"),

		WebhookSecret: getEnv("WEBHOOK_SECRET", ""),
		SiteURL:       getEnv("SITE_URL", "https://xstreamka.dev"),

		AdminUser:         getEnv("ADMIN_USER", ""),
		AdminPassword:     getEnv("ADMIN_PASSWORD", ""),
		AdminPasswordHash: getEnv("ADMIN_PASSWORD_HASH", ""),
		SessionSecret:     getEnv("SESSION_SECRET", ""),
	}

	if cfg.DBPass == "" {
		return nil, fmt.Errorf("DB_PASS is required")
	}
	if cfg.RobokassaLogin == "" {
		return nil, fmt.Errorf("ROBOKASSA_LOGIN is required")
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("WEBHOOK_SECRET is required")
	}
	if cfg.AdminUser == "" {
		return nil, fmt.Errorf("ADMIN_USER is required")
	}
	if cfg.AdminPassword == "" && cfg.AdminPasswordHash == "" {
		return nil, fmt.Errorf("ADMIN_PASSWORD or ADMIN_PASSWORD_HASH is required")
	}
	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("SESSION_SECRET is required (generate: openssl rand -hex 32)")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("SESSION_SECRET must be at least 32 chars")
	}

	return cfg, nil
}

func (c *Config) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		c.DBUser, c.DBPass, c.DBHost, c.DBPort, c.DBName,
	)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
