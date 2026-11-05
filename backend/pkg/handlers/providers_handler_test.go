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
	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
	"github.com/ai-openshell/console/backend/pkg/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListProviders(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.listFn = func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.Provider, error) {
		return []*openshell.Provider{
			{
				Name: "claude-prov",
				Type: "claude",
				Spec: openshell.ProviderSpec{
					Credentials: map[string]string{"api_key": "secret"},
					Config:      map[string]string{"region": "us"},
				},
			},
		}, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/providers", handler.ListProviders)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/providers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("got %d providers, want 1", len(body))
	}
	raw, _ := json.Marshal(body[0])
	if strings.Contains(string(raw), "secret") {
		t.Errorf("response leaked credential value: %s", raw)
	}
	creds, ok := body[0]["credentialNames"].([]any)
	if !ok {
		t.Fatal("credentialNames is not an array")
	}
	if len(creds) != 1 || creds[0] != "api_key" {
		t.Errorf("credentialNames = %v, want [api_key]", creds)
	}
}

func TestListProvidersUnavailable(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.listFn = func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.Provider, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "gateway down"}
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/providers", handler.ListProviders)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/providers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestCreateProvider(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		createFn   func(ctx context.Context, workspace string, provider *openshell.Provider) (*openshell.Provider, error)
		wantCode   string
		wantStatus int
	}{
		{
			name:       "success",
			body:       `{"name":"my-provider","type":"claude","credentials":{"api_key":"sk-123"}}`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "missing name",
			body:       `{"name":"","type":"claude"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_provider",
		},
		{
			name:       "missing type",
			body:       `{"name":"my-provider","type":""}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_provider",
		},
		{
			name:       "malformed JSON",
			body:       `{bad}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_body",
		},
		{
			name: "already exists",
			body: `{"name":"my-provider","type":"claude"}`,
			createFn: func(_ context.Context, _ string, _ *openshell.Provider) (*openshell.Provider, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorAlreadyExists, Message: "exists"}
			},
			wantStatus: http.StatusConflict,
			wantCode:   "already_exists",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.providers.createFn = tc.createFn
			handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/providers", handler.CreateProvider)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/providers", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var errResp map[string]any
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if errResp["code"] != tc.wantCode {
					t.Errorf("code = %q, want %q", errResp["code"], tc.wantCode)
				}
			}
		})
	}
}

// The create body mirrors the gateway's Provider message, so what the browser
// sends is what the SDK gets: the BFF neither renames a credential key nor
// works out a profile scope the request did not name.
func TestCreateProviderForwardsTheRequestUnchanged(t *testing.T) {
	tests := []struct {
		name                 string
		body                 string
		wantProfileWorkspace string
	}{
		{
			name: "profile scope named",
			body: `{"name":"my-openai","type":"openai","profileWorkspace":"team-a",` +
				`"credentials":{"OPENAI_API_KEY":"sk-123"},"config":{"region":"us"},"labels":{"team":"a"}}`,
			wantProfileWorkspace: "team-a",
		},
		{
			name: "no profile scope is the platform scope",
			body: `{"name":"my-openai","type":"openai",` +
				`"credentials":{"OPENAI_API_KEY":"sk-123"},"config":{"region":"us"},"labels":{"team":"a"}}`,
			wantProfileWorkspace: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotWorkspace string
			var got *openshell.Provider
			sdk := &mockSDK{}
			sdk.providers.createFn = func(_ context.Context, workspace string, provider *openshell.Provider) (*openshell.Provider, error) {
				gotWorkspace, got = workspace, provider
				return provider, nil
			}
			handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/providers", handler.CreateProvider)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/team-a/providers", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
			}
			if got == nil {
				t.Fatal("the SDK was not asked to create a provider")
			}
			if gotWorkspace != "team-a" || got.Name != "my-openai" || got.Type != "openai" {
				t.Errorf("created %q of type %q in %q, want my-openai of type openai in team-a", got.Name, got.Type, gotWorkspace)
			}
			if got.Spec.ProfileWorkspace != tc.wantProfileWorkspace {
				t.Errorf("profile workspace = %q, want %q", got.Spec.ProfileWorkspace, tc.wantProfileWorkspace)
			}
			if len(got.Spec.Credentials) != 1 || got.Spec.Credentials["OPENAI_API_KEY"] != "sk-123" {
				t.Errorf("credentials = %v, want the one key the request named", got.Spec.Credentials)
			}
			if got.Spec.Config["region"] != "us" || got.Labels["team"] != "a" {
				t.Errorf("config = %v, labels = %v; want region=us and team=a", got.Spec.Config, got.Labels)
			}
			if strings.Contains(w.Body.String(), "sk-123") {
				t.Errorf("response leaked the credential value: %s", w.Body.String())
			}
		})
	}
}

func TestGetProvider(t *testing.T) {
	tests := []struct {
		getFn      func(ctx context.Context, workspace, name string) (*openshell.Provider, error)
		name       string
		wantStatus int
	}{
		{
			name: "success",
			getFn: func(_ context.Context, _, name string) (*openshell.Provider, error) {
				return &openshell.Provider{Name: name, Type: "claude"}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "handles-only returns credentialNames",
			getFn: func(_ context.Context, _, name string) (*openshell.Provider, error) {
				return &openshell.Provider{
					Name: name,
					Type: "claude",
					Spec: openshell.ProviderSpec{
						CredentialHandles: map[string]types.CredentialHandle{
							"api_key": {Driver: "vault", Handle: "vault://must-not-leak"},
						},
					},
				}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "not found",
			getFn: func(_ context.Context, _, _ string) (*openshell.Provider, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "provider not found"}
			},
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.providers.getFn = tc.getFn
			handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
			r := chi.NewRouter()
			r.Get("/workspaces/{workspace}/providers/{name}", handler.GetProvider)

			req := httptest.NewRequest(http.MethodGet, "/workspaces/default/providers/claude-prov", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if tc.name == "handles-only returns credentialNames" && w.Code == http.StatusOK {
				var body map[string]any
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				names, _ := body["credentialNames"].([]any)
				if len(names) != 1 || names[0] != "api_key" {
					t.Errorf("credentialNames = %v, want [api_key]", body["credentialNames"])
				}
				raw, _ := json.Marshal(body)
				if strings.Contains(string(raw), "must-not-leak") {
					t.Errorf("leaked credential handle: %s", raw)
				}
			}
		})
	}
}

// gatewayProvider is a provider the way the SDK returns it from a gateway:
// the gateway clears the credential handles before answering and the SDK drops
// the redacted credentials map, so neither says which credentials it holds.
func gatewayProvider(name string) *openshell.Provider {
	return &openshell.Provider{
		Name:            name,
		Workspace:       "default",
		Type:            "claude",
		ResourceVersion: 4,
		Spec:            openshell.ProviderSpec{Config: map[string]string{"region": "us"}},
	}
}

// fakeCredentialKeys is a ProviderCredentialKeyReader over fixed answers.
type fakeCredentialKeys struct {
	err       error
	byName    map[string][]string
	bySandbox map[string][]string
	calls     []string
}

func (f *fakeCredentialKeys) ProviderCredentialKeys(_ context.Context, workspace, name string) ([]string, error) {
	f.calls = append(f.calls, "get "+workspace+"/"+name)
	return f.byName[name], f.err
}

func (f *fakeCredentialKeys) ListProviderCredentialKeys(_ context.Context, workspace string) (map[string][]string, error) {
	f.calls = append(f.calls, "list "+workspace)
	return f.byName, f.err
}

func (f *fakeCredentialKeys) ListSandboxProviderCredentialKeys(_ context.Context, workspace, sandboxName string) (map[string][]string, error) {
	f.calls = append(f.calls, "sandbox "+workspace+"/"+sandboxName)
	return f.bySandbox, f.err
}

func credentialNamesOf(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, present := body["credentialNames"]
	if !present {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("credentialNames is %T, want an array", raw)
	}
	names := make([]string, 0, len(list))
	for _, item := range list {
		name, _ := item.(string)
		names = append(names, name)
	}
	return names
}

// The gateway merges an update into the provider it has stored, so the BFF
// sends only what the request names and does not build the update from a
// provider it read first. The gateway answers every read with the credentials
// redacted to a literal and would store that literal if it came back; today
// the SDK drops the credentials it reads, which is all that hid the hazard.
func TestUpdateProviderSendsOnlyWhatTheRequestNames(t *testing.T) {
	tests := []struct {
		wantCredentials map[string]string
		wantConfig      map[string]string
		wantExpiresAtMs map[string]int64
		name            string
		body            string
	}{
		{
			name:       "config only leaves the credentials out",
			body:       `{"config":{"region":"eu","zone":""}}`,
			wantConfig: map[string]string{"region": "eu", "zone": ""},
		},
		{
			name:            "credentials only leaves the config out",
			body:            `{"credentials":{"OPENAI_API_KEY":"sk-rotated"}}`,
			wantCredentials: map[string]string{"OPENAI_API_KEY": "sk-rotated"},
		},
		{
			name:            "expiry only",
			body:            `{"credentialExpiresAtMs":{"OPENAI_API_KEY":1893456000000}}`,
			wantExpiresAtMs: map[string]int64{"OPENAI_API_KEY": 1893456000000},
		},
		{
			name:            "all three",
			body:            `{"credentials":{"OPENAI_API_KEY":"sk-rotated"},"config":{"region":"eu"},"credentialExpiresAtMs":{"OPENAI_API_KEY":1893456000000}}`,
			wantCredentials: map[string]string{"OPENAI_API_KEY": "sk-rotated"},
			wantConfig:      map[string]string{"region": "eu"},
			wantExpiresAtMs: map[string]int64{"OPENAI_API_KEY": 1893456000000},
		},
		{
			name: "an empty body changes nothing",
			body: `{}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got *openshell.Provider
			var gotWorkspace string
			sdk := &mockSDK{}
			sdk.providers.getFn = func(_ context.Context, _, _ string) (*openshell.Provider, error) {
				t.Error("the provider was read before the update; an update must not be built from a read")
				return gatewayProvider("claude-prov"), nil
			}
			sdk.providers.updateFn = func(_ context.Context, workspace string, provider *openshell.Provider) (*openshell.Provider, error) {
				gotWorkspace, got = workspace, provider
				return gatewayProvider(provider.Name), nil
			}
			handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
			r := chi.NewRouter()
			r.Put("/workspaces/{workspace}/providers/{name}", handler.UpdateProvider)

			req := httptest.NewRequest(http.MethodPut, "/workspaces/team-a/providers/claude-prov", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if got == nil {
				t.Fatal("the SDK was not asked to update the provider")
			}
			if gotWorkspace != "team-a" || got.Name != "claude-prov" {
				t.Errorf("updated %q in %q, want claude-prov in team-a", got.Name, gotWorkspace)
			}
			if !reflect.DeepEqual(got.Spec.Credentials, tc.wantCredentials) {
				t.Errorf("credentials = %v, want %v", got.Spec.Credentials, tc.wantCredentials)
			}
			if !reflect.DeepEqual(got.Spec.Config, tc.wantConfig) {
				t.Errorf("config = %v, want %v", got.Spec.Config, tc.wantConfig)
			}
			if len(got.Spec.CredentialExpiresAt) != len(tc.wantExpiresAtMs) {
				t.Errorf("credential expiries = %v, want %v", got.Spec.CredentialExpiresAt, tc.wantExpiresAtMs)
			}
			for key, ms := range tc.wantExpiresAtMs {
				if at := got.Spec.CredentialExpiresAt[key]; at.UnixMilli() != ms {
					t.Errorf("credential expiry of %s = %d, want %d", key, at.UnixMilli(), ms)
				}
			}
			// Nothing a read would have filled in travels with the update.
			if len(got.Spec.CredentialHandles) != 0 || got.Type != "" || got.Spec.ProfileWorkspace != "" || got.ResourceVersion != 0 {
				t.Errorf("update carries more than the request named: type %q, profile workspace %q, resource version %d, handles %v",
					got.Type, got.Spec.ProfileWorkspace, got.ResourceVersion, got.Spec.CredentialHandles)
			}
		})
	}
}

