package models

import (
	"encoding/json"
	"strings"
	"testing"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/types"
)

func TestBuildSDKSandboxSpecBasic(t *testing.T) {
	req := CreateSandboxRequest{
		Name:   "test-sandbox",
		Image:  "ubuntu:latest",
		Policy: json.RawMessage(`{"version":1,"filesystem":{"includeWorkdir":true}}`),
	}
	spec, err := BuildSDKSandboxSpec(req)
	if err != nil {
		t.Fatalf("BuildSDKSandboxSpec: %v", err)
	}
	if spec.Template == nil || spec.Template.Image != "ubuntu:latest" {
		t.Errorf("image = %v, want ubuntu:latest", spec.Template)
	}
	if spec.Policy == nil || spec.Policy.Version != 1 {
		t.Errorf("policy version = %v, want 1", spec.Policy)
	}
	if spec.Policy.Filesystem == nil || !spec.Policy.Filesystem.IncludeWorkdir {
		t.Error("expected filesystem.includeWorkdir = true")
	}
}

func TestBuildSDKSandboxSpecWithGPU(t *testing.T) {
	req := CreateSandboxRequest{
		Name:     "gpu-sandbox",
		Image:    "nvidia/cuda",
		GpuCount: 2,
		Policy:   json.RawMessage(`{"version":1}`),
	}
	spec, err := BuildSDKSandboxSpec(req)
	if err != nil {
		t.Fatalf("BuildSDKSandboxSpec: %v", err)
	}
	if spec.GPUCount == nil || *spec.GPUCount != 2 {
		t.Errorf("GPUCount = %v, want 2", spec.GPUCount)
	}
}

func TestBuildSDKSandboxSpecWithCPUMemory(t *testing.T) {
	req := CreateSandboxRequest{
		Name:   "resource-sandbox",
		Image:  "ubuntu:latest",
		CPU:    "4",
		Memory: "8Gi",
		Policy: json.RawMessage(`{"version":1}`),
	}
	spec, err := BuildSDKSandboxSpec(req)
	if err != nil {
		t.Fatalf("BuildSDKSandboxSpec: %v", err)
	}
	if spec.Template.Resources == nil {
		t.Fatal("expected Template.Resources to be set")
	}
	limits, ok := spec.Template.Resources["limits"]
	if !ok {
		t.Fatal("expected resources.limits to be set")
	}
	limitsMap, ok := limits.(map[string]any)
	if !ok {
		t.Fatalf("limits is %T, want map[string]any", limits)
	}
	if limitsMap["cpu"] != "4" {
		t.Errorf("cpu = %v, want 4", limitsMap["cpu"])
	}
	if limitsMap["memory"] != "8Gi" {
		t.Errorf("memory = %v, want 8Gi", limitsMap["memory"])
	}
}

func TestBuildSDKSandboxSpecInvalidPolicy(t *testing.T) {
	req := CreateSandboxRequest{
		Name:   "bad-policy",
		Image:  "ubuntu:latest",
		Policy: json.RawMessage(`{invalid json`),
	}
	_, err := BuildSDKSandboxSpec(req)
	if err == nil {
		t.Fatal("expected error for invalid policy JSON")
	}
}

func TestFromSDKSandboxNil(t *testing.T) {
	got := FromSDKSandbox(nil)
	if got.Metadata.Name != "" || got.Status.Phase != "" {
		t.Errorf("expected empty sandbox, got %+v", got)
	}
}

func TestFromSDKSandboxPhaseMapping(t *testing.T) {
	cases := []struct {
		phase openshell.SandboxPhase
		want  string
	}{
		{openshell.SandboxProvisioning, "PROVISIONING"},
		{openshell.SandboxReady, "READY"},
		{openshell.SandboxError, "ERROR"},
		{openshell.SandboxDeleting, "DELETING"},
		{openshell.SandboxUnknown, "UNKNOWN"},
	}
	for _, tc := range cases {
		t.Run(string(tc.phase), func(t *testing.T) {
			sb := &openshell.Sandbox{
				Status: openshell.SandboxStatus{Phase: tc.phase},
			}
			result := FromSDKSandbox(sb)
			if result.Status.Phase != tc.want {
				t.Errorf("phase %q: got %q, want %q", tc.phase, result.Status.Phase, tc.want)
			}
		})
	}
}

func TestFromSDKProviderStripsCredentials(t *testing.T) {
	got := FromSDKProvider(&openshell.Provider{
		Name: "claude-prov",
		Type: "claude",
		Spec: openshell.ProviderSpec{
			Credentials: map[string]string{"api_key": "sk-secret"},
			Config:      map[string]string{"region": "us"},
		},
	})
	if got.Type != "claude" {
		t.Errorf("type = %q", got.Type)
	}
	if got.Config["region"] != "us" {
		t.Errorf("config = %v", got.Config)
	}
	if len(got.CredentialNames) != 1 || got.CredentialNames[0] != "api_key" {
		t.Errorf("credentialNames = %v, want [api_key]", got.CredentialNames)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "sk-secret") {
		t.Errorf("leaked credential value: %s", raw)
	}
}

