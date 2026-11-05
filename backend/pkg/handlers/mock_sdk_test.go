package handlers

import (
	"context"
	"io"
	"sync"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
	pb "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"
	"google.golang.org/grpc"
)

// mockSDK is a mock of openshell.ClientInterface for testing.
type mockSDK struct {
	sandboxes  mockSDKSandboxes
	templates  mockSDKTemplates
	providers  mockSDKProviders
	workspaces mockSDKWorkspaces
	policy     mockSDKPolicy
	config     mockSDKConfig
	services   mockSDKServices
	health     mockSDKHealth
	exec       mockSDKExec
	files      mockSDKFiles
}

func (m *mockSDK) Sandboxes() openshell.SandboxInterface  { return &m.sandboxes }
func (m *mockSDK) Providers() openshell.ProviderInterface { return &m.providers }
func (m *mockSDK) Services() openshell.ServiceInterface   { return &m.services }
func (m *mockSDK) Exec() openshell.ExecInterface          { return &m.exec }
func (m *mockSDK) Files() openshell.FileInterface         { return &m.files }
func (m *mockSDK) Health() openshell.HealthInterface      { return &m.health }
func (m *mockSDK) SandboxSuspendResume() openshell.SandboxSuspendResumeInterface {
	panic("not implemented")
}
func (m *mockSDK) SSH() openshell.SSHInterface              { panic("not implemented") }
func (m *mockSDK) TCP() openshell.TCPInterface              { panic("not implemented") }
func (m *mockSDK) Config() openshell.ConfigInterface        { return &m.config }
func (m *mockSDK) Policy() openshell.PolicyInterface        { return &m.policy }
func (m *mockSDK) Workspaces() openshell.WorkspaceInterface { return &m.workspaces }
func (m *mockSDK) Close() error                             { return nil }

func mockDeletionResult(deleted bool) *openshell.DeletionResult {
	outcome := openshell.DeletionAlreadyAbsent
	if deleted {
		outcome = openshell.DeletionCompleted
	}
	return &openshell.DeletionResult{Outcome: outcome}
}

func (m *mockSDK) SandboxTemplates() openshell.SandboxTemplateInterface { return &m.templates }

func (m *mockSDK) CreateSandboxFromTemplate(
	ctx context.Context,
	workspace, name, templateName string,
	spec *openshell.SandboxSpec,
	opts ...openshell.CreateOption,
) (*openshell.Sandbox, error) {
	if m.templates.createFromTemplateFn != nil {
		return m.templates.createFromTemplateFn(ctx, workspace, name, templateName, spec, opts...)
	}
	return &openshell.Sandbox{Name: name}, nil
}

// mockSDKTemplates provides injectable reusable-template operations plus the
// top-level CreateSandboxFromTemplate hook.
type mockSDKTemplates struct {
	listFn               func(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.SandboxWorkloadTemplate, error)
	createFn             func(ctx context.Context, workspace string, template *openshell.SandboxWorkloadTemplate) (*openshell.SandboxWorkloadTemplate, error)
	getFn                func(ctx context.Context, workspace, name string) (*openshell.SandboxWorkloadTemplate, error)
	deleteFn             func(ctx context.Context, workspace, name string) (bool, error)
	createFromTemplateFn func(ctx context.Context, workspace, name, templateName string, spec *openshell.SandboxSpec, opts ...openshell.CreateOption) (*openshell.Sandbox, error)
}

func (m *mockSDKTemplates) List(workspace string, opts ...openshell.ListOptions) (*openshell.Pager[*openshell.SandboxWorkloadTemplate], error) {
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.SandboxWorkloadTemplate], error) {
		items, err := m.ListAll(ctx, workspace, opts...)
		return &openshell.Page[*openshell.SandboxWorkloadTemplate]{Items: items}, err
	}), nil
}

func (m *mockSDKTemplates) ListAll(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.SandboxWorkloadTemplate, error) {
	if m.listFn != nil {
		return m.listFn(ctx, workspace, opts...)
	}
	return nil, nil
}

func (m *mockSDKTemplates) Create(ctx context.Context, workspace string, template *openshell.SandboxWorkloadTemplate) (*openshell.SandboxWorkloadTemplate, error) {
	if m.createFn != nil {
		return m.createFn(ctx, workspace, template)
	}
	if template != nil {
		return template, nil
	}
	return &openshell.SandboxWorkloadTemplate{}, nil
}

func (m *mockSDKTemplates) Get(ctx context.Context, workspace, name string) (*openshell.SandboxWorkloadTemplate, error) {
	if m.getFn != nil {
		return m.getFn(ctx, workspace, name)
	}
	return &openshell.SandboxWorkloadTemplate{Name: name}, nil
}

