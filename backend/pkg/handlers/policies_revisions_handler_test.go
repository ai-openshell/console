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

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	sdktypes "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// policyRoutes mounts every policy route on the paths app.go gives them.
func policyRoutes(sdk *mockSDK) http.Handler {
	handler := NewPoliciesHandler(services.NewPolicyService(sdk.Policy()), services.NewConfig(sdk.Config()))
	r := chi.NewRouter()
	r.Get("/global-policy", handler.GetGlobalPolicy)
	r.Get("/global-policy/revisions/{version}", handler.GetGlobalPolicyRevision)
	r.Get("/workspaces/{workspace}/sandboxes/{name}/policy/effective", handler.GetEffectiveSandboxPolicy)
	r.Get("/workspaces/{workspace}/sandboxes/{name}/policy/revisions/{version}", handler.GetSandboxPolicyRevision)
	r.Post("/workspaces/{workspace}/sandboxes/{name}/policy/merge", handler.MergeSandboxPolicy)
	r.Put("/workspaces/{workspace}/sandboxes/{name}/policy", handler.UpdateSandboxPolicy)
	return r
}

func serve(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// decodeInto decodes a response body, failing the test when it is not the
// JSON the caller expects.
func decodeInto(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
}

// wantErrorResponse asserts the status and the error envelope of a refused
// request, and returns the message.
func wantErrorResponse(t *testing.T, w *httptest.ResponseRecorder, status int, code apiutils.ResponseCode) string {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, status, w.Body.String())
	}
	var envelope apiutils.ErrorResponse
	decodeInto(t, w, &envelope)
	if envelope.Code != code {
		t.Errorf("code = %q, want %q; message: %s", envelope.Code, code, envelope.Message)
	}
	return envelope.Message
}

func policyStatusOptions(opts []openshell.GetStatusOption) (version uint32, global bool) {
	cfg := sdktypes.ApplyGetStatusOptions(opts)
	return (&cfg).Version(), (&cfg).Global()
}

func revision(version uint32, status openshell.PolicyLoadStatus) openshell.SandboxPolicyRevision {
	return openshell.SandboxPolicyRevision{Version: version, PolicyHash: "hash", Status: status}
}

func policyWithRule(name, host string) *openshell.SandboxPolicy {
	return &openshell.SandboxPolicy{
		Version: 1,
		NetworkPolicies: map[string]openshell.NetworkPolicyRule{
			name: {Name: name, Endpoints: []openshell.PolicyNetworkEndpoint{{Host: host, Port: 443}}},
		},
	}
}

type globalPolicyCase struct {
	payloadErr     error
	name           string
	wantPolicyHost string
	listed         []openshell.SandboxPolicyRevision
	wantVersions   []uint32
	wantLatest     uint32
	wantActive     uint32
}

// globalPolicySDK plays the gateway for the global policy view: a listing
// without payloads, and the one revision read that has one. It returns the
// versions the payload was read for.
func globalPolicySDK(t *testing.T, tc globalPolicyCase) (*mockSDK, *[]uint32) {
	t.Helper()
	sdk := &mockSDK{}
	sdk.policy.listFn = func(_ context.Context, _ string, opts ...openshell.ListPolicyOption) ([]openshell.SandboxPolicyRevision, error) {
		cfg := sdktypes.ApplyListPolicyOptions(opts)
		if !(&cfg).Global() {
			t.Error("the revisions were listed without the global option")
		}
		return tc.listed, nil
	}
	payloadReads := []uint32{}
	sdk.policy.getStatusFn = func(_ context.Context, workspace, name string, opts ...openshell.GetStatusOption) (*openshell.PolicyStatusResult, error) {
		version, global := policyStatusOptions(opts)
		payloadReads = append(payloadReads, version)
		if !global || workspace != "" || name != "" {
			t.Errorf("payload read with global=%v workspace=%q sandbox=%q, want the global scope alone", global, workspace, name)
		}
		if tc.payloadErr != nil {
			return nil, tc.payloadErr
		}
		rev := revision(version, openshell.PolicyLoadStatusLoaded)
		rev.Policy = policyWithRule("global", "global.example.com")
		return &openshell.PolicyStatusResult{Revision: rev}, nil
	}
	return sdk, &payloadReads
}

