package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerRejectsUnauthorizedWhenTokenConfigured(t *testing.T) {
	handler := NewHandler("secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerServesAdminPageWithBearerAuth(t *testing.T) {
	handler := NewHandler("secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("content-type = %q, want text/html", got)
	}
	if !strings.Contains(rec.Body.String(), "/admin/api/queue") {
		t.Fatalf("body = %q, want admin api references", rec.Body.String())
	}
}
