package models

// This file bridges the SDK's domain policy types (which carry no JSON tags)
// and the proto SandboxPolicy, so we can (de)serialize policies with protojson:
// the camelCase, full-fidelity JSON the frontend reads and writes.
//
// The SDK's own domain<->proto converter lives in an internal package and is
// not importable, so the conversion below is a faithful port of
// openshell/v1/internal/converter/{policy,network_policy}.go (Apache-2.0).
// It covers EVERY field of the policy tree (L7 allow/deny rules, IP allowlists,
// multi-port, GraphQL persisted queries, MCP options, credential binding,
// signing, middleware, ...). If OpenShell adds a policy field upstream, add it
// here too — the round-trip tests in policyproto_test.go populate every field
// the SDK's proto declares, so a field missing here fails them by name.
//
// The same goes for the incremental merge operations at the end of the file:
// the browser sends them as protojson of openshell.v1.PolicyMergeOperation and
// the SDK takes its own typed form, so they are converted in one direction.
//
// One field is not carried, because the SDK's types have no place for it:
// NetworkEndpoint.token_grant_owner (OpenShell 0.1.3), which the gateway
// derives and ignores when a user writes it. The tests name it in
// policyFieldsTheSDKCannotCarry.

import (
	"encoding/json"
	"fmt"

	ostypes "github.com/ai-openshell/sdk-go/openshell/v1/types"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

func copyStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

// --- SandboxPolicy ---

func sandboxPolicyFromProto(p *sbv1.SandboxPolicy) *ostypes.SandboxPolicy {
	if p == nil {
		return nil
	}
	result := &ostypes.SandboxPolicy{
		Version:    p.GetVersion(),
		Filesystem: filesystemPolicyFromProto(p.GetFilesystem()),
		Landlock:   landlockPolicyFromProto(p.GetLandlock()),
		Process:    processPolicyFromProto(p.GetProcess()),
	}
	if np := p.GetNetworkPolicies(); np != nil {
		result.NetworkPolicies = make(map[string]ostypes.NetworkPolicyRule, len(np))
		for k, v := range np {
			if converted := networkPolicyRuleFromProto(v); converted != nil {
				result.NetworkPolicies[k] = *converted
			}
		}
	}
	if mw := p.GetNetworkMiddlewares(); mw != nil {
		result.NetworkMiddlewares = make(map[string]ostypes.NetworkMiddlewareConfig, len(mw))
		for k, v := range mw {
			if v != nil {
				result.NetworkMiddlewares[k] = middlewareConfigFromProto(v)
			}
		}
	}
	return result
}

func sandboxPolicyToProto(p *ostypes.SandboxPolicy) *sbv1.SandboxPolicy {
	if p == nil {
		return nil
	}
	result := &sbv1.SandboxPolicy{
		Version:    p.Version,
		Filesystem: filesystemPolicyToProto(p.Filesystem),
		Landlock:   landlockPolicyToProto(p.Landlock),
		Process:    processPolicyToProto(p.Process),
	}
	if p.NetworkPolicies != nil {
		result.NetworkPolicies = make(map[string]*sbv1.NetworkPolicyRule, len(p.NetworkPolicies))
		for k, v := range p.NetworkPolicies {
			rule := v
			result.NetworkPolicies[k] = networkPolicyRuleToProto(&rule)
		}
	}
	if p.NetworkMiddlewares != nil {
		result.NetworkMiddlewares = make(map[string]*sbv1.NetworkMiddlewareConfig, len(p.NetworkMiddlewares))
		for k, v := range p.NetworkMiddlewares {
			mw := v
			result.NetworkMiddlewares[k] = middlewareConfigToProto(&mw)
		}
	}
	return result
}

func middlewareConfigFromProto(m *sbv1.NetworkMiddlewareConfig) ostypes.NetworkMiddlewareConfig {
	result := ostypes.NetworkMiddlewareConfig{
		Name:       m.GetName(),
		Middleware: m.GetMiddleware(),
		OnError:    m.GetOnError(),
		Order:      m.GetOrder(),
	}
	if c := m.GetConfig(); c != nil {
		result.Config = c.AsMap()
	}
	if ep := m.GetEndpoints(); ep != nil {
		result.Endpoints = &ostypes.MiddlewareEndpointSelector{
			Include: copyStringSlice(ep.GetInclude()),
			Exclude: copyStringSlice(ep.GetExclude()),
		}
	}
	return result
}

func middlewareConfigToProto(m *ostypes.NetworkMiddlewareConfig) *sbv1.NetworkMiddlewareConfig {
	result := &sbv1.NetworkMiddlewareConfig{
		Name:       m.Name,
		Middleware: m.Middleware,
		OnError:    m.OnError,
		Order:      m.Order,
	}
	if m.Config != nil {
		// Config originates from frontend JSON (structpb.AsMap round-trips), so
		// it is always re-serializable; drop silently on the rare error.
		if s, err := structpb.NewStruct(m.Config); err == nil {
			result.Config = s
		}
	}
	if m.Endpoints != nil {
		result.Endpoints = &sbv1.MiddlewareEndpointSelector{
			Include: copyStringSlice(m.Endpoints.Include),
			Exclude: copyStringSlice(m.Endpoints.Exclude),
		}
	}
	return result
}

func filesystemPolicyFromProto(f *sbv1.FilesystemPolicy) *ostypes.FilesystemPolicy {
	if f == nil {
		return nil
	}
	return &ostypes.FilesystemPolicy{
		IncludeWorkdir: f.GetIncludeWorkdir(),
		ReadOnly:       copyStringSlice(f.GetReadOnly()),
		ReadWrite:      copyStringSlice(f.GetReadWrite()),
	}
}

func filesystemPolicyToProto(f *ostypes.FilesystemPolicy) *sbv1.FilesystemPolicy {
	if f == nil {
		return nil
	}
	return &sbv1.FilesystemPolicy{
		IncludeWorkdir: f.IncludeWorkdir,
		ReadOnly:       copyStringSlice(f.ReadOnly),
		ReadWrite:      copyStringSlice(f.ReadWrite),
	}
}

func landlockPolicyFromProto(l *sbv1.LandlockPolicy) *ostypes.LandlockPolicy {
	if l == nil {
		return nil
	}
	return &ostypes.LandlockPolicy{Compatibility: l.GetCompatibility()}
}

func landlockPolicyToProto(l *ostypes.LandlockPolicy) *sbv1.LandlockPolicy {
	if l == nil {
		return nil
	}
	return &sbv1.LandlockPolicy{Compatibility: l.Compatibility}
}

func processPolicyFromProto(p *sbv1.ProcessPolicy) *ostypes.ProcessPolicy {
	if p == nil {
		return nil
	}
	return &ostypes.ProcessPolicy{
		RunAsUser:  p.GetRunAsUser(),
		RunAsGroup: p.GetRunAsGroup(),
	}
}

func processPolicyToProto(p *ostypes.ProcessPolicy) *sbv1.ProcessPolicy {
	if p == nil {
		return nil
	}
	return &sbv1.ProcessPolicy{
		RunAsUser:  p.RunAsUser,
		RunAsGroup: p.RunAsGroup,
	}
}

// --- NetworkPolicyRule ---

func networkPolicyRuleFromProto(r *sbv1.NetworkPolicyRule) *ostypes.NetworkPolicyRule {
	if r == nil {
		return nil
	}
	result := &ostypes.NetworkPolicyRule{Name: r.GetName()}
	if eps := r.GetEndpoints(); len(eps) > 0 {
		result.Endpoints = make([]ostypes.PolicyNetworkEndpoint, len(eps))
		for i, ep := range eps {
			if ep != nil {
				result.Endpoints[i] = policyNetworkEndpointFromProto(ep)
			}
		}
	}
	if bins := r.GetBinaries(); len(bins) > 0 {
		result.Binaries = make([]ostypes.PolicyNetworkBinary, len(bins))
		for i, b := range bins {
			if b != nil {
				result.Binaries[i] = ostypes.PolicyNetworkBinary{Path: b.GetPath()}
			}
		}
	}
	return result
}

func networkPolicyRuleToProto(r *ostypes.NetworkPolicyRule) *sbv1.NetworkPolicyRule {
	if r == nil {
		return nil
	}
	result := &sbv1.NetworkPolicyRule{Name: r.Name}
	if len(r.Endpoints) > 0 {
		result.Endpoints = make([]*sbv1.NetworkEndpoint, len(r.Endpoints))
		for i := range r.Endpoints {
			result.Endpoints[i] = policyNetworkEndpointToProto(&r.Endpoints[i])
		}
	}
	if len(r.Binaries) > 0 {
		result.Binaries = make([]*sbv1.NetworkBinary, len(r.Binaries))
		for i := range r.Binaries {
			result.Binaries[i] = &sbv1.NetworkBinary{Path: r.Binaries[i].Path}
		}
	}
	return result
}

// --- PolicyNetworkEndpoint ---

func policyNetworkEndpointFromProto(ep *sbv1.NetworkEndpoint) ostypes.PolicyNetworkEndpoint {
	result := ostypes.PolicyNetworkEndpoint{
		Host:                         ep.GetHost(),
		Port:                         ep.GetPort(),
		Protocol:                     ep.GetProtocol(),
		TLS:                          ostypes.NetworkTLSMode(ep.GetTls()),
		Enforcement:                  ostypes.NetworkEnforcementMode(ep.GetEnforcement()),
		Access:                       ostypes.NetworkAccessPreset(ep.GetAccess()),
		AllowEncodedSlash:            ep.GetAllowEncodedSlash(),
		PersistedQueries:             ep.GetPersistedQueries(),
		GraphqlMaxBodyBytes:          ep.GetGraphqlMaxBodyBytes(),
		Path:                         ep.GetPath(),
		WebsocketCredentialRewrite:   ep.GetWebsocketCredentialRewrite(),
		RequestBodyCredentialRewrite: ep.GetRequestBodyCredentialRewrite(),
		AllowUninspectedCredentials:  ep.GetAllowUninspectedCredentials(),
		ProviderCredentialed:         ep.GetProviderCredentialed(),
		AdvisorProposed:              ep.GetAdvisorProposed(),
		CredentialSigning:            ep.GetCredentialSigning(),
		SigningService:               ep.GetSigningService(),
		SigningRegion:                ep.GetSigningRegion(),
		JSONRPCMaxBodyBytes:          ep.GetJsonRpcMaxBodyBytes(),
	}
	if binding := ep.GetCredentialBinding(); binding != nil {
		result.CredentialBinding = &ostypes.NetworkCredentialBinding{Provider: binding.GetProvider()}
	}
	if ports := ep.GetPorts(); len(ports) > 0 {
		result.Ports = make([]uint32, len(ports))
		copy(result.Ports, ports)
	}
	if ips := ep.GetAllowedIps(); len(ips) > 0 {
		result.AllowedIPs = copyStringSlice(ips)
	}
	if rules := ep.GetRules(); len(rules) > 0 {
		result.Rules = make([]ostypes.L7Rule, len(rules))
		for i, r := range rules {
			if r != nil {
				result.Rules[i] = l7RuleFromProto(r)
			}
		}
	}
	if deny := ep.GetDenyRules(); len(deny) > 0 {
		result.DenyRules = make([]ostypes.L7DenyRule, len(deny))
		for i, r := range deny {
			if r != nil {
				result.DenyRules[i] = l7DenyRuleFromProto(r)
			}
		}
	}
	if gql := ep.GetGraphqlPersistedQueries(); len(gql) > 0 {
		result.GraphqlPersistedQueries = make(map[string]ostypes.GraphqlOperation, len(gql))
		for k, v := range gql {
			if v != nil {
				result.GraphqlPersistedQueries[k] = graphqlOperationFromProto(v)
			}
		}
	}
	result.Mcp = mcpOptionsFromProto(ep.GetMcp())
	return result
}

func policyNetworkEndpointToProto(ep *ostypes.PolicyNetworkEndpoint) *sbv1.NetworkEndpoint {
	result := &sbv1.NetworkEndpoint{
		Host:                         ep.Host,
		Port:                         ep.Port,
		Protocol:                     ep.Protocol,
		Tls:                          sbv1.NetworkTlsMode(ep.TLS),
		Enforcement:                  sbv1.NetworkEnforcementMode(ep.Enforcement),
		Access:                       sbv1.NetworkAccessPreset(ep.Access),
		AllowEncodedSlash:            ep.AllowEncodedSlash,
		PersistedQueries:             ep.PersistedQueries,
		GraphqlMaxBodyBytes:          ep.GraphqlMaxBodyBytes,
		Path:                         ep.Path,
		WebsocketCredentialRewrite:   ep.WebsocketCredentialRewrite,
		RequestBodyCredentialRewrite: ep.RequestBodyCredentialRewrite,
		AllowUninspectedCredentials:  ep.AllowUninspectedCredentials,
		ProviderCredentialed:         ep.ProviderCredentialed,
		AdvisorProposed:              ep.AdvisorProposed,
		CredentialSigning:            ep.CredentialSigning,
		SigningService:               ep.SigningService,
		SigningRegion:                ep.SigningRegion,
		JsonRpcMaxBodyBytes:          ep.JSONRPCMaxBodyBytes,
	}
	if ep.CredentialBinding != nil {
		result.CredentialBinding = &sbv1.NetworkCredentialBinding{Provider: ep.CredentialBinding.Provider}
	}
	if len(ep.Ports) > 0 {
		result.Ports = make([]uint32, len(ep.Ports))
		copy(result.Ports, ep.Ports)
	}
	if len(ep.AllowedIPs) > 0 {
		result.AllowedIps = copyStringSlice(ep.AllowedIPs)
	}
	if len(ep.Rules) > 0 {
		result.Rules = make([]*sbv1.L7Rule, len(ep.Rules))
		for i := range ep.Rules {
			result.Rules[i] = l7RuleToProto(&ep.Rules[i])
		}
	}
	if len(ep.DenyRules) > 0 {
		result.DenyRules = make([]*sbv1.L7DenyRule, len(ep.DenyRules))
		for i := range ep.DenyRules {
			result.DenyRules[i] = l7DenyRuleToProto(&ep.DenyRules[i])
		}
	}
	if len(ep.GraphqlPersistedQueries) > 0 {
		result.GraphqlPersistedQueries = make(map[string]*sbv1.GraphqlOperation, len(ep.GraphqlPersistedQueries))
		for k, v := range ep.GraphqlPersistedQueries {
			op := v
			result.GraphqlPersistedQueries[k] = graphqlOperationToProto(&op)
		}
	}
	result.Mcp = mcpOptionsToProto(ep.Mcp)
	return result
}

// --- McpOptions ---

func mcpOptionsFromProto(m *sbv1.McpOptions) *ostypes.McpOptions {
	if m == nil {
		return nil
	}
	return &ostypes.McpOptions{
		StrictToolNames:         copyBoolPtr(m.StrictToolNames),
		AllowAllKnownMcpMethods: copyBoolPtr(m.AllowAllKnownMcpMethods),
		// The gateway replaces an empty list with its pinned default revision,
		// so dropping this turns an explicit allowlist into the default one on
		// the next save.
		Versions: copyStringSlice(m.GetVersions()),
	}
}

func mcpOptionsToProto(m *ostypes.McpOptions) *sbv1.McpOptions {
	if m == nil {
		return nil
	}
	return &sbv1.McpOptions{
		StrictToolNames:         copyBoolPtr(m.StrictToolNames),
		AllowAllKnownMcpMethods: copyBoolPtr(m.AllowAllKnownMcpMethods),
		Versions:                copyStringSlice(m.Versions),
	}
}

// --- L7Rule ---

func l7RuleFromProto(r *sbv1.L7Rule) ostypes.L7Rule {
	result := ostypes.L7Rule{}
	if a := r.GetAllow(); a != nil {
		result.Allow = &ostypes.L7Allow{
			Method:        a.GetMethod(),
			Path:          a.GetPath(),
			Command:       a.GetCommand(),
			OperationType: a.GetOperationType(),
			OperationName: a.GetOperationName(),
			Fields:        copyStringSlice(a.GetFields()),
		}
		if q := a.GetQuery(); len(q) > 0 {
			result.Allow.Query = l7QueryMapFromProto(q)
		}
		if p := a.GetParams(); len(p) > 0 {
			result.Allow.Params = l7QueryMapFromProto(p)
		}
	}
	return result
}

func l7RuleToProto(r *ostypes.L7Rule) *sbv1.L7Rule {
	result := &sbv1.L7Rule{}
	if r.Allow != nil {
		result.Allow = &sbv1.L7Allow{
			Method:        r.Allow.Method,
			Path:          r.Allow.Path,
			Command:       r.Allow.Command,
			OperationType: r.Allow.OperationType,
			OperationName: r.Allow.OperationName,
			Fields:        copyStringSlice(r.Allow.Fields),
		}
		if len(r.Allow.Query) > 0 {
			result.Allow.Query = l7QueryMapToProto(r.Allow.Query)
		}
		if len(r.Allow.Params) > 0 {
			result.Allow.Params = l7QueryMapToProto(r.Allow.Params)
		}
	}
	return result
}

// --- L7DenyRule ---

func l7DenyRuleFromProto(r *sbv1.L7DenyRule) ostypes.L7DenyRule {
	return ostypes.L7DenyRule{
		Method:        r.GetMethod(),
		Path:          r.GetPath(),
		Command:       r.GetCommand(),
		OperationType: r.GetOperationType(),
		OperationName: r.GetOperationName(),
		Fields:        copyStringSlice(r.GetFields()),
		Query:         l7QueryMapFromProto(r.GetQuery()),
		Params:        l7QueryMapFromProto(r.GetParams()),
	}
}

func l7DenyRuleToProto(r *ostypes.L7DenyRule) *sbv1.L7DenyRule {
	result := &sbv1.L7DenyRule{
		Method:        r.Method,
		Path:          r.Path,
		Command:       r.Command,
		OperationType: r.OperationType,
		OperationName: r.OperationName,
		Fields:        copyStringSlice(r.Fields),
	}
	if len(r.Query) > 0 {
		result.Query = l7QueryMapToProto(r.Query)
	}
	if len(r.Params) > 0 {
		result.Params = l7QueryMapToProto(r.Params)
	}
	return result
}

// --- L7QueryMatcher helpers ---

func l7QueryMapFromProto(m map[string]*sbv1.L7QueryMatcher) map[string]ostypes.L7QueryMatcher {
	if len(m) == 0 {
		return nil
	}
	result := make(map[string]ostypes.L7QueryMatcher, len(m))
	for k, v := range m {
		if v != nil {
			result[k] = ostypes.L7QueryMatcher{
				Glob: v.GetGlob(),
				Any:  copyStringSlice(v.GetAny()),
			}
		}
	}
	return result
}

func l7QueryMapToProto(m map[string]ostypes.L7QueryMatcher) map[string]*sbv1.L7QueryMatcher {
	if len(m) == 0 {
		return nil
	}
	result := make(map[string]*sbv1.L7QueryMatcher, len(m))
	for k, v := range m {
		result[k] = &sbv1.L7QueryMatcher{
			Glob: v.Glob,
			Any:  copyStringSlice(v.Any),
		}
	}
	return result
}

// --- GraphqlOperation ---

func graphqlOperationFromProto(op *sbv1.GraphqlOperation) ostypes.GraphqlOperation {
	return ostypes.GraphqlOperation{
		OperationType: op.GetOperationType(),
		OperationName: op.GetOperationName(),
		Fields:        copyStringSlice(op.GetFields()),
	}
}

func graphqlOperationToProto(op *ostypes.GraphqlOperation) *sbv1.GraphqlOperation {
	return &sbv1.GraphqlOperation{
		OperationType: op.OperationType,
		OperationName: op.OperationName,
		Fields:        copyStringSlice(op.Fields),
	}
}

// --- PolicyMergeOperation ---

// ParseSDKPolicyMergeOperations parses incremental policy operations, each the
// protojson form of openshell.v1.PolicyMergeOperation, into the SDK's typed
// operations. These are what `openshell policy update` sends: the gateway
// applies them to the sandbox's latest policy itself, so the policy does not
// make a round trip through the browser. An error names the operation it is
// about by its position in the request.
func ParseSDKPolicyMergeOperations(raw []json.RawMessage) ([]ostypes.PolicyMergeOperation, error) {
	operations := make([]ostypes.PolicyMergeOperation, 0, len(raw))
	for i, item := range raw {
		var op pb.PolicyMergeOperation
		if err := protojson.Unmarshal(item, &op); err != nil {
			return nil, fmt.Errorf("operations[%d]: %w", i, err)
		}
		converted, ok := policyMergeOperationFromProto(&op)
		if !ok {
			return nil, fmt.Errorf("operations[%d]: one of addRule, removeEndpoint, removeRule, "+
				"addDenyRules, addAllowRules or removeBinary is required", i)
		}
		operations = append(operations, converted)
	}
	return operations, nil
}

// policyMergeOperationFromProto converts one incremental policy operation to
// the SDK's typed form. It mirrors the SDK's own PolicyMergeOperationToProto
// (openshell/v1/internal/converter/setting.go), which runs on the way out, so
// the request the gateway receives is the one the browser sent. It reports
// false for an operation that names none of the six variants.
func policyMergeOperationFromProto(op *pb.PolicyMergeOperation) (ostypes.PolicyMergeOperation, bool) {
	switch v := op.GetOperation().(type) {
	case *pb.PolicyMergeOperation_AddRule:
		add := &ostypes.AddNetworkRule{RuleName: v.AddRule.GetRuleName()}
		if rule := networkPolicyRuleFromProto(v.AddRule.GetRule()); rule != nil {
			add.Rule = *rule
		}
		return ostypes.PolicyMergeOperation{AddRule: add}, true
	case *pb.PolicyMergeOperation_RemoveEndpoint:
		return ostypes.PolicyMergeOperation{RemoveEndpoint: &ostypes.RemoveNetworkEndpoint{
			RuleName: v.RemoveEndpoint.GetRuleName(),
			Host:     v.RemoveEndpoint.GetHost(),
			Port:     v.RemoveEndpoint.GetPort(),
		}}, true
	case *pb.PolicyMergeOperation_RemoveRule:
		return ostypes.PolicyMergeOperation{RemoveRule: &ostypes.RemoveNetworkRule{
			RuleName: v.RemoveRule.GetRuleName(),
		}}, true
	case *pb.PolicyMergeOperation_AddDenyRules:
		add := &ostypes.AddDenyRules{Target: l7RuleTargetFromProto(v.AddDenyRules.GetTarget())}
		if deny := v.AddDenyRules.GetDenyRules(); len(deny) > 0 {
			add.DenyRules = make([]ostypes.L7DenyRule, len(deny))
			for i, r := range deny {
				if r != nil {
					add.DenyRules[i] = l7DenyRuleFromProto(r)
				}
			}
		}
		return ostypes.PolicyMergeOperation{AddDenyRules: add}, true
	case *pb.PolicyMergeOperation_AddAllowRules:
		add := &ostypes.AddAllowRules{Target: l7RuleTargetFromProto(v.AddAllowRules.GetTarget())}
		if rules := v.AddAllowRules.GetRules(); len(rules) > 0 {
			add.Rules = make([]ostypes.L7Rule, len(rules))
			for i, r := range rules {
				if r != nil {
					add.Rules[i] = l7RuleFromProto(r)
				}
			}
		}
		return ostypes.PolicyMergeOperation{AddAllowRules: add}, true
	case *pb.PolicyMergeOperation_RemoveBinary:
		return ostypes.PolicyMergeOperation{RemoveBinary: &ostypes.RemoveNetworkBinary{
			RuleName:   v.RemoveBinary.GetRuleName(),
			BinaryPath: v.RemoveBinary.GetBinaryPath(),
		}}, true
	}
	return ostypes.PolicyMergeOperation{}, false
}

// l7RuleTargetFromProto copies the declared scope as sent. Whether path is
// present matters as much as its value: an empty path selects the endpoint
// that has no path, and an absent one asks the gateway for a unique match.
func l7RuleTargetFromProto(target *pb.L7RuleTarget) *ostypes.L7RuleTarget {
	if target == nil {
		return nil
	}
	result := &ostypes.L7RuleTarget{
		RuleName:  target.GetRuleName(),
		Host:      target.GetHost(),
		AnyBinary: target.GetAnyBinary(),
	}
	if ports := target.GetPorts(); len(ports) > 0 {
		result.Ports = make([]uint32, len(ports))
		copy(result.Ports, ports)
	}
	if target.Path != nil {
		path := target.GetPath()
		result.Path = &path
	}
	if bins := target.GetBinaries(); len(bins) > 0 {
		result.Binaries = make([]ostypes.PolicyNetworkBinary, len(bins))
		for i, b := range bins {
			if b != nil {
				result.Binaries[i] = ostypes.PolicyNetworkBinary{Path: b.GetPath()}
			}
		}
	}
	return result
}
