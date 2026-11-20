package clients

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	dm "github.com/ai-openshell/sdk-go/proto/datamodelv1"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// wholeProfile is a profile whose endpoint says what the SDK's type has no
// room for: what the endpoint allows, how strictly, and what it denies.
func wholeProfile(id string) *pb.ProviderProfile {
	return &pb.ProviderProfile{
		Id:          id,
		DisplayName: "GitHub",
		Credentials: []*pb.ProviderProfileCredential{{
			Name:       "api_token",
			EnvVars:    []string{"GITHUB_TOKEN"},
			HeaderName: "authorization",
		}},
		Endpoints: []*sbv1.NetworkEndpoint{{
			Host:        "api.github.com",
			Port:        443,
			Protocol:    "rest",
			Access:      sbv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY,
			Enforcement: sbv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_ENFORCE,
			DenyRules:   []*sbv1.L7DenyRule{{Method: "DELETE", Path: "/repos/**"}},
		}},
		Binaries: []*sbv1.NetworkBinary{{Path: "/usr/bin/gh"}},
	}
}

// scopeOf names the scope a profile request selected: the workspace, or
// "platform" for a request with no selector at all, which is how the gateway
// is told to use the platform scope.
func scopeOf(selector *dm.WorkspaceSelector) string {
	if selector == nil {
		return "platform"
	}
	return "workspace " + selector.GetWorkspace()
}

// fakeProfileServer plays the gateway's profile RPCs. It lists two profiles
// to a page and records what each request selected and carried.
type fakeProfileServer struct {
	pb.UnimplementedOpenShellServer
	stuckToken string
	profiles   []*pb.ProviderProfile
	requests   []string
	received   []*pb.ProviderProfileImportItem
	mu         sync.Mutex
	// supportsStablePlaceholder is what lint answers: true for a gateway of
	// OpenShell 0.1.4 or newer.
	supportsStablePlaceholder bool
}

func (f *fakeProfileServer) record(request string, items ...*pb.ProviderProfileImportItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	f.received = append(f.received, items...)
}

func (f *fakeProfileServer) seen() ([]string, []*pb.ProviderProfileImportItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...), append([]*pb.ProviderProfileImportItem(nil), f.received...)
}

func (f *fakeProfileServer) ListProviderProfiles(_ context.Context, req *pb.ListProviderProfilesRequest) (*pb.ListProviderProfilesResponse, error) {
	f.record("list " + scopeOf(req.GetWorkspaceScope()) + " @" + req.GetPageToken())
	if f.stuckToken != "" {
		return &pb.ListProviderProfilesResponse{Profiles: f.profiles[:1], NextPageToken: f.stuckToken}, nil
	}
	first := 0
	for i, profile := range f.profiles {
		if profile.GetId() == req.GetPageToken() {
			first = i
		}
	}
	last := min(first+2, len(f.profiles))
	next := ""
	if last < len(f.profiles) {
		next = f.profiles[last].GetId()
	}
	return &pb.ListProviderProfilesResponse{Profiles: f.profiles[first:last], NextPageToken: next}, nil
}

