// Package apiutils holds the shared HTTP helpers (JSON/error responses,
// body decoding, name validation) used by every BFF handler.
package apiutils

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ResponseCode is a stable JSON error code returned by the BFF.
type ResponseCode string

const (
	NotReady           ResponseCode = "not_ready"
	Internal           ResponseCode = "internal"
	NotFound           ResponseCode = "not_found"
	AlreadyExists      ResponseCode = "already_exists"
	InvalidArgument    ResponseCode = "invalid_argument"
	PermissionDenied   ResponseCode = "permission_denied"
	Unauthenticated    ResponseCode = "unauthenticated"
	Conflict           ResponseCode = "conflict"
	GatewayUnavailable ResponseCode = "gateway_unavailable"
	ResourceExhausted  ResponseCode = "resource_exhausted"
	InvalidBody        ResponseCode = "invalid_body"
	InvalidRoute       ResponseCode = "invalid_route"
	InvalidFileName    ResponseCode = "invalid_filename"
	InvalidName        ResponseCode = "invalid_name"
	InvalidImage       ResponseCode = "invalid_image"
	InvalidPath        ResponseCode = "invalid_path"
	InvalidUpload      ResponseCode = "invalid_upload"
	InvalidProvider    ResponseCode = "invalid_provider"
	InvalidRequest     ResponseCode = "invalid_request"
	InvalidService     ResponseCode = "invalid_service"
	InvalidPort        ResponseCode = "invalid_port"
	InvalidSetting     ResponseCode = "invalid_setting"
	InvalidTemplate    ResponseCode = "invalid_template"
	InvalidSubject     ResponseCode = "invalid_subject"
	InvalidRole        ResponseCode = "invalid_role"
	InvalidPolicy      ResponseCode = "invalid_policy"
	InvalidRule        ResponseCode = "invalid_rule"
	InvalidStrategy    ResponseCode = "invalid_strategy"
	InvalidProfile     ResponseCode = "invalid_profile"
	MissingFile        ResponseCode = "missing_file"
	FileReadError      ResponseCode = "read_error"
	FileUploadFailed   ResponseCode = "upload_failed"
	FileTooLarge       ResponseCode = "upload_too_large"
	FileNotFound       ResponseCode = "file_not_found"
	FileDownloadFailed ResponseCode = "download_failed"
	IDMismatch         ResponseCode = "id_mismatch"
)

func (rc ResponseCode) String() string {
	return string(rc)
}

// ErrorResponse is the standard error envelope.
type ErrorResponse struct {
	Code    ResponseCode `json:"code"`
	Message string       `json:"message"`
}

func WriteJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode response", "error", err)
	}
}

func WriteError(w http.ResponseWriter, statusCode int, code ResponseCode, message string) {
	WriteJSON(w, statusCode, ErrorResponse{Code: code, Message: message})
}

// WriteSDKError maps an SDK StatusError onto a safe HTTP error response.
// Uses the SDK's typed error helpers for classification and extracts the
// clean message from StatusError.Message (no error chain prefix).
func WriteSDKError(w http.ResponseWriter, err error) {
	var se *openshell.StatusError
	var msg string
	if errors.As(err, &se) {
		msg = se.Message
	} else {
		msg = err.Error()
	}

	switch {
	case openshell.IsNotFound(err):
		slog.Warn("gateway error", "code", "NotFound", "message", msg)
		WriteError(w, http.StatusNotFound, NotFound, msg)
	case openshell.IsAlreadyExists(err):
		slog.Warn("gateway error", "code", "AlreadyExists", "message", msg)
		WriteError(w, http.StatusConflict, AlreadyExists, msg)
	case openshell.IsInvalidArgument(err):
		slog.Warn("gateway error", "code", "InvalidArgument", "message", msg)
		WriteError(w, http.StatusBadRequest, InvalidArgument, msg)
	case openshell.IsPermissionDenied(err):
		slog.Warn("gateway error", "code", "PermissionDenied", "message", msg)
		WriteError(w, http.StatusForbidden, PermissionDenied, msg)
	case openshell.IsUnauthenticated(err):
		slog.Warn("gateway error", "code", "Unauthenticated", "message", msg)
		WriteError(w, http.StatusUnauthorized, Unauthenticated, msg)
	case openshell.IsConflict(err):
		slog.Warn("gateway error", "code", "Conflict", "message", msg)
		WriteError(w, http.StatusConflict, Conflict, msg)
	case openshell.IsUnavailable(err) || openshell.IsDeadlineExceeded(err):
		slog.Warn("gateway error", "code", "Unavailable", "message", msg)
		WriteError(w, http.StatusBadGateway, GatewayUnavailable, "OpenShell gateway is unreachable")
	default:
		if writeRawStatusError(w, err) {
			return
		}
		slog.Error("gateway call failed", "error", err)
		WriteError(w, http.StatusInternalServerError, Internal, "internal error")
	}
}

