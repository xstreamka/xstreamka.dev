package payment

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// HashAlgo — алгоритм хеширования подписи (выбирается в ЛК Робокассы)
type HashAlgo string

const (
	AlgoMD5    HashAlgo = "md5"
	AlgoSHA256 HashAlgo = "sha256"
)

const (
	prodURL = "https://auth.robokassa.ru/Merchant/Index.aspx"
	testURL = "https://auth.robokassa.ru/Merchant/Index.aspx" // тот же URL, IsTest передаётся параметром
)

// RobokassaConfig хранит настройки из .env
type RobokassaConfig struct {
	MerchantLogin string
	Password1     string // для формирования подписи при создании платежа
	Password2     string // для верификации callback (ResultURL)
	IsTest        bool
	HashAlgo      HashAlgo
}

// ReceiptItem — позиция в чеке (для Робочеков СМЗ)
type ReceiptItem struct {
	Name          string  `json:"name"`
	Quantity      int     `json:"quantity"`
	Sum           float64 `json:"sum"`
	Tax           string  `json:"tax"`            // "none" для самозанятых (НПД)
	PaymentMethod string  `json:"payment_method"` // "full_payment"
	PaymentObject string  `json:"payment_object"` // "service"
}

// Receipt — параметр фискализации
type Receipt struct {
	Items []ReceiptItem `json:"items"`
}

// Robokassa — сервис для работы с Робокассой
type Robokassa struct {
	cfg RobokassaConfig
}

func NewRobokassa(cfg RobokassaConfig) *Robokassa {
	if cfg.HashAlgo == "" {
		cfg.HashAlgo = AlgoMD5
	}
	return &Robokassa{cfg: cfg}
}

// GeneratePaymentURL формирует URL для редиректа пользователя на оплату.
//
// Формула подписи (без Receipt):
//
//	MerchantLogin:OutSum:InvId:Password1[:Shp_x=v:...]
//
// Формула подписи (с Receipt):
//
//	MerchantLogin:OutSum:InvId:Receipt:Password1[:Shp_x=v:...]
//
// Shp-параметры сортируются по алфавиту.
func (r *Robokassa) GeneratePaymentURL(invID int, outSum float64, description string, email string, receipt *Receipt, shpParams map[string]string) (string, error) {
	sumStr := formatSum(outSum)

	// Собираем Shp-параметры (сортировка по алфавиту)
	shpParts := buildShpParts(shpParams)

	// Формируем Receipt JSON (если есть)
	var receiptJSON string
	if receipt != nil {
		data, err := json.Marshal(receipt)
		if err != nil {
			return "", fmt.Errorf("marshal receipt: %w", err)
		}
		receiptJSON = string(data)
	}

	// Формируем строку для подписи
	sigParts := []string{r.cfg.MerchantLogin, sumStr, fmt.Sprintf("%d", invID)}
	if receiptJSON != "" {
		sigParts = append(sigParts, receiptJSON)
	}
	sigParts = append(sigParts, r.cfg.Password1)
	sigParts = append(sigParts, shpParts...)

	sigString := strings.Join(sigParts, ":")
	signature := r.hash(sigString)

	// Формируем URL
	params := url.Values{}
	params.Set("MerchantLogin", r.cfg.MerchantLogin)
	params.Set("OutSum", sumStr)
	params.Set("InvId", fmt.Sprintf("%d", invID))
	params.Set("Description", description)
	params.Set("SignatureValue", signature)

	if receiptJSON != "" {
		params.Set("Receipt", receiptJSON)
	}
	if email != "" {
		params.Set("Email", email)
	}
	if r.cfg.IsTest {
		params.Set("IsTest", "1")
	}

	// Добавляем Shp-параметры в URL
	for k, v := range shpParams {
		params.Set(k, v)
	}

	return prodURL + "?" + params.Encode(), nil
}

// VerifyResultSignature проверяет подпись в callback от Робокассы (ResultURL).
//
// Формула: OutSum:InvId:Password2[:Shp_x=v:...]
func (r *Robokassa) VerifyResultSignature(outSum string, invID string, signatureValue string, shpParams map[string]string) bool {
	shpParts := buildShpParts(shpParams)

	sigParts := []string{outSum, invID, r.cfg.Password2}
	sigParts = append(sigParts, shpParts...)

	sigString := strings.Join(sigParts, ":")
	expected := r.hash(sigString)

	return strings.EqualFold(expected, signatureValue)
}

// VerifySuccessSignature проверяет подпись на SuccessURL.
//
// Формула: OutSum:InvId:Password1[:Shp_x=v:...]
func (r *Robokassa) VerifySuccessSignature(outSum string, invID string, signatureValue string, shpParams map[string]string) bool {
	shpParts := buildShpParts(shpParams)

	sigParts := []string{outSum, invID, r.cfg.Password1}
	sigParts = append(sigParts, shpParts...)

	sigString := strings.Join(sigParts, ":")
	expected := r.hash(sigString)

	return strings.EqualFold(expected, signatureValue)
}

// BuildReceipt создаёт Receipt для VPN-подписки (самозанятый, НПД).
func BuildReceipt(description string, amount float64) *Receipt {
	return &Receipt{
		Items: []ReceiptItem{
			{
				Name:          description,
				Quantity:      1,
				Sum:           amount,
				Tax:           "none", // самозанятый — без НДС
				PaymentMethod: "full_payment",
				PaymentObject: "service",
			},
		},
	}
}

// hash вычисляет хеш строки выбранным алгоритмом
func (r *Robokassa) hash(s string) string {
	switch r.cfg.HashAlgo {
	case AlgoSHA256:
		h := sha256.Sum256([]byte(s))
		return hex.EncodeToString(h[:])
	default: // MD5
		h := md5.Sum([]byte(s))
		return hex.EncodeToString(h[:])
	}
}

// buildShpParts собирает Shp-параметры в формат "Shp_x=v", отсортированные по алфавиту
func buildShpParts(shpParams map[string]string) []string {
	if len(shpParams) == 0 {
		return nil
	}

	keys := make([]string, 0, len(shpParams))
	for k := range shpParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, shpParams[k]))
	}
	return parts
}

// formatSum форматирует сумму: "150.00"
func formatSum(sum float64) string {
	return fmt.Sprintf("%.2f", sum)
}
