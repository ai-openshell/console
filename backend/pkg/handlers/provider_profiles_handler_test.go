package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	pb "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"
	sbv1 "github.com/NVIDIA/OpenShell/sdk/go/proto/sandboxv1"
	"github.com/ai-openshell/console/backend/pkg/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// storedProfile is a profile as a gateway holds it, with everything on it the
// dashboard used to drop: where the credential goes in a request, how it is
// refreshed and granted, the binaries, discovery, annotations, and an endpoint
// that says what it allows. It also has what OpenShell 0.1.3 added: files, and
// the owners the gateway derives for a token grant.
func storedProfile() *pb.ProviderProfile {
	return &pb.ProviderProfile{
		Id:               "github",
		DisplayName:      "GitHub",
		Description:      "GitHub API and Git operations",
		Category:         pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_SOURCE_CONTROL,
		ResourceVersion:  7,
		Source:           "user",
		Scope:            "workspace",
		Annotations:      map[string]string{"example.com/owner": "platform-team"},
		InferenceCapable: true,
		Credentials: []*pb.ProviderProfileCredential{
			{
				Name:         "api_token",
				Description:  "GitHub token",
				EnvVars:      []string{"GITHUB_TOKEN", "GH_TOKEN"},
				Required:     true,
				AuthStyle:    "bearer",
				HeaderName:   "authorization",
				QueryParam:   "token",
				PathTemplate: "/v1/{credential}/x",
				Refresh: &pb.ProviderCredentialRefresh{
					Strategy:      pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_OAUTH2_CLIENT_CREDENTIALS,
					TokenUrl:      "https://login.example.com/oauth2/token",
					Scopes:        []string{"repo"},
					RefreshBefore: durationpb.New(300 * time.Second),
					MaxLifetime:   durationpb.New(time.Hour),
					Material: []*pb.ProviderCredentialRefreshMaterial{
						{Name: "client_id", Required: true},
						{Name: "client_secret", Description: "OAuth client secret", Required: true, Secret: true},
					},
					AdditionalOutputs: []*pb.ProviderCredentialRefreshOutput{{Output: "session_token", Credential: "session"}},
				},
			},
			{
				Name: "session",
				TokenGrant: &pb.ProviderCredentialTokenGrant{
					GrantType:           pb.ProviderCredentialTokenGrantType_PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_TOKEN_EXCHANGE,
					TokenEndpoint:       "https://login.example.com/token",
					Audience:            "api://github",
					JwtSvidAudience:     "https://login.example.com",
					ClientAssertionType: "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe",
					RequestedTokenType:  "urn:ietf:params:oauth:token-type:access_token",
					Scopes:              []string{"read"},
					CacheTtl:            durationpb.New(90 * time.Second),
					SubjectToken: &pb.ProviderCredentialTokenGrantSubjectToken{
						Source: "provider_credential", Credential: "api_token", SubjectTokenType: "urn:x",
					},
					AudienceOverrides: []*pb.ProviderCredentialTokenGrantAudienceOverride{
						{Host: "api.github.com", Port: 443, Path: "/graphql", Audience: "api://graphql", Scopes: []string{"gql"}},
					},
				},
				TokenGrantOwners: []string{"gateway-derived-owner"},
			},
		},
		Files: []*pb.ProviderProfileFile{
			{Path: "hosts.yml", Content: "host: {{config.host}}\n", EnvVar: "GH_CONFIG_FILE"},
			{Path: "notice.txt", Content: "managed by the platform team\n"},
		},
		Endpoints: []*sbv1.NetworkEndpoint{
			{
				Host:        "api.github.com",
				Port:        443,
				Protocol:    "rest",
				Access:      sbv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY,
				Enforcement: sbv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_ENFORCE,
			},
			{
				Host:     "github.com",
				Ports:    []uint32{22, 443},
				Protocol: "rest",
				Rules: []*sbv1.L7Rule{{Allow: &sbv1.L7Allow{
					Method: "GET", Path: "/repos/**",
					Query: map[string]*sbv1.L7QueryMatcher{"ref": {Any: []string{"main", "release-*"}}},
				}}},
				DenyRules: []*sbv1.L7DenyRule{{Method: "DELETE", Path: "/repos/**"}},
			},
		},
		Binaries:  []*sbv1.NetworkBinary{{Path: "/usr/bin/gh"}, {Path: "/usr/bin/git"}},
		Discovery: &pb.ProviderProfileDiscovery{Credentials: []string{"api_token"}},
	}
}

