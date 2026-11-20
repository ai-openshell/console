package services

import (
	"context"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

type ProviderServiceInterface interface {
	openshell.ProviderInterface
}

type ProviderService struct {
	openshell.ProviderInterface
}

func NewProviderService(client openshell.ProviderInterface) *ProviderService {
	return &ProviderService{ProviderInterface: client}
}

// ProviderCredentialKeyReader reads the keys of the credentials providers
// hold. The gateway reports them on every provider it returns and the SDK
// drops them, so they are read beside the SDK call that returns the provider
// itself. Keys only: a credential's value is never read. Implemented by
// clients.RawExecClient.
type ProviderCredentialKeyReader interface {
	// ProviderCredentialKeys returns the keys the named provider holds.
	ProviderCredentialKeys(ctx context.Context, workspace, name string) ([]string, error)
	// ListProviderCredentialKeys returns the keys each provider in the
	// workspace holds, by provider name.
	ListProviderCredentialKeys(ctx context.Context, workspace string) (map[string][]string, error)
	// ListSandboxProviderCredentialKeys returns the keys each provider
	// attached to the sandbox holds, by provider name.
	ListSandboxProviderCredentialKeys(ctx context.Context, workspace, sandboxName string) (map[string][]string, error)
}
