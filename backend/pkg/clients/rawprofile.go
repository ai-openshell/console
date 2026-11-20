package clients

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dm "github.com/ai-openshell/sdk-go/proto/datamodelv1"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
)

// The methods in this file read and write provider profiles as the gateway's
// own message, which the OpenShell Go SDK narrows on the way in and out.
//
// A profile's endpoints are openshell.sandbox.v1.NetworkEndpoint messages:
// each carries the access preset, the enforcement and TLS modes, the L7 allow
// and deny rules and the rest of what bounds the traffic of a sandbox the
// provider is attached to. The SDK's ProviderProfile holds a NetworkEndpoint
// of its own with a host, a port and a protocol, and its converter says so
// ("additional proto fields are ignored"). Read through the SDK a profile
// shows no more than that, and written through it the gateway stores no more
// than that: an import loses the rules it was written with, and an update,
// which replaces the stored profile, removes the rules it had. The profiles
// upstream publishes all set those fields, and the OpenShell CLI sends and
// prints them through the same messages these methods use.
//
// Deleting a profile needs none of this and stays on the SDK. Delete the file
// once the SDK carries a profile's endpoints whole.

// profileScope is the workspace selector of a profile request. The empty
// workspace is the platform scope, which the gateway reads from an absent
// selector.
func profileScope(workspace string) *dm.WorkspaceSelector {
	if workspace == "" {
		return nil
	}
	return namedWorkspace(workspace)
}

// ListProviderProfiles returns every provider profile visible in the scope,
// in the gateway's order.
func (r *RawExecClient) ListProviderProfiles(ctx context.Context, workspace string) ([]*pb.ProviderProfile, error) {
	var profiles []*pb.ProviderProfile
	pageToken := ""
	for {
		resp, err := r.client.ListProviderProfiles(ctx, &pb.ListProviderProfilesRequest{
			WorkspaceScope: profileScope(workspace),
			PageToken:      pageToken,
		})
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, resp.GetProfiles()...)
		next := resp.GetNextPageToken()
		if next == "" {
			return profiles, nil
		}
		if next == pageToken {
			return nil, errPageTokenRepeated
		}
		pageToken = next
	}
}

// GetProviderProfile returns the profile the scope resolves the id to.
func (r *RawExecClient) GetProviderProfile(ctx context.Context, workspace, id string) (*pb.ProviderProfile, error) {
	resp, err := r.client.GetProviderProfile(ctx, &pb.GetProviderProfileRequest{
		Id:             id,
		WorkspaceScope: profileScope(workspace),
	})
	if err != nil {
		return nil, err
	}
	return resp.GetProfile(), nil
}

// ImportProviderProfiles creates the profiles in the scope. A refusal is not
// an error: the response says so and carries the diagnostics.
func (r *RawExecClient) ImportProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.ImportProviderProfilesResponse, error) {
	if err := r.checkStablePlaceholder(ctx, workspace, items); err != nil {
		return nil, err
	}
	return r.client.ImportProviderProfiles(ctx, &pb.ImportProviderProfilesRequest{
		Profiles:       items,
		WorkspaceScope: profileScope(workspace),
	})
}

// UpdateProviderProfile replaces the stored profile with the one given.
func (r *RawExecClient) UpdateProviderProfile(ctx context.Context, workspace, id string, expectedResourceVersion uint64, item *pb.ProviderProfileImportItem) (*pb.UpdateProviderProfilesResponse, error) {
	if err := r.checkStablePlaceholder(ctx, workspace, []*pb.ProviderProfileImportItem{item}); err != nil {
		return nil, err
	}
	return r.client.UpdateProviderProfiles(ctx, &pb.UpdateProviderProfilesRequest{
		Id:                      id,
		Profile:                 item,
		ExpectedResourceVersion: expectedResourceVersion,
		WorkspaceScope:          profileScope(workspace),
	})
}

// LintProviderProfiles asks the gateway to validate the profiles without
// storing them.
func (r *RawExecClient) LintProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.LintProviderProfilesResponse, error) {
	return r.client.LintProviderProfiles(ctx, &pb.LintProviderProfilesRequest{
		Profiles:       items,
		WorkspaceScope: profileScope(workspace),
	})
}

// checkStablePlaceholder does what the SDK's profile client does before it
// writes a profile in which a credential sets stable_placeholder (OpenShell
// 0.1.4): it lints the profiles first and refuses the write unless the
// gateway says it supports the field. A gateway older than 0.1.4 does not
// know the field and would store the profile without it, saying nothing.
// Profiles that do not set it are written without the extra call.
func (r *RawExecClient) checkStablePlaceholder(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) error {
	if !setsStablePlaceholder(items) {
		return nil
	}
	resp, err := r.LintProviderProfiles(ctx, workspace, items)
	if err != nil {
		return err
	}
	if !resp.GetSupportsStablePlaceholder() {
		return status.Error(codes.FailedPrecondition,
			"gateway does not support stable_placeholder; upgrade the gateway before enabling it")
	}
	return nil
}

func setsStablePlaceholder(items []*pb.ProviderProfileImportItem) bool {
	for _, item := range items {
		for _, credential := range item.GetProfile().GetCredentials() {
			if credential.GetStablePlaceholder() {
				return true
			}
		}
	}
	return false
}
