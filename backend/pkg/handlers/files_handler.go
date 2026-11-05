package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/services"
)

const defaultUploadDir = "/sandbox"

// The parts of an upload. A "file" part is one file; a "relativePath" part
// before it says where below the destination directory that file goes, for a
// file that comes out of a folder. Without one the file lands in the
// destination directory under its own name.
const (
	uploadFileField         = "file"
	uploadRelativePathField = "relativePath"
)

// Limits on a relative path: PATH_MAX and NAME_MAX on Linux.
const (
	maxRelativePathLength = 4096
	maxPathSegmentLength  = 255
)

// sandboxErrorTail is how much of what a command printed on stderr is kept to
// work out why it failed. The reason is on the last lines.
const sandboxErrorTail = 8 << 10

func validateFilePath(p string) bool {
	if p == "" || strings.Contains(p, "\x00") || strings.Contains(p, "..") {
		return false
	}
	cleaned := filepath.Clean(p)
	return filepath.IsAbs(cleaned)
}

type FilesHandlerConfig struct {
	ExecTimeout   uint32
	MaxUploadSize int64
}

type FilesHandler struct {
	svc           services.FileServiceInterface
	execSvc       services.ExecServiceInterface
	sandboxes     services.SandboxServiceInterface
	execTimeout   uint32
	maxUploadSize int64
}

func NewFilesHandler(
	svc services.FileServiceInterface,
	execSvc services.ExecServiceInterface,
	sandboxSvc services.SandboxServiceInterface,
	cfg FilesHandlerConfig,
) *FilesHandler {
	return &FilesHandler{
		svc:           svc,
		execSvc:       execSvc,
		sandboxes:     sandboxSvc,
		execTimeout:   cfg.ExecTimeout,
		maxUploadSize: cfg.MaxUploadSize,
	}
}

// timeout is how long one command in a sandbox may take, and how long a
// streamed transfer may go without moving a byte.
func (h *FilesHandler) timeout() time.Duration {
	if h.execTimeout == 0 {
		return 30 * time.Second
	}
	return time.Duration(h.execTimeout) * time.Second
}

func (h *FilesHandler) execContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, h.timeout())
}

func (h *FilesHandler) uploadLimit() int64 {
	if h.maxUploadSize == 0 {
		return 64 << 20
	}
	return h.maxUploadSize
}

// transferOptions are the exec options of every command file transfer runs.
// The C locale keeps the messages classifySandboxError reads in English
// whatever locale the sandbox image sets.
func transferOptions() openshell.ExecOptions {
	return openshell.ExecOptions{Env: map[string]string{"LC_ALL": "C"}}
}

func resolveUploadDest(w http.ResponseWriter, destQuery, filename string) (string, bool) {
	if filename == "." || filename == ".." || filename == "/" {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidFileName, "invalid filename")
		return "", false
	}
	dest := destQuery
	if dest == "" {
		dest = defaultUploadDir
	}
	if !validateFilePath(dest) {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPath, "invalid destination directory")
		return "", false
	}
	destPath := filepath.Join(dest, filename)
	if !validateFilePath(destPath) {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPath, "invalid destination path")
		return "", false
	}
	return destPath, true
}

// validRelativePath reports whether rel may be joined onto a destination
// directory: a path a browser gives a file inside a folder the user picked,
// such as "project/src/main.go".
//
// It is strict rather than forgiving. Nothing is cleaned up or reinterpreted:
// a path with a ".." or "." segment, an empty segment (a leading, trailing or
// doubled slash), a backslash, a control character or bytes that are not UTF-8
// is refused, so a path that passes names exactly one place below the
// destination and every segment of it is a plain name.
func validRelativePath(rel string) bool {
	if rel == "" || len(rel) > maxRelativePathLength || !utf8.ValidString(rel) {
		return false
	}
	for _, r := range rel {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" || segment == "." || segment == ".." || len(segment) > maxPathSegmentLength {
			return false
		}
	}
	return true
}

