package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/ai-openshell/console/backend/pkg/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// listCall is what one all-workspaces list asked the SDK for.
type listCall struct {
	workspace string
	sandbox   string
	opts      []openshell.ListOptions
}

// allWorkspacesFixture is an AllWorkspacesHandler over a mock SDK, on the
// routes the app registers it under. Each list answers with one item in
// team-a and one in team-b, or with err when it is set, and records what it
// was asked.
type allWorkspacesFixture struct {
	err    error
	router http.Handler
	sdk    *mockSDK
	calls  map[string]listCall
	empty  bool
}

func newAllWorkspacesFixture(keys services.ProviderCredentialKeyReader) *allWorkspacesFixture {
	f := &allWorkspacesFixture{sdk: &mockSDK{}, calls: map[string]listCall{}}

	f.sdk.sandboxes.listFn = func(_ context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		f.calls["sandboxes"] = listCall{workspace: workspace, opts: opts}
		if f.err != nil || f.empty {
			return nil, f.err
		}
		return []*openshell.Sandbox{
			{Name: "agent", Workspace: "team-a", Status: openshell.SandboxStatus{Phase: openshell.SandboxReady}},
			{Name: "agent", Workspace: "team-b", Status: openshell.SandboxStatus{Phase: openshell.SandboxStopped}},
		}, nil
	}
	f.sdk.providers.listFn = func(_ context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Provider, error) {
		f.calls["providers"] = listCall{workspace: workspace, opts: opts}
		if f.err != nil || f.empty {
			return nil, f.err
		}
		return []*openshell.Provider{
			{Name: "claude", Workspace: "team-a", Type: "claude", Spec: openshell.ProviderSpec{
				Credentials: map[string]string{"ANTHROPIC_API_KEY": "sk-must-not-leak"},
			}},
			{Name: "claude", Workspace: "team-b", Type: "claude"},
		}, nil
	}
	f.sdk.templates.listFn = func(_ context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.SandboxWorkloadTemplate, error) {
		f.calls["templates"] = listCall{workspace: workspace, opts: opts}
		if f.err != nil || f.empty {
			return nil, f.err
		}
		return []*openshell.SandboxWorkloadTemplate{
			{Name: "python", Workspace: "team-a"},
			{Name: "python", Workspace: "team-b"},
		}, nil
	}
	f.sdk.services.listFn = func(_ context.Context, workspace, sandbox string, opts ...openshell.ListOptions) ([]*openshell.ServiceEndpoint, error) {
		f.calls["services"] = listCall{workspace: workspace, sandbox: sandbox, opts: opts}
		if f.err != nil || f.empty {
			return nil, f.err
		}
		return []*openshell.ServiceEndpoint{
			{ID: "ep-1", SandboxID: "sb-1", Sandbox: "agent", Name: "web", TargetPort: 8080, Domain: true, URL: "https://team-a--agent--web.example/", Workspace: "team-a"},
			{ID: "ep-2", SandboxID: "sb-2", Sandbox: "agent", Name: "", TargetPort: 3000, Domain: true, Workspace: "team-b"},
		}, nil
	}

	handler := NewAllWorkspacesHandler(
		services.NewSandboxService(f.sdk.Sandboxes(), nil),
		services.NewProviderService(f.sdk.Providers()),
		services.NewTemplateService(f.sdk),
		services.NewServiceService(f.sdk.Services()),
	)
	if keys != nil {
		handler.SetCredentialKeyReader(keys)
	}
	r := chi.NewRouter()
	r.Get("/sandboxes", handler.ListSandboxes)
	r.Get("/providers", handler.ListProviders)
	r.Get("/templates", handler.ListSandboxTemplates)
	r.Get("/services", handler.ListServices)
	f.router = r
	return f
}

func (f *allWorkspacesFixture) get(path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// workspaceOf reads the workspace an item of an all-workspaces list says it
// lives in: metadata.workspace for a resource with object metadata, the
// top-level workspace for a service endpoint.
func workspaceOf(item map[string]any) string {
	if metadata, ok := item["metadata"].(map[string]any); ok {
		workspace, _ := metadata["workspace"].(string)
		return workspace
	}
	workspace, _ := item["workspace"].(string)
	return workspace
}

// Every list asks for all workspaces, and every item says which one it is in.
// The two items of each list share a name, so the workspace is all that tells
// them apart.
func TestAllWorkspacesLists(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		list         string
		wantSelector string
	}{
		{name: "sandboxes", path: "/sandboxes", list: "sandboxes"},
		{name: "sandboxes by label", path: "/sandboxes?labelSelector=team%3Dml%2Ctier%3Dgpu", list: "sandboxes", wantSelector: "team=ml,tier=gpu"},
		{name: "providers", path: "/providers", list: "providers"},
		// ListProviders has no label selector, so the route takes none.
		{name: "providers ignore a label selector", path: "/providers?labelSelector=team%3Dml", list: "providers"},
		{name: "templates", path: "/templates", list: "templates"},
		{name: "templates by label", path: "/templates?labelSelector=team%3Dml", list: "templates", wantSelector: "team=ml"},
		{name: "services", path: "/services", list: "services"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAllWorkspacesFixture(nil)
			w := f.get(tc.path)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}

			call, called := f.calls[tc.list]
			if !called {
				t.Fatalf("the %s list was never asked; calls: %v", tc.list, f.calls)
			}
			if len(call.opts) != 1 || !call.opts[0].AllWorkspaces {
				t.Fatalf("list options = %+v, want exactly one with AllWorkspaces set", call.opts)
			}
			if call.workspace != "" || call.sandbox != "" {
				t.Errorf("asked for workspace %q sandbox %q, want neither: the scope is every workspace", call.workspace, call.sandbox)
			}
			if got := call.opts[0].LabelSelector; got != tc.wantSelector {
				t.Errorf("label selector = %q, want %q", got, tc.wantSelector)
			}

			var items []map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
				t.Fatalf("decode: %v; body: %s", err, w.Body.String())
			}
			var workspaces []string
			for _, item := range items {
				workspaces = append(workspaces, workspaceOf(item))
			}
			if want := []string{"team-a", "team-b"}; !reflect.DeepEqual(workspaces, want) {
				t.Errorf("workspaces of the listed items = %v, want %v; body: %s", workspaces, want, w.Body.String())
			}
		})
	}
}

