package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

// sandboxSettingsRouter serves the three sandbox-settings routes the way
// server.App registers them.
func sandboxSettingsRouter(sdk *mockSDK) http.Handler {
	handler := NewSettingsHandler(sdk.Config())
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/settings", handler.GetSandboxSettings)
	r.Put("/workspaces/{workspace}/sandboxes/{name}/settings", handler.SetSandboxSetting)
	r.Delete("/workspaces/{workspace}/sandboxes/{name}/settings", handler.DeleteSandboxSetting)
	return r
}

const sandboxSettingsPath = "/workspaces/team-a/sandboxes/agent-1/settings"

// The gateway's own refusals of a write to a key that is set globally, as
// gateway 0.1.2 words them. They are failed preconditions, which the SDK
// reports as conflicts.
const (
	globallyManagedSet    = "setting 'ocsf_json_enabled' is managed globally; delete the global setting before sandbox update"
	globallyManagedDelete = "setting 'ocsf_json_enabled' is managed globally; delete the global setting first"
)

// A sandbox's settings are read for that sandbox in that workspace, and each
// one says where its value comes from: the gateway, the sandbox, or nowhere.
func TestGetSandboxSettings(t *testing.T) {
	var gotWorkspace, gotSandbox string
	sdk := &mockSDK{}
	sdk.config.getSandboxFn = func(_ context.Context, workspace, sandboxName string) (*openshell.SandboxConfig, error) {
		gotWorkspace, gotSandbox = workspace, sandboxName
		return &openshell.SandboxConfig{
			PolicySource:                openshell.PolicySourceSandbox,
			PolicyHash:                  "sha256:abc",
			PolicyVersion:               2,
			ConfigRevision:              18446744073709551615,
			ProviderEnvRevision:         42,
			PolicyValidationFailureMode: "retain_last_valid",
			Settings: map[string]openshell.EffectiveSetting{
				"proposal_approval_mode": {
					Scope: openshell.SettingScopeSandbox,
					Value: openshell.SettingValue{Type: openshell.SettingValueString, StringVal: "auto"},
				},
				"ocsf_json_enabled": {
					Scope: openshell.SettingScopeGlobal,
					Value: openshell.SettingValue{Type: openshell.SettingValueBool, BoolVal: true},
				},
				"ocsf_schema_version": {},
			},
		}, nil
	}
	w := httptest.NewRecorder()
	sandboxSettingsRouter(sdk).ServeHTTP(w, httptest.NewRequest(http.MethodGet, sandboxSettingsPath, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if gotWorkspace != "team-a" || gotSandbox != "agent-1" {
		t.Errorf("config read for %q in %q, want agent-1 in team-a", gotSandbox, gotWorkspace)
	}

	// The whole answer. The settings are sorted by key; a setting that is set
	// nowhere has no value; the two revisions are fingerprints and travel as
	// strings so that no digit is rounded away in a browser.
	const want = `{
		"policySource": "SANDBOX",
		"policyHash": "sha256:abc",
		"policyValidationFailureMode": "retain_last_valid",
		"settings": [
			{"key": "ocsf_json_enabled", "value": true, "scope": "GLOBAL"},
			{"key": "ocsf_schema_version", "scope": "UNSPECIFIED"},
			{"key": "proposal_approval_mode", "value": "auto", "scope": "SANDBOX"}
		],
		"configRevision": "18446744073709551615",
		"providerEnvRevision": "42",
		"policyVersion": 2,
		"globalPolicyVersion": 0
	}`
	var got, wanted any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if err := json.Unmarshal([]byte(want), &wanted); err != nil {
		t.Fatalf("decode the expected document: %v", err)
	}
	if !reflect.DeepEqual(got, wanted) {
		t.Errorf("sandbox settings JSON differs.\n got: %s\nwant: %s", w.Body.String(), want)
	}
}

func TestGetSandboxSettingsErrors(t *testing.T) {
	tests := []struct {
		err        error
		name       string
		wantCode   string
		wantStatus int
	}{
		{
			name:       "unknown sandbox",
			err:        &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"},
			wantStatus: http.StatusNotFound, wantCode: "not_found",
		},
		{
			name:       "not a member of the workspace",
			err:        &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "access denied"},
			wantStatus: http.StatusForbidden, wantCode: "permission_denied",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.config.getSandboxFn = func(context.Context, string, string) (*openshell.SandboxConfig, error) {
				return nil, tc.err
			}
			w := httptest.NewRecorder()
			sandboxSettingsRouter(sdk).ServeHTTP(w, httptest.NewRequest(http.MethodGet, sandboxSettingsPath, nil))
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if w.Code != tc.wantStatus || body["code"] != tc.wantCode {
				t.Errorf("status = %d code = %v, want %d %q", w.Code, body["code"], tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// A sandbox setting is sent the way a global one is: the JSON type of the
// value selects the kind, and nothing is coerced. What differs is the scope:
// the update names the sandbox and its workspace and is not global.
func TestSetSandboxSetting(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		key   string
		value openshell.SettingValue
	}{
		{
			name: "string", body: `{"key":"proposal_approval_mode","value":"auto"}`, key: "proposal_approval_mode",
			value: openshell.SettingValue{Type: openshell.SettingValueString, StringVal: "auto"},
		},
		{
			name: "empty string is a value", body: `{"key":"ocsf_schema_version","value":""}`, key: "ocsf_schema_version",
			value: openshell.SettingValue{Type: openshell.SettingValueString, StringVal: ""},
		},
		{
			name: "bool false", body: `{"key":"ocsf_json_enabled","value":false}`, key: "ocsf_json_enabled",
			value: openshell.SettingValue{Type: openshell.SettingValueBool, BoolVal: false},
		},
		{
			name: "int", body: `{"key":"retries","value":3}`, key: "retries",
			value: openshell.SettingValue{Type: openshell.SettingValueInt, IntVal: 3},
		},
		{
			name: "a quoted bool stays a string", body: `{"key":"ocsf_json_enabled","value":"true"}`, key: "ocsf_json_enabled",
			value: openshell.SettingValue{Type: openshell.SettingValueString, StringVal: "true"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, got, workspace := putSandboxSetting(tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			// The whole update: the sandbox, the key and the typed value, and
			// neither a global scope nor a policy riding along.
			want := &openshell.ConfigUpdate{Name: "agent-1", SettingKey: tc.key, SettingValue: &tc.value}
			if workspace != "team-a" || !reflect.DeepEqual(got, want) {
				t.Errorf("update = %+v in workspace %q, want %+v in team-a", got, workspace, want)
			}
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if want := map[string]any{"updated": true, "settingsRevision": float64(5)}; !reflect.DeepEqual(body, want) {
				t.Errorf("response = %v, want %v", body, want)
			}
		})
	}
}

// putSandboxSetting sends a set-setting body through the route and returns the
// response with the update the gateway was sent and the workspace it was sent
// for. The update is nil when the gateway was never called.
func putSandboxSetting(body string) (*httptest.ResponseRecorder, *openshell.ConfigUpdate, string) {
	var (
		got          *openshell.ConfigUpdate
		gotWorkspace string
	)
	sdk := &mockSDK{}
	sdk.config.updateFn = func(_ context.Context, workspace string, update *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
		got, gotWorkspace = update, workspace
		return &openshell.ConfigUpdateResult{SettingsRevision: 5}, nil
	}
	req := httptest.NewRequest(http.MethodPut, sandboxSettingsPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	sandboxSettingsRouter(sdk).ServeHTTP(w, req)
	return w, got, gotWorkspace
}

// A body the BFF cannot read as a key and a typed value is refused before the
// gateway is asked, exactly as it is for a global setting.
func TestSetSandboxSettingRefuses(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "missing key", body: `{"key":"","value":"x"}`, wantCode: "invalid_setting"},
		{name: "missing value", body: `{"key":"retries"}`, wantCode: "invalid_setting"},
		{name: "null value", body: `{"key":"retries","value":null}`, wantCode: "invalid_setting"},
		{name: "fraction", body: `{"key":"retries","value":1.5}`, wantCode: "invalid_setting"},
		{name: "object", body: `{"key":"retries","value":{"boolValue":true}}`, wantCode: "invalid_setting"},
		{name: "malformed body", body: `{"key":`, wantCode: "invalid_body"},
		// The scope is the route's to say. A body cannot widen it.
		{name: "a global flag in the body", body: `{"key":"retries","value":1,"global":true}`, wantCode: "invalid_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, got, _ := putSandboxSetting(tc.body)
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if w.Code != http.StatusBadRequest || body["code"] != tc.wantCode {
				t.Errorf("status = %d code = %v, want 400 %q", w.Code, body["code"], tc.wantCode)
			}
			if got != nil {
				t.Errorf("a refused body still reached the gateway: %+v", got)
			}
		})
	}
}

// While a key is set globally the gateway refuses to set or delete it on a
// sandbox. The refusal reaches the browser as a conflict that carries the
// gateway's own sentence, which is what the Settings tab shows.
func TestSandboxSettingManagedGlobally(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		refusal string
	}{
		{
			name: "set", method: http.MethodPut, path: sandboxSettingsPath,
			body: `{"key":"ocsf_json_enabled","value":true}`, refusal: globallyManagedSet,
		},
		{
			name: "delete", method: http.MethodDelete, path: sandboxSettingsPath + "?key=ocsf_json_enabled",
			refusal: globallyManagedDelete,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.config.updateFn = func(context.Context, string, *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorConflict, Message: tc.refusal}
			}
			w := httptest.NewRecorder()
			sandboxSettingsRouter(sdk).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if w.Code != http.StatusConflict || body["code"] != "conflict" || body["message"] != tc.refusal {
				t.Errorf("status = %d, error = %v; want 409 conflict with the gateway's message %q", w.Code, body, tc.refusal)
			}
		})
	}
}

// Deleting a sandbox setting answers with what the gateway did: deleted is
// false when the key was not set on the sandbox, which is not an error.
func TestDeleteSandboxSetting(t *testing.T) {
	for _, removed := range []bool{true, false} {
		var (
			got          *openshell.ConfigUpdate
			gotWorkspace string
		)
		sdk := &mockSDK{}
		sdk.config.updateFn = func(_ context.Context, workspace string, update *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
			got, gotWorkspace = update, workspace
			return &openshell.ConfigUpdateResult{Deleted: removed, SettingsRevision: 6}, nil
		}
		w := httptest.NewRecorder()
		sandboxSettingsRouter(sdk).ServeHTTP(w,
			httptest.NewRequest(http.MethodDelete, sandboxSettingsPath+"?key=proposal_approval_mode", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
		}
		if got == nil {
			t.Fatal("the gateway was not called")
		}
		if got.Name != "agent-1" || gotWorkspace != "team-a" || got.SettingKey != "proposal_approval_mode" {
			t.Errorf("update = %+v in %q, want proposal_approval_mode of agent-1 in team-a", got, gotWorkspace)
		}
		if !got.DeleteSetting || got.Global || got.SettingValue != nil {
			t.Errorf("update = %+v, want a sandbox-scoped delete without a value", got)
		}
		var body map[string]any
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["deleted"] != removed || body["settingsRevision"] != float64(6) {
			t.Errorf("response = %v, want deleted %v at settings revision 6", body, removed)
		}
	}
}

func TestDeleteSandboxSettingMissingKey(t *testing.T) {
	called := false
	sdk := &mockSDK{}
	sdk.config.updateFn = func(context.Context, string, *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
		called = true
		return &openshell.ConfigUpdateResult{}, nil
	}
	w := httptest.NewRecorder()
	sandboxSettingsRouter(sdk).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, sandboxSettingsPath, nil))
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if w.Code != http.StatusBadRequest || body["code"] != "invalid_setting" {
		t.Errorf("status = %d code = %v, want 400 invalid_setting", w.Code, body["code"])
	}
	if called {
		t.Error("a delete without a key still reached the gateway")
	}
}