// The gateway lists global revisions without their policy, so the view has to
// fetch the newest one's payload itself; and whether a global policy is in
// force is only visible in that revision's status.
func TestGetGlobalPolicy(t *testing.T) {
	tests := []globalPolicyCase{
		{
			name:         "none set",
			listed:       nil,
			wantVersions: []uint32{},
		},
		{
			name: "newest revision is in force and carries its policy",
			listed: []openshell.SandboxPolicyRevision{
				revision(2, openshell.PolicyLoadStatusLoaded),
				revision(1, openshell.PolicyLoadStatusSuperseded),
			},
			wantVersions:   []uint32{2, 1},
			wantLatest:     2,
			wantActive:     2,
			wantPolicyHost: "global.example.com",
		},
		{
			name: "the newest revision is found whatever order the listing is in",
			listed: []openshell.SandboxPolicyRevision{
				revision(1, openshell.PolicyLoadStatusSuperseded),
				revision(3, openshell.PolicyLoadStatusLoaded),
				revision(2, openshell.PolicyLoadStatusSuperseded),
			},
			wantVersions:   []uint32{1, 3, 2},
			wantLatest:     3,
			wantActive:     3,
			wantPolicyHost: "global.example.com",
		},
		{
			name: "a removed global policy leaves its revisions and none active",
			listed: []openshell.SandboxPolicyRevision{
				revision(2, openshell.PolicyLoadStatusSuperseded),
				revision(1, openshell.PolicyLoadStatusSuperseded),
			},
			wantVersions:   []uint32{2, 1},
			wantLatest:     2,
			wantActive:     0,
			wantPolicyHost: "global.example.com",
		},
		{
			name: "an unreadable payload does not hide the history",
			listed: []openshell.SandboxPolicyRevision{
				revision(1, openshell.PolicyLoadStatusLoaded),
			},
			payloadErr:   &openshell.StatusError{Code: openshell.ErrorInternal, Message: "policy revision is invalid under the current schema"},
			wantVersions: []uint32{1},
			wantLatest:   1,
			wantActive:   1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk, payloadReads := globalPolicySDK(t, tc)

			w := serve(t, policyRoutes(sdk), http.MethodGet, "/global-policy", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
			// GlobalPolicyPage reads .revisions.length without a null check.
			if !strings.Contains(w.Body.String(), `"revisions":[`) {
				t.Fatalf(`no "revisions" array in: %s`, w.Body.String())
			}
			var view models.SandboxPolicyView
			decodeInto(t, w, &view)
			assertGlobalPolicyView(t, tc, view, *payloadReads)
		})
	}
}

func assertGlobalPolicyView(t *testing.T, tc globalPolicyCase, view models.SandboxPolicyView, payloadReads []uint32) {
	t.Helper()
	versions := []uint32{}
	for _, r := range view.Revisions {
		versions = append(versions, r.Version)
	}
	if !reflect.DeepEqual(versions, tc.wantVersions) {
		t.Errorf("revisions = %v, want %v in the gateway's order", versions, tc.wantVersions)
	}
	if view.ActiveVersion != tc.wantActive {
		t.Errorf("activeVersion = %d, want %d", view.ActiveVersion, tc.wantActive)
	}
	if tc.wantLatest == 0 {
		if view.Latest != nil || len(payloadReads) != 0 {
			t.Errorf("latest = %+v after %d payload read(s), want neither without a revision", view.Latest, len(payloadReads))
		}
		return
	}
	// One read, for the newest revision: the listing has the rest.
	if !reflect.DeepEqual(payloadReads, []uint32{tc.wantLatest}) {
		t.Errorf("payload read for revisions %v, want only the newest, v%d", payloadReads, tc.wantLatest)
	}
	if view.Latest == nil || view.Latest.Version != tc.wantLatest {
		t.Fatalf("latest = %+v, want revision v%d", view.Latest, tc.wantLatest)
	}
	policy := string(view.Latest.Policy)
	if tc.wantPolicyHost == "" && policy != "" {
		t.Errorf("latest.policy = %s, want it absent", policy)
	}
	if !strings.Contains(policy, tc.wantPolicyHost) {
		t.Errorf("latest.policy = %s, want the policy of v%d (%s)", policy, tc.wantLatest, tc.wantPolicyHost)
	}
}

