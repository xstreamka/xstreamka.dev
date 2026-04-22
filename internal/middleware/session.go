package middleware

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"pay-service/internal/auth"
)

type ctxKey int

const userCtxKey ctxKey = 1

// RequireSession — защищает handler сессионной кукой. Если невалидна — редирект на loginPath
// с ?next=<текущий путь>. Для POST/PUT/DELETE/PATCH также проверяется Origin/Referer
// против allowedOrigin (защита от CSRF).
func RequireSession(sm *auth.SessionManager, loginPath, allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isUnsafe(r.Method) {
				if !checkOrigin(r, allowedOrigin) {
					http.Error(w, "CSRF: bad origin", http.StatusForbidden)
					return
				}
			}
			username, err := sm.Verify(r)
			if err != nil {
				redirectToLogin(w, r, loginPath)
				return
			}
			ctx := context.WithValue(r.Context(), userCtxKey, username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext — имя текущего админа в контексте, если запрос прошёл RequireSession.
func UserFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userCtxKey).(string); ok {
		return v
	}
	return ""
}

func isUnsafe(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// checkOrigin — Origin или Referer должен матчиться с allowedOrigin.
// allowedOrigin — "https://xstreamka.dev" (scheme+host).
func checkOrigin(r *http.Request, allowed string) bool {
	if allowed == "" {
		return true // не настроено — не блокируем (dev)
	}
	if o := r.Header.Get("Origin"); o != "" {
		return o == allowed
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		return (u.Scheme + "://" + u.Host) == allowed
	}
	return false
}

func redirectToLogin(w http.ResponseWriter, r *http.Request, loginPath string) {
	next := r.URL.Path
	if r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	// не редиректим с самого login-пути
	if strings.HasPrefix(next, loginPath) {
		http.Redirect(w, r, loginPath, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, loginPath+"?next="+url.QueryEscape(next), http.StatusSeeOther)
}