// resolveRelativeUploadDest is resolveUploadDest for a file that carries a
// relative path. The result is checked once more after joining: it has to be
// strictly below the destination directory.
func resolveRelativeUploadDest(w http.ResponseWriter, destQuery, rel string) (string, bool) {
	if !validRelativePath(rel) {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPath,
			"relativePath must be a relative path of plain names, without . or .. segments")
		return "", false
	}
	dest := destQuery
	if dest == "" {
		dest = defaultUploadDir
	}
	if !validateFilePath(dest) {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPath, "invalid destination directory")
		return "", false
	}
	dest = path.Clean(dest)
	destPath := path.Join(dest, rel)
	if !validateFilePath(destPath) || !strings.HasPrefix(destPath, strings.TrimSuffix(dest, "/")+"/") {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPath, "invalid destination path")
		return "", false
	}
	return destPath, true
}

// uploadedFile is what became of one file of an upload.
type uploadedFile struct {
	// Error says why the file was not written, in the sandbox's own words
	// where it gave any.
	Error string `json:"error,omitempty"`
	Path  string `json:"path"`
	// Size is how many bytes were sent to the sandbox. For a file that failed
	// it is not how many were written.
	Size    int64 `json:"size"`
	Success bool  `json:"success"`
}

// uploadResponse answers an upload in which every file was written. The
// fields beside Files describe the first file.
type uploadResponse struct {
	Path     string         `json:"path"`
	Stdout   string         `json:"stdout"`
	Files    []uploadedFile `json:"files"`
	ExitCode int            `json:"exitCode"`
	Size     int64          `json:"size"`
	Success  bool           `json:"success"`
}

// uploadFailure is the error envelope with the outcome of each file the
// upload got to.
type uploadFailure struct {
	Code    apiutils.ResponseCode `json:"code"`
	Message string                `json:"message"`
	Files   []uploadedFile        `json:"files"`
}

// upload is the state of one upload request.
type upload struct {
	h *FilesHandler
	// body is the request body, kept to tell an upload that grew past the
	// limit from one that was cut short.
	body      *recordingBody
	madeDirs  map[string]bool
	workspace string
	sandbox   string
	dest      string
	// firstOutput is what dd printed for the first file.
	firstOutput string
	files       []uploadedFile
	// resolved is set once the sandbox is known to exist.
	resolved bool
}

// UploadFile writes the files of a multipart upload into a sandbox, one at a
// time and in the order they arrive. Each is streamed: the BFF holds one chunk
// of one file, not the upload.
//
// The size limit is on the request, so on the sum of its files. An upload
// that declares more is refused before anything is written. One that turns
// out larger, or is cut short, stops where it is: the files before it stay
// written, and the file it stopped in is left incomplete in the sandbox.
func (h *FilesHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	limit := h.uploadLimit()
	if r.ContentLength > limit {
		apiutils.WriteError(w, http.StatusRequestEntityTooLarge, apiutils.FileTooLarge, uploadTooLargeMessage(limit))
		return
	}
	body := &recordingBody{ReadCloser: http.MaxBytesReader(w, r.Body, limit)}
	r.Body = body
	parts, err := r.MultipartReader()
	if err != nil {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidUpload, "failed to parse multipart form")
		return
	}

	up := &upload{
		h:         h,
		body:      body,
		madeDirs:  map[string]bool{},
		workspace: r.PathValue("workspace"),
		sandbox:   r.PathValue("name"),
		dest:      r.URL.Query().Get("dest"),
	}
	if !up.receive(w, r, parts) {
		// Take the rest of the upload before answering. A client that is
		// still sending when the connection closes on it tends to report the
		// broken connection and not the answer that says what was wrong.
		_, _ = io.Copy(io.Discard, body)
		return
	}
	up.respond(w)
}

// receive reads the upload part by part. It returns false once it has written
// an error response.
func (up *upload) receive(w http.ResponseWriter, r *http.Request, parts *multipart.Reader) bool {
	relativePath := ""
	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			up.failInput(w, err)
			return false
		}
		switch {
		case part.FormName() == uploadRelativePathField:
			value, readErr := io.ReadAll(io.LimitReader(part, maxRelativePathLength+1))
			if readErr != nil {
				up.failInput(w, readErr)
				return false
			}
			relativePath = string(value)
		case part.FormName() == uploadFileField && part.FileName() != "":
			if !up.receiveFile(w, r, part, relativePath) {
				return false
			}
			relativePath = ""
		}
	}
}

