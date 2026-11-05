package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/fake"

	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
)

// withServices is an SDK client whose service endpoints come from a fixed
// list. The SDK's fake client answers every service call "unimplemented".
type withServices struct {
	openshell.ClientInterface
	deleted   *[]string
	endpoints []*openshell.ServiceEndpoint
}

func (c withServices) Services() openshell.ServiceInterface {
	return fixedServices{ServiceInterface: c.ClientInterface.Services(), endpoints: c.endpoints, deleted: c.deleted}
}

type fixedServices struct {
	openshell.ServiceInterface
	deleted   *[]string
	endpoints []*openshell.ServiceEndpoint
}

// ListAll filters the way the gateway does: by workspace unless every
// workspace was asked for, and by sandbox when one is named.
func (s fixedServices) ListAll(_ context.Context, workspace, sandbox string, opts ...openshell.ListOptions) ([]*openshell.ServiceEndpoint, error) {
	everyWorkspace := len(opts) > 0 && opts[0].AllWorkspaces
	var out []*openshell.ServiceEndpoint
	for _, endpoint := range s.endpoints {
		if !everyWorkspace && endpoint.Workspace != workspace {
			continue
		}
		if sandbox != "" && endpoint.Sandbox != sandbox {
			continue
		}
		out = append(out, endpoint)
	}
	return out, nil
}

func (s fixedServices) Delete(_ context.Context, workspace, sandbox, service string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	*s.deleted = append(*s.deleted, workspace+"/"+sandbox+"/"+service)
	return &openshell.DeletionResult{Outcome: openshell.DeletionCompleted}, nil
}

// twoWorkspaces is a gateway holding a sandbox, a provider, a template and a
// service endpoint of the same name in each of two workspaces, behind the
// app's real router.
func twoWorkspaces(t *testing.T, authMiddleware auth.MiddlewareInterface) (http.Handler, *[]string) {
	t.Helper()
	ctx := context.Background()
	sdk := fake.NewClient()
	var endpoints []*openshell.ServiceEndpoint
	for _, workspace := range []string{"team-a", "team-b"} {
		if _, err := sdk.Workspaces().Create(ctx, workspace, nil); err != nil {
			t.Fatalf("create workspace %s: %v", workspace, err)
		}
		if _, err := sdk.Sandboxes().Create(ctx, workspace, "agent", &openshell.SandboxSpec{}, openshell.WithLabels(map[string]string{"team": workspace})); err != nil {
			t.Fatalf("create sandbox in %s: %v", workspace, err)
		}
		if _, err := sdk.Providers().Create(ctx, workspace, &openshell.Provider{Name: "claude", Type: "claude"}); err != nil {
			t.Fatalf("create provider in %s: %v", workspace, err)
		}
		template := &openshell.SandboxWorkloadTemplate{
			Name: "python",
			Spec: openshell.SandboxWorkloadTemplateSpec{Workload: &openshell.SandboxWorkloadConfig{Image: "example.com/python:1"}},
		}
		if _, err := sdk.SandboxTemplates().Create(ctx, workspace, template); err != nil {
			t.Fatalf("create template in %s: %v", workspace, err)
		}
		endpoints = append(endpoints,
			&openshell.ServiceEndpoint{Sandbox: "agent", Name: "web", TargetPort: 8080, Domain: true, Workspace: workspace},
			&openshell.ServiceEndpoint{Sandbox: "other", Name: "", TargetPort: 3000, Domain: true, Workspace: workspace},
		)
	}
	deleted := &[]string{}
	app := NewApp(withServices{ClientInterface: sdk, endpoints: endpoints, deleted: deleted}, nil, authMiddleware, "", models.AuthConfigResponse{})
	return app.Routes(), deleted
}

// listedWorkspaces GETs a list route and returns the workspace each item says
// it is in, sorted.
func listedWorkspaces(t *testing.T, router http.Handler, path string) []string {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d; body: %s", path, recorder.Code, recorder.Body.String())
	}
	var items []struct {
		Metadata struct {
			Workspace string `json:"workspace"`
		} `json:"metadata"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode GET %s: %v; body: %s", path, err, recorder.Body.String())
	}
	workspaces := make([]string, 0, len(items))
	for _, item := range items {
		workspaces = append(workspaces, item.Metadata.Workspace+item.Workspace)
	}
	sort.Strings(workspaces)
	return workspaces
}

// The top-level list routes cover every workspace, and each item says which
// one it lives in. The workspace-scoped routes beside them still stop at their
// own workspace, so the two are not the same list under two paths.
func TestAllWorkspacesRoutes(t *testing.T) {
	router, _ := twoWorkspaces(t, auth.New(auth.Config{Disabled: true}))

	tests := []struct {
		name string
		path string
		want []string
	}{
		{name: "sandboxes of every workspace", path: "/api/v1/sandboxes", want: []string{"team-a", "team-b"}},
		{name: "providers of every workspace", path: "/api/v1/providers", want: []string{"team-a", "team-b"}},
		{name: "templates of every workspace", path: "/api/v1/templates", want: []string{"team-a", "team-b"}},
		{name: "services of every workspace", path: "/api/v1/services", want: []string{"team-a", "team-a", "team-b", "team-b"}},
		{name: "sandboxes of one workspace", path: "/api/v1/workspaces/team-a/sandboxes", want: []string{"team-a"}},
		{name: "providers of one workspace", path: "/api/v1/workspaces/team-a/providers", want: []string{"team-a"}},
		{name: "templates of one workspace", path: "/api/v1/workspaces/team-a/templates", want: []string{"team-a"}},
		{name: "services of one workspace", path: "/api/v1/workspaces/team-b/services", want: []string{"team-b", "team-b"}},
		{name: "services of one sandbox", path: "/api/v1/workspaces/team-b/sandboxes/agent/services", want: []string{"team-b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := listedWorkspaces(t, router, tc.path); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("GET %s lists items in %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// The all-workspaces routes sit behind the same bearer check as the rest of
// the API. Who may list across workspaces is then the gateway's decision.
func TestAllWorkspacesRoutes_NeedABearer(t *testing.T) {
	router, _ := twoWorkspaces(t, auth.New(auth.Config{}))

	for _, path := range []string{"/api/v1/sandboxes", "/api/v1/providers", "/api/v1/templates", "/api/v1/services"} {
		anonymous := httptest.NewRecorder()
		router.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
		if anonymous.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401", path, anonymous.Code)
		}

		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("x-forwarded-access-token", "user-token")
		signedIn := httptest.NewRecorder()
		router.ServeHTTP(signedIn, request)
		if signedIn.Code != http.StatusOK {
			t.Errorf("GET %s with a token = %d, want 200; body: %s", path, signedIn.Code, signedIn.Body.String())
		}
	}
}

// A sandbox's unnamed service endpoint is deleted on the route without a
// service name, and a named one on the route with it.
func TestDeleteServiceRoutes(t *testing.T) {
	router, deleted := twoWorkspaces(t, auth.New(auth.Config{Disabled: true}))

	for _, path := range []string{
		"/api/v1/workspaces/team-a/sandboxes/agent/services/web",
		"/api/v1/workspaces/team-a/sandboxes/other/services",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("DELETE %s = %d; body: %s", path, recorder.Code, recorder.Body.String())
		}
	}
	if want := []string{"team-a/agent/web", "team-a/other/"}; !reflect.DeepEqual(*deleted, want) {
		t.Errorf("deleted %v, want %v", *deleted, want)
	}
}