// An expiry of zero or less is what a JSON null decodes to, and the gateway
// takes it as a credential that expired in 1970 and withholds it. It is
// refused before it reaches the gateway.
func TestUpdateProviderRefusesAnExpiryThatIsNotATime(t *testing.T) {
	for _, body := range []string{
		`{"credentialExpiresAtMs":{"OPENAI_API_KEY":0}}`,
		`{"credentialExpiresAtMs":{"OPENAI_API_KEY":null}}`,
		`{"credentialExpiresAtMs":{"OPENAI_API_KEY":-1}}`,
		`{"credentials":{"OPENAI_API_KEY":"sk"},"credentialExpiresAtMs":{"OPENAI_API_KEY":1893456000000,"OTHER":0}}`,
	} {
		sdk := &mockSDK{}
		sdk.providers.updateFn = func(_ context.Context, _ string, _ *openshell.Provider) (*openshell.Provider, error) {
			t.Errorf("%s reached the gateway", body)
			return gatewayProvider("claude-prov"), nil
		}
		handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
		r := chi.NewRouter()
		r.Put("/workspaces/{workspace}/providers/{name}", handler.UpdateProvider)

		req := httptest.NewRequest(http.MethodPut, "/workspaces/default/providers/claude-prov", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body: %s", body, w.Code, w.Body.String())
		}
	}
}