// receiveFile writes one file part into the sandbox and records the outcome.
// It returns false once it has written an error response, which it does for
// anything that is not this one file failing in the sandbox.
func (up *upload) receiveFile(w http.ResponseWriter, r *http.Request, part *multipart.Part, relativePath string) bool {
	var destPath string
	var ok bool
	if relativePath != "" {
		destPath, ok = resolveRelativeUploadDest(w, up.dest, relativePath)
	} else {
		destPath, ok = resolveUploadDest(w, up.dest, filepath.Base(part.FileName()))
	}
	if !ok {
		return false
	}

	ctx, cancel := up.h.execContext(r.Context())
	defer cancel()

	// Resolve the sandbox first so raw gRPC not-found errors keep the BFF's
	// normal SDK error mapping.
	if !up.resolved {
		if _, err := up.h.sandboxes.Get(ctx, up.workspace, up.sandbox); err != nil {
			apiutils.WriteSDKError(w, err)
			return false
		}
		up.resolved = true
	}

	file := uploadedFile{Path: destPath}
	reason, err := up.makeDir(ctx, path.Dir(destPath))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return false
	}
	if reason != "" {
		file.Error = reason
		up.files = append(up.files, file)
		return true
	}

	// The SDK exposes no non-TTY stdin exec (Run has no stdin; Interactive
	// forces a PTY that corrupts binary payloads), and its Files().Upload copies
	// a local path over an SSH session, which a streamed request body is not.
	// Stream the bytes into `dd` over the gateway's interactive exec RPC without
	// a TTY via the dedicated raw client, which sends them in chunks: the
	// gateway refuses any single gRPC message over 1 MiB.
	content := &countingReader{r: part}
	output, exitCode, execErr := services.ExecWithStdinStream(ctx, up.h.svc, up.workspace, up.sandbox,
		[]string{"dd", "of=" + destPath, "bs=4096"}, content)
	file.Size = content.n
	if len(up.files) == 0 {
		up.firstOutput = output
	}
	if content.err != nil {
		// The file's own bytes ended early: the request body failed or grew
		// past the limit, or the multipart framing stopped before the file's
		// closing boundary. What the sandbox has of it is not all of it.
		file.Error = "upload was interrupted; the file in the sandbox is incomplete"
		up.files = append(up.files, file)
		up.failInput(w, content.err)
		return false
	}
	if execErr != nil {
		apiutils.WriteSDKError(w, execErr)
		return false
	}
	if exitCode != 0 {
		slog.Error("file upload failed", "path", destPath, "exitCode", exitCode, "stdout", output)
		file.Error = sandboxErrorReason("dd", output)
		up.files = append(up.files, file)
		return true
	}
	file.Success = true
	up.files = append(up.files, file)
	return true
}

// makeDir creates dir and its parents in the sandbox, once per upload, the
// way `openshell sandbox upload` runs `mkdir -p` on its destination. It
// returns the reason when the sandbox refuses.
func (up *upload) makeDir(ctx context.Context, dir string) (reason string, err error) {
	if up.madeDirs[dir] {
		return "", nil
	}
	result, err := up.h.execSvc.Run(ctx, up.workspace, up.sandbox, []string{"mkdir", "-p", "--", dir}, transferOptions())
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		slog.Error("file upload failed", "dir", dir, "exitCode", result.ExitCode, "stderr", string(result.Stderr))
		return sandboxErrorReason("mkdir", string(result.Stderr)), nil
	}
	for d := dir; !up.madeDirs[d]; d = path.Dir(d) {
		up.madeDirs[d] = true
		if d == "/" || d == "." {
			break
		}
	}
	return "", nil
}

