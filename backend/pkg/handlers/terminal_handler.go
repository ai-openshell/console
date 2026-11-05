package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/services"
)

const (
	defaultTerminalCols uint32 = 80
	defaultTerminalRows uint32 = 24
	defaultShell               = "/bin/bash"

	// startMessageQuery is the query parameter a client sets to "message" to
	// say that its first frame chooses what the session runs.
	startMessageQuery = "start"
	// The start frame is small and comes straight after the handshake; a
	// client that does not send one in time is not going to.
	startMessageTimeout       = 10 * time.Second
	startMessageMaxBytes      = 64 << 10
	startMessageMaxArguments  = 256
	startMessageMaxEnvEntries = 256
)

type resizeMessage struct {
	Type string `json:"type"`
	Cols uint32 `json:"cols"`
	Rows uint32 `json:"rows"`
}

// startMessage is the first frame of a session opened with ?start=message. It
// carries the options of `openshell sandbox exec`: what to run, where, and
// with which environment. They travel in a frame and not in the URL so that
// environment values stay out of access logs. Without it the session runs the
// default shell.
type startMessage struct {
	Environment  map[string]string `json:"environment,omitempty"`
	Type         string            `json:"type"`
	WorkDir      string            `json:"workdir,omitempty"`
	Command      []string          `json:"command,omitempty"`
	NoLoginShell bool              `json:"noLoginShell,omitempty"`
}

// validate refuses what could not be a command line. Whether the command
// exists, and whether the directory does, is for the sandbox to say.
func (m *startMessage) validate() error {
	if m.Type != "start" {
		return errors.New("the first message must be the start message")
	}
	if len(m.Command) > startMessageMaxArguments {
		return errors.New("the command has too many arguments")
	}
	if len(m.Command) > 0 && m.Command[0] == "" {
		return errors.New("the command is empty")
	}
	for _, arg := range m.Command {
		if strings.ContainsRune(arg, 0) {
			return errors.New("the command contains a NUL byte")
		}
	}
	if strings.ContainsRune(m.WorkDir, 0) {
		return errors.New("the working directory contains a NUL byte")
	}
	if len(m.Environment) > startMessageMaxEnvEntries {
		return errors.New("too many environment variables")
	}
	for key, value := range m.Environment {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return errors.New("an environment variable name is empty or contains '='")
		}
		if strings.ContainsRune(value, 0) {
			return errors.New("an environment variable value contains a NUL byte")
		}
	}
	return nil
}

// readStartMessage reads and checks the start frame.
func readStartMessage(ws *websocket.Conn) (*startMessage, error) {
	ws.SetReadLimit(startMessageMaxBytes)
	_ = ws.SetReadDeadline(time.Now().Add(startMessageTimeout))
	defer func() {
		ws.SetReadLimit(0)
		_ = ws.SetReadDeadline(time.Time{})
	}()

	msgType, data, err := ws.ReadMessage()
	if err != nil {
		return nil, errors.New("no start message was received")
	}
	if msgType != websocket.TextMessage {
		return nil, errors.New("the first message must be the start message")
	}
	var start startMessage
	if json.Unmarshal(data, &start) != nil {
		return nil, errors.New("the start message is not valid JSON")
	}
	if err := start.validate(); err != nil {
		return nil, err
	}
	return &start, nil
}

// sessionFor turns a start message into what Exec().Interactive takes. A nil
// message, or one that names no command, runs the default shell.
func sessionFor(start *startMessage) ([]string, []openshell.ExecOptions) {
	command := []string{defaultShell}
	if start == nil {
		return command, nil
	}
	if len(start.Command) > 0 {
		command = start.Command
	}
	if len(start.Environment) == 0 && start.WorkDir == "" && !start.NoLoginShell {
		return command, nil
	}
	return command, []openshell.ExecOptions{{
		Env:          start.Environment,
		WorkDir:      start.WorkDir,
		NoLoginShell: start.NoLoginShell,
	}}
}

type TerminalHandler struct {
	svc services.ExecServiceInterface
}

func NewTerminalHandler(svc services.ExecServiceInterface) *TerminalHandler {
	return &TerminalHandler{
		svc: svc,
	}
}

func parseDimensions(r *http.Request) (cols, rows uint32) {
	cols = defaultTerminalCols
	rows = defaultTerminalRows
	if c, parseErr := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 32); parseErr == nil {
		cols = uint32(c)
	}
	if ro, parseErr := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 32); parseErr == nil {
		rows = uint32(ro)
	}
	return cols, rows
}

