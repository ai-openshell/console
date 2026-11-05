package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/ai-openshell/console/backend/pkg/services"
)

func TestValidateFilePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "valid absolute path", path: "/sandbox/file.txt", want: true},
		{name: "valid nested path", path: "/home/user/data.json", want: true},
		{name: "empty path", path: "", want: false},
		{name: "relative path", path: "relative/file.txt", want: false},
		{name: "traversal attack", path: "/sandbox/../etc/passwd", want: false},
		{name: "null byte", path: "/sandbox/file\x00.txt", want: false},
		{name: "dot only", path: ".", want: false},
		{name: "double dot", path: "..", want: false},
		{name: "root path", path: "/", want: true},
		{name: "path with spaces", path: "/sandbox/my file.txt", want: true},
		{name: "double slash cleaned", path: "//sandbox//file.txt", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateFilePath(tc.path)
			if got != tc.want {
				t.Errorf("validateFilePath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// mockUploader is a test double for the non-TTY stdin exec (StdinExecer).
type mockUploader struct {
	fn           func(ctx context.Context, workspace, sandboxName string, command []string, stdin []byte) (string, int, error)
	gotWorkspace string
	gotName      string
	gotCmd       []string
	gotStdin     []byte
}

func (m *mockUploader) ExecWithStdin(ctx context.Context, workspace, sandboxName string, command []string, stdin []byte) (string, int, error) {
	m.gotWorkspace = workspace
	m.gotName = sandboxName
	m.gotCmd = command
	m.gotStdin = append([]byte(nil), stdin...)
	if m.fn != nil {
		return m.fn(ctx, workspace, sandboxName, command, stdin)
	}
	return "", 0, nil
}

func uploadRequest(t *testing.T, filename, content string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/workspaces/default/sandboxes/sb/files", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestUploadFile(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.getFn = func(_ context.Context, workspace, name string) (*openshell.Sandbox, error) {
		if workspace != "default" || name != "sb" {
			t.Errorf("Get(%q,%q), want (default,sb)", workspace, name)
		}
		return &openshell.Sandbox{ID: "sb-uuid-123"}, nil
	}
	up := &mockUploader{fn: func(_ context.Context, workspace, sandboxName string, command []string, _ []byte) (string, int, error) {
		if workspace != "default" || sandboxName != "sb" {
			t.Errorf("target = %q/%q, want default/sb", workspace, sandboxName)
		}
		if len(command) != 3 || command[0] != "dd" || command[1] != "of=/sandbox/hello.txt" || command[2] != "bs=4096" {
			t.Errorf("command = %v, want dd of=/sandbox/hello.txt bs=4096", command)
		}
		return "5+0 records in", 0, nil
	}}
	handler := NewFilesHandler(services.NewFileService(up), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), FilesHandlerConfig{})
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/{name}/files", handler.UploadFile)

	// Include a control byte (0x04 = EOT) that a PTY path would corrupt/truncate.
	content := "he\x04llo"
	req := uploadRequest(t, "hello.txt", content)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if string(up.gotStdin) != content {
		t.Errorf("stdin = %q, want %q (raw bytes, no TTY mangling)", up.gotStdin, content)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["path"] != "/sandbox/hello.txt" || resp["success"] != true {
		t.Errorf("resp = %+v", resp)
	}
}

func TestUploadFileSandboxNotFound(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.getFn = func(_ context.Context, _, _ string) (*openshell.Sandbox, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
	}
	handler := NewFilesHandler(services.NewFileService(&mockUploader{}), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), FilesHandlerConfig{})
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/{name}/files", handler.UploadFile)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, uploadRequest(t, "hello.txt", "hi"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadFile(t *testing.T) {
	sdk := &mockSDK{}
	// The handler asks the sandbox what the path is, then streams it.
	sdk.exec.runFn = func(_ context.Context, _, _ string, _ []string, _ ...openshell.ExecOptions) (*openshell.ExecResult, error) {
		return &openshell.ExecResult{ExitCode: 0, Stdout: []byte("regular file\n")}, nil
	}
	sdk.exec.streamFn = func(_ context.Context, _, _ string, command []string, _ ...openshell.ExecOptions) (openshell.ExecStream, error) {
		if len(command) != 2 || command[0] != "cat" || command[1] != "/sandbox/hello.txt" {
			t.Errorf("command = %v", command)
		}
		return &fakeExecStream{chunks: []openshell.ExecChunk{stdoutChunk("hello")}}, nil
	}
	handler := NewFilesHandler(services.NewFileService(&mockUploader{}), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), FilesHandlerConfig{})
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/files", handler.DownloadFile)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/sb/files?path=/sandbox/hello.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "hello" {
		t.Errorf("body = %q, want hello", w.Body.String())
	}
}

func TestDownloadFileNotFound(t *testing.T) {
	sdk := &mockSDK{}
	sdk.exec.runFn = func(_ context.Context, _, _ string, _ []string, _ ...openshell.ExecOptions) (*openshell.ExecResult, error) {
		return &openshell.ExecResult{ExitCode: 1, Stderr: []byte("stat: cannot statx '/sandbox/missing.txt': No such file or directory\n")}, nil
	}
	handler := NewFilesHandler(services.NewFileService(&mockUploader{}), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), FilesHandlerConfig{})
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/files", handler.DownloadFile)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/default/sandboxes/sb/files?path=/sandbox/missing.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestUploadFileFailed(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.getFn = func(_ context.Context, _, _ string) (*openshell.Sandbox, error) {
		return &openshell.Sandbox{ID: "sb-uuid-123"}, nil
	}
	up := &mockUploader{fn: func(_ context.Context, _, _ string, _ []string, _ []byte) (string, int, error) {
		return "dd: write error", 1, nil
	}}
	handler := NewFilesHandler(services.NewFileService(up), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), FilesHandlerConfig{})
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/{name}/files", handler.UploadFile)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, uploadRequest(t, "hello.txt", "hello"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "upload_failed") {
		t.Errorf("body = %s, want upload_failed", w.Body.String())
	}
}

func TestUploadFileExecError(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.getFn = func(_ context.Context, _, _ string) (*openshell.Sandbox, error) {
		return &openshell.Sandbox{ID: "sb-uuid-123"}, nil
	}
	up := &mockUploader{fn: func(_ context.Context, _, _ string, _ []string, _ []byte) (string, int, error) {
		return "", 0, fmt.Errorf("exec unavailable")
	}}
	handler := NewFilesHandler(services.NewFileService(up), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), FilesHandlerConfig{})
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/{name}/files", handler.UploadFile)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, uploadRequest(t, "hello.txt", "hello"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
