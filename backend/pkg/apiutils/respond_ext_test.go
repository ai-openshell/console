package apiutils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDecodeBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
		wantOK  bool
	}{
		{name: "valid JSON", body: `{"name":"ok"}`, wantOK: true, wantErr: ""},
		{name: "invalid JSON", body: `{bad`, wantOK: false, wantErr: "invalid_body"},
		{name: "unknown field", body: `{"name":"ok","bogus":1}`, wantOK: false, wantErr: "invalid_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var dst struct {
				Name string `json:"name"`
			}
			ok := DecodeBody(w, r, &dst)
			if ok != tc.wantOK {
				t.Errorf("DecodeBody() = %v, want %v", ok, tc.wantOK)
			}
			if !ok && tc.wantErr != "" {
				var errResp ErrorResponse
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if errResp.Code.String() != tc.wantErr {
					t.Errorf("code = %q, want %q", errResp.Code, tc.wantErr)
				}
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusCreated, map[string]string{"key": "value"})

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["key"] != "value" {
		t.Errorf("body = %v, want {key:value}", body)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusBadRequest, "test_code", "test message")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var body ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "test_code" {
		t.Errorf("code = %q, want test_code", body.Code)
	}
	if body.Message != "test message" {
		t.Errorf("message = %q, want test message", body.Message)
	}
}

func TestWriteSDKError(t *testing.T) {
	tests := []struct {
		err         error
		name        string
		wantCode    string
		wantMessage string
		wantHTTP    int
	}{
		{
			name:        "NotFound",
			err:         &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"},
			wantHTTP:    http.StatusNotFound,
			wantCode:    "not_found",
			wantMessage: "sandbox not found",
		},
		{
			name:        "AlreadyExists",
			err:         &openshell.StatusError{Code: openshell.ErrorAlreadyExists, Message: "already exists"},
			wantHTTP:    http.StatusConflict,
			wantCode:    "already_exists",
			wantMessage: "already exists",
		},
		{
			name:        "InvalidArgument",
			err:         &openshell.StatusError{Code: openshell.ErrorInvalidArgument, Message: "bad input"},
			wantHTTP:    http.StatusBadRequest,
			wantCode:    "invalid_argument",
			wantMessage: "bad input",
		},
		{
			name:        "PermissionDenied",
			err:         &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "denied"},
			wantHTTP:    http.StatusForbidden,
			wantCode:    "permission_denied",
			wantMessage: "denied",
		},
		{
			name:        "Unauthenticated",
			err:         &openshell.StatusError{Code: openshell.ErrorUnauthenticated, Message: "no token"},
			wantHTTP:    http.StatusUnauthorized,
			wantCode:    "unauthenticated",
			wantMessage: "no token",
		},
		{
			name:        "Unavailable uses generic message",
			err:         &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "connection refused"},
			wantHTTP:    http.StatusBadGateway,
			wantCode:    "gateway_unavailable",
			wantMessage: "OpenShell gateway is unreachable",
		},
		{
			name:        "Conflict",
			err:         &openshell.StatusError{Code: openshell.ErrorConflict, Message: "version mismatch"},
			wantHTTP:    http.StatusConflict,
			wantCode:    "conflict",
			wantMessage: "version mismatch",
		},
		{
			name:        "fallback FailedPrecondition via raw gRPC",
			err:         status.Error(codes.FailedPrecondition, "sandbox not ready"),
			wantHTTP:    http.StatusBadRequest,
			wantCode:    "invalid_argument",
			wantMessage: "sandbox not ready",
		},
		{
			name:        "fallback ResourceExhausted via raw gRPC",
			err:         status.Error(codes.ResourceExhausted, "rate limited"),
			wantHTTP:    http.StatusTooManyRequests,
			wantCode:    "resource_exhausted",
			wantMessage: "rate limited",
		},
		// The raw clients in pkg/clients return gRPC status errors that never
		// passed through the SDK. Each gets the response the SDK's typed error
		// for the same status gets.
		{
			name:        "raw gRPC NotFound",
			err:         status.Error(codes.NotFound, "provider not found"),
			wantHTTP:    http.StatusNotFound,
			wantCode:    "not_found",
			wantMessage: "provider not found",
		},
		{
			name:        "raw gRPC AlreadyExists",
			err:         status.Error(codes.AlreadyExists, "already exists"),
			wantHTTP:    http.StatusConflict,
			wantCode:    "already_exists",
			wantMessage: "already exists",
		},
		{
			name:        "raw gRPC InvalidArgument",
			err:         status.Error(codes.InvalidArgument, "bad input"),
			wantHTTP:    http.StatusBadRequest,
			wantCode:    "invalid_argument",
			wantMessage: "bad input",
		},
		{
			name:        "raw gRPC OutOfRange",
			err:         status.Error(codes.OutOfRange, "decoded message length too large"),
			wantHTTP:    http.StatusBadRequest,
			wantCode:    "invalid_argument",
			wantMessage: "decoded message length too large",
		},
		{
			name:        "raw gRPC PermissionDenied",
			err:         status.Error(codes.PermissionDenied, "denied"),
			wantHTTP:    http.StatusForbidden,
			wantCode:    "permission_denied",
			wantMessage: "denied",
		},
		{
			name:        "raw gRPC Unauthenticated",
			err:         status.Error(codes.Unauthenticated, "no token"),
			wantHTTP:    http.StatusUnauthorized,
			wantCode:    "unauthenticated",
			wantMessage: "no token",
		},
		{
			name:        "raw gRPC Aborted",
			err:         status.Error(codes.Aborted, "version mismatch"),
			wantHTTP:    http.StatusConflict,
			wantCode:    "conflict",
			wantMessage: "version mismatch",
		},
		{
			name:        "raw gRPC Unavailable uses generic message",
			err:         status.Error(codes.Unavailable, "connection refused"),
			wantHTTP:    http.StatusBadGateway,
			wantCode:    "gateway_unavailable",
			wantMessage: "OpenShell gateway is unreachable",
		},
		{
			name:        "raw gRPC DeadlineExceeded uses generic message",
			err:         status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
			wantHTTP:    http.StatusBadGateway,
			wantCode:    "gateway_unavailable",
			wantMessage: "OpenShell gateway is unreachable",
		},
		{
			name:        "raw gRPC status the table does not know returns 500",
			err:         status.Error(codes.DataLoss, "corrupt"),
			wantHTTP:    http.StatusInternalServerError,
			wantCode:    "internal",
			wantMessage: "internal error",
		},
		{
			name:        "unknown error returns 500",
			err:         fmt.Errorf("something unexpected"),
			wantHTTP:    http.StatusInternalServerError,
			wantCode:    "internal",
			wantMessage: "internal error",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteSDKError(w, tc.err)

			if w.Code != tc.wantHTTP {
				t.Errorf("HTTP status = %d, want %d", w.Code, tc.wantHTTP)
			}
			var body ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Code.String() != tc.wantCode {
				t.Errorf("error code = %q, want %q", body.Code, tc.wantCode)
			}
			if body.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Message, tc.wantMessage)
			}
		})
	}
}
