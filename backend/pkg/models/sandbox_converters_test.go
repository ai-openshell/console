package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/types"
)

// fullSDKSandbox is a sandbox with every field the gateway can report set to a
// value of its own, so a field that stops being copied changes the JSON below.
func fullSDKSandbox() *openshell.Sandbox {
	userNamespaces := true
	gpus := uint32(2)
	exit := int32(137)
	upstreamStatus := uint32(401)
	return &openshell.Sandbox{
		ID:              "sb-id",
		Name:            "agent-1",
		Workspace:       "team-a",
		CreatedAt:       time.UnixMilli(1_700_000_000_000),
		Labels:          map[string]string{"team": "ml"},
		Annotations:     map[string]string{"owner": "ml-team"},
		ResourceVersion: 7,
		CreatedFromWorkloadTemplate: &openshell.SandboxWorkloadTemplateProvenance{
			Name:            "claude-harness",
			ResourceVersion: "12",
		},
		ServiceURLs: map[string]string{"": "https://agent-1.example.test", "web": "https://web.example.test"},
		Spec: openshell.SandboxSpec{
			LogLevel:    "debug",
			Environment: map[string]string{"MODE": "test"},
			Providers:   []string{"claude"},
			GPU:         true,
			GPUCount:    &gpus,
			Command:     []string{"python", "-m", "http.server", "8080"},
			TTY:         true,
			Template: &openshell.SandboxTemplate{
				Image:            "ghcr.io/example/base:1",
				RuntimeClassName: "kata",
				AgentSocket:      "/run/agent-socket-must-not-leak",
				Labels:           map[string]string{"pool": "gpu"},
				Annotations:      map[string]string{"note": "inline"},
				Environment:      map[string]string{"FROM": "template"},
				UserNamespaces:   &userNamespaces,
				Resources:        map[string]any{"limits": map[string]any{"cpu": "500m", "memory": "512Mi"}},
				DriverConfig:     map[string]any{"kubernetes": map[string]any{"pod": map[string]any{"priority": "high"}}},
			},
		},
		Status: openshell.SandboxStatus{
			AgentPod:             "agent-1-pod",
			AgentFd:              "agent-fd-must-not-leak",
			SandboxFd:            "sandbox-fd-must-not-leak",
			Phase:                openshell.SandboxError,
			CurrentPolicyVersion: 3,
			ExitCode:             &exit,
			Conditions: []openshell.SandboxCondition{
				{Type: "Ready", Status: "False", Reason: "MainProcessFailed", Message: "exited", LastTransitionTime: "2026-10-06T10:00:00Z"},
			},
			EndpointStatuses: []openshell.EndpointStatus{{
				EndpointID:     "ep-1",
				Host:           "mcp.example.test",
				Ports:          []uint32{443, 8443},
				Path:           "/**",
				LastResult:     openshell.EndpointPolicyDenied,
				LastReportedAt: "2026-10-06T10:01:00Z",
			}, {
				EndpointID:     "ep-2",
				Host:           "tools.example.test",
				Ports:          []uint32{443},
				Path:           "/mcp",
				LastResult:     openshell.EndpointCredentialUnavailable,
				LastReportedAt: "2026-10-06T10:02:00Z",
				LastFailure: &openshell.EndpointFailure{
					Domain:             openshell.EndpointFailureDomainTokenGrant,
					Code:               "provider_authorization_required",
					RecoveryAction:     openshell.RefreshRecoveryActionReauthorize,
					Provider:           "github",
					CredentialKey:      "GITHUB_TOKEN",
					Stage:              "token_service",
					UpstreamHTTPStatus: &upstreamStatus,
					RequestID:          "6f1c2a3e-0000-4000-8000-000000000001",
				},
			}},
			LastTokenGrantFailure: &openshell.TokenGrantFailureObservation{
				DestinationHost: "api.example.test",
				DestinationPort: 8443,
				Failure: &openshell.EndpointFailure{
					Domain:         openshell.EndpointFailureDomainTokenGrant,
					Code:           "credential_selection_failed",
					RecoveryAction: openshell.RefreshRecoveryActionFixConfiguration,
					Stage:          "gateway_rpc",
					RequestID:      "6f1c2a3e-0000-4000-8000-000000000002",
				},
			},
			ConfigurationAdmission: &types.SandboxConfigurationAdmission{
				State:               types.ConfigurationAdmissionRejected,
				PolicyVersion:       3,
				PolicyHash:          "sha256:abc",
				ConfigRevision:      18446744073709551615,
				ProviderEnvRevision: 9007199254740993,
				Error:               "policy generation rejected",
			},
		},
	}
}