// The gateway decides who may list across workspaces. Its refusal reaches the
// caller as it is, and so does any other failure of the call.
func TestAllWorkspacesListsRelayGatewayErrors(t *testing.T) {
	errorsByName := []struct {
		err         error
		name        string
		wantCode    string
		wantMessage string
		wantStatus  int
	}{
		{
			name:        "not a platform admin",
			err:         &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "role 'openshell-admin' required"},
			wantStatus:  http.StatusForbidden,
			wantCode:    "permission_denied",
			wantMessage: "role 'openshell-admin' required",
		},
		{
			name:        "malformed label selector",
			err:         &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: "label selector must be key=value pairs"},
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_argument",
			wantMessage: "label selector must be key=value pairs",
		},
		{
			name:        "gateway unreachable",
			err:         &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "connection refused"},
			wantStatus:  http.StatusBadGateway,
			wantCode:    "gateway_unavailable",
			wantMessage: "OpenShell gateway is unreachable",
		},
	}
	for _, path := range []string{"/sandboxes", "/providers", "/templates", "/services"} {
		for _, tc := range errorsByName {
			t.Run(strings.TrimPrefix(path, "/")+" "+tc.name, func(t *testing.T) {
				f := newAllWorkspacesFixture(nil)
				f.err = tc.err
				w := f.get(path)
				if w.Code != tc.wantStatus {
					t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
				}
				var body map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode: %v; body: %s", err, w.Body.String())
				}
				if body["code"] != tc.wantCode || body["message"] != tc.wantMessage {
					t.Errorf("error = %v, want code %q message %q", body, tc.wantCode, tc.wantMessage)
				}
			})
		}
	}
}

// A gateway with nothing to list answers with an empty JSON array, which is
// what the frontend iterates over, and never with null.
func TestAllWorkspacesListsEmpty(t *testing.T) {
	for _, path := range []string{"/sandboxes", "/providers", "/templates", "/services"} {
		t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
			f := newAllWorkspacesFixture(nil)
			f.empty = true
			w := f.get(path)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if got := strings.TrimSpace(w.Body.String()); got != "[]" {
				t.Errorf("body = %s, want []", got)
			}
		})
	}
}

// A service endpoint carries its workspace and both ids, and the unnamed
// endpoint is listed with an empty service name.
func TestAllWorkspacesServicesShape(t *testing.T) {
	f := newAllWorkspacesFixture(nil)
	w := f.get("/services")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var endpoints []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &endpoints); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []map[string]any{
		{
			"id": "ep-1", "workspace": "team-a", "sandboxId": "sb-1", "sandboxName": "agent", "serviceName": "web",
			"url": "https://team-a--agent--web.example/", "targetPort": float64(8080), "domain": true,
		},
		{
			"id": "ep-2", "workspace": "team-b", "sandboxId": "sb-2", "sandboxName": "agent", "serviceName": "",
			"targetPort": float64(3000), "domain": true,
		},
	}
	if !reflect.DeepEqual(endpoints, want) {
		t.Errorf("endpoints = %v\nwant %v", endpoints, want)
	}
}

// The gateway refuses a sandbox filter across workspaces. The filter is
// forwarded so that the refusal is the gateway's, and a caller who sends it
// is never handed an unfiltered list instead.
func TestAllWorkspacesServicesForwardsSandboxFilter(t *testing.T) {
	f := newAllWorkspacesFixture(nil)
	f.err = &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: "all_workspaces is not supported by this request"}
	w := f.get("/services?sandbox=agent")

	if got := f.calls["services"].sandbox; got != "agent" {
		t.Errorf("sandbox filter the SDK was given = %q, want agent", got)
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "all_workspaces is not supported by this request") {
		t.Errorf("body = %s, want the gateway's own message", w.Body.String())
	}
}

