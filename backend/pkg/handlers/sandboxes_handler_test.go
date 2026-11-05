package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

func TestListSandboxes(t *testing.T) {
	tests := []struct {
		listFn     func(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Sandbox, error)
		name       string
		wantStatus int
	}{
		{
			name: "success returns sandbox list",
			listFn: func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				return []*openshell.Sandbox{
					{ID: "id-1", Name: "my-sandbox", Status: openshell.SandboxStatus{Phase: openshell.SandboxReady}},
				}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "empty list returns empty array",
			listFn: func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				return nil, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "gateway unavailable returns 502",
			listFn: func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "gateway down"}
			},
			wantStatus: http.StatusBadGateway,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.listFn = tc.listFn
			handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
			r := chi.NewRouter()
			r.Get("/workspaces/{workspace}/sandboxes", handler.ListSandboxes)

			req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestListSandboxesBody(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		return []*openshell.Sandbox{
			{
				ID: "id-1", Name: "my-sandbox", Workspace: "default",
				CreatedAt: time.Unix(1700000000, 0),
				Spec:      openshell.SandboxSpec{Template: &openshell.SandboxTemplate{Image: "ubuntu:latest"}},
				Status:    openshell.SandboxStatus{Phase: openshell.SandboxReady},
			},
		}, nil
	}
	handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes", handler.ListSandboxes)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("got %d sandboxes, want 1", len(body))
	}
	meta, ok := body[0]["metadata"].(map[string]any)
	if !ok {
		t.Fatal("metadata is not a map")
	}
	if meta["name"] != "my-sandbox" {
		t.Errorf("name = %v, want my-sandbox", meta["name"])
	}
	st, ok := body[0]["status"].(map[string]any)
	if !ok {
		t.Fatal("status is not a map")
	}
	if st["phase"] != "READY" {
		t.Errorf("phase = %v, want READY", st["phase"])
	}
}

func TestCreateSandbox(t *testing.T) {
	tests := []struct {
		createFn   func(ctx context.Context, workspace, name string, spec *openshell.SandboxSpec, opts ...openshell.CreateOption) (*openshell.Sandbox, error)
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{
			name: "success",
			body: `{"name":"my-sandbox","image":"ubuntu:latest","policy":{"version":1,"filesystem":{"includeWorkdir":true}}}`,
			createFn: func(_ context.Context, _, name string, _ *openshell.SandboxSpec, _ ...openshell.CreateOption) (*openshell.Sandbox, error) {
				return &openshell.Sandbox{ID: "id-1", Name: name, Status: openshell.SandboxStatus{Phase: openshell.SandboxProvisioning}}, nil
			},
			wantStatus: http.StatusCreated,
		},
		{
			// The gateway picks its default image; see
			// TestCreateSandboxWithoutImage for what it is sent.
			name:       "no image",
			body:       `{"name":"my-sandbox","policy":{"version":1}}`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "neither image nor policy",
			body:       `{"name":"my-sandbox"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_policy",
		},
		{
			name:       "missing policy",
			body:       `{"name":"my-sandbox","image":"ubuntu:latest"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_policy",
		},
		{
			name:       "invalid name - uppercase",
			body:       `{"name":"MyBadName","image":"ubuntu:latest","policy":{"version":1}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_name",
		},
		{
			name:       "invalid name - trailing dash",
			body:       `{"name":"bad-","image":"ubuntu:latest","policy":{"version":1}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_name",
		},
		{
			name:       "malformed JSON body",
			body:       `{invalid`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_body",
		},
		{
			name: "gateway already exists",
			body: `{"name":"dup","image":"ubuntu:latest","policy":{"version":1,"filesystem":{"includeWorkdir":true}}}`,
			createFn: func(_ context.Context, _, _ string, _ *openshell.SandboxSpec, _ ...openshell.CreateOption) (*openshell.Sandbox, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorAlreadyExists, Message: "sandbox already exists"}
			},
			wantStatus: http.StatusConflict,
			wantCode:   "already_exists",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.createFn = tc.createFn
			handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/sandboxes", handler.CreateSandbox)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var errResp map[string]any
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if errResp["code"] != tc.wantCode {
					t.Errorf("code = %q, want %q", errResp["code"], tc.wantCode)
				}
			}
		})
	}
}

