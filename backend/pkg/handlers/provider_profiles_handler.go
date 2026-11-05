package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
)

// The provider profile routes, which ProvidersHandler serves in two scopes.
//
// Under /workspaces/{workspace}/provider-profiles the scope is that workspace.
// Under /provider-profiles, registered through InPlatformScope, it is the
// platform, whose profiles every workspace sees: the CLI's --global. To the
// gateway and the SDK the platform scope is the empty workspace. Both sets of
// routes run the methods below; what a caller may do in either scope is the
// gateway's decision, and its refusal is passed on.

type platformScopeKey struct{}

// InPlatformScope makes a profile route address the platform scope instead of
// the workspace in its path.
//
// The scope is named this way, by the route, and not read from the absence of
// a workspace in the path: the router matches an empty path segment, so
// /workspaces//provider-profiles would otherwise be a way to write a platform
// profile, which every workspace gets, with a request meant for one.
func (h *ProvidersHandler) InPlatformScope(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(context.WithValue(r.Context(), platformScopeKey{}, true)))
	}
}

// profileScope is the scope a profile route addresses: the platform scope,
// which is the empty workspace, for a route registered through
// InPlatformScope, and the workspace in the path for any other. It answers for
// a path that names no workspace and reports false.
func profileScope(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Context().Value(platformScopeKey{}) != nil {
		return "", true
	}
	workspace := r.PathValue("workspace")
	if workspace == "" {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidName, "workspace is required")
		return "", false
	}
	return workspace, true
}

// writeProfileError answers for a failed profile call. An endpoint the store
// cannot send is the request's to fix, or the deployment's; everything else is
// the gateway's answer.
func writeProfileError(w http.ResponseWriter, err error) {
	if errors.Is(err, models.ErrEndpointNotExpressible) {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidProfile, err.Error())
		return
	}
	// Profiles are read and written on the generated client, whose errors
	// are bare gRPC statuses. A failed precondition (a workspace that is being
	// deleted, say) is answered as a conflict, as the SDK's typed error for it
	// is; apiutils.WriteSDKError would make a bare one a bad request.
	if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition {
		slog.Warn("gateway error", "code", st.Code().String(), "message", st.Message())
		apiutils.WriteError(w, http.StatusConflict, apiutils.Conflict, st.Message())
		return
	}
	apiutils.WriteSDKError(w, err)
}

// writeProfileConversionError answers for a profile the gateway returned and
// the BFF could not render, which is not something a client can act on.
func writeProfileConversionError(w http.ResponseWriter, err error) {
	slog.Error("render provider profile", "error", err)
	apiutils.WriteError(w, http.StatusInternalServerError, apiutils.Internal, "internal error")
}

// ListProviderProfiles returns the provider type profiles visible in the
// scope. A workspace's list holds its own profiles and the platform's, each
// naming its scope; the credential schemas drive the Add Provider form.
func (h *ProvidersHandler) ListProviderProfiles(w http.ResponseWriter, r *http.Request) {
	scope, ok := profileScope(w, r)
	if !ok {
		return
	}
	store, narrow := h.profileStore()
	profiles, err := store.ListProviderProfiles(r.Context(), scope)
	if err != nil {
		writeProfileError(w, err)
		return
	}
	out, err := models.ProviderProfilesFromProto(profiles, narrow)
	if err != nil {
		writeProfileConversionError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

func (h *ProvidersHandler) GetProviderProfile(w http.ResponseWriter, r *http.Request) {
	scope, ok := profileScope(w, r)
	if !ok {
		return
	}
	store, narrow := h.profileStore()
	profile, err := store.GetProviderProfile(r.Context(), scope, r.PathValue("profileId"))
	if err != nil {
		writeProfileError(w, err)
		return
	}
	out, err := models.ProviderProfileFromProto(profile, narrow)
	if err != nil {
		writeProfileConversionError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, out)
}

func (h *ProvidersHandler) ImportProviderProfiles(w http.ResponseWriter, r *http.Request) {
	scope, ok := profileScope(w, r)
	if !ok {
		return
	}
	var body ImportProviderProfilesBody
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	if len(body.Profiles) == 0 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidRequest, "at least one profile is required")
		return
	}
	for _, p := range body.Profiles {
		if p.ID == "" || p.DisplayName == "" {
			apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidProfile, "id and displayName are required")
			return
		}
	}
	items, err := models.ProfileImportItems(body.Profiles)
	if err != nil {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidProfile, err.Error())
		return
	}
	store, narrow := h.profileStore()
	resp, err := store.ImportProviderProfiles(r.Context(), scope, items)
	if err != nil {
		writeProfileError(w, err)
		return
	}
	profiles, err := models.ProviderProfilesFromProto(resp.GetProfiles(), narrow)
	if err != nil {
		writeProfileConversionError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusCreated, models.ImportProviderProfilesResult{
		Diagnostics: models.FromProtoDiagnostics(resp.GetDiagnostics()),
		Profiles:    profiles,
		Imported:    resp.GetImported(),
	})
}