func (m *mockSDKTemplates) Delete(ctx context.Context, workspace, name string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	deleted := true
	var err error
	if m.deleteFn != nil {
		deleted, err = m.deleteFn(ctx, workspace, name)
	}
	return mockDeletionResult(deleted), err
}

// sentCreate is what a sandbox create sent besides its spec, read back from
// its options the way the SDK reads them.
type sentCreate struct {
	labels           map[string]string
	annotations      map[string]string
	serviceExposures []openshell.ServiceExposure
}

func readCreateOptions(opts []openshell.CreateOption) sentCreate {
	cfg := types.ApplyCreateOptions(opts)
	return sentCreate{labels: cfg.Labels(), annotations: cfg.Annotations(), serviceExposures: cfg.ServiceExposures()}
}

// mockSDKSandboxes provides injectable sandbox operations.
type mockSDKSandboxes struct {
	listFn          func(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Sandbox, error)
	listPageFn      func(ctx context.Context, workspace string, opts ...openshell.ListOptions) (*openshell.Page[*openshell.Sandbox], error)
	createFn        func(ctx context.Context, workspace, name string, spec *openshell.SandboxSpec, opts ...openshell.CreateOption) (*openshell.Sandbox, error)
	getFn           func(ctx context.Context, workspace, name string) (*openshell.Sandbox, error)
	deleteFn        func(ctx context.Context, workspace, name string) error
	attachFn        func(ctx context.Context, workspace, sandboxName, providerName string, expectedResourceVersion uint64) (*openshell.AttachProviderResult, error)
	detachFn        func(ctx context.Context, workspace, sandboxName, providerName string, expectedResourceVersion uint64) (*openshell.DetachProviderResult, error)
	listProvidersFn func(ctx context.Context, workspace, sandboxName string) ([]*openshell.Provider, error)
	getLogsFn       func(ctx context.Context, workspace, name string, opts ...openshell.LogOption) (*openshell.LogResult, error)
	stopFn          func(ctx context.Context, workspace, name string) (*openshell.Sandbox, error)
	startFn         func(ctx context.Context, workspace, name string) (*openshell.Sandbox, error)
}

// mockSandboxRawExec provides the raw WatchSandbox call used for live logs.
type mockSandboxRawExec struct {
	watchLogsFn func(ctx context.Context, req *pb.WatchSandboxRequest) (grpc.ServerStreamingClient[pb.SandboxStreamEvent], error)
}

func (m *mockSandboxRawExec) WatchSandbox(ctx context.Context, req *pb.WatchSandboxRequest) (grpc.ServerStreamingClient[pb.SandboxStreamEvent], error) {
	return m.watchLogsFn(ctx, req)
}

type mockServerStream struct {
	grpc.ClientStream
	err    error
	events []*pb.SandboxStreamEvent
}

func (m *mockServerStream) Recv() (*pb.SandboxStreamEvent, error) {
	if len(m.events) > 0 {
		event := m.events[0]
		m.events = m.events[1:]
		return event, nil
	}
	if m.err != nil {
		return nil, m.err
	}
	return nil, io.EOF
}

func (m *mockSDKSandboxes) List(workspace string, opts ...openshell.ListOptions) (*openshell.Pager[*openshell.Sandbox], error) {
	if m.listPageFn != nil {
		return openshell.NewPager(opts[0].PageToken, func(ctx context.Context, _ string) (*openshell.Page[*openshell.Sandbox], error) {
			return m.listPageFn(ctx, workspace, opts...)
		}), nil
	}
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.Sandbox], error) {
		items, err := m.ListAll(ctx, workspace, opts...)
		return &openshell.Page[*openshell.Sandbox]{Items: items}, err
	}), nil
}

func (m *mockSDKSandboxes) ListAll(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
	if m.listFn != nil {
		return m.listFn(ctx, workspace, opts...)
	}
	return nil, nil
}

func (m *mockSDKSandboxes) Create(ctx context.Context, workspace, name string, spec *openshell.SandboxSpec, opts ...openshell.CreateOption) (*openshell.Sandbox, error) {
	if m.createFn != nil {
		return m.createFn(ctx, workspace, name, spec, opts...)
	}
	return &openshell.Sandbox{Name: name}, nil
}

func (m *mockSDKSandboxes) Get(ctx context.Context, workspace, name string) (*openshell.Sandbox, error) {
	if m.getFn != nil {
		return m.getFn(ctx, workspace, name)
	}
	return &openshell.Sandbox{Name: name}, nil
}

