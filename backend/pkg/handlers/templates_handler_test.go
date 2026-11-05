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

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/services"
)

func TestListSandboxTemplates(t *testing.T) {
	gpu := uint32(1)
	sdk := &mockSDK{}
	sdk.templates.listFn = func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.SandboxWorkloadTemplate, error) {
		return []*openshell.SandboxWorkloadTemplate{
			{
				ID: "id-1", Name: "gpu-kata", Workspace: "default",
				CreatedAt: time.Unix(1700000000, 0),
				Spec: openshell.SandboxWorkloadTemplateSpec{
					Workload: &openshell.SandboxWorkloadConfig{
						Image:       "nvcr.io/nvidia/openshell:latest",
						Environment: map[string]string{"FOO": "bar"},
						Resources: &openshell.SandboxResources{
							CPU: "2", Memory: "8Gi",
							GPU: &openshell.SandboxGPURequirements{Count: &gpu},
						},
					},
				},
			},
		}, nil
	}
	handler := NewTemplatesHandler(services.NewTemplateService(sdk))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/templates", handler.ListSandboxTemplates)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/templates", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len = %d, want 1", len(body))
	}
	spec, _ := body[0]["spec"].(map[string]any)
	workload, _ := spec["workload"].(map[string]any)
	if workload["image"] != "nvcr.io/nvidia/openshell:latest" {
		t.Errorf("image = %v, want nvcr.io/nvidia/openshell:latest", workload["image"])
	}
}