// fakeProfileStore is a ProviderProfileStore that holds its profiles whole,
// the way clients.RawExecClient does, and records what it is asked.
type fakeProfileStore struct {
	err         error
	profiles    []*pb.ProviderProfile
	diagnostics []*pb.ProviderProfileDiagnostic
	calls       []string
	items       []*pb.ProviderProfileImportItem
	expected    []uint64
	refuse      bool
}

func (f *fakeProfileStore) ListProviderProfiles(_ context.Context, workspace string) ([]*pb.ProviderProfile, error) {
	f.calls = append(f.calls, "list ["+workspace+"]")
	return f.profiles, f.err
}

func (f *fakeProfileStore) GetProviderProfile(_ context.Context, workspace, id string) (*pb.ProviderProfile, error) {
	f.calls = append(f.calls, "get ["+workspace+"] "+id)
	if f.err != nil {
		return nil, f.err
	}
	for _, profile := range f.profiles {
		if profile.GetId() == id {
			return profile, nil
		}
	}
	return nil, status.Error(codes.NotFound, "provider profile not found")
}

func (f *fakeProfileStore) ImportProviderProfiles(_ context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.ImportProviderProfilesResponse, error) {
	f.calls = append(f.calls, "import ["+workspace+"]")
	f.items = append(f.items, items...)
	if f.err != nil {
		return nil, f.err
	}
	resp := &pb.ImportProviderProfilesResponse{Diagnostics: f.diagnostics, Imported: !f.refuse}
	if !f.refuse {
		for _, item := range items {
			resp.Profiles = append(resp.Profiles, item.GetProfile())
		}
	}
	return resp, nil
}

func (f *fakeProfileStore) UpdateProviderProfile(_ context.Context, workspace, id string, expected uint64, item *pb.ProviderProfileImportItem) (*pb.UpdateProviderProfilesResponse, error) {
	f.calls = append(f.calls, "update ["+workspace+"] "+id)
	f.items = append(f.items, item)
	f.expected = append(f.expected, expected)
	if f.err != nil {
		return nil, f.err
	}
	if f.refuse {
		return &pb.UpdateProviderProfilesResponse{Diagnostics: f.diagnostics}, nil
	}
	return &pb.UpdateProviderProfilesResponse{Updated: true, Profile: item.GetProfile()}, nil
}

func (f *fakeProfileStore) LintProviderProfiles(_ context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.LintProviderProfilesResponse, error) {
	f.calls = append(f.calls, "lint ["+workspace+"]")
	f.items = append(f.items, items...)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.LintProviderProfilesResponse{Diagnostics: f.diagnostics, Valid: !f.refuse}, nil
}

