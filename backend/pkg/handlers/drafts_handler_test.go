package handlers

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	sdktypes "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// draftRoutes mounts the draft routes on the paths app.go gives them.
func draftRoutes(sdk *mockSDK) http.Handler {
	handler := NewDraftsHandler(services.NewPolicyService(sdk.Policy()))
	r := chi.NewRouter()
	r.Get("/workspaces/{workspace}/sandboxes/{name}/drafts", handler.GetDraftPolicy)
	r.Post("/workspaces/{workspace}/sandboxes/{name}/drafts/approve-all", handler.ApproveAllDraftChunks)
	return r
}

func draftStatusFilter(opts []openshell.GetDraftOption) string {
	cfg := sdktypes.ApplyGetDraftOptions(opts)
	return (&cfg).StatusFilter()
}

// A bulk approval is bound to review tokens: the gateway skips, as stale, any
// chunk that has a token and is approved without it. So the approvals have to
// reach the gateway, and when the client names none the BFF has to read them.
func TestApproveAllDraftChunksSendsTokenBoundApprovals(t *testing.T) {
	const path = "/workspaces/team-a/sandboxes/sb1/drafts/approve-all"
	pending := []openshell.PolicyChunk{
		{ID: "c1", Status: "pending", ReviewToken: "tok-1"},
		{ID: "c2", Status: "pending", ReviewToken: "tok-2", SecurityNotes: "wildcard host"},
	}
	tests := []struct {
		name          string
		body          string
		wantFilter    string
		wantApprovals []openshell.DraftChunkApproval
		wantStatus    int
		wantDraftRead bool
		wantFlagged   bool
	}{
		{
			name:       "the approvals the reviewer saw are sent as they are",
			body:       `{"approvals":[{"chunkId":"c1","reviewToken":"seen-1"},{"chunkId":"c2","reviewToken":"seen-2"}],"includeSecurityFlagged":true}`,
			wantStatus: http.StatusOK,
			wantApprovals: []openshell.DraftChunkApproval{
				{ChunkID: "c1", ReviewToken: "seen-1"},
				{ChunkID: "c2", ReviewToken: "seen-2"},
			},
			wantFlagged: true,
		},
		{
			name:       "a subset approves only what it names",
			body:       `{"approvals":[{"chunkId":"c2","reviewToken":"seen-2"}]}`,
			wantStatus: http.StatusOK,
			wantApprovals: []openshell.DraftChunkApproval{
				{ChunkID: "c2", ReviewToken: "seen-2"},
			},
		},
		{
			name:          "no approvals: every pending chunk with the token it carries now",
			body:          `{"includeSecurityFlagged":true}`,
			wantStatus:    http.StatusOK,
			wantDraftRead: true,
			wantFilter:    "pending",
			wantApprovals: []openshell.DraftChunkApproval{
				{ChunkID: "c1", ReviewToken: "tok-1"},
				{ChunkID: "c2", ReviewToken: "tok-2"},
			},
			wantFlagged: true,
		},
		{
			name:          "no body at all",
			body:          ``,
			wantStatus:    http.StatusOK,
			wantDraftRead: true,
			wantFilter:    "pending",
			wantApprovals: []openshell.DraftChunkApproval{
				{ChunkID: "c1", ReviewToken: "tok-1"},
				{ChunkID: "c2", ReviewToken: "tok-2"},
			},
		},
		{name: "an approval without a chunk", body: `{"approvals":[{"reviewToken":"seen-1"}]}`, wantStatus: http.StatusBadRequest},
		{name: "invalid json", body: `{"approvals":`, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			draftRead := false
			sdk.policy.getDraftFn = func(_ context.Context, workspace, name string, opts ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
				draftRead = true
				if workspace != "team-a" || name != "sb1" {
					t.Errorf("GetDraft(%q, %q), want (team-a, sb1)", workspace, name)
				}
				if got := draftStatusFilter(opts); got != tc.wantFilter {
					t.Errorf("draft read with status filter %q, want %q", got, tc.wantFilter)
				}
				return &openshell.DraftPolicy{Chunks: pending}, nil
			}
			called := false
			var gotApprovals []openshell.DraftChunkApproval
			gotFlagged := false
			sdk.policy.approveAllFn = func(_ context.Context, _, _ string, opts ...openshell.ApproveAllOption) (*openshell.ApproveAllResult, error) {
				called = true
				cfg := sdktypes.ApplyApproveAllOptions(opts)
				gotApprovals, gotFlagged = (&cfg).Approvals(), (&cfg).IncludeSecurityFlagged()
				return &openshell.ApproveAllResult{PolicyVersion: 5, PolicyHash: "h", ChunksApproved: 1, ChunksSkipped: 1}, nil
			}

			w := serve(t, draftRoutes(sdk), http.MethodPost, path, tc.body)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				if called {
					t.Error("the gateway was asked to approve for a request the BFF refuses")
				}
				return
			}
			if draftRead != tc.wantDraftRead {
				t.Errorf("draft read = %v, want %v", draftRead, tc.wantDraftRead)
			}
			if !reflect.DeepEqual(gotApprovals, tc.wantApprovals) {
				t.Errorf("approvals sent to the gateway = %+v, want %+v", gotApprovals, tc.wantApprovals)
			}
			if gotFlagged != tc.wantFlagged {
				t.Errorf("includeSecurityFlagged = %v, want %v", gotFlagged, tc.wantFlagged)
			}
			var got approveAllResponse
			decodeInto(t, w, &got)
			if want := (approveAllResponse{PolicyHash: "h", PolicyVersion: 5, ChunksApproved: 1, ChunksSkipped: 1}); got != want {
				t.Errorf("response = %+v, want %+v", got, want)
			}
		})
	}
}

