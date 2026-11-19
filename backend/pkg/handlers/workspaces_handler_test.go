package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

func TestListWorkspaces(t *testing.T) {
	sdk := &mockSDK{}
	sdk.workspaces.listFn = func(_ context.Context, _ ...openshell.ListOptions) ([]*openshell.Workspace, error) {
		return []*openshell.Workspace{
			{Name: "team-a", Phase: openshell.WorkspaceActive},
			{Name: "team-b", Phase: openshell.WorkspaceTerminating},
		}, nil
	}
	handler := NewWorkspacesHandler(sdk.Workspaces())
	r := chi.NewRouter()
	r.Get("/workspaces", handler.ListWorkspaces)

	req := httptest.NewRequest(http.MethodGet, "/workspaces", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(body))
	}
	if body[0]["phase"] != "ACTIVE" {
		t.Errorf("phase = %v, want ACTIVE", body[0]["phase"])
	}
}

func TestListWorkspacesUnavailable(t *testing.T) {
	sdk := &mockSDK{}
	sdk.workspaces.listFn = func(_ context.Context, _ ...openshell.ListOptions) ([]*openshell.Workspace, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorUnauthenticated, Message: "missing token"}
	}
	handler := NewWorkspacesHandler(sdk.Workspaces())
	r := chi.NewRouter()
	r.Get("/workspaces", handler.ListWorkspaces)
	req := httptest.NewRequest(http.MethodGet, "/workspaces", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestCreateWorkspace(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{name: "success", body: `{"name":"team-a"}`, wantStatus: http.StatusCreated},
		{name: "missing name", body: `{"name":""}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_name"},
		{name: "invalid name - uppercase", body: `{"name":"Team-A"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_name"},
		{name: "invalid JSON", body: `not-json`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			handler := NewWorkspacesHandler(mock.Workspaces())
			r := chi.NewRouter()
			r.Post("/workspaces", handler.CreateWorkspace)
			req := httptest.NewRequest(http.MethodPost, "/workspaces", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var errBody map[string]any
				if err := json.NewDecoder(w.Body).Decode(&errBody); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if errBody["code"] != tc.wantCode {
					t.Errorf("code = %v, want %q", errBody["code"], tc.wantCode)
				}
			}
		})
	}
}

func TestGetWorkspace(t *testing.T) {
	mock := &mockSDK{}
	handler := NewWorkspacesHandler(mock.Workspaces())
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}", handler.GetWorkspace)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/team-a", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	meta, _ := body["metadata"].(map[string]any)
	if meta["name"] != "team-a" {
		t.Errorf("name = %v, want team-a", meta["name"])
	}
}

func TestDeleteWorkspace(t *testing.T) {
	mock := &mockSDK{}
	handler := NewWorkspacesHandler(mock.Workspaces())
	r := chi.NewRouter()
	r.Delete("/workspaces/{workspace}", handler.DeleteWorkspace)
	req := httptest.NewRequest(http.MethodDelete, "/workspaces/team-a", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]bool
	_ = json.NewDecoder(w.Body).Decode(&body)
	if !body["deleted"] {
		t.Errorf("deleted = %v, want true", body["deleted"])
	}
}

func TestDeleteWorkspaceNotFound(t *testing.T) {
	sdk := &mockSDK{}
	sdk.workspaces.deleteFn = func(_ context.Context, _ string) error {
		return &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "missing"}
	}
	handler := NewWorkspacesHandler(sdk.Workspaces())
	r := chi.NewRouter()
	r.Delete("/workspaces/{workspace}", handler.DeleteWorkspace)
	req := httptest.NewRequest(http.MethodDelete, "/workspaces/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAddMember(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "success", body: `{"principalSubject":"user@example.com","role":"USER"}`, wantStatus: http.StatusCreated},
		{name: "missing subject", body: `{"principalSubject":"","role":"USER"}`, wantStatus: http.StatusBadRequest},
		{name: "invalid role", body: `{"principalSubject":"u","role":"SUPER"}`, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			handler := NewWorkspacesHandler(mock.Workspaces())
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/members", handler.AddMember)
			req := httptest.NewRequest(http.MethodPost, "/workspaces/team-a/members", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestRemoveMember(t *testing.T) {
	mock := &mockSDK{}
	handler := NewWorkspacesHandler(mock.Workspaces())
	r := chi.NewRouter()
	r.Delete("/workspaces/{workspace}/members/{subject}", handler.RemoveMember)
	req := httptest.NewRequest(http.MethodDelete, "/workspaces/team-a/members/user%40example.com", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]bool
	_ = json.NewDecoder(w.Body).Decode(&body)
	if !body["removed"] {
		t.Errorf("removed = %v, want true", body["removed"])
	}
}

func TestListMembers(t *testing.T) {
	sdk := &mockSDK{}
	sdk.workspaces.listMembersFn = func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.WorkspaceMember, error) {
		return []*openshell.WorkspaceMember{
			{PrincipalSubject: "user@example.com", Role: openshell.WorkspaceRoleUser},
		}, nil
	}
	handler := NewWorkspacesHandler(sdk.Workspaces())
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/members", handler.ListMembers)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/team-a/members", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	if len(body) != 1 || body[0]["role"] != "USER" {
		t.Errorf("body = %v", body)
	}
}
