package clients

import (
	"context"
	"errors"
	"slices"

	dm "github.com/ai-openshell/sdk-go/proto/datamodelv1"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
)

// The methods in this file read which credentials a provider holds, which the
// OpenShell Go SDK drops on the way in.
//
// The gateway never returns a credential value. On every read it returns the
// provider's credentials as a map from each key to the literal "REDACTED",
// folds the keys of its internal credential handles into that map, and clears
// the handles (redact_provider_credentials in upstream grpc/provider.rs). The
// SDK's converter copies credential_handles and never credentials, so read
// through the SDK every provider holds none. The OpenShell TUI lists a
// provider's credentials from the same map these methods read.
//
// Only the keys leave this file: the values are the redaction literal and are
// not passed on. Delete the file once the SDK surfaces the keys.

// errPageTokenRepeated stops a listing whose gateway keeps answering with the
// page token it was just given, which would otherwise never end.
var errPageTokenRepeated = errors.New("gateway returned the page token it was given: listing cannot make progress")

// ProviderCredentialKeys returns the keys of the credentials the named
// provider holds, sorted.
func (r *RawExecClient) ProviderCredentialKeys(ctx context.Context, workspace, name string) ([]string, error) {
	resp, err := r.client.GetProvider(ctx, &pb.GetProviderRequest{
		Name:           name,
		WorkspaceScope: namedWorkspace(workspace),
	})
	if err != nil {
		return nil, err
	}
	return credentialKeys(resp.GetProvider()), nil
}

// ListProviderCredentialKeys returns the keys of the credentials each provider
// in the workspace holds, by provider name.
func (r *RawExecClient) ListProviderCredentialKeys(ctx context.Context, workspace string) (map[string][]string, error) {
	return collectCredentialKeys(func(pageToken string) ([]*dm.Provider, string, error) {
		resp, err := r.client.ListProviders(ctx, &pb.ListProvidersRequest{
			WorkspaceScope: namedWorkspace(workspace),
			PageToken:      pageToken,
		})
		return resp.GetProviders(), resp.GetNextPageToken(), err
	})
}

// ListSandboxProviderCredentialKeys returns the keys of the credentials each
// provider attached to the sandbox holds, by provider name.
func (r *RawExecClient) ListSandboxProviderCredentialKeys(ctx context.Context, workspace, sandboxName string) (map[string][]string, error) {
	return collectCredentialKeys(func(pageToken string) ([]*dm.Provider, string, error) {
		resp, err := r.client.ListSandboxProviders(ctx, &pb.ListSandboxProvidersRequest{
			Sandbox:        sandboxName,
			WorkspaceScope: namedWorkspace(workspace),
			PageToken:      pageToken,
		})
		return resp.GetProviders(), resp.GetNextPageToken(), err
	})
}

func namedWorkspace(workspace string) *dm.WorkspaceSelector {
	return &dm.WorkspaceSelector{
		Selection: &dm.WorkspaceSelector_Workspace{Workspace: workspace},
	}
}

// collectCredentialKeys walks every page of a provider listing.
func collectCredentialKeys(page func(pageToken string) ([]*dm.Provider, string, error)) (map[string][]string, error) {
	keys := map[string][]string{}
	pageToken := ""
	for {
		providers, next, err := page(pageToken)
		if err != nil {
			return nil, err
		}
		for _, provider := range providers {
			keys[provider.GetMetadata().GetName()] = credentialKeys(provider)
		}
		if next == "" {
			return keys, nil
		}
		if next == pageToken {
			return nil, errPageTokenRepeated
		}
		pageToken = next
	}
}

// credentialKeys returns the keys a provider holds credentials under, sorted.
// A gateway that redacts leaves them all in credentials; the handles are read
// as well so that one that does not still reports every key.
func credentialKeys(provider *dm.Provider) []string {
	keys := make([]string, 0, len(provider.GetCredentials())+len(provider.GetCredentialHandles()))
	for key := range provider.GetCredentials() {
		keys = append(keys, key)
	}
	for key := range provider.GetCredentialHandles() {
		if _, held := provider.GetCredentials()[key]; !held {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}
