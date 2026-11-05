package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

func TestGetGateway(t *testing.T) {
	sdk := &mockSDK{}
	sdk.health.getGatewayInfoFn = func(_ context.Context) (*openshell.GatewayInfo, error) {
		return &openshell.GatewayInfo{
			Status:  openshell.ServiceStatusHealthy,
			Version: "0.0.92",
			ComputeDrivers: []openshell.ComputeDriverInfo{
				{Name: "podman", DriverName: "podman", DriverVersion: "5.0"},
			},
		}, nil
	}
	handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
	req := httptest.NewRequest(http.MethodGet, "/gateway", nil)
	w := httptest.NewRecorder()
	handler.GetGateway(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "HEALTHY" || body["gatewayVersion"] != "0.0.92" {
		t.Errorf("body = %v", body)
	}
	drivers, _ := body["computeDrivers"].([]any)
	if len(drivers) != 1 {
		t.Fatalf("got %d drivers, want 1", len(drivers))
	}
}

// GET /gateway carries the extensions the gateway negotiated, which is what
// `openshell gateway info` lists under "Extensions". A gateway that reports
// none still answers with an array.
func TestGetGatewayExtensions(t *testing.T) {
	tests := []struct {
		name       string
		extensions []openshell.ExtensionInfo
		want       []any
	}{
		{
			name: "negotiated extensions",
			extensions: []openshell.ExtensionInfo{
				{
					Kind:                  openshell.ExtensionKindComputeDriver,
					ConfiguredName:        "podman",
					ImplementationName:    "example-driver",
					ImplementationVersion: "0.1.2",
					ProtocolMajor:         1,
					ProtocolMinor:         2,
					SupportedCapabilities: []string{"capability-a"},
					RequiredCapabilities:  []string{"gateway-capability"},
				},
				{Kind: openshell.ExtensionKindGatewayInterceptor, ConfiguredName: "audit"},
			},
			want: []any{
				map[string]any{
					"kind":                  "COMPUTE_DRIVER",
					"configuredName":        "podman",
					"implementationName":    "example-driver",
					"implementationVersion": "0.1.2",
					"protocolMajor":         float64(1),
					"protocolMinor":         float64(2),
					"supportedCapabilities": []any{"capability-a"},
					"requiredCapabilities":  []any{"gateway-capability"},
				},
				map[string]any{
					"kind":           "GATEWAY_INTERCEPTOR",
					"configuredName": "audit",
					"protocolMajor":  float64(0),
					"protocolMinor":  float64(0),
				},
			},
		},
		{name: "none", want: []any{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.health.getGatewayInfoFn = func(_ context.Context) (*openshell.GatewayInfo, error) {
				return &openshell.GatewayInfo{Status: openshell.ServiceStatusHealthy, Version: "0.1.2", Extensions: tc.extensions}, nil
			}
			handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
			w := httptest.NewRecorder()
			handler.GetGateway(w, httptest.NewRequest(http.MethodGet, "/gateway", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			got, isArray := body["extensions"].([]any)
			if !isArray {
				t.Fatalf("extensions = %v, want a JSON array", body["extensions"])
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("extensions = %v\nwant %v", got, tc.want)
			}
		})
	}
}

// GET /auth/whoami carries what `openshell whoami` prints: the subject, the
// name, the provider that validated the identity, the roles and the scopes.
func TestGetWhoAmIIdentity(t *testing.T) {
	sdk := &mockSDK{}
	sdk.health.getCurrentUserFn = func(_ context.Context) (*openshell.CurrentUser, error) {
		return &openshell.CurrentUser{
			Subject:          "f3b1c2",
			DisplayName:      "Ada",
			Roles:            []string{"openshell-admin"},
			Scopes:           []string{"openid", "sandbox:read"},
			IdentityProvider: "oidc",
		}, nil
	}
	handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
	w := httptest.NewRecorder()
	handler.GetWhoAmI(w, httptest.NewRequest(http.MethodGet, "/auth/whoami", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	const want = `{"subject":"f3b1c2","displayName":"Ada","identityProvider":"oidc","roles":["openshell-admin"],"scopes":["openid","sandbox:read"]}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("body = %s\nwant %s", got, want)
	}
}

// gatewayLine is a release line for a test to hand a handler, so that what the
// test expects does not move when the line compiled into the build does. An
// empty string is the zero value: a line the BFF could not read.
func gatewayLine(t *testing.T, raw string) models.GatewayReleaseLine {
	t.Helper()
	if raw == "" {
		return models.GatewayReleaseLine{}
	}
	line, err := models.ParseGatewayReleaseLine(raw)
	if err != nil {
		t.Fatalf("ParseGatewayReleaseLine: %v", err)
	}
	return line
}

func TestGetWhoAmIPropagatesGatewayErrors(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name       string
		gatewayErr error
		proxyUser  string
		wantStatus int
		wantBody   apiutils.ErrorResponse
	}{
		{
			name:       "rejected token with proxy user",
			gatewayErr: &openshell.StatusError{Code: openshell.ErrorUnauthenticated, Message: "token rejected"},
			proxyUser:  "proxy-ada",
			wantStatus: http.StatusUnauthorized,
			wantBody:   apiutils.ErrorResponse{Code: apiutils.Unauthenticated, Message: "token rejected"},
		},
		{
			name:       "permission denied with proxy user",
			gatewayErr: &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "access denied"},
			proxyUser:  "proxy-ada",
			wantStatus: http.StatusForbidden,
			wantBody:   apiutils.ErrorResponse{Code: apiutils.PermissionDenied, Message: "access denied"},
		},
		{
			name:       "unavailable gateway with proxy user",
			gatewayErr: &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "connection refused"},
			proxyUser:  "proxy-ada",
			wantStatus: http.StatusBadGateway,
			wantBody:   apiutils.ErrorResponse{Code: apiutils.GatewayUnavailable, Message: "OpenShell gateway is unreachable"},
		},
		{
			name:       "unexpected gateway error with proxy user",
			gatewayErr: errors.New("internal gateway details"),
			proxyUser:  "proxy-ada",
			wantStatus: http.StatusInternalServerError,
			wantBody:   apiutils.ErrorResponse{Code: apiutils.Internal, Message: "internal error"},
		},
		{
			name:       "rejected token without proxy user",
			gatewayErr: &openshell.StatusError{Code: openshell.ErrorUnauthenticated, Message: "token rejected"},
			wantStatus: http.StatusUnauthorized,
			wantBody:   apiutils.ErrorResponse{Code: apiutils.Unauthenticated, Message: "token rejected"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.health.getCurrentUserFn = func(ctx context.Context) (*openshell.CurrentUser, error) {
				if got := auth.TokenFromContext(ctx); got != "test-token" {
					t.Errorf("gateway token = %q, want test-token", got)
				}
				return nil, tc.gatewayErr
			}
			middleware := auth.New(auth.Config{})
			handler := NewGatewayHandler(services.NewGatewayService(sdk), middleware, models.AuthConfigResponse{})
			req := httptest.NewRequest(http.MethodGet, "/auth/whoami", nil)
			req.Header.Set("x-forwarded-access-token", "test-token")
			if tc.proxyUser != "" {
				req.Header.Set("x-auth-request-user", tc.proxyUser)
			}
			w := httptest.NewRecorder()
			middleware.Handler(http.HandlerFunc(handler.GetWhoAmI)).ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			wantBody := map[string]any{"code": tc.wantBody.Code.String(), "message": tc.wantBody.Message}
			if !reflect.DeepEqual(body, wantBody) {
				t.Errorf("body = %v, want %v", body, wantBody)
			}
		})
	}
}

// GET /gateway carries the dashboard's verdict on the gateway's version. The
// gateways here are the ones that matter today: 0.0.116, which the one-off
// gateway-0.0.116 release is for, a release of this build's line, a dev build
// of that line, and the first release of the next line. Each body is logged,
// so `go test -v -run TestGetGatewayCompatibility` shows exactly what the
// frontend receives.
func TestGetGatewayCompatibility(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name     string
		line     string
		reported string
		want     map[string]any
	}{
		{
			name:     "gateway on an older line",
			line:     "0.1",
			reported: "0.0.116",
			want:     map[string]any{"status": "unsupported", "supportedLine": "0.1"},
		},
		{
			name:     "gateway on the line",
			line:     "0.1",
			reported: "0.1.2",
			want:     map[string]any{"status": "supported", "supportedLine": "0.1"},
		},
		{
			name:     "dev build of the line",
			line:     "0.1",
			reported: "0.1.3-dev.84+ge7fdd6bee",
			want:     map[string]any{"status": "supported", "supportedLine": "0.1"},
		},
		{
			name:     "gateway on a newer line",
			line:     "0.1",
			reported: "0.2.0",
			want:     map[string]any{"status": "unsupported", "supportedLine": "0.1"},
		},
		{
			name:     "gateway version unreadable",
			line:     "0.1",
			reported: "",
			want:     map[string]any{"status": "unknown", "supportedLine": "0.1"},
		},
		{
			// The line was overridden with something that is not one: the
			// BFF does not fall back to another, even for a gateway it would
			// otherwise call unsupported.
			name:     "a line that could not be read",
			reported: "0.0.116",
			want:     map[string]any{"status": "unknown"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.health.getGatewayInfoFn = func(_ context.Context) (*openshell.GatewayInfo, error) {
				return &openshell.GatewayInfo{
					Status:         openshell.ServiceStatusHealthy,
					Version:        tc.reported,
					ComputeDrivers: []openshell.ComputeDriverInfo{{Name: "podman"}},
				}, nil
			}
			handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
			handler.SetGatewayReleaseLine(gatewayLine(t, tc.line))

			w := httptest.NewRecorder()
			handler.GetGateway(w, httptest.NewRequest(http.MethodGet, "/gateway", nil))

			// Informational only: a gateway on another line is still a 200.
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			t.Logf("gateway %q, line %q -> %s", tc.reported, tc.line, w.Body.String())

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			// The gateway's own fields are passed through untouched.
			if body["status"] != "HEALTHY" || body["gatewayVersion"] != tc.reported {
				t.Errorf("gateway fields changed: %v", body)
			}
			got, ok := body["compatibility"].(map[string]any)
			if !ok {
				t.Fatalf("compatibility is missing or not an object: %v", body)
			}
			if len(got) != len(tc.want) {
				t.Errorf("compatibility = %v, want %v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("compatibility.%s = %v, want %v", key, got[key], want)
				}
			}
		})
	}
}

// A gateway that cannot be reached has no version to judge, so the error is
// relayed rather than dressed up as a compatibility result.
func TestGetGatewayUnavailable(t *testing.T) {
	sdk := &mockSDK{}
	sdk.health.getGatewayInfoFn = func(_ context.Context) (*openshell.GatewayInfo, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "down"}
	}
	handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
	handler.SetGatewayReleaseLine(gatewayLine(t, "0.1"))
	req := httptest.NewRequest(http.MethodGet, "/gateway", nil)
	w := httptest.NewRecorder()
	handler.GetGateway(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

// A handler nobody configured judges gateways against the line compiled into
// the build. This is what an image built with no build args and started with
// no environment serves, and it has to be a verdict, not "unknown".
func TestGetGatewayCompatibilityUsesTheBuiltInLine(t *testing.T) {
	builtIn := models.BuiltInGatewayReleaseLine
	sdk := &mockSDK{}
	sdk.health.checkFn = func(_ context.Context) (*openshell.HealthResult, error) {
		return &openshell.HealthResult{Healthy: true, Version: builtIn + ".0"}, nil
	}
	handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})

	w := httptest.NewRecorder()
	handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	want := `{"gatewayVersion":"` + builtIn + `.0","healthy":true,"compatibility":{"status":"supported","supportedLine":"` + builtIn + `"}}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// The gateway refuses GetGatewayInfo to anyone who is not a platform admin
// ("role 'openshell-admin' required"), yet a gateway on another line breaks
// that user's pages just the same. The verdict therefore has a route of its
// own that reads the version from the health check, which needs no role.
func TestGetGatewayCompatibilityNeedsNoAdminRole(t *testing.T) {
	sdk := &mockSDK{}
	gatewayInfoCalls := 0
	sdk.health.getGatewayInfoFn = func(_ context.Context) (*openshell.GatewayInfo, error) {
		gatewayInfoCalls++
		return nil, &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "role 'openshell-admin' required"}
	}
	sdk.health.checkFn = func(_ context.Context) (*openshell.HealthResult, error) {
		return &openshell.HealthResult{Healthy: true, Version: "0.0.116"}, nil
	}
	handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
	handler.SetGatewayReleaseLine(gatewayLine(t, "0.1"))

	// The admin-only route stays refused: the BFF relays the gateway's answer
	// and does not paper over it.
	refused := httptest.NewRecorder()
	handler.GetGateway(refused, httptest.NewRequest(http.MethodGet, "/gateway", nil))
	if refused.Code != http.StatusForbidden {
		t.Fatalf("GET /gateway = %d, want 403; body: %s", refused.Code, refused.Body.String())
	}

	// The verdict is not refused.
	w := httptest.NewRecorder()
	handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /gateway/compatibility = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	t.Logf("GetGatewayInfo refused -> %s", w.Body.String())
	const want = `{"gatewayVersion":"0.0.116","healthy":true,"compatibility":{"status":"unsupported","supportedLine":"0.1"}}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	// And it never touched the call that would have refused it.
	if gatewayInfoCalls != 1 {
		t.Errorf("GetGatewayInfo was called %d times, want 1 (by GET /gateway only)", gatewayInfoCalls)
	}
}

// GET /gateway/compatibility judges the version the health check reports the
// same way GET /gateway judges the one GetGatewayInfo reports. Each body is
// logged, so `go test -v -run TestGetGatewayCompatibilityRoute` shows exactly
// what the notice in the UI receives.
func TestGetGatewayCompatibilityRoute(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name     string
		line     string
		reported string
		want     string
	}{
		{
			name:     "gateway on an older line",
			line:     "0.1",
			reported: "0.0.116",
			want:     `{"gatewayVersion":"0.0.116","healthy":true,"compatibility":{"status":"unsupported","supportedLine":"0.1"}}`,
		},
		{
			name:     "gateway on the line",
			line:     "0.1",
			reported: "0.1.2",
			want:     `{"gatewayVersion":"0.1.2","healthy":true,"compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			name:     "dev build of the line",
			line:     "0.1",
			reported: "0.1.3-dev.84+ge7fdd6bee",
			want:     `{"gatewayVersion":"0.1.3-dev.84+ge7fdd6bee","healthy":true,"compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			// The version is passed through as reported; only the verdict
			// reads it as the release it rebuilds.
			name:     "rebuild of a release of the line",
			line:     "0.1",
			reported: "0.1.2-vendor.5",
			want:     `{"gatewayVersion":"0.1.2-vendor.5","healthy":true,"compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			name:     "gateway on a newer line",
			line:     "0.1",
			reported: "0.2.0",
			want:     `{"gatewayVersion":"0.2.0","healthy":true,"compatibility":{"status":"unsupported","supportedLine":"0.1"}}`,
		},
		{
			name:     "pre-release of a newer line",
			line:     "0.1",
			reported: "0.2.0-pre.1",
			want:     `{"gatewayVersion":"0.2.0-pre.1","healthy":true,"compatibility":{"status":"unsupported","supportedLine":"0.1"}}`,
		},
		{
			name:     "gateway that does not know its own version",
			line:     "0.1",
			reported: "0.0.0",
			want:     `{"gatewayVersion":"0.0.0","healthy":true,"compatibility":{"status":"unknown","supportedLine":"0.1"}}`,
		},
		{
			name:     "gateway version empty",
			line:     "0.1",
			reported: "",
			want:     `{"gatewayVersion":"","healthy":true,"compatibility":{"status":"unknown","supportedLine":"0.1"}}`,
		},
		{
			name:     "a line that could not be read",
			reported: "0.0.116",
			want:     `{"gatewayVersion":"0.0.116","healthy":true,"compatibility":{"status":"unknown"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.health.checkFn = func(_ context.Context) (*openshell.HealthResult, error) {
				return &openshell.HealthResult{Healthy: true, Version: tc.reported}, nil
			}
			handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
			handler.SetGatewayReleaseLine(gatewayLine(t, tc.line))

			w := httptest.NewRecorder()
			handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))

			// Informational only: a gateway on another line is still a 200.
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			t.Logf("gateway %q, line %q -> %s", tc.reported, tc.line, w.Body.String())
			if got := strings.TrimSpace(w.Body.String()); got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
		})
	}
}

// With the gateway down there is no version to judge. The error is relayed
// with the same status as on every other route, not turned into "unknown".
func TestGetGatewayCompatibilityUnavailable(t *testing.T) {
	sdk := &mockSDK{}
	sdk.health.checkFn = func(_ context.Context) (*openshell.HealthResult, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "down"}
	}
	handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
	handler.SetGatewayReleaseLine(gatewayLine(t, "0.1"))

	w := httptest.NewRecorder()
	handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "compatibility") {
		t.Errorf("an unreachable gateway was given a verdict: %s", w.Body.String())
	}
	// Nor is it called unhealthy: it said nothing about its health.
	if strings.Contains(w.Body.String(), "healthy") {
		t.Errorf("an unreachable gateway was given a health: %s", w.Body.String())
	}
}

// GET /gateway/compatibility carries what the gateway's health check says
// about the gateway, so that a user who is not a platform admin — and cannot
// read GET /gateway — still learns it. The answer is the health check's own:
// healthy, or answered and not healthy. A gateway that did not answer is an
// error, and a source that reports no health leaves the field out.
func TestGetGatewayCompatibilityHealth(t *testing.T) {
	unavailable := &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "down"}
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name       string
		result     *openshell.HealthResult
		err        error
		wantStatus int
		// wantHealthy is the JSON value of "healthy", or "" for a body
		// that must not have the key.
		wantHealthy string
		wantVersion string
	}{
		{
			name:       "healthy",
			result:     &openshell.HealthResult{Healthy: true, Version: "0.1.2"},
			wantStatus: http.StatusOK, wantHealthy: "true", wantVersion: "0.1.2",
		},
		{
			// Degraded, unhealthy or unspecified: the SDK does not say which.
			// It is still an answer, so it is reported and not left out.
			name:       "answered, and not with healthy",
			result:     &openshell.HealthResult{Healthy: false, Version: "0.1.2"},
			wantStatus: http.StatusOK, wantHealthy: "false", wantVersion: "0.1.2",
		},
		{
			name:       "the SDK answered with nothing",
			wantStatus: http.StatusOK,
		},
		{
			name:       "gateway unreachable",
			err:        unavailable,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "health check timed out",
			err:        &openshell.StatusError{Code: openshell.ErrorDeadlineExceeded, Message: "deadline exceeded"},
			wantStatus: http.StatusBadGateway,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			checks := 0
			sdk.health.checkFn = func(_ context.Context) (*openshell.HealthResult, error) {
				checks++
				return tc.result, tc.err
			}
			sdk.health.getGatewayInfoFn = func(_ context.Context) (*openshell.GatewayInfo, error) {
				t.Error("the admin-only GetGatewayInfo was called for the health")
				return nil, unavailable
			}
			handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})

			w := httptest.NewRecorder()
			handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			// One health check answers the version and the health together.
			if checks != 1 {
				t.Errorf("the health check was called %d times, want 1", checks)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v; body: %s", err, w.Body.String())
			}
			if got := string(body["healthy"]); got != tc.wantHealthy {
				t.Errorf("healthy = %q, want %q; body: %s", got, tc.wantHealthy, w.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				if string(body["code"]) != `"gateway_unavailable"` {
					t.Errorf("code = %s, want gateway_unavailable", body["code"])
				}
				return
			}
			if got := string(body["gatewayVersion"]); got != `"`+tc.wantVersion+`"` {
				t.Errorf("gatewayVersion = %s, want %q", got, tc.wantVersion)
			}
		})
	}
}

// versionOnlyGatewayService is a gateway service that offers the role-free
// version source and nothing for the health: it has GetGatewayVersion and no
// GetGatewayHealth.
type versionOnlyGatewayService struct {
	infoOnlyGatewayService
	version string
}

func (s versionOnlyGatewayService) GetGatewayVersion(context.Context) (string, error) {
	return s.version, nil
}

// A service gets its verdict whichever of the optional sources it has. The
// health comes from the best source on offer, and is left out when there is
// none rather than guessed.
func TestGetGatewayCompatibilityHealthSources(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name string
		svc  services.GatewayServiceInterface
		want string
	}{
		{
			name: "version reader only: no health to report",
			svc:  versionOnlyGatewayService{version: "0.1.2"},
			want: `{"gatewayVersion":"0.1.2","compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			name: "gateway info only, healthy",
			svc:  infoOnlyGatewayService{info: &models.GatewayInfo{Status: "HEALTHY", GatewayVersion: "0.1.2"}},
			want: `{"gatewayVersion":"0.1.2","healthy":true,"compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			name: "gateway info only, degraded",
			svc:  infoOnlyGatewayService{info: &models.GatewayInfo{Status: "DEGRADED", GatewayVersion: "0.1.2"}},
			want: `{"gatewayVersion":"0.1.2","healthy":false,"compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			name: "gateway info only, unhealthy",
			svc:  infoOnlyGatewayService{info: &models.GatewayInfo{Status: "UNHEALTHY", GatewayVersion: "0.1.2"}},
			want: `{"gatewayVersion":"0.1.2","healthy":false,"compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			name: "gateway info only, without a status",
			svc:  infoOnlyGatewayService{info: &models.GatewayInfo{GatewayVersion: "0.1.2"}},
			want: `{"gatewayVersion":"0.1.2","compatibility":{"status":"supported","supportedLine":"0.1"}}`,
		},
		{
			name: "gateway info only, answering with nothing",
			svc:  infoOnlyGatewayService{},
			want: `{"gatewayVersion":"","compatibility":{"status":"unknown","supportedLine":"0.1"}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewGatewayHandler(tc.svc, auth.New(auth.Config{}), models.AuthConfigResponse{})
			handler.SetGatewayReleaseLine(gatewayLine(t, "0.1"))
			w := httptest.NewRecorder()
			handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if got := strings.TrimSpace(w.Body.String()); got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
		})
	}
}

// infoOnlyGatewayService is a gateway service that implements
// GatewayServiceInterface and nothing more: it has no GetGatewayVersion.
type infoOnlyGatewayService struct {
	info *models.GatewayInfo
	err  error
}

func (s infoOnlyGatewayService) GetGatewayInfo(context.Context) (*models.GatewayInfo, error) {
	return s.info, s.err
}
func (infoOnlyGatewayService) CheckHealth(context.Context) error { return nil }
func (infoOnlyGatewayService) GetCurrentUser(context.Context) (*models.CurrentUser, error) {
	return &models.CurrentUser{}, nil
}

// Such a service gets a verdict from the only version source it has. That
// source is admin-only, so its refusal is relayed.
func TestGetGatewayCompatibilityFallsBackToGatewayInfo(t *testing.T) {
	get := func(svc services.GatewayServiceInterface) *httptest.ResponseRecorder {
		handler := NewGatewayHandler(svc, auth.New(auth.Config{}), models.AuthConfigResponse{})
		handler.SetGatewayReleaseLine(gatewayLine(t, "0.1"))
		w := httptest.NewRecorder()
		handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))
		return w
	}

	w := get(infoOnlyGatewayService{info: &models.GatewayInfo{Status: "HEALTHY", GatewayVersion: "0.0.116"}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	const want = `{"gatewayVersion":"0.0.116","healthy":true,"compatibility":{"status":"unsupported","supportedLine":"0.1"}}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	w = get(infoOnlyGatewayService{err: &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "role 'openshell-admin' required"}})
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 relayed from GetGatewayInfo; body: %s", w.Code, w.Body.String())
	}

	// A service that answers with nothing has no version to judge.
	w = get(infoOnlyGatewayService{})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"unknown"`) {
		t.Errorf("nil info: status = %d, body = %s; want 200 with status unknown", w.Code, w.Body.String())
	}
}

func TestGetReadyz(t *testing.T) {
	handler := NewGatewayHandler(services.NewGatewayService(&mockSDK{}), auth.New(auth.Config{}), models.AuthConfigResponse{})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	handler.GetReadyz(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetReadyzUnavailable(t *testing.T) {
	sdk := &mockSDK{}
	sdk.health.checkFn = func(_ context.Context) (*openshell.HealthResult, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "down"}
	}
	handler := NewGatewayHandler(services.NewGatewayService(sdk), auth.New(auth.Config{}), models.AuthConfigResponse{})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	handler.GetReadyz(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestGetWhoAmIAuthDisabled(t *testing.T) {
	handler := NewGatewayHandler(
		services.NewGatewayService(&mockSDK{}),
		auth.New(auth.Config{Disabled: true}),
		models.AuthConfigResponse{AdminRole: "openshell-admin"},
	)
	req := httptest.NewRequest(http.MethodGet, "/auth/whoami", nil)
	w := httptest.NewRecorder()
	handler.GetWhoAmI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["subject"] != "dev-user" {
		t.Errorf("subject = %v", body["subject"])
	}
}

// versionOverridingGateway embeds services.GatewayService and answers one
// method itself.
type versionOverridingGateway struct {
	*services.GatewayService
	version string
}

func (g *versionOverridingGateway) GetGatewayVersion(context.Context) (string, error) {
	return g.version, nil
}

// A service that embeds services.GatewayService and answers the version
// itself inherits GetGatewayHealth without having written it. Its version is
// the one served and judged; only the health comes from the health check.
func TestGetGatewayCompatibilityKeepsAServiceVersion(t *testing.T) {
	sdk := &mockSDK{}
	sdk.health.checkFn = func(context.Context) (*openshell.HealthResult, error) {
		return &openshell.HealthResult{Healthy: true, Version: "0.0.1"}, nil
	}
	svc := &versionOverridingGateway{GatewayService: services.NewGatewayService(sdk), version: "9.9.9-custom"}
	handler := NewGatewayHandler(svc, auth.New(auth.Config{}), models.AuthConfigResponse{})

	w := httptest.NewRecorder()
	handler.GetGatewayCompatibility(w, httptest.NewRequest(http.MethodGet, "/gateway/compatibility", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got models.GatewayCompatibilityInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if got.GatewayVersion != "9.9.9-custom" {
		t.Errorf("gatewayVersion = %q, want the service's own answer", got.GatewayVersion)
	}
	if got.Healthy == nil || !*got.Healthy {
		t.Errorf("healthy = %v, want true from the health check", got.Healthy)
	}
}
