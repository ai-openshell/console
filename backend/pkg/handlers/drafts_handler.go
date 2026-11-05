package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// ApproveDraftChunkRequest optionally carries the chunk's review token, which
// the frontend has from its last GetDraftPolicy fetch. Without one the handler
// reads the chunk's token from the gateway (resolveDraftReviewToken).
type ApproveDraftChunkRequest struct {
	ReviewToken string `json:"reviewToken,omitempty"`
}

// RejectDraftChunkRequest carries the optional reviewer reason, surfaced back
// to the in-sandbox agent.
type RejectDraftChunkRequest struct {
	Reason string `json:"reason,omitempty"`
}

type DraftsHandler struct {
	svc       services.PolicyServiceInterface
	sandboxes services.SandboxServiceInterface
}

func NewDraftsHandler(svc services.PolicyServiceInterface) *DraftsHandler {
	return &DraftsHandler{svc: svc}
}

// SetSandboxService gives the handler the sandbox lists the draft summaries
// are built from. It is a method rather than a NewDraftsHandler parameter, so
// a handler can be built without one; its summaries are then empty. Call it
// before the handler serves requests.
func (h *DraftsHandler) SetSandboxService(sandboxes services.SandboxServiceInterface) {
	h.sandboxes = sandboxes
}

// draftStatusPending is the status filter of a chunk nobody has decided yet.
const draftStatusPending = "pending"

// A draft summary is one GetDraftPolicy per sandbox. draftSummaryConcurrency
// bounds how many are in flight at once (the TUI reads 16 at a time for the
// same badges), and draftSummaryReadTimeout how long one may take before its
// sandbox is reported as unavailable, so that a slow inbox cannot hold up the
// summary of every other sandbox. draftSummaryTimeout bounds the whole
// summary: a client polls it, so an answer that takes longer than a poll is
// worth less than one that says which sandboxes it did not get to.
const (
	draftSummaryConcurrency = 16
	draftSummaryReadTimeout = 5 * time.Second
	draftSummaryTimeout     = 20 * time.Second
)

// GetWorkspaceDraftSummary counts the pending draft chunks of every sandbox
// in a workspace. See summarizeDrafts.
func (h *DraftsHandler) GetWorkspaceDraftSummary(w http.ResponseWriter, r *http.Request) {
	if names, named := r.URL.Query()["sandbox"]; named {
		if len(names) > 100 {
			apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidArgument, "you can request draft summaries for no more than 100 sandboxes at a time")
			return
		}
		seen := make(map[string]bool, len(names))
		sandboxes := make([]*openshell.Sandbox, 0, len(names))
		for _, name := range names {
			if !apiutils.ValidDNS1123(name) {
				apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidName, "sandbox must be a valid DNS-1123 name")
				return
			}
			if !seen[name] {
				seen[name] = true
				sandboxes = append(sandboxes, &openshell.Sandbox{Name: name, Workspace: r.PathValue("workspace")})
			}
		}
		apiutils.WriteJSON(w, http.StatusOK, h.summarizeDrafts(r.Context(), r.PathValue("workspace"), sandboxes))
		return
	}
	h.writeDraftSummary(w, r, r.PathValue("workspace"))
}

// GetDraftSummary is the summary outside a workspace route. With ?workspace=
// it is that workspace's, the same answer as GetWorkspaceDraftSummary. With
// ?allWorkspaces=true it is every workspace's, which is what a platform
// admin's shell (the Proposals count in the navigation), Gateway page and
// Proposals page ask for: the gateway answers the all-workspaces sandbox list
// for platform admins only and refuses everyone else, and that refusal is
// relayed as it is (ADR 0002).
//
// With neither it answers an empty summary. Frontends older than the
// workspace route poll this URL without a workspace, every few seconds and
// for every user; a summary of every workspace would be one draft read per
// sandbox on the gateway per poll, and a 403 for everyone who is not a
// platform admin. So the cross-workspace summary is asked for by name,
// never inferred.
func (h *DraftsHandler) GetDraftSummary(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("allWorkspaces") == "true" {
		h.writeDraftSummary(w, r, "", everyWorkspace(""))
		return
	}
	workspace := r.URL.Query().Get("workspace")
	if workspace == "" {
		apiutils.WriteJSON(w, http.StatusOK, models.DraftSummary{Sandboxes: []models.DraftSandboxSummary{}})
		return
	}
	h.writeDraftSummary(w, r, workspace)
}

