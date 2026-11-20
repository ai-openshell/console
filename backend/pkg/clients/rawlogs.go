package clients

import (
	"context"

	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	"google.golang.org/grpc"
)

// WatchSandbox listens to sandbox events and extra fields that the SDK does not support such as logs.
func (r *RawExecClient) WatchSandbox(ctx context.Context, req *pb.WatchSandboxRequest) (grpc.ServerStreamingClient[pb.SandboxStreamEvent], error) {
	return r.client.WatchSandbox(ctx, req)
}
