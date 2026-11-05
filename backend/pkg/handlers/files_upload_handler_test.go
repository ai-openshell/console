package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/services"
)

func TestValidRelativePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "a file name", path: "main.go", want: true},
		{name: "a file in a folder", path: "project/src/main.go", want: true},
		{name: "spaces", path: "My Project/read me.txt", want: true},
		{name: "a hidden file", path: "project/.env", want: true},
		{name: "not ASCII", path: "проект/файл.txt", want: true},
		{name: "dots inside a name", path: "archive.tar.gz", want: true},
		{name: "a name that starts with a dash", path: "-rf/--help", want: true},
		{name: "percent signs are not decoded", path: "%2e%2e/x", want: true},
		{name: "empty", path: "", want: false},
		{name: "absolute", path: "/etc/passwd", want: false},
		{name: "parent", path: "..", want: false},
		{name: "leading parent", path: "../etc/passwd", want: false},
		{name: "parent in the middle", path: "a/../../b", want: false},
		{name: "trailing parent", path: "a/..", want: false},
		{name: "current directory", path: "./a", want: false},
		{name: "current directory in the middle", path: "a/./b", want: false},
		{name: "doubled slash", path: "a//b", want: false},
		{name: "trailing slash", path: "a/", want: false},
		{name: "backslash separators", path: `..\..\etc\passwd`, want: false},
		{name: "a backslash in a name", path: `a\b`, want: false},
		{name: "NUL", path: "a\x00b", want: false},
		{name: "newline", path: "a\nb", want: false},
		{name: "carriage return", path: "a\rb", want: false},
		{name: "escape", path: "a\x1bb", want: false},
		{name: "delete", path: "a\x7fb", want: false},
		{name: "not UTF-8", path: "a\xffb", want: false},
		{name: "a name at the limit", path: strings.Repeat("a", 255), want: true},
		{name: "a name over the limit", path: strings.Repeat("a", 256), want: false},
		{name: "a path over the limit", path: strings.Repeat("abcdefgh/", 512) + "x", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validRelativePath(tc.path); got != tc.want {
				t.Errorf("validRelativePath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// uploadCall is one command the upload ran with stdin.
type uploadCall struct {
	readErr error
	command []string
	stdin   []byte
}

// streamUploader is a test double for the streaming stdin exec. Like dd, it
// reads its stdin to the end, unless the destination is one it fails for, and
// then it exits without reading any of it.
type streamUploader struct {
	// failures maps a destination path to what dd prints when it cannot
	// write there.
	failures map[string]string
	// started, when set, is closed once the first bytes of stdin arrive.
	started chan struct{}
	calls   []uploadCall
	mu      sync.Mutex
}

func (u *streamUploader) ExecWithStdin(ctx context.Context, workspace, sandboxName string, command []string, stdin []byte) (string, int, error) {
	return u.ExecWithStdinStream(ctx, workspace, sandboxName, command, bytes.NewReader(stdin))
}

func (u *streamUploader) ExecWithStdinStream(_ context.Context, _, _ string, command []string, stdin io.Reader) (string, int, error) {
	dest := strings.TrimPrefix(command[1], "of=")
	if output, fails := u.failures[dest]; fails {
		u.record(uploadCall{command: command})
		return output, 1, nil
	}
	var data bytes.Buffer
	buf := make([]byte, 512)
	var readErr error
	for readErr == nil {
		var n int
		n, readErr = stdin.Read(buf)
		data.Write(buf[:n])
		if n > 0 && u.started != nil {
			close(u.started)
			u.started = nil
		}
	}
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	u.record(uploadCall{command: command, stdin: data.Bytes(), readErr: readErr})
	if readErr != nil {
		return "", -1, readErr
	}
	return "0+1 records in\n0+1 records out\n", 0, nil
}

func (u *streamUploader) record(call uploadCall) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, call)
}

// written returns the destination of every dd that ran, in order.
func (u *streamUploader) written() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var paths []string
	for _, call := range u.calls {
		paths = append(paths, strings.TrimPrefix(call.command[1], "of="))
	}
	return paths
}