// writeDraftSummary summarizes the sandboxes the given list options select.
// Across workspaces the workspace argument is empty and the SDK ignores it;
// each sandbox names its own, which summarizeDrafts prefers. A sandbox that
// still resolves to no workspace is reported unavailable, never read with an
// empty selector (see summarizeDrafts).
func (h *DraftsHandler) writeDraftSummary(w http.ResponseWriter, r *http.Request, workspace string, opts ...openshell.ListOptions) {
	if h.sandboxes == nil {
		slog.Warn("draft summary requested, but the drafts handler was given no sandbox service")
		apiutils.WriteJSON(w, http.StatusOK, models.DraftSummary{Sandboxes: []models.DraftSandboxSummary{}})
		return
	}
	sandboxes, err := h.sandboxes.ListAll(r.Context(), workspace, opts...)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, h.summarizeDrafts(r.Context(), workspace, sandboxes))
}

// summarizeDrafts reads the pending draft chunks of each sandbox, a bounded
// number at a time, and reports the sandboxes that have any.
//
// One sandbox failing does not fail the summary. Its entry says the count is
// unavailable, which a client must not read as none pending, and so does the
// entry of a sandbox the summary ran out of time for. A sandbox that is gone
// by the time it is read is left out: there is nothing to report on.
// workspace is the fallback for a sandbox that names none itself. Across
// workspaces there is no fallback, and a sandbox that names no workspace is
// reported unavailable without a read: the gateway refuses a selector with
// an empty name (hard fact 12), and a client could not lead anywhere from an
// entry without one.
func (h *DraftsHandler) summarizeDrafts(ctx context.Context, workspace string, sandboxes []*openshell.Sandbox) models.DraftSummary {
	ctx, cancel := context.WithTimeout(ctx, draftSummaryTimeout)
	defer cancel()

	entries := make([]*models.DraftSandboxSummary, len(sandboxes))
	read := make([]bool, len(sandboxes))
	// Decided before any read starts, so the workers never see these.
	markUnscoped(workspace, sandboxes, entries, read)
	indexes := make(chan int)
	var wg sync.WaitGroup
	for range min(draftSummaryConcurrency, len(sandboxes)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indexes {
				sandbox := sandboxes[i]
				if sandbox == nil {
					continue
				}
				entries[i] = h.pendingDrafts(ctx, sandboxWorkspaceOr(sandbox, workspace), sandbox.Name)
			}
		}()
	}
	// Nothing more is started once the client has gone or the time is up.
	for i := range sandboxes {
		if read[i] {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		read[i] = true
		indexes <- i
	}
	close(indexes)
	wg.Wait()

	for i, sandbox := range sandboxes {
		if !read[i] && sandbox != nil {
			entries[i] = &models.DraftSandboxSummary{
				Workspace:   sandboxWorkspaceOr(sandbox, workspace),
				SandboxName: sandbox.Name,
				Unavailable: true,
			}
		}
	}

	summary := models.DraftSummary{Sandboxes: []models.DraftSandboxSummary{}}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		summary.Sandboxes = append(summary.Sandboxes, *entry)
		summary.TotalPending += entry.PendingCount
	}
	return summary
}

// markUnscoped reports every sandbox that resolves to no workspace as
// unavailable and marks it read, so it is never read with an empty selector
// (hard fact 12). It logs them once.
func markUnscoped(workspace string, sandboxes []*openshell.Sandbox, entries []*models.DraftSandboxSummary, read []bool) {
	var unscoped []string
	for i, sandbox := range sandboxes {
		if sandbox != nil && sandboxWorkspaceOr(sandbox, workspace) == "" {
			entries[i] = &models.DraftSandboxSummary{SandboxName: sandbox.Name, Unavailable: true}
			read[i] = true
			unscoped = append(unscoped, sandbox.Name)
		}
	}
	if len(unscoped) > 0 {
		slog.Warn("pending draft chunks not read for sandboxes listed without a workspace", "sandboxes", unscoped)
	}
}

// sandboxWorkspaceOr is the workspace a sandbox names, or the one it was
// listed in when it names none.
func sandboxWorkspaceOr(sandbox *openshell.Sandbox, workspace string) string {
	if sandbox.Workspace != "" {
		return sandbox.Workspace
	}
	return workspace
}

