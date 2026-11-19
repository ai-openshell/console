package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/types"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/protobuf/encoding/protojson"
)

// policyProtoMarshaler emits the camelCase protojson the frontend reads.
var policyProtoMarshaler = protojson.MarshalOptions{UseProtoNames: false}

// FromSDKSandbox converts an SDK Sandbox to the JSON DTO the frontend expects.
func FromSDKSandbox(sandbox *openshell.Sandbox) Sandbox {
	if sandbox == nil {
		return Sandbox{}
	}
	out := Sandbox{
		Metadata: ObjectMeta{
			ID:              sandbox.ID,
			Name:            sandbox.Name,
			Workspace:       sandbox.Workspace,
			Labels:          sandbox.Labels,
			Annotations:     sandbox.Annotations,
			CreatedAtMs:     timeToMs(sandbox.CreatedAt),
			ResourceVersion: sandbox.ResourceVersion,
		},
	}
	if sandbox.DeletionTimestamp != nil {
		out.Metadata.DeletionTimestampMs = sandbox.DeletionTimestamp.UnixMilli()
	}

	if provenance := sandbox.CreatedFromWorkloadTemplate; provenance != nil {
		out.CreatedFromWorkloadTemplate = &WorkloadTemplateProvenance{
			Name:            provenance.Name,
			ResourceVersion: provenance.ResourceVersion,
		}
	}
	if len(sandbox.ServiceURLs) > 0 {
		out.ServiceURLs = sandbox.ServiceURLs
	}

	out.Spec = SandboxSpec{
		LogLevel:    sandbox.Spec.LogLevel,
		Environment: sandbox.Spec.Environment,
		Providers:   sandbox.Spec.Providers,
		Command:     sandbox.Spec.Command,
		TTY:         sandbox.Spec.TTY,
		// In the SDK's terms a count implies a GPU request.
		GPU:      sandbox.Spec.GPU || sandbox.Spec.GPUCount != nil,
		GPUCount: sandbox.Spec.GPUCount,
	}
	if sandbox.Spec.Template != nil {
		out.Spec.Image = sandbox.Spec.Template.Image
		out.Spec.Template = fromSDKSpecTemplate(sandbox.Spec.Template)
	}
	if sandbox.Spec.Policy != nil {
		out.Spec.Policy = marshalSDKPolicy(sandbox.Spec.Policy)
	}

	// AgentFd and SandboxFd stay behind: they are how the gateway reaches the
	// sandbox's own services and nothing a browser is to be told.
	out.Status = SandboxStatus{
		SandboxName:          sandbox.Name,
		AgentPod:             sandbox.Status.AgentPod,
		Phase:                strings.ToUpper(string(sandbox.Status.Phase)),
		CurrentPolicyVersion: sandbox.Status.CurrentPolicyVersion,
		ExitCode:             sandbox.Status.ExitCode,
	}
	for _, cond := range sandbox.Status.Conditions {
		out.Status.Conditions = append(out.Status.Conditions, SandboxCondition{
			Type:               cond.Type,
			Status:             cond.Status,
			Reason:             cond.Reason,
			Message:            cond.Message,
			LastTransitionTime: cond.LastTransitionTime,
		})
	}
	for _, endpoint := range sandbox.Status.EndpointStatuses {
		out.Status.EndpointStatuses = append(out.Status.EndpointStatuses, EndpointStatus{
			EndpointID:     endpoint.EndpointID,
			Host:           endpoint.Host,
			Ports:          endpoint.Ports,
			Path:           endpoint.Path,
			LastResult:     sdkEndpointResultString(endpoint.LastResult),
			LastReportedAt: endpoint.LastReportedAt,
			LastFailure:    fromSDKEndpointFailure(endpoint.LastFailure),
		})
	}
	if observation := sandbox.Status.LastTokenGrantFailure; observation != nil {
		out.Status.LastTokenGrantFailure = &TokenGrantFailureObservation{
			DestinationHost: observation.DestinationHost,
			DestinationPort: observation.DestinationPort,
			Failure:         fromSDKEndpointFailure(observation.Failure),
		}
	}
	if admission := sandbox.Status.ConfigurationAdmission; admission != nil {
		// The SDK calls a state the gateway left unspecified "unknown".
		state := strings.ToUpper(string(admission.State))
		if state == "" || state == "UNKNOWN" {
			state = "UNSPECIFIED"
		}
		out.Status.ConfigurationAdmission = &ConfigurationAdmission{
			State:               state,
			PolicyVersion:       admission.PolicyVersion,
			PolicyHash:          admission.PolicyHash,
			ConfigRevision:      admission.ConfigRevision,
			ProviderEnvRevision: admission.ProviderEnvRevision,
			Error:               admission.Error,
		}
	}
	return out
}

