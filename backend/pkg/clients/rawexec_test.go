package clients

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// gatewayMaxMessage is the largest gRPC message the gateway decodes
// (MAX_GRPC_DECODE_SIZE in upstream multiplex.rs). The fake server enforces
// the same limit, so a payload sent in one piece fails here as it does there.
const gatewayMaxMessage = 1 << 20

// fakeExecServer plays the gateway's ExecSandboxInteractive: it takes the
// start message, reads stdin frames until the client closes its side, then
// replays a fixed event sequence (stdout, stderr, exit).
type fakeExecServer struct { //nolint:govet // fieldalignment: test readability
	pb.UnimplementedOpenShellServer
	// failWith, when set, ends the stream with this status after the start
	// message instead of running anything.
	failWith error
	gotStart *pb.ExecSandboxRequest
	gotStdin []byte
	chunks   []int
	mu       sync.Mutex
	exitCode int32
	// exitEarly makes the command exit right after it starts, without reading
	// its stdin, the way dd does when it cannot open its output file.
	exitEarly bool
	// stdinEnded is set when the client closed its side of the stream, which
	// is what the gateway turns into end of input for the command.
	stdinEnded bool
	// done, when set, is closed when the call is over on the server.
	done chan struct{}
}

func (f *fakeExecServer) ExecSandboxInteractive(stream grpc.BidiStreamingServer[pb.ExecSandboxInput, pb.ExecSandboxEvent]) error {
	if f.done != nil {
		defer close(f.done)
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be a start payload")
	}
	f.mu.Lock()
	f.gotStart = start
	f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if !f.exitEarly {
		for {
			msg, recvErr := stream.Recv()
			if errors.Is(recvErr, io.EOF) {
				f.mu.Lock()
				f.stdinEnded = true
				f.mu.Unlock()
				break
			}
			if recvErr != nil {
				return recvErr
			}
			if _, isStdin := msg.GetPayload().(*pb.ExecSandboxInput_Stdin); !isStdin {
				return status.Error(codes.InvalidArgument, "expected stdin after exec start")
			}
			f.mu.Lock()
			f.gotStdin = append(f.gotStdin, msg.GetStdin()...)
			f.chunks = append(f.chunks, len(msg.GetStdin()))
			f.mu.Unlock()
		}
	}
	if err := stream.Send(&pb.ExecSandboxEvent{Payload: &pb.ExecSandboxEvent_Stdout{Stdout: &pb.ExecSandboxStdout{Data: []byte("out")}}}); err != nil {
		return err
	}
	if err := stream.Send(&pb.ExecSandboxEvent{Payload: &pb.ExecSandboxEvent_Stderr{Stderr: &pb.ExecSandboxStderr{Data: []byte("err")}}}); err != nil {
		return err
	}
	return stream.Send(&pb.ExecSandboxEvent{Payload: &pb.ExecSandboxEvent_Exit{Exit: &pb.ExecSandboxExit{ExitCode: f.exitCode}}})
}

// received returns what the fake has seen so far.
func (f *fakeExecServer) received() (start *pb.ExecSandboxRequest, stdin []byte, chunks []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotStart, f.gotStdin, f.chunks
}

func newTestRawExec(t *testing.T, fake pb.OpenShellServer) *RawExecClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.MaxRecvMsgSize(gatewayMaxMessage))
	pb.RegisterOpenShellServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return &RawExecClient{conn: conn, client: pb.NewOpenShellClient(conn)}
}

// binaryPayload is n bytes that no line discipline or text encoding would
// leave alone, and that differ from one chunk to the next.
func binaryPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/251)
	}
	return b
}

