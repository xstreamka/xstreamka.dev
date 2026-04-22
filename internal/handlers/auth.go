package handlers

import (
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pay-service/internal/auth"
)

type AuthHandler struct {
	sessions  *auth.SessionManager
	limiter   *auth.LoginLimiter
	templates *template.Template

	adminUser string
	password  auth.PasswordChecker
}

func NewAuthHandler(
	sessions *auth.SessionManager,
	limiter *auth.LoginLimiter,
	templates *template.Template,
	adminUser string,
	password auth.PasswordChecker,
) *AuthHandler {
	return &AuthHandler{
		sessions:  sessions,
		limiter:   limiter,
		templates: templates,
		adminUser: adminUser,
		password:  password,
	}
}

// LoginPage — GET /admin/login.
func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	// если уже авторизован — пропускаем на dashboard
	if _, err := h.sessions.Verify(r); err == nil {
		http.Redirect(w, r, "/admin/payments", http.StatusSeeOther)
		return
	}
	h.render(w, loginData{Next: sanitizeNext(r.URL.Query().Get("next"))})
}

// LoginSubmit — POST /admin/login.
func (h *AuthHandler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.PostForm.Get("username"))
	password := r.PostForm.Get("password")
	remember := r.PostForm.Get("remember") == "1"
	next := sanitizeNext(r.PostForm.Get("next"))

	ip := clientIP(r)
	if ok, wait := h.limiter.Allowed(ip); !ok {
		log.Printf("Login: rate-limited ip=%s wait=%s", ip, wait.Round(time.Second))
		h.renderError(w, "Слишком много попыток. Попробуйте позже.", username, next, http.StatusTooManyRequests)
		return
	}

	if username != h.adminUser || !h.password.Check(password) {
		h.limiter.RegisterFailure(ip)
		log.Printf("Login: failed ip=%s user=%q", ip, username)
		h.renderError(w, "Неверный логин или пароль.", username, next, http.StatusUnauthorized)
		return
	}

	h.limiter.Reset(ip)
	ttl := auth.DefaultTTL
	if remember {
		ttl = auth.RememberTTL
	}
	h.sessions.Issue(w, username, ttl)
	log.Printf("Login: ok ip=%s user=%s", ip, username)

	http.Redirect(w, r, next, http.StatusSeeOther)
}

// Logout — POST /admin/logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	h.sessions.Clear(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// ── rendering ──

type loginData struct {
	Error string
	User  string
	Next  string
}

func (h *AuthHandler) render(w http.ResponseWriter, d loginData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "admin_login.html", d); err != nil {
		log.Printf("Auth: render: %v", err)
	}
}

func (h *AuthHandler) renderError(w http.ResponseWriter, msg, user, next string, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := h.templates.ExecuteTemplate(w, "admin_login.html", loginData{Error: msg, User: user, Next: next}); err != nil {
		log.Printf("Auth: render: %v", err)
	}
}

// sanitizeNext — разрешаем только относительные пути в /admin/*, чтобы не было open-redirect.
func sanitizeNext(next string) string {
	if next == "" {
		return "/admin/payments"
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "/admin/payments"
	}
	if !strings.HasPrefix(u.Path, "/admin/") {
		return "/admin/payments"
	}
	if strings.HasPrefix(u.Path, "/admin/login") || strings.HasPrefix(u.Path, "/admin/logout") {
		return "/admin/payments"
	}
	return u.RequestURI()
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