type UpdateProviderProfileBody struct {
	Profile                 ImportProfileBody `json:"profile"`
	ExpectedResourceVersion uint64            `json:"expectedResourceVersion,omitempty"`
}

// UpdateProviderProfile replaces the stored profile with the one in the body.
// The gateway does not merge: a field the body leaves out is removed from the
// profile, so the body is the whole profile as it should be afterwards.
func (h *ProvidersHandler) UpdateProviderProfile(w http.ResponseWriter, r *http.Request) {
	scope, ok := profileScope(w, r)
	if !ok {
		return
	}
	var body UpdateProviderProfileBody
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	profileID := r.PathValue("profileId")
	if body.Profile.ID != "" && body.Profile.ID != profileID {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.IDMismatch, "profile id in body must match URL")
		return
	}
	body.Profile.ID = profileID
	items, err := models.ProfileImportItems([]ImportProfileBody{body.Profile})
	if err != nil {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidProfile, err.Error())
		return
	}
	store, narrow := h.profileStore()
	resp, err := store.UpdateProviderProfile(r.Context(), scope, profileID, body.ExpectedResourceVersion, items[0])
	if err != nil {
		writeProfileError(w, err)
		return
	}
	result := models.UpdateProviderProfileResult{
		Diagnostics: models.FromProtoDiagnostics(resp.GetDiagnostics()),
		Updated:     resp.GetUpdated(),
	}
	if resp.GetProfile() != nil {
		profile, convErr := models.ProviderProfileFromProto(resp.GetProfile(), narrow)
		if convErr != nil {
			writeProfileConversionError(w, convErr)
			return
		}
		result.Profile = &profile
	}
	apiutils.WriteJSON(w, http.StatusOK, result)
}

func (h *ProvidersHandler) DeleteProviderProfile(w http.ResponseWriter, r *http.Request) {
	scope, ok := profileScope(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Profiles().Delete(r.Context(), scope, r.PathValue("profileId"))
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.FromSDKDeletion(res))
}

type LintProviderProfilesBody struct {
	Profiles []ImportProfileBody `json:"profiles"`
}

func (h *ProvidersHandler) LintProviderProfiles(w http.ResponseWriter, r *http.Request) {
	scope, ok := profileScope(w, r)
	if !ok {
		return
	}
	var body LintProviderProfilesBody
	if !apiutils.DecodeBody(w, r, &body) {
		return
	}
	items, err := models.ProfileImportItems(body.Profiles)
	if err != nil {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidProfile, err.Error())
		return
	}
	store, _ := h.profileStore()
	resp, err := store.LintProviderProfiles(r.Context(), scope, items)
	if err != nil {
		writeProfileError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.LintProviderProfilesResult{
		Diagnostics: models.FromProtoDiagnostics(resp.GetDiagnostics()),
		Valid:       resp.GetValid(),
	})
}