// failInput answers an upload whose bytes could not be read to the end.
func (up *upload) failInput(w http.ResponseWriter, err error) {
	// The body's own error says more than what multipart made of it. It is
	// not what decides that a file is incomplete, though: the multipart reader
	// reads ahead, so the body can fail while the file being written is
	// already whole in its buffer.
	if up.body.err != nil {
		err = up.body.err
	}
	status, code, message := http.StatusBadRequest, apiutils.InvalidUpload, "failed to parse multipart form"
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status, code, message = http.StatusRequestEntityTooLarge, apiutils.FileTooLarge, uploadTooLargeMessage(tooLarge.Limit)
	}
	if len(up.files) == 0 {
		apiutils.WriteError(w, status, code, message)
		return
	}
	if code == apiutils.InvalidUpload {
		message = "upload was interrupted"
	}
	apiutils.WriteJSON(w, status, uploadFailure{Code: code, Message: message, Files: up.files})
}

func (up *upload) respond(w http.ResponseWriter) {
	if len(up.files) == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.MissingFile, "file field is required")
		return
	}
	failed := 0
	for _, file := range up.files {
		if !file.Success {
			failed++
		}
	}
	if failed > 0 {
		message := "file upload failed"
		if len(up.files) > 1 {
			message = fmt.Sprintf("%d of %d files could not be uploaded", failed, len(up.files))
		}
		apiutils.WriteJSON(w, http.StatusBadGateway, uploadFailure{Code: apiutils.FileUploadFailed, Message: message, Files: up.files})
		return
	}
	first := up.files[0]
	apiutils.WriteJSON(w, http.StatusOK, uploadResponse{
		ExitCode: 0,
		Path:     first.Path,
		Size:     first.Size,
		Stdout:   up.firstOutput,
		Success:  true,
		Files:    up.files,
	})
}

func uploadTooLargeMessage(limit int64) string {
	return fmt.Sprintf("upload is larger than the limit of %d bytes for one request", limit)
}

// recordingBody remembers the first error reading a request body, other than
// its end. The multipart reader wraps what it passes on, and io.EOF from a
// part only means that part ended.
type recordingBody struct {
	io.ReadCloser
	err error
}

func (b *recordingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && b.err == nil {
		b.err = err
	}
	return n, err
}

// countingReader counts what is read through it and remembers an error other
// than the end.
type countingReader struct {
	r   io.Reader
	err error
	n   int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if err != nil && !errors.Is(err, io.EOF) && c.err == nil {
		c.err = err
	}
	return n, err
}

// pathKind is what a path in a sandbox turned out to be.
type pathKind int

const (
	// pathUnknown is a path the sandbox could not be asked about because it
	// has no stat. It is downloaded as a file.
	pathUnknown pathKind = iota
	pathFile
	pathDirectory
)

// sandboxError is why a command in a sandbox refused a path.
type sandboxError int

const (
	sandboxErrorOther sandboxError = iota
	sandboxErrorNotFound
	sandboxErrorPermission
)

// The exit codes a shell gives a command it could not run: found but not
// executable, and not found.
const (
	exitNotExecutable = 126
	exitNotFound      = 127
)

// commandErrors returns the lines a command printed about itself: those that
// start with its own name, as the messages of coreutils, busybox and tar do.
// A sandbox runs commands through a login shell, and what that shell prints
// (a profile it may not read, for one) is on stderr too and is not about the
// path.
func commandErrors(command, stderr string) []string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, command+": ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// classifySandboxError reads why command failed from what it printed. The
// reason is the text after the last colon of a message, which is the system's
// own description of the error.
func classifySandboxError(command, stderr string) sandboxError {
	for _, line := range commandErrors(command, stderr) {
		switch {
		case strings.HasSuffix(line, ": No such file or directory"), strings.HasSuffix(line, ": Not a directory"):
			return sandboxErrorNotFound
		case strings.HasSuffix(line, ": Permission denied"), strings.HasSuffix(line, ": Operation not permitted"):
			return sandboxErrorPermission
		}
	}
	return sandboxErrorOther
}

// sandboxErrorReason is the short reason a file could not be written, for the
// person who uploaded it: the system's description of the error and nothing
// else of what the sandbox printed.
func sandboxErrorReason(command, output string) string {
	const fallback = "the sandbox could not write the file"
	lines := commandErrors(command, output)
	if len(lines) == 0 {
		return fallback
	}
	line := lines[0]
	reason := line[strings.LastIndex(line, ": ")+2:]
	if reason == "" || len(reason) > 100 {
		return fallback
	}
	for _, r := range reason {
		if r < 0x20 || r == 0x7f {
			return fallback
		}
	}
	return reason
}