// fromSDKSpecTemplate converts the inline template of a sandbox's spec, and
// returns nil for one that holds nothing but the image, which SandboxSpec
// carries itself. AgentSocket is a path on the sandbox host and is left out.
func fromSDKSpecTemplate(t *openshell.SandboxTemplate) *SandboxSpecTemplate {
	out := SandboxSpecTemplate{
		RuntimeClassName: t.RuntimeClassName,
		UserNamespaces:   t.UserNamespaces,
	}
	if len(t.Labels) > 0 {
		out.Labels = t.Labels
	}
	if len(t.Annotations) > 0 {
		out.Annotations = t.Annotations
	}
	if len(t.Environment) > 0 {
		out.Environment = t.Environment
	}
	if len(t.Resources) > 0 {
		out.Resources = t.Resources
	}
	if len(t.DriverConfig) > 0 {
		out.DriverConfig = t.DriverConfig
	}
	if out.RuntimeClassName == "" && out.UserNamespaces == nil && out.Labels == nil && out.Annotations == nil &&
		out.Environment == nil && out.Resources == nil && out.DriverConfig == nil {
		return nil
	}
	return &out
}

// fromSDKEndpointFailure converts the cause of a failed exchange, and returns
// nil when the sandbox classified none.
func fromSDKEndpointFailure(failure *openshell.EndpointFailure) *EndpointFailure {
	if failure == nil {
		return nil
	}
	return &EndpointFailure{
		Domain:             sdkEndpointFailureDomainString(failure.Domain),
		Code:               failure.Code,
		RecoveryAction:     sdkRecoveryActionString(failure.RecoveryAction),
		Provider:           failure.Provider,
		CredentialKey:      failure.CredentialKey,
		Stage:              failure.Stage,
		UpstreamHTTPStatus: failure.UpstreamHTTPStatus,
		RequestID:          failure.RequestID,
	}
}

func sdkEndpointFailureDomainString(domain openshell.EndpointFailureDomain) string {
	if domain == openshell.EndpointFailureDomainTokenGrant {
		return "TOKEN_GRANT"
	}
	return "UNSPECIFIED"
}

func sdkEndpointResultString(result openshell.EndpointResult) string {
	switch result {
	case openshell.EndpointNoObservedExchange:
		return "NO_OBSERVED_EXCHANGE"
	case openshell.EndpointHTTPResponseReceived:
		return "HTTP_RESPONSE_RECEIVED"
	case openshell.EndpointPolicyDenied:
		return "POLICY_DENIED"
	case openshell.EndpointCredentialUnavailable:
		return "CREDENTIAL_UNAVAILABLE"
	case openshell.EndpointTLSFailed:
		return "TLS_FAILED"
	case openshell.EndpointTransportFailed:
		return "TRANSPORT_FAILED"
	case openshell.EndpointUpstreamRejected:
		return "UPSTREAM_REJECTED"
	}
	return "UNSPECIFIED"
}

// BuildSDKSandboxSpec constructs an SDK SandboxSpec from a create request.
func BuildSDKSandboxSpec(req CreateSandboxRequest) (*openshell.SandboxSpec, error) {
	policy, err := ParseSDKPolicy(req.Policy)
	if err != nil {
		return nil, fmt.Errorf("policy does not match the SandboxPolicy schema: %w", err)
	}

	spec := &openshell.SandboxSpec{
		LogLevel:    req.LogLevel,
		Environment: req.Environment,
		Template: &openshell.SandboxTemplate{
			Image:            req.Image,
			RuntimeClassName: req.RuntimeClassName,
			DriverConfig:     req.DriverConfig,
		},
		Policy:    policy,
		Providers: req.Providers,
		Command:   req.Command,
		TTY:       req.TTY,
	}

	if req.CPU != "" || req.Memory != "" {
		resources := map[string]any{}
		limits := map[string]any{}
		if req.CPU != "" {
			limits["cpu"] = req.CPU
		}
		if req.Memory != "" {
			limits["memory"] = req.Memory
		}
		resources["limits"] = limits
		spec.Template.Resources = resources
	}

	if req.GpuCount > 0 {
		gpu := req.GpuCount
		spec.GPUCount = &gpu
	}
	return spec, nil
}

// BuildSDKCreateOptions builds the options of a sandbox create: the sandbox's
// labels and annotations and the services to expose with it. It returns an
// option only for what the request sets, and none when it sets nothing.
func BuildSDKCreateOptions(labels, annotations map[string]string, exposures []ServiceExposure) []openshell.CreateOption {
	var opts []openshell.CreateOption
	if len(labels) > 0 {
		opts = append(opts, openshell.WithLabels(labels))
	}
	if len(annotations) > 0 {
		opts = append(opts, openshell.WithAnnotations(annotations))
	}
	if len(exposures) > 0 {
		sdkExposures := make([]openshell.ServiceExposure, 0, len(exposures))
		for _, exposure := range exposures {
			sdkExposures = append(sdkExposures, openshell.ServiceExposure{
				Service:    exposure.Service,
				TargetPort: exposure.TargetPort,
			})
		}
		opts = append(opts, openshell.WithServiceExposures(sdkExposures...))
	}
	return opts
}

func sdkSettingScopeString(scope openshell.SettingScope) string {
	switch scope {
	case openshell.SettingScopeSandbox:
		return "SANDBOX"
	case openshell.SettingScopeGlobal:
		return "GLOBAL"
	}
	return "UNSPECIFIED"
}

