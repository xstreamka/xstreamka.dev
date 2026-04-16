package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PaymentStatus string

const (
	StatusPending PaymentStatus = "pending"
	StatusPaid    PaymentStatus = "paid"
	StatusFailed  PaymentStatus = "failed"
)

type Payment struct {
	ID                   int             `json:"id"`
	InvID                int             `json:"inv_id"`
	ProductType          string          `json:"product_type"`
	PlanID               string          `json:"plan_id"`
	Amount               float64         `json:"amount"`
	Description          string          `json:"description"`
	Status               PaymentStatus   `json:"status"`
	PaidAt               *time.Time      `json:"paid_at,omitempty"`
	CallbackURL          string          `json:"callback_url"`
	ReturnURL            string          `json:"return_url"`
	UserRef              string          `json:"user_ref"`
	Email                string          `json:"email"`
	Metadata             json.RawMessage `json:"metadata"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	WebhookDeliveredAt   *time.Time      `json:"webhook_delivered_at,omitempty"`
	WebhookAttempts      int             `json:"webhook_attempts"`
	WebhookLastAttemptAt *time.Time      `json:"webhook_last_attempt_at,omitempty"`
	WebhookLastError     *string         `json:"webhook_last_error,omitempty"`
}

type PaymentStore struct {
	pool *pgxpool.Pool
}

func NewPaymentStore(pool *pgxpool.Pool) *PaymentStore {
	return &PaymentStore{pool: pool}
}

// Create создаёт платёж. InvID = ID (autoincrement, уникальный).
func (s *PaymentStore) Create(ctx context.Context, p *Payment) (*Payment, error) {
	if p.Metadata == nil {
		p.Metadata = json.RawMessage(`{}`)
	}

	err := s.pool.QueryRow(ctx,
		`INSERT INTO payments (product_type, plan_id, amount, description, callback_url, return_url, user_ref, email, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, product_type, plan_id, amount, description, status, callback_url, return_url, user_ref, email, metadata, created_at, updated_at`,
		p.ProductType, p.PlanID, p.Amount, p.Description,
		p.CallbackURL, p.ReturnURL, p.UserRef, p.Email, p.Metadata,
	).Scan(&p.ID, &p.ProductType, &p.PlanID, &p.Amount, &p.Description,
		&p.Status, &p.CallbackURL, &p.ReturnURL, &p.UserRef, &p.Email,
		&p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create payment: %w", err)
	}

	// inv_id = id
	p.InvID = p.ID
	_, err = s.pool.Exec(ctx, `UPDATE payments SET inv_id = id WHERE id = $1`, p.ID)
	if err != nil {
		return nil, fmt.Errorf("set inv_id: %w", err)
	}

	return p, nil
}

// MarkPaid помечает платёж оплаченным, возвращает обновлённый платёж.
func (s *PaymentStore) MarkPaid(ctx context.Context, invID int) (*Payment, error) {
	p := &Payment{}
	err := s.pool.QueryRow(ctx,
		`UPDATE payments SET status = $1, paid_at = NOW(), updated_at = NOW()
		 WHERE inv_id = $2 AND status = $3
		 RETURNING id, inv_id, product_type, plan_id, amount, description, status, paid_at,
		           callback_url, return_url, user_ref, email, metadata, created_at, updated_at`,
		StatusPaid, invID, StatusPending,
	).Scan(&p.ID, &p.InvID, &p.ProductType, &p.PlanID, &p.Amount, &p.Description,
		&p.Status, &p.PaidAt, &p.CallbackURL, &p.ReturnURL, &p.UserRef, &p.Email,
		&p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("mark paid inv_id=%d: %w", invID, err)
	}
	return p, nil
}

// MarkFailed помечает платёж неуспешным.
func (s *PaymentStore) MarkFailed(ctx context.Context, invID int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE payments SET status = $1, updated_at = NOW() WHERE inv_id = $2 AND status = $3`,
		StatusFailed, invID, StatusPending,
	)
	return err
}

