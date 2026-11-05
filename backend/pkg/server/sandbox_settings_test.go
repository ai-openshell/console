package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/fake"

	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
)

// recordingConfig is an SDK client whose config calls are recorded and
// answered here. The fake client answers them with Unimplemented.
type recordingConfig struct {
	openshell.ClientInterface
	calls *[]string
}

func (c recordingConfig) Config() openshell.ConfigInterface {
	return recordedConfig{c.calls}
}

type recordedConfig struct {
	calls *[]string
}

func (c recordedConfig) GetSandbox(_ context.Context, workspace, sandboxName string) (*openshell.SandboxConfig, error) {
	*c.calls = append(*c.calls, "get "+workspace+"/"+sandboxName)
	return &openshell.SandboxConfig{PolicySource: openshell.PolicySourceSandbox}, nil
}

func (c recordedConfig) GetGateway(context.Context) (*openshell.GatewayConfig, error) {
	*c.calls = append(*c.calls, "get gateway")
	return &openshell.GatewayConfig{}, nil
}

func (c recordedConfig) Update(_ context.Context, workspace string, update *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
	call := "set "
	if update.DeleteSetting {
		call = "delete "
	}
	if update.Global {
		call += "global "
	}
	*c.calls = append(*c.calls, call+workspace+"/"+update.Name+" "+update.SettingKey)
	return &openshell.ConfigUpdateResult{Deleted: update.DeleteSetting}, nil
}

// The sandbox-settings routes are registered on the real router with the
// workspace and the sandbox taken from the path. A route that is missing, or
// registered with a different parameter name, sends the gateway an empty name
// without any handler test noticing.
func TestSandboxSettingsRoutes(t *testing.T) {
	const path = "/api/v1/workspaces/team-a/sandboxes/agent-1/settings"
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCall string
		wantBody string
	}{
		{name: "read", method: http.MethodGet, path: path, wantCall: "get team-a/agent-1", wantBody: `"policySource":"SANDBOX"`},
		{
			name: "set", method: http.MethodPut, path: path, body: `{"key":"proposal_approval_mode","value":"auto"}`,
			wantCall: "set team-a/agent-1 proposal_approval_mode", wantBody: `"updated":true`,
		},
		{
			name: "delete", method: http.MethodDelete, path: path + "?key=proposal_approval_mode",
			wantCall: "delete team-a/agent-1 proposal_approval_mode", wantBody: `"deleted":true`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			sdk := recordingConfig{ClientInterface: fake.NewClient(), calls: &calls}
			app := NewApp(sdk, nil, auth.New(auth.Config{Disabled: true}), "", models.AuthConfigResponse{AuthDisabled: true})

			recorder := httptest.NewRecorder()
			app.Routes().ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s %s = %d; body: %s", tc.method, tc.path, recorder.Code, recorder.Body.String())
			}
			if len(calls) != 1 || calls[0] != tc.wantCall {
				t.Errorf("gateway calls = %v, want [%s]", calls, tc.wantCall)
			}
			if !json.Valid(recorder.Body.Bytes()) || !strings.Contains(recorder.Body.String(), tc.wantBody) {
				t.Errorf("body = %s, want JSON containing %s", recorder.Body.String(), tc.wantBody)
			}
		})
	}
}

// The sandbox-settings routes sit behind the same authentication as every
// other gateway call: without a bearer they answer 401 and call nothing.
func TestSandboxSettingsRoutesRequireAuth(t *testing.T) {
	var calls []string
	sdk := recordingConfig{ClientInterface: fake.NewClient(), calls: &calls}
	app := NewApp(sdk, nil, auth.New(auth.Config{}), "", models.AuthConfigResponse{})
	router := app.Routes()

	const path = "/api/v1/workspaces/team-a/sandboxes/agent-1/settings"
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, path+"?key=k", strings.NewReader(`{"key":"k","value":"v"}`)))
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s without a bearer = %d, want 401", method, recorder.Code)
		}
	}
	if len(calls) != 0 {
		t.Errorf("unauthenticated requests reached the gateway: %v", calls)
	}
}
