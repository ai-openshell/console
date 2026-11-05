package services

import (
	"context"
	"errors"
	"reflect"
	"testing"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	pb "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"
	sbv1 "github.com/NVIDIA/OpenShell/sdk/go/proto/sandboxv1"
	"google.golang.org/protobuf/proto"

	"github.com/ai-openshell/console/backend/pkg/models"
)

// fakeSDKProfiles is the SDK's profile client over what it was last given.
type fakeSDKProfiles struct {
	openshell.ProfileInterface
	stored   []*openshell.ProviderProfile
	received []openshell.ProfileImportItem
	calls    []string
}

func (f *fakeSDKProfiles) ListAll(_ context.Context, workspace string, _ ...openshell.ListOptions) ([]*openshell.ProviderProfile, error) {
	f.calls = append(f.calls, "list "+workspace)
	return f.stored, nil
}

func (f *fakeSDKProfiles) Get(_ context.Context, workspace, id string) (*openshell.ProviderProfile, error) {
	f.calls = append(f.calls, "get "+workspace+"/"+id)
	return f.stored[0], nil
}

func (f *fakeSDKProfiles) Import(_ context.Context, workspace string, items []openshell.ProfileImportItem) (*openshell.ImportResult, error) {
	f.calls = append(f.calls, "import "+workspace)
	f.received = append(f.received, items...)
	return &openshell.ImportResult{
		Imported:    true,
		Profiles:    []openshell.ProviderProfile{items[0].Profile},
		Diagnostics: []openshell.ProfileDiagnostic{{Source: items[0].Source, Field: "id", Message: "shadows a platform profile", Severity: "warning"}},
	}, nil
}

func (f *fakeSDKProfiles) Update(_ context.Context, workspace, id string, expected uint64, item openshell.ProfileImportItem) (*openshell.UpdateResult, error) {
	f.calls = append(f.calls, "update "+workspace+"/"+id)
	f.received = append(f.received, item)
	if expected != 4 {
		return &openshell.UpdateResult{}, nil
	}
	return &openshell.UpdateResult{Updated: true, Profile: &item.Profile}, nil
}

func (f *fakeSDKProfiles) Lint(_ context.Context, workspace string, items []openshell.ProfileImportItem) (*openshell.LintResult, error) {
	f.calls = append(f.calls, "lint "+workspace)
	f.received = append(f.received, items...)
	return &openshell.LintResult{Valid: true}, nil
}

func plainItem() *pb.ProviderProfileImportItem {
	return &pb.ProviderProfileImportItem{
		Source: "custom.yaml",
		Profile: &pb.ProviderProfile{
			Id:          "custom",
			DisplayName: "Custom",
			Credentials: []*pb.ProviderProfileCredential{{Name: "api_key", HeaderName: "x-api-key"}},
			Endpoints:   []*sbv1.NetworkEndpoint{{Host: "api.example.com", Port: 443, Protocol: "rest"}},
			Binaries:    []*sbv1.NetworkBinary{{Path: "/usr/bin/curl"}},
		},
	}
}

func ruledItem() *pb.ProviderProfileImportItem {
	item := plainItem()
	item.Profile.Endpoints[0].Access = sbv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY
	return item
}

// The SDK-backed store says its endpoints are partial, so that a handler does
// not present them as whole.
func TestSDKProviderProfileStoreIsNarrow(t *testing.T) {
	var store ProviderProfileStore = NewSDKProviderProfileStore(&fakeSDKProfiles{})
	narrow, ok := store.(NarrowProviderProfileStore)
	if !ok || !narrow.EndpointsAreNarrow() {
		t.Errorf("the SDK-backed store does not report narrow endpoints")
	}
}

func TestSDKProviderProfileStoreReads(t *testing.T) {
	sdk := &fakeSDKProfiles{stored: []*openshell.ProviderProfile{{
		ID:          "github",
		DisplayName: "GitHub",
		Scope:       "platform",
		Endpoints:   []openshell.NetworkEndpoint{{Host: "api.github.com", Port: 443, Protocol: "rest"}},
		Credentials: []openshell.ProfileCredential{{Name: "api_token", HeaderName: "authorization"}},
	}}}
	store := NewSDKProviderProfileStore(sdk)

	listed, err := store.ListProviderProfiles(context.Background(), "team-a")
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListProviderProfiles = %v, %v", listed, err)
	}
	got, err := store.GetProviderProfile(context.Background(), "", "github")
	if err != nil {
		t.Fatalf("GetProviderProfile: %v", err)
	}
	for _, profile := range []*pb.ProviderProfile{listed[0], got} {
		if profile.GetId() != "github" || profile.GetScope() != "platform" ||
			profile.GetCredentials()[0].GetHeaderName() != "authorization" ||
			profile.GetEndpoints()[0].GetProtocol() != "rest" {
			t.Errorf("profile = %v", profile)
		}
	}
	if want := []string{"list team-a", "get /github"}; !equal(sdk.calls, want) {
		t.Errorf("calls = %v, want %v: the empty workspace is the platform scope", sdk.calls, want)
	}
}