func (f *fakeProfileServer) GetProviderProfile(_ context.Context, req *pb.GetProviderProfileRequest) (*pb.ProviderProfileResponse, error) {
	f.record("get " + scopeOf(req.GetWorkspaceScope()) + " " + req.GetId())
	for _, profile := range f.profiles {
		if profile.GetId() == req.GetId() {
			return &pb.ProviderProfileResponse{Profile: profile}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "provider profile not found")
}

func (f *fakeProfileServer) ImportProviderProfiles(_ context.Context, req *pb.ImportProviderProfilesRequest) (*pb.ImportProviderProfilesResponse, error) {
	f.record("import "+scopeOf(req.GetWorkspaceScope()), req.GetProfiles()...)
	resp := &pb.ImportProviderProfilesResponse{Imported: true}
	for _, item := range req.GetProfiles() {
		resp.Profiles = append(resp.Profiles, item.GetProfile())
	}
	return resp, nil
}

func (f *fakeProfileServer) UpdateProviderProfiles(_ context.Context, req *pb.UpdateProviderProfilesRequest) (*pb.UpdateProviderProfilesResponse, error) {
	if req.GetExpectedResourceVersion() != 7 {
		return nil, status.Error(codes.Aborted, "provider profile was modified concurrently")
	}
	f.record("update "+scopeOf(req.GetWorkspaceScope())+" "+req.GetId(), req.GetProfile())
	return &pb.UpdateProviderProfilesResponse{Updated: true, Profile: req.GetProfile().GetProfile()}, nil
}

func (f *fakeProfileServer) LintProviderProfiles(_ context.Context, req *pb.LintProviderProfilesRequest) (*pb.LintProviderProfilesResponse, error) {
	f.record("lint "+scopeOf(req.GetWorkspaceScope()), req.GetProfiles()...)
	return &pb.LintProviderProfilesResponse{
		SupportsStablePlaceholder: f.supportsStablePlaceholder,
		Valid:                     false,
		Diagnostics: []*pb.ProviderProfileDiagnostic{
			{Source: req.GetProfiles()[0].GetSource(), ProfileId: "github", Field: "id", Message: "already exists", Severity: "error"},
		},
	}, nil
}

// A profile comes back as the gateway sent it, the whole endpoint included.
func TestGetProviderProfileReturnsTheWholeProfile(t *testing.T) {
	fake := &fakeProfileServer{profiles: []*pb.ProviderProfile{wholeProfile("github")}}
	rc := newTestRawExec(t, fake)

	got, err := rc.GetProviderProfile(context.Background(), "team-a", "github")
	if err != nil {
		t.Fatalf("GetProviderProfile: %v", err)
	}
	if !proto.Equal(got, wholeProfile("github")) {
		t.Errorf("profile = %v, want %v", got, wholeProfile("github"))
	}

	_, err = rc.GetProviderProfile(context.Background(), "team-a", "missing")
	if status.Code(err) != codes.NotFound {
		t.Errorf("error = %v, want the gateway's NotFound", err)
	}
}

// The empty workspace is the platform scope, and the gateway reads that scope
// from a request that carries no selector, not from an empty one.
func TestProfileRequestsSelectTheScope(t *testing.T) {
	fake := &fakeProfileServer{profiles: []*pb.ProviderProfile{wholeProfile("github")}}
	rc := newTestRawExec(t, fake)
	ctx := context.Background()
	item := &pb.ProviderProfileImportItem{Profile: wholeProfile("github"), Source: "github.yaml"}

	for _, workspace := range []string{"team-a", ""} {
		if _, err := rc.ListProviderProfiles(ctx, workspace); err != nil {
			t.Fatalf("ListProviderProfiles(%q): %v", workspace, err)
		}
		if _, err := rc.GetProviderProfile(ctx, workspace, "github"); err != nil {
			t.Fatalf("GetProviderProfile(%q): %v", workspace, err)
		}
		if _, err := rc.ImportProviderProfiles(ctx, workspace, []*pb.ProviderProfileImportItem{item}); err != nil {
			t.Fatalf("ImportProviderProfiles(%q): %v", workspace, err)
		}
		if _, err := rc.UpdateProviderProfile(ctx, workspace, "github", 7, item); err != nil {
			t.Fatalf("UpdateProviderProfile(%q): %v", workspace, err)
		}
		if _, err := rc.LintProviderProfiles(ctx, workspace, []*pb.ProviderProfileImportItem{item}); err != nil {
			t.Fatalf("LintProviderProfiles(%q): %v", workspace, err)
		}
	}

	requests, _ := fake.seen()
	want := []string{
		"list workspace team-a @", "get workspace team-a github", "import workspace team-a",
		"update workspace team-a github", "lint workspace team-a",
		"list platform @", "get platform github", "import platform", "update platform github", "lint platform",
	}
	if !reflect.DeepEqual(requests, want) {
		t.Errorf("requests = %v\nwant       %v", requests, want)
	}
}

// What is written reaches the gateway as it was given: the endpoint with its
// access preset, enforcement and deny rule, and the item's source label.
func TestProfileWritesSendTheWholeProfile(t *testing.T) {
	fake := &fakeProfileServer{}
	rc := newTestRawExec(t, fake)
	ctx := context.Background()
	item := &pb.ProviderProfileImportItem{Profile: wholeProfile("github"), Source: "github.yaml"}

	imported, err := rc.ImportProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{item})
	if err != nil {
		t.Fatalf("ImportProviderProfiles: %v", err)
	}
	if !imported.GetImported() || len(imported.GetProfiles()) != 1 {
		t.Errorf("import answer = %v", imported)
	}
	updated, err := rc.UpdateProviderProfile(ctx, "team-a", "github", 7, item)
	if err != nil {
		t.Fatalf("UpdateProviderProfile: %v", err)
	}
	if !updated.GetUpdated() || !proto.Equal(updated.GetProfile(), wholeProfile("github")) {
		t.Errorf("update answer = %v", updated)
	}
	linted, err := rc.LintProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{item})
	if err != nil {
		t.Fatalf("LintProviderProfiles: %v", err)
	}
	if linted.GetValid() || len(linted.GetDiagnostics()) != 1 || linted.GetDiagnostics()[0].GetSource() != "github.yaml" {
		t.Errorf("lint answer = %v, want the gateway's diagnostic for github.yaml", linted)
	}

	_, received := fake.seen()
	if len(received) != 3 {
		t.Fatalf("the gateway received %d items, want 3", len(received))
	}
	for i, got := range received {
		if !proto.Equal(got, item) {
			t.Errorf("item %d reached the gateway as %v, want %v", i, got, item)
		}
	}
}

