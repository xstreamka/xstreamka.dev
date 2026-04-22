package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testSecret = "this-is-a-32-chars-long-secret!!"

func TestIssueAndVerify(t *testing.T) {
	sm, err := NewSessionManager(testSecret, false)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	sm.Issue(rec, "admin", 0)

	req := httptest.NewRequest("GET", "/admin/payments", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}

	got, err := sm.Verify(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != "admin" {
		t.Fatalf("want admin, got %q", got)
	}
}

func TestVerifyExpired(t *testing.T) {
	sm, _ := NewSessionManager(testSecret, false)
	// выпускаем токен, уже просроченный
	token := sm.sign("admin", time.Now().Add(-time.Minute))
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})

	if _, err := sm.Verify(req); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	sm, _ := NewSessionManager(testSecret, false)
	token := sm.sign("admin", time.Now().Add(time.Hour))

	// меняем последний символ подписи
	b := []byte(token)
	if b[len(b)-1] == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: string(b)})

	if _, err := sm.Verify(req); err != ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	sm1, _ := NewSessionManager(testSecret, false)
	sm2, _ := NewSessionManager("OTHER-secret-that-is-long-enough!!", false)

	rec := httptest.NewRecorder()
	sm1.Issue(rec, "admin", 0)
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if _, err := sm2.Verify(req); err != ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestShortSecretRejected(t *testing.T) {
	if _, err := NewSessionManager("short", false); err == nil {
		t.Fatal("expected error for short secret")
	}
}

func TestClearCookie(t *testing.T) {
	sm, _ := NewSessionManager(testSecret, false)
	rec := httptest.NewRecorder()
	sm.Clear(rec)
	h := rec.Header().Get("Set-Cookie")
	if !strings.Contains(h, CookieName+"=") || !strings.Contains(h, "Max-Age=0") {
		t.Fatalf("unexpected clear cookie: %q", h)
	}
}

func TestPasswordChecker(t *testing.T) {
	// plaintext path
	p := PasswordChecker{Plain: "hunter2"}
	if !p.Check("hunter2") {
		t.Fatal("plain check failed")
	}
	if p.Check("wrong") {
		t.Fatal("wrong password accepted")
	}

	// bcrypt path (hash of "hunter2", cost 4 for speed)
	// сгенерим на лету чтобы не хранить захардкоженный хэш
	h := mustHashForTest(t, "hunter2")
	ph := PasswordChecker{Hash: h}
	if !ph.Check("hunter2") {
		t.Fatal("bcrypt check failed")
	}
	if ph.Check("wrong") {
		t.Fatal("bcrypt wrong accepted")
	}

	// IsHash
	if !IsHash(h) {
		t.Fatal("IsHash should detect bcrypt")
	}
	if IsHash("plaintext") {
		t.Fatal("IsHash false positive")
	}
}

func TestLoginLimiter(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	ip := "1.2.3.4"
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allowed(ip); !ok {
			t.Fatalf("locked too early at i=%d", i)
		}
		l.RegisterFailure(ip)
	}
	// 3rd failure — порог достигнут
	if ok, _ := l.Allowed(ip); !ok {
		t.Fatal("should still be allowed before 3rd attempt registers lock")
	}
	l.RegisterFailure(ip)
	if ok, _ := l.Allowed(ip); ok {
		t.Fatal("should be locked after threshold")
	}

	// сброс после удачного логина
	l.Reset(ip)
	if ok, _ := l.Allowed(ip); !ok {
		t.Fatal("should be allowed after reset")
	}
}

func mustHashForTest(t *testing.T, pw string) string {
	t.Helper()
	// импортируем bcrypt здесь, чтобы не тащить его в рантайм-тесты всего пакета
	b, err := bcryptHashForTest(pw)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