func sdkPolicySourceString(source openshell.PolicySource) string {
	switch source {
	case openshell.PolicySourceSandbox:
		return "SANDBOX"
	case openshell.PolicySourceGlobal:
		return "GLOBAL"
	}
	return "UNSPECIFIED"
}

// FromSDKSandboxSettings converts a sandbox's effective configuration to the
// JSON DTO: its settings sorted by key, each with the scope it was resolved
// from, and what the gateway says about the sandbox's policy. The policy
// itself is left to the policy endpoint.
func FromSDKSandboxSettings(config *openshell.SandboxConfig) SandboxSettings {
	if config == nil {
		return SandboxSettings{Settings: []SandboxSettingEntry{}, PolicySource: "UNSPECIFIED"}
	}
	out := SandboxSettings{
		Settings:                    []SandboxSettingEntry{},
		PolicySource:                sdkPolicySourceString(config.PolicySource),
		PolicyHash:                  config.PolicyHash,
		PolicyVersion:               config.PolicyVersion,
		GlobalPolicyVersion:         config.GlobalPolicyVersion,
		ConfigRevision:              config.ConfigRevision,
		ProviderEnvRevision:         config.ProviderEnvRevision,
		PolicyValidationFailureMode: config.PolicyValidationFailureMode,
	}
	for key, setting := range config.Settings {
		out.Settings = append(out.Settings, SandboxSettingEntry{
			Key:   key,
			Value: sdkSettingValueJSON(setting.Value),
			Scope: sdkSettingScopeString(setting.Scope),
		})
	}
	sort.Slice(out.Settings, func(i, j int) bool {
		return out.Settings[i].Key < out.Settings[j].Key
	})
	return out
}

// FromSDKSandboxTemplate converts an SDK SandboxWorkloadTemplate to the JSON
// DTO the frontend expects.
func FromSDKSandboxTemplate(t *openshell.SandboxWorkloadTemplate) SandboxTemplate {
	if t == nil {
		return SandboxTemplate{}
	}
	out := SandboxTemplate{
		Metadata: ObjectMeta{
			ID:              t.ID,
			Name:            t.Name,
			Workspace:       t.Workspace,
			Labels:          t.Labels,
			Annotations:     t.Annotations,
			CreatedAtMs:     timeToMs(t.CreatedAt),
			ResourceVersion: t.ResourceVersion,
		},
		Spec: fromSDKTemplateSpec(t.Spec),
	}
	if t.DeletionTimestamp != nil {
		out.Metadata.DeletionTimestampMs = t.DeletionTimestamp.UnixMilli()
	}
	return out
}

func fromSDKTemplateSpec(spec openshell.SandboxWorkloadTemplateSpec) SandboxTemplateSpec {
	out := SandboxTemplateSpec{DriverConfig: spec.DriverConfig}
	if spec.Workload != nil {
		workload := &SandboxWorkload{
			Image:       spec.Workload.Image,
			Environment: spec.Workload.Environment,
		}
		if res := spec.Workload.Resources; res != nil {
			workload.Resources = &SandboxResources{CPU: res.CPU, Memory: res.Memory}
			if res.GPU != nil {
				workload.Resources.GPU = &SandboxGPU{Count: res.GPU.Count}
			}
		}
		out.Workload = workload
	}
	if sl := spec.DesiredServiceLevel; sl != nil && sl.Startup != nil {
		out.DesiredServiceLevel = &SandboxServiceLevel{
			Startup: &SandboxStartup{
				ReadyWithinMs: sl.Startup.ReadyWithin.Milliseconds(),
				MaxBurst:      sl.Startup.MaxBurst,
			},
		}
	}
	return out
}

// BuildSDKSandboxWorkloadTemplate constructs an SDK SandboxWorkloadTemplate from
// a create request.
func BuildSDKSandboxWorkloadTemplate(req CreateSandboxTemplateRequest) *openshell.SandboxWorkloadTemplate {
	return &openshell.SandboxWorkloadTemplate{
		Name:        req.Name,
		Labels:      req.Labels,
		Annotations: req.Annotations,
		Spec:        buildSDKTemplateSpec(req.Spec),
	}
}

func buildSDKTemplateSpec(spec SandboxTemplateSpec) openshell.SandboxWorkloadTemplateSpec {
	out := openshell.SandboxWorkloadTemplateSpec{DriverConfig: spec.DriverConfig}
	if spec.Workload != nil {
		workload := &openshell.SandboxWorkloadConfig{
			Image:       spec.Workload.Image,
			Environment: spec.Workload.Environment,
		}
		if res := spec.Workload.Resources; res != nil {
			workload.Resources = &openshell.SandboxResources{CPU: res.CPU, Memory: res.Memory}
			if res.GPU != nil {
				workload.Resources.GPU = &openshell.SandboxGPURequirements{Count: res.GPU.Count}
			}
		}
		out.Workload = workload
	}
	if sl := spec.DesiredServiceLevel; sl != nil && sl.Startup != nil {
		out.DesiredServiceLevel = &openshell.SandboxServiceLevel{
			Startup: &openshell.SandboxStartup{
				ReadyWithin: time.Duration(sl.Startup.ReadyWithinMs) * time.Millisecond,
				MaxBurst:    sl.Startup.MaxBurst,
			},
		}
	}
	return out
}