func (m *mockSDKSandboxes) Delete(ctx context.Context, workspace, name string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	if m.deleteFn != nil {
		if err := m.deleteFn(ctx, workspace, name); err != nil {
			return nil, err
		}
	}
	return mockDeletionResult(true), nil
}

func (m *mockSDKSandboxes) AttachProvider(ctx context.Context, workspace, sandboxName, providerName string, expectedResourceVersion uint64) (*openshell.AttachProviderResult, error) {
	if m.attachFn != nil {
		return m.attachFn(ctx, workspace, sandboxName, providerName, expectedResourceVersion)
	}
	return &openshell.AttachProviderResult{Attached: true, Sandbox: &openshell.Sandbox{Name: sandboxName}}, nil
}

func (m *mockSDKSandboxes) DetachProvider(ctx context.Context, workspace, sandboxName, providerName string, expectedResourceVersion uint64) (*openshell.DetachProviderResult, error) {
	if m.detachFn != nil {
		return m.detachFn(ctx, workspace, sandboxName, providerName, expectedResourceVersion)
	}
	return &openshell.DetachProviderResult{Detached: true, Sandbox: &openshell.Sandbox{Name: sandboxName}}, nil
}

func (m *mockSDKSandboxes) ListProviders(workspace, sandboxName string, opts ...openshell.ListOptions) (*openshell.Pager[*openshell.Provider], error) {
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.Provider], error) {
		items, err := m.ListAllProviders(ctx, workspace, sandboxName, opts...)
		return &openshell.Page[*openshell.Provider]{Items: items}, err
	}), nil
}

func (m *mockSDKSandboxes) ListAllProviders(ctx context.Context, workspace, sandboxName string, _ ...openshell.ListOptions) ([]*openshell.Provider, error) {
	if m.listProvidersFn != nil {
		return m.listProvidersFn(ctx, workspace, sandboxName)
	}
	return nil, nil
}

func (m *mockSDKSandboxes) Watch(_ context.Context, _, _ string, _ ...openshell.WatchOptions) (openshell.WatchInterface[*openshell.Sandbox], error) {
	panic("not implemented")
}

func (m *mockSDKSandboxes) WaitReady(_ context.Context, _, _ string, _ ...openshell.WaitOptions) (*openshell.Sandbox, error) {
	panic("not implemented")
}

func (m *mockSDKSandboxes) GetLogs(ctx context.Context, workspace, name string, opts ...openshell.LogOption) (*openshell.LogResult, error) {
	if m.getLogsFn != nil {
		return m.getLogsFn(ctx, workspace, name, opts...)
	}
	return &openshell.LogResult{}, nil
}

func (m *mockSDKSandboxes) Start(ctx context.Context, workspace, name string) (*openshell.Sandbox, error) {
	if m.startFn != nil {
		return m.startFn(ctx, workspace, name)
	}
	return &openshell.Sandbox{Name: name}, nil
}

func (m *mockSDKSandboxes) Stop(ctx context.Context, workspace, name string) (*openshell.Sandbox, error) {
	if m.stopFn != nil {
		return m.stopFn(ctx, workspace, name)
	}
	return &openshell.Sandbox{Name: name}, nil
}

func (m *mockSDKSandboxes) WaitStopped(_ context.Context, _, _ string, _ ...openshell.WaitOptions) (*openshell.Sandbox, error) {
	panic("not implemented")
}

type mockSDKProviders struct {
	createFn func(ctx context.Context, workspace string, provider *openshell.Provider) (*openshell.Provider, error)
	getFn    func(ctx context.Context, workspace, name string) (*openshell.Provider, error)
	listFn   func(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Provider, error)
	updateFn func(ctx context.Context, workspace string, provider *openshell.Provider) (*openshell.Provider, error)
	deleteFn func(ctx context.Context, workspace, name string) error
	profiles mockSDKProfiles
	refresh  mockSDKRefresh
}

func (m *mockSDKProviders) Profiles() openshell.ProfileInterface { return &m.profiles }
func (m *mockSDKProviders) Refresh() openshell.RefreshInterface  { return &m.refresh }

func (m *mockSDKProviders) Create(ctx context.Context, workspace string, provider *openshell.Provider) (*openshell.Provider, error) {
	if m.createFn != nil {
		return m.createFn(ctx, workspace, provider)
	}
	if provider != nil {
		return provider, nil
	}
	return &openshell.Provider{}, nil
}

func (m *mockSDKProviders) Get(ctx context.Context, workspace, name string) (*openshell.Provider, error) {
	if m.getFn != nil {
		return m.getFn(ctx, workspace, name)
	}
	return &openshell.Provider{Name: name, Workspace: workspace}, nil
}

