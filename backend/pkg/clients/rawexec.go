package clients

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type RawExecClientInterface interface {
	Close() error
	ExecWithStdin(ctx context.Context, workspace, sandboxName string, command []string, stdin []byte) (string, int, error)
	ExecWithStdinStream(ctx context.Context, workspace, sandboxName string, command []string, stdin io.Reader) (string, int, error)
	ListProviderProfiles(ctx context.Context, workspace string) ([]*pb.ProviderProfile, error)
	GetProviderProfile(ctx context.Context, workspace, id string) (*pb.ProviderProfile, error)
	ImportProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.ImportProviderProfilesResponse, error)
	UpdateProviderProfile(ctx context.Context, workspace, id string, expectedResourceVersion uint64, item *pb.ProviderProfileImportItem) (*pb.UpdateProviderProfilesResponse, error)
	LintProviderProfiles(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) (*pb.LintProviderProfilesResponse, error)
	checkStablePlaceholder(ctx context.Context, workspace string, items []*pb.ProviderProfileImportItem) error
	ProviderCredentialKeys(ctx context.Context, workspace, name string) ([]string, error)
	ListProviderCredentialKeys(ctx context.Context, workspace string) (map[string][]string, error)
	ListSandboxProviderCredentialKeys(ctx context.Context, workspace, sandboxName string) (map[string][]string, error)
	ListProviderCredentialKeysAllWorkspaces(ctx context.Context) (map[string]map[string][]string, error)
	WatchSandbox(ctx context.Context, req *pb.WatchSandboxRequest) (grpc.ServerStreamingClient[pb.SandboxStreamEvent], error)
}

// RawExecClient calls the gateway through the SDK's generated proto client for
// the three things the OpenShell Go SDK's own client does not offer. It shares
// the same address, TLS, and per-request bearer forwarding as the main SDK
// client.
//
// The first, in this file, is running a command in a sandbox with stdin piped
// in and no TTY: the SDK's Run takes no stdin and its Interactive always asks
// for a PTY. Binary file uploads need it, so that they run through a clean
// pipe — `dd` with raw stdin bytes — instead of a PTY, whose line discipline
// (EOF/flow-control bytes, CR/LF translation, echo) silently corrupts binary
// content. It uses the gateway's ExecSandboxInteractive RPC with tty=false.
// The PTY is the SDK wrapper's choice, not the RPC's: the gateway allocates
// one only when the start message asks for it, relays every stdin frame as it
// arrives, and closes the command's stdin when the request stream ends. The
// unary ExecSandbox RPC also takes stdin, but as one field of one message, and
// the gateway refuses any gRPC message over 1 MiB.
//
// The second, in rawprovider.go, is reading which credentials a provider
// holds. The third, in rawprofile.go, is reading and writing a provider
// profile with its endpoints whole.
type RawExecClient struct {
	conn   *grpc.ClientConn
	client pb.OpenShellClient
}