// profilesHandler is a ProvidersHandler whose profiles come from the store,
// or through the SDK when the store is nil. Deletes always go through the
// SDK, whose calls are recorded in the returned slice.
func profilesHandler(store services.ProviderProfileStore) (*ProvidersHandler, *mockSDK, *[]string) {
	sdk := &mockSDK{}
	deletes := &[]string{}
	sdk.providers.profiles.deleteFn = func(_ context.Context, workspace, id string) (bool, error) {
		*deletes = append(*deletes, "delete ["+workspace+"] "+id)
		return true, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	if store != nil {
		handler.SetProfileStore(store)
	}
	return handler, sdk, deletes
}

// serveProfiles routes a request the way app.go does: the same handlers under
// a workspace and, told to address the platform scope, at the top level.
func serveProfiles(handler *ProvidersHandler, method, path, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	asIs := func(next http.HandlerFunc) http.HandlerFunc { return next }
	for base, in := range map[string]func(http.HandlerFunc) http.HandlerFunc{
		"/provider-profiles":                        handler.InPlatformScope,
		"/workspaces/{workspace}/provider-profiles": asIs,
	} {
		r.Get(base, in(handler.ListProviderProfiles))
		r.Post(base, in(handler.ImportProviderProfiles))
		r.Post(base+"/lint", in(handler.LintProviderProfiles))
		r.Get(base+"/{profileId}", in(handler.GetProviderProfile))
		r.Put(base+"/{profileId}", in(handler.UpdateProviderProfile))
		r.Delete(base+"/{profileId}", in(handler.DeleteProviderProfile))
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const minimalProfile = `{"id":"github","displayName":"GitHub","category":"SOURCE_CONTROL","inferenceCapable":false}`

// A route under a workspace addresses that workspace, and the same route at
// the top level addresses the platform scope, which is the empty workspace.
func TestProfileRoutesAddressTheirScope(t *testing.T) {
	scopes := []struct{ base, workspace string }{
		{base: "/workspaces/team-a/provider-profiles", workspace: "team-a"},
		{base: "/provider-profiles", workspace: ""},
	}
	for _, scope := range scopes {
		t.Run("scope ["+scope.workspace+"]", func(t *testing.T) {
			store := &fakeProfileStore{profiles: []*pb.ProviderProfile{storedProfile()}}
			handler, _, deletes := profilesHandler(store)
			requests := []struct {
				method, path, body string
				want               int
			}{
				{method: http.MethodGet, path: scope.base, want: http.StatusOK},
				{method: http.MethodGet, path: scope.base + "/github", want: http.StatusOK},
				{method: http.MethodPost, path: scope.base, body: `{"profiles":[` + minimalProfile + `]}`, want: http.StatusCreated},
				{method: http.MethodPost, path: scope.base + "/lint", body: `{"profiles":[` + minimalProfile + `]}`, want: http.StatusOK},
				{method: http.MethodPut, path: scope.base + "/github", body: `{"profile":` + minimalProfile + `,"expectedResourceVersion":7}`, want: http.StatusOK},
				{method: http.MethodDelete, path: scope.base + "/github", want: http.StatusOK},
			}
			for _, request := range requests {
				if w := serveProfiles(handler, request.method, request.path, request.body); w.Code != request.want {
					t.Fatalf("%s %s: status = %d, want %d; body: %s", request.method, request.path, w.Code, request.want, w.Body.String())
				}
			}
			ws := "[" + scope.workspace + "]"
			wantCalls := []string{"list " + ws, "get " + ws + " github", "import " + ws, "lint " + ws, "update " + ws + " github"}
			if !reflect.DeepEqual(store.calls, wantCalls) {
				t.Errorf("store calls = %v, want %v", store.calls, wantCalls)
			}
			if want := []string{"delete " + ws + " github"}; !reflect.DeepEqual(*deletes, want) {
				t.Errorf("deletes = %v, want %v", *deletes, want)
			}
		})
	}
}

// The router matches an empty path segment, and to the gateway an empty
// workspace is the platform scope. A workspace route with no workspace in it
// must not become a platform route: a platform profile reaches every
// workspace.
func TestWorkspaceProfileRoutesRefuseAnEmptyWorkspace(t *testing.T) {
	store := &fakeProfileStore{profiles: []*pb.ProviderProfile{storedProfile()}}
	handler, _, deletes := profilesHandler(store)
	requests := []struct{ method, path, body string }{
		{http.MethodGet, "/workspaces//provider-profiles", ""},
		{http.MethodGet, "/workspaces//provider-profiles/github", ""},
		{http.MethodPost, "/workspaces//provider-profiles", `{"profiles":[` + minimalProfile + `]}`},
		{http.MethodPost, "/workspaces//provider-profiles/lint", `{"profiles":[` + minimalProfile + `]}`},
		{http.MethodPut, "/workspaces//provider-profiles/github", `{"profile":` + minimalProfile + `,"expectedResourceVersion":7}`},
		{http.MethodDelete, "/workspaces//provider-profiles/github", ""},
	}
	for _, request := range requests {
		w := serveProfiles(handler, request.method, request.path, request.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400; body: %s", request.method, request.path, w.Code, w.Body.String())
			continue
		}
		if answer := decodeObject(t, w); answer["code"] != "invalid_name" {
			t.Errorf("%s %s: code = %v, want invalid_name", request.method, request.path, answer["code"])
		}
	}
	if len(store.calls) != 0 || len(*deletes) != 0 {
		t.Errorf("a request that named no workspace reached the gateway: %v %v", store.calls, *deletes)
	}
}

// A profile is returned with everything the gateway holds for it.
func TestProfilesAreReturnedWhole(t *testing.T) {
	store := &fakeProfileStore{profiles: []*pb.ProviderProfile{storedProfile()}}
	handler, _, _ := profilesHandler(store)

	var listed []map[string]any
	w := serveProfiles(handler, http.MethodGet, "/workspaces/team-a/provider-profiles", "")
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil || len(listed) != 1 {
		t.Fatalf("list: %v, %d profiles", err, len(listed))
	}
	one := decodeObject(t, serveProfiles(handler, http.MethodGet, "/workspaces/team-a/provider-profiles/github", ""))

	var want map[string]any
	if err := json.Unmarshal([]byte(storedProfileJSON), &want); err != nil {
		t.Fatalf("storedProfileJSON: %v", err)
	}
	for name, profile := range map[string]map[string]any{"listed": listed[0], "read": one} {
		if !reflect.DeepEqual(profile, want) {
			got, _ := json.MarshalIndent(profile, "", "  ")
			t.Errorf("the %s profile is not the stored profile, field for field.\ngot:  %s\nwant: %s", name, got, storedProfileJSON)
		}
	}
}

// storedProfileJSON is storedProfile as the BFF returns it. Everything from
// "annotations" down is what the dashboard used to leave out.
const storedProfileJSON = `{
  "id": "github",
  "displayName": "GitHub",
  "description": "GitHub API and Git operations",
  "category": "SOURCE_CONTROL",
  "source": "user",
  "scope": "workspace",
  "inferenceCapable": true,
  "resourceVersion": 7,
  "endpoints": ["api.github.com:443", "github.com:22,443"],
  "annotations": {"example.com/owner": "platform-team"},
  "credentials": [
    {
      "name": "api_token",
      "description": "GitHub token",
      "envVars": ["GITHUB_TOKEN", "GH_TOKEN"],
      "required": true,
      "authStyle": "bearer",
      "headerName": "authorization",
      "queryParam": "token",
      "pathTemplate": "/v1/{credential}/x",
      "refresh": {
        "strategy": "OAUTH2_CLIENT_CREDENTIALS",
        "tokenUrl": "https://login.example.com/oauth2/token",
        "scopes": ["repo"],
        "refreshBefore": "300s",
        "maxLifetime": "3600s",
        "material": [
          {"name": "client_id", "required": true, "secret": false},
          {"name": "client_secret", "description": "OAuth client secret", "required": true, "secret": true}
        ],
        "additionalOutputs": [{"output": "session_token", "credential": "session"}]
      }
    },
    {
      "name": "session",
      "required": false,
      "tokenGrant": {
        "grantType": "TOKEN_EXCHANGE",
        "tokenEndpoint": "https://login.example.com/token",
        "audience": "api://github",
        "jwtSvidAudience": "https://login.example.com",
        "clientAssertionType": "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe",
        "requestedTokenType": "urn:ietf:params:oauth:token-type:access_token",
        "scopes": ["read"],
        "cacheTtl": "90s",
        "subjectToken": {"source": "provider_credential", "credential": "api_token", "subjectTokenType": "urn:x"},
        "audienceOverrides": [
          {"host": "api.github.com", "port": 443, "path": "/graphql", "audience": "api://graphql", "scopes": ["gql"]}
        ]
      },
      "tokenGrantOwners": ["gateway-derived-owner"]
    }
  ],
  "files": [
    {"path": "hosts.yml", "content": "host: {{config.host}}\n", "envVar": "GH_CONFIG_FILE"},
    {"path": "notice.txt", "content": "managed by the platform team\n"}
  ],
  "networkEndpoints": [
    {
      "host": "api.github.com",
      "port": 443,
      "protocol": "rest",
      "access": "NETWORK_ACCESS_PRESET_READ_ONLY",
      "enforcement": "NETWORK_ENFORCEMENT_MODE_ENFORCE"
    },
    {
      "host": "github.com",
      "ports": [22, 443],
      "protocol": "rest",
      "rules": [{"allow": {"method": "GET", "path": "/repos/**", "query": {"ref": {"any": ["main", "release-*"]}}}}],
      "denyRules": [{"method": "DELETE", "path": "/repos/**"}]
    }
  ],
  "binaries": [{"path": "/usr/bin/gh"}, {"path": "/usr/bin/git"}],
  "discovery": {"credentials": ["api_token"]}
}`

// asWritten is the profile a client sends back after reading it: the JSON the
// BFF returned, without the host:port summaries.
func asWritten(t *testing.T, profile map[string]any) string {
	t.Helper()
	delete(profile, "endpoints")
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// The regression this file exists for. Updating a profile replaces it, and the
// update used to be built from a body that had no room for most of a profile:
// saving one removed its header name, refresh, token grant, binaries,
// discovery, annotations and everything about its endpoints but a host and a
// port. A profile read and sent back now reaches the gateway as it was read.
func TestUpdatingAProfileSendsBackAllOfIt(t *testing.T) {
	for _, base := range []string{"/workspaces/team-a/provider-profiles", "/provider-profiles"} {
		t.Run(base, func(t *testing.T) {
			store := &fakeProfileStore{profiles: []*pb.ProviderProfile{storedProfile()}}
			handler, _, _ := profilesHandler(store)

			read := decodeObject(t, serveProfiles(handler, http.MethodGet, base+"/github", ""))
			body := `{"profile":` + asWritten(t, read) + `,"expectedResourceVersion":7}`
			w := serveProfiles(handler, http.MethodPut, base+"/github", body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}

			if len(store.items) != 1 || store.expected[0] != 7 {
				t.Fatalf("the store was sent %d profiles at versions %v, want one at 7", len(store.items), store.expected)
			}
			if got := store.items[0].GetProfile(); !proto.Equal(got, storedProfile()) {
				t.Errorf("the profile that was read is not the profile that was written.\nread:    %s\nwritten: %s",
					protojson.Format(storedProfile()), protojson.Format(got))
			}
			answer := decodeObject(t, w)
			if answer["updated"] != true || answer["profile"] == nil {
				t.Errorf("answer = %v", answer)
			}
		})
	}
}

// Importing and linting carry the whole profile too, with the label that
// tells the gateway's diagnostics which file a profile came from.
func TestImportAndLintSendTheWholeProfile(t *testing.T) {
	store := &fakeProfileStore{
		profiles:    []*pb.ProviderProfile{storedProfile()},
		diagnostics: []*pb.ProviderProfileDiagnostic{{Source: "github.yaml", ProfileId: "github", Field: "id", Message: "shadows a platform-scoped profile", Severity: "warning"}},
	}
	handler, _, _ := profilesHandler(store)
	read := decodeObject(t, serveProfiles(handler, http.MethodGet, "/provider-profiles/github", ""))
	read["importSource"] = "github.yaml"
	body := `{"profiles":[` + asWritten(t, read) + `]}`

	imported := serveProfiles(handler, http.MethodPost, "/provider-profiles", body)
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status = %d; body: %s", imported.Code, imported.Body.String())
	}
	var answer struct {
		Diagnostics []map[string]string `json:"diagnostics"`
		Profiles    []map[string]any    `json:"profiles"`
		Imported    bool                `json:"imported"`
	}
	if err := json.NewDecoder(imported.Body).Decode(&answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A warning does not stop an import: the profile is stored and the
	// warning is still returned to be shown.
	wantDiagnostics := []map[string]string{{
		"source": "github.yaml", "profileId": "github", "field": "id",
		"message": "shadows a platform-scoped profile", "severity": "warning",
	}}
	if !answer.Imported || !reflect.DeepEqual(answer.Diagnostics, wantDiagnostics) {
		t.Errorf("import answer = imported %v with %v", answer.Imported, answer.Diagnostics)
	}
	if len(answer.Profiles) != 1 || answer.Profiles[0]["networkEndpoints"] == nil {
		t.Errorf("imported profiles = %v", answer.Profiles)
	}

	linted := serveProfiles(handler, http.MethodPost, "/provider-profiles/lint", body)
	if linted.Code != http.StatusOK || decodeObject(t, linted)["valid"] != true {
		t.Errorf("lint status = %d", linted.Code)
	}

	if len(store.items) != 2 {
		t.Fatalf("the store was sent %d items, want 2", len(store.items))
	}
	for i, item := range store.items {
		if item.GetSource() != "github.yaml" || !proto.Equal(item.GetProfile(), storedProfile()) {
			t.Errorf("item %d = %s", i, protojson.Format(item))
		}
	}
}

func TestProfileWritesRefuseWhatIsNotAProfile(t *testing.T) {
	bodies := map[string]string{
		"both forms of endpoint": `{"id":"p","displayName":"P","category":"OTHER","inferenceCapable":false,
			"endpoints":[{"host":"a.example","port":443}],"networkEndpoints":[{"host":"a.example"}]}`,
		"an endpoint field that does not exist": `{"id":"p","displayName":"P","category":"OTHER","inferenceCapable":false,
			"networkEndpoints":[{"host":"a.example","acces":"NETWORK_ACCESS_PRESET_FULL"}]}`,
		"a refresh strategy that does not exist": `{"id":"p","displayName":"P","category":"OTHER","inferenceCapable":false,
			"credentials":[{"name":"t","required":true,"refresh":{"strategy":"OAUTH3"}}]}`,
		"a duration that is not one": `{"id":"p","displayName":"P","category":"OTHER","inferenceCapable":false,
			"credentials":[{"name":"t","required":true,"tokenGrant":{"tokenEndpoint":"https://x","grantType":"TOKEN_EXCHANGE","cacheTtl":"5m"}}]}`,
	}
	for name, profile := range bodies {
		t.Run(name, func(t *testing.T) {
			store := &fakeProfileStore{}
			handler, _, _ := profilesHandler(store)
			requests := []struct{ method, path, body string }{
				{http.MethodPost, "/workspaces/team-a/provider-profiles", `{"profiles":[` + profile + `]}`},
				{http.MethodPost, "/workspaces/team-a/provider-profiles/lint", `{"profiles":[` + profile + `]}`},
				{http.MethodPut, "/workspaces/team-a/provider-profiles/p", `{"profile":` + profile + `,"expectedResourceVersion":1}`},
			}
			for _, request := range requests {
				w := serveProfiles(handler, request.method, request.path, request.body)
				if w.Code != http.StatusBadRequest {
					t.Errorf("%s %s: status = %d, want 400; body: %s", request.method, request.path, w.Code, w.Body.String())
					continue
				}
				if answer := decodeObject(t, w); answer["code"] != "invalid_profile" || answer["message"] == "" {
					t.Errorf("%s %s: answer = %v, want invalid_profile with a message", request.method, request.path, answer)
				}
			}
			if len(store.calls) != 0 {
				t.Errorf("the store was called (%v) with a profile the BFF could not read", store.calls)
			}
		})
	}
}

// What the gateway decides is passed on: who may touch the platform scope, a
// stale version, a profile it does not have.
func TestProfileRoutesPassOnTheGatewaysAnswer(t *testing.T) {
	tests := []struct {
		err        error
		name       string
		wantCode   string
		wantStatus int
	}{
		{name: "not a platform admin", err: status.Error(codes.PermissionDenied, "platform admin required"), wantStatus: http.StatusForbidden, wantCode: "permission_denied"},
		{name: "stale version", err: status.Error(codes.Aborted, "provider profile was modified concurrently"), wantStatus: http.StatusConflict, wantCode: "conflict"},
		{name: "no such profile", err: status.Error(codes.NotFound, "provider profile not found"), wantStatus: http.StatusNotFound, wantCode: "not_found"},
		// A failed precondition is a conflict on these routes, as the SDK's
		// typed error for it is.
		{name: "a workspace that is being deleted", err: status.Error(codes.FailedPrecondition, "workspace is being deleted"), wantStatus: http.StatusConflict, wantCode: "conflict"},
		{name: "through the SDK", err: &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "platform admin required"}, wantStatus: http.StatusForbidden, wantCode: "permission_denied"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _ := profilesHandler(&fakeProfileStore{err: tc.err})
			requests := []struct{ method, path, body string }{
				{http.MethodGet, "/provider-profiles", ""},
				{http.MethodGet, "/provider-profiles/github", ""},
				{http.MethodPost, "/provider-profiles", `{"profiles":[` + minimalProfile + `]}`},
				{http.MethodPost, "/provider-profiles/lint", `{"profiles":[` + minimalProfile + `]}`},
				{http.MethodPut, "/provider-profiles/github", `{"profile":` + minimalProfile + `}`},
			}
			for _, request := range requests {
				w := serveProfiles(handler, request.method, request.path, request.body)
				if w.Code != tc.wantStatus {
					t.Errorf("%s %s: status = %d, want %d", request.method, request.path, w.Code, tc.wantStatus)
					continue
				}
				if answer := decodeObject(t, w); answer["code"] != tc.wantCode {
					t.Errorf("%s %s: code = %v, want %s", request.method, request.path, answer["code"], tc.wantCode)
				}
			}
		})
	}
}

// A refused import or update is not an HTTP error: the answer says it was not
// applied and carries the gateway's diagnostics, which the UI shows.
func TestRefusedProfileWritesReturnTheDiagnostics(t *testing.T) {
	store := &fakeProfileStore{
		refuse: true,
		diagnostics: []*pb.ProviderProfileDiagnostic{
			{Source: "github.yaml", ProfileId: "github", Field: "endpoints[0]", Message: "credentialed endpoint requires L7 inspection", Severity: "error"},
		},
	}
	handler, _, _ := profilesHandler(store)

	imported := serveProfiles(handler, http.MethodPost, "/provider-profiles", `{"profiles":[`+minimalProfile+`]}`)
	updated := serveProfiles(handler, http.MethodPut, "/provider-profiles/github", `{"profile":`+minimalProfile+`,"expectedResourceVersion":7}`)
	linted := serveProfiles(handler, http.MethodPost, "/provider-profiles/lint", `{"profiles":[`+minimalProfile+`]}`)
	if imported.Code != http.StatusCreated || updated.Code != http.StatusOK || linted.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d, %d", imported.Code, updated.Code, linted.Code)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{"import": imported, "update": updated, "lint": linted} {
		answer := decodeObject(t, w)
		wantDiagnostics := []any{map[string]any{
			"source": "github.yaml", "profileId": "github", "field": "endpoints[0]",
			"message": "credentialed endpoint requires L7 inspection", "severity": "error",
		}}
		if !reflect.DeepEqual(answer["diagnostics"], wantDiagnostics) {
			t.Errorf("%s diagnostics = %v", name, answer["diagnostics"])
		}
		if answer["imported"] == true || answer["updated"] == true || answer["valid"] == true {
			t.Errorf("%s answer = %v, want it to say the write was refused", name, answer)
		}
		if _, present := answer["profile"]; present {
			t.Errorf("%s answer carries a profile that was not written: %v", name, answer["profile"])
		}
	}
}

// Without a profile store the handler has the SDK, which carries a host, a
// port and a protocol of each endpoint. A profile read that way says so by
// having no networkEndpoints, and carries everything else.
func TestProfilesReadThroughTheSDK(t *testing.T) {
	handler, sdk, _ := profilesHandler(nil)
	sdk.providers.profiles.getFn = func(_ context.Context, _, id string) (*openshell.ProviderProfile, error) {
		return &openshell.ProviderProfile{
			ID:          id,
			DisplayName: "GitHub",
			Category:    openshell.ProfileCategorySourceControl,
			Annotations: map[string]string{"example.com/owner": "platform-team"},
			Credentials: []openshell.ProfileCredential{{Name: "api_token", HeaderName: "authorization"}},
			Endpoints:   []openshell.NetworkEndpoint{{Host: "api.github.com", Port: 443, Protocol: "rest"}},
			Binaries:    []openshell.NetworkBinary{{Path: "/usr/bin/gh"}},
			Discovery:   openshell.ProfileDiscovery{Credentials: []string{"api_token"}},
		}, nil
	}

	read := decodeObject(t, serveProfiles(handler, http.MethodGet, "/workspaces/team-a/provider-profiles/github", ""))
	want := map[string]any{
		"id":               "github",
		"displayName":      "GitHub",
		"category":         "SOURCE_CONTROL",
		"inferenceCapable": false,
		"resourceVersion":  float64(0),
		"annotations":      map[string]any{"example.com/owner": "platform-team"},
		"credentials":      []any{map[string]any{"name": "api_token", "required": false, "headerName": "authorization"}},
		"endpoints":        []any{"api.github.com:443"},
		"binaries":         []any{map[string]any{"path": "/usr/bin/gh"}},
		"discovery":        map[string]any{"credentials": []any{"api_token"}},
	}
	if !reflect.DeepEqual(read, want) {
		t.Errorf("profile = %v\nwant      %v: everything the SDK carries, and no networkEndpoints", read, want)
	}
}

// Through the SDK a profile is written when the SDK can carry it, and refused
// when one of its endpoints says more than the SDK would send.
func TestProfilesWrittenThroughTheSDK(t *testing.T) {
	handler, sdk, _ := profilesHandler(nil)
	var imported []openshell.ProfileImportItem
	sdk.providers.profiles.importFn = func(_ context.Context, _ string, items []openshell.ProfileImportItem) (*openshell.ImportResult, error) {
		imported = append(imported, items...)
		return &openshell.ImportResult{Imported: true, Profiles: []openshell.ProviderProfile{items[0].Profile}}, nil
	}

	plain := `{"id":"custom","displayName":"Custom","category":"DATA","inferenceCapable":false,"importSource":"custom.yaml",
		"credentials":[{"name":"api_key","required":true,"headerName":"x-api-key"}],
		"networkEndpoints":[{"host":"api.example.com","port":443,"protocol":"rest"}],
		"binaries":[{"path":"/usr/bin/curl"}]}`
	if w := serveProfiles(handler, http.MethodPost, "/workspaces/team-a/provider-profiles", `{"profiles":[`+plain+`]}`); w.Code != http.StatusCreated {
		t.Fatalf("import status = %d; body: %s", w.Code, w.Body.String())
	}
	wantImported := []openshell.ProfileImportItem{{
		Source: "custom.yaml",
		Profile: openshell.ProviderProfile{
			ID:          "custom",
			DisplayName: "Custom",
			Category:    openshell.ProfileCategoryData,
			Credentials: []openshell.ProfileCredential{{Name: "api_key", Required: true, HeaderName: "x-api-key"}},
			Endpoints:   []openshell.NetworkEndpoint{{Host: "api.example.com", Port: 443, Protocol: "rest"}},
			Binaries:    []openshell.NetworkBinary{{Path: "/usr/bin/curl"}},
		},
	}}
	if !reflect.DeepEqual(imported, wantImported) {
		t.Fatalf("the SDK was given %+v\nwant              %+v", imported, wantImported)
	}

	ruled := strings.Replace(plain, `"protocol":"rest"`, `"protocol":"rest","access":"NETWORK_ACCESS_PRESET_READ_ONLY"`, 1)
	w := serveProfiles(handler, http.MethodPost, "/workspaces/team-a/provider-profiles", `{"profiles":[`+ruled+`]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	answer := decodeObject(t, w)
	message, _ := answer["message"].(string)
	if answer["code"] != "invalid_profile" || !strings.Contains(message, "endpoints[0]") {
		t.Errorf("answer = %v", answer)
	}
	if len(imported) != 1 {
		t.Errorf("the SDK was given a profile whose endpoint it would have stored without its access preset")
	}
}
