package clients

import (
	"context"

	"github.com/ai-openshell/console/backend/pkg/auth"
)

// ContextAuthProvider implements grpc.PerRPCCredentials by reading the JWT
// from the request context on every gRPC call. This allows a single shared
// SDK client to forward per-request user tokens to the gateway.
type ContextAuthProvider struct {
	RequireTLS bool
}

func (c ContextAuthProvider) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	token := auth.TokenFromContext(ctx)
	if token == "" {
		return nil, nil
	}
	return map[string]string{"authorization": "Bearer " + token}, nil
}

func (c ContextAuthProvider) RequireTransportSecurity() bool {
	return c.RequireTLS
}
