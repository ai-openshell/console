package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

func TestCheckWebSocketOrigin(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name   string
		origin string
		host   string
		want   bool
	}{
		{
			name:   "empty origin allowed (non-browser client)",
			origin: "",
			host:   "dashboard.example.com",
			want:   true,
		},
		{
			name:   "same-origin http match",
			origin: "http://localhost:8080",
			host:   "localhost:8080",
			want:   true,
		},
		{
			name:   "same-origin https match",
			origin: "https://dashboard.example.com",
			host:   "dashboard.example.com",
			want:   true,
		},
		{
			name:   "cross-origin rejected",
			origin: "https://evil.com",
			host:   "dashboard.example.com",
			want:   false,
		},
		{
			name:   "subdomain rejected",
			origin: "https://evil.dashboard.example.com",
			host:   "dashboard.example.com",
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/terminal", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			got := checkWebSocketOrigin(req)
			if got != tc.want {
				t.Errorf("checkWebSocketOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func stubInteractive(t *testing.T, session openshell.InteractiveSession, gotCols, gotRows *uint32) func(context.Context, string, string, []string, uint32, uint32, ...openshell.ExecOptions) (openshell.InteractiveSession, error) {
	t.Helper()
	return func(_ context.Context, workspace, name string, command []string, cols, rows uint32, _ ...openshell.ExecOptions) (openshell.InteractiveSession, error) {
		if workspace != "default" || name != "sb" {
			t.Errorf("sandbox = %s/%s", workspace, name)
		}
		if len(command) != 1 || command[0] != defaultShell {
			t.Errorf("command = %v", command)
		}
		*gotCols, *gotRows = cols, rows
		return session, nil
	}
}

func waitSessionIO(session *mockInteractiveSession, timeout time.Duration) (string, [][2]uint32) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		ok := string(session.written) == "ls\n" && len(session.resizes) == 1
		session.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return string(session.written), append([][2]uint32(nil), session.resizes...)
}

func TestTerminalRelay(t *testing.T) {
	session := &mockInteractiveSession{reads: make(chan []byte, 1)}
	session.reads <- []byte("hi")

	var gotCols, gotRows uint32
	sdk := &mockSDK{}
	sdk.exec.interactiveFn = stubInteractive(t, session, &gotCols, &gotRows)

	handler := NewTerminalHandler(sdk.Exec())
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/terminal", handler.Terminal)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/workspaces/default/sandboxes/sb/terminal?cols=100&rows=30"
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.BinaryMessage || string(data) != "hi" {
		t.Fatalf("got type=%d data=%q, want binary hi", msgType, data)
	}

	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatal(err)
	}

	written, resizes := waitSessionIO(session, 2*time.Second)
	if written != "ls\n" {
		t.Errorf("written = %q, want ls\\n", written)
	}
	if len(resizes) != 1 || resizes[0] != [2]uint32{120, 40} {
		t.Errorf("resizes = %v, want [120 40]", resizes)
	}
	if gotCols != 100 || gotRows != 30 {
		t.Errorf("dims = %d x %d, want 100x30", gotCols, gotRows)
	}
}

func TestStartMessageValidate(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name    string
		message startMessage
		wantErr string
	}{
		{name: "start with nothing else", message: startMessage{Type: "start"}},
		{
			name: "every option",
			message: startMessage{
				Type:         "start",
				Command:      []string{"/bin/sh", "-c", "echo hi", ""},
				WorkDir:      "/sandbox/project",
				Environment:  map[string]string{"FOO": "a=b", "EMPTY": ""},
				NoLoginShell: true,
			},
		},
		{name: "another message type", message: startMessage{Type: "resize"}, wantErr: "must be the start message"},
		{name: "no type", message: startMessage{}, wantErr: "must be the start message"},
		{name: "empty program", message: startMessage{Type: "start", Command: []string{"", "-l"}}, wantErr: "command is empty"},
		{name: "NUL in an argument", message: startMessage{Type: "start", Command: []string{"sh", "a\x00b"}}, wantErr: "NUL"},
		{name: "too many arguments", message: startMessage{Type: "start", Command: make([]string, startMessageMaxArguments+1)}, wantErr: "too many arguments"},
		{name: "NUL in the working directory", message: startMessage{Type: "start", WorkDir: "/a\x00"}, wantErr: "working directory"},
		{name: "empty variable name", message: startMessage{Type: "start", Environment: map[string]string{"": "x"}}, wantErr: "environment variable name"},
		{name: "equals sign in a variable name", message: startMessage{Type: "start", Environment: map[string]string{"A=B": "x"}}, wantErr: "environment variable name"},
		{name: "NUL in a variable value", message: startMessage{Type: "start", Environment: map[string]string{"A": "x\x00"}}, wantErr: "environment variable value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.message.validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("validate() = %v, want no error", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("validate() = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestSessionFor(t *testing.T) {
	command, opts := sessionFor(nil)
	if len(command) != 1 || command[0] != defaultShell || opts != nil {
		t.Errorf("no start message: command %v, options %v; want the default shell and no options", command, opts)
	}

	command, opts = sessionFor(&startMessage{Type: "start"})
	if len(command) != 1 || command[0] != defaultShell || opts != nil {
		t.Errorf("empty start message: command %v, options %v; want the default shell and no options", command, opts)
	}

	command, opts = sessionFor(&startMessage{
		Type:         "start",
		Command:      []string{"/bin/sh", "-l"},
		WorkDir:      "/work",
		Environment:  map[string]string{"A": "1"},
		NoLoginShell: true,
	})
	if strings.Join(command, " ") != "/bin/sh -l" {
		t.Errorf("command = %v, want [/bin/sh -l]", command)
	}
	if len(opts) != 1 || opts[0].WorkDir != "/work" || opts[0].Env["A"] != "1" || !opts[0].NoLoginShell {
		t.Errorf("options = %+v, want the working directory, the environment and no login shell", opts)
	}

	// One option alone is still passed on.
	if _, opts = sessionFor(&startMessage{Type: "start", NoLoginShell: true}); len(opts) != 1 || !opts[0].NoLoginShell {
		t.Errorf("options = %+v, want no login shell", opts)
	}
}

// dialTerminal serves the handler and opens a socket on it.
func dialTerminal(t *testing.T, sdk *mockSDK, query string) *websocket.Conn {
	t.Helper()
	handler := NewTerminalHandler(sdk.Exec())
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/terminal", handler.Terminal)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/workspaces/default/sandboxes/sb/terminal" + query
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func TestTerminalStartMessage(t *testing.T) {
	session := &mockInteractiveSession{reads: make(chan []byte, 1)}
	session.reads <- []byte("ready")

	type call struct {
		command []string
		opts    []openshell.ExecOptions
	}
	calls := make(chan call, 1)
	sdk := &mockSDK{}
	sdk.exec.interactiveFn = func(_ context.Context, _, _ string, command []string, _, _ uint32, opts ...openshell.ExecOptions) (openshell.InteractiveSession, error) {
		calls <- call{command: command, opts: opts}
		return session, nil
	}

	ws := dialTerminal(t, sdk, "?cols=100&rows=30&start=message")
	start := `{"type":"start","command":["/bin/sh","-c","pwd"],"workdir":"/sandbox","environment":{"MODE":"ci"},"noLoginShell":true}`
	if err := ws.WriteMessage(websocket.TextMessage, []byte(start)); err != nil {
		t.Fatal(err)
	}

	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, data, err := ws.ReadMessage(); err != nil || string(data) != "ready" {
		t.Fatalf("read = %q, %v; want the session's output", data, err)
	}

	select {
	case got := <-calls:
		if strings.Join(got.command, "|") != "/bin/sh|-c|pwd" {
			t.Errorf("command = %v, want [/bin/sh -c pwd] as three arguments", got.command)
		}
		if len(got.opts) != 1 || got.opts[0].WorkDir != "/sandbox" || got.opts[0].Env["MODE"] != "ci" || !got.opts[0].NoLoginShell {
			t.Errorf("options = %+v, want workdir /sandbox, MODE=ci and no login shell", got.opts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the session was never opened")
	}
}

func TestTerminalRefusesABadStartMessage(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name       string
		frameType  int
		frame      string
		wantReason string
	}{
		{name: "not JSON", frameType: websocket.TextMessage, frame: "ls\n", wantReason: "not valid JSON"},
		{name: "keystrokes before the start message", frameType: websocket.BinaryMessage, frame: "ls\n", wantReason: "must be the start message"},
		{name: "a resize first", frameType: websocket.TextMessage, frame: `{"type":"resize","cols":1,"rows":1}`, wantReason: "must be the start message"},
		{name: "an empty program", frameType: websocket.TextMessage, frame: `{"type":"start","command":[""]}`, wantReason: "command is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.exec.interactiveFn = func(context.Context, string, string, []string, uint32, uint32, ...openshell.ExecOptions) (openshell.InteractiveSession, error) {
				t.Error("a session was opened for a refused start message")
				return nil, context.Canceled
			}

			ws := dialTerminal(t, sdk, "?start=message")
			if err := ws.WriteMessage(tc.frameType, []byte(tc.frame)); err != nil {
				t.Fatal(err)
			}

			_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _, err := ws.ReadMessage()
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) {
				t.Fatalf("read = %v, want the socket closed with a reason", err)
			}
			if closeErr.Code != websocket.ClosePolicyViolation || !strings.Contains(closeErr.Text, tc.wantReason) {
				t.Errorf("closed with %d %q, want %d and a reason containing %q", closeErr.Code, closeErr.Text, websocket.ClosePolicyViolation, tc.wantReason)
			}
		})
	}
}

// refusedSession is a session the gateway ended before any command ran: its
// read fails and it has no exit status.
type refusedSession struct {
	err error
}

func (s *refusedSession) Read([]byte) (int, error)    { return 0, s.err }
func (s *refusedSession) Write(p []byte) (int, error) { return len(p), nil }
func (s *refusedSession) Resize(uint32, uint32) error { return nil }
func (s *refusedSession) ExitCode() (int, error)      { return 0, errors.New("no exit status received") }
func (s *refusedSession) Close() error                { return nil }

func TestSessionFailureReason(t *testing.T) {
	long := strings.Repeat("é", 200)
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name string
		err  error
		want string
	}{
		{name: "no error", err: nil, want: "the session ended without an exit status"},
		{name: "end of stream", err: io.EOF, want: "the session ended without an exit status"},
		{
			name: "a refusal the user can act on carries the gateway's words",
			err:  &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: "environment variable name 'MY-VAR' is invalid"},
			want: "environment variable name 'MY-VAR' is invalid",
		},
		{
			name: "a sandbox that is not ready",
			err:  &openshell.StatusError{Code: openshell.ErrorConflict, Message: "sandbox is not ready"},
			want: "sandbox is not ready",
		},
		{
			name: "an unreachable gateway is named, not quoted",
			err:  &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "dial tcp 10.0.0.7:8080: connect: connection refused"},
			want: "OpenShell gateway is unreachable",
		},
		{
			name: "an internal failure says nothing of itself",
			err:  &openshell.StatusError{Code: openshell.ErrorInternal, Message: "panic in handler at server.rs:120"},
			want: "the session ended without an exit status",
		},
		{
			name: "a long message is cut to what a close frame holds, on a rune boundary",
			err:  &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: long},
			want: strings.Repeat("é", maxCloseReasonBytes/2),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionFailureReason(tc.err)
			if got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
			if len(got) > maxCloseReasonBytes || !utf8.ValidString(got) {
				t.Errorf("reason is %d bytes (valid UTF-8: %v), want at most %d and valid", len(got), utf8.ValidString(got), maxCloseReasonBytes)
			}
		})
	}
}

