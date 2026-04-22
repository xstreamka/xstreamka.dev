package auth

import (
	"crypto/subtle"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// PasswordChecker — проверяет пароль против bcrypt-хэша или plaintext.
// Если установлен Hash — он используется (с константным временем, гарантируемым bcrypt).
// Иначе сравнивается Plain константным временем.
// Ровно одно из полей должно быть непустым.
type PasswordChecker struct {
	Hash  string // bcrypt hash, например "$2a$..."
	Plain string // plaintext fallback (для миграции)
}

// IsHash — эвристика: bcrypt-хэши начинаются с $2a$, $2b$, $2y$.
func IsHash(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

// Check — true, если input совпадает. Тихо возвращает false на любых ошибках.
func (p PasswordChecker) Check(input string) bool {
	if p.Hash != "" {
		return bcrypt.CompareHashAndPassword([]byte(p.Hash), []byte(input)) == nil
	}
	if p.Plain == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(p.Plain), []byte(input)) == 1
}