// Every field of a sandbox the browser is to see, as the JSON it is sent. The
// two revisions are 64-bit fingerprints and travel as strings: the second one
// is 2^53+1, the first whole number a browser's JSON parser rounds.
const fullSandboxJSON = `{
  "createdFromWorkloadTemplate": {"name": "claude-harness", "resourceVersion": "12"},
  "serviceUrls": {"": "https://agent-1.example.test", "web": "https://web.example.test"},
  "spec": {
    "logLevel": "debug",
    "environment": {"MODE": "test"},
    "image": "ghcr.io/example/base:1",
    "providers": ["claude"],
    "template": {
      "userNamespaces": true,
      "labels": {"pool": "gpu"},
      "annotations": {"note": "inline"},
      "environment": {"FROM": "template"},
      "resources": {"limits": {"cpu": "500m", "memory": "512Mi"}},
      "driverConfig": {"kubernetes": {"pod": {"priority": "high"}}},
      "runtimeClassName": "kata"
    },
    "gpuCount": 2,
    "command": ["python", "-m", "http.server", "8080"],
    "gpu": true,
    "tty": true
  },
  "status": {
    "exitCode": 137,
    "sandboxName": "agent-1",
    "agentPod": "agent-1-pod",
    "phase": "ERROR",
    "conditions": [
      {"type": "Ready", "status": "False", "reason": "MainProcessFailed", "message": "exited", "lastTransitionTime": "2026-10-06T10:00:00Z"}
    ],
    "configurationAdmission": {
      "state": "REJECTED",
      "policyHash": "sha256:abc",
      "error": "policy generation rejected",
      "configRevision": "18446744073709551615",
      "providerEnvRevision": "9007199254740993",
      "policyVersion": 3
    },
    "endpointStatuses": [
      {"endpointId": "ep-1", "host": "mcp.example.test", "path": "/**", "lastResult": "POLICY_DENIED", "lastReportedAt": "2026-10-06T10:01:00Z", "ports": [443, 8443]},
      {
        "lastFailure": {
          "upstreamHttpStatus": 401,
          "domain": "TOKEN_GRANT",
          "code": "provider_authorization_required",
          "recoveryAction": "REAUTHORIZE",
          "provider": "github",
          "credentialKey": "GITHUB_TOKEN",
          "stage": "token_service",
          "requestId": "6f1c2a3e-0000-4000-8000-000000000001"
        },
        "endpointId": "ep-2", "host": "tools.example.test", "path": "/mcp", "lastResult": "CREDENTIAL_UNAVAILABLE", "lastReportedAt": "2026-10-06T10:02:00Z", "ports": [443]
      }
    ],
    "lastTokenGrantFailure": {
      "failure": {
        "domain": "TOKEN_GRANT",
        "code": "credential_selection_failed",
        "recoveryAction": "FIX_CONFIGURATION",
        "stage": "gateway_rpc",
        "requestId": "6f1c2a3e-0000-4000-8000-000000000002"
      },
      "destinationHost": "api.example.test",
      "destinationPort": 8443
    },
    "currentPolicyVersion": 3
  },
  "metadata": {
    "labels": {"team": "ml"},
    "annotations": {"owner": "ml-team"},
    "id": "sb-id",
    "name": "agent-1",
    "workspace": "team-a",
    "createdAtMs": 1700000000000,
    "resourceVersion": 7
  }
}`

// decodeJSON reads a JSON document into the generic form reflect.DeepEqual can
// compare, keeping numbers as written so that a large one is not rounded on
// the way.
func decodeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("decode: %v; document: %s", err, raw)
	}
	return out
}

