package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
	pb "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"
	"github.com/ai-openshell/console/backend/pkg/services"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetSandboxLogs(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.getLogsFn = func(_ context.Context, workspace, name string, opts ...openshell.LogOption) (*openshell.LogResult, error) {
		if workspace != "default" || name != "my-sandbox" {
			t.Errorf("GetLogs(%s, %s), want (default, my-sandbox)", workspace, name)
		}
		cfg := types.ApplyLogOptions(opts)
		if cfg.Lines() != 50 {
			t.Errorf("lines = %d, want 50", cfg.Lines())
		}
		if !cfg.Since().Equal(time.UnixMilli(1000)) {
			t.Errorf("since = %v, want unix milli 1000", cfg.Since())
		}
		if got := cfg.Sources(); len(got) != 2 {
			t.Errorf("sources = %v, want [gateway sandbox]", got)
		}
		if cfg.MinLevel() != "INFO" {
			t.Errorf("minLevel = %q, want INFO", cfg.MinLevel())
		}
		return &openshell.LogResult{
			Lines:       []openshell.LogLine{{Message: "hello", Level: "INFO", Timestamp: time.UnixMilli(1234)}},
			BufferTotal: 100,
		}, nil
	}
	// TODO: Add actual rawexecclient
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/logs", handler.GetSandboxLogs)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/my-sandbox/logs?lines=50&sinceMs=1000&source=gateway&source=sandbox&level=INFO", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body["bufferTotal"] != float64(100) {
		t.Errorf("bufferTotal = %v", body["bufferTotal"])
	}
	logs, _ := body["logs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(logs))
	}
}

func TestGetSandboxLogsDefaults(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.getLogsFn = func(_ context.Context, _, _ string, opts ...openshell.LogOption) (*openshell.LogResult, error) {
		cfg := types.ApplyLogOptions(opts)
		if cfg.Lines() != 200 {
			t.Errorf("default lines = %d, want 200", cfg.Lines())
		}
		return &openshell.LogResult{}, nil
	}
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/logs", handler.GetSandboxLogs)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/my-sandbox/logs?lines=notanumber", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetSandboxLogsNotFound(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.getLogsFn = func(_ context.Context, _, _ string, _ ...openshell.LogOption) (*openshell.LogResult, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
	}
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/logs", handler.GetSandboxLogs)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/missing/logs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestStreamSandboxLogs(t *testing.T) {
	sdk := &mockSDK{}
	stream := &mockServerStream{events: []*pb.SandboxStreamEvent{
		{Payload: &pb.SandboxStreamEvent_Log{Log: &pb.SandboxLogLine{Message: "first log"}}},
		{Payload: &pb.SandboxStreamEvent_Log{Log: &pb.SandboxLogLine{Message: "second log"}}},
	}}
	streamCtx := context.Background()
	raw := &mockSandboxRawExec{watchLogsFn: func(ctx context.Context, req *pb.WatchSandboxRequest) (grpc.ServerStreamingClient[pb.SandboxStreamEvent], error) {
		streamCtx = ctx
		if req.GetWorkspaceScope().GetWorkspace() != "team-a" || req.GetSandbox() != "my-sandbox" {
			t.Errorf("WatchSandbox target = %s/%s, want team-a/my-sandbox", req.GetWorkspaceScope().GetWorkspace(), req.GetSandbox())
		}
		if !req.GetFollowLogs() || !req.GetFollowStatus() {
			t.Error("WatchSandbox must follow logs and status")
		}
		if req.GetLogTailLines() != 50 || req.GetLogMinLevel() != "INFO" || !slices.Equal(req.GetLogSources(), []string{"gateway", "sandbox"}) {
			t.Errorf("unexpected log filters: %v", req)
		}
		return stream, nil
	}}
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), raw))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/logs/stream", handler.StreamSandboxLogs)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/workspaces/team-a/sandboxes/my-sandbox/logs/stream?lines=50&source=gateway&source=sandbox&level=INFO", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache, no-transform",
		"X-Accel-Buffering": "no",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	want := ": connected\n\n" +
		"event: log\ndata: {\"message\":\"first log\",\"timestampMs\":0}\n\n" +
		"event: log\ndata: {\"message\":\"second log\",\"timestampMs\":0}\n\n" +
		"event: end\ndata: {}\n\n"
	if got := w.Body.String(); got != want {
		t.Errorf("stream = %q, want %q", got, want)
	}
	if !w.Flushed {
		t.Error("stream was not flushed")
	}
	if err := streamCtx.Err(); err != context.Canceled {
		t.Errorf("stream context error = %v, want canceled", err)
	}
}