func TestUpdateProviderNotFound(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.updateFn = func(_ context.Context, _ string, _ *openshell.Provider) (*openshell.Provider, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "provider not found"}
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Put("/workspaces/{workspace}/providers/{name}", handler.UpdateProvider)

	req := httptest.NewRequest(http.MethodPut, "/workspaces/default/providers/missing", strings.NewReader(`{"config":{"region":"eu"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// keyedProvidersHandler is a ProvidersHandler over an SDK that answers the way
// a gateway does, with no credential keys, and the given key reader (nil for
// none).
func keyedProvidersHandler(keys services.ProviderCredentialKeyReader) *ProvidersHandler {
	sdk := &mockSDK{}
	sdk.providers.listFn = func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.Provider, error) {
		return []*openshell.Provider{gatewayProvider("with-keys"), gatewayProvider("without-keys")}, nil
	}
	sdk.providers.getFn = func(_ context.Context, _, name string) (*openshell.Provider, error) {
		return gatewayProvider(name), nil
	}
	sdk.providers.createFn = func(_ context.Context, _ string, provider *openshell.Provider) (*openshell.Provider, error) {
		return gatewayProvider(provider.Name), nil
	}
	sdk.providers.updateFn = func(_ context.Context, _ string, provider *openshell.Provider) (*openshell.Provider, error) {
		return gatewayProvider(provider.Name), nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	if keys != nil {
		handler.SetCredentialKeyReader(keys)
	}
	return handler
}

func serveProviders(handler *ProvidersHandler, method, path, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/providers", handler.ListProviders)
	r.Post("/workspaces/{workspace}/providers", handler.CreateProvider)
	r.Get("/workspaces/{workspace}/providers/{name}", handler.GetProvider)
	r.Put("/workspaces/{workspace}/providers/{name}", handler.UpdateProvider)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// heldKeys is a key reader in which the provider "with-keys" holds two
// credentials and no other provider holds any.
func heldKeys() *fakeCredentialKeys {
	return &fakeCredentialKeys{byName: map[string][]string{
		"with-keys": {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
	}}
}

var wantHeldKeys = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

func decodeObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// The SDK does not say which credentials a provider holds, so the names come
// from the reader, joined to each provider by name.
func TestListProvidersNamesCredentialsFromTheKeyReader(t *testing.T) {
	reader := heldKeys()
	w := serveProviders(keyedProvidersHandler(reader), http.MethodGet, "/workspaces/team-a/providers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("got %d providers, want 2", len(body))
	}
	if got := credentialNamesOf(t, body[0]); !reflect.DeepEqual(got, wantHeldKeys) {
		t.Errorf("credentialNames of with-keys = %v, want %v", got, wantHeldKeys)
	}
	if got := credentialNamesOf(t, body[1]); len(got) != 0 {
		t.Errorf("credentialNames of without-keys = %v, want none", got)
	}
	if !reflect.DeepEqual(reader.calls, []string{"list team-a"}) {
		t.Errorf("key reader calls = %v, want one list of team-a", reader.calls)
	}
}

func TestGetProviderNamesCredentialsFromTheKeyReader(t *testing.T) {
	reader := heldKeys()
	w := serveProviders(keyedProvidersHandler(reader), http.MethodGet, "/workspaces/team-a/providers/with-keys", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := credentialNamesOf(t, decodeObject(t, w)); !reflect.DeepEqual(got, wantHeldKeys) {
		t.Errorf("credentialNames = %v, want %v", got, wantHeldKeys)
	}
	if !reflect.DeepEqual(reader.calls, []string{"get team-a/with-keys"}) {
		t.Errorf("key reader calls = %v, want one get of team-a/with-keys", reader.calls)
	}
}

// A create and an update answer with the keys the provider holds afterwards.
func TestProviderWritesAnswerWithTheKeysNowHeld(t *testing.T) {
	writes := []struct {
		method, path, body string
		wantStatus         int
	}{
		{http.MethodPost, "/workspaces/team-a/providers", `{"name":"with-keys","type":"claude","credentials":{"ANTHROPIC_API_KEY":"sk"}}`, http.StatusCreated},
		{http.MethodPut, "/workspaces/team-a/providers/with-keys", `{"config":{"region":"eu"}}`, http.StatusOK},
	}
	for _, write := range writes {
		t.Run(write.method, func(t *testing.T) {
			w := serveProviders(keyedProvidersHandler(heldKeys()), write.method, write.path, write.body)
			if w.Code != write.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, write.wantStatus, w.Body.String())
			}
			if got := credentialNamesOf(t, decodeObject(t, w)); !reflect.DeepEqual(got, wantHeldKeys) {
				t.Errorf("credentialNames = %v, want %v", got, wantHeldKeys)
			}
		})
	}
}

// A read answers with what the gateway says or fails. It never passes a
// provider off as holding no credentials because the keys were not read. The
// key reader is a raw gRPC client, so its errors are gRPC statuses that never
// passed through the SDK, and they still map like the SDK's own.
func TestProviderReadsFailWhenTheKeysCannotBeRead(t *testing.T) {
	tests := []struct {
		err        error
		name       string
		path       string
		wantCode   string
		wantStatus int
	}{
		{
			name: "list, gateway unreachable", path: "/workspaces/team-a/providers",
			err:        status.Error(codes.Unavailable, "connection refused"),
			wantStatus: http.StatusBadGateway, wantCode: "gateway_unavailable",
		},
		{
			name: "get, gateway unreachable", path: "/workspaces/team-a/providers/with-keys",
			err:        status.Error(codes.Unavailable, "connection refused"),
			wantStatus: http.StatusBadGateway, wantCode: "gateway_unavailable",
		},
		{
			// Deleted between the SDK read and the key read.
			name: "get, provider gone", path: "/workspaces/team-a/providers/with-keys",
			err:        status.Error(codes.NotFound, "provider not found"),
			wantStatus: http.StatusNotFound, wantCode: "not_found",
		},
		{
			name: "list, not allowed", path: "/workspaces/team-a/providers",
			err:        status.Error(codes.PermissionDenied, "workspace role required"),
			wantStatus: http.StatusForbidden, wantCode: "permission_denied",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := heldKeys()
			reader.err = tc.err
			w := serveProviders(keyedProvidersHandler(reader), http.MethodGet, tc.path, "")
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if code := decodeObject(t, w)["code"]; code != tc.wantCode {
				t.Errorf("code = %v, want %q", code, tc.wantCode)
			}
		})
	}
}

// A write that went through must not read as one that failed.
func TestProviderWriteSucceedsWhenTheKeysCannotBeReadBack(t *testing.T) {
	reader := heldKeys()
	reader.err = status.Error(codes.Unavailable, "connection refused")
	w := serveProviders(keyedProvidersHandler(reader), http.MethodPut, "/workspaces/team-a/providers/with-keys", `{"config":{"region":"eu"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := credentialNamesOf(t, decodeObject(t, w)); len(got) != 0 {
		t.Errorf("credentialNames = %v, want none: they could not be read", got)
	}
}

// Without a reader a provider has the names the SDK carries, which against a
// gateway is none.
func TestProviderWithoutAKeyReaderHasNoCredentialNames(t *testing.T) {
	w := serveProviders(keyedProvidersHandler(nil), http.MethodGet, "/workspaces/team-a/providers/with-keys", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := credentialNamesOf(t, decodeObject(t, w)); len(got) != 0 {
		t.Errorf("credentialNames = %v, want none", got)
	}
}

func TestDeleteProvider(t *testing.T) {
	mock := &mockSDK{}
	handler := NewProvidersHandler(services.NewProviderService(mock.Providers()))
	r := chi.NewRouter()
	r.Delete("/workspaces/{workspace}/providers/{name}", handler.DeleteProvider)

	req := httptest.NewRequest(http.MethodDelete, "/workspaces/default/providers/claude-prov", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["deleted"] != true {
		t.Errorf("deleted = %v, want true", resp["deleted"])
	}
}

func TestConfigureProviderRefresh(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{
			name:       "success oauth2",
			body:       `{"credentialKey":"api_key","strategy":"oauth2-refresh-token"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "aws sts accepted",
			body:       `{"credentialKey":"role","strategy":"aws-sts-assume-role"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing key",
			body:       `{"credentialKey":"","strategy":"static"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "unknown strategy",
			body:       `{"credentialKey":"api_key","strategy":"nope"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_strategy",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			handler := NewProvidersHandler(services.NewProviderService(mock.Providers()))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/providers/{name}/refresh", handler.ConfigureProviderRefresh)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/providers/claude-prov/refresh", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var errResp map[string]any
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if errResp["code"] != tc.wantCode {
					t.Errorf("code = %q, want %q", errResp["code"], tc.wantCode)
				}
			}
		})
	}
}

func TestRotateAndDeleteProviderRefresh(t *testing.T) {
	mock := &mockSDK{}
	handler := NewProvidersHandler(services.NewProviderService(mock.Providers()))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/providers/{name}/refresh/rotate", handler.RotateProviderCredential)
	r.Delete("/workspaces/{workspace}/providers/{name}/refresh", handler.DeleteProviderRefresh)

	req := httptest.NewRequest(http.MethodPost, "/workspaces/default/providers/claude-prov/refresh/rotate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("rotate missing key status = %d, want 400", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/workspaces/default/providers/claude-prov/refresh", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("delete missing key status = %d, want 400", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/workspaces/default/providers/claude-prov/refresh/rotate", strings.NewReader(`{"credentialKey":"api_key"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("rotate status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/workspaces/default/providers/claude-prov/refresh?credentialKey=api_key", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("delete status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestGetProviderProfile(t *testing.T) {
	tests := []struct {
		getFn      func(ctx context.Context, workspace, id string) (*openshell.ProviderProfile, error)
		name       string
		wantStatus int
	}{
		{
			name: "success",
			getFn: func(_ context.Context, _, id string) (*openshell.ProviderProfile, error) {
				return &openshell.ProviderProfile{
					ID:          id,
					DisplayName: "Claude",
					Category:    openshell.ProfileCategoryInference,
				}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "not found",
			getFn: func(_ context.Context, _, _ string) (*openshell.ProviderProfile, error) {
				return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "profile not found"}
			},
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.providers.profiles.getFn = tc.getFn
			handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
			r := chi.NewRouter()
			r.Get("/workspaces/{workspace}/provider-profiles/{profileId}", handler.GetProviderProfile)

			req := httptest.NewRequest(http.MethodGet, "/workspaces/default/provider-profiles/claude", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}

func TestGetProviderProfileArgOrder(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.profiles.getFn = func(_ context.Context, workspace, id string) (*openshell.ProviderProfile, error) {
		if workspace != "default" || id != "claude" {
			t.Errorf("Get args workspace=%q id=%q, want default/claude", workspace, id)
		}
		return &openshell.ProviderProfile{ID: id, DisplayName: "Claude"}, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/provider-profiles/{profileId}", handler.GetProviderProfile)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/provider-profiles/claude", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestImportProviderProfiles(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{
			name:       "success",
			body:       `{"profiles":[{"id":"custom-llm","displayName":"Custom LLM","category":"INFERENCE","inferenceCapable":true,"credentials":[{"name":"api_key","required":true}]}]}`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "empty profiles",
			body:       `{"profiles":[]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "missing id",
			body:       `{"profiles":[{"id":"","displayName":"No ID","category":"OTHER"}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_profile",
		},
		{
			name:       "missing displayName",
			body:       `{"profiles":[{"id":"test","displayName":"","category":"OTHER"}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_profile",
		},
		{
			name:       "malformed JSON",
			body:       `{bad}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_body",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			handler := NewProvidersHandler(services.NewProviderService(mock.Providers()))
			r := chi.NewRouter()
			r.Post("/workspaces/{workspace}/provider-profiles", handler.ImportProviderProfiles)

			req := httptest.NewRequest(http.MethodPost, "/workspaces/default/provider-profiles", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var errResp map[string]any
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if errResp["code"] != tc.wantCode {
					t.Errorf("code = %q, want %q", errResp["code"], tc.wantCode)
				}
			}
		})
	}
}

func TestImportProviderProfilesResponse(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.profiles.importFn = func(_ context.Context, _ string, items []openshell.ProfileImportItem) (*openshell.ImportResult, error) {
		return &openshell.ImportResult{
			Profiles: []openshell.ProviderProfile{items[0].Profile},
			Imported: true,
			Diagnostics: []openshell.ProfileDiagnostic{
				{ProfileID: "custom-llm", Field: "credentials", Message: "consider adding env_vars", Severity: "warning"},
			},
		}, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/provider-profiles", handler.ImportProviderProfiles)

	body := `{"profiles":[{"id":"custom-llm","displayName":"Custom LLM","category":"INFERENCE","inferenceCapable":false}]}`
	req := httptest.NewRequest(http.MethodPost, "/workspaces/default/provider-profiles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["imported"] != true {
		t.Errorf("imported = %v, want true", resp["imported"])
	}
	profiles, ok := resp["profiles"].([]any)
	if !ok || len(profiles) != 1 {
		t.Fatalf("profiles length = %d, want 1", len(profiles))
	}
	diagnostics, ok := resp["diagnostics"].([]any)
	if !ok || len(diagnostics) != 1 {
		t.Fatalf("diagnostics length = %d, want 1", len(diagnostics))
	}
}

func TestUpdateProviderProfile(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{
			name:       "success",
			body:       `{"profile":{"id":"custom-llm","displayName":"Updated LLM","category":"INFERENCE","inferenceCapable":true},"expectedResourceVersion":1}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "id mismatch",
			body:       `{"profile":{"id":"wrong-id","displayName":"Mismatch","category":"OTHER","inferenceCapable":false}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "id_mismatch",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSDK{}
			handler := NewProvidersHandler(services.NewProviderService(mock.Providers()))
			r := chi.NewRouter()
			r.Put("/workspaces/{workspace}/provider-profiles/{profileId}", handler.UpdateProviderProfile)

			req := httptest.NewRequest(http.MethodPut, "/workspaces/default/provider-profiles/custom-llm", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var errResp map[string]any
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if errResp["code"] != tc.wantCode {
					t.Errorf("code = %q, want %q", errResp["code"], tc.wantCode)
				}
			}
		})
	}
}

func TestDeleteProviderProfile(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.profiles.deleteFn = func(_ context.Context, workspace, id string) (bool, error) {
		if workspace != "default" || id != "custom-llm" {
			t.Errorf("Delete args workspace=%q id=%q, want default/custom-llm", workspace, id)
		}
		return true, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Delete("/workspaces/{workspace}/provider-profiles/{profileId}", handler.DeleteProviderProfile)

	req := httptest.NewRequest(http.MethodDelete, "/workspaces/default/provider-profiles/custom-llm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["deleted"] != true {
		t.Errorf("deleted = %v, want true", resp["deleted"])
	}
}

func TestLintProviderProfiles(t *testing.T) {
	mock := &mockSDK{}
	handler := NewProvidersHandler(services.NewProviderService(mock.Providers()))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/provider-profiles/lint", handler.LintProviderProfiles)

	body := `{"profiles":[{"id":"test","displayName":"Test","category":"OTHER","inferenceCapable":false}]}`
	req := httptest.NewRequest(http.MethodPost, "/workspaces/default/provider-profiles/lint", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["valid"] != true {
		t.Errorf("valid = %v, want true", resp["valid"])
	}
}

func TestGetProviderRefreshStatus(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.refresh.getStatusFn = func(_ context.Context, _, _, _ string) ([]*openshell.RefreshStatus, error) {
		return []*openshell.RefreshStatus{
			{CredentialKey: "api_key", Strategy: openshell.RefreshStrategyStatic, Status: "active"},
		}, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/providers/{name}/refresh-status", handler.GetProviderRefreshStatus)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/providers/claude-prov/refresh-status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 || body[0]["credentialKey"] != "api_key" || body[0]["strategy"] != "STATIC" {
		t.Errorf("body = %v", body)
	}
}

// A failed refresh is returned with what the gateway says about it: what it
// needs, its stable failure code, and when it failed.
func TestGetProviderRefreshStatusSaysWhatAFailureNeeds(t *testing.T) {
	failedAt := time.UnixMilli(1_900_000_000_000)
	sdk := &mockSDK{}
	sdk.providers.refresh.getStatusFn = func(_ context.Context, _, _, _ string) ([]*openshell.RefreshStatus, error) {
		return []*openshell.RefreshStatus{
			{
				CredentialKey:        "GOOGLE_ACCESS_TOKEN",
				Strategy:             openshell.RefreshStrategyOAuth2RefreshToken,
				Status:               "failed",
				LastError:            "the refresh token was revoked",
				RecoveryAction:       types.RefreshRecoveryActionReauthorize,
				FailureCode:          "oauth_invalid_grant",
				ProviderErrorSubtype: "token_revoked",
				LastErrorAt:          failedAt,
			},
			{CredentialKey: "OTHER_TOKEN", Strategy: openshell.RefreshStrategyOAuth2ClientCredentials, Status: "active"},
		}, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/providers/{name}/refresh-status", handler.GetProviderRefreshStatus)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces/default/providers/google/refresh-status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	failed := body[0]
	if failed["recoveryAction"] != "REAUTHORIZE" || failed["failureCode"] != "oauth_invalid_grant" ||
		failed["providerErrorSubtype"] != "token_revoked" || failed["lastErrorAtMs"] != float64(failedAt.UnixMilli()) {
		t.Errorf("failed refresh = %v", failed)
	}
	for _, key := range []string{"recoveryAction", "failureCode", "providerErrorSubtype", "lastErrorAtMs"} {
		if _, present := body[1][key]; present {
			t.Errorf("a refresh that is working carries %s: %v", key, body[1])
		}
	}
}

func TestListProviderProfiles(t *testing.T) {
	sdk := &mockSDK{}
	sdk.providers.profiles.listFn = func(_ context.Context, _ string, _ ...openshell.ListOptions) ([]*openshell.ProviderProfile, error) {
		return []*openshell.ProviderProfile{
			{ID: "claude", DisplayName: "Claude", Category: openshell.ProfileCategoryInference},
		}, nil
	}
	handler := NewProvidersHandler(services.NewProviderService(sdk.Providers()))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/provider-profiles", handler.ListProviderProfiles)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/provider-profiles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var profiles []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&profiles); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1", len(profiles))
	}
	if profiles[0]["id"] != "claude" || profiles[0]["category"] != "INFERENCE" {
		t.Errorf("profile = %v, want id=claude category=INFERENCE", profiles[0])
	}
}
