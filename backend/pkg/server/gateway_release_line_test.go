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

// getGateway drives GET /api/v1/gateway through the real router of an App
// built the way main builds it, against a gateway reporting the given version.
func getGateway(t *testing.T, reported string, configure func(*App)) map[string]any {
	t.Helper()
	sdk := fake.NewClient(fake.WithGatewayInfo(&openshell.GatewayInfo{
		Status:  openshell.ServiceStatusHealthy,
		Version: reported,
	}))
	app := NewApp(sdk, nil, auth.New(auth.Config{Disabled: true}), "", models.AuthConfigResponse{AuthDisabled: true})
	if configure != nil {
		configure(app)
	}

	recorder := httptest.NewRecorder()
	app.Routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/gateway", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/gateway = %d; body: %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body: %s", err, recorder.Body.String())
	}
	return body
}

// otherLine is a release line for a test to hand an App: one that is certainly
// not the line compiled into the build, whatever that becomes, so that a test
// passing with it proves the override reached the route.
func otherLine(t *testing.T) models.GatewayReleaseLine {
	t.Helper()
	line, err := models.ParseGatewayReleaseLine("9.9")
	if err != nil {
		t.Fatalf("ParseGatewayReleaseLine: %v", err)
	}
	return line
}

// SetGatewayReleaseLine is the only way an override reaches the route, so
// prove the wiring end to end rather than only the handler in isolation.
func TestSetGatewayReleaseLine_ReachesGatewayRoute(t *testing.T) {
	body := getGateway(t, "0.0.116", func(app *App) { app.SetGatewayReleaseLine(otherLine(t)) })

	compatibility, ok := body["compatibility"].(map[string]any)
	if !ok {
		t.Fatalf("compatibility missing from %v", body)
	}
	if len(compatibility) != 2 || compatibility["status"] != "unsupported" || compatibility["supportedLine"] != "9.9" {
		t.Errorf("compatibility = %v, want unsupported on line 9.9 and nothing else", compatibility)
	}
	if body["gatewayVersion"] != "0.0.116" {
		t.Errorf("gatewayVersion = %v, want 0.0.116", body["gatewayVersion"])
	}
}

// adminOnlyGatewayInfo is an SDK client whose gateway refuses GetGatewayInfo,
// the way a real gateway refuses it to a caller without the platform-admin
// role. Every other call, the health check included, goes to the wrapped fake.
type adminOnlyGatewayInfo struct {
	openshell.ClientInterface
}

func (c adminOnlyGatewayInfo) Health() openshell.HealthInterface {
	return refusedGatewayInfo{c.ClientInterface.Health()}
}

type refusedGatewayInfo struct {
	openshell.HealthInterface
}

func (refusedGatewayInfo) GetGatewayInfo(context.Context) (*openshell.GatewayInfo, error) {
	return nil, &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "role 'openshell-admin' required"}
}

// A signed-in user who is not a platform admin: the gateway refuses the
// gateway-info call, and the verdict must reach them anyway. Driven through
// the real router with auth ON, the way a deployment behind a proxy runs.
func TestGatewayCompatibilityRoute_ServesUsersWithoutTheAdminRole(t *testing.T) {
	sdk := adminOnlyGatewayInfo{fake.NewClient(fake.WithHealthResult(&openshell.HealthResult{
		Healthy: true,
		Version: "0.0.116",
	}))}
	app := NewApp(sdk, nil, auth.New(auth.Config{}), "", models.AuthConfigResponse{})
	app.SetGatewayReleaseLine(otherLine(t))
	router := app.Routes()

	get := func(path, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			request.Header.Set("x-forwarded-access-token", token)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	// The gateway-info route relays the refusal.
	if refused := get("/api/v1/gateway", "user-token"); refused.Code != http.StatusForbidden {
		t.Fatalf("GET /api/v1/gateway = %d, want 403; body: %s", refused.Code, refused.Body.String())
	}

	// The verdict route answers the same user.
	verdict := get("/api/v1/gateway/compatibility", "user-token")
	if verdict.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/gateway/compatibility = %d, want 200; body: %s", verdict.Code, verdict.Body.String())
	}
	var body struct {
		Healthy        *bool  `json:"healthy"`
		GatewayVersion string `json:"gatewayVersion"`
		Compatibility  struct {
			Status        string `json:"status"`
			SupportedLine string `json:"supportedLine"`
		} `json:"compatibility"`
	}
	if err := json.Unmarshal(verdict.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body: %s", err, verdict.Body.String())
	}
	if body.GatewayVersion != "0.0.116" || body.Compatibility.Status != "unsupported" || body.Compatibility.SupportedLine != "9.9" {
		t.Errorf("body = %s, want gateway 0.0.116 unsupported on line 9.9", verdict.Body.String())
	}
	// And so does the gateway's health, which GET /gateway would have told
	// an admin.
	if body.Healthy == nil || !*body.Healthy {
		t.Errorf("body = %s, want healthy true for a user without the admin role", verdict.Body.String())
	}

	// "Every signed-in user", not everyone: the route is behind the same
	// bearer check as the rest of the API.
	if anonymous := get("/api/v1/gateway/compatibility", ""); anonymous.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/gateway/compatibility without a token = %d, want 401", anonymous.Code)
	}
}

// An App nobody gave a line to judges gateways against the line compiled into
// the build, the line a BFF started with no environment hands its App. The
// verdict route answers with it...
func TestGatewayCompatibilityRoute_UsesTheBuiltInLine(t *testing.T) {
	builtIn := models.BuiltInGatewayReleaseLine
	sdk := fake.NewClient(fake.WithHealthResult(&openshell.HealthResult{Healthy: true, Version: builtIn + ".0"}))
	app := NewApp(sdk, nil, auth.New(auth.Config{Disabled: true}), "", models.AuthConfigResponse{AuthDisabled: true})

	recorder := httptest.NewRecorder()
	app.Routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/gateway/compatibility", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/gateway/compatibility = %d; body: %s", recorder.Code, recorder.Body.String())
	}
	want := `{"gatewayVersion":"` + builtIn + `.0","healthy":true,"compatibility":{"status":"supported","supportedLine":"` + builtIn + `"}}`
	if got := strings.TrimSpace(recorder.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// ...and GET /gateway gives the same answer.
func TestNewApp_UsesTheBuiltInLine(t *testing.T) {
	builtIn := models.BuiltInGatewayReleaseLine
	body := getGateway(t, builtIn+".0", nil)

	compatibility, ok := body["compatibility"].(map[string]any)
	if !ok {
		t.Fatalf("compatibility missing from %v", body)
	}
	if len(compatibility) != 2 || compatibility["status"] != "supported" || compatibility["supportedLine"] != builtIn {
		t.Errorf("compatibility = %v, want supported on line %s and nothing else", compatibility, builtIn)
	}
}

// An App whose line was replaced by one that could not be read reports
// "unknown" and names no line. It must never fall back to another.
func TestSetGatewayReleaseLine_ZeroValueReportsUnknown(t *testing.T) {
	body := getGateway(t, "0.0.116", func(app *App) { app.SetGatewayReleaseLine(models.GatewayReleaseLine{}) })

	compatibility, ok := body["compatibility"].(map[string]any)
	if !ok {
		t.Fatalf("compatibility missing from %v", body)
	}
	if len(compatibility) != 1 || compatibility["status"] != "unknown" {
		t.Errorf("compatibility = %v, want only status unknown", compatibility)
	}
}
