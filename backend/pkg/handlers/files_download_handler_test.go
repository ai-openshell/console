package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// loginShellNoise is what a sandbox under the base policy prints on stderr
// before every command: its login shell may not read /etc/profile. It ends in
// the same words as a real refusal and must never be taken for one.
const loginShellNoise = "/bin/bash: /etc/profile: Permission denied\n"

func stdoutChunk(data string) openshell.ExecChunk {
	return openshell.ExecChunk{Stream: openshell.StreamStdout, Data: []byte(data)}
}

func stderrChunk(data string) openshell.ExecChunk {
	return openshell.ExecChunk{Stream: openshell.StreamStderr, Data: []byte(data)}
}

// fakeExecStream plays the SDK's ExecStream: it hands out its chunks, then
// ends the way it is told to.
type fakeExecStream struct { //nolint:govet // fieldalignment: test readability
	// ctx, when set, is what the stream was opened with: block waits on it.
	ctx context.Context
	// failWith ends the stream with this error after the chunks, the way a
	// gateway that goes away does.
	failWith error
	// exitErr is what ExitCode returns when the stream ended without an exit
	// event.
	exitErr error
	chunks  []openshell.ExecChunk
	// every is how long each chunk takes to arrive.
	every time.Duration
	// block, after the chunks, waits for ctx to end instead of ending.
	block    bool
	exitCode int
	pos      int
	mu       sync.Mutex
	closed   bool
}

func (s *fakeExecStream) Next() (*openshell.ExecChunk, error) {
	// A cancelled stream yields nothing more, as the SDK's does not.
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-time.After(s.every):
		}
	}
	if s.pos < len(s.chunks) {
		chunk := s.chunks[s.pos]
		s.pos++
		return &chunk, nil
	}
	if s.block {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	if s.failWith != nil {
		return nil, s.failWith
	}
	return nil, io.EOF
}

func (s *fakeExecStream) ExitCode() (int, error) {
	if s.exitErr != nil {
		return -1, s.exitErr
	}
	return s.exitCode, nil
}

func (s *fakeExecStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeExecStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// downloadFixture is a files handler over a sandbox whose stat answers with
// stat and whose cat or tar is stream.
type downloadFixture struct { //nolint:govet // fieldalignment: test readability
	stat   *openshell.ExecResult
	stream *fakeExecStream
	// streamErr fails the opening of the stream.
	streamErr error
	// statErr fails the stat call itself, as the gateway would.
	statErr error

	mu       sync.Mutex
	statCmd  []string
	statOpts []openshell.ExecOptions
	// streamCmd is nil when no stream was opened.
	streamCmd []string
}

func (f *downloadFixture) router(cfg FilesHandlerConfig) http.Handler {
	sdk := &mockSDK{}
	sdk.exec.runFn = func(_ context.Context, _, _ string, command []string, opts ...openshell.ExecOptions) (*openshell.ExecResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.statCmd, f.statOpts = command, opts
		if f.statErr != nil {
			return nil, f.statErr
		}
		return f.stat, nil
	}
	sdk.exec.streamFn = func(ctx context.Context, _, _ string, command []string, _ ...openshell.ExecOptions) (openshell.ExecStream, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.streamCmd = command
		if f.streamErr != nil {
			return nil, f.streamErr
		}
		f.stream.ctx = ctx
		return f.stream, nil
	}
	handler := NewFilesHandler(services.NewFileService(&mockUploader{}), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), cfg)
	r := chi.NewRouter()
	// The middleware the app serves this route through: an aborted download
	// has to get past its recoverer.
	r.Use(chimiddleware.Recoverer)
	r.Get("/workspaces/{workspace}/sandboxes/{name}/files", handler.DownloadFile)
	return r
}

func (f *downloadFixture) commands() (stat, stream []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statCmd, f.streamCmd
}

func statSays(kind string) *openshell.ExecResult {
	return &openshell.ExecResult{Stdout: []byte(kind + "\n"), Stderr: []byte(loginShellNoise)}
}

func statFails(message string) *openshell.ExecResult {
	return &openshell.ExecResult{ExitCode: 1, Stderr: []byte(loginShellNoise + message + "\n")}
}