// pendingDrafts reads one sandbox's pending chunks and returns its summary
// entry, or nil when it has none pending or no longer exists.
func (h *DraftsHandler) pendingDrafts(ctx context.Context, workspace, name string) *models.DraftSandboxSummary {
	readCtx, cancel := context.WithTimeout(ctx, draftSummaryReadTimeout)
	defer cancel()
	draft, err := h.svc.GetDraft(readCtx, workspace, name, openshell.WithStatusFilter(draftStatusPending))
	switch {
	case err == nil:
	case openshell.IsNotFound(err):
		return nil
	default:
		// A summary nobody is waiting for any more is not worth a line each.
		if ctx.Err() == nil {
			slog.Warn("pending draft chunks unavailable", "workspace", workspace, "sandbox", name, "error", err)
		}
		return &models.DraftSandboxSummary{Workspace: workspace, SandboxName: name, Unavailable: true}
	}
	if draft == nil || len(draft.Chunks) == 0 {
		return nil
	}
	entry := &models.DraftSandboxSummary{
		Workspace:    workspace,
		SandboxName:  name,
		PendingCount: len(draft.Chunks),
	}
	for i := range draft.Chunks {
		chunk := &draft.Chunks[i]
		if chunk.SecurityNotes != "" {
			entry.HasSecurityFlags = true
		}
		if chunk.CreatedAt.IsZero() {
			// No creation time to order by: keep the first rationale we see.
			if entry.LatestRationale == "" {
				entry.LatestRationale = chunk.Rationale
			}
			continue
		}
		createdMs := chunk.CreatedAt.UnixMilli()
		if createdMs > entry.LatestDraftMs {
			entry.LatestDraftMs = createdMs
			entry.LatestRationale = chunk.Rationale
		}
	}
	return entry
}

// GetDraftPolicy returns the draft-policy inbox for a sandbox. Optional
// ?status=pending|approved|rejected filter.
func (h *DraftsHandler) GetDraftPolicy(w http.ResponseWriter, r *http.Request) {
	var opts []openshell.GetDraftOption
	if status := r.URL.Query().Get("status"); status != "" {
		opts = append(opts, openshell.WithStatusFilter(status))
	}
	draft, err := h.svc.GetDraft(r.Context(), r.PathValue("workspace"), r.PathValue("name"), opts...)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKDraftPolicy(draft))
}

// ApproveDraftChunk merges one proposed rule into the active policy.
func (h *DraftsHandler) ApproveDraftChunk(w http.ResponseWriter, r *http.Request) {
	workspace := r.PathValue("workspace")
	name := r.PathValue("name")
	chunkID := r.PathValue("chunk")

	var body ApproveDraftChunkRequest
	if r.ContentLength > 0 && !apiutils.DecodeBody(w, r, &body) {
		return
	}

	// The gateway binds each approval to a review_token that pins the exact
	// evaluated candidate (optimistic concurrency). Use the token the client
	// already has; only fall back to a GetDraft round-trip when it didn't send
	// one. Older gateways return an empty token, which the RPC accepts.
	reviewToken := body.ReviewToken
	if reviewToken == "" {
		var err error
		reviewToken, err = h.resolveDraftReviewToken(r.Context(), workspace, name, chunkID)
		if err != nil {
			apiutils.WriteSDKError(w, err)
			return
		}
	}

	result, err := h.svc.ApproveDraftChunk(r.Context(), workspace, name, chunkID, reviewToken)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.PolicyUpdateResult{
		Version:    result.PolicyVersion,
		PolicyHash: result.PolicyHash,
	})
}

// resolveDraftReviewToken returns the review token bound to the given draft
// chunk, or an empty string if the chunk carries none. The token pins an
// approval to the exact candidate the gateway last evaluated.
func (h *DraftsHandler) resolveDraftReviewToken(ctx context.Context, workspace, name, chunkID string) (string, error) {
	draft, err := h.svc.GetDraft(ctx, workspace, name)
	if err != nil {
		return "", err
	}
	for i := range draft.Chunks {
		if draft.Chunks[i].ID == chunkID {
			return draft.Chunks[i].ReviewToken, nil
		}
	}
	return "", nil
}

// RejectDraftChunk rejects one proposed rule.
func (h *DraftsHandler) RejectDraftChunk(w http.ResponseWriter, r *http.Request) {
	var body RejectDraftChunkRequest
	if r.ContentLength > 0 && !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if err := h.svc.RejectDraftChunk(r.Context(), r.PathValue("workspace"), r.PathValue("name"), r.PathValue("chunk"), body.Reason); err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, map[string]bool{"rejected": true})
}

// DraftChunkApproval names one reviewed chunk and the review token it was
// fetched with. It mirrors openshell.v1.DraftChunkApproval.
type DraftChunkApproval struct {
	ChunkID     string `json:"chunkId"`
	ReviewToken string `json:"reviewToken,omitempty"`
}

// ApproveAllDraftChunksRequest mirrors ApproveAllDraftChunksRequest: the
// chunks the reviewer saw, each with its review token, and whether the
// security-flagged ones among them are approved too. Approvals may be absent:
// the BFF then approves the pending chunks it reads (see
// ApproveAllDraftChunks).
type ApproveAllDraftChunksRequest struct {
	Approvals              []DraftChunkApproval `json:"approvals,omitempty"`
	IncludeSecurityFlagged bool                 `json:"includeSecurityFlagged,omitempty"`
}