// postSandbox sends a create-sandbox body through the route and returns the
// response together with what the handler passed to the SDK. The spec is nil
// when the gateway was never called.
func postSandbox(t *testing.T, body string, createErr error) (*httptest.ResponseRecorder, *openshell.SandboxSpec, []openshell.CreateOption) {
	t.Helper()
	var (
		gotSpec *openshell.SandboxSpec
		gotOpts []openshell.CreateOption
	)
	sdk := &mockSDK{}
	sdk.sandboxes.createFn = func(_ context.Context, _, name string, spec *openshell.SandboxSpec, opts ...openshell.CreateOption) (*openshell.Sandbox, error) {
		gotSpec, gotOpts = spec, opts
		if createErr != nil {
			return nil, createErr
		}
		return &openshell.Sandbox{
			Name:        name,
			Spec:        *spec,
			ServiceURLs: map[string]string{"": "https://sandbox.example.test"},
		}, nil
	}
	handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes", handler.CreateSandbox)

	req := httptest.NewRequest(http.MethodPost, "/workspaces/team-a/sandboxes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, gotSpec, gotOpts
}

// Everything the create form can send reaches the gateway in the field the
// gateway reads it from: the main command and its terminal on the spec, the
// runtime class and the driver config on the spec's inline template, and the
// labels, the annotations and the services to expose in the create options.
func TestCreateSandboxForwardsOptions(t *testing.T) {
	const body = `{
		"name": "agent-1",
		"image": "base",
		"policy": {"version": 1},
		"labels": {"team": "ml"},
		"annotations": {"owner": "ml-team"},
		"environment": {"MODE": "test"},
		"logLevel": "debug",
		"command": ["python", "-m", "http.server", "8080"],
		"tty": true,
		"runtimeClassName": "kata",
		"driverConfig": {"kubernetes": {"pod": {"node_selector": {"pool": "gpu"}}}},
		"serviceExposures": [{"targetPort": 8080}, {"service": "web", "targetPort": 3000}]
	}`
	w, spec, opts := postSandbox(t, body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if spec == nil {
		t.Fatal("the gateway was not called")
	}
	// The policy has tests of its own; everything else is compared as a whole,
	// so an option that lands in another field, or in none, is a failure.
	spec.Policy = nil
	wantSpec := &openshell.SandboxSpec{
		LogLevel:    "debug",
		Environment: map[string]string{"MODE": "test"},
		Command:     []string{"python", "-m", "http.server", "8080"},
		TTY:         true,
		Template: &openshell.SandboxTemplate{
			Image:            "base",
			RuntimeClassName: "kata",
			DriverConfig: map[string]any{
				"kubernetes": map[string]any{"pod": map[string]any{"node_selector": map[string]any{"pool": "gpu"}}},
			},
		},
	}
	if !reflect.DeepEqual(spec, wantSpec) {
		t.Errorf("spec = %+v with template %+v\nwant %+v with template %+v", spec, spec.Template, wantSpec, wantSpec.Template)
	}
	sent := readCreateOptions(opts)
	if want := map[string]string{"team": "ml"}; !reflect.DeepEqual(sent.labels, want) {
		t.Errorf("labels = %v, want %v", sent.labels, want)
	}
	if want := map[string]string{"owner": "ml-team"}; !reflect.DeepEqual(sent.annotations, want) {
		t.Errorf("annotations = %v, want %v", sent.annotations, want)
	}
	if want := []openshell.ServiceExposure{{TargetPort: 8080}, {Service: "web", TargetPort: 3000}}; !reflect.DeepEqual(sent.serviceExposures, want) {
		t.Errorf("service exposures = %+v, want %+v", sent.serviceExposures, want)
	}
}

// The URLs of the services exposed with a sandbox come back in the answer to
// the create, which is the only place the gateway reports them.
func TestCreateSandboxReturnsServiceURLs(t *testing.T) {
	w, _, _ := postSandbox(t,
		`{"image":"base","policy":{"version":1},"command":["sleep","infinity"],"serviceExposures":[{"targetPort":8080}]}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created struct {
		ServiceURLs map[string]string `json:"serviceUrls"`
		Spec        struct {
			Command []string `json:"command"`
		} `json:"spec"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := map[string]string{"": "https://sandbox.example.test"}; !reflect.DeepEqual(created.ServiceURLs, want) {
		t.Errorf("serviceUrls = %v, want %v", created.ServiceURLs, want)
	}
	if want := []string{"sleep", "infinity"}; !reflect.DeepEqual(created.Spec.Command, want) {
		t.Errorf("spec.command = %v, want %v", created.Spec.Command, want)
	}
}

// A create that sets none of the options calls the gateway with no create
// options, no command and an image-only template.
func TestCreateSandboxWithoutOptions(t *testing.T) {
	w, spec, opts := postSandbox(t, `{"image":"base","policy":{"version":1}}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if len(opts) != 0 {
		t.Errorf("create options = %+v, want none", opts)
	}
	if spec.Command != nil || spec.TTY || spec.Template.RuntimeClassName != "" || spec.Template.DriverConfig != nil {
		t.Errorf("spec = %+v with template %+v, want no command, tty, runtime class or driver config", spec, spec.Template)
	}
}

// A create that names no image is sent on with none, which is how the gateway
// is asked for its default image. The image the gateway chose is what the
// response then carries.
func TestCreateSandboxWithoutImage(t *testing.T) {
	const defaultImage = "registry.example.test/gateway-default:1"
	tests := []struct {
		name string
		body string
	}{
		{name: "no image field", body: `{"name":"agent-1","policy":{"version":1}}`},
		{name: "an empty image", body: `{"name":"agent-1","image":"","policy":{"version":1}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotSpec *openshell.SandboxSpec
			sdk := &mockSDK{}
			sdk.sandboxes.createFn = func(_ context.Context, _, name string, spec *openshell.SandboxSpec, _ ...openshell.CreateOption) (*openshell.Sandbox, error) {
				gotSpec = spec
				resolved := *spec
				resolved.Template = &openshell.SandboxTemplate{Image: defaultImage}
				return &openshell.Sandbox{Name: name, Spec: resolved}, nil
			}
			handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/sandboxes", handler.CreateSandbox)

			w := serve(t, r, http.MethodPost, "/workspaces/team-a/sandboxes", tc.body)
			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
			}
			if gotSpec == nil {
				t.Fatal("the gateway was not called")
			}
			if gotSpec.Template == nil || gotSpec.Template.Image != "" {
				t.Errorf("template sent to the gateway = %+v, want one with no image", gotSpec.Template)
			}
			if gotSpec.Policy == nil {
				t.Error("the policy did not reach the gateway")
			}
			var created models.Sandbox
			decodeInto(t, w, &created)
			if created.Spec.Image != defaultImage {
				t.Errorf("spec.image = %q, want the image the gateway chose, %q", created.Spec.Image, defaultImage)
			}
		})
	}
}

func TestCreateSandboxRefusesOptions(t *testing.T) {
	const prefix = `{"image":"base","policy":{"version":1},`
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "service port zero", body: prefix + `"serviceExposures":[{"service":"web","targetPort":0}]}`, wantCode: "invalid_port"},
		{name: "service port missing", body: prefix + `"serviceExposures":[{"service":"web"}]}`, wantCode: "invalid_port"},
		{name: "service port above 65535", body: prefix + `"serviceExposures":[{"targetPort":8080},{"targetPort":65536}]}`, wantCode: "invalid_port"},
		{name: "service port negative", body: prefix + `"serviceExposures":[{"targetPort":-1}]}`, wantCode: "invalid_body"},
		{name: "driver config that is not an object", body: prefix + `"driverConfig":["kubernetes"]}`, wantCode: "invalid_body"},
		{name: "command that is not a list", body: prefix + `"command":"sleep infinity"}`, wantCode: "invalid_body"},
		{name: "a field the BFF does not know", body: prefix + `"forward":"8080"}`, wantCode: "invalid_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, spec, _ := postSandbox(t, tc.body, nil)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body["code"] != tc.wantCode {
				t.Errorf("code = %v, want %q; message: %v", body["code"], tc.wantCode, body["message"])
			}
			if spec != nil {
				t.Error("a refused request still reached the gateway")
			}
		})
	}
}