// approveAllResponse is the approve-all response as the frontend reads it.
type approveAllResponse struct {
	PolicyHash     string `json:"policyHash"`
	PolicyVersion  uint32 `json:"policyVersion"`
	ChunksApproved uint32 `json:"chunksApproved"`
	ChunksSkipped  uint32 `json:"chunksSkipped"`
}

// With nothing pending the BFF sends no approvals and the gateway answers,
// rather than the BFF inventing a result of its own.
func TestApproveAllDraftChunksEmptyInbox(t *testing.T) {
	sdk := &mockSDK{}
	sdk.policy.getDraftFn = func(context.Context, string, string, ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
		return &openshell.DraftPolicy{}, nil
	}
	sdk.policy.approveAllFn = func(_ context.Context, _, _ string, opts ...openshell.ApproveAllOption) (*openshell.ApproveAllResult, error) {
		cfg := sdktypes.ApplyApproveAllOptions(opts)
		if approvals := (&cfg).Approvals(); len(approvals) != 0 {
			t.Errorf("approvals = %+v, want none", approvals)
		}
		return nil, &openshell.StatusError{Code: openshell.ErrorConflict, Message: "no pending chunks to approve"}
	}
	w := serve(t, draftRoutes(sdk), http.MethodPost, "/workspaces/team-a/sandboxes/sb1/drafts/approve-all", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want the gateway's 409; body: %s", w.Code, w.Body.String())
	}
}

func TestApproveAllDraftChunksDraftReadFails(t *testing.T) {
	sdk := &mockSDK{}
	sdk.policy.getDraftFn = func(context.Context, string, string, ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"}
	}
	sdk.policy.approveAllFn = func(context.Context, string, string, ...openshell.ApproveAllOption) (*openshell.ApproveAllResult, error) {
		t.Error("the gateway was asked to approve although the pending chunks could not be read")
		return nil, nil
	}
	w := serve(t, draftRoutes(sdk), http.MethodPost, "/workspaces/team-a/sandboxes/missing/drafts/approve-all", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestGetDraftPolicyStatusFilterAndChunkFields(t *testing.T) {
	first := time.UnixMilli(1_700_000_100_000)
	last := time.UnixMilli(1_700_000_200_000)
	tests := []struct {
		name       string
		query      string
		wantFilter string
	}{
		{name: "all", query: "", wantFilter: ""},
		{name: "pending", query: "?status=pending", wantFilter: "pending"},
		{name: "approved", query: "?status=approved", wantFilter: "approved"},
		{name: "rejected", query: "?status=rejected", wantFilter: "rejected"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.policy.getDraftFn = func(_ context.Context, _, _ string, opts ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
				if got := draftStatusFilter(opts); got != tc.wantFilter {
					t.Errorf("status filter = %q, want %q", got, tc.wantFilter)
				}
				return &openshell.DraftPolicy{
					DraftVersion: 3,
					Chunks: []openshell.PolicyChunk{{
						ID: "c2", Status: "pending", Stage: "refined", SupersedesChunkID: "c1",
						DenialSummaryIDs: []string{"d1"}, HitCount: 4, FirstSeen: first, LastSeen: last,
					}},
				}, nil
			}
			w := serve(t, draftRoutes(sdk), http.MethodGet, "/workspaces/team-a/sandboxes/sb1/drafts"+tc.query, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
			}
			var got models.DraftPolicy
			decodeInto(t, w, &got)
			want := models.DraftPolicy{DraftVersion: 3, Chunks: []models.PolicyChunk{{
				ID: "c2", Status: "pending", Stage: "refined", SupersedesChunkID: "c1",
				DenialSummaryIDs: []string{"d1"}, HitCount: 4,
				FirstSeenMs: first.UnixMilli(), LastSeenMs: last.UnixMilli(),
			}}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("draft differs.\ngot:  %+v\nwant: %+v", got, want)
			}
		})
	}
}

// An empty inbox is an empty array, never null: the tab reads chunks.length.
func TestGetDraftPolicyEmptyInbox(t *testing.T) {
	w := serve(t, draftRoutes(&mockSDK{}), http.MethodGet, "/workspaces/team-a/sandboxes/sb1/drafts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"chunks":[]`) {
		t.Errorf(`want an empty "chunks" array, got: %s`, w.Body.String())
	}
}