// A provider read from a gateway reaches the BFF without its credential keys:
// the SDK drops them. They are added from the gateway's own answer, and the
// list stays sorted with each key once however it was learned.
func TestProviderAddCredentialNames(t *testing.T) {
	got := FromSDKProvider(&openshell.Provider{Name: "claude-prov", Type: "claude"})
	if len(got.CredentialNames) != 0 {
		t.Fatalf("credentialNames = %v before any were added, want none", got.CredentialNames)
	}

	got.AddCredentialNames(nil)
	if got.CredentialNames != nil {
		t.Errorf("credentialNames = %v after adding none, want it left absent", got.CredentialNames)
	}

	got.AddCredentialNames([]string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"})
	got.AddCredentialNames([]string{"ANTHROPIC_API_KEY", "AWS_ROLE"})
	want := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "AWS_ROLE"}
	if strings.Join(got.CredentialNames, ",") != strings.Join(want, ",") {
		t.Errorf("credentialNames = %v, want %v", got.CredentialNames, want)
	}

	// Names the SDK does carry and names read beside it are one list.
	carried := FromSDKProvider(&openshell.Provider{
		Name: "claude-prov",
		Spec: openshell.ProviderSpec{Credentials: map[string]string{"ANTHROPIC_API_KEY": "REDACTED"}},
	})
	carried.AddCredentialNames([]string{"ANTHROPIC_API_KEY"})
	if len(carried.CredentialNames) != 1 || carried.CredentialNames[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("credentialNames = %v, want [ANTHROPIC_API_KEY] once", carried.CredentialNames)
	}
	raw, _ := json.Marshal(carried)
	if strings.Contains(string(raw), "REDACTED") {
		t.Errorf("the redaction placeholder was serialized: %s", raw)
	}
}

func TestFromSDKProviderCredentialNamesFromHandles(t *testing.T) {
	got := FromSDKProvider(&openshell.Provider{
		Name: "claude-prov",
		Type: "claude",
		Spec: openshell.ProviderSpec{
			CredentialHandles: map[string]types.CredentialHandle{
				"api_key": {Driver: "vault", Handle: "vault://must-not-leak"},
			},
		},
	})
	if len(got.CredentialNames) != 1 || got.CredentialNames[0] != "api_key" {
		t.Errorf("credentialNames = %v, want [api_key]", got.CredentialNames)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "vault://must-not-leak") || strings.Contains(string(raw), "vault") {
		t.Errorf("leaked credential handle: %s", raw)
	}
}

func TestFromSDKProviderNil(t *testing.T) {
	got := FromSDKProvider(nil)
	if got.Type != "" || got.Metadata.Name != "" {
		t.Errorf("expected empty provider, got %+v", got)
	}
}

func TestFromSDKRefreshStatusStrategies(t *testing.T) {
	tests := []struct {
		want     string
		strategy openshell.RefreshStrategy
	}{
		{want: "STATIC", strategy: openshell.RefreshStrategyStatic},
		{want: "EXTERNAL", strategy: openshell.RefreshStrategyExternal},
		{want: "OAUTH2_REFRESH_TOKEN", strategy: openshell.RefreshStrategyOAuth2RefreshToken},
		{want: "OAUTH2_CLIENT_CREDENTIALS", strategy: openshell.RefreshStrategyOAuth2ClientCredentials},
		{want: "GOOGLE_SERVICE_ACCOUNT_JWT", strategy: openshell.RefreshStrategyGoogleServiceAccountJWT},
		{want: "AWS_STS_ASSUME_ROLE", strategy: RefreshStrategyAWSStsAssumeRole},
		{want: "UNSPECIFIED", strategy: "nope"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got := FromSDKRefreshStatus(&openshell.RefreshStatus{
				CredentialKey: "api_key",
				Strategy:      tc.strategy,
				Status:        "active",
			})
			if got.Strategy != tc.want {
				t.Errorf("strategy = %q, want %q", got.Strategy, tc.want)
			}
		})
	}
}

func TestFromSDKProviderProfileCategories(t *testing.T) {
	tests := []struct {
		want     string
		category openshell.ProfileCategory
	}{
		{want: "OTHER", category: openshell.ProfileCategoryOther},
		{want: "INFERENCE", category: openshell.ProfileCategoryInference},
		{want: "AGENT", category: openshell.ProfileCategoryAgent},
		{want: "SOURCE_CONTROL", category: openshell.ProfileCategorySourceControl},
		{want: "MESSAGING", category: openshell.ProfileCategoryMessaging},
		{want: "DATA", category: openshell.ProfileCategoryData},
		{want: "KNOWLEDGE", category: openshell.ProfileCategoryKnowledge},
		{want: "UNSPECIFIED", category: "nope"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got := FromSDKProviderProfile(&openshell.ProviderProfile{
				ID:       "p",
				Category: tc.category,
				Endpoints: []openshell.NetworkEndpoint{
					{Host: "api.example.com", Port: 443},
				},
			})
			if got.Category != tc.want {
				t.Errorf("category = %q, want %q", got.Category, tc.want)
			}
			if len(got.Endpoints) != 1 || got.Endpoints[0] != "api.example.com:443" {
				t.Errorf("endpoints = %v, want [api.example.com:443]", got.Endpoints)
			}
		})
	}
}

