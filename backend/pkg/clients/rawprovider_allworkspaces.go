package clients

import (
	"context"

	dm "github.com/ai-openshell/sdk-go/proto/datamodelv1"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
)

// ListProviderCredentialKeysAllWorkspaces returns the keys of the credentials
// each provider on the gateway holds, by workspace and then by provider name.
//
// It is ListProviderCredentialKeys with the all-workspaces selector, for the
// reason given in rawprovider.go: the SDK drops the keys. The gateway answers
// that selector for platform admins only, and its refusal comes back as the
// gRPC status it sent. Delete it together with rawprovider.go.
func (r *RawExecClient) ListProviderCredentialKeysAllWorkspaces(ctx context.Context) (map[string]map[string][]string, error) {
	keys := map[string]map[string][]string{}
	pageToken := ""
	for {
		resp, err := r.client.ListProviders(ctx, &pb.ListProvidersRequest{
			WorkspaceScope: &dm.WorkspaceSelector{
				Selection: &dm.WorkspaceSelector_AllWorkspaces{AllWorkspaces: &dm.AllWorkspaces{}},
			},
			PageToken: pageToken,
		})
		if err != nil {
			return nil, err
		}
		for _, provider := range resp.GetProviders() {
			workspace := provider.GetMetadata().GetWorkspace()
			if keys[workspace] == nil {
				keys[workspace] = map[string][]string{}
			}
			keys[workspace][provider.GetMetadata().GetName()] = credentialKeys(provider)
		}
		next := resp.GetNextPageToken()
		if next == "" {
			return keys, nil
		}
		if next == pageToken {
			return nil, errPageTokenRepeated
		}
		pageToken = next
	}
}
