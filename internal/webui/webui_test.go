package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beyond5959/ngent/internal/webui"
)

func TestHandlerServesIndexHTML(t *testing.T) {
	h := webui.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("GET / expected text/html content-type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "Ngent") {
		t.Fatalf("GET / expected body to contain 'Ngent', got: %s", w.Body.String())
	}
}

func TestHandlerServesAssets(t *testing.T) {
	h := webui.Handler()

	// Vite places JS/CSS under /assets/. We don't know the exact hashed filename,
	// so we verify that /assets/ directory returns 200 (directory listing is enabled
	// by http.FS; the response contains HTML, which is acceptable).
	req := httptest.NewRequest(http.MethodGet, "/assets/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /assets/ expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandlerServesFavicon(t *testing.T) {
	h := webui.Handler()

	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /favicon.svg expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "image/svg+xml") {
		t.Fatalf("GET /favicon.svg expected image/svg+xml content-type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "<svg") {
		t.Fatalf("GET /favicon.svg expected SVG body, got: %s", w.Body.String())
	}
}

func TestHandlerSPAFallback(t *testing.T) {
	h := webui.Handler()

	paths := []string{"/threads", "/threads/abc-123", "/settings", "/unknown/deep/path"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("SPA fallback %s expected 200, got %d", p, w.Code)
			continue
		}
		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			t.Errorf("SPA fallback %s expected text/html, got %q", p, ct)
		}
		if !strings.Contains(w.Body.String(), "Ngent") {
			t.Errorf("SPA fallback %s expected body to contain 'Ngent'", p)
		}
	}
}