func relaySessionToWS(ws *websocket.Conn, session openshell.InteractiveSession, cancel context.CancelFunc) {
	defer cancel()
	buf := make([]byte, 32*1024)
	for {
		n, err := session.Read(buf)
		if n > 0 {
			_ = ws.WriteMessage(websocket.BinaryMessage, buf[:n])
		}
		if err != nil {
			_ = ws.WriteMessage(websocket.CloseMessage, sessionCloseMessage(session, err))
			return
		}
	}
}

// sessionCloseMessage is the close frame for a session whose output ended.
// A command that ran closes normally with its exit code as the reason. One
// that never produced an exit status did not run to an end: the gateway
// refused it (a sandbox that is not ready, an environment variable name it
// does not take) or the stream broke, and reporting that as "exit code 0"
// would tell the user their command succeeded.
func sessionCloseMessage(session openshell.InteractiveSession, readErr error) []byte {
	if exitCode, exitErr := session.ExitCode(); exitErr == nil {
		return websocket.FormatCloseMessage(websocket.CloseNormalClosure, strconv.Itoa(exitCode))
	}
	return websocket.FormatCloseMessage(websocket.CloseInternalServerErr, sessionFailureReason(readErr))
}

// maxCloseReasonBytes is what a close frame can carry: a control frame holds
// 125 bytes, two of which are the status code.
const maxCloseReasonBytes = 123

// sessionFailureReason says why a session failed, in words that are safe to
// show: the gateway's own message for a refusal the user can act on, and
// nothing of any other failure.
func sessionFailureReason(err error) string {
	reason := "the session ended without an exit status"
	switch {
	case err == nil, errors.Is(err, io.EOF):
	case openshell.IsInvalidArgument(err), openshell.IsNotFound(err), openshell.IsConflict(err), openshell.IsPermissionDenied(err):
		var se *openshell.StatusError
		if errors.As(err, &se) && se.Message != "" {
			reason = se.Message
		}
	case openshell.IsUnavailable(err), openshell.IsDeadlineExceeded(err):
		reason = "OpenShell gateway is unreachable"
	}
	// A reason is cut on a rune boundary; a close frame must be valid UTF-8.
	for len(reason) > maxCloseReasonBytes {
		_, size := utf8.DecodeLastRuneInString(reason)
		reason = reason[:len(reason)-size]
	}
	return reason
}

func (h *TerminalHandler) Terminal(w http.ResponseWriter, r *http.Request) {
	workspace := r.PathValue("workspace")
	name := r.PathValue("name")
	cols, rows := parseDimensions(r)

	upgrader := websocket.Upgrader{
		CheckOrigin: checkWebSocketOrigin,
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}
	defer ws.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var start *startMessage
	if r.URL.Query().Get(startMessageQuery) == "message" {
		if start, err = readStartMessage(ws); err != nil {
			_ = ws.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, err.Error()))
			return
		}
	}
	command, opts := sessionFor(start)

	// The command and its environment are the user's own and are not logged.
	slog.Info("opening interactive session", "workspace", workspace, "name", name, "cols", cols, "rows", rows)
	session, err := h.svc.Interactive(ctx, workspace, name, command, cols, rows, opts...)
	if err != nil {
		slog.Error("interactive session open failed", "error", err)
		reason := "failed to open exec stream"
		if refusal := sessionFailureReason(err); refusal != sessionFailureReason(nil) {
			reason = refusal
		}
		_ = ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, reason))
		return
	}
	defer session.Close()
	slog.Info("interactive session opened, entering relay loop")

	go relaySessionToWS(ws, session, cancel)

	for {
		msgType, data, readErr := ws.ReadMessage()
		if readErr != nil {
			cancel()
			return
		}
		if msgType == websocket.TextMessage {
			var resize resizeMessage
			if json.Unmarshal(data, &resize) == nil && resize.Type == "resize" {
				_ = session.Resize(resize.Cols, resize.Rows)
				continue
			}
		}
		if _, writeErr := session.Write(data); writeErr != nil && writeErr != io.EOF {
			cancel()
			return
		}
	}
}

// checkWebSocketOrigin enforces same-origin on browser WebSocket handshakes,
// as defense-in-depth against cross-site WebSocket hijacking. The BFF is
// same-origin-only by design (ADR 0002): browsers reach it via its own origin
// or through a fronting proxy on that origin — there is no cross-origin
// consumer to allow for.
func checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Browsers always send Origin on a WebSocket handshake; a missing one
		// is a non-browser client that carries no victim's ambient credentials.
		return true
	}
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}
