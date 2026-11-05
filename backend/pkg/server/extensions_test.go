package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/handlers"
)

func testApp(extensions ...Extension) *App {
	return &App{
		auth:       auth.New(auth.Config{}),
		drafts:     &handlers.DraftsHandler{},
		files:      &handlers.FilesHandler{},
		gateway:    &handlers.GatewayHandler{},
		logs:       &handlers.LogsHandler{},
		policies:   &handlers.PoliciesHandler{},
		providers:  &handlers.ProvidersHandler{},
		sandboxes:  &handlers.SandboxHandler{},
		services:   &handlers.ServicesHandler{},
		settings:   &handlers.SettingsHandler{},
		terminal:   &handlers.TerminalHandler{},
		templates:  &handlers.TemplatesHandler{},
		workspaces: &handlers.WorkspacesHandler{},
		extensions: extensions,
	}
}

func TestExtensions_OverrideAndAddRoutes(t *testing.T) {
	app := testApp(
		func(r chi.Router) {
			r.Get("/gateway", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
		},
		func(r chi.Router) {
			r.Get("/gateway", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
			})
			r.Post("/extra/sync", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
		},
	)
	router := app.Routes()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/gateway", nil)
	request.Header.Set("x-forwarded-access-token", "test-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("overridden route status = %d, want %d", recorder.Code, http.StatusCreated)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/extra/sync", nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated extension status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/extra/sync", nil)
	request.Header.Set("x-forwarded-access-token", "test-token")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("authenticated extension status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}