func (m *mockSDKProviders) List(workspace string, opts ...openshell.ListOptions) (*openshell.Pager[*openshell.Provider], error) {
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.Provider], error) {
		items, err := m.ListAll(ctx, workspace, opts...)
		return &openshell.Page[*openshell.Provider]{Items: items}, err
	}), nil
}

func (m *mockSDKProviders) ListAll(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Provider, error) {
	if m.listFn != nil {
		return m.listFn(ctx, workspace, opts...)
	}
	return nil, nil
}

func (m *mockSDKProviders) Update(ctx context.Context, workspace string, provider *openshell.Provider) (*openshell.Provider, error) {
	if m.updateFn != nil {
		return m.updateFn(ctx, workspace, provider)
	}
	return provider, nil
}

func (m *mockSDKProviders) Delete(ctx context.Context, workspace, name string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	if m.deleteFn != nil {
		if err := m.deleteFn(ctx, workspace, name); err != nil {
			return nil, err
		}
	}
	return mockDeletionResult(true), nil
}

func (m *mockSDKProviders) Ensure(_ context.Context, _ string, _ *openshell.Provider) (*openshell.Provider, error) {
	panic("not implemented")
}

type mockSDKRefresh struct {
	getStatusFn func(ctx context.Context, workspace, provider, credentialKey string) ([]*openshell.RefreshStatus, error)
	configureFn func(ctx context.Context, workspace string, config *openshell.RefreshConfig) (*openshell.RefreshStatus, error)
	rotateFn    func(ctx context.Context, workspace, provider, credentialKey string) (*openshell.RefreshStatus, error)
	deleteFn    func(ctx context.Context, workspace, provider, credentialKey string) (bool, error)
}

func (m *mockSDKRefresh) GetStatus(ctx context.Context, workspace, provider, credentialKey string) ([]*openshell.RefreshStatus, error) {
	if m.getStatusFn != nil {
		return m.getStatusFn(ctx, workspace, provider, credentialKey)
	}
	return nil, nil
}

func (m *mockSDKRefresh) Configure(ctx context.Context, workspace string, config *openshell.RefreshConfig) (*openshell.RefreshStatus, error) {
	if m.configureFn != nil {
		return m.configureFn(ctx, workspace, config)
	}
	return &openshell.RefreshStatus{CredentialKey: config.CredentialKey, Strategy: config.Strategy}, nil
}

func (m *mockSDKRefresh) Rotate(ctx context.Context, workspace, provider, credentialKey string) (*openshell.RefreshStatus, error) {
	if m.rotateFn != nil {
		return m.rotateFn(ctx, workspace, provider, credentialKey)
	}
	return &openshell.RefreshStatus{CredentialKey: credentialKey}, nil
}

func (m *mockSDKRefresh) Delete(ctx context.Context, workspace, provider, credentialKey string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	deleted := true
	var err error
	if m.deleteFn != nil {
		deleted, err = m.deleteFn(ctx, workspace, provider, credentialKey)
	}
	return mockDeletionResult(deleted), err
}

type mockSDKProfiles struct {
	listFn   func(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.ProviderProfile, error)
	getFn    func(ctx context.Context, workspace, id string) (*openshell.ProviderProfile, error)
	importFn func(ctx context.Context, workspace string, items []openshell.ProfileImportItem) (*openshell.ImportResult, error)
	updateFn func(ctx context.Context, workspace, id string, expectedResourceVersion uint64, item openshell.ProfileImportItem) (*openshell.UpdateResult, error)
	lintFn   func(ctx context.Context, workspace string, items []openshell.ProfileImportItem) (*openshell.LintResult, error)
	deleteFn func(ctx context.Context, workspace, id string) (bool, error)
}

func (m *mockSDKProfiles) List(workspace string, opts ...openshell.ListOptions) (*openshell.Pager[*openshell.ProviderProfile], error) {
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.ProviderProfile], error) {
		items, err := m.ListAll(ctx, workspace, opts...)
		return &openshell.Page[*openshell.ProviderProfile]{Items: items}, err
	}), nil
}

func (m *mockSDKProfiles) ListAll(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.ProviderProfile, error) {
	if m.listFn != nil {
		return m.listFn(ctx, workspace, opts...)
	}
	return nil, nil
}

func (m *mockSDKProfiles) Get(ctx context.Context, workspace, id string) (*openshell.ProviderProfile, error) {
	if m.getFn != nil {
		return m.getFn(ctx, workspace, id)
	}
	return &openshell.ProviderProfile{ID: id}, nil
}