// BuildSDKTemplateGovernanceSpec builds the governance-only SandboxSpec allowed
// when creating a sandbox from a template. The gateway rejects any workload
// fields here — only policy, providers, command and tty may be supplied.
func BuildSDKTemplateGovernanceSpec(req CreateSandboxFromTemplateRequest) (*openshell.SandboxSpec, error) {
	policy, err := ParseSDKPolicy(req.Policy)
	if err != nil {
		return nil, fmt.Errorf("policy does not match the SandboxPolicy schema: %w", err)
	}
	return &openshell.SandboxSpec{
		Policy:    policy,
		Providers: req.Providers,
		Command:   req.Command,
		TTY:       req.TTY,
	}, nil
}

// ParseSDKPolicy converts camelCase JSON from the frontend into the SDK
// SandboxPolicy. It round-trips through the proto with protojson so every
// policy field (L7 rules, deny rules, IP allowlists, multi-port, MCP,
// middleware, ...) is preserved. protojson rejects unknown fields.
func ParseSDKPolicy(raw json.RawMessage) (*openshell.SandboxPolicy, error) {
	var pb sbv1.SandboxPolicy
	if err := protojson.Unmarshal(raw, &pb); err != nil {
		return nil, err
	}
	return sandboxPolicyFromProto(&pb), nil
}

func timeToMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// marshalSDKPolicy converts an SDK SandboxPolicy into camelCase JSON for the
// frontend, round-tripping through the proto with protojson for full fidelity.
func marshalSDKPolicy(p *openshell.SandboxPolicy) json.RawMessage {
	raw, err := policyProtoMarshaler.Marshal(sandboxPolicyToProto(p))
	if err != nil {
		return nil
	}
	return raw
}

// RefreshStrategyAWSStsAssumeRole is defined in SDK types but not re-exported
// from openshell/v1.
const RefreshStrategyAWSStsAssumeRole openshell.RefreshStrategy = "AWSStsAssumeRole"

// FromSDKProvider converts an SDK Provider to the JSON DTO. Credential values
// and handles are secret — only key names are surfaced.
func FromSDKProvider(provider *openshell.Provider) Provider {
	if provider == nil {
		return Provider{}
	}
	out := Provider{
		Metadata: ObjectMeta{
			ID:              provider.ID,
			Name:            provider.Name,
			Workspace:       provider.Workspace,
			Labels:          provider.Labels,
			Annotations:     provider.Annotations,
			CreatedAtMs:     timeToMs(provider.CreatedAt),
			ResourceVersion: provider.ResourceVersion,
		},
		Type:             provider.Type,
		Config:           provider.Spec.Config,
		ProfileWorkspace: provider.Spec.ProfileWorkspace,
	}
	if provider.DeletionTimestamp != nil {
		out.Metadata.DeletionTimestampMs = provider.DeletionTimestamp.UnixMilli()
	}
	if len(provider.Spec.CredentialExpiresAt) > 0 {
		out.CredentialExpiresAtMs = make(map[string]int64, len(provider.Spec.CredentialExpiresAt))
		for k, t := range provider.Spec.CredentialExpiresAt {
			out.CredentialExpiresAtMs[k] = timeToMs(t)
		}
	}
	// The SDK does not carry the keys of the credentials a provider holds:
	// the gateway returns them as its redacted credentials map, which the
	// SDK's converter drops, and it clears the handles before answering. So
	// for a provider read from a gateway both maps are empty here and the
	// names come from AddCredentialNames. Whatever the SDK does carry is still
	// counted, for the day it stops dropping them.
	names := make([]string, 0, len(provider.Spec.Credentials)+len(provider.Spec.CredentialHandles))
	for name := range provider.Spec.Credentials {
		names = append(names, name)
	}
	for name := range provider.Spec.CredentialHandles {
		names = append(names, name)
	}
	out.AddCredentialNames(names)
	return out
}

// AddCredentialNames adds the keys of credentials the provider holds to
// CredentialNames, which stays sorted and free of duplicates.
func (p *Provider) AddCredentialNames(names []string) {
	if len(names) == 0 {
		return
	}
	merged := append(slices.Clone(p.CredentialNames), names...)
	slices.Sort(merged)
	p.CredentialNames = slices.Compact(merged)
}

// FromSDKRefreshStatus converts an SDK RefreshStatus to the JSON DTO.
func FromSDKRefreshStatus(status *openshell.RefreshStatus) CredentialRefreshStatus {
	if status == nil {
		return CredentialRefreshStatus{}
	}
	return CredentialRefreshStatus{
		CredentialKey:        status.CredentialKey,
		Strategy:             sdkRefreshStrategyString(status.Strategy),
		Status:               status.Status,
		ExpiresAtMs:          timeToMs(status.ExpiresAt),
		NextRefreshAtMs:      timeToMs(status.NextRefreshAt),
		LastRefreshAtMs:      timeToMs(status.LastRefreshAt),
		LastError:            status.LastError,
		RecoveryAction:       sdkRecoveryActionString(status.RecoveryAction),
		FailureCode:          status.FailureCode,
		ProviderErrorSubtype: status.ProviderErrorSubtype,
		LastErrorAtMs:        timeToMs(status.LastErrorAt),
	}
}