// writePathError answers a download whose path the sandbox refused.
func writePathError(w http.ResponseWriter, reason sandboxError) {
	switch reason {
	case sandboxErrorNotFound:
		apiutils.WriteError(w, http.StatusNotFound, apiutils.FileNotFound, "no such file or directory in the sandbox")
	case sandboxErrorPermission:
		apiutils.WriteError(w, http.StatusForbidden, apiutils.PermissionDenied, "the sandbox may not read this path")
	default:
		apiutils.WriteError(w, http.StatusBadGateway, apiutils.FileDownloadFailed, "file download failed")
	}
}

// probePath asks the sandbox what filePath is, following symlinks, so that a
// directory is recognised by what is there and not by how its path is
// written. It returns false once it has written an error response.
func (h *FilesHandler) probePath(w http.ResponseWriter, r *http.Request, workspace, name, filePath string) (pathKind, bool) {
	ctx, cancel := h.execContext(r.Context())
	defer cancel()

	result, err := h.execSvc.Run(ctx, workspace, name, []string{"stat", "-L", "-c", "%F", "--", filePath}, transferOptions())
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return pathUnknown, false
	}
	if result.ExitCode == exitNotFound || result.ExitCode == exitNotExecutable {
		return pathUnknown, true
	}
	if result.ExitCode != 0 {
		slog.Error("file download failed", "path", filePath, "exitCode", result.ExitCode, "stderr", string(result.Stderr))
		writePathError(w, classifySandboxError("stat", string(result.Stderr)))
		return pathUnknown, false
	}
	// %F of coreutils and busybox: "regular file", "regular empty file",
	// "directory", and a name for each kind of special file.
	switch kind := strings.TrimSpace(string(result.Stdout)); {
	case kind == "directory":
		return pathDirectory, true
	case strings.HasPrefix(kind, "regular"):
		return pathFile, true
	default:
		// A device, a FIFO or a socket: reading one never ends, or never
		// starts.
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPath, "path is not a regular file or a directory")
		return pathUnknown, false
	}
}

// DownloadFile streams a file out of a sandbox as it is, and a directory as a
// tar archive of its contents: the archive `openshell sandbox download`
// unpacks, made by the same command.
func (h *FilesHandler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	workspace := r.PathValue("workspace")
	name := r.PathValue("name")
	filePath := r.URL.Query().Get("path")

	if !validateFilePath(filePath) {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPath, "path must be an absolute path without traversal")
		return
	}

	kind, ok := h.probePath(w, r, workspace, name, filePath)
	if !ok {
		return
	}
	if kind == pathDirectory {
		h.streamDownload(w, r, download{
			workspace:   workspace,
			sandbox:     name,
			path:        filePath,
			command:     []string{"tar", "cf", "-", "-C", filePath, "."},
			contentType: "application/x-tar",
			filename:    archiveName(filePath, name),
		})
		return
	}
	h.streamDownload(w, r, download{
		workspace:   workspace,
		sandbox:     name,
		path:        filePath,
		command:     []string{"cat", filePath},
		contentType: "application/octet-stream",
		filename:    filepath.Base(filePath),
	})
}

// archiveName names the archive of a directory after the directory, and after
// the sandbox for the root, which has no name of its own.
func archiveName(dirPath, sandboxName string) string {
	base := path.Base(path.Clean(dirPath))
	if base == "/" || base == "." {
		base = sandboxName
	}
	return base + ".tar"
}

// download is one command whose standard output is the response body.
type download struct {
	workspace string
	sandbox   string
	// path is what was asked for, for the log.
	path        string
	contentType string
	filename    string
	command     []string
}

