package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func Connect(dsn string) (*DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &DB{Pool: pool}, nil
}

func (db *DB) Close() { db.Pool.Close() }

func (db *DB) Migrate() error {
	ctx := context.Background()
	for i, m := range migrations {
		if _, err := db.Pool.Exec(ctx, m); err != nil {
			return fmt.Errorf("migration %d failed: %w", i, err)
		}
	}
	log.Println("Migrations applied successfully")
	return nil
}

var migrations = []string{
	// Универсальная таблица платежей — не привязана к конкретному продукту
	`CREATE TABLE IF NOT EXISTS payments (
		id             SERIAL PRIMARY KEY,
		inv_id         INTEGER UNIQUE,

		-- Что оплачивается
		product_type   VARCHAR(50)  NOT NULL,           -- 'vpn', 'course', ...
		plan_id        VARCHAR(100) NOT NULL,            -- 'vpn_basic_30', ...
		amount         DECIMAL(10,2) NOT NULL,
		description    VARCHAR(255) NOT NULL DEFAULT '',

		-- Статус
		status         VARCHAR(20)  NOT NULL DEFAULT 'pending',
		paid_at        TIMESTAMPTZ,

		-- Внешний вызывающий сервис
		callback_url   VARCHAR(500) NOT NULL DEFAULT '', -- куда POST после оплаты
		return_url     VARCHAR(500) NOT NULL DEFAULT '', -- куда редирект пользователя
		user_ref       VARCHAR(255) NOT NULL DEFAULT '', -- внешний ID (user_id VPN-панели и т.д.)
		email          VARCHAR(255) NOT NULL DEFAULT '',

		-- Произвольные данные (JSON) — пробросятся в webhook
		metadata       JSONB NOT NULL DEFAULT '{}',

		created_at     TIMESTAMPTZ DEFAULT NOW(),
		updated_at     TIMESTAMPTZ DEFAULT NOW()
	)`,

	`CREATE INDEX IF NOT EXISTS idx_payments_inv_id ON payments(inv_id)`,
	`CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status)`,
	`CREATE INDEX IF NOT EXISTS idx_payments_product_type ON payments(product_type)`,
	`CREATE INDEX IF NOT EXISTS idx_payments_user_ref ON payments(user_ref)`,

	// Статус доставки webhook-а в callback_url
	`ALTER TABLE payments ADD COLUMN IF NOT EXISTS webhook_delivered_at TIMESTAMPTZ`,
	`ALTER TABLE payments ADD COLUMN IF NOT EXISTS webhook_attempts INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE payments ADD COLUMN IF NOT EXISTS webhook_last_attempt_at TIMESTAMPTZ`,
	`ALTER TABLE payments ADD COLUMN IF NOT EXISTS webhook_last_error TEXT`,

	// Индекс для reconciler-а: найти оплаченные но недоставленные, свежие
	`CREATE INDEX IF NOT EXISTS idx_payments_pending_webhook
 	ON payments(paid_at)
 	WHERE status = 'paid' AND callback_url <> '' AND webhook_delivered_at IS NULL`,

	// Токен доступа к странице оплаты (32 случайных байта → base64url, 43 символа).
	// Передаётся пользователю через httpOnly cookie + query-параметр при первом
	// заходе. Без токена /pay/order/{id} и callback-ы возвращают 404.
	`ALTER TABLE payments ADD COLUMN IF NOT EXISTS access_token VARCHAR(64) NOT NULL DEFAULT ''`,
}