// uploadFixture is a files handler whose sandbox records the directories it
// was asked to make and the files it was sent.
type uploadFixture struct {
	uploader *streamUploader
	// mkdirFailures maps a directory to what mkdir prints when it refuses.
	mkdirFailures map[string]string
	mkdirs        []string
	gets          int
}

func (f *uploadFixture) router(cfg FilesHandlerConfig) http.Handler {
	if f.uploader == nil {
		f.uploader = &streamUploader{}
	}
	sdk := &mockSDK{}
	sdk.sandboxes.getFn = func(_ context.Context, _, name string) (*openshell.Sandbox, error) {
		f.gets++
		return &openshell.Sandbox{Name: name}, nil
	}
	sdk.exec.runFn = func(_ context.Context, _, _ string, command []string, _ ...openshell.ExecOptions) (*openshell.ExecResult, error) {
		if len(command) != 4 || command[0] != "mkdir" || command[1] != "-p" || command[2] != "--" {
			return nil, errors.New("unexpected command: " + strings.Join(command, " "))
		}
		f.mkdirs = append(f.mkdirs, command[3])
		if output, fails := f.mkdirFailures[command[3]]; fails {
			return &openshell.ExecResult{ExitCode: 1, Stderr: []byte(loginShellNoise + output + "\n")}, nil
		}
		return &openshell.ExecResult{Stderr: []byte(loginShellNoise)}, nil
	}
	handler := NewFilesHandler(services.NewFileService(f.uploader), services.NewExecService(sdk.Exec()), services.NewSandboxService(sdk.Sandboxes(), nil), cfg)
	r := chi.NewRouter()
	r.Post("/workspaces/{workspace}/sandboxes/{name}/files", handler.UploadFile)
	return r
}

// uploadPart is one file of a multipart upload, with the relative path sent
// ahead of it when it has one.
type uploadPart struct {
	relativePath string
	filename     string
	content      string
}