func TestExecWithStdinForwardsRawBytesNoTTY(t *testing.T) {
	fake := &fakeExecServer{exitCode: 0}
	rc := newTestRawExec(t, fake)

	// Binary payload with control bytes a PTY would corrupt.
	payload := []byte("bin\x00\x04\x03\x11\x13data")
	stdout, code, err := rc.ExecWithStdin(context.Background(), "default", "sb", []string{"dd", "of=/x"}, payload)
	if err != nil {
		t.Fatalf("ExecWithStdin: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != "outerr" {
		t.Errorf("stdout = %q, want %q (stdout+stderr merged)", stdout, "outerr")
	}
	start, stdin, _ := fake.received()
	if start.GetTty() {
		t.Error("Tty = true, want false (non-TTY exec required for binary fidelity)")
	}
	if len(start.GetStdin()) != 0 {
		t.Errorf("the start message carries %d bytes of stdin, want none: stdin is streamed", len(start.GetStdin()))
	}
	if !bytes.Equal(stdin, payload) {
		t.Errorf("stdin = %q, want %q (exact bytes, unmangled)", stdin, payload)
	}
	if start.GetWorkspaceScope().GetWorkspace() != "default" {
		t.Errorf("workspace = %q, want default", start.GetWorkspaceScope().GetWorkspace())
	}
	if start.GetSandbox() != "sb" {
		t.Errorf("sandbox = %q, want sb", start.GetSandbox())
	}
	if got := start.GetCommand(); len(got) != 2 || got[0] != "dd" || got[1] != "of=/x" {
		t.Errorf("command = %v", got)
	}
}

// A payload larger than one gRPC message the gateway accepts has to arrive
// whole and in order, in messages that each fit.
func TestExecWithStdinStreamsPayloadsLargerThanOneMessage(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{name: "just over the gateway's message limit", size: gatewayMaxMessage + 1},
		{name: "several chunks and a remainder", size: 3*stdinChunkSize + 7},
		{name: "an exact multiple of the chunk size", size: 2 * stdinChunkSize},
		{name: "several times the message limit", size: 5 * gatewayMaxMessage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeExecServer{}
			rc := newTestRawExec(t, fake)
			payload := binaryPayload(tc.size)

			_, code, err := rc.ExecWithStdin(context.Background(), "default", "sb", []string{"dd", "of=/x"}, payload)
			if err != nil {
				t.Fatalf("ExecWithStdin with %d bytes: %v", tc.size, err)
			}
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			_, stdin, chunks := fake.received()
			if !bytes.Equal(stdin, payload) {
				t.Errorf("the server received %d bytes that differ from the %d sent", len(stdin), len(payload))
			}
			wantChunks := (tc.size + stdinChunkSize - 1) / stdinChunkSize
			if len(chunks) != wantChunks {
				t.Errorf("stdin arrived in %d messages, want %d", len(chunks), wantChunks)
			}
			for i, n := range chunks {
				if n > stdinChunkSize || n == 0 {
					t.Errorf("message %d carries %d bytes, want 1 to %d", i, n, stdinChunkSize)
				}
			}
		})
	}
}

