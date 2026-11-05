package handlers

import (
	"context"
	"net/http"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

type GatewayHandler struct {
	svc        services.GatewayServiceInterface
	auth       auth.MiddlewareInterface
	authConfig models.AuthConfigResponse
	// line is the gateway release line this build is for. The zero value means
	// it was replaced by one that cannot be read, and every gateway is then
	// reported as "unknown".
	line models.GatewayReleaseLine
}

// NewGatewayHandler returns a handler that judges gateways against the release
// line compiled into this build (models.BuiltInGatewayReleaseLine) until
// SetGatewayReleaseLine replaces it.
func NewGatewayHandler(svc services.GatewayServiceInterface, authMiddleware auth.MiddlewareInterface, authConfig models.AuthConfigResponse) *GatewayHandler {
	// The constant always parses: its unit test holds it to that. If it ever
	// did not, the zero value reports "unknown", never a wrong verdict.
	line, _ := models.ParseGatewayReleaseLine(models.BuiltInGatewayReleaseLine)
	return &GatewayHandler{svc: svc, auth: authMiddleware, authConfig: authConfig, line: line}
}

// SetGatewayReleaseLine replaces the built-in release line that GetGateway and
// GetGatewayCompatibility compare the gateway's reported version against. It
// is a setter, not a NewGatewayHandler parameter: a handler it was never
// called on judges against the built-in line. Call it before the handler
// serves requests.
func (h *GatewayHandler) SetGatewayReleaseLine(line models.GatewayReleaseLine) {
	h.line = line
}

// GetGateway returns gateway status, version, and compute drivers, plus the
// dashboard's own verdict on whether that version is one it supports.
//
// The verdict only informs. A gateway on another release line is still served
// in full — the verdict blocks nothing (ADR 0005, decision 9) — so the UI can
// explain the errors a mismatched gateway produces instead of leaving them
// unexplained.
//
// A gateway that enforces roles answers GetGatewayInfo only for platform
// admins, so there this route is a 403 for everyone else.
// GetGatewayCompatibility is where a caller without that role gets the
// verdict.
func (h *GatewayHandler) GetGateway(w http.ResponseWriter, r *http.Request) {
	info, err := h.svc.GetGatewayInfo(r.Context())
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	if info == nil {
		apiutils.WriteJSON(w, http.StatusOK, info)
		return
	}
	// Judged here rather than in the service: the service reports what the
	// gateway said, and the handler holds the release line. Copy first: the
	// service owns the value it returned.
	out := *info
	compatibility := h.line.Check(info.GatewayVersion)
	out.Compatibility = &compatibility
	apiutils.WriteJSON(w, http.StatusOK, out)
}

// GetGatewayCompatibility returns the gateway's version, whether the gateway
// calls itself healthy, and the dashboard's verdict on the version, to any
// signed-in caller.
//
// A gateway on another release line breaks every user's pages, not only an
// admin's, so the explanation must not depend on a role. Neither the version
// nor the health is privileged: the gateway's health check hands both to
// anyone, without a token. This route therefore reads them from the health
// check instead of from GetGatewayInfo, and makes no authorization decision of
// its own.
//
// Like GetGateway it only informs. A gateway that cannot be reached is relayed
// as the error it is, never dressed up as a verdict or as an unhealthy
// gateway: "did not answer" and "answered that it is not healthy" are
// different findings and stay different responses.
func (h *GatewayHandler) GetGatewayCompatibility(w http.ResponseWriter, r *http.Request) {
	version, healthy, err := h.gatewayHealth(r.Context())
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, models.GatewayCompatibilityInfo{
		GatewayVersion: version,
		Healthy:        healthy,
		Compatibility:  h.line.Check(version),
	})
}

// gatewayHealth reads the gateway's version and health the way the fewest
// callers are refused: through the health check when the service offers it,
// and through GetGatewayInfo — admins only — when it does not. healthy is nil
// when the source reports no health: a service that offers only the version,
// or one that answers with nothing.
//
// services.GatewayService answers both from one health check. Any other type
// with both methods is asked for the version through GetGatewayVersion, and
// only its health is taken from GetGatewayHealth: a type that embeds
// GatewayService and overrides GetGatewayVersion inherits a GetGatewayHealth
// that does not know of the override.
func (h *GatewayHandler) gatewayHealth(ctx context.Context) (string, *bool, error) {
	versionReader, readsVersion := h.svc.(services.GatewayVersionReader)
	healthReader, readsHealth := h.svc.(services.GatewayHealthReader)
	if readsHealth {
		health, err := healthReader.GetGatewayHealth(ctx)
		if err != nil || health == nil {
			return "", nil, err
		}
		version := health.Version
		if _, upstream := h.svc.(*services.GatewayService); !upstream && readsVersion {
			if version, err = versionReader.GetGatewayVersion(ctx); err != nil {
				return "", nil, err
			}
		}
		return version, &health.Healthy, nil
	}
	if readsVersion {
		version, err := versionReader.GetGatewayVersion(ctx)
		return version, nil, err
	}
	info, err := h.svc.GetGatewayInfo(ctx)
	if err != nil || info == nil {
		return "", nil, err
	}
	// The health check counts a gateway healthy when its status is exactly
	// that, and so does this. A service that reports no status reports no
	// health.
	var healthy *bool
	if info.Status != "" {
		isHealthy := info.Status == models.GatewayStatusHealthy
		healthy = &isHealthy
	}
	return info.GatewayVersion, healthy, nil
}

// GetReadyz checks gateway reachability for readiness probes.
func (h *GatewayHandler) GetReadyz(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.CheckHealth(r.Context()); err != nil {
		apiutils.WriteError(w, http.StatusServiceUnavailable, apiutils.NotReady, "gateway unreachable")
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// GetWhoAmI returns the gateway-validated identity or its error.
func (h *GatewayHandler) GetWhoAmI(w http.ResponseWriter, r *http.Request) {
	if h.auth.Disabled() {
		apiutils.WriteJSON(w, http.StatusOK, models.CurrentUser{
			Subject:     "dev-user",
			DisplayName: "Development User",
			Roles:       []string{h.authConfig.AdminRole},
		})
		return
	}

	user, err := h.svc.GetCurrentUser(r.Context())
	if err != nil {
		apiutils.WriteSDKError(w, err)
		return
	}
	apiutils.WriteJSON(w, http.StatusOK, user)
}
