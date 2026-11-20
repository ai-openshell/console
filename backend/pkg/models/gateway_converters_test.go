package models

import (
	"encoding/json"
	"reflect"
	"testing"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

// Every field of a negotiated extension reaches the frontend, and the kind is
// spelled the way the BFF spells every other enum.
func TestFromSDKGatewayInfoExtensions(t *testing.T) {
	got := FromSDKGatewayInfo(&openshell.GatewayInfo{
		Status:  openshell.ServiceStatusHealthy,
		Version: "0.1.2",
		Extensions: []openshell.ExtensionInfo{
			{
				Kind:                  openshell.ExtensionKindComputeDriver,
				ConfiguredName:        "podman",
				ImplementationName:    "example-driver",
				ImplementationVersion: "0.1.2",
				ProtocolMajor:         1,
				ProtocolMinor:         3,
				SupportedCapabilities: []string{"capability-a", "capability-b"},
				RequiredCapabilities:  []string{"gateway-capability"},
			},
			{Kind: openshell.ExtensionKindCredentialDriver, ConfiguredName: "vault"},
		},
	})

	want := []GatewayExtension{
		{
			Kind:                  "COMPUTE_DRIVER",
			ConfiguredName:        "podman",
			ImplementationName:    "example-driver",
			ImplementationVersion: "0.1.2",
			ProtocolMajor:         1,
			ProtocolMinor:         3,
			SupportedCapabilities: []string{"capability-a", "capability-b"},
			RequiredCapabilities:  []string{"gateway-capability"},
		},
		{Kind: "CREDENTIAL_DRIVER", ConfiguredName: "vault"},
	}
	if !reflect.DeepEqual(got.Extensions, want) {
		t.Errorf("extensions = %+v\nwant %+v", got.Extensions, want)
	}
}

func TestFromSDKGatewayInfoExtensionKinds(t *testing.T) {
	kinds := map[openshell.ExtensionKind]string{
		openshell.ExtensionKindComputeDriver:        "COMPUTE_DRIVER",
		openshell.ExtensionKindCredentialDriver:     "CREDENTIAL_DRIVER",
		openshell.ExtensionKindGatewayInterceptor:   "GATEWAY_INTERCEPTOR",
		openshell.ExtensionKindSupervisorMiddleware: "SUPERVISOR_MIDDLEWARE",
		openshell.ExtensionKindUnknown:              "UNSPECIFIED",
		// A kind a newer gateway adds and this SDK cannot name.
		openshell.ExtensionKind("SomethingNew"): "UNSPECIFIED",
	}
	for kind, want := range kinds {
		got := FromSDKGatewayInfo(&openshell.GatewayInfo{Extensions: []openshell.ExtensionInfo{{Kind: kind}}})
		if len(got.Extensions) != 1 || got.Extensions[0].Kind != want {
			t.Errorf("kind %q = %+v, want %q", kind, got.Extensions, want)
		}
	}
}

// A gateway that reports no extensions, and a missing answer, both serialize
// extensions as an empty array: the frontend maps over it without a guard.
func TestFromSDKGatewayInfoWithoutExtensions(t *testing.T) {
	for name, info := range map[string]*openshell.GatewayInfo{
		"none reported": {Status: openshell.ServiceStatusHealthy, Version: "0.1.0"},
		"nil info":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(FromSDKGatewayInfo(info))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := string(body["extensions"]); got != "[]" {
				t.Errorf("extensions = %s, want []; body: %s", got, raw)
			}
			if got := string(body["computeDrivers"]); got != "[]" {
				t.Errorf("computeDrivers = %s, want []; body: %s", got, raw)
			}
		})
	}
}

func TestFromSDKServiceEndpoint(t *testing.T) {
	got := FromSDKServiceEndpoint(&openshell.ServiceEndpoint{
		ID:         "ep-1",
		SandboxID:  "sb-1",
		Sandbox:    "agent",
		Name:       "web",
		TargetPort: 8080,
		Domain:     true,
		URL:        "https://team-a--agent--web.example/",
		Workspace:  "team-a",
	})
	want := ServiceEndpoint{
		ID:          "ep-1",
		Workspace:   "team-a",
		SandboxID:   "sb-1",
		SandboxName: "agent",
		ServiceName: "web",
		URL:         "https://team-a--agent--web.example/",
		TargetPort:  8080,
		Domain:      true,
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if got := FromSDKServiceEndpoint(nil); got != (ServiceEndpoint{}) {
		t.Errorf("nil endpoint = %+v, want the zero value", got)
	}
}

// The unnamed endpoint keeps its empty name in JSON, where the frontend tells
// it apart from a named one; the ids and the workspace are left out only when
// the gateway did not send them.
func TestServiceEndpointJSON(t *testing.T) {
	raw, err := json.Marshal(FromSDKServiceEndpoint(&openshell.ServiceEndpoint{Sandbox: "agent", TargetPort: 3000}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"sandboxName":"agent","serviceName":"","targetPort":3000,"domain":false}`; string(raw) != want {
		t.Errorf("json = %s\nwant %s", raw, want)
	}
}

// whoami shows what `openshell whoami` prints: subject, name, provider, roles
// and scopes.
func TestFromSDKCurrentUser(t *testing.T) {
	got := FromSDKCurrentUser(&openshell.CurrentUser{
		Subject:          "f3b1c2",
		DisplayName:      "Ada",
		Roles:            []string{"openshell-admin"},
		Scopes:           []string{"openid", "sandbox:read"},
		IdentityProvider: "oidc",
	})
	want := CurrentUser{
		Subject:          "f3b1c2",
		DisplayName:      "Ada",
		Roles:            []string{"openshell-admin"},
		Scopes:           []string{"openid", "sandbox:read"},
		IdentityProvider: "oidc",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if got := FromSDKCurrentUser(nil); !reflect.DeepEqual(got, CurrentUser{}) {
		t.Errorf("nil user = %+v, want the zero value", got)
	}
}
