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

func TestListServices(t *testing.T) {
	sdk := &mockSDK{}
	sdk.services.listFn = func(_ context.Context, _, _ string, _ ...openshell.ListOptions) ([]*openshell.ServiceEndpoint, error) {
		return []*openshell.ServiceEndpoint{
			{Sandbox: "my-sandbox", Name: "web", TargetPort: 8080, URL: "https://web.example"},
		}, nil
	}
	handler := NewServicesHandler(sdk.Services())
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/services", handler.ListServices)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/my-sandbox/services", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	if len(body) != 1 || body[0]["serviceName"] != "web" {
		t.Errorf("body = %v", body)
	}
}

// Without a sandbox name in the route the list covers the whole workspace: the
// gateway takes an empty sandbox filter to mean every sandbox.
func TestListServicesOfAWorkspace(t *testing.T) {
	sdk := &mockSDK{}
	var gotWorkspace, gotSandbox string
	asked := false
	sdk.services.listFn = func(_ context.Context, workspace, sandbox string, _ ...openshell.ListOptions) ([]*openshell.ServiceEndpoint, error) {
		gotWorkspace, gotSandbox, asked = workspace, sandbox, true
		return []*openshell.ServiceEndpoint{
			{ID: "ep-1", SandboxID: "sb-1", Sandbox: "agent", Name: "web", TargetPort: 8080, Domain: true, Workspace: "team-a"},
			{ID: "ep-2", SandboxID: "sb-2", Sandbox: "other", Name: "", TargetPort: 3000, Domain: true, Workspace: "team-a"},
		}, nil
	}
	handler := NewServicesHandler(sdk.Services())
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/services", handler.ListServices)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/team-a/services", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !asked || gotWorkspace != "team-a" || gotSandbox != "" {
		t.Errorf("listed (workspace %q, sandbox %q), want (team-a, no sandbox)", gotWorkspace, gotSandbox)
	}
	var body []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("got %d endpoints, want 2: %v", len(body), body)
	}
	// Each endpoint names the sandbox and the workspace it belongs to, and
	// the unnamed one is listed with an empty service name.
	if body[0]["sandboxName"] != "agent" || body[0]["workspace"] != "team-a" || body[0]["id"] != "ep-1" || body[0]["sandboxId"] != "sb-1" {
		t.Errorf("first endpoint = %v", body[0])
	}
	if name, present := body[1]["serviceName"]; !present || name != "" {
		t.Errorf("serviceName of the unnamed endpoint = %v (present: %v), want an empty string", name, present)
	}
}

func TestExposeService(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{name: "success", body: `{"service":"web","targetPort":8080}`, wantStatus: http.StatusCreated},
		// The gateway allows a sandbox one endpoint without a name, which is
		// what `openshell service expose <sandbox> <port>` creates.
		{name: "unnamed service", body: `{"service":"","targetPort":8080}`, wantStatus: http.StatusCreated},
		{name: "service left out", body: `{"targetPort":8080}`, wantStatus: http.StatusCreated},
		{name: "zero port", body: `{"service":"web","targetPort":0}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			handler := NewServicesHandler(mock.Services())
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/sandboxes/{name}/services", handler.ExposeService)
			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes/my-sandbox/services", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var body map[string]any
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if body["code"] != tc.wantCode {
					t.Errorf("code = %v, want %q", body["code"], tc.wantCode)
				}
			}
		})
	}
}

// The request reaches the gateway as it was sent: the service name, empty for
// the unnamed endpoint, the port and the domain flag. What the gateway makes
// of a name or a port is the gateway's to say.
func TestExposeServiceForwardsTheRequest(t *testing.T) {
	type exposed struct {
		workspace, sandbox, service string
		port                        uint32
		domain                      bool
	}
	tests := []struct {
		name string
		body string
		want exposed
	}{
		{name: "named", body: `{"service":"web","targetPort":8080,"domain":true}`, want: exposed{"team-a", "agent", "web", 8080, true}},
		{name: "unnamed", body: `{"targetPort":3000,"domain":true}`, want: exposed{"team-a", "agent", "", 3000, true}},
		{name: "domain not asked for", body: `{"service":"web","targetPort":8080}`, want: exposed{"team-a", "agent", "web", 8080, false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			var got exposed
			mock.services.exposeFn = func(_ context.Context, workspace, sandbox, service string, port uint32, domain bool) (*openshell.ServiceEndpoint, error) {
				got = exposed{workspace, sandbox, service, port, domain}
				return &openshell.ServiceEndpoint{Sandbox: sandbox, Name: service, TargetPort: port, Domain: true, Workspace: workspace}, nil
			}
			handler := NewServicesHandler(mock.Services())
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/sandboxes/{name}/services", handler.ExposeService)
			req := httptest.NewRequest(http.MethodPost, "/workspaces/team-a/sandboxes/agent/services", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
			}
			if got != tc.want {
				t.Errorf("exposed %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A name or port the gateway does not take is refused by the gateway, in its
// own words.
func TestExposeServiceRelaysTheGatewaysRefusal(t *testing.T) {
	mock := &mockSDK{}
	mock.services.exposeFn = func(context.Context, string, string, string, uint32, bool) (*openshell.ServiceEndpoint, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: "service must be a lowercase DNS label"}
	}
	handler := NewServicesHandler(mock.Services())
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/{name}/services", handler.ExposeService)
	req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes/my-sandbox/services", strings.NewReader(`{"service":"Web","targetPort":8080}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "invalid_argument" || body["message"] != "service must be a lowercase DNS label" {
		t.Errorf("error = %v, want the gateway's invalid_argument and message", body)
	}
}

// The unnamed endpoint has no name to put in the path, so it is deleted on the
// route that stops at the collection. Both routes delete exactly one endpoint.
func TestDeleteServiceRoutes(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantService string
	}{
		{name: "named", path: "/workspaces/team-a/sandboxes/agent/services/web", wantService: "web"},
		{name: "unnamed", path: "/workspaces/team-a/sandboxes/agent/services", wantService: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			var deleted []string
			mock.services.deleteFn = func(_ context.Context, workspace, sandbox, service string) error {
				deleted = append(deleted, workspace+"/"+sandbox+"/"+service)
				return nil
			}
			handler := NewServicesHandler(mock.Services())
			r := chi.NewRouter()
			r.Delete("/workspaces/{workspace}/sandboxes/{name}/services/{svc}", handler.DeleteService)
			r.Delete("/workspaces/{workspace}/sandboxes/{name}/services", handler.DeleteService)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, tc.path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
			if want := []string{"team-a/agent/" + tc.wantService}; len(deleted) != 1 || deleted[0] != want[0] {
				t.Errorf("deleted %v, want %v", deleted, want)
			}
		})
	}
}

func TestDeleteService(t *testing.T) {
	mock := &mockSDK{}
	handler := NewServicesHandler(mock.Services())
	r := chi.NewRouter()
	r.Delete("/workspaces/{workspace}/sandboxes/{name}/services/{svc}", handler.DeleteService)
	req := httptest.NewRequest(http.MethodDelete, "/workspaces/default/sandboxes/my-sandbox/services/web", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]bool
	_ = json.NewDecoder(w.Body).Decode(&body)
	if !body["deleted"] {
		t.Errorf("deleted = %v", body["deleted"])
	}
}
