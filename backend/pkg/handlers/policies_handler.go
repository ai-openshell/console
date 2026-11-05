package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// UpdatePolicyRequest carries the full replacement policy as JSON.
// Sandbox-scoped updates may only change network_policies and inference
// fields — filesystem/landlock/process must match the create-time policy.
type UpdatePolicyRequest struct {
	Policy                  json.RawMessage `json:"policy"`
	ExpectedResourceVersion uint64          `json:"expectedResourceVersion,omitempty"`
}

// MergePolicyRequest carries incremental policy operations, each the protojson
// form of openshell.v1.PolicyMergeOperation: exactly one of addRule,
// removeEndpoint, removeRule, addDenyRules, addAllowRules or removeBinary.
//
// It has no expectedResourceVersion on purpose. The gateway applies the
// operations to the latest policy at the moment it writes and retries when it
// loses a race, so there is no stale copy for a version to guard, and
// `openshell policy update` sends none either.
type MergePolicyRequest struct {
	Operations []json.RawMessage `json:"operations"`
}

// policyStatusSuperseded is the status of a revision a newer one, or the
// removal of the global policy, has replaced.
const policyStatusSuperseded = "SUPERSEDED"

type PoliciesHandler struct {
	svc       services.PolicyServiceInterface
	configSvc services.ConfigServiceInterface
}

func NewPoliciesHandler(
	svc services.PolicyServiceInterface,
	configSvc services.ConfigServiceInterface,
) *PoliciesHandler {
	return &PoliciesHandler{
		svc:       svc,
		configSvc: configSvc,
	}
}

// GetSandboxPolicy returns the latest revision, active version, and revision
// history for a sandbox. Every revision carries its policy, which the
// gateway's listing leaves out, so history is read with GetStatus(WithVersion).
func (h *PoliciesHandler) GetSandboxPolicy(w http.ResponseWriter, r *http.Request) {
	workspace := r.PathValue("workspace")
	name := r.PathValue("name")
	ctx := r.Context()

	status, err := h.svc.GetStatus(ctx, workspace, name)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}

	view := models.FromSDKPolicyStatus(status)
	latest := models.FromSDKPolicyRevision(&status.Revision)
	n := status.Revision.Version
	view.Revisions = make([]models.PolicyRevision, 0, n)

	// One GetStatus(WithVersion) per revision, because only that returns the
	// policy. Fetch older versions in parallel; keep latest from the first
	// GetStatus to avoid a redundant round-trip.
	if n > 1 {
		historical := make([]models.PolicyRevision, n-1)
		var wg sync.WaitGroup
		for v := uint32(1); v < n; v++ {
			wg.Add(1)
			go func(v uint32) {
				defer wg.Done()
				revStatus, revErr := h.svc.GetStatus(ctx, workspace, name, openshell.WithVersion(v))
				if revErr != nil {
					slog.Warn("sandbox policy revision unavailable",
						"workspace", workspace, "sandbox", name, "version", v, "error", revErr)
					return
				}
				historical[v-1] = models.FromSDKPolicyRevision(&revStatus.Revision)
			}(v)
		}
		wg.Wait()
		for _, rev := range historical {
			if rev.Version != 0 {
				view.Revisions = append(view.Revisions, rev)
			}
		}
	}
	if n >= 1 {
		view.Revisions = append(view.Revisions, latest)
	}
	apiutils.WriteJSON(w, http.StatusOK, view)
}

// UpdateSandboxPolicy applies a policy update to a sandbox via Config.Update.
func (h *PoliciesHandler) UpdateSandboxPolicy(w http.ResponseWriter, r *http.Request) {
	var body UpdatePolicyRequest
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if len(body.Policy) == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy, "policy is required")
		return
	}
	policy, err := models.ParseSDKPolicy(body.Policy)
	if err != nil {
		slog.Error("invalid policy specification", "error", err)
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy, "policy does not match the SandboxPolicy schema: "+err.Error())
		return
	}
	result, err := h.configSvc.Update(r.Context(), r.PathValue("workspace"), &openshell.ConfigUpdate{
		Name:                    r.PathValue("name"),
		Policy:                  policy,
		ExpectedResourceVersion: body.ExpectedResourceVersion,
	})
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.PolicyUpdateResult{
		Version:    result.Version,
		PolicyHash: result.PolicyHash,
	})
}

// MergeSandboxPolicy applies incremental policy operations to a sandbox, the
// way `openshell policy update` does. The gateway merges them into the
// sandbox's latest policy itself and retries when another writer gets in
// first, so nothing the caller did not name is rewritten, and it refuses a
// merge that would hand a binary or an endpoint access the operation did not
// declare. A full replacement goes through UpdateSandboxPolicy instead.
func (h *PoliciesHandler) MergeSandboxPolicy(w http.ResponseWriter, r *http.Request) {
	var body MergePolicyRequest
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if len(body.Operations) == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy, "operations is required")
		return
	}
	operations, err := models.ParseSDKPolicyMergeOperations(body.Operations)
	if err != nil {
		slog.Error("invalid policy merge operation", "error", err)
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy,
			"operations do not match the PolicyMergeOperation schema: "+err.Error())
		return
	}
	result, err := h.configSvc.Update(r.Context(), r.PathValue("workspace"), &openshell.ConfigUpdate{
		Name:            r.PathValue("name"),
		MergeOperations: operations,
	})
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.PolicyUpdateResult{
		Version:    result.Version,
		PolicyHash: result.PolicyHash,
	})
}