// The sandbox response carries everything the gateway reports about a sandbox
// except what a browser has no business with. Comparing the whole document
// makes a field that is dropped, renamed or newly leaked a failure.
func TestFromSDKSandboxCarriesEveryField(t *testing.T) {
	raw, err := json.Marshal(FromSDKSandbox(fullSDKSandbox()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := decodeJSON(t, raw), decodeJSON(t, []byte(fullSandboxJSON)); !reflect.DeepEqual(got, want) {
		t.Errorf("sandbox JSON differs.\n got: %s\nwant: %s", raw, fullSandboxJSON)
	}
	for _, private := range []string{"agent-fd-must-not-leak", "sandbox-fd-must-not-leak", "agent-socket-must-not-leak"} {
		if strings.Contains(string(raw), private) {
			t.Errorf("%s was serialized: %s", private, raw)
		}
	}
}

// A sandbox with none of the optional fields set answers without them: an
// empty template, an absent admission and no GPU request are left out rather
// than sent as zero values.
func TestFromSDKSandboxOmitsWhatIsNotSet(t *testing.T) {
	raw, err := json.Marshal(FromSDKSandbox(&openshell.Sandbox{
		Name: "plain",
		Spec: openshell.SandboxSpec{
			Template: &openshell.SandboxTemplate{Image: "base", AgentSocket: "/run/agent.sock"},
		},
		Status: openshell.SandboxStatus{Phase: openshell.SandboxReady},
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["spec"]["image"] != "base" {
		t.Errorf("spec.image = %v, want base", got["spec"]["image"])
	}
	for _, key := range []string{"template", "command", "tty", "gpu", "gpuCount"} {
		if value, present := got["spec"][key]; present {
			t.Errorf("spec.%s = %v for a sandbox that sets none, want it absent", key, value)
		}
	}
	for _, key := range []string{"endpointStatuses", "configurationAdmission"} {
		if value, present := got["status"][key]; present {
			t.Errorf("status.%s = %v for a sandbox that reports none, want it absent", key, value)
		}
	}
	for _, key := range []string{"createdFromWorkloadTemplate", "serviceUrls"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("%s is present for a sandbox that has none: %s", key, raw)
		}
	}
}

// A GPU request without a count is still a GPU request, and a count alone is
// one in the SDK's own terms.
func TestFromSDKSandboxGPU(t *testing.T) {
	one := uint32(1)
	tests := []struct {
		count     *uint32
		name      string
		gpu       bool
		wantGPU   bool
		wantCount bool
	}{
		{name: "none"},
		{name: "driver default", gpu: true, wantGPU: true},
		{name: "counted", gpu: true, count: &one, wantGPU: true, wantCount: true},
		{name: "count alone", count: &one, wantGPU: true, wantCount: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FromSDKSandbox(&openshell.Sandbox{Spec: openshell.SandboxSpec{GPU: tc.gpu, GPUCount: tc.count}})
			if got.Spec.GPU != tc.wantGPU {
				t.Errorf("gpu = %v, want %v", got.Spec.GPU, tc.wantGPU)
			}
			if (got.Spec.GPUCount != nil) != tc.wantCount {
				t.Errorf("gpuCount = %v, want it present: %v", got.Spec.GPUCount, tc.wantCount)
			}
		})
	}
}

func TestFromSDKSandboxEndpointResults(t *testing.T) {
	tests := []struct {
		result openshell.EndpointResult
		want   string
	}{
		{openshell.EndpointUnspecified, "UNSPECIFIED"},
		{openshell.EndpointNoObservedExchange, "NO_OBSERVED_EXCHANGE"},
		{openshell.EndpointHTTPResponseReceived, "HTTP_RESPONSE_RECEIVED"},
		{openshell.EndpointPolicyDenied, "POLICY_DENIED"},
		{openshell.EndpointCredentialUnavailable, "CREDENTIAL_UNAVAILABLE"},
		{openshell.EndpointTLSFailed, "TLS_FAILED"},
		{openshell.EndpointTransportFailed, "TRANSPORT_FAILED"},
		{openshell.EndpointUpstreamRejected, "UPSTREAM_REJECTED"},
		// A result this build does not know must not read as a success.
		{openshell.EndpointResult("SomethingNew"), "UNSPECIFIED"},
	}
	for _, tc := range tests {
		t.Run(tc.want+" from "+string(tc.result), func(t *testing.T) {
			got := FromSDKSandbox(&openshell.Sandbox{Status: openshell.SandboxStatus{
				EndpointStatuses: []openshell.EndpointStatus{{EndpointID: "ep", LastResult: tc.result}},
			}})
			if len(got.Status.EndpointStatuses) != 1 || got.Status.EndpointStatuses[0].LastResult != tc.want {
				t.Errorf("endpointStatuses = %+v, want one with lastResult %q", got.Status.EndpointStatuses, tc.want)
			}
		})
	}
}

// A failure's domain is named the way the gateway's enum names it, and a
// domain this build does not know reads as unspecified. A sandbox that
// classified no cause, for an endpoint or for a grant, sends none.
func TestFromSDKSandboxEndpointFailures(t *testing.T) {
	domains := map[openshell.EndpointFailureDomain]string{
		openshell.EndpointFailureDomainTokenGrant:       "TOKEN_GRANT",
		openshell.EndpointFailureDomainUnspecified:      "UNSPECIFIED",
		openshell.EndpointFailureDomain("SomethingNew"): "UNSPECIFIED",
	}
	for domain, want := range domains {
		got := FromSDKSandbox(&openshell.Sandbox{Status: openshell.SandboxStatus{
			EndpointStatuses: []openshell.EndpointStatus{{
				EndpointID:  "ep",
				LastResult:  openshell.EndpointCredentialUnavailable,
				LastFailure: &openshell.EndpointFailure{Domain: domain, Code: "some_code"},
			}},
		}})
		failure := got.Status.EndpointStatuses[0].LastFailure
		if failure == nil || failure.Domain != want || failure.Code != "some_code" || failure.RecoveryAction != "" {
			t.Errorf("failure in domain %q = %+v, want domain %s, its code and no recovery action", domain, failure, want)
		}
	}

	got := FromSDKSandbox(&openshell.Sandbox{Status: openshell.SandboxStatus{
		EndpointStatuses: []openshell.EndpointStatus{{EndpointID: "ep", LastResult: openshell.EndpointTLSFailed}},
		LastTokenGrantFailure: &openshell.TokenGrantFailureObservation{
			DestinationHost: "api.example.test",
			DestinationPort: 443,
		},
	}})
	if got.Status.EndpointStatuses[0].LastFailure != nil {
		t.Errorf("endpoint with no classified cause has lastFailure %+v", got.Status.EndpointStatuses[0].LastFailure)
	}
	if grant := got.Status.LastTokenGrantFailure; grant == nil || grant.Failure != nil || grant.DestinationPort != 443 {
		t.Errorf("lastTokenGrantFailure = %+v, want the destination and no failure", grant)
	}
	if none := FromSDKSandbox(&openshell.Sandbox{}); none.Status.LastTokenGrantFailure != nil {
		t.Errorf("sandbox with no refused grant has lastTokenGrantFailure %+v", none.Status.LastTokenGrantFailure)
	}
}

func TestFromSDKSandboxAdmissionStates(t *testing.T) {
	tests := []struct {
		state types.ConfigurationAdmissionState
		want  string
	}{
		{types.ConfigurationAdmissionUnknown, "UNSPECIFIED"},
		{types.ConfigurationAdmissionState(""), "UNSPECIFIED"},
		{types.ConfigurationAdmissionPending, "PENDING"},
		{types.ConfigurationAdmissionAccepted, "ACCEPTED"},
		{types.ConfigurationAdmissionRejected, "REJECTED"},
	}
	for _, tc := range tests {
		t.Run(tc.want+" from "+string(tc.state), func(t *testing.T) {
			got := FromSDKSandbox(&openshell.Sandbox{Status: openshell.SandboxStatus{
				ConfigurationAdmission: &types.SandboxConfigurationAdmission{State: tc.state},
			}})
			if got.Status.ConfigurationAdmission == nil || got.Status.ConfigurationAdmission.State != tc.want {
				t.Errorf("configurationAdmission = %+v, want state %q", got.Status.ConfigurationAdmission, tc.want)
			}
		})
	}
}

// The create form's options land where the gateway reads them: the command and
// the terminal on the spec, the runtime class and the driver config on the
// spec's inline template.
func TestBuildSDKSandboxSpecCarriesCreateOptions(t *testing.T) {
	driverConfig := map[string]any{"kubernetes": map[string]any{"pod": map[string]any{"node_selector": map[string]any{"pool": "gpu"}}}}
	spec, err := BuildSDKSandboxSpec(CreateSandboxRequest{
		Image:            "base",
		LogLevel:         "debug",
		Environment:      map[string]string{"MODE": "test"},
		Command:          []string{"sleep", "infinity"},
		TTY:              true,
		RuntimeClassName: "kata",
		DriverConfig:     driverConfig,
		Policy:           json.RawMessage(`{"version":1}`),
	})
	if err != nil {
		t.Fatalf("BuildSDKSandboxSpec: %v", err)
	}
	if !reflect.DeepEqual(spec.Command, []string{"sleep", "infinity"}) || !spec.TTY {
		t.Errorf("command = %v, tty = %v; want [sleep infinity] with a tty", spec.Command, spec.TTY)
	}
	if spec.LogLevel != "debug" || spec.Environment["MODE"] != "test" {
		t.Errorf("logLevel = %q, environment = %v; want debug and MODE=test", spec.LogLevel, spec.Environment)
	}
	if spec.Template == nil || spec.Template.Image != "base" || spec.Template.RuntimeClassName != "kata" {
		t.Fatalf("template = %+v, want image base with runtime class kata", spec.Template)
	}
	if !reflect.DeepEqual(spec.Template.DriverConfig, driverConfig) {
		t.Errorf("template.driverConfig = %v, want %v", spec.Template.DriverConfig, driverConfig)
	}
}

// A request that sets none of the options sends a spec with no command, no
// terminal and a template that holds only the image.
func TestBuildSDKSandboxSpecWithoutCreateOptions(t *testing.T) {
	spec, err := BuildSDKSandboxSpec(CreateSandboxRequest{Image: "base", Policy: json.RawMessage(`{"version":1}`)})
	if err != nil {
		t.Fatalf("BuildSDKSandboxSpec: %v", err)
	}
	if spec.Command != nil || spec.TTY {
		t.Errorf("command = %v, tty = %v; want neither", spec.Command, spec.TTY)
	}
	if want := (&openshell.SandboxTemplate{Image: "base"}); !reflect.DeepEqual(spec.Template, want) {
		t.Errorf("template = %+v, want %+v", spec.Template, want)
	}
}

// A sandbox made from a template may still be given its command and terminal:
// neither is part of a template. Nothing of the workload is sent, because the
// gateway refuses a workload field beside a template name.
func TestBuildSDKTemplateGovernanceSpecCarriesCommand(t *testing.T) {
	spec, err := BuildSDKTemplateGovernanceSpec(CreateSandboxFromTemplateRequest{
		TemplateName: "claude-harness",
		Providers:    []string{"claude"},
		Command:      []string{"claude", "--print"},
		TTY:          true,
		Policy:       json.RawMessage(`{"version":1}`),
	})
	if err != nil {
		t.Fatalf("BuildSDKTemplateGovernanceSpec: %v", err)
	}
	if !reflect.DeepEqual(spec.Command, []string{"claude", "--print"}) || !spec.TTY {
		t.Errorf("command = %v, tty = %v; want [claude --print] with a tty", spec.Command, spec.TTY)
	}
	if !reflect.DeepEqual(spec.Providers, []string{"claude"}) || spec.Policy == nil {
		t.Errorf("providers = %v, policy = %v; want [claude] and a policy", spec.Providers, spec.Policy)
	}
	if spec.Template != nil || spec.LogLevel != "" || spec.Environment != nil || spec.GPU || spec.GPUCount != nil {
		t.Errorf("spec carries workload fields the gateway refuses beside a template: %+v", spec)
	}
}

func TestBuildSDKCreateOptions(t *testing.T) {
	labels := map[string]string{"team": "ml"}
	annotations := map[string]string{"owner": "ml-team"}
	exposures := []ServiceExposure{{TargetPort: 8080}, {Service: "web", TargetPort: 3000}}
	wantExposures := []openshell.ServiceExposure{{TargetPort: 8080}, {Service: "web", TargetPort: 3000}}

	if got := BuildSDKCreateOptions(nil, nil, nil); got != nil {
		t.Errorf("options for a create with no labels, annotations or services = %d, want none", len(got))
	}
	if got := BuildSDKCreateOptions(map[string]string{}, map[string]string{}, []ServiceExposure{}); got != nil {
		t.Errorf("options for empty labels, annotations and services = %d, want none", len(got))
	}

	// The options are functions, so they are read back the way the SDK reads them.
	tests := []struct {
		name            string
		labels          map[string]string
		annotations     map[string]string
		exposures       []ServiceExposure
		wantLabels      map[string]string
		wantAnnotations map[string]string
		wantExposures   []openshell.ServiceExposure
	}{
		{name: "labels alone", labels: labels, wantLabels: labels},
		{name: "annotations alone", annotations: annotations, wantAnnotations: annotations},
		{name: "services alone", exposures: exposures, wantExposures: wantExposures},
		{
			name: "all three", labels: labels, annotations: annotations, exposures: exposures,
			wantLabels: labels, wantAnnotations: annotations, wantExposures: wantExposures,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := types.ApplyCreateOptions(BuildSDKCreateOptions(tc.labels, tc.annotations, tc.exposures))
			if !reflect.DeepEqual(cfg.Labels(), tc.wantLabels) {
				t.Errorf("labels = %v, want %v", cfg.Labels(), tc.wantLabels)
			}
			if !reflect.DeepEqual(cfg.Annotations(), tc.wantAnnotations) {
				t.Errorf("annotations = %v, want %v", cfg.Annotations(), tc.wantAnnotations)
			}
			if !reflect.DeepEqual(cfg.ServiceExposures(), tc.wantExposures) {
				t.Errorf("service exposures = %+v, want %+v", cfg.ServiceExposures(), tc.wantExposures)
			}
		})
	}
}

// A sandbox's settings come back sorted, each in the JSON type the gateway has
// it in and with the scope it was resolved from. A setting set at neither
// scope is listed without a value, and its scope says so.
func TestFromSDKSandboxSettings(t *testing.T) {
	got := FromSDKSandboxSettings(&openshell.SandboxConfig{
		Policy:                      &openshell.SandboxPolicy{Version: 1},
		PolicyVersion:               4,
		PolicyHash:                  "sha256:abc",
		ConfigRevision:              18446744073709551615,
		PolicySource:                openshell.PolicySourceGlobal,
		GlobalPolicyVersion:         2,
		ProviderEnvRevision:         9007199254740993,
		PolicyValidationFailureMode: "fail_closed",
		Settings: map[string]openshell.EffectiveSetting{
			"proposal_approval_mode": {
				Scope: openshell.SettingScopeSandbox,
				Value: openshell.SettingValue{Type: openshell.SettingValueString, StringVal: "auto"},
			},
			"ocsf_json_enabled": {
				Scope: openshell.SettingScopeGlobal,
				Value: openshell.SettingValue{Type: openshell.SettingValueBool, BoolVal: false},
			},
			"retries": {
				Scope: openshell.SettingScopeSandbox,
				Value: openshell.SettingValue{Type: openshell.SettingValueInt, IntVal: 3},
			},
			"agent_policy_proposals_enabled": {},
		},
	})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{
	  "policySource": "GLOBAL",
	  "policyHash": "sha256:abc",
	  "policyValidationFailureMode": "fail_closed",
	  "settings": [
	    {"key": "agent_policy_proposals_enabled", "scope": "UNSPECIFIED"},
	    {"key": "ocsf_json_enabled", "value": false, "scope": "GLOBAL"},
	    {"key": "proposal_approval_mode", "value": "auto", "scope": "SANDBOX"},
	    {"key": "retries", "value": 3, "scope": "SANDBOX"}
	  ],
	  "configRevision": "18446744073709551615",
	  "providerEnvRevision": "9007199254740993",
	  "policyVersion": 4,
	  "globalPolicyVersion": 2
	}`
	if !reflect.DeepEqual(decodeJSON(t, raw), decodeJSON(t, []byte(want))) {
		t.Errorf("sandbox settings JSON differs.\n got: %s\nwant: %s", raw, want)
	}
	// The key order is part of the contract, which a comparison of maps hides.
	keys := make([]string, 0, len(got.Settings))
	for _, entry := range got.Settings {
		keys = append(keys, entry.Key)
	}
	if !reflect.DeepEqual(keys, []string{"agent_policy_proposals_enabled", "ocsf_json_enabled", "proposal_approval_mode", "retries"}) {
		t.Errorf("settings are not sorted by key: %v", keys)
	}
}

func TestFromSDKSandboxSettingsSources(t *testing.T) {
	tests := []struct {
		source openshell.PolicySource
		want   string
	}{
		{openshell.PolicySourceSandbox, "SANDBOX"},
		{openshell.PolicySourceGlobal, "GLOBAL"},
		{openshell.PolicySourceUnspecified, "UNSPECIFIED"},
	}
	for _, tc := range tests {
		if got := FromSDKSandboxSettings(&openshell.SandboxConfig{PolicySource: tc.source}); got.PolicySource != tc.want {
			t.Errorf("policySource for %q = %q, want %q", tc.source, got.PolicySource, tc.want)
		}
	}
}

// With no settings the list is an empty array, never null: the tab reads it
// without a guard.
func TestFromSDKSandboxSettingsEmpty(t *testing.T) {
	for name, config := range map[string]*openshell.SandboxConfig{"nil": nil, "empty": {}} {
		raw, err := json.Marshal(FromSDKSandboxSettings(config))
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if !strings.Contains(string(raw), `"settings":[]`) {
			t.Errorf("%s config: settings must serialize as [], got %s", name, raw)
		}
		if !strings.Contains(string(raw), `"policySource":"UNSPECIFIED"`) {
			t.Errorf("%s config: policySource must be UNSPECIFIED, got %s", name, raw)
		}
	}
}
