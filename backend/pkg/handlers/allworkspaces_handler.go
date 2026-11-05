package handlers

import (
	"net/http"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// AllWorkspacesHandler serves the lists that span every workspace on the
// gateway: what the CLI's --all-workspaces flag does for sandbox list,
// provider list, sandbox template list and service list. Those four are the
// only RPCs that accept the all-workspaces scope.
//
// The gateway answers that scope for platform admins only. The BFF does not
// check the role (ADR 0002): a caller without it gets the gateway's refusal,
// relayed as it is. Every item carries the workspace it lives in, because a
// name is only unique within one.
type AllWorkspacesHandler struct {
	sandboxes services.SandboxServiceInterface
	providers services.ProviderServiceInterface
	templates services.TemplateServiceInterface
	services  services.ServiceServiceInterface
	keys      services.AllWorkspacesProviderCredentialKeyReader
}

func NewAllWorkspacesHandler(
	sandboxes services.SandboxServiceInterface,
	providers services.ProviderServiceInterface,
	templates services.TemplateServiceInterface,
	serviceEndpoints services.ServiceServiceInterface,
) *AllWorkspacesHandler {
	return &AllWorkspacesHandler{
		sandboxes: sandboxes,
		providers: providers,
		templates: templates,
		services:  serviceEndpoints,
	}
}

// SetCredentialKeyReader gives the handler the way to read which credentials
// each provider holds; see ProvidersHandler.SetCredentialKeyReader. Only a
// reader that can list across workspaces is used: without one the providers
// are returned with whatever the SDK carries. Call it before the handler
// serves requests.
func (h *AllWorkspacesHandler) SetCredentialKeyReader(keys services.ProviderCredentialKeyReader) {
	if all, ok := keys.(services.AllWorkspacesProviderCredentialKeyReader); ok {
		h.keys = all
	}
}

// everyWorkspace is the scope of every list here. With it the SDK ignores the
// workspace argument of ListAll, which is therefore passed empty.
func everyWorkspace(labelSelector string) openshell.ListOptions {
	return openshell.ListOptions{AllWorkspaces: true, LabelSelector: labelSelector}
}

// ListSandboxes lists the sandboxes of every workspace. Query params:
// labelSelector, as on the workspace-scoped list.
func (h *AllWorkspacesHandler) ListSandboxes(w http.ResponseWriter, r *http.Request) {
	opts, paged, valid := listRequestOptions(w, r)
	if !valid {
		return
	}
	opts.AllWorkspaces = true
	if paged {
		pager, err := h.sandboxes.List("", opts)
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
	sandboxes, err := h.sandboxes.ListAll(r.Context(), "", opts)
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

// ListProviders lists the providers of every workspace. Like the
// workspace-scoped list it takes no filter: the gateway offers none.
func (h *AllWorkspacesHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.providers.ListAll(r.Context(), "", everyWorkspace(""))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	var keys map[string]map[string][]string
	if h.keys != nil {
		if keys, err = h.keys.ListProviderCredentialKeysAllWorkspaces(r.Context()); err != nil {
			apiutils.WriteSDKError(w, err)
			return
		}
	}
	out := make([]models.Provider, 0, len(providers))
	for _, provider := range providers {
		dto := models.FromSDKProvider(provider)
		dto.AddCredentialNames(keys[dto.Metadata.Workspace][dto.Metadata.Name])
		out = append(out, dto)
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

// ListSandboxTemplates lists the reusable workload templates of every
// workspace. Query params: labelSelector, as on the workspace-scoped list.
func (h *AllWorkspacesHandler) ListSandboxTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.templates.ListAll(r.Context(), "", everyWorkspace(r.URL.Query().Get("labelSelector")))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	out := make([]models.SandboxTemplate, 0, len(templates))
	for _, t := range templates {
		out = append(out, models.FromSDKSandboxTemplate(t))
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

// ListServices lists the service endpoints exposed by the sandboxes of every
// workspace.
//
// Query params: sandbox. The gateway refuses it in this scope, because a
// sandbox name says nothing without its workspace. It is forwarded all the
// same, so that a caller who sends it gets that refusal instead of a list
// that silently ignored the filter.
func (h *AllWorkspacesHandler) ListServices(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.services.ListAll(r.Context(), "", r.URL.Query().Get("sandbox"), everyWorkspace(""))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	out := make([]models.ServiceEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		out = append(out, models.FromSDKServiceEndpoint(endpoint))
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}
