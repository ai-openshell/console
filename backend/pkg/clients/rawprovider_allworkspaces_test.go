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

// redactedIn is a provider the way a gateway returns it in an all-workspaces
// list: every credential it holds is a key of the credentials map with the
// literal "REDACTED" for a value, and its metadata names the workspace it
// lives in, which is what tells two providers of the same name apart.
func redactedIn(workspace, name string, keys ...string) *dm.Provider {
	credentials := make(map[string]string, len(keys))
	for _, key := range keys {
		credentials[key] = "REDACTED"
	}
	return &dm.Provider{
		Metadata:    &dm.ObjectMeta{Name: name, Workspace: workspace},
		Type:        "claude",
		Credentials: credentials,
	}
}

// allWorkspacesProviderServer plays the gateway's ListProviders for the
// all-workspaces scope: one provider to a page, and a refusal for any other
// scope so that a request with the wrong selector cannot pass.
type allWorkspacesProviderServer struct {
	pb.UnimplementedOpenShellServer
	refuse     error
	stuckToken string
	providers  []*dm.Provider
	tokens     []string
	mu         sync.Mutex
}

func (f *allWorkspacesProviderServer) ListProviders(_ context.Context, req *pb.ListProvidersRequest) (*pb.ListProvidersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, req.GetPageToken())
	if f.refuse != nil {
		return nil, f.refuse
	}
	if req.GetWorkspaceScope().GetAllWorkspaces() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "expected the all-workspaces selector, got %v", req.GetWorkspaceScope())
	}
	if f.stuckToken != "" {
		return &pb.ListProvidersResponse{Providers: f.providers[:1], NextPageToken: f.stuckToken}, nil
	}
	index := 0
	for i, provider := range f.providers {
		if provider.GetMetadata().GetWorkspace()+"/"+provider.GetMetadata().GetName() == req.GetPageToken() {
			index = i
		}
	}
	if index >= len(f.providers) {
		return &pb.ListProvidersResponse{}, nil
	}
	next := ""
	if index+1 < len(f.providers) {
		following := f.providers[index+1].GetMetadata()
		next = following.GetWorkspace() + "/" + following.GetName()
	}
	return &pb.ListProvidersResponse{Providers: f.providers[index : index+1], NextPageToken: next}, nil
}

func (f *allWorkspacesProviderServer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokens...)
}

func TestListProviderCredentialKeysAllWorkspaces(t *testing.T) {
	fake := &allWorkspacesProviderServer{providers: []*dm.Provider{
		redactedIn("team-a", "claude", "ANTHROPIC_API_KEY"),
		redactedIn("team-b", "claude", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"),
		redactedIn("team-b", "empty"),
	}}
	rc := newTestRawExec(t, fake)

	keys, err := rc.ListProviderCredentialKeysAllWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListProviderCredentialKeysAllWorkspaces: %v", err)
	}
	// The two providers named claude keep their own keys, sorted, and only
	// the keys: the redaction literal is not one of them.
	want := map[string]map[string][]string{
		"team-a": {"claude": {"ANTHROPIC_API_KEY"}},
		"team-b": {"claude": {"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}, "empty": {}},
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v\nwant %v", keys, want)
	}
	// Every page was read, each asked for with the token the last one gave.
	if wantTokens := []string{"", "team-b/claude", "team-b/empty"}; !reflect.DeepEqual(fake.seen(), wantTokens) {
		t.Errorf("page tokens sent = %q, want %q", fake.seen(), wantTokens)
	}
}

func TestListProviderCredentialKeysAllWorkspacesEmpty(t *testing.T) {
	rc := newTestRawExec(t, &allWorkspacesProviderServer{})

	keys, err := rc.ListProviderCredentialKeysAllWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListProviderCredentialKeysAllWorkspaces: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("keys on a gateway without providers = %v, want none", keys)
	}
}

// The gateway answers the all-workspaces scope for platform admins only. Its
// refusal comes back as the gRPC status it sent.
func TestListProviderCredentialKeysAllWorkspacesRefused(t *testing.T) {
	rc := newTestRawExec(t, &allWorkspacesProviderServer{
		refuse: status.Error(codes.PermissionDenied, "role 'openshell-admin' required"),
	})

	_, err := rc.ListProviderCredentialKeysAllWorkspaces(context.Background())
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("error = %v, want the gateway's PermissionDenied", err)
	}
}

func TestListProviderCredentialKeysAllWorkspacesStopsOnARepeatedPageToken(t *testing.T) {
	fake := &allWorkspacesProviderServer{
		providers:  []*dm.Provider{redactedIn("team-a", "claude", "ANTHROPIC_API_KEY")},
		stuckToken: "again",
	}
	rc := newTestRawExec(t, fake)

	_, err := rc.ListProviderCredentialKeysAllWorkspaces(context.Background())
	if !errors.Is(err, errPageTokenRepeated) {
		t.Fatalf("error = %v, want errPageTokenRepeated", err)
	}
	if got := len(fake.seen()); got != 2 {
		t.Errorf("the gateway was asked %d times, want 2: the first page and the one that repeated", got)
	}
}
