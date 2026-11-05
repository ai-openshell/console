package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/go-chi/chi/v5"

	"github.com/ai-openshell/console/backend/pkg/services"
)

func TestPagedListRoutesFetchOneGatewayPage(t *testing.T) {
	for _, route := range []string{"sandboxes", "workspaces", "all-sandboxes"} {
		t.Run(route, func(t *testing.T) {
			sdk := &mockSDK{}
			calls := 0
			var gotOpts openshell.ListOptions
			var gotWorkspace string
			sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				t.Fatal("paged request must not call ListAll")
				return nil, nil
			}
			sdk.workspaces.listFn = func(context.Context, ...openshell.ListOptions) ([]*openshell.Workspace, error) {
				t.Fatal("paged request must not call ListAll")
				return nil, nil
			}
			sdk.sandboxes.listPageFn = func(_ context.Context, workspace string, opts ...openshell.ListOptions) (*openshell.Page[*openshell.Sandbox], error) {
				calls++
				gotWorkspace, gotOpts = workspace, opts[0]
				return &openshell.Page[*openshell.Sandbox]{Items: []*openshell.Sandbox{{Name: "agent", Workspace: "team-a"}}, NextPageToken: "next-token"}, nil
			}
			sdk.workspaces.listPageFn = func(_ context.Context, opts ...openshell.ListOptions) (*openshell.Page[*openshell.Workspace], error) {
				calls++
				gotOpts = opts[0]
				return &openshell.Page[*openshell.Workspace]{Items: []*openshell.Workspace{{Name: "team-a"}}, NextPageToken: "next-token"}, nil
			}

			r := chi.NewRouter()
			path := "/workspaces/team-a/sandboxes"
			switch route {
			case "sandboxes":
				r.Get("/workspaces/{workspace}/sandboxes", NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil)).ListSandboxes)
			case "workspaces":
				path = "/workspaces"
				r.Get(path, NewWorkspacesHandler(sdk.Workspaces()).ListWorkspaces)
			case "all-sandboxes":
				path = "/sandboxes"
				r.Get(path, NewAllWorkspacesHandler(services.NewSandboxService(sdk.Sandboxes(), nil), nil, nil, nil).ListSandboxes)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?pageSize=1&pageToken=prior-token&labelSelector=type%3Dtool", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
			if calls != 1 {
				t.Fatalf("SDK page calls = %d, want 1", calls)
			}
			want := openshell.ListOptions{PageSize: 1, PageToken: "prior-token", LabelSelector: "type=tool", AllWorkspaces: route == "all-sandboxes"}
			if !reflect.DeepEqual(gotOpts, want) {
				t.Errorf("options = %+v, want %+v", gotOpts, want)
			}
			if route == "sandboxes" && gotWorkspace != "team-a" || route == "all-sandboxes" && gotWorkspace != "" {
				t.Errorf("workspace = %q", gotWorkspace)
			}
			var body struct {
				NextPageToken string            `json:"nextPageToken"`
				Items         []json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Items) != 1 || body.NextPageToken != "next-token" {
				t.Errorf("body = %s", w.Body.String())
			}
		})
	}
}

func TestPagedListValidation(t *testing.T) {
	for _, path := range []string{"/workspaces", "/workspaces/team-a/sandboxes", "/sandboxes"} {
		for _, query := range []string{"pageSize=0", "pageSize=-1", "pageSize=101", "pageSize=abc", "pageSize=999999999999999999999999", "pageToken=token"} {
			t.Run(path+"?"+query, func(t *testing.T) {
				sdk := &mockSDK{}
				r := chi.NewRouter()
				r.Get("/workspaces", NewWorkspacesHandler(sdk.Workspaces()).ListWorkspaces)
				r.Get("/workspaces/{workspace}/sandboxes", NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil)).ListSandboxes)
				r.Get("/sandboxes", NewAllWorkspacesHandler(services.NewSandboxService(sdk.Sandboxes(), nil), nil, nil, nil).ListSandboxes)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
				if w.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestPagedListEmptyAndGatewayError(t *testing.T) {
	sdk := &mockSDK{}
	r := chi.NewRouter()
	r.Get("/workspaces", NewWorkspacesHandler(sdk.Workspaces()).ListWorkspaces)
	sdk.workspaces.listPageFn = func(context.Context, ...openshell.ListOptions) (*openshell.Page[*openshell.Workspace], error) {
		return &openshell.Page[*openshell.Workspace]{}, nil
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces?pageSize=10", nil))
	var emptyPage map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &emptyPage); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || string(emptyPage["items"]) != "[]" || string(emptyPage["nextPageToken"]) != `""` {
		t.Errorf("empty page: status %d, body %s", w.Code, w.Body.String())
	}
	sdk.workspaces.listPageFn = func(context.Context, ...openshell.ListOptions) (*openshell.Page[*openshell.Workspace], error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "gateway down"}
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces?pageSize=10", nil))
	if w.Code != http.StatusBadGateway {
		t.Errorf("gateway error: status %d, body %s", w.Code, w.Body.String())
	}
}