// What the gateway refuses is passed on in its own words. A driver config is
// refused unless the gateway's administrator allowed it, and only the gateway
// knows whether that is so. This is gateway 0.1.2's refusal, a failed
// precondition, which the SDK reports as a conflict.
func TestCreateSandboxPassesGatewayRefusalOn(t *testing.T) {
	const refusal = "caller driver config is disabled; a gateway administrator must enable allow_driver_config"
	w, spec, _ := postSandbox(t,
		`{"image":"base","policy":{"version":1},"driverConfig":{"kubernetes":{"pod":{"priority":"high"}}}}`,
		&openshell.StatusError{Code: openshell.ErrorConflict, Message: refusal})
	if spec == nil {
		t.Fatal("the gateway was not asked")
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "conflict" || body["message"] != refusal {
		t.Errorf("error = %v, want code conflict with the gateway's own message %q", body, refusal)
	}
}

// The sandbox page reads the resources a sandbox was created with, what it
// runs and what the gateway last observed from this one response.
func TestGetSandboxBody(t *testing.T) {
	gpus := uint32(1)
	sdk := &mockSDK{}
	sdk.sandboxes.getFn = func(_ context.Context, workspace, name string) (*openshell.Sandbox, error) {
		return &openshell.Sandbox{
			Name:      name,
			Workspace: workspace,
			CreatedFromWorkloadTemplate: &openshell.SandboxWorkloadTemplateProvenance{
				Name: "claude-harness", ResourceVersion: "4",
			},
			Spec: openshell.SandboxSpec{
				GPUCount: &gpus,
				Command:  []string{"sleep", "infinity"},
				Template: &openshell.SandboxTemplate{
					Image:     "base",
					Resources: map[string]any{"limits": map[string]any{"cpu": "500m", "memory": "512Mi"}},
				},
			},
			Status: openshell.SandboxStatus{
				Phase:     openshell.SandboxReady,
				AgentFd:   "agent-fd-must-not-leak",
				SandboxFd: "sandbox-fd-must-not-leak",
				EndpointStatuses: []openshell.EndpointStatus{
					{EndpointID: "ep-1", Host: "mcp.example.test", Ports: []uint32{443}, LastResult: openshell.EndpointHTTPResponseReceived},
				},
			},
		}, nil
	}
	handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}", handler.GetSandbox)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces/team-a/sandboxes/agent-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	// The whole document: what is not listed here must not be in the answer,
	// the sandbox's own service endpoints (agentFd, sandboxFd) least of all.
	const want = `{
		"createdFromWorkloadTemplate": {"name": "claude-harness", "resourceVersion": "4"},
		"spec": {
			"image": "base",
			"template": {"resources": {"limits": {"cpu": "500m", "memory": "512Mi"}}},
			"gpu": true,
			"gpuCount": 1,
			"command": ["sleep", "infinity"]
		},
		"status": {
			"sandboxName": "agent-1",
			"phase": "READY",
			"currentPolicyVersion": 0,
			"endpointStatuses": [
				{"endpointId": "ep-1", "host": "mcp.example.test", "ports": [443], "lastResult": "HTTP_RESPONSE_RECEIVED"}
			]
		},
		"metadata": {"id": "", "name": "agent-1", "workspace": "team-a", "createdAtMs": 0, "resourceVersion": 0}
	}`
	var got, wanted any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if err := json.Unmarshal([]byte(want), &wanted); err != nil {
		t.Fatalf("decode the expected document: %v", err)
	}
	if !reflect.DeepEqual(got, wanted) {
		t.Errorf("sandbox JSON differs.\n got: %s\nwant: %s", w.Body.String(), want)
	}
}

