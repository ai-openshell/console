package handlers

import (
	"net/http"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// ExposeServiceRequest is the expose-service body. Service may be empty: a
// sandbox has one unnamed endpoint, which is what `openshell service expose
// <sandbox> <port>` creates when no service name is given.
//
// Domain is forwarded as it is. Gateways 0.1.0 to 0.1.2 ignore it and enable
// browser-facing routing on every endpoint, which is also what the CLI asks
// for.
type ExposeServiceRequest struct {
	Service    string `json:"service"`
	TargetPort uint32 `json:"targetPort"`
	Domain     bool   `json:"domain"`
}

type ServicesHandler struct {
	svc services.ServiceServiceInterface
}

func NewServicesHandler(svc services.ServiceServiceInterface) *ServicesHandler {
	return &ServicesHandler{
		svc: svc,
	}
}

// ListServices lists the service endpoints of one sandbox. On the route
// without a sandbox name it lists those of every sandbox in the workspace,
// which is what the gateway does with an empty sandbox filter and what
// `openshell service list` prints when no sandbox is named.
func (h *ServicesHandler) ListServices(w http.ResponseWriter, r *http.Request) {
	serviceEndpoints, err := h.svc.ListAll(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	out := make([]models.ServiceEndpoint, 0, len(serviceEndpoints))
	for _, svc := range serviceEndpoints {
		out = append(out, models.FromSDKServiceEndpoint(svc))
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

func (h *ServicesHandler) ExposeService(w http.ResponseWriter, r *http.Request) {
	var body ExposeServiceRequest
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if body.TargetPort == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidPort, "targetPort must be greater than 0")
		return
	}
	svc, err := h.svc.Expose(r.Context(), r.PathValue("workspace"), r.PathValue("name"), body.Service, body.TargetPort, body.Domain)
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusCreated, models.FromSDKServiceEndpoint(svc))
}

// DeleteService removes one service endpoint of a sandbox. On the route
// without a service name it removes the sandbox's unnamed endpoint, which a
// path segment cannot name.
func (h *ServicesHandler) DeleteService(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Delete(r.Context(), r.PathValue("workspace"), r.PathValue("name"), r.PathValue("svc"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKDeletion(res))
}