func TestStreamSandboxLogsError(t *testing.T) {
	sdk := &mockSDK{}
	stream := &mockServerStream{
		events: []*pb.SandboxStreamEvent{
			{Payload: &pb.SandboxStreamEvent_Log{Log: &pb.SandboxLogLine{Message: "first log"}}},
		},
		err: status.Error(codes.Unavailable, "internal gateway details"),
	}
	streamCtx := context.Background()
	raw := &mockSandboxRawExec{watchLogsFn: func(ctx context.Context, _ *pb.WatchSandboxRequest) (grpc.ServerStreamingClient[pb.SandboxStreamEvent], error) {
		streamCtx = ctx
		return stream, nil
	}}
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), raw))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/logs/stream", handler.StreamSandboxLogs)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/workspaces/team-a/sandboxes/my-sandbox/logs/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after stream starts; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	want := ": connected\n\n" +
		"event: log\ndata: {\"message\":\"first log\",\"timestampMs\":0}\n\n" +
		"event: stream-error\ndata: {\"code\":\"gateway_unavailable\",\"message\":\"Live logs disconnected\"}\n\n"
	if got := w.Body.String(); got != want {
		t.Errorf("stream = %q, want %q", got, want)
	}
	if err := streamCtx.Err(); err != context.Canceled {
		t.Errorf("stream context error = %v, want canceled", err)
	}
}

func TestListSandboxProviders(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listProvidersFn = func(_ context.Context, _, _ string) ([]*openshell.Provider, error) {
		return []*openshell.Provider{
			{
				Name: "claude-prov",
				Type: "claude",
				Spec: openshell.ProviderSpec{
					CredentialHandles: map[string]types.CredentialHandle{
						"api_key": {Driver: "vault", Handle: "vault://must-not-leak"},
					},
				},
			},
		}, nil
	}
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/providers", handler.ListSandboxProviders)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/my-sandbox/providers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "must-not-leak") {
		t.Errorf("leaked credential handle: %s", body)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d providers, want 1", len(got))
	}
	names, _ := got[0]["credentialNames"].([]any)
	if len(names) != 1 || names[0] != "api_key" {
		t.Errorf("credentialNames = %v, want [api_key]", got[0]["credentialNames"])
	}
}

// The providers attached to a sandbox name their credentials the same way the
// workspace's providers do: from the key reader, by provider name.
func TestListSandboxProvidersNamesCredentialsFromTheKeyReader(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listProvidersFn = func(_ context.Context, _, _ string) ([]*openshell.Provider, error) {
		return []*openshell.Provider{gatewayProvider("attached")}, nil
	}
	reader := &fakeCredentialKeys{bySandbox: map[string][]string{"attached": {"GITHUB_TOKEN"}}}
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	handler.SetCredentialKeyReader(reader)
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/providers", handler.ListSandboxProviders)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/team-a/sandboxes/my-sandbox/providers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d providers, want 1", len(got))
	}
	if names := credentialNamesOf(t, got[0]); len(names) != 1 || names[0] != "GITHUB_TOKEN" {
		t.Errorf("credentialNames = %v, want [GITHUB_TOKEN]", names)
	}
	if len(reader.calls) != 1 || reader.calls[0] != "sandbox team-a/my-sandbox" {
		t.Errorf("key reader calls = %v, want one read for team-a/my-sandbox", reader.calls)
	}

	// The reader is a raw gRPC client; its errors never passed through the SDK.
	reader.err = status.Error(codes.Unavailable, "connection refused")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workspaces/team-a/sandboxes/my-sandbox/providers", nil))
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d when the keys cannot be read, want 502", w.Code)
	}
}

func TestAttachDetachSandboxProvider(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.attachFn = func(_ context.Context, workspace, sandboxName, providerName string, expectedRV uint64) (*openshell.AttachProviderResult, error) {
		if workspace != "default" || sandboxName != "my-sandbox" || providerName != "claude-prov" {
			t.Errorf("attach args = %s/%s/%s", workspace, sandboxName, providerName)
		}
		if expectedRV != 42 {
			t.Errorf("expectedResourceVersion = %d, want 42", expectedRV)
		}
		return &openshell.AttachProviderResult{Attached: true, Sandbox: &openshell.Sandbox{Name: sandboxName}}, nil
	}
	sdk.sandboxes.detachFn = func(_ context.Context, _, sandboxName, _ string, _ uint64) (*openshell.DetachProviderResult, error) {
		return &openshell.DetachProviderResult{Detached: true, Sandbox: &openshell.Sandbox{Name: sandboxName}}, nil
	}
	handler := NewLogsHandler(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/{name}/providers/{provider}", handler.AttachSandboxProvider)
	r.Delete("/workspaces/{workspace}/sandboxes/{name}/providers/{provider}", handler.DetachSandboxProvider)

	req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes/my-sandbox/providers/claude-prov", strings.NewReader(`{"expectedResourceVersion":42}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("attach status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var attach map[string]any
	if err := json.NewDecoder(w.Body).Decode(&attach); err != nil {
		t.Fatalf("decode attach: %v", err)
	}
	if attach["attached"] != true {
		t.Errorf("attached = %v", attach["attached"])
	}

	req = httptest.NewRequest(http.MethodDelete, "/workspaces/default/sandboxes/my-sandbox/providers/claude-prov", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detach status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}
