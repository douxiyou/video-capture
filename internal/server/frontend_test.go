package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFrontendHandler(t *testing.T) {
	frontend := fstest.MapFS{
		"index.html":        {Data: []byte("<html>app</html>")},
		"assets/app.js":     {Data: []byte("console.log('app')")},
		"assets/index.html": {Data: []byte("directory index")},
	}
	handler := newRouterWithFrontend(nil, frontend)
	tests := []struct {
		method, path, accept string
		status               int
		body                 string
	}{
		{http.MethodGet, "/", "", http.StatusOK, "<html>app</html>"},
		{http.MethodGet, "/assets/app.js", "", http.StatusOK, "console.log('app')"},
		{http.MethodGet, "/dashboard", "text/html", http.StatusOK, "<html>app</html>"},
		{http.MethodHead, "/dashboard", "text/html", http.StatusOK, ""},
		{http.MethodGet, "/dashboard", "application/json", http.StatusNotFound, "404"},
		{http.MethodGet, "/assets/missing.js", "text/html", http.StatusNotFound, "404"},
		{http.MethodGet, "/assets/missing", "text/html", http.StatusNotFound, "404"},
		{http.MethodGet, "/assets/", "text/html", http.StatusNotFound, "404"},
		{http.MethodGet, "/api/missing", "text/html", http.StatusNotFound, "404"},
		{http.MethodGet, "/health", "text/html", http.StatusOK, `{"status":"ok"}`},
		{http.MethodGet, "/health/missing", "text/html", http.StatusNotFound, "404"},
		{http.MethodPost, "/dashboard", "text/html", http.StatusNotFound, "404"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path+" "+tt.accept, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Accept", tt.accept)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.body) {
				t.Fatalf("status = %d, body = %q; want %d, %q", rec.Code, rec.Body.String(), tt.status, tt.body)
			}
		})
	}
}

func TestFrontendMissingDirectory(t *testing.T) {
	handler := frontendHandler(os.DirFS(filepath.Join(t.TempDir(), "missing")))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFrontendDirectoryWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	frontendHandler(os.DirFS(dir)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "private.txt") {
		t.Fatalf("status = %d, body = %q; want 404 without directory listing", rec.Code, rec.Body.String())
	}
}