// sdkRecoveryActionString names what a failed refresh needs, the way the
// gateway's enum does (RETRY, REAUTHORIZE, FIX_CONFIGURATION, INVESTIGATE). A
// refresh that needs nothing has no action, which is the empty string.
func sdkRecoveryActionString(action types.RefreshRecoveryAction) string {
	if action == types.RefreshRecoveryActionUnspecified {
		return ""
	}
	return strings.ToUpper(action.String())
}

func sdkRefreshStrategyString(s openshell.RefreshStrategy) string {
	switch s {
	case openshell.RefreshStrategyStatic:
		return "STATIC"
	case openshell.RefreshStrategyExternal:
		return "EXTERNAL"
	case openshell.RefreshStrategyOAuth2RefreshToken:
		return "OAUTH2_REFRESH_TOKEN"
	case openshell.RefreshStrategyOAuth2ClientCredentials:
		return "OAUTH2_CLIENT_CREDENTIALS"
	case openshell.RefreshStrategyGoogleServiceAccountJWT:
		return "GOOGLE_SERVICE_ACCOUNT_JWT"
	case RefreshStrategyAWSStsAssumeRole:
		return "AWS_STS_ASSUME_ROLE"
	}
	return "UNSPECIFIED"
}

// FromSDKProviderProfile converts an SDK ProviderProfile to the JSON DTO. The
// SDK carries three fields of an endpoint, so the result summarizes the
// endpoints and has no NetworkEndpoints; FromProtoProviderProfile converts a
// profile that was read whole.
func FromSDKProviderProfile(profile *openshell.ProviderProfile) ProviderProfile {
	if profile == nil {
		return ProviderProfile{Credentials: []ProfileCredential{}}
	}
	return FromNarrowProviderProfile(ProviderProfileFromSDK(profile))
}

// ParseSDKProfileCategory maps the frontend UPPER_SNAKE category to the SDK type.
func ParseSDKProfileCategory(s string) openshell.ProfileCategory {
	switch s {
	case "INFERENCE":
		return openshell.ProfileCategoryInference
	case "AGENT":
		return openshell.ProfileCategoryAgent
	case "SOURCE_CONTROL":
		return openshell.ProfileCategorySourceControl
	case "MESSAGING":
		return openshell.ProfileCategoryMessaging
	case "DATA":
		return openshell.ProfileCategoryData
	case "KNOWLEDGE":
		return openshell.ProfileCategoryKnowledge
	default:
		return openshell.ProfileCategoryOther
	}
}

// FromSDKDiagnostics converts SDK profile diagnostics to JSON DTOs.
func FromSDKDiagnostics(diagnostics []openshell.ProfileDiagnostic) []ProviderProfileDiagnostic {
	out := make([]ProviderProfileDiagnostic, 0, len(diagnostics))
	for _, d := range diagnostics {
		out = append(out, ProviderProfileDiagnostic{
			Source:    d.Source,
			ProfileID: d.ProfileID,
			Field:     d.Field,
			Message:   d.Message,
			Severity:  d.Severity,
		})
	}
	return out
}

func timePtrToMs(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}

// FromSDKWorkspace converts an SDK Workspace to the JSON DTO.
func FromSDKWorkspace(ws *openshell.Workspace) Workspace {
	if ws == nil {
		return Workspace{Phase: "UNSPECIFIED"}
	}
	phase := strings.ToUpper(string(ws.Phase))
	if phase == "" || phase == "UNKNOWN" {
		phase = "UNSPECIFIED"
	}
	return Workspace{
		Metadata: ObjectMeta{
			ID:                  ws.ID,
			Name:                ws.Name,
			Workspace:           ws.Workspace,
			Labels:              ws.Labels,
			Annotations:         ws.Annotations,
			CreatedAtMs:         timeToMs(ws.CreatedAt),
			ResourceVersion:     ws.ResourceVersion,
			DeletionTimestampMs: timePtrToMs(ws.DeletionTimestamp),
		},
		Phase: phase,
	}
}

// FromSDKWorkspaceMember converts an SDK WorkspaceMember to the JSON DTO.
func FromSDKWorkspaceMember(member *openshell.WorkspaceMember) WorkspaceMember {
	if member == nil {
		return WorkspaceMember{Role: "UNSPECIFIED"}
	}
	role := strings.ToUpper(string(member.Role))
	if role == "" || role == "UNKNOWN" {
		role = "UNSPECIFIED"
	}
	return WorkspaceMember{
		Metadata: ObjectMeta{
			ID:              member.ID,
			Name:            member.Name,
			Labels:          member.Labels,
			Annotations:     member.Annotations,
			CreatedAtMs:     timeToMs(member.CreatedAt),
			ResourceVersion: member.ResourceVersion,
		},
		PrincipalSubject: member.PrincipalSubject,
		Role:             role,
	}
}