func TestGetGlobalPolicyListError(t *testing.T) {
	sdk := &mockSDK{}
	sdk.policy.listFn = func(context.Context, string, ...openshell.ListPolicyOption) ([]openshell.SandboxPolicyRevision, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "platform admin role required"}
	}
	w := serve(t, policyRoutes(sdk), http.MethodGet, "/global-policy", "")
	wantErrorResponse(t, w, http.StatusForbidden, apiutils.PermissionDenied)
}

func TestGetPolicyRevision(t *testing.T) {
	const sandboxBase = "/workspaces/team-a/sandboxes/sb1/policy/revisions/"
	tests := []struct {
		name          string
		path          string
		wantWorkspace string
		wantSandbox   string
		wantVersion   uint32
		wantGlobal    bool
	}{
		{name: "sandbox revision", path: sandboxBase + "2", wantVersion: 2, wantWorkspace: "team-a", wantSandbox: "sb1"},
		{name: "global revision", path: "/global-policy/revisions/5", wantVersion: 5, wantGlobal: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.policy.getStatusFn = func(_ context.Context, workspace, name string, opts ...openshell.GetStatusOption) (*openshell.PolicyStatusResult, error) {
				version, global := policyStatusOptions(opts)
				if version != tc.wantVersion || global != tc.wantGlobal || workspace != tc.wantWorkspace || name != tc.wantSandbox {
					t.Errorf("GetStatus(workspace=%q, sandbox=%q, version=%d, global=%v), want (%q, %q, %d, %v)",
						workspace, name, version, global, tc.wantWorkspace, tc.wantSandbox, tc.wantVersion, tc.wantGlobal)
				}
				rev := revision(version, openshell.PolicyLoadStatusFailed)
				rev.LoadError = "landlock unavailable"
				rev.Provenance = map[string]string{"source": "dashboard"}
				rev.Policy = policyWithRule("gh", "api.github.com")
				return &openshell.PolicyStatusResult{Revision: rev, ActiveVersion: 1}, nil
			}

			w := serve(t, policyRoutes(sdk), http.MethodGet, tc.path, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
			var got models.PolicyRevision
			decodeInto(t, w, &got)
			if got.Version != tc.wantVersion || got.Status != "FAILED" || got.LoadError != "landlock unavailable" || got.PolicyHash != "hash" {
				t.Errorf("revision = %+v, want v%d FAILED with its hash and load error", got, tc.wantVersion)
			}
			if got.Provenance["source"] != "dashboard" {
				t.Errorf("provenance = %v, want it carried", got.Provenance)
			}
			if !strings.Contains(string(got.Policy), "api.github.com") {
				t.Errorf("policy = %s, want the revision's payload", got.Policy)
			}
		})
	}
}

func TestGetPolicyRevisionRefusals(t *testing.T) {
	const sandboxBase = "/workspaces/team-a/sandboxes/sb1/policy/revisions/"
	tests := []struct {
		name       string
		path       string
		wantCode   apiutils.ResponseCode
		wantStatus int
		wantAsked  bool
	}{
		{name: "sandbox revision the gateway does not have", path: sandboxBase + "9", wantStatus: http.StatusNotFound, wantCode: apiutils.NotFound, wantAsked: true},
		{name: "global revision the gateway does not have", path: "/global-policy/revisions/9", wantStatus: http.StatusNotFound, wantCode: apiutils.NotFound, wantAsked: true},
		// Zero means "the latest" to the gateway; a URL that names a revision
		// must not quietly return a different one.
		{name: "sandbox version zero", path: sandboxBase + "0", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidRequest},
		{name: "global version zero", path: "/global-policy/revisions/0", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidRequest},
		{name: "not a number", path: sandboxBase + "latest", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidRequest},
		{name: "negative", path: sandboxBase + "-1", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidRequest},
		{name: "larger than a revision number can be", path: "/global-policy/revisions/4294967296", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			asked := false
			sdk.policy.getStatusFn = func(context.Context, string, string, ...openshell.GetStatusOption) (*openshell.PolicyStatusResult, error) {
				asked = true
				return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "no policy revision found"}
			}
			w := serve(t, policyRoutes(sdk), http.MethodGet, tc.path, "")
			wantErrorResponse(t, w, tc.wantStatus, tc.wantCode)
			if asked != tc.wantAsked {
				t.Errorf("gateway asked = %v, want %v", asked, tc.wantAsked)
			}
		})
	}
}