// An empty file is a start message and a closed stream: the command still
// runs, and sees end of input at once.
func TestExecWithStdinEmptyPayload(t *testing.T) {
	fake := &fakeExecServer{}
	rc := newTestRawExec(t, fake)

	_, code, err := rc.ExecWithStdin(context.Background(), "default", "sb", []string{"dd", "of=/x"}, nil)
	if err != nil {
		t.Fatalf("ExecWithStdin: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	start, stdin, chunks := fake.received()
	if start == nil {
		t.Fatal("the server saw no start message")
	}
	if len(stdin) != 0 || len(chunks) != 0 {
		t.Errorf("the server received %d bytes in %d stdin messages, want none", len(stdin), len(chunks))
	}
}

func TestExecWithStdinNonZeroExit(t *testing.T) {
	rc := newTestRawExec(t, &fakeExecServer{exitCode: 1})
	_, code, err := rc.ExecWithStdin(context.Background(), "default", "sb", []string{"dd"}, []byte("x"))
	if err != nil {
		t.Fatalf("ExecWithStdin: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

// A command that exits without reading its stdin ends the stream while the
// payload is still being sent. That is a result, not a hang and not an error:
// the caller gets the command's output and its exit code.
func TestExecWithStdinCommandExitsBeforeReadingStdin(t *testing.T) {
	rc := newTestRawExec(t, &fakeExecServer{exitCode: 1, exitEarly: true})

	type result struct {
		err    error
		stdout string
		code   int
	}
	done := make(chan result, 1)
	go func() {
		stdout, code, err := rc.ExecWithStdin(context.Background(), "default", "sb", []string{"dd", "of=/usr/x"}, binaryPayload(8*gatewayMaxMessage))
		done <- result{stdout: stdout, code: code, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("ExecWithStdin: %v", got.err)
		}
		if got.code != 1 || got.stdout != "outerr" {
			t.Errorf("exit code = %d with output %q, want 1 with %q", got.code, got.stdout, "outerr")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ExecWithStdin did not return after the command exited without reading its stdin")
	}
}

// The gateway's own refusal comes back as the gRPC status it sent, so the
// handler can map it like any other gateway error.
func TestExecWithStdinReturnsTheGatewayStatus(t *testing.T) {
	rc := newTestRawExec(t, &fakeExecServer{failWith: status.Error(codes.FailedPrecondition, "sandbox is not ready")})

	_, code, err := rc.ExecWithStdin(context.Background(), "default", "sb", []string{"dd", "of=/x"}, binaryPayload(2*gatewayMaxMessage))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("error = %v, want the gateway's FailedPrecondition", err)
	}
	if code != -1 {
		t.Errorf("exit code = %d, want -1: the command never ran", code)
	}
}

// smallReads hands out its bytes a few at a time, as a multipart part read
// off a network connection does.
type smallReads struct {
	data []byte
	step int
}

func (r *smallReads) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data[:min(r.step, len(r.data))])
	r.data = r.data[n:]
	return n, nil
}

// A reader that yields little at a time must not become as many messages: the
// payload is gathered into full chunks, and still arrives whole and in order
// in messages the gateway accepts.
func TestExecWithStdinStreamGathersSmallReads(t *testing.T) {
	tests := []struct {
		name string
		size int
		step int
	}{
		{name: "4 KiB reads over several chunks", size: 3*stdinChunkSize + 7, step: 4096},
		{name: "odd-sized reads that straddle chunk boundaries", size: 2*stdinChunkSize + 1000, step: 1000},
		{name: "several times the gateway's message limit", size: 5 * gatewayMaxMessage, step: 32 << 10},
		{name: "less than one chunk", size: 100, step: 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeExecServer{}
			rc := newTestRawExec(t, fake)
			payload := binaryPayload(tc.size)

			_, code, err := rc.ExecWithStdinStream(context.Background(), "default", "sb", []string{"dd", "of=/x"},
				&smallReads{data: append([]byte(nil), payload...), step: tc.step})
			if err != nil {
				t.Fatalf("ExecWithStdinStream with %d bytes: %v", tc.size, err)
			}
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			start, stdin, chunks := fake.received()
			if start.GetTty() {
				t.Error("Tty = true, want false")
			}
			if !bytes.Equal(stdin, payload) {
				t.Errorf("the server received %d bytes that differ from the %d sent", len(stdin), len(payload))
			}
			if want := (tc.size + stdinChunkSize - 1) / stdinChunkSize; len(chunks) != want {
				t.Errorf("stdin arrived in %d messages, want %d: reads of %d bytes were not gathered", len(chunks), want, tc.step)
			}
		})
	}
}

// failingReader yields data and then fails with err.
type failingReader struct {
	err  error
	data []byte
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// A stdin that fails is not a stdin that ended. The command must never be
// told its input is complete, because it would then report a truncated file
// as written, and the caller gets the reader's own error back.
func TestExecWithStdinStreamReaderFailure(t *testing.T) {
	errBody := errors.New("request body: connection reset")
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name string
		err  error
		size int
	}{
		{name: "before the first byte", err: errBody, size: 0},
		{name: "part way through the first chunk", err: errBody, size: 1000},
		{name: "on a chunk boundary", err: errBody, size: 2 * stdinChunkSize},
		{name: "after several chunks", err: errBody, size: 3*stdinChunkSize + 500},
		// What a multipart part returns when the body ends before the
		// closing boundary, and also what io.ReadFull makes of a reader that
		// simply ended: the two must not be mixed up.
		{name: "an unexpected EOF part way through a chunk", err: io.ErrUnexpectedEOF, size: 1000},
		{name: "an unexpected EOF on a chunk boundary", err: io.ErrUnexpectedEOF, size: stdinChunkSize},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeExecServer{done: make(chan struct{})}
			rc := newTestRawExec(t, fake)

			type result struct {
				err  error
				code int
			}
			returned := make(chan result, 1)
			go func() {
				_, code, err := rc.ExecWithStdinStream(context.Background(), "default", "sb", []string{"dd", "of=/x"},
					&failingReader{data: binaryPayload(tc.size), err: tc.err})
				returned <- result{code: code, err: err}
			}()
			select {
			case got := <-returned:
				if !errors.Is(got.err, tc.err) {
					t.Errorf("error = %v, want the reader's own: %v", got.err, tc.err)
				}
				if got.code == 0 {
					t.Error("exit code = 0: a failed upload reads as one that succeeded")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("ExecWithStdinStream did not return after its stdin failed")
			}

			select {
			case <-fake.done:
			case <-time.After(10 * time.Second):
				t.Fatal("the call is still open on the server 10s after stdin failed")
			}
			fake.mu.Lock()
			ended := fake.stdinEnded
			fake.mu.Unlock()
			if ended {
				t.Error("the server was told stdin ended: the command would write a truncated file and exit 0")
			}
		})
	}
}