// SDKWorkspaceRoleFromString maps USER/ADMIN to the SDK WorkspaceRole.
func SDKWorkspaceRoleFromString(role string) (openshell.WorkspaceRole, bool) {
	switch role {
	case "USER":
		return openshell.WorkspaceRoleUser, true
	case "ADMIN":
		return openshell.WorkspaceRoleAdmin, true
	}
	return "", false
}

func sdkPolicyLoadStatusString(status openshell.PolicyLoadStatus) string {
	switch status {
	case openshell.PolicyLoadStatusPending:
		return "PENDING"
	case openshell.PolicyLoadStatusLoaded:
		return "LOADED"
	case openshell.PolicyLoadStatusFailed:
		return "FAILED"
	case openshell.PolicyLoadStatusSuperseded:
		return "SUPERSEDED"
	}
	return "UNSPECIFIED"
}

// FromSDKPolicyRevision converts an SDK policy revision to the JSON DTO.
func FromSDKPolicyRevision(revision *openshell.SandboxPolicyRevision) PolicyRevision {
	if revision == nil {
		return PolicyRevision{Status: "UNSPECIFIED"}
	}
	out := PolicyRevision{
		Version:     revision.Version,
		PolicyHash:  revision.PolicyHash,
		Status:      sdkPolicyLoadStatusString(revision.Status),
		LoadError:   revision.LoadError,
		CreatedAtMs: timeToMs(revision.CreatedAt),
		LoadedAtMs:  timeToMs(revision.LoadedAt),
		Provenance:  revision.Provenance,
	}
	if revision.Policy != nil {
		out.Policy = marshalSDKPolicy(revision.Policy)
	}
	return out
}

// FromSDKPolicyStatus maps GetStatus into the dashboard view. Revisions is left
// empty — the handler fills history via GetStatus(WithVersion).
func FromSDKPolicyStatus(status *openshell.PolicyStatusResult) SandboxPolicyView {
	if status == nil {
		return SandboxPolicyView{Revisions: []PolicyRevision{}}
	}
	latest := FromSDKPolicyRevision(&status.Revision)
	return SandboxPolicyView{
		ActiveVersion: status.ActiveVersion,
		Latest:        &latest,
		Revisions:     []PolicyRevision{},
	}
}

// MarshalSDKNetworkPolicyRule converts an SDK NetworkPolicyRule to camelCase
// JSON, round-tripping through the proto for full fidelity.
func MarshalSDKNetworkPolicyRule(rule *openshell.NetworkPolicyRule) json.RawMessage {
	if rule == nil {
		return nil
	}
	raw, err := policyProtoMarshaler.Marshal(networkPolicyRuleToProto(rule))
	if err != nil {
		return nil
	}
	return raw
}

// ParseSDKNetworkPolicyRule parses camelCase JSON into an SDK NetworkPolicyRule,
// preserving every field via the proto round-trip.
func ParseSDKNetworkPolicyRule(raw json.RawMessage) (*openshell.NetworkPolicyRule, error) {
	var pb sbv1.NetworkPolicyRule
	if err := protojson.Unmarshal(raw, &pb); err != nil {
		return nil, err
	}
	return networkPolicyRuleFromProto(&pb), nil
}

// FromSDKEffectivePolicy converts the policy half of a sandbox's effective
// configuration to the JSON DTO.
func FromSDKEffectivePolicy(config *openshell.SandboxConfig) EffectivePolicy {
	if config == nil {
		return EffectivePolicy{}
	}
	out := EffectivePolicy{
		Version:                     config.PolicyVersion,
		PolicyHash:                  config.PolicyHash,
		PolicySource:                sdkPolicySourceString(config.PolicySource),
		GlobalPolicyVersion:         config.GlobalPolicyVersion,
		PolicyValidationFailureMode: config.PolicyValidationFailureMode,
	}
	if config.Policy != nil {
		out.Policy = marshalSDKPolicy(config.Policy)
	}
	return out
}

