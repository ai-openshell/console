package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ai-openshell/sdk-go/openshell/v1/fake"

	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
)

// A cache between the browser and the BFF must never replay an API answer:
// a list read again after a create came back without the new workspace
// (issue #129). Every /api/v1 response says no-store, public or not, success
// or error; the static frontend does not.
func TestAPIResponsesAreNoStore(t *testing.T) {
	static := t.TempDir()
	if err := os.WriteFile(filepath.Join(static, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(static, "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp(fake.NewClient(), nil, auth.New(auth.Config{Disabled: true}), static, models.AuthConfigResponse{AuthDisabled: true})
	router := app.Routes()

	apiTests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "public", method: http.MethodGet, path: "/api/v1/auth/config"},
		{name: "health", method: http.MethodGet, path: "/api/v1/healthz"},
		{name: "workspace list", method: http.MethodGet, path: "/api/v1/workspaces"},
		{name: "handler error", method: http.MethodGet, path: "/api/v1/workspaces/team-a/sandboxes/sb1/policy/revisions/0"},
		{name: "unknown API route", method: http.MethodGet, path: "/api/v1/no-such-route"},
	}
	for _, tc := range apiTests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("%s %s (status %d): Cache-Control = %q, want no-store", tc.method, tc.path, recorder.Code, got)
			}
		})
	}

	for _, path := range []string{"/", "/app.js", "/workspaces/team-a"} {
		t.Run("static "+path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", path, recorder.Code)
			}
			if got := recorder.Header().Get("Cache-Control"); got != "" {
				t.Errorf("GET %s: Cache-Control = %q, want none", path, got)
			}
		})
	}
}

// The header is set before the handler runs, so a handler with a policy of
// its own (the logs stream's "no-cache, no-transform") keeps it.
func TestNoStoreLeavesAHandlersOwnCacheControl(t *testing.T) {
	handler := noStore(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache, no-transform" {
		t.Errorf("Cache-Control = %q, want the handler's own", got)
	}
}
