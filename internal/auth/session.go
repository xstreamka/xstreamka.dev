// Package auth — cookie-based session auth для админки.
//
// Сессия — HMAC-SHA256 подписанный токен "username|expUnix|hmac" в cookie.
// Без хранилища на сервере: отзыв возможен только сменой SESSION_SECRET или ожиданием истечения.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	CookieName    = "pay_session"
	DefaultTTL    = 12 * time.Hour
	RememberTTL   = 30 * 24 * time.Hour
	cookiePath    = "/admin"
	userMaxLength = 128
)

var (
	ErrInvalidToken = errors.New("invalid session token")
	ErrExpired      = errors.New("session expired")
)

// SessionManager — выпускает и проверяет session-куки.
type SessionManager struct {
	secret []byte
	secure bool // Secure-флаг на куки (HTTPS только)
}

func NewSessionManager(secret string, secure bool) (*SessionManager, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("session secret must be at least 32 chars")
	}
	return &SessionManager{secret: []byte(secret), secure: secure}, nil
}

// Issue — ставит cookie с токеном на указанный TTL. Если ttl == 0, используется DefaultTTL.
func (sm *SessionManager) Issue(w http.ResponseWriter, username string, ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	exp := time.Now().Add(ttl)
	token := sm.sign(username, exp)

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     cookiePath,
		Expires:  exp,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Clear — удаляет cookie (logout).
func (sm *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Verify — возвращает имя пользователя, если cookie валидна и не истекла.
func (sm *SessionManager) Verify(r *http.Request) (string, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", ErrInvalidToken
	}
	return sm.parse(c.Value)
}

// ── internal ──

func (sm *SessionManager) sign(username string, exp time.Time) string {
	payload := fmt.Sprintf("%s|%d", username, exp.Unix())
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

func (sm *SessionManager) parse(token string) (string, error) {
	dot := strings.IndexByte(token, '.')
	if dot < 0 {
		return "", ErrInvalidToken
	}
	payloadB64, sigB64 := token[:dot], token[dot+1:]

	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return "", ErrInvalidToken
	}

	mac := hmac.New(sha256.New, sm.secret)
	mac.Write(payload)
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(sig, expected) != 1 {
		return "", ErrInvalidToken
	}

	pipe := strings.LastIndexByte(string(payload), '|')
	if pipe <= 0 {
		return "", ErrInvalidToken
	}
	username := string(payload[:pipe])
	if len(username) == 0 || len(username) > userMaxLength {
		return "", ErrInvalidToken
	}
	expUnix, err := strconv.ParseInt(string(payload[pipe+1:]), 10, 64)
	if err != nil {
		return "", ErrInvalidToken
	}
	if time.Now().Unix() > expUnix {
		return "", ErrExpired
	}
	return username, nil
}
