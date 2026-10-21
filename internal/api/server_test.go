package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apot4711-web/sawt-plus/internal/config"
)

const testToken = "test-api-token-0123456789abcdef"

func TestMiddlewareRejectsCrossOrigin(t *testing.T) {
	called := false
	handler := withMiddleware(testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/gain", strings.NewReader(`{"gain":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if called {
		t.Fatal("handler must not run for cross-origin requests")
	}
}

func TestMiddlewareRequiresJSON(t *testing.T) {
	handler := withMiddleware(testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/gain", strings.NewReader("gain=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestMiddlewareAllowsSameOrigin(t *testing.T) {
	called := false
	handler := withMiddleware(testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/gain", strings.NewReader(`{"gain":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "http://127.0.0.1:8787")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !called {
		t.Fatal("handler should run for same-origin requests")
	}
}

func TestMiddlewareRequiresToken(t *testing.T) {
	called := false
	handler := withMiddleware(testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("handler must not run without a token")
	}
}

func TestMiddlewareAcceptsCookie(t *testing.T) {
	called := false
	handler := withMiddleware(testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/status", nil)
	req.AddCookie(&http.Cookie{Name: authCookie, Value: testToken})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("status = %d, called = %v; want 200/true", rec.Code, called)
	}
}

func TestMiddlewareExchangesURLTokenForCookie(t *testing.T) {
	handler := withMiddleware(testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the UI handler must not run for the token exchange")
	}))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/?token="+testToken, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookie || cookies[0].Value != testToken {
		t.Fatalf("unexpected cookies: %+v", cookies)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie is not hardened: %+v", cookies[0])
	}
}

func TestMiddlewareRejectsWrongURLToken(t *testing.T) {
	handler := withMiddleware(testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/?token=wrong", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestPublicConfigHidesTokens(t *testing.T) {
	cfg := config.Config{Devices: []config.DeviceEntry{
		{ID: "a", Address: "192.168.1.10:8765", Token: "secret"},
	}}
	out := publicConfig(cfg)
	if out.Devices[0].Token != "" {
		t.Fatalf("token leaked: %+v", out.Devices[0])
	}
	if cfg.Devices[0].Token != "secret" {
		t.Fatal("publicConfig must not mutate the caller's copy")
	}
}
