// Package reconciler периодически добивает доставку webhook-ов,
// которые не прошли с первого раза (панель была недоступна и т.п.).
package reconciler

import (
	"context"
	"log"
	"time"

	"pay-service/internal/models"
	"pay-service/internal/payment"
)

type Config struct {
	Interval      time.Duration // как часто запускать (рекомендуется 5 минут)
	RetryAfter    time.Duration // не повторять доставку чаще (5 минут)
	MaxAge        time.Duration // платежи старше — игнорируем (7 дней)
	BatchLimit    int           // не более N платежей за один тик
	PendingMaxAge time.Duration // pending старше этого → failed
}

func DefaultConfig() Config {
	return Config{
		Interval:      5 * time.Minute,
		RetryAfter:    5 * time.Minute,
		MaxAge:        7 * 24 * time.Hour,
		BatchLimit:    50,
		PendingMaxAge: 30 * time.Minute,
	}
}

type Reconciler struct {
	store  *models.PaymentStore
	sender *payment.WebhookSender
	cfg    Config
}

func New(store *models.PaymentStore, sender *payment.WebhookSender, cfg Config) *Reconciler {
	return &Reconciler{store: store, sender: sender, cfg: cfg}
}

// Run блокирующий loop — запускай в отдельной goroutine.
// Завершается по ctx.Done().
func (r *Reconciler) Run(ctx context.Context) {
	log.Printf("Reconciler started: every %s, retry_after=%s, max_age=%s",
		r.cfg.Interval, r.cfg.RetryAfter, r.cfg.MaxAge)

	// Первый прогон с небольшой задержкой — даём сервису прогреться
	time.Sleep(30 * time.Second)
	r.tick(ctx)

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Reconciler stopped")
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	r.retryPendingWebhooks(ctx)
	r.cleanupExpiredPending(ctx)
}

func (r *Reconciler) retryPendingWebhooks(ctx context.Context) {
	pmts, err := r.store.ListPendingWebhooks(ctx, r.cfg.RetryAfter, r.cfg.MaxAge, r.cfg.BatchLimit)
	if err != nil {
		log.Printf("Reconciler: list pending error: %v", err)
		return
	}
	if len(pmts) == 0 {
		return
	}

	log.Printf("Reconciler: %d pending webhook(s) to retry", len(pmts))

	var delivered, failed int
	for _, pmt := range pmts {
		paidAt := ""
		if pmt.PaidAt != nil {
			paidAt = pmt.PaidAt.Format(time.RFC3339)
		}
		payload := payment.WebhookPayload{
			InvID:       pmt.InvID,
			ProductType: pmt.ProductType,
			PlanID:      pmt.PlanID,
			Amount:      pmt.Amount,
			Status:      string(pmt.Status),
			UserRef:     pmt.UserRef,
			Email:       pmt.Email,
			Metadata:    pmt.Metadata,
			PaidAt:      paidAt,
		}

		if err := r.sender.Send(pmt.CallbackURL, payload); err != nil {
			log.Printf("Reconciler: inv_id=%d attempt#%d failed: %v",
				pmt.InvID, pmt.WebhookAttempts+1, err)
			_ = r.store.MarkWebhookFailed(ctx, pmt.InvID, err.Error())
			failed++
			continue
		}
		log.Printf("Reconciler: inv_id=%d delivered (after %d previous attempts)",
			pmt.InvID, pmt.WebhookAttempts)
		_ = r.store.MarkWebhookDelivered(ctx, pmt.InvID)
		delivered++

		// Небольшая пауза между платежами, чтобы не лупить панель пачкой
		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("Reconciler: tick done, delivered=%d failed=%d", delivered, failed)
}

func (r *Reconciler) cleanupExpiredPending(ctx context.Context) {
	n, err := r.store.CleanupExpiredPending(ctx, r.cfg.PendingMaxAge)
	if err != nil {
		log.Printf("Reconciler: cleanup error: %v", err)
		return
	}
	if n > 0 {
		log.Printf("Reconciler: cleaned up %d expired pending payment(s) (>%s old)",
			n, r.cfg.PendingMaxAge)
	}
}
