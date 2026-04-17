package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"pay-service/internal/models"
	"pay-service/internal/payment"
)

type AdminHandler struct {
	payments  *models.PaymentStore
	webhook   *payment.WebhookSender
	templates *template.Template
}

func NewAdminHandler(
	payments *models.PaymentStore,
	webhook *payment.WebhookSender,
	templates *template.Template,
) *AdminHandler {
	return &AdminHandler{payments: payments, webhook: webhook, templates: templates}
}

const pageSize = 50

// Payments — GET /admin/payments
func (h *AdminHandler) Payments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f := models.PaymentFilter{
		Status:         q.Get("status"),
		ProductType:    q.Get("product_type"),
		PlanID:         q.Get("plan_id"),
		Search:         q.Get("search"),
		WebhookProblem: q.Get("webhook_problem") == "1",
	}

	// Период: по умолчанию — последние 30 дней
	period := q.Get("period")
	if period == "" {
		period = "30d"
	}
	from, to := parsePeriod(period)
	if from != nil {
		f.From = from
	}
	if to != nil {
		f.To = to
	}

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	list, total, err := h.payments.ListFiltered(r.Context(), f, pageSize, offset)
	if err != nil {
		log.Printf("Admin: list payments: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	stats, err := h.payments.StatsFor(r.Context(), from, to)
	if err != nil {
		log.Printf("Admin: stats: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	totalPages := (total + pageSize - 1) / pageSize

	data := map[string]any{
		"Payments":    list,
		"Stats":       stats,
		"Filter":      f,
		"Period":      period,
		"Page":        page,
		"PrevPage":    page - 1,
		"NextPage":    page + 1,
		"TotalPages":  totalPages,
		"Total":       total,
		"QueryString": stripPageFromQuery(r.URL.RawQuery),
	}

	if err := h.templates.ExecuteTemplate(w, "admin_payments.html", data); err != nil {
		log.Printf("Admin: render: %v", err)
	}
}

// PaymentDetail — GET /admin/payments/{inv_id}
func (h *AdminHandler) PaymentDetail(w http.ResponseWriter, r *http.Request) {
	invID, err := strconv.Atoi(r.PathValue("inv_id"))
	if err != nil {
		http.Error(w, "bad inv_id", http.StatusBadRequest)
		return
	}

	p, err := h.payments.GetByInvID(r.Context(), invID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Metadata красиво
	var metaPretty string
	if len(p.Metadata) > 0 {
		var buf json.RawMessage
		if err := json.Unmarshal(p.Metadata, &buf); err == nil {
			if b, err := json.MarshalIndent(buf, "", "  "); err == nil {
				metaPretty = string(b)
			}
		}
	}

	data := map[string]any{
		"P":            p,
		"MetaPretty":   metaPretty,
		"Flash":        r.URL.Query().Get("flash"),
		"RobokassaURL": robokassaLKURL(p.InvID),
	}

	if err := h.templates.ExecuteTemplate(w, "admin_payment_detail.html", data); err != nil {
		log.Printf("Admin: render detail: %v", err)
	}
}

// RetryWebhook — POST /admin/payments/{inv_id}/retry-webhook
func (h *AdminHandler) RetryWebhook(w http.ResponseWriter, r *http.Request) {
	invID, err := strconv.Atoi(r.PathValue("inv_id"))
	if err != nil {
		http.Error(w, "bad inv_id", http.StatusBadRequest)
		return
	}

	p, err := h.payments.GetByInvID(r.Context(), invID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if p.Status != models.StatusPaid {
		http.Redirect(w, r,
			fmt.Sprintf("/admin/payments/%d?flash=only_paid", invID), http.StatusSeeOther)
		return
	}
	if p.CallbackURL == "" {
		http.Redirect(w, r,
			fmt.Sprintf("/admin/payments/%d?flash=no_callback", invID), http.StatusSeeOther)
		return
	}

	paidAt := ""
	if p.PaidAt != nil {
		paidAt = p.PaidAt.Format(time.RFC3339)
	}
	payload := payment.WebhookPayload{
		InvID:       p.InvID,
		ProductType: p.ProductType,
		PlanID:      p.PlanID,
		Amount:      p.Amount,
		Status:      string(p.Status),
		UserRef:     p.UserRef,
		Email:       p.Email,
		Metadata:    p.Metadata,
		PaidAt:      paidAt,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := h.webhook.Send(p.CallbackURL, payload); err != nil {
		log.Printf("Admin retry webhook inv_id=%d: %v", invID, err)
		_ = h.payments.MarkWebhookFailed(ctx, invID, err.Error())
		http.Redirect(w, r,
			fmt.Sprintf("/admin/payments/%d?flash=retry_failed", invID), http.StatusSeeOther)
		return
	}

	if err := h.payments.MarkWebhookDelivered(ctx, invID); err != nil {
		log.Printf("Admin retry: mark delivered inv_id=%d: %v", invID, err)
	}
	http.Redirect(w, r,
		fmt.Sprintf("/admin/payments/%d?flash=retry_ok", invID), http.StatusSeeOther)
}

// ──────────── helpers ────────────

func parsePeriod(p string) (*time.Time, *time.Time) {
	now := time.Now()
	switch p {
	case "today":
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return &from, nil
	case "7d":
		from := now.AddDate(0, 0, -7)
		return &from, nil
	case "30d":
		from := now.AddDate(0, 0, -30)
		return &from, nil
	case "90d":
		from := now.AddDate(0, 0, -90)
		return &from, nil
	case "all", "":
		return nil, nil
	}
	return nil, nil
}

// robokassaLKURL — ссылка в ЛК Робокассы на страницу платежа.
// Универсальной ссылки на конкретный InvId у Робокассы нет,
// поэтому ведём на общий список операций.
func robokassaLKURL(invID int) string {
	return "https://partner.robokassa.ru/Operation"
}

// Signature обновлён в payment: BuildWebhookPayload должен возвращать тот же map,
// что и sendWebhook в payment.go.

// stripPageFromQuery — убирает page из querystring, чтобы не дублировать в пагинации.
func stripPageFromQuery(raw string) string {
	v, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	v.Del("page")
	return v.Encode()
}