// NewRawExecClient dials the gateway. address is host:port (no URL scheme).
// When useTLS is set, caFile (optional) verifies the server and clientCert +
// clientKey (optional, both required together) enable mTLS client auth — the
// same knobs the SDK client uses, so upload honors gateway mTLS too.
func NewRawExecClient(address, caFile, clientCert, clientKey string, useTLS bool) (*RawExecClient, error) {
	var creds credentials.TransportCredentials
	if useTLS {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if caFile != "" {
			pem, err := os.ReadFile(caFile)
			if err != nil {
				return nil, fmt.Errorf("read gateway CA cert: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("no valid certificates in %s", caFile)
			}
			tlsCfg.RootCAs = pool
		}
		if clientCert != "" && clientKey != "" {
			cert, err := tls.LoadX509KeyPair(clientCert, clientKey)
			if err != nil {
				return nil, fmt.Errorf("load gateway client cert/key: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
		creds = credentials.NewTLS(tlsCfg)
	} else {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(ContextAuthProvider{RequireTLS: useTLS}),
	)
	if err != nil {
		return nil, err
	}
	return &RawExecClient{conn: conn, client: pb.NewOpenShellClient(conn)}, nil
}

// Close closes the underlying gRPC connection.
func (r *RawExecClient) Close() error { return r.conn.Close() }

// stdinChunkSize is how much of the payload one stream message carries. The
// gateway refuses a gRPC message over 1 MiB (MAX_GRPC_DECODE_SIZE in upstream
// multiplex.rs), so a chunk stays well under that.
const stdinChunkSize = 256 << 10

// ExecWithStdin runs command in the named workspace sandbox with stdin piped
// in and no TTY, returning merged stdout+stderr and the process exit code.
// exitCode is -1 if the gateway sent no exit event.
//
// stdin is streamed in chunks, so its size is bounded by the caller and not by
// the gateway's per-message limit.
func (r *RawExecClient) ExecWithStdin(ctx context.Context, workspace, sandboxName string, command []string, stdin []byte) (string, int, error) {
	return r.ExecWithStdinStream(ctx, workspace, sandboxName, command, bytes.NewReader(stdin))
}

// ExecWithStdinStream is ExecWithStdin with stdin read as it is sent, so the
// payload is never held whole: one chunk is in flight at a time, and the
// gateway's flow control decides how fast the reader is drained.
//
// A stdin that fails part way is not a shorter stdin. The command is not told
// its input ended, the stream is cancelled instead, and the reader's own error
// is returned. What the command wrote before that stays written.
//
// The reader is not touched again once this returns.
func (r *RawExecClient) ExecWithStdinStream(ctx context.Context, workspace, sandboxName string, command []string, stdin io.Reader) (string, int, error) {
	// Cancelling ends the send side when the receive side returns first, as it
	// does when the command exits without reading all of its stdin.
	ctx, cancel := context.WithCancel(ctx)
	stream, err := r.client.ExecSandboxInteractive(ctx)
	if err != nil {
		cancel()
		return "", 0, err
	}
	start := &pb.ExecSandboxRequest{
		WorkspaceScope: namedWorkspace(workspace),
		Sandbox:        sandboxName,
		Command:        command,
		Tty:            false,
	}
	sent := make(chan error, 1)
	go func() {
		sendErr := sendStdin(stream, start, stdin)
		var readErr *stdinReadError
		if errors.As(sendErr, &readErr) {
			cancel()
		}
		sent <- sendErr
	}()

	var out strings.Builder
	exitCode := -1
	var recvErr error
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			recvErr = err
			break
		}
		switch p := ev.Payload.(type) {
		case *pb.ExecSandboxEvent_Stdout:
			out.Write(p.Stdout.GetData())
		case *pb.ExecSandboxEvent_Stderr:
			out.Write(p.Stderr.GetData())
		case *pb.ExecSandboxEvent_Exit:
			exitCode = int(p.Exit.GetExitCode())
		}
	}
	// The sender holds stdin, so it must not outlive this call.
	cancel()
	var readErr *stdinReadError
	if errors.As(<-sent, &readErr) {
		return out.String(), exitCode, readErr.err
	}
	return out.String(), exitCode, recvErr
}

// stdinReadError is a failure of the reader stdin comes from, as opposed to a
// failure of the stream it is sent on.
type stdinReadError struct{ err error }

func (e *stdinReadError) Error() string { return "read stdin: " + e.err.Error() }

func (e *stdinReadError) Unwrap() error { return e.err }

// sendStdin sends the start message, then stdin in chunks, then closes the
// send side, which is what tells the gateway to close the command's stdin.
//
// A stream error is not reported by the caller: when the gateway ends the
// stream early, Send fails with io.EOF and the status that says why arrives
// through Recv, and a failure on this side cancels the stream, which Recv
// reports as well. A failure to read stdin is reported, as a stdinReadError.
func sendStdin(stream grpc.BidiStreamingClient[pb.ExecSandboxInput, pb.ExecSandboxEvent], start *pb.ExecSandboxRequest, stdin io.Reader) error {
	if err := stream.Send(&pb.ExecSandboxInput{Payload: &pb.ExecSandboxInput_Start{Start: start}}); err != nil {
		return err
	}
	for {
		// A chunk of its own for every message: gRPC may still hold a message
		// after Send returns.
		chunk := make([]byte, stdinChunkSize)
		n, ended, err := fillChunk(stdin, chunk)
		if err != nil {
			return &stdinReadError{err: err}
		}
		if n > 0 {
			if err := stream.Send(&pb.ExecSandboxInput{Payload: &pb.ExecSandboxInput_Stdin{Stdin: chunk[:n]}}); err != nil {
				return err
			}
		}
		if ended {
			return stream.CloseSend()
		}
	}
}

// fillChunk reads until chunk is full or the reader ends, so that a reader
// which hands out a few kilobytes at a time does not become as many messages.
//
// Only io.EOF is the end. io.ReadFull would not do here: it reports a reader
// that ended part way through the chunk as io.ErrUnexpectedEOF, which is also
// what a multipart body cut short by a dropped connection returns, and the two
// must not be confused. One is a whole file and the other is not.
func fillChunk(r io.Reader, chunk []byte) (n int, ended bool, err error) {
	for n < len(chunk) {
		read, readErr := r.Read(chunk[n:])
		n += read
		if readErr == io.EOF {
			return n, true, nil
		}
		if readErr != nil {
			return n, false, readErr
		}
	}
	return n, false, nil
}