// GetEffectiveSandboxPolicy returns the policy the sandbox is given to
// enforce and where it comes from, which is what `openshell policy get` shows
// without --rev. See models.EffectivePolicy for how it differs from the latest
// revision GetSandboxPolicy returns.
func (h *PoliciesHandler) GetEffectiveSandboxPolicy(w http.ResponseWriter, r *http.Request) {
	config, err := h.configSvc.GetSandbox(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKEffectivePolicy(config))
}

// policyVersionParam reads the {version} path parameter. It writes the error
// response and returns false when it is not a positive revision number: zero
// means "the latest" to the gateway, which is not what a URL naming a revision
// asks for.
func policyVersionParam(w http.ResponseWriter, r *http.Request) (uint32, bool) {
	version, err := strconv.ParseUint(r.PathValue("version"), 10, 32)
	if err != nil || version == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidRequest, "version must be a positive policy revision number")
		return 0, false
	}
	return uint32(version), true
}

// GetSandboxPolicyRevision returns one revision of a sandbox's own policy with
// its payload (`openshell policy get --rev N --full`). It is also the cheap
// way to follow a revision from PENDING to LOADED or FAILED after an update.
func (h *PoliciesHandler) GetSandboxPolicyRevision(w http.ResponseWriter, r *http.Request) {
	version, ok := policyVersionParam(w, r)
	if !ok {
		return
	}
	status, err := h.svc.GetStatus(r.Context(), r.PathValue("workspace"), r.PathValue("name"), openshell.WithVersion(version))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKPolicyRevision(&status.Revision))
}

// GetGlobalPolicyRevision returns one revision of the gateway-global policy
// with its payload (`openshell policy get --global --rev N --full`). Platform
// Admin operation.
func (h *PoliciesHandler) GetGlobalPolicyRevision(w http.ResponseWriter, r *http.Request) {
	version, ok := policyVersionParam(w, r)
	if !ok {
		return
	}
	status, err := h.svc.GetStatus(r.Context(), "", "", openshell.WithStatusGlobal(true), openshell.WithVersion(version))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKPolicyRevision(&status.Revision))
}

// GetGlobalPolicy returns gateway-global policy revisions (Platform Admin).
//
// ActiveVersion is the version of the global policy in force, and zero when
// there is none. The gateway reports no active version for the global scope,
// so it is read off the newest revision: setting a global policy marks its
// revision loaded at once, and removing the policy supersedes every revision
// without deleting any.
func (h *PoliciesHandler) GetGlobalPolicy(w http.ResponseWriter, r *http.Request) {
	revisions, err := h.svc.ListAll(r.Context(), "", "", openshell.WithListGlobal(true))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	view := models.SandboxPolicyView{Revisions: []models.PolicyRevision{}}
	newest := -1
	for i := range revisions {
		view.Revisions = append(view.Revisions, models.FromSDKPolicyRevision(&revisions[i]))
		if newest < 0 || revisions[i].Version > revisions[newest].Version {
			newest = i
		}
	}
	if newest >= 0 {
		latest := &view.Revisions[newest]
		h.fillGlobalPolicyPayload(r.Context(), latest)
		view.Latest = latest
		if latest.Status != policyStatusSuperseded {
			view.ActiveVersion = latest.Version
		}
	}
	apiutils.WriteJSON(w, http.StatusOK, view)
}

// fillGlobalPolicyPayload adds the policy to the newest global revision. The
// gateway's listing carries every revision's metadata and no payload, so
// without this the page has nothing to show for the policy in force and
// nothing to start an edit from. A revision whose payload cannot be read is
// left as listed: the listing is the gateway's recovery surface for exactly
// that case, and it stays readable.
func (h *PoliciesHandler) fillGlobalPolicyPayload(ctx context.Context, latest *models.PolicyRevision) {
	status, err := h.svc.GetStatus(ctx, "", "", openshell.WithStatusGlobal(true), openshell.WithVersion(latest.Version))
	if err != nil {
		slog.Warn("global policy revision payload unavailable", "version", latest.Version, "error", err)
		return
	}
	if status == nil || status.Revision.Version != latest.Version {
		return
	}
	latest.Policy = models.FromSDKPolicyRevision(&status.Revision).Policy
}

// SetGlobalPolicy applies a gateway-global policy to all sandboxes in full
// (no merge). Platform Admin operation.
func (h *PoliciesHandler) SetGlobalPolicy(w http.ResponseWriter, r *http.Request) {
	var body UpdatePolicyRequest
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if len(body.Policy) == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy, "policy is required")
		return
	}
	policy, err := models.ParseSDKPolicy(body.Policy)
	if err != nil {
		slog.Error("invalid policy specification", "error", err)
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy, "policy does not match the SandboxPolicy schema: "+err.Error())
		return
	}
	result, err := h.configSvc.Update(r.Context(), "", &openshell.ConfigUpdate{
		Policy: policy,
		Global: true,
	})
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.PolicyUpdateResult{
		Version:    result.Version,
		PolicyHash: result.PolicyHash,
	})
}

// DeleteGlobalPolicy removes the gateway-global policy lock, restoring
// sandbox-level policy control. Platform Admin operation.
func (h *PoliciesHandler) DeleteGlobalPolicy(w http.ResponseWriter, r *http.Request) {
	if _, err := h.configSvc.Update(r.Context(), "", &openshell.ConfigUpdate{
		Global:        true,
		DeleteSetting: true,
		SettingKey:    "policy",
	}); err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