func TestSDKProviderProfileStoreWritesWhatTheSDKCanCarry(t *testing.T) {
	sdk := &fakeSDKProfiles{}
	store := NewSDKProviderProfileStore(sdk)
	ctx := context.Background()

	imported, err := store.ImportProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{plainItem()})
	if err != nil {
		t.Fatalf("ImportProviderProfiles: %v", err)
	}
	wantImported := &pb.ImportProviderProfilesResponse{
		Imported:    true,
		Profiles:    []*pb.ProviderProfile{plainItem().GetProfile()},
		Diagnostics: []*pb.ProviderProfileDiagnostic{{Source: "custom.yaml", Field: "id", Message: "shadows a platform profile", Severity: "warning"}},
	}
	if !proto.Equal(imported, wantImported) {
		t.Errorf("import answer = %v, want %v", imported, wantImported)
	}

	updated, err := store.UpdateProviderProfile(ctx, "", "custom", 4, plainItem())
	if err != nil {
		t.Fatalf("UpdateProviderProfile: %v", err)
	}
	if want := (&pb.UpdateProviderProfilesResponse{Updated: true, Profile: plainItem().GetProfile()}); !proto.Equal(updated, want) {
		t.Errorf("update answer = %v, want %v", updated, want)
	}
	// An update the gateway did not apply has no profile to return.
	refused, err := store.UpdateProviderProfile(ctx, "", "custom", 9, plainItem())
	if err != nil {
		t.Fatalf("UpdateProviderProfile: %v", err)
	}
	if !proto.Equal(refused, &pb.UpdateProviderProfilesResponse{}) {
		t.Errorf("refused update = %v, want an answer with no profile", refused)
	}
	linted, err := store.LintProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{plainItem()})
	if err != nil {
		t.Fatalf("LintProviderProfiles: %v", err)
	}
	if !linted.GetValid() {
		t.Errorf("lint answer = %v", linted)
	}

	if want := []string{"import team-a", "update /custom", "update /custom", "lint team-a"}; !equal(sdk.calls, want) {
		t.Errorf("calls = %v, want %v", sdk.calls, want)
	}
	wantItem := openshell.ProfileImportItem{
		Source: "custom.yaml",
		Profile: openshell.ProviderProfile{
			ID:          "custom",
			DisplayName: "Custom",
			Credentials: []openshell.ProfileCredential{{Name: "api_key", HeaderName: "x-api-key"}},
			Endpoints:   []openshell.NetworkEndpoint{{Host: "api.example.com", Port: 443, Protocol: "rest"}},
			Binaries:    []openshell.NetworkBinary{{Path: "/usr/bin/curl"}},
		},
	}
	for _, item := range sdk.received {
		if !reflect.DeepEqual(item, wantItem) {
			t.Errorf("the SDK was given %+v\nwant              %+v", item, wantItem)
		}
	}
}

// An endpoint with an access preset cannot go through the SDK whole. It is
// refused before the SDK is called, not stored without the preset.
func TestSDKProviderProfileStoreRefusesToNarrowAnEndpoint(t *testing.T) {
	sdk := &fakeSDKProfiles{}
	store := NewSDKProviderProfileStore(sdk)
	ctx := context.Background()

	_, importErr := store.ImportProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{plainItem(), ruledItem()})
	_, updateErr := store.UpdateProviderProfile(ctx, "team-a", "custom", 4, ruledItem())
	_, lintErr := store.LintProviderProfiles(ctx, "team-a", []*pb.ProviderProfileImportItem{ruledItem()})
	for name, err := range map[string]error{"import": importErr, "update": updateErr, "lint": lintErr} {
		if !errors.Is(err, models.ErrEndpointNotExpressible) {
			t.Errorf("%s error = %v, want ErrEndpointNotExpressible", name, err)
		}
	}
	if len(sdk.calls) != 0 {
		t.Errorf("the SDK was called (%v) with a profile it would have narrowed", sdk.calls)
	}
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