func (m *mockSDKProfiles) Import(ctx context.Context, workspace string, items []openshell.ProfileImportItem) (*openshell.ImportResult, error) {
	if m.importFn != nil {
		return m.importFn(ctx, workspace, items)
	}
	result := &openshell.ImportResult{Imported: true}
	for _, item := range items {
		result.Profiles = append(result.Profiles, item.Profile)
	}
	return result, nil
}

func (m *mockSDKProfiles) Update(ctx context.Context, workspace, id string, expectedResourceVersion uint64, item openshell.ProfileImportItem) (*openshell.UpdateResult, error) {
	if m.updateFn != nil {
		return m.updateFn(ctx, workspace, id, expectedResourceVersion, item)
	}
	profile := item.Profile
	return &openshell.UpdateResult{Updated: true, Profile: &profile}, nil
}

func (m *mockSDKProfiles) Lint(ctx context.Context, workspace string, items []openshell.ProfileImportItem) (*openshell.LintResult, error) {
	if m.lintFn != nil {
		return m.lintFn(ctx, workspace, items)
	}
	return &openshell.LintResult{Valid: true}, nil
}

func (m *mockSDKProfiles) Delete(ctx context.Context, workspace, id string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	deleted := true
	var err error
	if m.deleteFn != nil {
		deleted, err = m.deleteFn(ctx, workspace, id)
	}
	return mockDeletionResult(deleted), err
}

type mockSDKWorkspaces struct {
	createFn       func(ctx context.Context, name string, labels map[string]string) (*openshell.Workspace, error)
	getFn          func(ctx context.Context, name string) (*openshell.Workspace, error)
	listFn         func(ctx context.Context, opts ...openshell.ListOptions) ([]*openshell.Workspace, error)
	listPageFn     func(ctx context.Context, opts ...openshell.ListOptions) (*openshell.Page[*openshell.Workspace], error)
	deleteFn       func(ctx context.Context, name string) error
	addMemberFn    func(ctx context.Context, workspace, principalSubject string, role openshell.WorkspaceRole) (*openshell.WorkspaceMember, error)
	removeMemberFn func(ctx context.Context, workspace, principalSubject string) error
	listMembersFn  func(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.WorkspaceMember, error)
}

func (m *mockSDKWorkspaces) Create(ctx context.Context, name string, labels map[string]string) (*openshell.Workspace, error) {
	if m.createFn != nil {
		return m.createFn(ctx, name, labels)
	}
	return &openshell.Workspace{Name: name, Labels: labels, Phase: openshell.WorkspaceActive}, nil
}

func (m *mockSDKWorkspaces) Get(ctx context.Context, name string) (*openshell.Workspace, error) {
	if m.getFn != nil {
		return m.getFn(ctx, name)
	}
	return &openshell.Workspace{Name: name, Phase: openshell.WorkspaceActive}, nil
}

func (m *mockSDKWorkspaces) List(opts ...openshell.ListOptions) (*openshell.Pager[*openshell.Workspace], error) {
	if m.listPageFn != nil {
		return openshell.NewPager(opts[0].PageToken, func(ctx context.Context, _ string) (*openshell.Page[*openshell.Workspace], error) {
			return m.listPageFn(ctx, opts...)
		}), nil
	}
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.Workspace], error) {
		items, err := m.ListAll(ctx, opts...)
		return &openshell.Page[*openshell.Workspace]{Items: items}, err
	}), nil
}

func (m *mockSDKWorkspaces) ListAll(ctx context.Context, opts ...openshell.ListOptions) ([]*openshell.Workspace, error) {
	if m.listFn != nil {
		return m.listFn(ctx, opts...)
	}
	return nil, nil
}

func (m *mockSDKWorkspaces) Delete(ctx context.Context, name string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	if m.deleteFn != nil {
		if err := m.deleteFn(ctx, name); err != nil {
			return nil, err
		}
	}
	return mockDeletionResult(true), nil
}

func (m *mockSDKWorkspaces) AddMember(ctx context.Context, workspace, principalSubject string, role openshell.WorkspaceRole) (*openshell.WorkspaceMember, error) {
	if m.addMemberFn != nil {
		return m.addMemberFn(ctx, workspace, principalSubject, role)
	}
	return &openshell.WorkspaceMember{PrincipalSubject: principalSubject, Role: role}, nil
}

func (m *mockSDKWorkspaces) RemoveMember(ctx context.Context, workspace, principalSubject string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	if m.removeMemberFn != nil {
		if err := m.removeMemberFn(ctx, workspace, principalSubject); err != nil {
			return nil, err
		}
	}
	return mockDeletionResult(true), nil
}

