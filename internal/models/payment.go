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
	ID          int             `json:"id"`
	InvID       int             `json:"inv_id"`
	ProductType string          `json:"product_type"`
	PlanID      string          `json:"plan_id"`
	Amount      float64         `json:"amount"`
	Description string          `json:"description"`
	Status      PaymentStatus   `json:"status"`
	PaidAt      *time.Time      `json:"paid_at,omitempty"`
	CallbackURL string          `json:"callback_url"`
	ReturnURL   string          `json:"return_url"`
	UserRef     string          `json:"user_ref"`
	Email       string          `json:"email"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
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
