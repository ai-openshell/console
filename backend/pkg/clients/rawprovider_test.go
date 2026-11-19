package clients

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	dm "github.com/ai-openshell/sdk-go/proto/datamodelv1"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// redacted is a provider the way a gateway returns it: every credential it
// holds is a key of the credentials map with the literal "REDACTED" for a
// value, and the credential handles are cleared.
func redacted(name string, keys ...string) *dm.Provider {
	credentials := make(map[string]string, len(keys))
	for _, key := range keys {
		credentials[key] = "REDACTED"
	}
	return &dm.Provider{
		Metadata:    &dm.ObjectMeta{Name: name},
		Type:        "claude",
		Credentials: credentials,
	}
}

// fakeProviderServer plays the gateway's provider reads. Listings are served
// two providers to a page, so a caller that reads one page sees too few.
type fakeProviderServer struct {
	pb.UnimplementedOpenShellServer
	providers []*dm.Provider
	// stuckToken makes every listing answer with the same page token.
	stuckToken string
	requests   []string
	mu         sync.Mutex
}

func (f *fakeProviderServer) record(request string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
}

func (f *fakeProviderServer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeProviderServer) GetProvider(_ context.Context, req *pb.GetProviderRequest) (*pb.ProviderResponse, error) {
	f.record("get " + req.GetWorkspaceScope().GetWorkspace() + "/" + req.GetName())
	for _, provider := range f.providers {
		if provider.GetMetadata().GetName() == req.GetName() {
			return &pb.ProviderResponse{Provider: provider}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "provider not found")
}

// page serves the listing two providers at a time. A page token is the name
// of the first provider of the page it stands for.
func (f *fakeProviderServer) page(token string) ([]*dm.Provider, string) {
	if f.stuckToken != "" {
		return f.providers[:1], f.stuckToken
	}
	first := 0
	for i, provider := range f.providers {
		if provider.GetMetadata().GetName() == token {
			first = i
		}
	}
	last := min(first+2, len(f.providers))
	next := ""
	if last < len(f.providers) {
		next = f.providers[last].GetMetadata().GetName()
	}
	return f.providers[first:last], next
}

func (f *fakeProviderServer) ListProviders(_ context.Context, req *pb.ListProvidersRequest) (*pb.ListProvidersResponse, error) {
	f.record("list " + req.GetWorkspaceScope().GetWorkspace() + " @" + req.GetPageToken())
	providers, next := f.page(req.GetPageToken())
	return &pb.ListProvidersResponse{Providers: providers, NextPageToken: next}, nil
}

func (f *fakeProviderServer) ListSandboxProviders(_ context.Context, req *pb.ListSandboxProvidersRequest) (*pb.ListSandboxProvidersResponse, error) {
	f.record("sandbox " + req.GetWorkspaceScope().GetWorkspace() + "/" + req.GetSandbox() + " @" + req.GetPageToken())
	providers, next := f.page(req.GetPageToken())
	return &pb.ListSandboxProvidersResponse{Providers: providers, NextPageToken: next}, nil
}

func TestProviderCredentialKeys(t *testing.T) {
	fake := &fakeProviderServer{providers: []*dm.Provider{
		redacted("claude", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"),
		redacted("empty"),
	}}
	rc := newTestRawExec(t, fake)

	keys, err := rc.ProviderCredentialKeys(context.Background(), "team-a", "claude")
	if err != nil {
		t.Fatalf("ProviderCredentialKeys: %v", err)
	}
	if want := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v (sorted)", keys, want)
	}
	for _, key := range keys {
		if key == "REDACTED" {
			t.Error("the redaction literal was returned as a key")
		}
	}

	keys, err = rc.ProviderCredentialKeys(context.Background(), "team-a", "empty")
	if err != nil {
		t.Fatalf("ProviderCredentialKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("keys of a provider that holds no credentials = %v, want none", keys)
	}

	if want := []string{"get team-a/claude", "get team-a/empty"}; !reflect.DeepEqual(fake.seen(), want) {
		t.Errorf("requests = %v, want %v", fake.seen(), want)
	}
}

// A gateway that does not redact reports stored credentials as handles. Their
// keys count the same, once each.
func TestProviderCredentialKeysCountsHandles(t *testing.T) {
	provider := redacted("vaulted", "SHARED")
	provider.CredentialHandles = map[string]*dm.CredentialHandle{
		"SHARED":      {Driver: "vault", Handle: "vault://shared"},
		"HANDLE_ONLY": {Driver: "vault", Handle: "vault://only"},
	}
	rc := newTestRawExec(t, &fakeProviderServer{providers: []*dm.Provider{provider}})

	keys, err := rc.ProviderCredentialKeys(context.Background(), "team-a", "vaulted")
	if err != nil {
		t.Fatalf("ProviderCredentialKeys: %v", err)
	}
	if want := []string{"HANDLE_ONLY", "SHARED"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

// The gateway's own refusal comes back as the gRPC status it sent.
func TestProviderCredentialKeysNotFound(t *testing.T) {
	rc := newTestRawExec(t, &fakeProviderServer{})

	_, err := rc.ProviderCredentialKeys(context.Background(), "team-a", "missing")
	if status.Code(err) != codes.NotFound {
		t.Fatalf("error = %v, want the gateway's NotFound", err)
	}
}

func TestListProviderCredentialKeysReadsEveryPage(t *testing.T) {
	fake := &fakeProviderServer{providers: []*dm.Provider{
		redacted("a", "A_KEY"),
		redacted("b"),
		redacted("c", "C_TOKEN", "C_KEY"),
		redacted("d", "D_KEY"),
		redacted("e", "E_KEY"),
	}}
	rc := newTestRawExec(t, fake)

	keys, err := rc.ListProviderCredentialKeys(context.Background(), "team-a")
	if err != nil {
		t.Fatalf("ListProviderCredentialKeys: %v", err)
	}
	want := map[string][]string{
		"a": {"A_KEY"},
		"b": {},
		"c": {"C_KEY", "C_TOKEN"},
		"d": {"D_KEY"},
		"e": {"E_KEY"},
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if wantRequests := []string{"list team-a @", "list team-a @c", "list team-a @e"}; !reflect.DeepEqual(fake.seen(), wantRequests) {
		t.Errorf("requests = %v, want %v", fake.seen(), wantRequests)
	}
}

func TestListSandboxProviderCredentialKeys(t *testing.T) {
	fake := &fakeProviderServer{providers: []*dm.Provider{
		redacted("a", "A_KEY"),
		redacted("b", "B_KEY"),
		redacted("c", "C_KEY"),
	}}
	rc := newTestRawExec(t, fake)

	keys, err := rc.ListSandboxProviderCredentialKeys(context.Background(), "team-a", "sb")
	if err != nil {
		t.Fatalf("ListSandboxProviderCredentialKeys: %v", err)
	}
	want := map[string][]string{"a": {"A_KEY"}, "b": {"B_KEY"}, "c": {"C_KEY"}}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if wantRequests := []string{"sandbox team-a/sb @", "sandbox team-a/sb @c"}; !reflect.DeepEqual(fake.seen(), wantRequests) {
		t.Errorf("requests = %v, want %v", fake.seen(), wantRequests)
	}
}

// A gateway that keeps handing back the page token it was given would be
// listed forever. That is an error, not a hang.
func TestListProviderCredentialKeysStopsOnARepeatedPageToken(t *testing.T) {
	fake := &fakeProviderServer{
		providers:  []*dm.Provider{redacted("a", "A_KEY")},
		stuckToken: "again",
	}
	rc := newTestRawExec(t, fake)

	_, err := rc.ListProviderCredentialKeys(context.Background(), "team-a")
	if !errors.Is(err, errPageTokenRepeated) {
		t.Fatalf("error = %v, want errPageTokenRepeated", err)
	}
	if got := len(fake.seen()); got != 2 {
		t.Errorf("the gateway was asked %d times, want 2: the first page and the one that repeated", got)
	}
}