func (m *mockSDKWorkspaces) ListMembers(workspace string, opts ...openshell.ListOptions) (*openshell.Pager[*openshell.WorkspaceMember], error) {
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.WorkspaceMember], error) {
		items, err := m.ListAllMembers(ctx, workspace, opts...)
		return &openshell.Page[*openshell.WorkspaceMember]{Items: items}, err
	}), nil
}

func (m *mockSDKWorkspaces) ListAllMembers(ctx context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.WorkspaceMember, error) {
	if m.listMembersFn != nil {
		return m.listMembersFn(ctx, workspace, opts...)
	}
	return nil, nil
}

type mockSDKPolicy struct {
	getDraftFn   func(ctx context.Context, workspace, sandboxName string, opts ...openshell.GetDraftOption) (*openshell.DraftPolicy, error)
	approveFn    func(ctx context.Context, workspace, sandboxName, chunkID, reviewToken string) (*openshell.ApproveResult, error)
	rejectFn     func(ctx context.Context, workspace, sandboxName, chunkID, reason string) error
	approveAllFn func(ctx context.Context, workspace, sandboxName string, opts ...openshell.ApproveAllOption) (*openshell.ApproveAllResult, error)
	clearFn      func(ctx context.Context, workspace, sandboxName string) (*openshell.ClearResult, error)
	historyFn    func(ctx context.Context, workspace, sandboxName string) ([]openshell.DraftHistoryEntry, error)
	getStatusFn  func(ctx context.Context, workspace, sandboxName string, opts ...openshell.GetStatusOption) (*openshell.PolicyStatusResult, error)
	listFn       func(ctx context.Context, workspace string, opts ...openshell.ListPolicyOption) ([]openshell.SandboxPolicyRevision, error)
	editFn       func(ctx context.Context, workspace, sandboxName, chunkID string, proposedRule *openshell.NetworkPolicyRule) error
	undoFn       func(ctx context.Context, workspace, sandboxName, chunkID string) (*openshell.UndoResult, error)
}

func (m *mockSDKPolicy) GetDraft(ctx context.Context, workspace, sandboxName string, opts ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
	if m.getDraftFn != nil {
		return m.getDraftFn(ctx, workspace, sandboxName, opts...)
	}
	return &openshell.DraftPolicy{}, nil
}

func (m *mockSDKPolicy) ApproveDraftChunk(ctx context.Context, workspace, sandboxName, chunkID, reviewToken string) (*openshell.ApproveResult, error) {
	if m.approveFn != nil {
		return m.approveFn(ctx, workspace, sandboxName, chunkID, reviewToken)
	}
	return &openshell.ApproveResult{}, nil
}

func (m *mockSDKPolicy) RejectDraftChunk(ctx context.Context, workspace, sandboxName, chunkID, reason string) error {
	if m.rejectFn != nil {
		return m.rejectFn(ctx, workspace, sandboxName, chunkID, reason)
	}
	return nil
}

func (m *mockSDKPolicy) ApproveAllDraftChunks(ctx context.Context, workspace, sandboxName string, opts ...openshell.ApproveAllOption) (*openshell.ApproveAllResult, error) {
	if m.approveAllFn != nil {
		return m.approveAllFn(ctx, workspace, sandboxName, opts...)
	}
	return &openshell.ApproveAllResult{}, nil
}

func (m *mockSDKPolicy) ClearDraftChunks(ctx context.Context, workspace, sandboxName string) (*openshell.ClearResult, error) {
	if m.clearFn != nil {
		return m.clearFn(ctx, workspace, sandboxName)
	}
	return &openshell.ClearResult{}, nil
}

func (m *mockSDKPolicy) GetDraftHistory(ctx context.Context, workspace, sandboxName string) ([]openshell.DraftHistoryEntry, error) {
	if m.historyFn != nil {
		return m.historyFn(ctx, workspace, sandboxName)
	}
	return nil, nil
}

func (m *mockSDKPolicy) GetStatus(ctx context.Context, workspace, sandboxName string, opts ...openshell.GetStatusOption) (*openshell.PolicyStatusResult, error) {
	if m.getStatusFn != nil {
		return m.getStatusFn(ctx, workspace, sandboxName, opts...)
	}
	return &openshell.PolicyStatusResult{}, nil
}

func (m *mockSDKPolicy) List(workspace, sandboxName string, opts ...openshell.ListPolicyOption) (*openshell.Pager[openshell.SandboxPolicyRevision], error) {
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[openshell.SandboxPolicyRevision], error) {
		items, err := m.ListAll(ctx, workspace, sandboxName, opts...)
		return &openshell.Page[openshell.SandboxPolicyRevision]{Items: items}, err
	}), nil
}

