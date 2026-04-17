package middleware

import (
	"crypto/subtle"
	"net/http"
)

// BasicAuth защищает handler логином/паролем. Использует constant-time
// сравнение, чтобы не было timing-атак.
func BasicAuth(realm, user, pass string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WrapFunc — обёртка для HandlerFunc, чтобы не писать http.HandlerFunc каждый раз.
func WrapFunc(mw func(http.Handler) http.Handler, h http.HandlerFunc) http.HandlerFunc {
	wrapped := mw(h)
	return func(w http.ResponseWriter, r *http.Request) {
		wrapped.ServeHTTP(w, r)
	}
}
