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
