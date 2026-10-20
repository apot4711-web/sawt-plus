package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareRejectsCrossOrigin(t *testing.T) {
	called := false
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/gain", strings.NewReader(`{"gain":1}`))
	req.Header.Set("Content-Type", "application/json")
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
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/gain", strings.NewReader("gain=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestMiddlewareAllowsSameOrigin(t *testing.T) {
	called := false
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/gain", strings.NewReader(`{"gain":1}`))
	req.Header.Set("Content-Type", "application/json")
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