func (m *mockSDKPolicy) ListAll(ctx context.Context, workspace, _ string, opts ...openshell.ListPolicyOption) ([]openshell.SandboxPolicyRevision, error) {
	if m.listFn != nil {
		return m.listFn(ctx, workspace, opts...)
	}
	return nil, nil
}

func (m *mockSDKPolicy) EditDraftChunk(ctx context.Context, workspace, sandboxName, chunkID string, proposedRule *openshell.NetworkPolicyRule) error {
	if m.editFn != nil {
		return m.editFn(ctx, workspace, sandboxName, chunkID, proposedRule)
	}
	return nil
}

func (m *mockSDKPolicy) UndoDraftChunk(ctx context.Context, workspace, sandboxName, chunkID string) (*openshell.UndoResult, error) {
	if m.undoFn != nil {
		return m.undoFn(ctx, workspace, sandboxName, chunkID)
	}
	return &openshell.UndoResult{}, nil
}

type mockSDKConfig struct {
	getSandboxFn func(ctx context.Context, workspace, sandboxName string) (*openshell.SandboxConfig, error)
	getGatewayFn func(ctx context.Context) (*openshell.GatewayConfig, error)
	updateFn     func(ctx context.Context, workspace string, update *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error)
}

func (m *mockSDKConfig) GetSandbox(ctx context.Context, workspace, sandboxName string) (*openshell.SandboxConfig, error) {
	if m.getSandboxFn != nil {
		return m.getSandboxFn(ctx, workspace, sandboxName)
	}
	return &openshell.SandboxConfig{}, nil
}

func (m *mockSDKConfig) GetGateway(ctx context.Context) (*openshell.GatewayConfig, error) {
	if m.getGatewayFn != nil {
		return m.getGatewayFn(ctx)
	}
	return &openshell.GatewayConfig{}, nil
}

func (m *mockSDKConfig) Update(ctx context.Context, workspace string, update *openshell.ConfigUpdate) (*openshell.ConfigUpdateResult, error) {
	if m.updateFn != nil {
		return m.updateFn(ctx, workspace, update)
	}
	return &openshell.ConfigUpdateResult{}, nil
}

type mockSDKServices struct {
	exposeFn func(ctx context.Context, workspace, sandboxName, serviceName string, targetPort uint32, domain bool) (*openshell.ServiceEndpoint, error)
	getFn    func(ctx context.Context, workspace, sandboxName, serviceName string) (*openshell.ServiceEndpoint, error)
	listFn   func(ctx context.Context, workspace, sandboxName string, opts ...openshell.ListOptions) ([]*openshell.ServiceEndpoint, error)
	deleteFn func(ctx context.Context, workspace, sandboxName, serviceName string) error
}

func (m *mockSDKServices) Expose(ctx context.Context, workspace, sandboxName, serviceName string, targetPort uint32, domain bool, _ ...openshell.ExposeServiceOptions) (*openshell.ServiceEndpoint, error) {
	if m.exposeFn != nil {
		return m.exposeFn(ctx, workspace, sandboxName, serviceName, targetPort, domain)
	}
	return &openshell.ServiceEndpoint{Sandbox: sandboxName, Name: serviceName, TargetPort: targetPort, Domain: domain}, nil
}

func (m *mockSDKServices) Get(ctx context.Context, workspace, sandboxName, serviceName string) (*openshell.ServiceEndpoint, error) {
	if m.getFn != nil {
		return m.getFn(ctx, workspace, sandboxName, serviceName)
	}
	return &openshell.ServiceEndpoint{Sandbox: sandboxName, Name: serviceName}, nil
}

func (m *mockSDKServices) List(workspace, sandboxName string, opts ...openshell.ListOptions) (*openshell.Pager[*openshell.ServiceEndpoint], error) {
	return openshell.NewPager("", func(ctx context.Context, _ string) (*openshell.Page[*openshell.ServiceEndpoint], error) {
		items, err := m.ListAll(ctx, workspace, sandboxName, opts...)
		return &openshell.Page[*openshell.ServiceEndpoint]{Items: items}, err
	}), nil
}

func (m *mockSDKServices) ListAll(ctx context.Context, workspace, sandboxName string, opts ...openshell.ListOptions) ([]*openshell.ServiceEndpoint, error) {
	if m.listFn != nil {
		return m.listFn(ctx, workspace, sandboxName, opts...)
	}
	return nil, nil
}