// A session the gateway refuses is not a command that exited with code 0:
// the socket closes with an error and the gateway's reason, which is what the
// frontend shows.
func TestTerminalReportsARefusedSession(t *testing.T) {
	refusal := &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: "environment variable name 'MY-VAR' is invalid"}
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name string
		open func() (openshell.InteractiveSession, error)
	}{
		{
			name: "refused when the stream is read",
			open: func() (openshell.InteractiveSession, error) { return &refusedSession{err: refusal}, nil },
		},
		{
			name: "refused when the stream is opened",
			open: func() (openshell.InteractiveSession, error) { return nil, refusal },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.exec.interactiveFn = func(context.Context, string, string, []string, uint32, uint32, ...openshell.ExecOptions) (openshell.InteractiveSession, error) {
				return tc.open()
			}
			ws := dialTerminal(t, sdk, "")

			_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _, err := ws.ReadMessage()
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) {
				t.Fatalf("read = %v, want the socket closed with a reason", err)
			}
			if closeErr.Code != websocket.CloseInternalServerErr || closeErr.Text != refusal.Message {
				t.Errorf("closed with %d %q, want %d and the gateway's reason %q", closeErr.Code, closeErr.Text, websocket.CloseInternalServerErr, refusal.Message)
			}
		})
	}
}

// A command that ran still closes normally with its exit code.
func TestTerminalReportsTheExitCode(t *testing.T) {
	session := &mockInteractiveSession{exit: 3}
	sdk := &mockSDK{}
	sdk.exec.interactiveFn = func(context.Context, string, string, []string, uint32, uint32, ...openshell.ExecOptions) (openshell.InteractiveSession, error) {
		return session, nil
	}
	ws := dialTerminal(t, sdk, "")

	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := ws.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("read = %v, want the socket closed", err)
	}
	if closeErr.Code != websocket.CloseNormalClosure || closeErr.Text != "3" {
		t.Errorf("closed with %d %q, want %d and the exit code %q", closeErr.Code, closeErr.Text, websocket.CloseNormalClosure, "3")
	}
}
