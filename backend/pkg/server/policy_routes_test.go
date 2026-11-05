package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/fake"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
)

// The policy routes exist only if app.go mounts them. Each request here is one
// the handler itself refuses before it calls the gateway, so the BFF's own
// error envelope coming back means the router found the handler: a path the
// router does not know answers with chi's plain-text 404 or 405 instead.
func TestPolicyRoutesAreMounted(t *testing.T) {
	app := NewApp(fake.NewClient(), nil, auth.New(auth.Config{Disabled: true}), "", models.AuthConfigResponse{AuthDisabled: true})
	router := app.Routes()

	const sandbox = "/api/v1/workspaces/team-a/sandboxes/sb1"
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode apiutils.ResponseCode
	}{
		{name: "merge", method: http.MethodPost, path: sandbox + "/policy/merge", body: `{"operations":[]}`, wantCode: apiutils.InvalidPolicy},
		{name: "sandbox revision", method: http.MethodGet, path: sandbox + "/policy/revisions/0", wantCode: apiutils.InvalidRequest},
		{name: "global revision", method: http.MethodGet, path: "/api/v1/global-policy/revisions/0", wantCode: apiutils.InvalidRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("%s %s = %d, want the handler's 400; body: %s", tc.method, tc.path, recorder.Code, recorder.Body.String())
			}
			var envelope apiutils.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("%s %s did not answer with the BFF's error envelope: %v; body: %s", tc.method, tc.path, err, recorder.Body.String())
			}
			if envelope.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", envelope.Code, tc.wantCode)
			}
		})
	}

	// The effective policy route has nothing of its own to refuse, and the
	// SDK's fake does not implement the call behind it. Its answer is JSON all
	// the same, which the router's plain-text 404 is not.
	t.Run("effective", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, sandbox+"/policy/effective", nil))
		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("GET %s/policy/effective = %d with Content-Type %q, want the handler's JSON; body: %s",
				sandbox, recorder.Code, got, recorder.Body.String())
		}
	})
}