func downloadPath(p string) string {
	return "/workspaces/default/sandboxes/sb/files?path=" + strings.ReplaceAll(p, " ", "%20")
}

// A download that succeeds: what is sent, under which name, and which command
// in the sandbox produced it.
func TestDownloadStreamsFilesAndDirectories(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name            string
		path            string
		stat            *openshell.ExecResult
		chunks          []openshell.ExecChunk
		wantCommand     []string
		wantBody        string
		wantContentType string
		wantFilename    string
	}{
		{
			name:            "a file is sent as it is",
			path:            "/sandbox/out/report.bin",
			stat:            statSays("regular file"),
			chunks:          []openshell.ExecChunk{stdoutChunk("\x00\x01"), stdoutChunk("\xff\xfe")},
			wantCommand:     []string{"cat", "/sandbox/out/report.bin"},
			wantBody:        "\x00\x01\xff\xfe",
			wantContentType: "application/octet-stream",
			wantFilename:    `"report.bin"`,
		},
		{
			name:            "an empty file is an empty 200",
			path:            "/sandbox/empty",
			stat:            statSays("regular empty file"),
			wantCommand:     []string{"cat", "/sandbox/empty"},
			wantBody:        "",
			wantContentType: "application/octet-stream",
			wantFilename:    `"empty"`,
		},
		{
			name:            "a directory is sent as a tar archive of its contents",
			path:            "/sandbox/project",
			stat:            statSays("directory"),
			chunks:          []openshell.ExecChunk{stdoutChunk("tar-bytes")},
			wantCommand:     []string{"tar", "cf", "-", "-C", "/sandbox/project", "."},
			wantBody:        "tar-bytes",
			wantContentType: "application/x-tar",
			wantFilename:    `"project.tar"`,
		},
		{
			name:            "a directory is recognised by what the sandbox reports, not by a trailing slash",
			path:            "/sandbox/my dir/",
			stat:            statSays("directory"),
			chunks:          []openshell.ExecChunk{stdoutChunk("tar-bytes")},
			wantCommand:     []string{"tar", "cf", "-", "-C", "/sandbox/my dir/", "."},
			wantBody:        "tar-bytes",
			wantContentType: "application/x-tar",
			wantFilename:    `"my dir.tar"`,
		},
		{
			name:            "the root directory is named after the sandbox",
			path:            "/",
			stat:            statSays("directory"),
			chunks:          []openshell.ExecChunk{stdoutChunk("tar-bytes")},
			wantCommand:     []string{"tar", "cf", "-", "-C", "/", "."},
			wantBody:        "tar-bytes",
			wantContentType: "application/x-tar",
			wantFilename:    `"sb.tar"`,
		},
		{
			name: "what the command prints on stderr is not part of the file",
			path: "/sandbox/a.txt",
			stat: statSays("regular file"),
			chunks: []openshell.ExecChunk{
				stderrChunk(loginShellNoise), stdoutChunk("con"), stderrChunk("cat: warning\n"), stdoutChunk("tent"),
			},
			wantCommand:     []string{"cat", "/sandbox/a.txt"},
			wantBody:        "content",
			wantContentType: "application/octet-stream",
			wantFilename:    `"a.txt"`,
		},
		{
			name:            "a sandbox without stat still serves files",
			path:            "/sandbox/a.txt",
			stat:            &openshell.ExecResult{ExitCode: 127, Stderr: []byte("bash: line 1: stat: command not found\n")},
			chunks:          []openshell.ExecChunk{stdoutChunk("content")},
			wantCommand:     []string{"cat", "/sandbox/a.txt"},
			wantBody:        "content",
			wantContentType: "application/octet-stream",
			wantFilename:    `"a.txt"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &downloadFixture{stat: tc.stat, stream: &fakeExecStream{chunks: tc.chunks}}
			w := httptest.NewRecorder()
			f.router(FilesHandlerConfig{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, downloadPath(tc.path), nil))

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if w.Body.String() != tc.wantBody {
				t.Errorf("body = %q, want %q", w.Body.String(), tc.wantBody)
			}
			if got := w.Header().Get("Content-Type"); got != tc.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantContentType)
			}
			if got, want := w.Header().Get("Content-Disposition"), "attachment; filename="+tc.wantFilename; got != want {
				t.Errorf("Content-Disposition = %q, want %q", got, want)
			}
			if got := w.Header().Get("Content-Length"); got != "" {
				t.Errorf("Content-Length = %q, want none: the length is not known before the command ends", got)
			}
			stat, stream := f.commands()
			if want := []string{"stat", "-L", "-c", "%F", "--", tc.path}; !reflect.DeepEqual(stat, want) {
				t.Errorf("probe = %q, want %q", stat, want)
			}
			if !reflect.DeepEqual(stream, tc.wantCommand) {
				t.Errorf("command = %q, want %q", stream, tc.wantCommand)
			}
			if len(f.statOpts) != 1 || f.statOpts[0].Env["LC_ALL"] != "C" {
				t.Errorf("probe options = %+v, want LC_ALL=C so the messages read are in English", f.statOpts)
			}
			if !f.stream.isClosed() {
				t.Error("the exec stream was left open")
			}
		})
	}
}

// A download that is refused before any of it is sent is an error response,
// never a 200.
func TestDownloadRefusedBeforeItStarts(t *testing.T) {
	notFound := &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name       string
		path       string
		stat       *openshell.ExecResult
		statErr    error
		stream     *fakeExecStream
		streamErr  error
		wantStatus int
		wantCode   apiutils.ResponseCode
		// wantStream says whether a command to read the path was run at all.
		wantStream bool
	}{
		{
			name:       "no such path",
			path:       "/sandbox/nope",
			stat:       statFails("stat: cannot statx '/sandbox/nope': No such file or directory"),
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.FileNotFound,
		},
		{
			name:       "a parent that is a file",
			path:       "/sandbox/a.txt/x",
			stat:       statFails("stat: cannot statx '/sandbox/a.txt/x': Not a directory"),
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.FileNotFound,
		},
		{
			name:       "busybox words it differently",
			path:       "/sandbox/nope",
			stat:       statFails("stat: can't stat '/sandbox/nope': No such file or directory"),
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.FileNotFound,
		},
		{
			name:       "a path the sandbox may not look at",
			path:       "/root/x",
			stat:       statFails("stat: cannot statx '/root/x': Permission denied"),
			wantStatus: http.StatusForbidden,
			wantCode:   apiutils.PermissionDenied,
		},
		{
			name:       "a missing file whose name says permission denied",
			path:       "/sandbox/Permission denied",
			stat:       statFails("stat: cannot statx '/sandbox/Permission denied': No such file or directory"),
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.FileNotFound,
		},
		{
			name:       "a failure stat does not explain",
			path:       "/sandbox/x",
			stat:       statFails("stat: cannot statx '/sandbox/x': Input/output error"),
			wantStatus: http.StatusBadGateway,
			wantCode:   apiutils.FileDownloadFailed,
		},
		{
			name:       "a device",
			path:       "/dev/zero",
			stat:       statSays("character special file"),
			wantStatus: http.StatusBadRequest,
			wantCode:   apiutils.InvalidPath,
		},
		{
			name:       "a FIFO",
			path:       "/sandbox/pipe",
			stat:       statSays("fifo"),
			wantStatus: http.StatusBadRequest,
			wantCode:   apiutils.InvalidPath,
		},
		{
			name:       "a traversal in the path",
			path:       "/sandbox/../etc/passwd",
			wantStatus: http.StatusBadRequest,
			wantCode:   apiutils.InvalidPath,
		},
		{
			name:       "a relative path",
			path:       "sandbox/a.txt",
			wantStatus: http.StatusBadRequest,
			wantCode:   apiutils.InvalidPath,
		},
		{
			name:       "an unknown sandbox",
			path:       "/sandbox/a.txt",
			statErr:    notFound,
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.NotFound,
		},
		{
			name:       "the sandbox goes away between the probe and the read",
			path:       "/sandbox/a.txt",
			stat:       statSays("regular file"),
			streamErr:  notFound,
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.NotFound,
			wantStream: true,
		},
		{
			name: "a file that can be seen but not read",
			path: "/sandbox/locked",
			stat: statSays("regular file"),
			stream: &fakeExecStream{
				chunks:   []openshell.ExecChunk{stderrChunk(loginShellNoise + "cat: /sandbox/locked: Permission denied\n")},
				exitCode: 1,
			},
			wantStatus: http.StatusForbidden,
			wantCode:   apiutils.PermissionDenied,
			wantStream: true,
		},
		{
			name: "a file removed between the probe and the read",
			path: "/sandbox/gone",
			stat: statSays("regular file"),
			stream: &fakeExecStream{
				chunks:   []openshell.ExecChunk{stderrChunk("cat: /sandbox/gone: No such file or directory\n")},
				exitCode: 1,
			},
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.FileNotFound,
			wantStream: true,
		},
		{
			name: "a directory that cannot be opened",
			path: "/sandbox/private",
			stat: statSays("directory"),
			stream: &fakeExecStream{
				chunks: []openshell.ExecChunk{stderrChunk(loginShellNoise +
					"tar: /sandbox/private: Cannot open: Permission denied\ntar: Error is not recoverable: exiting now\n")},
				exitCode: 2,
			},
			wantStatus: http.StatusForbidden,
			wantCode:   apiutils.PermissionDenied,
			wantStream: true,
		},
		{
			name: "a sandbox without tar",
			path: "/sandbox/project",
			stat: statSays("directory"),
			stream: &fakeExecStream{
				chunks:   []openshell.ExecChunk{stderrChunk(loginShellNoise + "bash: line 1: tar: command not found\n")},
				exitCode: 127,
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   apiutils.FileDownloadFailed,
			wantStream: true,
		},
		{
			name:       "the gateway ends the stream before the command reports",
			path:       "/sandbox/a.txt",
			stat:       statSays("regular file"),
			stream:     &fakeExecStream{failWith: &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "relay closed"}},
			wantStatus: http.StatusBadGateway,
			wantCode:   apiutils.GatewayUnavailable,
			wantStream: true,
		},
		{
			name:       "the stream ends without an exit code",
			path:       "/sandbox/a.txt",
			stat:       statSays("regular file"),
			stream:     &fakeExecStream{exitErr: &openshell.StatusError{Code: openshell.ErrorInternal, Message: "stream ended without exit event"}},
			wantStatus: http.StatusInternalServerError,
			wantCode:   apiutils.Internal,
			wantStream: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &downloadFixture{stat: tc.stat, statErr: tc.statErr, stream: tc.stream, streamErr: tc.streamErr}
			w := httptest.NewRecorder()
			f.router(FilesHandlerConfig{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, downloadPath(tc.path), nil))

			wantErrorResponse(t, w, tc.wantStatus, tc.wantCode)
			if got := w.Header().Get("Content-Disposition"); got != "" {
				t.Errorf("Content-Disposition = %q on an error response, want none", got)
			}
			if _, stream := f.commands(); (stream != nil) != tc.wantStream {
				t.Errorf("command run to read the path = %q, want one: %v", stream, tc.wantStream)
			}
		})
	}
}

// Once the body has started there is no status left to change, so a failure
// has to break the transfer: a client must be able to tell a download that
// failed part way from a complete one.
func TestDownloadFailureAfterTheStreamStarted(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name   string
		stat   *openshell.ExecResult
		stream *fakeExecStream
	}{
		{
			name: "tar could not read a member and exits 2 after the rest",
			stat: statSays("directory"),
			stream: &fakeExecStream{
				chunks: []openshell.ExecChunk{
					stdoutChunk(strings.Repeat("a", 10240)),
					stderrChunk("tar: ./sub/locked: Cannot open: Permission denied\n"),
					stdoutChunk(strings.Repeat("b", 10240)),
					stderrChunk("tar: Exiting with failure status due to previous errors\n"),
				},
				exitCode: 2,
			},
		},
		{
			name: "the gateway drops the stream part way",
			stat: statSays("regular file"),
			stream: &fakeExecStream{
				chunks:   []openshell.ExecChunk{stdoutChunk("first half")},
				failWith: &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "relay closed"},
			},
		},
		{
			name: "the stream ends without an exit code",
			stat: statSays("regular file"),
			stream: &fakeExecStream{
				chunks:  []openshell.ExecChunk{stdoutChunk("first half")},
				exitErr: &openshell.StatusError{Code: openshell.ErrorInternal, Message: "stream ended without exit event"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &downloadFixture{stat: tc.stat, stream: tc.stream}
			srv := httptest.NewServer(f.router(FilesHandlerConfig{}))
			defer srv.Close()

			defer waitClosed(t, tc.stream)
			resp, err := srv.Client().Get(srv.URL + downloadPath("/sandbox/x"))
			if err != nil {
				// So little was sent that the connection broke before the
				// status line left the server's buffer. That is a failure the
				// client can see too.
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want the 200 that was already sent", resp.StatusCode)
			}
			body, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				t.Fatalf("the body read to a clean end after %d bytes: a download that failed looks complete", len(body))
			}
		})
	}
}

// The same failures over a recorder: the handler gives up by panicking with
// http.ErrAbortHandler, which is what makes the server drop the connection.
func TestDownloadFailureAfterTheStreamStartedAborts(t *testing.T) {
	f := &downloadFixture{
		stat:   statSays("directory"),
		stream: &fakeExecStream{chunks: []openshell.ExecChunk{stdoutChunk("partial")}, exitCode: 2},
	}
	w := httptest.NewRecorder()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Errorf("handler recovered %v, want it to abort with http.ErrAbortHandler; status %d", got, w.Code)
		}
	}()
	f.router(FilesHandlerConfig{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, downloadPath("/sandbox/project"), nil))
}

// A sandbox that stops sending does not hold the request open for ever.
func TestDownloadStalls(t *testing.T) {
	t.Run("before anything was sent", func(t *testing.T) {
		f := &downloadFixture{stat: statSays("regular file"), stream: &fakeExecStream{block: true}}
		w := httptest.NewRecorder()
		start := time.Now()
		f.router(FilesHandlerConfig{ExecTimeout: 1}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, downloadPath("/sandbox/a.txt"), nil))

		wantErrorResponse(t, w, http.StatusGatewayTimeout, apiutils.FileDownloadFailed)
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("gave up after %v, want about the 1s timeout", took)
		}
		if !f.stream.isClosed() {
			t.Error("the exec stream was left open")
		}
	})

	t.Run("part way", func(t *testing.T) {
		f := &downloadFixture{
			stat:   statSays("regular file"),
			stream: &fakeExecStream{chunks: []openshell.ExecChunk{stdoutChunk("first half")}, block: true},
		}
		srv := httptest.NewServer(f.router(FilesHandlerConfig{ExecTimeout: 1}))
		defer srv.Close()

		resp, err := srv.Client().Get(srv.URL + downloadPath("/sandbox/a.txt"))
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if _, readErr := io.ReadAll(resp.Body); readErr == nil {
			t.Fatal("the body read to a clean end: a download that stalled looks complete")
		}
	})
}

// A transfer is limited by standing still, not by how long it takes: one that
// keeps moving outlives the timeout.
func TestDownloadOutlivesTheTimeoutWhileItMoves(t *testing.T) {
	stream := &fakeExecStream{
		chunks: []openshell.ExecChunk{stdoutChunk("a"), stdoutChunk("b"), stdoutChunk("c"), stdoutChunk("d")},
		every:  400 * time.Millisecond,
	}
	f := &downloadFixture{stat: statSays("regular file"), stream: stream}
	w := httptest.NewRecorder()
	start := time.Now()
	f.router(FilesHandlerConfig{ExecTimeout: 1}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, downloadPath("/sandbox/a.txt"), nil))

	if took := time.Since(start); took < time.Second {
		t.Fatalf("the download took %v, which does not outlast the 1s timeout: the test proves nothing", took)
	}
	if w.Code != http.StatusOK || w.Body.String() != "abcd" {
		t.Errorf("status %d with body %q, want 200 with %q", w.Code, w.Body.String(), "abcd")
	}
}

// slowWriter is a client that takes its time over every write.
type slowWriter struct {
	*httptest.ResponseRecorder
	delay time.Duration
}

func (s *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	return s.ResponseRecorder.Write(p)
}

// Standing still is the sandbox sending nothing. Time the client takes to
// accept a chunk is not that: with chunks 600ms apart and a client that needs
// 600ms for each, neither side is ever idle for the 1s timeout, although
// more than 1s passes between one chunk arriving and the next.
func TestDownloadDoesNotCountASlowClientAsAStall(t *testing.T) {
	stream := &fakeExecStream{
		chunks: []openshell.ExecChunk{stdoutChunk("a"), stdoutChunk("b"), stdoutChunk("c")},
		every:  600 * time.Millisecond,
	}
	f := &downloadFixture{stat: statSays("regular file"), stream: stream}
	w := &slowWriter{ResponseRecorder: httptest.NewRecorder(), delay: 600 * time.Millisecond}

	aborted := func() (aborted bool) {
		defer func() {
			if recover() != nil {
				aborted = true
			}
		}()
		f.router(FilesHandlerConfig{ExecTimeout: 1}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, downloadPath("/sandbox/a.txt"), nil))
		return false
	}()

	if aborted {
		t.Fatalf("the download was aborted after %q: time spent writing to the client was counted as the sandbox standing still", w.Body.String())
	}
	if w.Code != http.StatusOK || w.Body.String() != "abc" {
		t.Errorf("status %d with body %q, want 200 with %q", w.Code, w.Body.String(), "abc")
	}
}

// A client that goes away ends the command in the sandbox.
func TestDownloadClientGoesAway(t *testing.T) {
	// More than the server buffers, so the first bytes reach the client while
	// the command is still running.
	f := &downloadFixture{
		stat:   statSays("directory"),
		stream: &fakeExecStream{chunks: []openshell.ExecChunk{stdoutChunk(strings.Repeat("a", 64<<10))}, block: true},
	}
	srv := httptest.NewServer(f.router(FilesHandlerConfig{}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+downloadPath("/sandbox/project"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 4096)); err != nil {
		t.Fatalf("read the first chunk: %v", err)
	}
	cancel()
	resp.Body.Close()
	waitClosed(t, f.stream)
}

// waitClosed waits for the handler to let go of the exec stream, which it
// does on its way out, a moment after the client sees the response end.
func waitClosed(t *testing.T, stream *fakeExecStream) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !stream.isClosed() {
		if time.Now().After(deadline) {
			t.Fatal("the exec stream is still open 10s after the download ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClassifySandboxError(t *testing.T) {
	tests := []struct {
		name    string
		command string
		stderr  string
		want    sandboxError
	}{
		{name: "nothing printed", command: "stat", stderr: "", want: sandboxErrorOther},
		{name: "only the login shell", command: "stat", stderr: loginShellNoise, want: sandboxErrorOther},
		{name: "missing", command: "cat", stderr: loginShellNoise + "cat: /x: No such file or directory\n", want: sandboxErrorNotFound},
		{name: "forbidden", command: "cat", stderr: "cat: /x: Permission denied\n", want: sandboxErrorPermission},
		{name: "landlock", command: "tar", stderr: "tar: /x: Cannot open: Operation not permitted\n", want: sandboxErrorPermission},
		{name: "carriage returns", command: "stat", stderr: "stat: cannot statx '/x': No such file or directory\r\n", want: sandboxErrorNotFound},
		{name: "another command's message", command: "stat", stderr: "cat: /x: Permission denied\n", want: sandboxErrorOther},
		{name: "the words in the middle of a line", command: "cat", stderr: "cat: /Permission denied: Is a directory\n", want: sandboxErrorOther},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySandboxError(tc.command, tc.stderr); got != tc.want {
				t.Errorf("classifySandboxError(%q, %q) = %d, want %d", tc.command, tc.stderr, got, tc.want)
			}
		})
	}
}

func TestArchiveName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/sandbox/project", want: "project.tar"},
		{path: "/sandbox/project/", want: "project.tar"},
		{path: "/sandbox//project//", want: "project.tar"},
		{path: "/sandbox/.config", want: ".config.tar"},
		{path: "/", want: "sb.tar"},
		{path: "//", want: "sb.tar"},
	}
	for _, tc := range tests {
		if got := archiveName(tc.path, "sb"); got != tc.want {
			t.Errorf("archiveName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