func multipartUpload(t *testing.T, parts ...uploadPart) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for _, p := range parts {
		if p.relativePath != "" {
			if err := mw.WriteField(uploadRelativePathField, p.relativePath); err != nil {
				t.Fatal(err)
			}
		}
		part, err := mw.CreateFormFile(uploadFileField, p.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(p.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return body, mw.FormDataContentType()
}

func uploadTo(dest string) string {
	target := "/workspaces/default/sandboxes/sb/files"
	if dest != "" {
		target += "?dest=" + dest
	}
	return target
}

// serveUpload posts the parts with a declared length, as a browser does.
func serveUpload(t *testing.T, f *uploadFixture, cfg FilesHandlerConfig, dest string, parts ...uploadPart) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartUpload(t, parts...)
	req := httptest.NewRequest(http.MethodPost, uploadTo(dest), body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	f.router(cfg).ServeHTTP(w, req)
	return w
}

// undeclared hides the length of a body, the way a chunked request does.
type undeclared struct{ io.Reader }

// uploadOutcome is the answer to an upload, a success or a failure: both
// carry the files.
type uploadOutcome struct {
	Code    apiutils.ResponseCode `json:"code"`
	Message string                `json:"message"`
	Path    string                `json:"path"`
	Stdout  string                `json:"stdout"`
	Files   []uploadedFile        `json:"files"`
	Size    int64                 `json:"size"`
	Success bool                  `json:"success"`
}

// Where a file that comes with a relative path is written, and which paths
// are refused before anything is run in the sandbox.
func TestUploadRelativePath(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name         string
		dest         string
		relativePath string
		wantStatus   int
		wantCode     apiutils.ResponseCode
		wantPath     string
		wantMkdir    string
	}{
		{name: "below the default destination", relativePath: "project/src/main.go", wantStatus: http.StatusOK, wantPath: "/sandbox/project/src/main.go", wantMkdir: "/sandbox/project/src"},
		{name: "below a chosen destination", dest: "/sandbox/work", relativePath: "a/b.txt", wantStatus: http.StatusOK, wantPath: "/sandbox/work/a/b.txt", wantMkdir: "/sandbox/work/a"},
		{name: "a destination with a trailing slash", dest: "/sandbox/work/", relativePath: "b.txt", wantStatus: http.StatusOK, wantPath: "/sandbox/work/b.txt", wantMkdir: "/sandbox/work"},
		{name: "the root as destination", dest: "/", relativePath: "tmp/b.txt", wantStatus: http.StatusOK, wantPath: "/tmp/b.txt", wantMkdir: "/tmp"},
		{name: "the relative path names the file, not the upload's filename", relativePath: "renamed.txt", wantStatus: http.StatusOK, wantPath: "/sandbox/renamed.txt", wantMkdir: "/sandbox"},
		{name: "percent signs are a name, not an escape", relativePath: "%2e%2e/x", wantStatus: http.StatusOK, wantPath: "/sandbox/%2e%2e/x", wantMkdir: "/sandbox/%2e%2e"},
		{name: "a name that starts with a dash", relativePath: "-rf/--help", wantStatus: http.StatusOK, wantPath: "/sandbox/-rf/--help", wantMkdir: "/sandbox/-rf"},

		{name: "parent directory", relativePath: "../../etc/passwd", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "parent directory in the middle", relativePath: "a/../../../etc/passwd", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "a parent that would stay inside", relativePath: "a/../b.txt", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "absolute", relativePath: "/etc/passwd", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "backslash separators", relativePath: `..\..\etc\passwd`, wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "a current-directory segment", relativePath: "./a.txt", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "an empty segment", relativePath: "a//b.txt", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "a directory, not a file", relativePath: "a/", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "a newline", relativePath: "a\nb.txt", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "two dots inside a name", relativePath: "a..b.txt", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "longer than a path may be", relativePath: strings.Repeat("abcdefgh/", 600) + "x", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "a destination that is not absolute", dest: "sandbox", relativePath: "a.txt", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
		{name: "a destination with a traversal", dest: "/sandbox/../etc", relativePath: "a.txt", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &uploadFixture{}
			w := serveUpload(t, f, FilesHandlerConfig{}, tc.dest, uploadPart{relativePath: tc.relativePath, filename: "upload.bin", content: "payload"})

			if tc.wantStatus != http.StatusOK {
				wantErrorResponse(t, w, tc.wantStatus, tc.wantCode)
				if f.gets != 0 || len(f.mkdirs) != 0 || len(f.uploader.calls) != 0 {
					t.Errorf("a refused path still reached the sandbox: %d lookups, mkdir %q, writes %q", f.gets, f.mkdirs, f.uploader.written())
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			var got uploadOutcome
			decodeInto(t, w, &got)
			if got.Path != tc.wantPath || !got.Success || got.Size != int64(len("payload")) {
				t.Errorf("response = %+v, want path %s, success, size %d", got, tc.wantPath, len("payload"))
			}
			if want := []string{tc.wantMkdir}; !reflect.DeepEqual(f.mkdirs, want) {
				t.Errorf("mkdir -p of %q, want %q", f.mkdirs, want)
			}
			calls := f.uploader.calls
			if len(calls) != 1 {
				t.Fatalf("%d writes, want 1", len(calls))
			}
			// The path is one argument of an argv. Nothing is quoted or
			// joined into a command line here.
			if want := []string{"dd", "of=" + tc.wantPath, "bs=4096"}; !reflect.DeepEqual(calls[0].command, want) {
				t.Errorf("command = %q, want %q", calls[0].command, want)
			}
			if string(calls[0].stdin) != "payload" {
				t.Errorf("stdin = %q, want %q", calls[0].stdin, "payload")
			}
		})
	}
}

// An upload without a relative path keeps the name of the file, and only the
// name: this is the request the Files tab sends for a file that is not in a
// folder.
func TestUploadWithoutRelativePath(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name       string
		filename   string
		wantStatus int
		wantCode   apiutils.ResponseCode
		wantPath   string
	}{
		{name: "a plain name", filename: "notes.txt", wantStatus: http.StatusOK, wantPath: "/sandbox/notes.txt"},
		{name: "a name with a directory keeps its base", filename: "../../etc/passwd", wantStatus: http.StatusOK, wantPath: "/sandbox/passwd"},
		{name: "a name that is a parent directory", filename: "..", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidFileName},
		{name: "two dots inside the name", filename: "a..b", wantStatus: http.StatusBadRequest, wantCode: apiutils.InvalidPath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &uploadFixture{}
			w := serveUpload(t, f, FilesHandlerConfig{}, "", uploadPart{filename: tc.filename, content: "x"})
			if tc.wantStatus != http.StatusOK {
				wantErrorResponse(t, w, tc.wantStatus, tc.wantCode)
				if len(f.uploader.calls) != 0 {
					t.Errorf("a refused name was still written: %q", f.uploader.written())
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if got := f.uploader.written(); !reflect.DeepEqual(got, []string{tc.wantPath}) {
				t.Errorf("written = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

// Several files in one request are written in order, each to its own place,
// and every directory is made once.
func TestUploadSeveralFiles(t *testing.T) {
	f := &uploadFixture{}
	w := serveUpload(t, f, FilesHandlerConfig{}, "/sandbox/work",
		uploadPart{relativePath: "project/README.md", filename: "README.md", content: "readme"},
		uploadPart{relativePath: "project/src/main.go", filename: "main.go", content: "package main"},
		uploadPart{relativePath: "project/src/util.go", filename: "util.go", content: "package main // util"},
		uploadPart{relativePath: "project/LICENSE", filename: "LICENSE", content: ""},
		uploadPart{filename: "loose.bin", content: "\x00\x04\xff"},
	)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got uploadOutcome
	decodeInto(t, w, &got)
	wantFiles := []uploadedFile{
		{Path: "/sandbox/work/project/README.md", Size: 6, Success: true},
		{Path: "/sandbox/work/project/src/main.go", Size: 12, Success: true},
		{Path: "/sandbox/work/project/src/util.go", Size: 20, Success: true},
		{Path: "/sandbox/work/project/LICENSE", Size: 0, Success: true},
		{Path: "/sandbox/work/loose.bin", Size: 3, Success: true},
	}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Errorf("files = %+v\nwant    %+v", got.Files, wantFiles)
	}
	// The fields beside files describe the first file.
	if got.Path != wantFiles[0].Path || got.Size != wantFiles[0].Size || !got.Success || got.Stdout == "" {
		t.Errorf("first-file fields = path %q size %d success %v stdout %q", got.Path, got.Size, got.Success, got.Stdout)
	}
	// /sandbox/work is made on the way to /sandbox/work/project.
	if want := []string{"/sandbox/work/project", "/sandbox/work/project/src"}; !reflect.DeepEqual(f.mkdirs, want) {
		t.Errorf("mkdir -p of %q, want %q: each directory once", f.mkdirs, want)
	}
	if f.gets != 1 {
		t.Errorf("the sandbox was looked up %d times, want once", f.gets)
	}
	calls := f.uploader.calls
	if len(calls) != len(wantFiles) {
		t.Fatalf("%d writes, want %d", len(calls), len(wantFiles))
	}
	if string(calls[4].stdin) != "\x00\x04\xff" {
		t.Errorf("the last file arrived as %q", calls[4].stdin)
	}
}

// A file the sandbox refuses does not stop the files after it, and the answer
// says which one it was and why.
func TestUploadOneOfSeveralFails(t *testing.T) {
	f := &uploadFixture{
		uploader: &streamUploader{failures: map[string]string{
			"/sandbox/b": loginShellNoise + "dd: failed to open '/sandbox/b': Is a directory\n",
		}},
		mkdirFailures: map[string]string{
			"/sandbox/a.txt": "mkdir: cannot create directory '/sandbox/a.txt': Not a directory",
		},
	}
	w := serveUpload(t, f, FilesHandlerConfig{}, "",
		uploadPart{relativePath: "a.txt", filename: "a.txt", content: "first"},
		uploadPart{relativePath: "b", filename: "b", content: strings.Repeat("unread ", 4096)},
		uploadPart{relativePath: "a.txt/nested", filename: "nested", content: "under a file"},
		uploadPart{relativePath: "c.txt", filename: "c.txt", content: "last"},
	)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	var got uploadOutcome
	decodeInto(t, w, &got)
	if got.Code != apiutils.FileUploadFailed || got.Message != "2 of 4 files could not be uploaded" {
		t.Errorf("code %q, message %q", got.Code, got.Message)
	}
	wantFiles := []uploadedFile{
		{Path: "/sandbox/a.txt", Size: 5, Success: true},
		{Path: "/sandbox/b", Error: "Is a directory"},
		{Path: "/sandbox/a.txt/nested", Error: "Not a directory"},
		{Path: "/sandbox/c.txt", Size: 4, Success: true},
	}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Errorf("files = %+v\nwant    %+v", got.Files, wantFiles)
	}
	// Nothing was sent to a directory that could not be made.
	if want := []string{"/sandbox/a.txt", "/sandbox/b", "/sandbox/c.txt"}; !reflect.DeepEqual(f.uploader.written(), want) {
		t.Errorf("written = %q, want %q", f.uploader.written(), want)
	}
}

// One file failing is a 502.
func TestUploadOneFileFails(t *testing.T) {
	f := &uploadFixture{uploader: &streamUploader{failures: map[string]string{
		"/usr/x.txt": "dd: failed to open '/usr/x.txt': Permission denied\n",
	}}}
	w := serveUpload(t, f, FilesHandlerConfig{}, "/usr", uploadPart{filename: "x.txt", content: "x"})

	if got := wantErrorResponse(t, w, http.StatusBadGateway, apiutils.FileUploadFailed); got != "file upload failed" {
		t.Errorf("message = %q, want the one a failed upload has always had", got)
	}
	var got uploadOutcome
	decodeInto(t, w, &got)
	if want := []uploadedFile{{Path: "/usr/x.txt", Error: "Permission denied"}}; !reflect.DeepEqual(got.Files, want) {
		t.Errorf("files = %+v, want %+v", got.Files, want)
	}
}

// The limit is on the request, so on the files of one upload together.
func TestUploadSizeLimitAcrossFiles(t *testing.T) {
	// Room for three files of 400 bytes with their multipart framing, and not
	// for three of 1200.
	const limit = 4096
	file := func(name string, size int) uploadPart {
		return uploadPart{relativePath: name, filename: name, content: strings.Repeat("x", size)}
	}
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name string
		// declared says whether the request states its length, as a browser's
		// does.
		declared   bool
		parts      []uploadPart
		wantStatus int
		// wantWritten are the files that reached the sandbox whole.
		wantWritten []string
		// wantCutOff is the file the limit was reached in.
		wantCutOff string
	}{
		{
			name:        "several files that fit together",
			declared:    true,
			parts:       []uploadPart{file("a", 400), file("b", 400), file("c", 400)},
			wantStatus:  http.StatusOK,
			wantWritten: []string{"/sandbox/a", "/sandbox/b", "/sandbox/c"},
		},
		{
			name:       "each fits and together they do not: refused before anything is written",
			declared:   true,
			parts:      []uploadPart{file("a", 1200), file("b", 1200), file("c", 1200)},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "one file over the limit",
			declared:   true,
			parts:      []uploadPart{file("a", 8192)},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:        "a length that is not declared is found out on the way",
			parts:       []uploadPart{file("a", 1200), file("b", 1200), file("c", 1200)},
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantWritten: []string{"/sandbox/a", "/sandbox/b"},
			wantCutOff:  "/sandbox/c",
		},
		{
			name:        "not declared and within the limit",
			parts:       []uploadPart{file("a", 400), file("b", 400)},
			wantStatus:  http.StatusOK,
			wantWritten: []string{"/sandbox/a", "/sandbox/b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &uploadFixture{}
			body, contentType := multipartUpload(t, tc.parts...)
			var reader io.Reader = body
			if !tc.declared {
				reader = undeclared{body}
			}
			req := httptest.NewRequest(http.MethodPost, uploadTo(""), reader)
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			f.router(FilesHandlerConfig{MaxUploadSize: limit}).ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			var got uploadOutcome
			decodeInto(t, w, &got)
			if tc.wantStatus == http.StatusRequestEntityTooLarge && got.Code != apiutils.FileTooLarge {
				t.Errorf("code = %q, want %q", got.Code, apiutils.FileTooLarge)
			}
			var whole []string
			cutOff := ""
			for _, call := range f.uploader.calls {
				dest := strings.TrimPrefix(call.command[1], "of=")
				if call.readErr != nil {
					cutOff = dest
					continue
				}
				whole = append(whole, dest)
			}
			if !reflect.DeepEqual(whole, tc.wantWritten) {
				t.Errorf("written whole = %q, want %q", whole, tc.wantWritten)
			}
			if cutOff != tc.wantCutOff {
				t.Errorf("cut off in %q, want %q", cutOff, tc.wantCutOff)
			}
			if tc.wantCutOff == "" {
				return
			}
			// The answer accounts for every file the upload got to, and the
			// one it stopped in is not reported as written.
			if len(got.Files) != len(tc.wantWritten)+1 {
				t.Fatalf("files = %+v, want %d written and the one cut off", got.Files, len(tc.wantWritten))
			}
			last := got.Files[len(got.Files)-1]
			if last.Path != tc.wantCutOff || last.Success || last.Error == "" {
				t.Errorf("the file the limit was reached in is reported as %+v", last)
			}
		})
	}
}

// A body that ends before the file does is not a shorter file. The command
// that writes it must see its input fail, not end, and the answer must not
// say the file was uploaded.
func TestUploadCutShort(t *testing.T) {
	body, contentType := multipartUpload(t,
		uploadPart{relativePath: "a.txt", filename: "a.txt", content: "whole"},
		uploadPart{relativePath: "big.bin", filename: "big.bin", content: strings.Repeat("0123456789", 2000)},
	)
	// Drop the second half of the second file and the closing boundary, as a
	// connection that goes away does.
	cut := body.Bytes()[:body.Len()-12000]

	f := &uploadFixture{}
	req := httptest.NewRequest(http.MethodPost, uploadTo(""), undeclared{bytes.NewReader(cut)})
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	f.router(FilesHandlerConfig{}).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	var got uploadOutcome
	decodeInto(t, w, &got)
	if got.Code != apiutils.InvalidUpload {
		t.Errorf("code = %q, want %q", got.Code, apiutils.InvalidUpload)
	}
	if len(got.Files) != 2 || !got.Files[0].Success || got.Files[1].Success || got.Files[1].Path != "/sandbox/big.bin" {
		t.Fatalf("files = %+v, want a.txt written and big.bin not", got.Files)
	}
	calls := f.uploader.calls
	if len(calls) != 2 {
		t.Fatalf("%d writes, want 2", len(calls))
	}
	if calls[1].readErr == nil {
		t.Errorf("the command writing big.bin saw its input end after %d bytes: it would report a truncated file as written", len(calls[1].stdin))
	}
}

// The upload reaches the sandbox while it is still arriving: the BFF does not
// collect the request first.
func TestUploadIsStreamed(t *testing.T) {
	started := make(chan struct{})
	f := &uploadFixture{uploader: &streamUploader{started: started}}
	bodyReader, bodyWriter := io.Pipe()
	mw := multipart.NewWriter(bodyWriter)
	req := httptest.NewRequest(http.MethodPost, uploadTo(""), bodyReader)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	sent := make(chan error, 1)
	go func() {
		part, err := mw.CreateFormFile(uploadFileField, "big.bin")
		if err != nil {
			sent <- err
			return
		}
		if _, err = part.Write(bytes.Repeat([]byte("a"), 64<<10)); err != nil {
			sent <- err
			return
		}
		// The rest is held back until the sandbox has the beginning.
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			_ = bodyWriter.CloseWithError(errors.New("the upload was not passed on while it was arriving"))
			sent <- errors.New("no byte reached the sandbox before the request body ended")
			return
		}
		if _, err = part.Write(bytes.Repeat([]byte("b"), 64<<10)); err != nil {
			sent <- err
			return
		}
		if err = mw.Close(); err != nil {
			sent <- err
			return
		}
		sent <- bodyWriter.Close()
	}()

	w := httptest.NewRecorder()
	f.router(FilesHandlerConfig{}).ServeHTTP(w, req)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := len(f.uploader.calls[0].stdin); got != 128<<10 {
		t.Errorf("the sandbox received %d bytes, want %d", got, 128<<10)
	}
}

func TestUploadWithoutAFile(t *testing.T) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.WriteField(uploadRelativePathField, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	f := &uploadFixture{}
	req := httptest.NewRequest(http.MethodPost, uploadTo(""), body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	f.router(FilesHandlerConfig{}).ServeHTTP(w, req)
	wantErrorResponse(t, w, http.StatusBadRequest, apiutils.MissingFile)
}

// An upload that is refused is still read to its end before it is answered:
// a browser that is cut off while it is sending reports the broken connection
// and not the answer that says what was wrong.
func TestUploadRefusedIsReadToTheEnd(t *testing.T) {
	body, contentType := multipartUpload(t,
		uploadPart{relativePath: "../escape.txt", filename: "escape.txt", content: "x"},
		uploadPart{relativePath: "big.bin", filename: "big.bin", content: strings.Repeat("y", 256<<10)},
	)
	sent := body.Len()
	f := &uploadFixture{}
	req := httptest.NewRequest(http.MethodPost, uploadTo(""), body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	f.router(FilesHandlerConfig{}).ServeHTTP(w, req)

	wantErrorResponse(t, w, http.StatusBadRequest, apiutils.InvalidPath)
	if left := body.Len(); left != 0 {
		t.Errorf("%d of %d bytes of the upload were left unread", left, sent)
	}
	if len(f.uploader.calls) != 0 {
		t.Errorf("files after the refused one were written: %q", f.uploader.written())
	}
}

func TestSandboxErrorReason(t *testing.T) {
	const fallback = "the sandbox could not write the file"
	tests := []struct {
		name    string
		command string
		output  string
		want    string
	}{
		{name: "coreutils dd", command: "dd", output: loginShellNoise + "dd: failed to open '/usr/x': Permission denied\n", want: "Permission denied"},
		{name: "busybox dd", command: "dd", output: "dd: can't open '/usr/x': Read-only file system\n", want: "Read-only file system"},
		{name: "a full disk", command: "dd", output: "dd: error writing '/sandbox/x': No space left on device\n1+0 records in\n", want: "No space left on device"},
		{name: "mkdir", command: "mkdir", output: "mkdir: cannot create directory '/usr/x': Permission denied\n", want: "Permission denied"},
		{name: "only the login shell", command: "dd", output: loginShellNoise, want: fallback},
		{name: "nothing", command: "dd", output: "", want: fallback},
		{name: "a reason too long to be one", command: "dd", output: "dd: " + strings.Repeat("x", 200) + "\n", want: fallback},
		{name: "control characters", command: "dd", output: "dd: failed: \x1b[31mred\n", want: fallback},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxErrorReason(tc.command, tc.output); got != tc.want {
				t.Errorf("sandboxErrorReason(%q, %q) = %q, want %q", tc.command, tc.output, got, tc.want)
			}
		})
	}
}