// FromSDKDraftPolicy converts an SDK DraftPolicy to the JSON DTO.
func FromSDKDraftPolicy(draft *openshell.DraftPolicy) DraftPolicy {
	if draft == nil {
		return DraftPolicy{Chunks: []PolicyChunk{}}
	}
	out := DraftPolicy{
		Chunks:           []PolicyChunk{},
		RollingSummary:   draft.RollingSummary,
		DraftVersion:     draft.DraftVersion,
		LastAnalyzedAtMs: timeToMs(draft.LastAnalyzedAt),
	}
	for i := range draft.Chunks {
		chunk := &draft.Chunks[i]
		item := PolicyChunk{
			ID:               chunk.ID,
			Status:           chunk.Status,
			RuleName:         chunk.RuleName,
			Rationale:        chunk.Rationale,
			SecurityNotes:    chunk.SecurityNotes,
			Confidence:       chunk.Confidence,
			CreatedAtMs:      timeToMs(chunk.CreatedAt),
			DecidedAtMs:      timeToMs(chunk.DecidedAt),
			HitCount:         chunk.HitCount,
			Binary:           chunk.Binary,
			ValidationResult: chunk.ValidationResult,
			RejectionReason:  chunk.RejectionReason,
			ReviewToken:      chunk.ReviewToken,
			ApplicationError: chunk.ApplicationError,

			Stage:             chunk.Stage,
			SupersedesChunkID: chunk.SupersedesChunkID,
			DenialSummaryIDs:  chunk.DenialSummaryIDs,
			FirstSeenMs:       timeToMs(chunk.FirstSeen),
			LastSeenMs:        timeToMs(chunk.LastSeen),

			CurrentEffectivePolicyHash:   chunk.CurrentEffectivePolicyHash,
			CandidateEffectivePolicyHash: chunk.CandidateEffectivePolicyHash,
		}
		if chunk.ProposedRule != nil {
			item.ProposedRule = MarshalSDKNetworkPolicyRule(chunk.ProposedRule)
		}
		if chunk.CurrentEffectivePolicy != nil {
			item.CurrentEffectivePolicy = marshalSDKPolicy(chunk.CurrentEffectivePolicy)
		}
		if chunk.CandidateEffectivePolicy != nil {
			item.CandidateEffectivePolicy = marshalSDKPolicy(chunk.CandidateEffectivePolicy)
		}
		out.Chunks = append(out.Chunks, item)
	}
	return out
}

// FromSDKDraftHistory converts SDK draft history entries to JSON DTOs.
func FromSDKDraftHistory(entries []openshell.DraftHistoryEntry) []DraftHistoryEntry {
	out := make([]DraftHistoryEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, DraftHistoryEntry{
			TimestampMs: timeToMs(e.Timestamp),
			EventType:   e.EventType,
			Description: e.Description,
			ChunkID:     e.ChunkID,
		})
	}
	return out
}

// FromSDKServiceEndpoint converts an SDK ServiceEndpoint to the JSON DTO.
func FromSDKServiceEndpoint(svc *openshell.ServiceEndpoint) ServiceEndpoint {
	if svc == nil {
		return ServiceEndpoint{}
	}
	return ServiceEndpoint{
		ID:          svc.ID,
		Workspace:   svc.Workspace,
		SandboxID:   svc.SandboxID,
		SandboxName: svc.Sandbox,
		ServiceName: svc.Name,
		TargetPort:  svc.TargetPort,
		Domain:      svc.Domain,
		URL:         svc.URL,
	}
}

// sdkSettingValueJSON renders a typed SDK setting value as the matching JSON
// type, and nil for a setting that has no value. No setting the gateway
// registers takes bytes; should one ever come back it is shown as hex.
func sdkSettingValueJSON(sv openshell.SettingValue) any {
	switch sv.Type {
	case openshell.SettingValueString:
		return sv.StringVal
	case openshell.SettingValueBool:
		return sv.BoolVal
	case openshell.SettingValueInt:
		return sv.IntVal
	case openshell.SettingValueBytes:
		return fmt.Sprintf("%x", sv.BytesVal)
	}
	return nil
}

// ErrSettingValue is what ParseSDKSettingValue returns for a value that is not
// one of the JSON types a setting can take.
var ErrSettingValue = errors.New("value must be a JSON string, boolean or integer")

// ParseSDKSettingValue reads a setting value from JSON into the gateway's
// typed SettingValue: a JSON string is a string value, a boolean a bool value
// and a whole number an int value. The gateway type-checks every setting and
// refuses a value of another kind, so the JSON type selects the kind and
// nothing is coerced: "true" stays a string.
func ParseSDKSettingValue(raw json.RawMessage) (*openshell.SettingValue, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrSettingValue
	}
	switch v := value.(type) {
	case string:
		return &openshell.SettingValue{Type: openshell.SettingValueString, StringVal: v}, nil
	case bool:
		return &openshell.SettingValue{Type: openshell.SettingValueBool, BoolVal: v}, nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return nil, ErrSettingValue
		}
		return &openshell.SettingValue{Type: openshell.SettingValueInt, IntVal: n}, nil
	}
	return nil, ErrSettingValue
}

// FromSDKGatewaySettings converts an SDK GatewayConfig to the JSON DTO.
func FromSDKGatewaySettings(config *openshell.GatewayConfig) GatewaySettings {
	if config == nil {
		return GatewaySettings{Settings: []SettingEntry{}}
	}
	out := GatewaySettings{
		Settings:         []SettingEntry{},
		SettingsRevision: config.SettingsRevision,
	}
	for key, val := range config.Settings {
		out.Settings = append(out.Settings, SettingEntry{
			Key:   key,
			Value: sdkSettingValueJSON(val),
		})
	}
	sort.Slice(out.Settings, func(i, j int) bool {
		return out.Settings[i].Key < out.Settings[j].Key
	})
	return out
}

// FromSDKCurrentUser converts an SDK CurrentUser to the JSON DTO.
func FromSDKCurrentUser(user *openshell.CurrentUser) CurrentUser {
	if user == nil {
		return CurrentUser{}
	}
	return CurrentUser{
		Subject:          user.Subject,
		DisplayName:      user.DisplayName,
		Roles:            user.Roles,
		Scopes:           user.Scopes,
		IdentityProvider: user.IdentityProvider,
	}
}

