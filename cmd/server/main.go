package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"pay-service/internal/middleware"
	"pay-service/internal/reconciler"
	"syscall"
	"time"

	"pay-service/internal/config"
	"pay-service/internal/database"
	"pay-service/internal/handlers"
	"pay-service/internal/models"
	"pay-service/internal/payment"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config: %v", err)
	}

	db, err := database.Connect(cfg.DSN())
	if err != nil {
		log.Fatalf("Database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Fatalf("Migration: %v", err)
	}

	paymentStore := models.NewPaymentStore(db.Pool)

	robokassa := payment.NewRobokassa(payment.RobokassaConfig{
		MerchantLogin: cfg.RobokassaLogin,
		Password1:     cfg.RobokassaPass1,
		Password2:     cfg.RobokassaPass2,
		IsTest:        cfg.RobokassaTest,
		HashAlgo:      payment.HashAlgo(cfg.RobokassaAlgo),
	})

	webhookSender := payment.NewWebhookSender(cfg.WebhookSecret)

	// Reconciler — добивает непришедшие webhook-и
	rec := reconciler.New(paymentStore, webhookSender, reconciler.DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	go rec.Run(ctx)

	tmpl, err := template.ParseGlob("internal/templates/pay/*.html")
	if err != nil {
		log.Fatalf("Templates: %v", err)
	}

	h := handlers.NewPaymentHandler(paymentStore, robokassa, webhookSender, cfg.WebhookSecret, cfg.SiteURL, tmpl)

	mux := http.NewServeMux()

	// ─── Admin UI ───
	adminH := handlers.NewAdminHandler(paymentStore, webhookSender, tmpl)
	auth := middleware.BasicAuth("pay-service admin", cfg.AdminUser, cfg.AdminPassword)

	mux.Handle("GET /admin/payments", auth(http.HandlerFunc(adminH.Payments)))
	mux.Handle("GET /admin/payments/{inv_id}", auth(http.HandlerFunc(adminH.PaymentDetail)))
	mux.Handle("POST /admin/payments/{inv_id}/retry-webhook", auth(http.HandlerFunc(adminH.RetryWebhook)))

	log.Println("Admin UI enabled at /admin/payments")

	// Checkout — принимает подписанные параметры, создаёт платёж, редирект
	mux.HandleFunc("GET /pay/checkout", h.Checkout)
	// Страница оплаты — чистый URL, безопасен для F5
	mux.HandleFunc("GET /pay/order/{id}", h.OrderPage)
	// Отмена платежа, возращение назад
	mux.HandleFunc("POST /pay/order/{id}/cancel", h.CancelOrder)

	// Тестовая страница — генерирует подписанную ссылку (убрать в продакшене)
	mux.HandleFunc("GET /pay/demo", func(w http.ResponseWriter, r *http.Request) {
		params := map[string]string{
			"product_type": "vpn",
			"plan_id":      "basic_30",
			"amount":       "150.00",
			"description":  "VPN Basic 30 days 50GB",
			"user_ref":     "1",
			"email":        "test@xstreamka.dev",
			"callback_url": "",
			"return_url":   cfg.SiteURL,
			"metadata":     `{"traffic_gb":50,"duration_days":30}`,
			"ts":           fmt.Sprintf("%d", time.Now().Unix()),
		}
		sig := handlers.SignRedirectParams(params, cfg.WebhookSecret)

		q := make(url.Values)
		for k, v := range params {
			q.Set(k, v)
		}
		q.Set("sig", sig)

		checkoutURL := cfg.SiteURL + "/pay/checkout?" + q.Encode()
		http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
	})

	// Робокасса callbacks
	mux.HandleFunc("POST /payments/result", h.ResultURL)
	mux.HandleFunc("GET /payments/result", h.ResultURL)
	mux.HandleFunc("GET /payments/success", h.SuccessURL)
	mux.HandleFunc("GET /payments/fail", h.FailURL)

	// Лендинг — статика из папки static/ (index.html, favicon.svg, оферта)
	fs := http.FileServer(http.Dir("static"))
	mux.Handle("/", fs)

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: logMiddleware(mux)}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel() // ← останавливаем reconciler
		srv.Close()
	}()

	log.Printf("Pay service started on %s", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server: %v", err)
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.Header.Get("X-Real-IP")
		if ip == "" {
			ip = r.RemoteAddr
		}
		log.Printf("%s %s %s", r.Method, r.URL.Path, ip)
		next.ServeHTTP(w, r)
	})
}