// workspaceOnlyKeys is a credential key reader that reads one workspace at a
// time and cannot list across them.
type workspaceOnlyKeys struct {
	calls []string
}

func (k *workspaceOnlyKeys) ProviderCredentialKeys(_ context.Context, workspace, name string) ([]string, error) {
	k.calls = append(k.calls, "get "+workspace+"/"+name)
	return []string{"FROM_ONE_WORKSPACE"}, nil
}

func (k *workspaceOnlyKeys) ListProviderCredentialKeys(_ context.Context, workspace string) (map[string][]string, error) {
	k.calls = append(k.calls, "list "+workspace)
	return map[string][]string{"claude": {"FROM_ONE_WORKSPACE"}}, nil
}

func (k *workspaceOnlyKeys) ListSandboxProviderCredentialKeys(_ context.Context, workspace, sandboxName string) (map[string][]string, error) {
	k.calls = append(k.calls, "sandbox "+workspace+"/"+sandboxName)
	return map[string][]string{"claude": {"FROM_ONE_WORKSPACE"}}, nil
}

// allWorkspacesKeys is a credential key reader that can also list across
// workspaces, as clients.RawExecClient can.
type allWorkspacesKeys struct {
	err     error
	byScope map[string]map[string][]string
	workspaceOnlyKeys
	asked int
}

func (k *allWorkspacesKeys) ListProviderCredentialKeysAllWorkspaces(context.Context) (map[string]map[string][]string, error) {
	k.asked++
	return k.byScope, k.err
}

func credentialNamesByWorkspace(t *testing.T, body []byte) map[string][]string {
	t.Helper()
	var providers []struct {
		Metadata struct {
			Workspace string `json:"workspace"`
		} `json:"metadata"`
		CredentialNames []string `json:"credentialNames"`
	}
	if err := json.Unmarshal(body, &providers); err != nil {
		t.Fatalf("decode: %v; body: %s", err, body)
	}
	names := map[string][]string{}
	for _, provider := range providers {
		names[provider.Metadata.Workspace] = provider.CredentialNames
	}
	return names
}

// Providers in two workspaces may share a name, so the credential keys are
// matched on workspace and name together. Only the keys are returned.
func TestAllWorkspacesProvidersCredentialNames(t *testing.T) {
	keys := &allWorkspacesKeys{byScope: map[string]map[string][]string{
		"team-a": {"claude": {"ANTHROPIC_API_KEY"}},
		"team-b": {"claude": {"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}},
	}}
	f := newAllWorkspacesFixture(keys)
	w := f.get("/providers")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}

	want := map[string][]string{
		"team-a": {"ANTHROPIC_API_KEY"},
		"team-b": {"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"},
	}
	if got := credentialNamesByWorkspace(t, w.Body.Bytes()); !reflect.DeepEqual(got, want) {
		t.Errorf("credential names by workspace = %v, want %v", got, want)
	}
	if keys.asked != 1 {
		t.Errorf("the key reader was asked %d times, want once for the whole list", keys.asked)
	}
	if strings.Contains(w.Body.String(), "sk-must-not-leak") {
		t.Errorf("response leaked a credential value: %s", w.Body.String())
	}
}

// A reader that cannot list across workspaces is not asked workspace by
// workspace: the providers keep what the SDK carries.
func TestAllWorkspacesProvidersWithoutAnAllWorkspacesKeyReader(t *testing.T) {
	keys := &workspaceOnlyKeys{}
	f := newAllWorkspacesFixture(keys)
	w := f.get("/providers")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	// team-a's provider holds a credential the SDK does carry; team-b's none.
	want := map[string][]string{"team-a": {"ANTHROPIC_API_KEY"}, "team-b": nil}
	if got := credentialNamesByWorkspace(t, w.Body.Bytes()); !reflect.DeepEqual(got, want) {
		t.Errorf("credential names by workspace = %v, want %v", got, want)
	}
	if len(keys.calls) != 0 {
		t.Errorf("the single-workspace reader was asked %v, want nothing", keys.calls)
	}
}

// The key read is a call to the gateway like the list itself: when it fails,
// its error is the answer, not a list with the names quietly missing.
func TestAllWorkspacesProvidersKeyReadFails(t *testing.T) {
	tests := []struct {
		err        error
		name       string
		wantCode   string
		wantStatus int
	}{
		{name: "refused", err: status.Error(codes.PermissionDenied, "role 'openshell-admin' required"), wantStatus: http.StatusForbidden, wantCode: "permission_denied"},
		{name: "gateway unreachable", err: status.Error(codes.Unavailable, "connection refused"), wantStatus: http.StatusBadGateway, wantCode: "gateway_unavailable"},
		{name: "not a gRPC status", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAllWorkspacesFixture(&allWorkspacesKeys{err: tc.err})
			w := f.get("/providers")
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v; body: %s", err, w.Body.String())
			}
			if body["code"] != tc.wantCode {
				t.Errorf("code = %q, want %q", body["code"], tc.wantCode)
			}
		})
	}
}