func sdkServiceStatusString(status openshell.ServiceStatus) string {
	switch status {
	case openshell.ServiceStatusHealthy:
		return GatewayStatusHealthy
	case openshell.ServiceStatusDegraded:
		return "DEGRADED"
	case openshell.ServiceStatusUnhealthy:
		return "UNHEALTHY"
	}
	return "UNSPECIFIED"
}

func sdkExtensionKindString(kind openshell.ExtensionKind) string {
	switch kind {
	case openshell.ExtensionKindComputeDriver:
		return "COMPUTE_DRIVER"
	case openshell.ExtensionKindCredentialDriver:
		return "CREDENTIAL_DRIVER"
	case openshell.ExtensionKindGatewayInterceptor:
		return "GATEWAY_INTERCEPTOR"
	case openshell.ExtensionKindSupervisorMiddleware:
		return "SUPERVISOR_MIDDLEWARE"
	}
	return "UNSPECIFIED"
}

// FromSDKGatewayInfo converts an SDK GatewayInfo to the JSON DTO.
func FromSDKGatewayInfo(info *openshell.GatewayInfo) GatewayInfo {
	if info == nil {
		return GatewayInfo{Status: "UNSPECIFIED", ComputeDrivers: []ComputeDriver{}, Extensions: []GatewayExtension{}}
	}
	out := GatewayInfo{
		Status:         sdkServiceStatusString(info.Status),
		GatewayVersion: info.Version,
		ComputeDrivers: []ComputeDriver{},
		Extensions:     []GatewayExtension{},
	}
	for _, driver := range info.ComputeDrivers {
		out.ComputeDrivers = append(out.ComputeDrivers, ComputeDriver{
			Name:          driver.Name,
			DriverName:    driver.DriverName,
			DriverVersion: driver.DriverVersion,
		})
	}
	for _, extension := range info.Extensions {
		out.Extensions = append(out.Extensions, GatewayExtension{
			Kind:                  sdkExtensionKindString(extension.Kind),
			ConfiguredName:        extension.ConfiguredName,
			ImplementationName:    extension.ImplementationName,
			ImplementationVersion: extension.ImplementationVersion,
			ProtocolMajor:         extension.ProtocolMajor,
			ProtocolMinor:         extension.ProtocolMinor,
			SupportedCapabilities: extension.SupportedCapabilities,
			RequiredCapabilities:  extension.RequiredCapabilities,
		})
	}
	return out
}

// FromSDKSandboxLogs converts an SDK LogResult to the JSON DTO.
func FromSDKSandboxLogs(result *openshell.LogResult) SandboxLogs {
	if result == nil {
		return SandboxLogs{Logs: []LogLine{}}
	}
	out := SandboxLogs{Logs: []LogLine{}, BufferTotal: result.BufferTotal}
	for _, line := range result.Lines {
		out.Logs = append(out.Logs, LogLine{
			TimestampMs: timeToMs(line.Timestamp),
			Level:       line.Level,
			Target:      line.Target,
			Message:     line.Message,
			Source:      line.Source,
			Fields:      line.Fields,
		})
	}
	return out
}

// FromProtoSandboxLogLine converts a protobuf SandboxLogLine to the JSON DTO
func FromProtoSandboxLogLine(line *pb.SandboxLogLine) LogLine {
	out := LogLine{
		SandboxID: line.GetSandboxId(),
		Level:     line.GetLevel(),
		Target:    line.GetTarget(),
		Message:   line.GetMessage(),
		Source:    line.GetSource(),
		Fields:    line.GetFields(),
	}
	if ts := line.GetEventTime(); ts != nil {
		out.TimestampMs = ts.AsTime().UnixMilli()
	}
	return out
}

// DeleteResult is the BFF's response envelope for every delete endpoint.
//
// Outcome carries the gateway's own answer. Deleted is the boolean the
// frontend reads, and is true only when completion is actually established:
// per the SDK (openshell/v1/types/mutations.go) that means DeletionCompleted
// or DeletionAlreadyAbsent. DeletionAccepted means the gateway queued
// asynchronous cleanup and the resource may still exist, and unrecognized
// numeric outcomes must not be treated as completion.
type DeleteResult struct {
	Outcome string `json:"outcome"`
	Deleted bool   `json:"deleted"`
}

// FromSDKDeletion converts an SDK DeletionResult into the BFF response.
// A nil result (an SDK call that reports no outcome) is reported as
// completed: a handler converts the result only after the call returned no
// error.
func FromSDKDeletion(res *openshell.DeletionResult) DeleteResult {
	if res == nil {
		return DeleteResult{Deleted: true, Outcome: "completed"}
	}
	switch res.Outcome {
	case openshell.DeletionCompleted:
		return DeleteResult{Deleted: true, Outcome: "completed"}
	case openshell.DeletionAlreadyAbsent:
		return DeleteResult{Deleted: true, Outcome: "already_absent"}
	case openshell.DeletionAccepted:
		return DeleteResult{Deleted: false, Outcome: "accepted"}
	default:
		return DeleteResult{Deleted: false, Outcome: "unspecified"}
	}
}
