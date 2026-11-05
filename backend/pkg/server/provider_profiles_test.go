package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/fake"
	pb "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"

	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
)

// scopeRecorder is a profile store that notes the scope of every call.
type scopeRecorder struct {
	calls []string
}

func (s *scopeRecorder) note(call, workspace string) {
	if workspace == "" {
		workspace = "platform"
	}
	s.calls = append(s.calls, call+" "+workspace)
}

func (s *scopeRecorder) ListProviderProfiles(_ context.Context, workspace string) ([]*pb.ProviderProfile, error) {
	s.note("list", workspace)
	return nil, nil
}

func (s *scopeRecorder) GetProviderProfile(_ context.Context, workspace, id string) (*pb.ProviderProfile, error) {
	s.note("get", workspace)
	return &pb.ProviderProfile{Id: id}, nil
}

func (s *scopeRecorder) ImportProviderProfiles(_ context.Context, workspace string, _ []*pb.ProviderProfileImportItem) (*pb.ImportProviderProfilesResponse, error) {
	s.note("import", workspace)
	return &pb.ImportProviderProfilesResponse{Imported: true}, nil
}

func (s *scopeRecorder) UpdateProviderProfile(_ context.Context, workspace, _ string, _ uint64, _ *pb.ProviderProfileImportItem) (*pb.UpdateProviderProfilesResponse, error) {
	s.note("update", workspace)
	return &pb.UpdateProviderProfilesResponse{Updated: true}, nil
}

func (s *scopeRecorder) LintProviderProfiles(_ context.Context, workspace string, _ []*pb.ProviderProfileImportItem) (*pb.LintProviderProfilesResponse, error) {
	s.note("lint", workspace)
	return &pb.LintProviderProfilesResponse{Valid: true}, nil
}

const routeProfile = `{"id":"github","displayName":"GitHub","category":"SOURCE_CONTROL","inferenceCapable":false}`

// The profile routes exist in both scopes, and SetProviderProfiles is what
// they run on: prove the wiring through the real router, as main builds it.
func TestProviderProfileRoutesInBothScopes(t *testing.T) {
	store := &scopeRecorder{}
	app := NewApp(fake.NewClient(), nil, auth.New(auth.Config{Disabled: true}), "", models.AuthConfigResponse{AuthDisabled: true})
	app.SetProviderProfiles(store)
	router := app.Routes()

	for _, base := range []string{"/api/v1/provider-profiles", "/api/v1/workspaces/team-a/provider-profiles"} {
		requests := []struct {
			method, path, body string
			want               int
		}{
			{http.MethodGet, base, "", http.StatusOK},
			{http.MethodGet, base + "/github", "", http.StatusOK},
			{http.MethodPost, base, `{"profiles":[` + routeProfile + `]}`, http.StatusCreated},
			{http.MethodPost, base + "/lint", `{"profiles":[` + routeProfile + `]}`, http.StatusOK},
			{http.MethodPut, base + "/github", `{"profile":` + routeProfile + `,"expectedResourceVersion":1}`, http.StatusOK},
		}
		for _, request := range requests {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
			if recorder.Code != request.want {
				t.Fatalf("%s %s = %d, want %d; body: %s", request.method, request.path, recorder.Code, request.want, recorder.Body.String())
			}
		}
	}

	want := []string{
		"list platform", "get platform", "import platform", "lint platform", "update platform",
		"list team-a", "get team-a", "import team-a", "lint team-a", "update team-a",
	}
	if !reflect.DeepEqual(store.calls, want) {
		t.Errorf("calls = %v\nwant    %v", store.calls, want)
	}
}

// A path with nothing where the workspace goes is not a way into the platform
// scope: it is not a route.
func TestProviderProfileRoutesNeedAWorkspaceName(t *testing.T) {
	store := &scopeRecorder{}
	app := NewApp(fake.NewClient(), nil, auth.New(auth.Config{Disabled: true}), "", models.AuthConfigResponse{AuthDisabled: true})
	app.SetProviderProfiles(store)

	recorder := httptest.NewRecorder()
	app.Routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces//provider-profiles", nil))
	if recorder.Code == http.StatusOK || len(store.calls) != 0 {
		t.Errorf("GET /api/v1/workspaces//provider-profiles = %d and reached the store (%v), want neither", recorder.Code, store.calls)
	}
}

// The platform routes sit behind the same auth middleware as every other
// gateway call: without a bearer they answer 401 and reach nothing.
func TestPlatformProviderProfileRoutesNeedABearer(t *testing.T) {
	store := &scopeRecorder{}
	app := NewApp(fake.NewClient(), nil, auth.New(auth.Config{}), "", models.AuthConfigResponse{})
	app.SetProviderProfiles(store)
	router := app.Routes()

	requests := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/provider-profiles"},
		{http.MethodGet, "/api/v1/provider-profiles/github"},
		{http.MethodPost, "/api/v1/provider-profiles"},
		{http.MethodPost, "/api/v1/provider-profiles/lint"},
		{http.MethodPut, "/api/v1/provider-profiles/github"},
		{http.MethodDelete, "/api/v1/provider-profiles/github"},
	}
	for _, request := range requests {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, strings.NewReader(`{}`)))
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a bearer = %d, want 401", request.method, request.path, recorder.Code)
		}
	}
	if len(store.calls) != 0 {
		t.Errorf("unauthenticated requests reached the store: %v", store.calls)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/provider-profiles", nil)
	request.Header.Set("x-forwarded-access-token", "test-token")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(store.calls, []string{"list platform"}) {
		t.Errorf("with a bearer: status = %d, calls = %v", recorder.Code, store.calls)
	}
}