func (m *mockSDKServices) Delete(ctx context.Context, workspace, sandboxName, serviceName string, _ ...openshell.DeleteOptions) (*openshell.DeletionResult, error) {
	if m.deleteFn != nil {
		if err := m.deleteFn(ctx, workspace, sandboxName, serviceName); err != nil {
			return nil, err
		}
	}
	return mockDeletionResult(true), nil
}

type mockSDKHealth struct {
	checkFn          func(ctx context.Context) (*openshell.HealthResult, error)
	getGatewayInfoFn func(ctx context.Context) (*openshell.GatewayInfo, error)
	getCurrentUserFn func(ctx context.Context) (*openshell.CurrentUser, error)
}

func (m *mockSDKHealth) Check(ctx context.Context) (*openshell.HealthResult, error) {
	if m.checkFn != nil {
		return m.checkFn(ctx)
	}
	return &openshell.HealthResult{Healthy: true}, nil
}

func (m *mockSDKHealth) GetGatewayInfo(ctx context.Context) (*openshell.GatewayInfo, error) {
	if m.getGatewayInfoFn != nil {
		return m.getGatewayInfoFn(ctx)
	}
	return &openshell.GatewayInfo{Status: openshell.ServiceStatusHealthy}, nil
}

func (m *mockSDKHealth) GetCurrentUser(ctx context.Context) (*openshell.CurrentUser, error) {
	if m.getCurrentUserFn != nil {
		return m.getCurrentUserFn(ctx)
	}
	return &openshell.CurrentUser{Subject: "test-user"}, nil
}

type mockInteractiveSession struct {
	reads      chan []byte
	written    []byte
	resizes    [][2]uint32
	exit       int
	closeReads sync.Once
	mu         sync.Mutex
	closed     bool
}

func (m *mockInteractiveSession) Read(p []byte) (int, error) {
	if m.reads == nil {
		return 0, io.EOF
	}
	data, ok := <-m.reads
	if !ok {
		return 0, io.EOF
	}
	return copy(p, data), nil
}

func (m *mockInteractiveSession) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written = append(m.written, p...)
	return len(p), nil
}

func (m *mockInteractiveSession) Resize(cols, rows uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resizes = append(m.resizes, [2]uint32{cols, rows})
	return nil
}

func (m *mockInteractiveSession) ExitCode() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exit, nil
}

func (m *mockInteractiveSession) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.closeReads.Do(func() {
		if m.reads != nil {
			close(m.reads)
		}
	})
	return nil
}

type mockSDKExec struct {
	runFn         func(ctx context.Context, workspace, sandboxName string, command []string, opts ...openshell.ExecOptions) (*openshell.ExecResult, error)
	streamFn      func(ctx context.Context, workspace, sandboxName string, command []string, opts ...openshell.ExecOptions) (openshell.ExecStream, error)
	interactiveFn func(ctx context.Context, workspace, sandboxName string, command []string, cols, rows uint32, opts ...openshell.ExecOptions) (openshell.InteractiveSession, error)
}

func (m *mockSDKExec) Run(ctx context.Context, workspace, sandboxName string, command []string, opts ...openshell.ExecOptions) (*openshell.ExecResult, error) {
	if m.runFn != nil {
		return m.runFn(ctx, workspace, sandboxName, command, opts...)
	}
	return &openshell.ExecResult{}, nil
}

func (m *mockSDKExec) Stream(ctx context.Context, workspace, sandboxName string, command []string, opts ...openshell.ExecOptions) (openshell.ExecStream, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, workspace, sandboxName, command, opts...)
	}
	panic("not implemented")
}

func (m *mockSDKExec) Interactive(ctx context.Context, workspace, sandboxName string, command []string, cols, rows uint32, opts ...openshell.ExecOptions) (openshell.InteractiveSession, error) {
	if m.interactiveFn != nil {
		return m.interactiveFn(ctx, workspace, sandboxName, command, cols, rows, opts...)
	}
	panic("not implemented")
}

type mockSDKFiles struct {
	uploadFn   func(ctx context.Context, workspace, sandboxName, localPath, remotePath string) error
	downloadFn func(ctx context.Context, workspace, sandboxName, remotePath, localPath string) error
}

func (m *mockSDKFiles) Upload(ctx context.Context, workspace, sandboxName, localPath, remotePath string) error {
	if m.uploadFn != nil {
		return m.uploadFn(ctx, workspace, sandboxName, localPath, remotePath)
	}
	return nil
}

func (m *mockSDKFiles) Download(ctx context.Context, workspace, sandboxName, remotePath, localPath string) error {
	if m.downloadFn != nil {
		return m.downloadFn(ctx, workspace, sandboxName, remotePath, localPath)
	}
	return nil
}
