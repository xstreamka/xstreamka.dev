package auth

import (
	"sync"
	"time"
)

// LoginLimiter — простой in-memory rate-limit для /login.
// Считает неудачные попытки по ключу (обычно IP). После Threshold попыток
// блокирует на Window. Старые записи чистятся лениво.
type LoginLimiter struct {
	mu        sync.Mutex
	attempts  map[string]*attemptState
	Threshold int
	Window    time.Duration
}

type attemptState struct {
	count       int
	firstFailAt time.Time
	lockedUntil time.Time
}

func NewLoginLimiter(threshold int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{
		attempts:  make(map[string]*attemptState),
		Threshold: threshold,
		Window:    window,
	}
}

// Allowed — true, если по ключу сейчас разрешена попытка логина.
// Если false — возвращает сколько секунд ещё ждать.
func (l *LoginLimiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	st, ok := l.attempts[key]
	if !ok {
		return true, 0
	}
	if now.Before(st.lockedUntil) {
		return false, st.lockedUntil.Sub(now)
	}
	// окно сбросилось — начинаем заново
	if now.Sub(st.firstFailAt) > l.Window {
		delete(l.attempts, key)
	}
	return true, 0
}

// RegisterFailure — инкрементит счётчик; при достижении порога выставляет lock.
func (l *LoginLimiter) RegisterFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	st, ok := l.attempts[key]
	if !ok || now.Sub(st.firstFailAt) > l.Window {
		l.attempts[key] = &attemptState{count: 1, firstFailAt: now}
		return
	}
	st.count++
	if st.count >= l.Threshold {
		st.lockedUntil = now.Add(l.Window)
	}
}

// Reset — сбрасывает счётчик (успешный логин).
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