// streamDownload runs a command in the sandbox and relays its standard output
// as it arrives. The gateway's flow control slows the command down to the
// speed the client reads at, so nothing accumulates here.
//
// The status line is not sent until the command has produced output or has
// finished, so a command that fails before writing anything still gets a
// proper error response. Once the body has started there is no status left to
// change: a failure from then on aborts the connection, which a client sees
// as a transfer that broke, never as a complete one that is short.
func (h *FilesHandler) streamDownload(w http.ResponseWriter, r *http.Request, d download) {
	// A transfer has no time limit of its own, only one on standing still.
	idle := h.timeout()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(idle, func() {
		stalled.Store(true)
		cancel()
	})
	defer watchdog.Stop()

	stream, err := h.execSvc.Stream(ctx, d.workspace, d.sandbox, d.command, transferOptions())
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	defer func() { _ = stream.Close() }()

	out := &downloadWriter{w: w, controller: http.NewResponseController(w), download: d, idle: idle}
	stderr := &tailBuffer{limit: sandboxErrorTail}
	for {
		chunk, nextErr := stream.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			h.failDownload(w, out, d, stalled.Load(), nextErr)
			return
		}
		watchdog.Reset(idle)
		if chunk.Stream != openshell.StreamStdout {
			stderr.Write(chunk.Data)
			continue
		}
		if writeErr := out.Write(chunk.Data); writeErr != nil {
			// The client went away or stopped reading.
			slog.Warn("file download abandoned", "path", d.path, "sentBytes", out.written, "error", writeErr)
			panic(http.ErrAbortHandler)
		}
		// The write is over: the wait for the sandbox's next chunk starts
		// now, not when this one arrived. A slow client has its own deadline.
		watchdog.Reset(idle)
	}

	exitCode, err := stream.ExitCode()
	if err != nil {
		h.failDownload(w, out, d, stalled.Load(), err)
		return
	}
	if exitCode != 0 {
		slog.Error("file download failed", "path", d.path, "command", d.command[0], "exitCode", exitCode,
			"sentBytes", out.written, "stderr", stderr.String())
		if out.started {
			panic(http.ErrAbortHandler)
		}
		writePathError(w, classifySandboxError(d.command[0], stderr.String()))
		return
	}
	out.finish()
}

// failDownload ends a download whose stream failed: with an error response
// while there is still one to give, and by aborting the connection after.
func (h *FilesHandler) failDownload(w http.ResponseWriter, out *downloadWriter, d download, stalled bool, err error) {
	if out.started {
		slog.Error("file download failed mid-stream", "path", d.path, "sentBytes", out.written, "stalled", stalled, "error", err)
		panic(http.ErrAbortHandler)
	}
	if stalled {
		apiutils.WriteError(w, http.StatusGatewayTimeout, apiutils.FileDownloadFailed, "the sandbox sent nothing for too long")
		return
	}
	apiutils.WriteSDKError(w, err)
}

// downloadWriter writes a download's body, sending the headers with the first
// byte.
type downloadWriter struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	download   download
	idle       time.Duration
	written    int64
	started    bool
}

func (d *downloadWriter) start() {
	if d.started {
		return
	}
	d.started = true
	header := d.w.Header()
	// No Content-Length: how much the command will write is not known until
	// it is done.
	header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.download.filename))
	header.Set("Content-Type", d.download.contentType)
	// A browser must not guess another type for bytes out of a sandbox.
	header.Set("X-Content-Type-Options", "nosniff")
	d.w.WriteHeader(http.StatusOK)
}

func (d *downloadWriter) Write(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	d.start()
	// A client that stops reading must not hold the command open for ever.
	// Not every ResponseWriter has deadlines; one without them just blocks.
	_ = d.controller.SetWriteDeadline(time.Now().Add(d.idle))
	n, err := d.w.Write(p) //nolint:gosec // an attachment with a fixed content type, not HTML
	d.written += int64(n)
	return err
}

// finish completes a download that succeeded. An empty file has sent nothing
// yet.
func (d *downloadWriter) finish() {
	d.start()
	// The deadline is on the connection, which the next request may reuse.
	_ = d.controller.SetWriteDeadline(time.Time{})
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	data  []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) {
	t.data = append(t.data, p...)
	if len(t.data) > t.limit {
		t.data = t.data[len(t.data)-t.limit:]
	}
}

func (t *tailBuffer) String() string { return string(t.data) }