func TestGetEffectiveSandboxPolicy(t *testing.T) {
	t.Run("returns the enforced policy and its source", func(t *testing.T) {
		sdk := &mockSDK{}
		sdk.config.getSandboxFn = func(_ context.Context, workspace, name string) (*openshell.SandboxConfig, error) {
			if workspace != "team-a" || name != "sb1" {
				t.Errorf("GetSandbox(%q, %q), want (team-a, sb1)", workspace, name)
			}
			return &openshell.SandboxConfig{
				Policy:              policyWithRule("_provider_claude", "api.anthropic.com"),
				PolicyVersion:       3,
				PolicyHash:          "effective-hash",
				PolicySource:        openshell.PolicySourceGlobal,
				GlobalPolicyVersion: 7,
			}, nil
		}
		w := serve(t, policyRoutes(sdk), http.MethodGet, "/workspaces/team-a/sandboxes/sb1/policy/effective", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
		}
		var got models.EffectivePolicy
		decodeInto(t, w, &got)
		if got.Version != 3 || got.PolicyHash != "effective-hash" || got.PolicySource != "GLOBAL" || got.GlobalPolicyVersion != 7 {
			t.Errorf("got %+v, want v3 effective-hash from global v7", got)
		}
		if !strings.Contains(string(got.Policy), "_provider_claude") {
			t.Errorf("policy = %s, want the composed rule", got.Policy)
		}
	})

	t.Run("unknown sandbox", func(t *testing.T) {
		sdk := &mockSDK{}
		sdk.config.getSandboxFn = func(context.Context, string, string) (*openshell.SandboxConfig, error) {
			return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
		}
		w := serve(t, policyRoutes(sdk), http.MethodGet, "/workspaces/team-a/sandboxes/missing/policy/effective", "")
		wantErrorResponse(t, w, http.StatusNotFound, apiutils.NotFound)
	})
}

const mergePath = "/workspaces/team-a/sandboxes/sb1/policy/merge"

