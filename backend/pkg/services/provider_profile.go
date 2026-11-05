package services

import (
	"context"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	pb "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"

	"github.com/ai-openshell/console/backend/pkg/models"
)

// ProviderProfileStore reads and writes provider profiles as the gateway's own
// message. An empty workspace is the platform scope.
//
// It exists beside the SDK's ProfileInterface because that one narrows a
// profile: of each endpoint it carries a host, a port and a protocol, and the
// rest of an endpoint is what bounds a provider's traffic. clients.RawExecClient
// implements it whole. NewSDKProviderProfileStore implements it over the SDK
// and is narrow in the same way; the handler uses it when no other store was
// set.
type ProviderProfileStore interface {
	ListProviderProfiles(ctx context.Context, workspace string) ([]*pb.ProviderProfile, error)
	GetProviderProfile(ctx context.Context, workspace, id string) (*pb.ProviderProfile, error)
	ImportProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.ImportProviderProfilesResponse, error)
	UpdateProviderProfile(ctx context.Context, workspace, id string, expectedResourceVersion uint64, item *pb.ProviderProfileImportItem) (*pb.UpdateProviderProfilesResponse, error)
	LintProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.LintProviderProfilesResponse, error)
}

// NarrowProviderProfileStore is implemented by a store whose profiles do not
// hold their endpoints whole. A handler asks so that it does not present part
// of an endpoint as the endpoint.
type NarrowProviderProfileStore interface {
	// EndpointsAreNarrow reports that the profiles this store returns hold a
	// host, a port and a protocol of each endpoint and nothing else of it.
	EndpointsAreNarrow() bool
}

// SDKProviderProfileStore is a ProviderProfileStore over the SDK's profile
// client.
//
// What it reads has narrow endpoints. What it writes is checked first: a
// profile with an endpoint the SDK could only send in part is refused with
// models.ErrEndpointNotExpressible instead of being stored without its rules.
// It cannot protect what it cannot see, though: an update replaces the stored
// profile, so updating a profile whose stored endpoints say more than a host,
// a port and a protocol drops the rest. Use clients.RawExecClient wherever
// profiles are edited.
type SDKProviderProfileStore struct {
	profiles openshell.ProfileInterface
}

func NewSDKProviderProfileStore(profiles openshell.ProfileInterface) *SDKProviderProfileStore {
	return &SDKProviderProfileStore{profiles: profiles}
}

func (*SDKProviderProfileStore) EndpointsAreNarrow() bool { return true }

func (s *SDKProviderProfileStore) ListProviderProfiles(ctx context.Context, workspace string) ([]*pb.ProviderProfile, error) {
	profiles, err := s.profiles.ListAll(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := make([]*pb.ProviderProfile, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, models.ProviderProfileFromSDK(profile))
	}
	return out, nil
}

func (s *SDKProviderProfileStore) GetProviderProfile(ctx context.Context, workspace, id string) (*pb.ProviderProfile, error) {
	profile, err := s.profiles.Get(ctx, workspace, id)
	if err != nil {
		return nil, err
	}
	return models.ProviderProfileFromSDK(profile), nil
}

func sdkImportItems(items []*pb.ProviderProfileImportItem) ([]openshell.ProfileImportItem, error) {
	out := make([]openshell.ProfileImportItem, 0, len(items))
	for _, item := range items {
		profile, err := models.ProviderProfileToSDK(item.GetProfile())
		if err != nil {
			return nil, err
		}
		out = append(out, openshell.ProfileImportItem{Profile: profile, Source: item.GetSource()})
	}
	return out, nil
}

func (s *SDKProviderProfileStore) ImportProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.ImportProviderProfilesResponse, error) {
	sdkItems, err := sdkImportItems(items)
	if err != nil {
		return nil, err
	}
	result, err := s.profiles.Import(ctx, workspace, sdkItems)
	if err != nil {
		return nil, err
	}
	resp := &pb.ImportProviderProfilesResponse{
		Diagnostics: models.ProfileDiagnosticsFromSDK(result.Diagnostics),
		Imported:    result.Imported,
	}
	for index := range result.Profiles {
		resp.Profiles = append(resp.Profiles, models.ProviderProfileFromSDK(&result.Profiles[index]))
	}
	return resp, nil
}

func (s *SDKProviderProfileStore) UpdateProviderProfile(ctx context.Context, workspace, id string, expectedResourceVersion uint64, item *pb.ProviderProfileImportItem) (*pb.UpdateProviderProfilesResponse, error) {
	sdkItems, err := sdkImportItems([]*pb.ProviderProfileImportItem{item})
	if err != nil {
		return nil, err
	}
	result, err := s.profiles.Update(ctx, workspace, id, expectedResourceVersion, sdkItems[0])
	if err != nil {
		return nil, err
	}
	return &pb.UpdateProviderProfilesResponse{
		Diagnostics: models.ProfileDiagnosticsFromSDK(result.Diagnostics),
		Profile:     models.ProviderProfileFromSDK(result.Profile),
		Updated:     result.Updated,
	}, nil
}

func (s *SDKProviderProfileStore) LintProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.LintProviderProfilesResponse, error) {
	sdkItems, err := sdkImportItems(items)
	if err != nil {
		return nil, err
	}
	result, err := s.profiles.Lint(ctx, workspace, sdkItems)
	if err != nil {
		return nil, err
	}
	return &pb.LintProviderProfilesResponse{
		Diagnostics: models.ProfileDiagnosticsFromSDK(result.Diagnostics),
		Valid:       result.Valid,
	}, nil
}