func TestGetSandbox(t *testing.T) {
	tests := []struct {
		getFn      func(ctx context.Context, workspace, name string) (*openshell.Sandbox, error)
		name       string
		wantStatus int
	}{
		{
			name: "success",
			getFn: func(_ context.Context, _, name string) (*openshell.Sandbox, error) {
				return &openshell.Sandbox{ID: "id-1", Name: name, Status: openshell.SandboxStatus{Phase: openshell.SandboxReady}}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "not found",
			getFn: func(_ context.Context, _, _ string) (*openshell.Sandbox, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
			},
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.getFn = tc.getFn
			handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
			r := chi.NewRouter()
			r.Get("/workspaces/{workspace}/sandboxes/{name}", handler.GetSandbox)

			req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/my-sandbox", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}

func TestStopSandbox(t *testing.T) {
	tests := []struct {
		stopFn     func(ctx context.Context, workspace, name string) (*openshell.Sandbox, error)
		name       string
		wantStatus int
	}{
		{
			name: "success",
			stopFn: func(_ context.Context, _, name string) (*openshell.Sandbox, error) {
				return &openshell.Sandbox{ID: "id-1", Name: name, Status: openshell.SandboxStatus{Phase: openshell.SandboxStopping}}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "not found",
			stopFn: func(_ context.Context, _, _ string) (*openshell.Sandbox, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "already stopped conflicts",
			stopFn: func(_ context.Context, _, _ string) (*openshell.Sandbox, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorConflict, Message: "sandbox is not running"}
			},
			wantStatus: http.StatusConflict,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.stopFn = tc.stopFn
			handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/sandboxes/{name}/stop", handler.StopSandbox)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes/my-sandbox/stop", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestStartSandbox(t *testing.T) {
	tests := []struct {
		startFn    func(ctx context.Context, workspace, name string) (*openshell.Sandbox, error)
		name       string
		wantStatus int
	}{
		{
			name: "success",
			startFn: func(_ context.Context, _, name string) (*openshell.Sandbox, error) {
				return &openshell.Sandbox{ID: "id-1", Name: name, Status: openshell.SandboxStatus{Phase: openshell.SandboxStarting}}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "not found",
			startFn: func(_ context.Context, _, _ string) (*openshell.Sandbox, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
			},
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.startFn = tc.startFn
			handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/sandboxes/{name}/start", handler.StartSandbox)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes/my-sandbox/start", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestDeleteSandbox(t *testing.T) {
	tests := []struct {
		deleteFn   func(ctx context.Context, workspace, name string) error
		name       string
		wantStatus int
	}{
		{
			name: "success",
			deleteFn: func(_ context.Context, _, _ string) error {
				return nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "not found",
			deleteFn: func(_ context.Context, _, _ string) error {
				return &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "permission denied",
			deleteFn: func(_ context.Context, _, _ string) error {
				return &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "access denied"}
			},
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.deleteFn = tc.deleteFn
			handler := NewSandboxHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
			r := chi.NewRouter()
			r.Delete("/workspaces/{workspace}/sandboxes/{name}", handler.DeleteSandbox)

			req := httptest.NewRequest(http.MethodDelete, "/workspaces/default/sandboxes/my-sandbox", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}