// ApproveAllDraftChunks approves pending chunks in one write (security-flagged
// ones are skipped unless explicitly included).
//
// The gateway binds a bulk approval to review tokens the same way it binds a
// single one: a chunk that has a token and is approved without it is skipped
// as stale, so a request that names no approvals approves nothing against a
// gateway that issues tokens. The tokens the client sends are used as they
// are, because they pin what the reviewer was looking at. Only when it sends
// none does the BFF read the pending chunks and approve those, which is what
// `openshell draft approve-all` does.
func (h *DraftsHandler) ApproveAllDraftChunks(w http.ResponseWriter, r *http.Request) {
	workspace := r.PathValue("workspace")
	name := r.PathValue("name")

	var body ApproveAllDraftChunksRequest
	if r.ContentLength > 0 && !apiutils.DecodeBody(w, r, &body) {
		return
	}

	approvals := make([]openshell.DraftChunkApproval, 0, len(body.Approvals))
	for _, approval := range body.Approvals {
		if approval.ChunkID == "" {
			apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidRequest, "approvals[].chunkId is required")
			return
		}
		approvals = append(approvals, openshell.DraftChunkApproval{
			ChunkID:     approval.ChunkID,
			ReviewToken: approval.ReviewToken,
		})
	}
	if len(approvals) == 0 {
		pending, err := h.pendingDraftApprovals(r.Context(), workspace, name)
		if err != nil {
			apiutils.WriteSDKError(w, err)
			return
		}
		approvals = pending
	}

	var opts []openshell.ApproveAllOption
	if body.IncludeSecurityFlagged {
		opts = append(opts, openshell.WithIncludeSecurityFlagged())
	}
	if len(approvals) > 0 {
		opts = append(opts, openshell.WithDraftApprovals(approvals...))
	}
	result, err := h.svc.ApproveAllDraftChunks(r.Context(), workspace, name, opts...)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, map[string]any{
		"policyVersion":  result.PolicyVersion,
		"policyHash":     result.PolicyHash,
		"chunksApproved": result.ChunksApproved,
		"chunksSkipped":  result.ChunksSkipped,
	})
}

// pendingDraftApprovals returns an approval for every pending chunk of the
// sandbox, each bound to the review token the chunk carries now. An empty
// inbox yields none, and the gateway then answers the bulk approval itself.
func (h *DraftsHandler) pendingDraftApprovals(ctx context.Context, workspace, name string) ([]openshell.DraftChunkApproval, error) {
	draft, err := h.svc.GetDraft(ctx, workspace, name, openshell.WithStatusFilter(draftStatusPending))
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, nil
	}
	approvals := make([]openshell.DraftChunkApproval, 0, len(draft.Chunks))
	for i := range draft.Chunks {
		approvals = append(approvals, openshell.DraftChunkApproval{
			ChunkID:     draft.Chunks[i].ID,
			ReviewToken: draft.Chunks[i].ReviewToken,
		})
	}
	return approvals, nil
}

// EditDraftChunkRequest carries the replacement proposed rule as JSON.
type EditDraftChunkRequest struct {
	ProposedRule json.RawMessage `json:"proposedRule"`
}

// EditDraftChunk replaces the proposed rule on a pending draft chunk.
func (h *DraftsHandler) EditDraftChunk(w http.ResponseWriter, r *http.Request) {
	var body EditDraftChunkRequest
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if len(body.ProposedRule) == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidRule, "proposedRule is required")
		return
	}
	rule, err := models.ParseSDKNetworkPolicyRule(body.ProposedRule)
	if err != nil {
		slog.Error("invalid network policy rule", "error", err)
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidRule, "proposedRule does not match NetworkPolicyRule schema: "+err.Error())
		return
	}
	if err := h.svc.EditDraftChunk(r.Context(), r.PathValue("workspace"), r.PathValue("name"), r.PathValue("chunk"), rule); err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, map[string]bool{"edited": true})
}

// UndoDraftChunk reverts an already-approved chunk, removing its rule from the
// active policy.
func (h *DraftsHandler) UndoDraftChunk(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.UndoDraftChunk(r.Context(), r.PathValue("workspace"), r.PathValue("name"), r.PathValue("chunk"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.PolicyUpdateResult{
		Version:    result.PolicyVersion,
		PolicyHash: result.PolicyHash,
	})
}

// ClearDraftChunks removes all pending draft chunks for a sandbox.
func (h *DraftsHandler) ClearDraftChunks(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.ClearDraftChunks(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, map[string]any{
		"chunksCleared": result.ChunksCleared,
	})
}

// GetDraftHistory returns the chronological decision history for a sandbox's
// draft policy.
func (h *DraftsHandler) GetDraftHistory(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.GetDraftHistory(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKDraftHistory(entries))
}
