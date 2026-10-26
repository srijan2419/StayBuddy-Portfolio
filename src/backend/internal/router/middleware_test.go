package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	rid := rr.Header().Get("X-Request-ID")
	if rid == "" {
		t.Error("expected X-Request-ID header to be set")
	}
	if len(rid) != 16 { // 8 bytes = 16 hex chars
		t.Errorf("expected 16 char request ID, got %d: %s", len(rid), rid)
	}
}

func TestRequestIDMiddleware_PreservesExisting(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "existing-id-123")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	rid := rr.Header().Get("X-Request-ID")
	if rid != "existing-id-123" {
		t.Errorf("expected preserved request ID 'existing-id-123', got %s", rid)
	}
}

func TestSecurityMiddleware_SetsHeaders(t *testing.T) {
	handler := SecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	tests := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":       "DENY",
		"X-XSS-Protection":      "1; mode=block",
	}

	for header, expected := range tests {
		if got := rr.Header().Get(header); got != expected {
			t.Errorf("expected %s=%s, got %s", header, expected, got)
		}
	}
}

func TestRequestSizeLimitMiddleware_AllowsGET(t *testing.T) {
	handler := RequestSizeLimitMiddleware(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
