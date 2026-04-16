package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"pay-service/internal/models"
	"pay-service/internal/payment"
)

type PaymentHandler struct {
	payments  *models.PaymentStore
	robokassa *payment.Robokassa
	webhook   *payment.WebhookSender
	secret    string // общий секрет для подписи redirect-ов
	siteURL   string
	templates *template.Template
}

func NewPaymentHandler(
	payments *models.PaymentStore,
	robokassa *payment.Robokassa,
	webhook *payment.WebhookSender,
	secret string,
	siteURL string,
	templates *template.Template,
) *PaymentHandler {
	return &PaymentHandler{
		payments:  payments,
		robokassa: robokassa,
		webhook:   webhook,
		secret:    secret,
		siteURL:   siteURL,
		templates: templates,
	}
}

// ──────────────────────────────────────────────
// 1. Checkout — пользователь приходит с VPN-панели
// ──────────────────────────────────────────────

// Checkout показывает страницу оплаты.
//
// GET /pay/checkout?product_type=vpn&plan_id=basic_30&amount=150&description=VPN+Basic
//
//	&user_ref=42&email=user@test.com&callback_url=https://...&return_url=https://...
//	&metadata={"traffic_gb":50,"duration_days":30}&ts=1713200000&sig=hmac
//
// VPN-панель (или любой сервис) формирует эту ссылку с подписью.
// Checkout принимает подписанные параметры, создаёт платёж и редиректит на чистый URL.
// GET /pay/checkout?...&sig=... → 303 → /pay/order/{inv_id}
func (h *PaymentHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// 1. Проверяем подпись
	sig := q.Get("sig")
	if sig == "" {
		http.Error(w, "missing signature", http.StatusBadRequest)
		return
	}
	if !h.verifyRedirectSignature(q, sig) {
		log.Printf("Checkout: invalid signature")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	// 2. Проверяем timestamp (±10 минут)
	ts, _ := strconv.ParseInt(q.Get("ts"), 10, 64)
	if math.Abs(float64(time.Now().Unix()-ts)) > 600 {
		http.Error(w, "link expired", http.StatusForbidden)
		return
	}

	// 3. Парсим параметры
	amount, _ := strconv.ParseFloat(q.Get("amount"), 64)
	if amount <= 0 {
		http.Error(w, "invalid amount", http.StatusBadRequest)
		return
	}

	metadataRaw := q.Get("metadata")
	if metadataRaw == "" {
		metadataRaw = "{}"
	}

	// 4. Ищем существующий pending-платёж (защита от повторного перехода по ссылке)
	pmt, _ := h.payments.FindPending(r.Context(), q.Get("user_ref"), q.Get("plan_id"), amount)

	if pmt == nil {
		var err error
		pmt, err = h.payments.Create(r.Context(), &models.Payment{
			ProductType: q.Get("product_type"),
			PlanID:      q.Get("plan_id"),
			Amount:      amount,
			Description: q.Get("description"),
			CallbackURL: q.Get("callback_url"),
			ReturnURL:   q.Get("return_url"),
			UserRef:     q.Get("user_ref"),
			Email:       q.Get("email"),
			Metadata:    json.RawMessage(metadataRaw),
		})
		if err != nil {
			log.Printf("Checkout: create payment error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		log.Printf("Checkout: payment created inv_id=%d, amount=%.2f, user_ref=%s",
			pmt.InvID, pmt.Amount, pmt.UserRef)
	} else {
		log.Printf("Checkout: reusing existing inv_id=%d", pmt.InvID)
	}

	// 5. Редирект на чистый URL — F5 просто перечитает страницу из БД
	http.Redirect(w, r, fmt.Sprintf("/pay/order/%d", pmt.InvID), http.StatusSeeOther)
}

// OrderPage показывает страницу оплаты по inv_id. Безопасен для F5.
// GET /pay/order/{id}
func (h *PaymentHandler) OrderPage(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	invID, err := strconv.Atoi(idStr)
	if err != nil || invID <= 0 {
		http.Error(w, "invalid order", http.StatusBadRequest)
		return
	}

	pmt, err := h.payments.GetByInvID(r.Context(), invID)
	if err != nil {
		http.Error(w, "order not found", http.StatusNotFound)
		return
	}

	// Если уже оплачен — показываем success
	if pmt.Status == models.StatusPaid {
		h.templates.ExecuteTemplate(w, "success.html", map[string]any{
			"InvID": pmt.InvID,
		})
		return
	}

	// Если failed — показываем fail
	if pmt.Status == models.StatusFailed {
		h.templates.ExecuteTemplate(w, "fail.html", map[string]any{
			"InvID": pmt.InvID,
		})
		return
	}

	h.templates.ExecuteTemplate(w, "checkout.html", map[string]any{
		"Payment":      pmt,
		"RobokassaURL": h.buildRobokassaURL(pmt),
	})
}

func (h *PaymentHandler) buildRobokassaURL(pmt *models.Payment) string {
	receipt := payment.BuildReceipt(pmt.Description, pmt.Amount)

	payURL, err := h.robokassa.GeneratePaymentURL(
		pmt.InvID, pmt.Amount, pmt.Description, pmt.Email, receipt, nil,
	)
	if err != nil {
		log.Printf("buildRobokassaURL error: %v", err)
		return ""
	}
	return payURL
}

// ──────────────────────────────────────────────
// 2. Робокасса callbacks
// ──────────────────────────────────────────────

// ResultURL — callback от Робокассы (сервер→сервер).
// POST /payments/result
func (h *PaymentHandler) ResultURL(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	outSum := r.FormValue("OutSum")
	invIDStr := r.FormValue("InvId")
	signatureValue := r.FormValue("SignatureValue")
	shpParams := extractShpParams(r)

	if !h.robokassa.VerifyResultSignature(outSum, invIDStr, signatureValue, shpParams) {
		log.Printf("ResultURL: bad signature inv_id=%s", invIDStr)
		http.Error(w, "bad sign", http.StatusBadRequest)
		return
	}

	invID, _ := strconv.Atoi(invIDStr)

	pmt, err := h.payments.MarkPaid(r.Context(), invID)
	if err != nil {
		log.Printf("ResultURL: mark paid error inv_id=%d: %v", invID, err)
		// Отвечаем OK чтобы Робокасса не повторяла
		fmt.Fprintf(w, "OK%d", invID)
		return
	}

	log.Printf("ResultURL: inv_id=%d PAID, product=%s, plan=%s, user_ref=%s",
		invID, pmt.ProductType, pmt.PlanID, pmt.UserRef)

	// Отправляем webhook на callback_url (в фоне, чтобы не задерживать ответ Робокассе)
	if pmt.CallbackURL != "" {
		go h.sendWebhook(pmt)
	}

	fmt.Fprintf(w, "OK%d", invID)
}

func (h *PaymentHandler) sendWebhook(pmt *models.Payment) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := h.webhook.Send(pmt.CallbackURL, payload); err != nil {
		log.Printf("Webhook FAILED for inv_id=%d to %s: %v", pmt.InvID, pmt.CallbackURL, err)
		if markErr := h.payments.MarkWebhookFailed(ctx, pmt.InvID, err.Error()); markErr != nil {
			log.Printf("Webhook: mark failed error inv_id=%d: %v", pmt.InvID, markErr)
		}
		return
	}

	if err := h.payments.MarkWebhookDelivered(ctx, pmt.InvID); err != nil {
		log.Printf("Webhook: mark delivered error inv_id=%d: %v", pmt.InvID, err)
	}
}

// SuccessURL — пользователь вернулся после оплаты.
// GET /payments/success
func (h *PaymentHandler) SuccessURL(w http.ResponseWriter, r *http.Request) {
	invIDStr := r.FormValue("InvId")
	invID, _ := strconv.Atoi(invIDStr)

	if invID > 0 {
		pmt, err := h.payments.GetByInvID(r.Context(), invID)
		if err == nil && pmt.ReturnURL != "" {
			// Редиректим пользователя обратно на VPN-панель
			sep := "?"
			if strings.Contains(pmt.ReturnURL, "?") {
				sep = "&"
			}
			redirectURL := fmt.Sprintf("%s%spayment=success&inv_id=%d", pmt.ReturnURL, sep, invID)

			log.Printf("SuccessURL: redirecting inv_id=%d to %s", invID, redirectURL)
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
			return
		}
	}

	// Fallback: показываем страницу успеха
	h.templates.ExecuteTemplate(w, "success.html", map[string]any{
		"InvID": invID,
	})
}

// FailURL — пользователь отменил оплату.
// GET /payments/fail
func (h *PaymentHandler) FailURL(w http.ResponseWriter, r *http.Request) {
	invIDStr := r.FormValue("InvId")
	invID, _ := strconv.Atoi(invIDStr)

	if invID > 0 {
		h.payments.MarkFailed(r.Context(), invID)

		pmt, err := h.payments.GetByInvID(r.Context(), invID)
		if err == nil && pmt.ReturnURL != "" {
			sep := "?"
			if strings.Contains(pmt.ReturnURL, "?") {
				sep = "&"
			}
			redirectURL := fmt.Sprintf("%s%spayment=failed&inv_id=%d", pmt.ReturnURL, sep, invID)
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
			return
		}
	}

	h.templates.ExecuteTemplate(w, "fail.html", map[string]any{
		"InvID": invID,
	})
}

// ──────────────────────────────────────────────
// Подпись redirect-ов
// ──────────────────────────────────────────────

// verifyRedirectSignature проверяет HMAC-SHA256 подпись query-параметров.
// Подписывается строка вида "key1=val1&key2=val2&..." (отсортировано, без sig).
func (h *PaymentHandler) verifyRedirectSignature(params map[string][]string, sig string) bool {
	canonical := buildCanonicalString(params)
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

// SignRedirectParams подписывает параметры (экспортируемая — для тестов и клиентов).
func SignRedirectParams(params map[string]string, secret string) string {
	// Конвертируем в формат url.Values для buildCanonicalString
	multiParams := make(map[string][]string, len(params))
	for k, v := range params {
		multiParams[k] = []string{v}
	}
	canonical := buildCanonicalString(multiParams)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func buildCanonicalString(params map[string][]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sig" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if len(params[k]) > 0 {
			parts = append(parts, k+"="+params[k][0])
		}
	}
	return strings.Join(parts, "&")
}

func extractShpParams(r *http.Request) map[string]string {
	r.ParseForm()
	params := make(map[string]string)
	for key, values := range r.Form {
		if len(values) > 0 && len(key) > 4 && strings.EqualFold(key[:4], "shp_") {
			params[key] = values[0]
		}
	}
	return params
}
