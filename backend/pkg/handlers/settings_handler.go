package handlers

import (
	"encoding/json"
	"net/http"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

type SettingsHandler struct {
	svc services.ConfigServiceInterface
}

func NewSettingsHandler(svc services.ConfigServiceInterface) *SettingsHandler {
	return &SettingsHandler{
		svc: svc,
	}
}

func (h *SettingsHandler) GetGlobalSettings(w http.ResponseWriter, r *http.Request) {
	config, err := h.svc.GetGateway(r.Context())
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKGatewaySettings(config))
}

// SetSettingRequest is the set-setting body. Value mirrors the gateway's typed
// SettingValue: a JSON string, boolean or integer, sent as the kind it is.
type SetSettingRequest struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// decodeSetting reads a set-setting body into the key and the typed value the
// gateway is sent. It answers the request itself and reports false for a body
// that names no key or carries a value that is not one a setting can take.
func decodeSetting(w http.ResponseWriter, r *http.Request) (string, *openshell.SettingValue, bool) {
	var body SetSettingRequest
	if !apiutils.DecodeBody(w, r, &body) {
		return "", nil, false
	}
	if body.Key == "" {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidSetting, "key is required")
		return "", nil, false
	}
	value, err := models.ParseSDKSettingValue(body.Value)
	if err != nil {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidSetting, err.Error())
		return "", nil, false
	}
	return body.Key, value, true
}

func (h *SettingsHandler) SetGlobalSetting(w http.ResponseWriter, r *http.Request) {
	key, value, ok := decodeSetting(w, r)
	if !ok {
		return
	}
	if _, err := h.svc.Update(r.Context(), "", &openshell.ConfigUpdate{
		SettingKey:   key,
		SettingValue: value,
		Global:       true,
	}); err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// DeleteGlobalSetting removes one gateway-global setting. The answer is the
// gateway's own: deleted is false when the key had no global value, so there
// was nothing to delete, and settingsRevision is the global settings revision
// afterwards, which such a delete leaves where it was.
func (h *SettingsHandler) DeleteGlobalSetting(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidSetting, "key query parameter is required")
		return
	}
	res, err := h.svc.Update(r.Context(), "", &openshell.ConfigUpdate{
		SettingKey:    key,
		DeleteSetting: true,
		Global:        true,
	})
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	var out models.SettingDeleteResult
	if res != nil {
		out.Deleted = res.Deleted
		out.SettingsRevision = res.SettingsRevision
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

// GetSandboxSettings returns the settings in effect for one sandbox, each with
// the scope its value comes from, and what the gateway reports about the
// sandbox's configuration (`openshell settings get <sandbox>`).
func (h *SettingsHandler) GetSandboxSettings(w http.ResponseWriter, r *http.Request) {
	config, err := h.svc.GetSandbox(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKSandboxSettings(config))
}

// SetSandboxSetting sets one setting on a sandbox. The body and its typed
// value are those of SetGlobalSetting.
//
// The gateway's scope wins over the sandbox's: it refuses the write while the
// key is set globally, and that refusal is passed on with the gateway's own
// message.
func (h *SettingsHandler) SetSandboxSetting(w http.ResponseWriter, r *http.Request) {
	key, value, ok := decodeSetting(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Update(r.Context(), r.PathValue("workspace"), &openshell.ConfigUpdate{
		Name:         r.PathValue("name"),
		SettingKey:   key,
		SettingValue: value,
	})
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	out := models.SettingSetResult{Updated: true}
	if res != nil {
		out.SettingsRevision = res.SettingsRevision
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

// DeleteSandboxSetting removes one setting from a sandbox, which then follows
// the gateway's default for it again. Like a write it is refused while the key
// is set globally.
func (h *SettingsHandler) DeleteSandboxSetting(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidSetting, "key query parameter is required")
		return
	}
	res, err := h.svc.Update(r.Context(), r.PathValue("workspace"), &openshell.ConfigUpdate{
		Name:          r.PathValue("name"),
		SettingKey:    key,
		DeleteSetting: true,
	})
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	var out models.SettingDeleteResult
	if res != nil {
		out.Deleted = res.Deleted
		out.SettingsRevision = res.SettingsRevision
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}