// GetByInvID находит платёж по InvId.
func (s *PaymentStore) GetByInvID(ctx context.Context, invID int) (*Payment, error) {
	p := &Payment{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, inv_id, product_type, plan_id, amount, description, status, paid_at,
		        callback_url, return_url, user_ref, email, metadata, created_at, updated_at
		 FROM payments WHERE inv_id = $1`,
		invID,
	).Scan(&p.ID, &p.InvID, &p.ProductType, &p.PlanID, &p.Amount, &p.Description,
		&p.Status, &p.PaidAt, &p.CallbackURL, &p.ReturnURL, &p.UserRef, &p.Email,
		&p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("payment not found: inv_id=%d", invID)
	}
	return p, nil
}

// FindPending ищет незавершённый платёж с такими же параметрами (не старше 30 минут).
// Если найден — возвращаем его вместо создания дубля.
func (s *PaymentStore) FindPending(ctx context.Context, userRef, planID string, amount float64) (*Payment, error) {
	p := &Payment{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, inv_id, product_type, plan_id, amount, description, status, paid_at,
		        callback_url, return_url, user_ref, email, metadata, created_at, updated_at
		 FROM payments
		 WHERE user_ref = $1 AND plan_id = $2 AND amount = $3
		   AND status = 'pending'
		   AND created_at > NOW() - INTERVAL '30 minutes'
		 ORDER BY id DESC LIMIT 1`,
		userRef, planID, amount,
	).Scan(&p.ID, &p.InvID, &p.ProductType, &p.PlanID, &p.Amount, &p.Description,
		&p.Status, &p.PaidAt, &p.CallbackURL, &p.ReturnURL, &p.UserRef, &p.Email,
		&p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListAll — все платежи (для админки).
func (s *PaymentStore) ListAll(ctx context.Context, limit int) ([]Payment, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, inv_id, product_type, plan_id, amount, description, status, paid_at,
		        callback_url, return_url, user_ref, email, metadata, created_at, updated_at
		 FROM payments ORDER BY id DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var payments []Payment
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.InvID, &p.ProductType, &p.PlanID, &p.Amount, &p.Description,
			&p.Status, &p.PaidAt, &p.CallbackURL, &p.ReturnURL, &p.UserRef, &p.Email,
			&p.Metadata, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		payments = append(payments, p)
	}
	return payments, nil
}

// MarkWebhookDelivered — фиксируем успешную доставку вебхука.
func (s *PaymentStore) MarkWebhookDelivered(ctx context.Context, invID int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE payments
		 SET webhook_delivered_at = NOW(),
		     webhook_last_attempt_at = NOW(),
		     webhook_attempts = webhook_attempts + 1,
		     webhook_last_error = NULL,
		     updated_at = NOW()
		 WHERE inv_id = $1`,
		invID,
	)
	return err
}

// MarkWebhookFailed — увеличиваем счётчик попыток, сохраняем ошибку.
func (s *PaymentStore) MarkWebhookFailed(ctx context.Context, invID int, errMsg string) error {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE payments
		 SET webhook_attempts = webhook_attempts + 1,
		     webhook_last_attempt_at = NOW(),
		     webhook_last_error = $1,
		     updated_at = NOW()
		 WHERE inv_id = $2`,
		errMsg, invID,
	)
	return err
}

// ListPendingWebhooks — платежи с непришедшей доставкой, для reconciler-а.
// retryAfter: не повторять, если последняя попытка была раньше этого интервала.
// maxAge: платежи старше этого возраста игнорируем (считаем safer фейлом).
func (s *PaymentStore) ListPendingWebhooks(ctx context.Context, retryAfter, maxAge time.Duration, limit int) ([]Payment, error) {
	const paymentCols = `id, inv_id, product_type, plan_id, amount, description,
                     status, paid_at, callback_url, return_url, user_ref, email,
                     metadata, webhook_delivered_at, webhook_attempts,
                     webhook_last_attempt_at, webhook_last_error,
                     created_at, updated_at`
	rows, err := s.pool.Query(ctx,
		`SELECT `+paymentCols+`
		 FROM payments
		 WHERE status = 'paid'
		   AND callback_url <> ''
		   AND webhook_delivered_at IS NULL
		   AND paid_at > NOW() - $1::interval
		   AND (webhook_last_attempt_at IS NULL OR webhook_last_attempt_at < NOW() - $2::interval)
		 ORDER BY paid_at
		 LIMIT $3`,
		fmt.Sprintf("%d seconds", int(maxAge.Seconds())),
		fmt.Sprintf("%d seconds", int(retryAfter.Seconds())),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending webhooks: %w", err)
	}
	defer rows.Close()

	var list []Payment
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.InvID, &p.ProductType, &p.PlanID, &p.Amount,
			&p.Description, &p.Status, &p.PaidAt, &p.CallbackURL, &p.ReturnURL,
			&p.UserRef, &p.Email, &p.Metadata, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, nil
}

// BuildWebhookPayload собирает payload из Payment — используется и в handler, и в reconciler.
func (p *Payment) BuildWebhookPayload() map[string]any {
	paidAt := ""
	if p.PaidAt != nil {
		paidAt = p.PaidAt.Format(time.RFC3339)
	}
	return map[string]any{
		"inv_id":       p.InvID,
		"product_type": p.ProductType,
		"plan_id":      p.PlanID,
		"amount":       p.Amount,
		"status":       string(p.Status),
		"user_ref":     p.UserRef,
		"email":        p.Email,
		"metadata":     p.Metadata,
		"paid_at":      paidAt,
	}
}
