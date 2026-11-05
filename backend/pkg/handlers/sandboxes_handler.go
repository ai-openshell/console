package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

type SandboxHandler struct {
	svc services.SandboxServiceInterface
}

func NewSandboxHandler(svc services.SandboxServiceInterface) *SandboxHandler {
	return &SandboxHandler{
		svc: svc,
	}
}

func (h *SandboxHandler) ListSandboxes(w http.ResponseWriter, r *http.Request) {
	opts, paged, valid := listRequestOptions(w, r)
	if !valid {
		return
	}
	if paged {
		pager, err := h.svc.List(r.PathValue("workspace"), opts)
		if err != nil {
			apiutils.WriteSDKError(w, err)
			return
		}
		page, err := pager.NextPage(r.Context())
		if err != nil {
			apiutils.WriteSDKError(w, err)
			return
		}
		items := make([]models.Sandbox, 0, len(page.Items))
		for _, sandbox := range page.Items {
			items = append(items, models.FromSDKSandbox(sandbox))
		}
		apiutils.WriteJSON(w, http.StatusOK, listPageResponse[models.Sandbox]{Items: items, NextPageToken: page.NextPageToken})
		return
	}
	sandboxes, err := h.svc.ListAll(r.Context(), r.PathValue("workspace"), opts)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	out := make([]models.Sandbox, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		out = append(out, models.FromSDKSandbox(sandbox))
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

func (h *SandboxHandler) CreateSandbox(w http.ResponseWriter, r *http.Request) {
	var body models.CreateSandboxRequest
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if body.Name != "" && !apiutils.ValidDNS1123(body.Name) {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidName, "sandbox name must be a valid DNS-1123 label")
		return
	}
	// No image is not an error: the gateway then runs its own default image,
	// which is what `openshell sandbox create` without --from asks for.
	if len(body.Policy) == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy, "policy is required: the gateway accepts a sandbox without one, but unless it has a default policy to apply the sandbox never becomes ready")
		return
	}
	if !validServiceExposures(w, body.ServiceExposures) {
		return
	}
	spec, err := models.BuildSDKSandboxSpec(body)
	if err != nil {
		slog.Error("invalid sandbox specification", "error", err)
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPolicy, "invalid sandbox specification")
		return
	}

	createOpts := models.BuildSDKCreateOptions(body.Labels, body.Annotations, body.ServiceExposures)

	sandbox, err := h.svc.Create(r.Context(), r.PathValue("workspace"), body.Name, spec, createOpts...)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusCreated, models.FromSDKSandbox(sandbox))
}

// maxServicePort is the highest port a service can be exposed on.
const maxServicePort = 65535

// validServiceExposures refuses a service to expose at create whose port is
// not a port, the one check `openshell sandbox create --expose` makes before
// it calls the gateway, and reports whether there was none. Everything else
// about an exposure (its name, a name used twice, how many there are) is the
// gateway's to judge, and its answer is passed on.
func validServiceExposures(w http.ResponseWriter, exposures []models.ServiceExposure) bool {
	for i, exposure := range exposures {
		if exposure.TargetPort == 0 || exposure.TargetPort > maxServicePort {
			apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPort,
				fmt.Sprintf("serviceExposures[%d].targetPort must be in 1..=%d", i, maxServicePort))
			return false
		}
	}
	return true
}

func (h *SandboxHandler) GetSandbox(w http.ResponseWriter, r *http.Request) {
	sandbox, err := h.svc.Get(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKSandbox(sandbox))
}

// StopSandbox stops a running sandbox while retaining its persistent state.
// The sandbox transitions through STOPPING to STOPPED and can be resumed with
// StartSandbox.
func (h *SandboxHandler) StopSandbox(w http.ResponseWriter, r *http.Request) {
	sandbox, err := h.svc.Stop(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKSandbox(sandbox))
}

// StartSandbox resumes a previously stopped sandbox. The sandbox transitions
// through STARTING back to READY.
func (h *SandboxHandler) StartSandbox(w http.ResponseWriter, r *http.Request) {
	sandbox, err := h.svc.Start(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKSandbox(sandbox))
}

func (h *SandboxHandler) DeleteSandbox(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Delete(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	// A missing sandbox is still NotFound (mapped to 404). Deletion may also be
	// accepted for asynchronous cleanup rather than completed — see
	// models.FromSDKDeletion.
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKDeletion(res))
}