func TestParseSDKProfileCategory(t *testing.T) {
	if ParseSDKProfileCategory("INFERENCE") != openshell.ProfileCategoryInference {
		t.Error("INFERENCE should map to ProfileCategoryInference")
	}
	if ParseSDKProfileCategory("unknown") != openshell.ProfileCategoryOther {
		t.Error("unknown should default to Other")
	}
}

func TestFromSDKDiagnostics(t *testing.T) {
	got := FromSDKDiagnostics([]openshell.ProfileDiagnostic{
		{Source: "import", ProfileID: "custom-llm", Field: "credentials[0].name", Message: "name is required", Severity: "error"},
	})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].ProfileID != "custom-llm" || got[0].Severity != "error" {
		t.Errorf("diagnostic = %+v", got[0])
	}
	if len(FromSDKDiagnostics(nil)) != 0 {
		t.Error("nil diagnostics should yield empty slice")
	}
}

func TestSDKWorkspaceRoleFromString(t *testing.T) {
	role, ok := SDKWorkspaceRoleFromString("USER")
	if !ok || role != openshell.WorkspaceRoleUser {
		t.Errorf("USER = %q ok=%v", role, ok)
	}
	role, ok = SDKWorkspaceRoleFromString("ADMIN")
	if !ok || role != openshell.WorkspaceRoleAdmin {
		t.Errorf("ADMIN = %q ok=%v", role, ok)
	}
	if _, ok := SDKWorkspaceRoleFromString("nope"); ok {
		t.Error("expected invalid role to fail")
	}
}

func TestFromSDKWorkspacePhaseMapping(t *testing.T) {
	got := FromSDKWorkspace(&openshell.Workspace{Name: "ws", Phase: openshell.WorkspaceActive})
	if got.Phase != "ACTIVE" || got.Metadata.Name != "ws" {
		t.Errorf("got %+v", got)
	}
	got = FromSDKWorkspace(&openshell.Workspace{Name: "ws", Phase: openshell.WorkspaceUnknown})
	if got.Phase != "UNSPECIFIED" {
		t.Errorf("unknown phase = %q, want UNSPECIFIED", got.Phase)
	}
}

func TestFromSDKPolicyStatusLatestOnly(t *testing.T) {
	view := FromSDKPolicyStatus(&openshell.PolicyStatusResult{
		ActiveVersion: 3,
		Revision:      openshell.SandboxPolicyRevision{Version: 3, Status: openshell.PolicyLoadStatusLoaded},
	})
	if view.ActiveVersion != 3 || view.Latest == nil || view.Latest.Status != "LOADED" {
		t.Errorf("view = %+v", view)
	}
	if len(view.Revisions) != 0 {
		t.Errorf("revisions = %d, want 0 (handler fills history)", len(view.Revisions))
	}
}

func TestFromSDKSandboxLogs(t *testing.T) {
	got := FromSDKSandboxLogs(&openshell.LogResult{
		BufferTotal: 4,
		Lines: []openshell.LogLine{
			{Message: "hi", Level: "INFO", Source: "gateway"},
		},
	})
	if got.BufferTotal != 4 || len(got.Logs) != 1 || got.Logs[0].Message != "hi" {
		t.Errorf("got %+v", got)
	}
}

func TestFromSDKGatewayInfo(t *testing.T) {
	got := FromSDKGatewayInfo(&openshell.GatewayInfo{
		Status:         openshell.ServiceStatusHealthy,
		Version:        "1.2.3",
		ComputeDrivers: []openshell.ComputeDriverInfo{{Name: "podman"}},
	})
	if got.Status != "HEALTHY" || got.GatewayVersion != "1.2.3" || len(got.ComputeDrivers) != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestParseSDKNetworkPolicyRule(t *testing.T) {
	rule, err := ParseSDKNetworkPolicyRule(json.RawMessage(`{"name":"allow-api","endpoints":[{"host":"api.example.com","port":443}]}`))
	if err != nil {
		t.Fatalf("ParseSDKNetworkPolicyRule: %v", err)
	}
	if rule.Name != "allow-api" || len(rule.Endpoints) != 1 || rule.Endpoints[0].Host != "api.example.com" {
		t.Errorf("rule = %+v", rule)
	}
}
