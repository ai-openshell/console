package services

import (
	"context"
	"fmt"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	dm "github.com/ai-openshell/sdk-go/proto/datamodelv1"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	"google.golang.org/grpc"
)

// SandboxRawExec is the interface that holds raw grpc calls to the openshell server.
// Currently, this is meant for watching logs and is implemented by the rawexec client
type SandboxRawExec interface {
	WatchSandbox(ctx context.Context, req *pb.WatchSandboxRequest) (grpc.ServerStreamingClient[pb.SandboxStreamEvent], error)
}

type SandboxLogStream interface {
	Recv() (*pb.SandboxStreamEvent, error)
}

// SandboxServiceInterface is the SDK's sandbox sub-client, its suspend and
// resume sub-client (a separate interface in the SDK, on the same client) and
// the raw log watch.
type SandboxServiceInterface interface {
	openshell.SandboxInterface
	openshell.SandboxSuspendResumeInterface
	WatchSandboxLogs(
		ctx context.Context,
		workspace string,
		sandbox string,
		logTailLines uint32,
		logSources []string,
		logMinLevel string,
	) (SandboxLogStream, error)
}

type SandboxService struct {
	openshell.SandboxInterface
	openshell.SandboxSuspendResumeInterface
	rawExec SandboxRawExec
}

func NewSandboxService(client openshell.SandboxInterface, suspendResume openshell.SandboxSuspendResumeInterface, rawExecClient SandboxRawExec) SandboxServiceInterface {
	return &SandboxService{SandboxInterface: client, SandboxSuspendResumeInterface: suspendResume, rawExec: rawExecClient}
}

func (s *SandboxService) WatchSandboxLogs(ctx context.Context, workspace string, sandbox string, logTailLines uint32, logSources []string, logMinLevel string) (SandboxLogStream, error) {
	req := &pb.WatchSandboxRequest{
		WorkspaceScope: &dm.WorkspaceSelector{
			Selection: &dm.WorkspaceSelector_Workspace{
				Workspace: workspace,
			},
		},
		Sandbox:      sandbox,
		FollowStatus: true,
		FollowLogs:   true,
		LogTailLines: logTailLines,
		LogSources:   logSources,
		LogMinLevel:  logMinLevel,
	}

	stream, err := s.rawExec.WatchSandbox(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to start log stream: %w", err)
	}

	return stream, nil
}