// rawStatusResponse is the response for one gRPC status code.
type rawStatusResponse struct {
	code ResponseCode
	// message replaces the gateway's own message when set.
	message string
	status  int
}

// rawStatusResponses maps the gRPC status of an error that did not come
// through the SDK to an HTTP response. The raw clients in pkg/clients return
// such errors, and so do the statuses the SDK's typed helpers do not cover.
// A status gets the response the SDK's typed error for it gets above, so a
// call answers the same whichever client made it, with two exceptions: the
// SDK reads FailedPrecondition as a conflict and ResourceExhausted as an
// unavailable gateway, and here they are a bad request and a rate limit.
var rawStatusResponses = map[codes.Code]rawStatusResponse{
	codes.NotFound:           {status: http.StatusNotFound, code: NotFound},
	codes.AlreadyExists:      {status: http.StatusConflict, code: AlreadyExists},
	codes.InvalidArgument:    {status: http.StatusBadRequest, code: InvalidArgument},
	codes.FailedPrecondition: {status: http.StatusBadRequest, code: InvalidArgument},
	codes.OutOfRange:         {status: http.StatusBadRequest, code: InvalidArgument},
	codes.PermissionDenied:   {status: http.StatusForbidden, code: PermissionDenied},
	codes.Unauthenticated:    {status: http.StatusUnauthorized, code: Unauthenticated},
	codes.Aborted:            {status: http.StatusConflict, code: Conflict},
	codes.ResourceExhausted:  {status: http.StatusTooManyRequests, code: ResourceExhausted},
	codes.Unavailable:        {status: http.StatusBadGateway, code: GatewayUnavailable, message: "OpenShell gateway is unreachable"},
	codes.DeadlineExceeded:   {status: http.StatusBadGateway, code: GatewayUnavailable, message: "OpenShell gateway is unreachable"},
}

// writeRawStatusError answers for an error that carries a gRPC status the
// table knows, and reports whether it did.
func writeRawStatusError(w http.ResponseWriter, err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	response, known := rawStatusResponses[st.Code()]
	if !known {
		return false
	}
	slog.Warn("gateway error", "code", st.Code().String(), "message", st.Message())
	message := response.message
	if message == "" {
		message = st.Message()
	}
	WriteError(w, response.status, response.code, message)
	return true
}

const maxJSONBodyBytes int64 = 1 << 20 // 1 MB

func DecodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		slog.Debug("request body decode failed", "error", err)
		WriteError(w, http.StatusBadRequest, InvalidBody, "invalid request body")
		return false
	}
	return true
}

var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

const maxDNS1123LabelLength = 63

// ValidDNS1123 reports whether name is a valid DNS-1123 label (workspace and
// sandbox names).
func ValidDNS1123(name string) bool {
	return len(name) <= maxDNS1123LabelLength && dns1123Label.MatchString(name)
}
