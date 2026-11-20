package services

import (
	"context"
	"testing"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/fake"
	"github.com/ai-openshell/sdk-go/proto/openshellv1"
	"github.com/ai-openshell/sdk-go/proto/optionsv1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The default service is what gives every signed-in user a verdict, so it must
// keep offering the role-free version source.
var _ GatewayVersionReader = (*GatewayService)(nil)

// authorizationRule returns the authorization the gateway's proto declares for
// one OpenShell RPC, as compiled into the SDK. It is the same
// descriptor option the gateway builds its own authorization table from.
func authorizationRule(t *testing.T, method string) *optionsv1.AuthorizationRule {
	t.Helper()
	service := openshellv1.File_openshell_proto.Services().ByName("OpenShell")
	if service == nil {
		t.Fatal("the SDK's proto has no OpenShell service")
	}
	descriptor := service.Methods().ByName(protoreflect.Name(method))
	if descriptor == nil {
		t.Fatalf("the SDK's proto has no OpenShell.%s RPC", method)
	}
	rule, ok := proto.GetExtension(descriptor.Options(), optionsv1.E_Authorization).(*optionsv1.AuthorizationRule)
	if !ok || rule == nil {
		t.Fatalf("OpenShell.%s declares no authorization rule", method)
	}
	return rule
}

// GET /gateway/compatibility is served to every signed-in user on the strength
// of two facts about the gateway's API, both read here from the SDK this
// build is pinned to rather than taken on trust:
//
//  1. the health check asks for no token and no role, and
//  2. its response carries the gateway's version.
//
// If an SDK bump changes either, this fails and the route's data source has to
// be rethought — before users without the admin role silently lose the notice.
func TestGatewayVersionSourceNeedsNoRole(t *testing.T) {
	health := authorizationRule(t, "Health")
	if health.GetAuthMode() != "unauthenticated" || health.GetGlobalRole() != "" || health.GetWorkspaceRole() != "" {
		t.Errorf("OpenShell.Health authorization = {auth_mode: %q, global_role: %q, workspace_role: %q}, want unauthenticated with no role",
			health.GetAuthMode(), health.GetGlobalRole(), health.GetWorkspaceRole())
	}

	response := (&openshellv1.HealthResponse{}).ProtoReflect().Descriptor()
	version := response.Fields().ByName("version")
	if version == nil || version.Kind() != protoreflect.StringKind {
		t.Errorf("HealthResponse has no string field named version: %v", version)
	}

	// Why the verdict cannot simply ride on GET /gateway: that RPC is for
	// platform admins. Logged rather than asserted — upstream relaxing it
	// would make the separate route unnecessary, not wrong.
	info := authorizationRule(t, "GetGatewayInfo")
	t.Logf("OpenShell.Health: auth_mode=%q; OpenShell.GetGatewayInfo: auth_mode=%q global_role=%q scope=%q",
		health.GetAuthMode(), info.GetAuthMode(), info.GetGlobalRole(), info.GetScope())
}

func TestGetGatewayVersion(t *testing.T) {
	sdk := fake.NewClient(fake.WithHealthResult(&openshell.HealthResult{
		Healthy: true,
		Version: "0.1.3-dev.84+ge7fdd6bee",
	}))
	service := NewGatewayService(sdk)

	// Exactly as reported: comparing it is the handler's job.
	version, err := service.GetGatewayVersion(context.Background())
	if err != nil {
		t.Fatalf("GetGatewayVersion: %v", err)
	}
	if version != "0.1.3-dev.84+ge7fdd6bee" {
		t.Errorf("version = %q, want the health check's version unchanged", version)
	}

	// A failed health check is an error, never an empty version that would
	// read as "the gateway reported nothing".
	if err := sdk.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if version, err := service.GetGatewayVersion(context.Background()); err == nil {
		t.Errorf("GetGatewayVersion on an unreachable gateway = %q, want an error", version)
	}
}

// The default service is also what gives every signed-in user the gateway's
// health, so it must keep offering the role-free source for that too.
var _ GatewayHealthReader = (*GatewayService)(nil)

// GetGatewayHealth hands on both halves of the health check's answer as the
// SDK reports them, and an error when the gateway did not answer.
func TestGetGatewayHealth(t *testing.T) {
	tests := []struct {
		name   string
		result openshell.HealthResult
	}{
		{name: "healthy", result: openshell.HealthResult{Healthy: true, Version: "0.1.2"}},
		{name: "not healthy", result: openshell.HealthResult{Healthy: false, Version: "0.1.2"}},
		{name: "healthy without a version", result: openshell.HealthResult{Healthy: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.result
			sdk := fake.NewClient(fake.WithHealthResult(&result))
			service := NewGatewayService(sdk)

			health, err := service.GetGatewayHealth(context.Background())
			if err != nil {
				t.Fatalf("GetGatewayHealth: %v", err)
			}
			if health == nil {
				t.Fatal("GetGatewayHealth = nil, want the health check's answer")
			}
			if health.Healthy != tc.result.Healthy || health.Version != tc.result.Version {
				t.Errorf("health = %+v, want %+v", *health, tc.result)
			}

			// A gateway that does not answer is an error, never a gateway
			// that answered "not healthy".
			if err := sdk.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if health, err := service.GetGatewayHealth(context.Background()); err == nil {
				t.Errorf("GetGatewayHealth on an unreachable gateway = %+v, want an error", health)
			}
		})
	}
}

// The health check is where the health comes from for the same reason it is
// where the version comes from: it asks for no role. What it carries is a
// status, read here from the SDK this build is pinned to.
func TestGatewayHealthSourceCarriesAStatus(t *testing.T) {
	response := (&openshellv1.HealthResponse{}).ProtoReflect().Descriptor()
	status := response.Fields().ByName("status")
	if status == nil || status.Kind() != protoreflect.EnumKind {
		t.Fatalf("HealthResponse has no enum field named status: %v", status)
	}
	// The four values the gateway can answer with. The SDK's HealthResult
	// keeps only whether it was HEALTHY, which is why the BFF reports a
	// boolean and not a status.
	var names []string
	values := status.Enum().Values()
	for i := 0; i < values.Len(); i++ {
		names = append(names, string(values.Get(i).Name()))
	}
	want := []string{"SERVICE_STATUS_UNSPECIFIED", "SERVICE_STATUS_HEALTHY", "SERVICE_STATUS_DEGRADED", "SERVICE_STATUS_UNHEALTHY"}
	if len(names) != len(want) {
		t.Fatalf("ServiceStatus values = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("ServiceStatus values = %v, want %v", names, want)
			break
		}
	}
}
