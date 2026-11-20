package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

// effectivePolicyJSON returns the DTO for config and the JSON object the
// frontend receives for it.
func effectivePolicyJSON(t *testing.T, config *openshell.SandboxConfig) (EffectivePolicy, map[string]any) {
	t.Helper()
	got := FromSDKEffectivePolicy(config)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got, raw
}

func composedPolicy() *openshell.SandboxPolicy {
	return &openshell.SandboxPolicy{
		Version: 1,
		NetworkPolicies: map[string]openshell.NetworkPolicyRule{
			"gh": {Name: "gh", Endpoints: []openshell.PolicyNetworkEndpoint{{Host: "api.github.com", Port: 443}}},
			"_provider_claude": {Name: "_provider_claude", Endpoints: []openshell.PolicyNetworkEndpoint{
				{Host: "api.anthropic.com", Port: 443, ProviderCredentialed: true},
			}},
		},
	}
}

func TestFromSDKEffectivePolicySandboxSource(t *testing.T) {
	got, raw := effectivePolicyJSON(t, &openshell.SandboxConfig{
		Policy:                      composedPolicy(),
		PolicyVersion:               4,
		PolicyHash:                  "abc123",
		PolicySource:                openshell.PolicySourceSandbox,
		PolicyValidationFailureMode: "retain_last_valid",
	})

	want := EffectivePolicy{
		Policy:                      got.Policy,
		Version:                     4,
		PolicyHash:                  "abc123",
		PolicySource:                "SANDBOX",
		PolicyValidationFailureMode: "retain_last_valid",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if value, present := raw["globalPolicyVersion"]; present {
		t.Errorf("globalPolicyVersion = %v for a policy the sandbox owns, want the key absent", value)
	}
	// The rule the gateway composes in for an attached provider, and the
	// marker that tells it from an authored one, both reach the browser.
	policy := string(got.Policy)
	for _, part := range []string{`"_provider_claude"`, `"providerCredentialed":true`, `"gh"`} {
		if !strings.Contains(policy, part) {
			t.Errorf("effective policy lacks %s: %s", part, policy)
		}
	}
}

func TestFromSDKEffectivePolicyGlobalSource(t *testing.T) {
	got, raw := effectivePolicyJSON(t, &openshell.SandboxConfig{
		Policy:              composedPolicy(),
		PolicyVersion:       4,
		PolicyHash:          "def456",
		PolicySource:        openshell.PolicySourceGlobal,
		GlobalPolicyVersion: 7,
	})
	if got.PolicySource != "GLOBAL" || got.GlobalPolicyVersion != 7 || got.Version != 4 {
		t.Errorf("got source %q, global v%d, sandbox v%d; want GLOBAL, 7, 4", got.PolicySource, got.GlobalPolicyVersion, got.Version)
	}
	if raw["globalPolicyVersion"] != float64(7) || raw["policySource"] != "GLOBAL" {
		t.Errorf("JSON carries globalPolicyVersion = %v, policySource = %v; want 7 and GLOBAL", raw["globalPolicyVersion"], raw["policySource"])
	}
}

func TestFromSDKEffectivePolicyWithoutAPolicy(t *testing.T) {
	got, raw := effectivePolicyJSON(t, &openshell.SandboxConfig{PolicySource: openshell.PolicySourceSandbox})
	if got.Policy != nil {
		t.Errorf("policy = %s, want it absent", got.Policy)
	}
	if _, present := raw["policy"]; present {
		t.Error("the policy key is present in JSON for a sandbox without one")
	}
	// The UI reads both without a null check.
	if raw["version"] != float64(0) || raw["policySource"] != "SANDBOX" {
		t.Errorf("version = %v, policySource = %v; want 0 and SANDBOX", raw["version"], raw["policySource"])
	}

	if nilConfig := FromSDKEffectivePolicy(nil); !reflect.DeepEqual(nilConfig, EffectivePolicy{}) {
		t.Errorf("a nil config gives %+v, want the zero value", nilConfig)
	}
}

// The draft chunk fields a reviewer is shown by `openshell draft get` and the
// TUI: where the proposal is in its life and how often, and when, the denial
// behind it was seen.
func TestFromSDKDraftPolicyChunkFields(t *testing.T) {
	created := time.UnixMilli(1_700_000_000_000)
	first := time.UnixMilli(1_700_000_100_000)
	last := time.UnixMilli(1_700_000_200_000)
	decided := time.UnixMilli(1_700_000_300_000)
	analyzed := time.UnixMilli(1_700_000_400_000)

	got := FromSDKDraftPolicy(&openshell.DraftPolicy{
		RollingSummary: "two hosts keep being denied",
		DraftVersion:   9,
		LastAnalyzedAt: analyzed,
		Chunks: []openshell.PolicyChunk{{
			ID:                "c2",
			Status:            "rejected",
			RuleName:          "allow_api_github_com_443",
			Stage:             "refined",
			SupersedesChunkID: "c1",
			DenialSummaryIDs:  []string{"d1", "d2"},
			HitCount:          12,
			CreatedAt:         created,
			DecidedAt:         decided,
			FirstSeen:         first,
			LastSeen:          last,
			RejectionReason:   "too broad",
			ApplicationError:  "conflicts with rule gh",
			ReviewToken:       "tok",
		}, {
			ID:     "c3",
			Status: "pending",
		}},
	})

	wantDraft := DraftPolicy{
		RollingSummary:   "two hosts keep being denied",
		DraftVersion:     9,
		LastAnalyzedAtMs: analyzed.UnixMilli(),
		Chunks: []PolicyChunk{{
			ID:                "c2",
			Status:            "rejected",
			RuleName:          "allow_api_github_com_443",
			Stage:             "refined",
			SupersedesChunkID: "c1",
			DenialSummaryIDs:  []string{"d1", "d2"},
			HitCount:          12,
			CreatedAtMs:       created.UnixMilli(),
			DecidedAtMs:       decided.UnixMilli(),
			FirstSeenMs:       first.UnixMilli(),
			LastSeenMs:        last.UnixMilli(),
			RejectionReason:   "too broad",
			ApplicationError:  "conflicts with rule gh",
			ReviewToken:       "tok",
		}, {
			ID:     "c3",
			Status: "pending",
		}},
	}
	if !reflect.DeepEqual(got, wantDraft) {
		t.Errorf("draft differs.\ngot:  %+v\nwant: %+v", got, wantDraft)
	}

	// A chunk the gateway sent none of these for says nothing about them,
	// rather than a stage of "" or a first sighting in 1970.
	encoded, err := json.Marshal(got.Chunks[1])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var bare map[string]any
	if err := json.Unmarshal(encoded, &bare); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"stage", "supersedesChunkId", "denialSummaryIds", "firstSeenMs", "lastSeenMs"} {
		if value, present := bare[key]; present {
			t.Errorf("%s = %v on a chunk that has none, want the key absent", key, value)
		}
	}
}
