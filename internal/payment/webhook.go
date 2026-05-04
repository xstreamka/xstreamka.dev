package payment

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// WebhookPayload — данные, отправляемые на callback_url после оплаты.
// Внешний сервис (VPN-панель и т.д.) получает это и активирует услугу.
type WebhookPayload struct {
	InvID       int             `json:"inv_id"`
	ProductType string          `json:"product_type"`
	PlanID      string          `json:"plan_id"`
	Amount      float64         `json:"amount"`
	Status      string          `json:"status"`
	UserRef     string          `json:"user_ref"`
	Email       string          `json:"email"`
	Metadata    json.RawMessage `json:"metadata"`
	PaidAt      string          `json:"paid_at"`
	// ForceNotify — выставляется только при ручном «переотправить вебхук» из
	// админки. Принимающая сторона (VPN-панель) на штатных ретраях видит
	// идемпотентность и тихо отвечает 200 без писем; этот флаг просит
	// переотправить уведомления (клиенту/админу) для уже учтённого платежа.
	// omitempty — чтобы для обычных вебхуков подпись и тело не менялись.
	ForceNotify bool `json:"force_notify,omitempty"`
}

// WebhookSender отправляет уведомления на callback_url.
type WebhookSender struct {
	secret string
	client *http.Client
}

func NewWebhookSender(secret string) *WebhookSender {
	return &WebhookSender{
		secret: secret,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send отправляет POST на callbackURL с подписанным payload.
// Заголовок X-Webhook-Signature содержит HMAC-SHA256 от тела запроса.
// Retry: 3 попытки с интервалом 2, 5, 10 секунд.
func (ws *WebhookSender) Send(callbackURL string, payload WebhookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook: %w", err)
	}

	signature := ws.sign(body)

	delays := []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second}

	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}

		err = ws.doRequest(callbackURL, body, signature)
		if err == nil {
			log.Printf("Webhook sent: %s (attempt %d)", callbackURL, attempt+1)
			return nil
		}
		log.Printf("Webhook attempt %d failed for %s: %v", attempt+1, callbackURL, err)
	}

	return fmt.Errorf("webhook failed after %d attempts: %w", len(delays), err)
}

func (ws *WebhookSender) doRequest(url string, body []byte, signature string) error {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", signature)

	resp, err := ws.client.Do(req)
	if err != nil {
		return fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (ws *WebhookSender) sign(data []byte) string {
	mac := hmac.New(sha256.New, []byte(ws.secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature проверяет подпись (для получающей стороны).
func VerifySignature(body []byte, signature string, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