// The BFF checks the template's name and nothing about its workload. The
// gateway here answers as 0.1.2 does: it takes a workload without an image,
// which stands for its default image, and refuses a template without a
// workload.
func TestCreateSandboxTemplate(t *testing.T) {
	// sent is what reached the gateway of one request.
	type sent struct {
		image       string
		called      bool
		hasWorkload bool
	}
	tests := []struct {
		name       string
		body       string
		wantCode   apiutils.ResponseCode
		want       sent
		wantStatus int
	}{
		{
			name:       "success",
			body:       `{"name":"claude-harness","spec":{"workload":{"image":"ghcr.io/nvidia/openshell-community/sandboxes/base:latest"}}}`,
			want:       sent{called: true, hasWorkload: true, image: "ghcr.io/nvidia/openshell-community/sandboxes/base:latest"},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "invalid name",
			body:       `{"name":"Bad_Name","spec":{"workload":{"image":"base"}}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   apiutils.InvalidName,
		},
		{
			name:       "a workload without an image is the gateway's default image",
			body:       `{"name":"no-image","spec":{"workload":{}}}`,
			want:       sent{called: true, hasWorkload: true},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "an image-less workload keeps the rest of the workload",
			body:       `{"name":"no-image","spec":{"workload":{"environment":{"MODE":"test"},"resources":{"cpu":"1"}}}}`,
			want:       sent{called: true, hasWorkload: true},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "no workload: the gateway's refusal is passed on",
			body:       `{"name":"no-workload","spec":{}}`,
			want:       sent{called: true},
			wantStatus: http.StatusBadRequest,
			wantCode:   apiutils.InvalidArgument,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			var got sent
			sdk.templates.createFn = func(_ context.Context, _ string, template *openshell.SandboxWorkloadTemplate) (*openshell.SandboxWorkloadTemplate, error) {
				got.called = true
				if template.Spec.Workload == nil {
					return nil, &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: "sandbox template workload is required"}
				}
				got.hasWorkload, got.image = true, template.Spec.Workload.Image
				return template, nil
			}
			handler := NewTemplatesHandler(services.NewTemplateService(sdk))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/templates", handler.CreateSandboxTemplate)

			w := serve(t, r, http.MethodPost, "/workspaces/default/templates", tc.body)
			if tc.wantCode != "" {
				wantErrorResponse(t, w, tc.wantStatus, tc.wantCode)
			} else if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if got != tc.want {
				t.Errorf("sent to the gateway = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCreateSandboxTemplatePassesWorkload(t *testing.T) {
	sdk := &mockSDK{}
	var gotImage string
	sdk.templates.createFn = func(_ context.Context, _ string, template *openshell.SandboxWorkloadTemplate) (*openshell.SandboxWorkloadTemplate, error) {
		if template.Spec.Workload != nil {
			gotImage = template.Spec.Workload.Image
		}
		return template, nil
	}
	handler := NewTemplatesHandler(services.NewTemplateService(sdk))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/templates", handler.CreateSandboxTemplate)

	body := `{"name":"codex-harness","spec":{"workload":{"image":"base","environment":{"HARNESS":"codex"},"resources":{"cpu":"1","memory":"2Gi"}}}}`
	req := httptest.NewRequest(http.MethodPost, "/workspaces/default/templates", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if gotImage != "base" {
		t.Errorf("template image = %q, want base", gotImage)
	}
}

func TestDeleteSandboxTemplate(t *testing.T) {
	sdk := &mockSDK{}
	sdk.templates.deleteFn = func(_ context.Context, _, name string) (bool, error) {
		if name != "gpu-kata" {
			t.Errorf("name = %q, want gpu-kata", name)
		}
		return true, nil
	}
	handler := NewTemplatesHandler(services.NewTemplateService(sdk))
	r := chi.NewRouter()
	r.Delete("/workspaces/{workspace}/templates/{name}", handler.DeleteSandboxTemplate)

	req := httptest.NewRequest(http.MethodDelete, "/workspaces/default/templates/gpu-kata", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateSandboxFromTemplate(t *testing.T) {
	const policy = `"policy":{"version":1,"filesystem":{"includeWorkdir":true}}`
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "success",
			body:       `{"name":"agent-1","templateName":"claude-harness","providers":["claude"],` + policy + `}`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "missing template",
			body:       `{"name":"agent-1",` + policy + `}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing policy",
			body:       `{"name":"agent-1","templateName":"claude-harness"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid name",
			body:       `{"name":"Bad_Name","templateName":"claude-harness",` + policy + `}`,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			var gotTemplate string
			sdk.templates.createFromTemplateFn = func(_ context.Context, _, name, templateName string, _ *openshell.SandboxSpec, _ ...openshell.CreateOption) (*openshell.Sandbox, error) {
				gotTemplate = templateName
				return &openshell.Sandbox{Name: name}, nil
			}
			handler := NewTemplatesHandler(services.NewTemplateService(sdk))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/sandboxes/from-template", handler.CreateSandboxFromTemplate)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes/from-template", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus == http.StatusCreated && gotTemplate != "claude-harness" {
				t.Errorf("templateName = %q, want claude-harness", gotTemplate)
			}
		})
	}
}

// The create-template form's startup service level, annotations and driver
// config reach the gateway on the template, and come back on the answer the
// Templates tab renders.
func TestCreateSandboxTemplatePassesEverything(t *testing.T) {
	sdk := &mockSDK{}
	var got *openshell.SandboxWorkloadTemplate
	sdk.templates.createFn = func(_ context.Context, _ string, template *openshell.SandboxWorkloadTemplate) (*openshell.SandboxWorkloadTemplate, error) {
		got = template
		return template, nil
	}
	handler := NewTemplatesHandler(services.NewTemplateService(sdk))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/templates", handler.CreateSandboxTemplate)

	const body = `{
		"name": "claude-harness",
		"labels": {"team": "ml"},
		"annotations": {"owner": "ml-team"},
		"spec": {
			"workload": {"image": "base"},
			"driverConfig": {"kubernetes": {"pod": {"node_selector": {"pool": "gpu"}}}},
			"desiredServiceLevel": {"startup": {"readyWithinMs": 30000, "maxBurst": 4}}
		}
	}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/workspaces/default/templates", strings.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if got == nil {
		t.Fatal("the gateway was not called")
	}
	if got.Labels["team"] != "ml" || got.Annotations["owner"] != "ml-team" {
		t.Errorf("labels = %v, annotations = %v; want team=ml and owner=ml-team", got.Labels, got.Annotations)
	}
	wantDriverConfig := map[string]any{"kubernetes": map[string]any{"pod": map[string]any{"node_selector": map[string]any{"pool": "gpu"}}}}
	if !reflect.DeepEqual(got.Spec.DriverConfig, wantDriverConfig) {
		t.Errorf("driverConfig = %v, want %v", got.Spec.DriverConfig, wantDriverConfig)
	}
	level := got.Spec.DesiredServiceLevel
	if level == nil || level.Startup == nil || level.Startup.ReadyWithin != 30*time.Second || level.Startup.MaxBurst != 4 {
		t.Errorf("desiredServiceLevel = %+v, want a 30s startup with a burst of 4", level)
	}

	var created struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			DriverConfig        map[string]any `json:"driverConfig"`
			DesiredServiceLevel struct {
				Startup struct {
					ReadyWithinMs int64  `json:"readyWithinMs"`
					MaxBurst      uint32 `json:"maxBurst"`
				} `json:"startup"`
			} `json:"desiredServiceLevel"`
		} `json:"spec"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Metadata.Annotations["owner"] != "ml-team" {
		t.Errorf("returned annotations = %v, want owner=ml-team", created.Metadata.Annotations)
	}
	if !reflect.DeepEqual(created.Spec.DriverConfig, wantDriverConfig) {
		t.Errorf("returned driverConfig = %v, want %v", created.Spec.DriverConfig, wantDriverConfig)
	}
	if startup := created.Spec.DesiredServiceLevel.Startup; startup.ReadyWithinMs != 30000 || startup.MaxBurst != 4 {
		t.Errorf("returned startup = %+v, want 30000 ms and a burst of 4", startup)
	}
}

// postFromTemplate sends a create-from-template body through the route and
// returns the response with what the handler passed to the SDK. The spec is
// nil when the gateway was never called.
func postFromTemplate(t *testing.T, body string) (*httptest.ResponseRecorder, *openshell.SandboxSpec, []openshell.CreateOption) {
	t.Helper()
	var (
		gotSpec *openshell.SandboxSpec
		gotOpts []openshell.CreateOption
	)
	sdk := &mockSDK{}
	sdk.templates.createFromTemplateFn = func(_ context.Context, _, name, _ string, spec *openshell.SandboxSpec, opts ...openshell.CreateOption) (*openshell.Sandbox, error) {
		gotSpec, gotOpts = spec, opts
		return &openshell.Sandbox{Name: name}, nil
	}
	handler := NewTemplatesHandler(services.NewTemplateService(sdk))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/from-template", handler.CreateSandboxFromTemplate)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/workspaces/team-a/sandboxes/from-template", strings.NewReader(body)))
	return w, gotSpec, gotOpts
}

// A sandbox made from a template takes its labels and annotations, its main
// command and the services to expose from the request, because a template
// holds none of them, and still nothing of the workload.
func TestCreateSandboxFromTemplateForwardsOptions(t *testing.T) {
	const body = `{
		"name": "agent-1",
		"templateName": "claude-harness",
		"policy": {"version": 1},
		"providers": ["claude"],
		"labels": {"team": "ml"},
		"annotations": {"owner": "ml-team"},
		"command": ["claude", "--print"],
		"tty": true,
		"serviceExposures": [{"targetPort": 8080}]
	}`
	w, spec, opts := postFromTemplate(t, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if spec == nil {
		t.Fatal("the gateway was not called")
	}
	if !reflect.DeepEqual(spec.Command, []string{"claude", "--print"}) || !spec.TTY {
		t.Errorf("command = %v, tty = %v; want [claude --print] with a tty", spec.Command, spec.TTY)
	}
	if !reflect.DeepEqual(spec.Providers, []string{"claude"}) {
		t.Errorf("providers = %v, want [claude]", spec.Providers)
	}
	// The SDK refuses a create from a template whose spec carries any of these.
	if spec.Template != nil || spec.LogLevel != "" || len(spec.Environment) != 0 || spec.GPU || spec.GPUCount != nil {
		t.Errorf("spec carries workload fields beside a template: %+v", spec)
	}
	sent := readCreateOptions(opts)
	if sent.labels["team"] != "ml" {
		t.Errorf("labels = %v, want team=ml", sent.labels)
	}
	if sent.annotations["owner"] != "ml-team" {
		t.Errorf("annotations = %v, want owner=ml-team", sent.annotations)
	}
	if want := []openshell.ServiceExposure{{TargetPort: 8080}}; !reflect.DeepEqual(sent.serviceExposures, want) {
		t.Errorf("service exposures = %+v, want %+v", sent.serviceExposures, want)
	}
}

func TestCreateSandboxFromTemplateRefusesOptions(t *testing.T) {
	const prefix = `{"templateName":"claude-harness","policy":{"version":1},`
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "service port zero", body: prefix + `"serviceExposures":[{"targetPort":0}]}`, wantCode: "invalid_port"},
		{name: "service port above 65535", body: prefix + `"serviceExposures":[{"targetPort":70000}]}`, wantCode: "invalid_port"},
		// The workload is the template's: the request has no field for it.
		{name: "an image beside a template", body: prefix + `"image":"base"}`, wantCode: "invalid_body"},
		{name: "environment beside a template", body: prefix + `"environment":{"A":"b"}}`, wantCode: "invalid_body"},
		{name: "a driver config beside a template", body: prefix + `"driverConfig":{"kubernetes":{}}}`, wantCode: "invalid_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, spec, _ := postFromTemplate(t, tc.body)
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
