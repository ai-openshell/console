// Package server wires the BFF REST API consumed by the React frontend.
package server

import (
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/clients"
	"github.com/ai-openshell/console/backend/pkg/handlers"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// App wires the OpenShell SDK client, auth middleware, and REST routes.
type App struct { //nolint:govet // fieldalignment: readability over padding
	// authConfig is serialized to the browser via GET /auth/config — never
	// put secrets in it.
	auth          auth.MiddlewareInterface
	authConfig    models.AuthConfigResponse
	staticDir     string
	maxUploadSize int64
	execTimeout   uint32

	allWorkspaces *handlers.AllWorkspacesHandler

	drafts     *handlers.DraftsHandler
	files      *handlers.FilesHandler
	gateway    *handlers.GatewayHandler
	logs       *handlers.LogsHandler
	policies   *handlers.PoliciesHandler
	providers  *handlers.ProvidersHandler
	sandboxes  *handlers.SandboxHandler
	services   *handlers.ServicesHandler
	settings   *handlers.SettingsHandler
	terminal   *handlers.TerminalHandler
	templates  *handlers.TemplatesHandler
	workspaces *handlers.WorkspacesHandler

	extensions []Extension
}

// NewApp builds the application.
func NewApp(
	sdkClient openshell.ClientInterface,
	exec clients.RawExecClientInterface,
	authMiddleware auth.MiddlewareInterface,
	staticDir string,
	authCfg models.AuthConfigResponse,
	extensions ...Extension,
) *App {
	app := &App{
		auth:          authMiddleware,
		authConfig:    authCfg,
		staticDir:     staticDir,
		maxUploadSize: 64 << 20,
		execTimeout:   30,
	}

	sandboxSvc := services.NewSandboxService(sdkClient.Sandboxes(), exec)
	configSvc := services.NewConfig(sdkClient.Config())
	execSvc := services.NewExecService(sdkClient.Exec())
	policySvc := services.NewPolicyService(sdkClient.Policy())

	app.drafts = handlers.NewDraftsHandler(policySvc)
	app.drafts.SetSandboxService(sandboxSvc)
	app.files = handlers.NewFilesHandler(services.NewFileService(exec), execSvc, sandboxSvc, handlers.FilesHandlerConfig{ExecTimeout: app.execTimeout, MaxUploadSize: app.maxUploadSize})
	app.gateway = handlers.NewGatewayHandler(services.NewGatewayService(sdkClient), authMiddleware, authCfg)
	app.logs = handlers.NewLogsHandler(sandboxSvc)
	app.policies = handlers.NewPoliciesHandler(policySvc, configSvc)
	app.providers = handlers.NewProvidersHandler(services.NewProviderService(sdkClient.Providers()))
	app.sandboxes = handlers.NewSandboxHandler(sandboxSvc)
	app.services = handlers.NewServicesHandler(services.NewServiceService(sdkClient.Services()))
	app.settings = handlers.NewSettingsHandler(configSvc)
	app.terminal = handlers.NewTerminalHandler(execSvc)
	app.templates = handlers.NewTemplatesHandler(services.NewTemplateService(sdkClient))
	app.workspaces = handlers.NewWorkspacesHandler(services.NewWorkspaceService(sdkClient.Workspaces()))
	app.allWorkspaces = handlers.NewAllWorkspacesHandler(
		sandboxSvc,
		services.NewProviderService(sdkClient.Providers()),
		services.NewTemplateService(sdkClient),
		services.NewServiceService(sdkClient.Services()),
	)
	app.extensions = extensions

	return app
}

// SetGatewayReleaseLine replaces the gateway release line this build is for.
// GET /gateway/compatibility (for every signed-in user) and GET /gateway (for
// platform admins) report whether the connected gateway is on it. An App that
// never calls this uses the line compiled into the build
// (models.BuiltInGatewayReleaseLine); given the zero value, both routes report
// "unknown". Nothing is ever blocked on the result.
//
// It is a method rather than a NewApp parameter because NewApp's trailing
// parameter is already the variadic extension list. Call it before Routes.
func (app *App) SetGatewayReleaseLine(line models.GatewayReleaseLine) {
	app.gateway.SetGatewayReleaseLine(line)
}

// SetProviderCredentialKeys gives the app the way to read which credentials
// each provider holds, so that provider lists and details can name them. The
// gateway reports those keys and the SDK drops them; clients.RawExecClient
// reads them from the gateway's own answer. Without it providers are returned
// with no credential names against gateways 0.1.x.
//
// Like SetGatewayReleaseLine it is a method rather than a NewApp parameter.
// Call it before Routes.
func (app *App) SetProviderCredentialKeys(keys services.ProviderCredentialKeyReader) {
	app.providers.SetCredentialKeyReader(keys)
	app.logs.SetCredentialKeyReader(keys)
	app.allWorkspaces.SetCredentialKeyReader(keys)
}

// SetProviderProfiles gives the app the way to read and write provider
// profiles with their endpoints whole: the access preset, enforcement and L7
// rules that bound a provider's traffic. The SDK carries a host, a port and a
// protocol of each endpoint; clients.RawExecClient uses the gateway's own
// message. Without it profiles are returned without networkEndpoints, a
// profile that sets more on an endpoint cannot be imported, and an update
// drops whatever the stored profile's endpoints held beyond those three
// fields.
//
// Like SetGatewayReleaseLine it is a method rather than a NewApp parameter.
// Call it before Routes.
func (app *App) SetProviderProfiles(profiles services.ProviderProfileStore) {
	app.providers.SetProfileStore(profiles)
}

// Routes builds the chi router.
func (app *App) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		// Public: frontend bootstrap config, no token needed.
		r.Get("/auth/config", app.GetAuthConfig)
		// BFF liveness (does not call the gateway).
		r.Get("/healthz", app.GetHealthz)
		r.Get("/readyz", app.gateway.GetReadyz)

		r.Group(func(r chi.Router) {
			r.Use(app.auth.Handler)

			r.Get("/auth/whoami", app.gateway.GetWhoAmI)
			r.Get("/gateway", app.gateway.GetGateway)
			// The verdict on its own: /gateway carries it too, but the
			// gateway only answers that one for platform admins.
			r.Get("/gateway/compatibility", app.gateway.GetGatewayCompatibility)
			// Pending draft chunks per sandbox, of the workspace named in
			// ?workspace=. Without one the summary is empty.
			r.Get("/draft-summary", app.drafts.GetDraftSummary)

			r.Route("/global-policy", func(r chi.Router) {
				r.Get("/", app.policies.GetGlobalPolicy)
				r.Put("/", app.policies.SetGlobalPolicy)
				r.Get("/revisions/{version}", app.policies.GetGlobalPolicyRevision)
			})

			r.Get("/settings/global", app.settings.GetGlobalSettings)
			r.Put("/settings/global", app.settings.SetGlobalSetting)
			r.Delete("/settings/global", app.settings.DeleteGlobalSetting)
			r.Delete("/global-policy", app.policies.DeleteGlobalPolicy)

			// Lists across every workspace (the CLI's --all-workspaces). The
			// gateway answers them for platform admins only.
			r.Get("/sandboxes", app.allWorkspaces.ListSandboxes)
			r.Get("/providers", app.allWorkspaces.ListProviders)
			r.Get("/templates", app.allWorkspaces.ListSandboxTemplates)
			r.Get("/services", app.allWorkspaces.ListServices)

			// Platform-scoped provider profiles, which every workspace sees.
			// The handlers are the ones that serve a workspace's own profiles
			// below, told to address the platform scope; the gateway answers
			// these for platform admins only.
			platform := app.providers.InPlatformScope
			r.Get("/provider-profiles", platform(app.providers.ListProviderProfiles))
			r.Post("/provider-profiles", platform(app.providers.ImportProviderProfiles))
			r.Post("/provider-profiles/lint", platform(app.providers.LintProviderProfiles))
			r.Get("/provider-profiles/{profileId}", platform(app.providers.GetProviderProfile))
			r.Put("/provider-profiles/{profileId}", platform(app.providers.UpdateProviderProfile))
			r.Delete("/provider-profiles/{profileId}", platform(app.providers.DeleteProviderProfile))

			r.Route("/workspaces", func(r chi.Router) {
				r.Get("/", app.workspaces.ListWorkspaces)
				r.Post("/", app.workspaces.CreateWorkspace)
				r.Route("/{workspace}", func(r chi.Router) {
					r.Get("/", app.workspaces.GetWorkspace)
					r.Delete("/", app.workspaces.DeleteWorkspace)

					r.Get("/members", app.workspaces.ListMembers)
					r.Post("/members", app.workspaces.AddMember)
					r.Delete("/members/{subject}", app.workspaces.RemoveMember)

					r.Get("/templates", app.templates.ListSandboxTemplates)
					r.Post("/templates", app.templates.CreateSandboxTemplate)
					r.Get("/templates/{name}", app.templates.GetSandboxTemplate)
					r.Delete("/templates/{name}", app.templates.DeleteSandboxTemplate)

					r.Get("/draft-summary", app.drafts.GetWorkspaceDraftSummary)

					r.Get("/sandboxes", app.sandboxes.ListSandboxes)
					r.Post("/sandboxes", app.sandboxes.CreateSandbox)
					r.Post("/sandboxes/from-template", app.templates.CreateSandboxFromTemplate)
					r.Get("/sandboxes/{name}", app.sandboxes.GetSandbox)
					r.Delete("/sandboxes/{name}", app.sandboxes.DeleteSandbox)
					r.Post("/sandboxes/{name}/stop", app.sandboxes.StopSandbox)
					r.Post("/sandboxes/{name}/start", app.sandboxes.StartSandbox)
					r.Get("/sandboxes/{name}/settings", app.settings.GetSandboxSettings)
					r.Put("/sandboxes/{name}/settings", app.settings.SetSandboxSetting)
					r.Delete("/sandboxes/{name}/settings", app.settings.DeleteSandboxSetting)
					r.Get("/sandboxes/{name}/logs/stream", app.logs.StreamSandboxLogs)
					r.Get("/sandboxes/{name}/logs", app.logs.GetSandboxLogs)
					r.Get("/sandboxes/{name}/terminal", app.terminal.Terminal)
					r.Get("/sandboxes/{name}/providers", app.logs.ListSandboxProviders)
					r.Post("/sandboxes/{name}/providers/{provider}", app.logs.AttachSandboxProvider)
					r.Delete("/sandboxes/{name}/providers/{provider}", app.logs.DetachSandboxProvider)
					r.Get("/sandboxes/{name}/policy", app.policies.GetSandboxPolicy)
					r.Put("/sandboxes/{name}/policy", app.policies.UpdateSandboxPolicy)
					r.Post("/sandboxes/{name}/policy/merge", app.policies.MergeSandboxPolicy)
					r.Get("/sandboxes/{name}/policy/effective", app.policies.GetEffectiveSandboxPolicy)
					r.Get("/sandboxes/{name}/policy/revisions/{version}", app.policies.GetSandboxPolicyRevision)
					r.Get("/sandboxes/{name}/drafts", app.drafts.GetDraftPolicy)
					r.Post("/sandboxes/{name}/drafts/{chunk}/approve", app.drafts.ApproveDraftChunk)
					r.Post("/sandboxes/{name}/drafts/{chunk}/reject", app.drafts.RejectDraftChunk)
					r.Post("/sandboxes/{name}/drafts/approve-all", app.drafts.ApproveAllDraftChunks)
					r.Put("/sandboxes/{name}/drafts/{chunk}", app.drafts.EditDraftChunk)
					r.Post("/sandboxes/{name}/drafts/{chunk}/undo", app.drafts.UndoDraftChunk)
					r.Post("/sandboxes/{name}/drafts/clear", app.drafts.ClearDraftChunks)
					r.Get("/sandboxes/{name}/drafts/history", app.drafts.GetDraftHistory)
					r.Post("/sandboxes/{name}/files", app.files.UploadFile)
					r.Get("/sandboxes/{name}/files", app.files.DownloadFile)

					r.Get("/sandboxes/{name}/services", app.services.ListServices)
					r.Post("/sandboxes/{name}/services", app.services.ExposeService)
					r.Delete("/sandboxes/{name}/services/{svc}", app.services.DeleteService)
					// Without a service name: the sandbox's unnamed endpoint.
					r.Delete("/sandboxes/{name}/services", app.services.DeleteService)
					// Without a sandbox name: every endpoint in the workspace.
					r.Get("/services", app.services.ListServices)

					r.Get("/providers", app.providers.ListProviders)
					r.Post("/providers", app.providers.CreateProvider)
					r.Get("/providers/{name}", app.providers.GetProvider)
					r.Put("/providers/{name}", app.providers.UpdateProvider)
					r.Delete("/providers/{name}", app.providers.DeleteProvider)
					r.Get("/providers/{name}/refresh-status", app.providers.GetProviderRefreshStatus)
					r.Post("/providers/{name}/refresh", app.providers.ConfigureProviderRefresh)
					r.Post("/providers/{name}/refresh/rotate", app.providers.RotateProviderCredential)
					r.Delete("/providers/{name}/refresh", app.providers.DeleteProviderRefresh)

					r.Get("/provider-profiles", app.providers.ListProviderProfiles)
					r.Post("/provider-profiles", app.providers.ImportProviderProfiles)
					r.Post("/provider-profiles/lint", app.providers.LintProviderProfiles)
					r.Get("/provider-profiles/{profileId}", app.providers.GetProviderProfile)
					r.Put("/provider-profiles/{profileId}", app.providers.UpdateProviderProfile)
					r.Delete("/provider-profiles/{profileId}", app.providers.DeleteProviderProfile)
				})
			})

			for _, extension := range app.extensions {
				extension(r)
			}
		})
	})

	if app.staticDir != "" {
		r.NotFound(app.serveStatic)
	}

	return r
}

// serveStatic serves the built frontend with SPA fallback to index.html.
func (app *App) serveStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		apiutils.WriteError(w, http.StatusNotFound, apiutils.NotFound, "unknown API route")
		return
	}
	root, err := os.OpenRoot(app.staticDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = root.Close() }()

	// Root also rejects symlinks that escape the frontend directory.
	requested := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if info, err := root.Stat(requested); err == nil && !info.IsDir() {
		http.FileServerFS(root.FS()).ServeHTTP(w, r)
		return
	}
	http.ServeFileFS(w, r, root.FS(), "index.html")
}

// GetHealthz reports BFF liveness without touching the gateway.
func (app *App) GetHealthz(w http.ResponseWriter, _ *http.Request) {
	apiutils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetAuthConfig is public — the frontend needs it before any auth exists.
func (app *App) GetAuthConfig(w http.ResponseWriter, _ *http.Request) {
	apiutils.WriteJSON(w, http.StatusOK, app.authConfig)
}