// The gateway's refusal of a stale update is its gRPC status, unchanged, which
// the BFF answers as a conflict.
func TestUpdateProviderProfileReturnsTheGatewayStatus(t *testing.T) {
	rc := newTestRawExec(t, &fakeProfileServer{})

	_, err := rc.UpdateProviderProfile(context.Background(), "team-a", "github", 3,
		&pb.ProviderProfileImportItem{Profile: wholeProfile("github")})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("error = %v, want the gateway's Aborted", err)
	}
}

// A profile in which a credential sets stable_placeholder is written only
// once the gateway has said it supports the field, as the SDK does: a gateway
// that does not know it would store the profile without it. A profile that
// does not set it is written without asking.
func TestStablePlaceholderIsWrittenOnlyToAGatewayThatSupportsIt(t *testing.T) {
	ctx := context.Background()
	stable := wholeProfile("github")
	stable.Credentials[0].StablePlaceholder = true
	item := &pb.ProviderProfileImportItem{Profile: stable, Source: "github.yaml"}

	older := &fakeProfileServer{}
	rc := newTestRawExec(t, older)
	_, err := rc.ImportProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{item})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("import to an older gateway: error = %v, want FailedPrecondition", err)
	}
	_, err = rc.UpdateProviderProfile(ctx, "team-a", "github", 7, item)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("update on an older gateway: error = %v, want FailedPrecondition", err)
	}
	if requests, _ := older.seen(); !reflect.DeepEqual(requests, []string{"lint workspace team-a", "lint workspace team-a"}) {
		t.Errorf("an older gateway saw %v, want two lints and no write", requests)
	}

	newer := &fakeProfileServer{supportsStablePlaceholder: true}
	rc = newTestRawExec(t, newer)
	if _, err := rc.ImportProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{item}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := rc.UpdateProviderProfile(ctx, "team-a", "github", 7, item); err != nil {
		t.Fatalf("update: %v", err)
	}
	requests, received := newer.seen()
	want := []string{"lint workspace team-a", "import workspace team-a", "lint workspace team-a", "update workspace team-a github"}
	if !reflect.DeepEqual(requests, want) {
		t.Errorf("requests = %v\nwant       %v", requests, want)
	}
	for i, got := range received {
		if !got.GetProfile().GetCredentials()[0].GetStablePlaceholder() {
			t.Errorf("item %d reached the gateway without stable_placeholder", i)
		}
	}
}

func TestListProviderProfilesReadsEveryPage(t *testing.T) {
	fake := &fakeProfileServer{profiles: []*pb.ProviderProfile{
		wholeProfile("a"), wholeProfile("b"), wholeProfile("c"), wholeProfile("d"), wholeProfile("e"),
	}}
	rc := newTestRawExec(t, fake)

	profiles, err := rc.ListProviderProfiles(context.Background(), "team-a")
	if err != nil {
		t.Fatalf("ListProviderProfiles: %v", err)
	}
	ids := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		ids = append(ids, profile.GetId())
	}
	if want := []string{"a", "b", "c", "d", "e"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("profiles = %v, want %v in the gateway's order", ids, want)
	}
	requests, _ := fake.seen()
	if want := []string{"list workspace team-a @", "list workspace team-a @c", "list workspace team-a @e"}; !reflect.DeepEqual(requests, want) {
		t.Errorf("requests = %v, want %v", requests, want)
	}
}

func TestListProviderProfilesStopsOnARepeatedPageToken(t *testing.T) {
	fake := &fakeProfileServer{profiles: []*pb.ProviderProfile{wholeProfile("a")}, stuckToken: "again"}
	rc := newTestRawExec(t, fake)

	_, err := rc.ListProviderProfiles(context.Background(), "")
	if !errors.Is(err, errPageTokenRepeated) {
		t.Fatalf("error = %v, want errPageTokenRepeated", err)
	}
	if requests, _ := fake.seen(); len(requests) != 2 {
		t.Errorf("the gateway was asked %d times, want 2: the first page and the one that repeated", len(requests))
	}
}