// What the browser sends as protojson is what the SDK is handed, operation
// for operation and in order.
func TestMergeSandboxPolicy(t *testing.T) {
	emptyPath := ""
	tests := []struct {
		name string
		body string
		want []openshell.PolicyMergeOperation
	}{
		{
			name: "add an endpoint under a rule name",
			body: `{"operations":[{"addRule":{"ruleName":"allow_api_github_com_443","rule":{"name":"allow_api_github_com_443",` +
				`"endpoints":[{"host":"api.github.com","port":443,"ports":[443],"protocol":"rest","access":"NETWORK_ACCESS_PRESET_READ_ONLY",` +
				`"enforcement":"NETWORK_ENFORCEMENT_MODE_ENFORCE","allowedIps":["10.0.0.0/8"],"allowUninspectedCredentials":true}],` +
				`"binaries":[{"path":"/usr/bin/gh"}]}}}]}`,
			want: []openshell.PolicyMergeOperation{{AddRule: &openshell.AddNetworkRule{
				RuleName: "allow_api_github_com_443",
				Rule: openshell.NetworkPolicyRule{
					Name: "allow_api_github_com_443",
					Endpoints: []openshell.PolicyNetworkEndpoint{{
						Host: "api.github.com", Port: 443, Ports: []uint32{443}, Protocol: "rest",
						Access:                      sdktypes.NetworkAccessPresetReadOnly,
						Enforcement:                 sdktypes.NetworkEnforcementModeEnforce,
						AllowedIPs:                  []string{"10.0.0.0/8"},
						AllowUninspectedCredentials: true,
					}},
					Binaries: []openshell.PolicyNetworkBinary{{Path: "/usr/bin/gh"}},
				},
			}}},
		},
		{
			name: "several operations keep their order",
			body: `{"operations":[{"removeEndpoint":{"ruleName":"gh","host":"api.github.com","port":443}},` +
				`{"removeRule":{"ruleName":"old"}},{"removeBinary":{"ruleName":"gh","binaryPath":"/usr/bin/curl"}}]}`,
			want: []openshell.PolicyMergeOperation{
				{RemoveEndpoint: &openshell.RemoveNetworkEndpoint{RuleName: "gh", Host: "api.github.com", Port: 443}},
				{RemoveRule: &openshell.RemoveNetworkRule{RuleName: "old"}},
				{RemoveBinary: &openshell.RemoveNetworkBinary{RuleName: "gh", BinaryPath: "/usr/bin/curl"}},
			},
		},
		{
			name: "append an allow rule to a declared scope",
			body: `{"operations":[{"addAllowRules":{"target":{"ruleName":"gh","host":"api.github.com","ports":[443,8443],"path":"",` +
				`"binaries":[{"path":"/usr/bin/gh"}]},"rules":[{"allow":{"method":"POST","path":"/repos/**"}}]}}]}`,
			want: []openshell.PolicyMergeOperation{{AddAllowRules: &openshell.AddAllowRules{
				Target: &openshell.L7RuleTarget{
					RuleName: "gh", Host: "api.github.com", Ports: []uint32{443, 8443}, Path: &emptyPath,
					Binaries: []openshell.PolicyNetworkBinary{{Path: "/usr/bin/gh"}},
				},
				Rules: []openshell.L7Rule{{Allow: &sdktypes.L7Allow{Method: "POST", Path: "/repos/**"}}},
			}}},
		},
		{
			name: "append a deny rule to an any-binary rule",
			body: `{"operations":[{"addDenyRules":{"target":{"ruleName":"gh","host":"api.github.com","ports":[443],"anyBinary":true},` +
				`"denyRules":[{"method":"DELETE","path":"/repos/**"}]}}]}`,
			want: []openshell.PolicyMergeOperation{{AddDenyRules: &openshell.AddDenyRules{
				Target:    &openshell.L7RuleTarget{RuleName: "gh", Host: "api.github.com", Ports: []uint32{443}, AnyBinary: true},
				DenyRules: []openshell.L7DenyRule{{Method: "DELETE", Path: "/repos/**"}},
			}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			var got *openshell.ConfigUpdate
			var gotWorkspace string
			sdk.config.updateFn = func(_ context.Context, workspace string, update *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
				got, gotWorkspace = update, workspace
				return &openshell.ConfigUpdateResult{Version: 4, PolicyHash: "merged"}, nil
			}

			w := serve(t, policyRoutes(sdk), http.MethodPost, mergePath, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
			var result models.PolicyUpdateResult
			decodeInto(t, w, &result)
			if result.Version != 4 || result.PolicyHash != "merged" {
				t.Errorf("result = %+v, want version 4 and the merged hash", result)
			}
			// The operations and nothing else: no policy, and no resource
			// version, which the gateway does not need for a merge.
			want := &openshell.ConfigUpdate{Name: "sb1", MergeOperations: tc.want}
			if gotWorkspace != "team-a" || !reflect.DeepEqual(got, want) {
				t.Errorf("update sent to the gateway differs from the request.\nworkspace: %q\ngot:  %+v\nwant: %+v", gotWorkspace, got, want)
			}
		})
	}
}

func TestMergeSandboxPolicyRefusals(t *testing.T) {
	removeRule := `{"operations":[{"removeRule":{"ruleName":"gh"}}]}`
	tests := []struct {
		gatewayErr error
		name       string
		body       string
		wantCode   apiutils.ResponseCode
		wantStatus int
	}{
		{name: "no operations", body: `{"operations":[]}`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPolicy},
		{name: "operations missing", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPolicy},
		{name: "an operation that names no variant", body: `{"operations":[{}]}`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPolicy},
		{name: "an operation the gateway does not have", body: `{"operations":[{"replaceRule":{"ruleName":"gh"}}]}`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPolicy},
		{name: "a full policy sent to the wrong route", body: `{"policy":{"version":1}}`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidBody},
		{name: "a resource version, which a merge does not take", body: `{"operations":[{"removeRule":{"ruleName":"gh"}}],"expectedResourceVersion":3}`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidBody},
		{name: "invalid json", body: `not-json`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidBody},
		{
			// The gateway refuses a merge that would hand a binary access the
			// operation did not declare; the user has to see why.
			name:       "the gateway refuses the merge",
			body:       removeRule,
			gatewayErr: &openshell.StatusError{Code: openshell.ErrorConflict, Message: "adding binary '/usr/bin/curl' would let it reach every endpoint of rule 'gh'"},
			wantStatus: http.StatusConflict,
			wantCode:   apiutils.Conflict,
		},
		{
			name:       "a global policy is in force",
			body:       removeRule,
			gatewayErr: &openshell.StatusError{Code: openshell.ErrorConflict, Message: "policy is managed globally; delete global policy before sandbox policy update"},
			wantStatus: http.StatusConflict,
			wantCode:   apiutils.Conflict,
		},
		{
			name:       "unknown sandbox",
			body:       removeRule,
			gatewayErr: &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"},
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.NotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			asked := false
			sdk.config.updateFn = func(context.Context, string, *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
				asked = true
				return nil, tc.gatewayErr
			}

			w := serve(t, policyRoutes(sdk), http.MethodPost, mergePath, tc.body)
			message := wantErrorResponse(t, w, tc.wantStatus, tc.wantCode)
			if asked != (tc.gatewayErr != nil) {
				t.Errorf("gateway asked = %v, want %v", asked, tc.gatewayErr != nil)
			}
			var gatewayErr *openshell.StatusError
			if tc.gatewayErr != nil {
				gatewayErr, _ = tc.gatewayErr.(*openshell.StatusError)
			}
			if gatewayErr != nil && message != gatewayErr.Message {
				t.Errorf("message = %q, want the gateway's own reason, %q", message, gatewayErr.Message)
			}
		})
	}
}

// A full replacement carries the policy and nothing else, and the resource
// version the editor read the sandbox at.
func TestUpdateSandboxPolicySendsTheWholePolicy(t *testing.T) {
	sdk := &mockSDK{}
	var got *openshell.ConfigUpdate
	sdk.config.updateFn = func(_ context.Context, _ string, update *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
		got = update
		return &openshell.ConfigUpdateResult{Version: 2, PolicyHash: "replaced"}, nil
	}
	body := `{"policy":{"version":1,"networkPolicies":{"mcp":{"name":"mcp","endpoints":[{"host":"mcp.example.com","port":443,` +
		`"protocol":"mcp","mcp":{"versions":["2025-03-26","2025-06-18"]}}]}}},"expectedResourceVersion":8}`

	w := serve(t, policyRoutes(sdk), http.MethodPut, "/workspaces/team-a/sandboxes/sb1/policy", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if got == nil || got.Policy == nil || len(got.MergeOperations) != 0 || got.ExpectedResourceVersion != 8 {
		t.Fatalf("update = %+v, want the policy alone at resource version 8", got)
	}
	mcp := got.Policy.NetworkPolicies["mcp"].Endpoints[0].Mcp
	if mcp == nil || !reflect.DeepEqual(mcp.Versions, []string{"2025-03-26", "2025-06-18"}) {
		t.Errorf("mcp = %+v, want the MCP revision allowlist the request carried: the gateway replaces an empty one with its default", mcp)
	}
}
