package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
)

// This file holds the provider profile DTOs that nest under ProviderProfile
// and ProfileCredential (models.go) and the conversion between those DTOs and
// the gateway's own message, openshell.v1.ProviderProfile.
//
// The conversion is to the proto message and not to the SDK's curated type
// because the SDK type is narrower: its NetworkEndpoint carries a host, a port
// and a protocol, and a profile endpoint is a whole
// openshell.sandbox.v1.NetworkEndpoint (access preset, enforcement, TLS mode,
// L7 rules and so on), which decides what a sandbox with the provider attached
// may reach. Reading or writing a profile through the narrower type drops
// that. provider_profile_sdk.go converts between the proto message and the SDK
// type, for the store that reaches profiles through the SDK.
//
// Nothing in a profile is secret: it is the schema of a provider type, and no
// field of ProviderProfile or of the messages under it carries the proto
// `secret` option. ProviderCredentialRefreshMaterial.secret is a flag that
// says an input is secret, not the input.

// ProfileCredentialRefresh mirrors openshell.v1.ProviderCredentialRefresh.
//
// Strategy is the enum name without its prefix (OAUTH2_CLIENT_CREDENTIALS).
// RefreshBefore and MaxLifetime are protobuf durations in their JSON form
// ("300s", "1.500s") and empty when the profile does not set them, which is
// not the same as "0s": an absent refresh_before takes the gateway's default
// and an explicit zero refreshes at expiry.
type ProfileCredentialRefresh struct {
	Strategy          string                   `json:"strategy"`
	TokenURL          string                   `json:"tokenUrl,omitempty"`
	RefreshBefore     string                   `json:"refreshBefore,omitempty"`
	MaxLifetime       string                   `json:"maxLifetime,omitempty"`
	Scopes            []string                 `json:"scopes,omitempty"`
	Material          []ProfileRefreshMaterial `json:"material,omitempty"`
	AdditionalOutputs []ProfileRefreshOutput   `json:"additionalOutputs,omitempty"`
}

// ProfileRefreshMaterial mirrors openshell.v1.ProviderCredentialRefreshMaterial:
// one input a refresh strategy needs. Secret says the input is a secret; the
// input itself is supplied when refresh is configured on a provider.
type ProfileRefreshMaterial struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
}

// ProfileRefreshOutput mirrors openshell.v1.ProviderCredentialRefreshOutput:
// a further credential the same refresh mints.
type ProfileRefreshOutput struct {
	Output     string `json:"output"`
	Credential string `json:"credential"`
}

// ProfileTokenGrant mirrors openshell.v1.ProviderCredentialTokenGrant: a
// credential the sandbox obtains through an OAuth2 grant when it is needed.
// GrantType is the enum name without its prefix (CLIENT_CREDENTIALS,
// TOKEN_EXCHANGE, UNSPECIFIED) and CacheTTL a protobuf duration in its JSON
// form, empty when not set.
type ProfileTokenGrant struct {
	SubjectToken        *ProfileTokenGrantSubjectToken      `json:"subjectToken,omitempty"`
	GrantType           string                              `json:"grantType"`
	TokenEndpoint       string                              `json:"tokenEndpoint"`
	Audience            string                              `json:"audience,omitempty"`
	JWTSVIDAudience     string                              `json:"jwtSvidAudience,omitempty"`
	ClientAssertionType string                              `json:"clientAssertionType,omitempty"`
	RequestedTokenType  string                              `json:"requestedTokenType,omitempty"`
	CacheTTL            string                              `json:"cacheTtl,omitempty"`
	Scopes              []string                            `json:"scopes,omitempty"`
	AudienceOverrides   []ProfileTokenGrantAudienceOverride `json:"audienceOverrides,omitempty"`
}

// ProfileTokenGrantSubjectToken mirrors
// openshell.v1.ProviderCredentialTokenGrantSubjectToken.
type ProfileTokenGrantSubjectToken struct {
	Source           string `json:"source"`
	Credential       string `json:"credential"`
	SubjectTokenType string `json:"subjectTokenType,omitempty"`
}

// ProfileTokenGrantAudienceOverride mirrors
// openshell.v1.ProviderCredentialTokenGrantAudienceOverride.
type ProfileTokenGrantAudienceOverride struct {
	Host     string   `json:"host,omitempty"`
	Path     string   `json:"path,omitempty"`
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes,omitempty"`
	Port     uint32   `json:"port,omitempty"`
}

// ProfileBinary mirrors openshell.sandbox.v1.NetworkBinary: an executable
// allowed to reach the profile's endpoints.
type ProfileBinary struct {
	Path string `json:"path"`
}

// ProfileDiscovery mirrors openshell.v1.ProviderProfileDiscovery.
type ProfileDiscovery struct {
	Credentials []string `json:"credentials,omitempty"`
}

// ProfileFile mirrors openshell.v1.ProviderProfileFile: a file that a sandbox
// with the provider attached finds under /run/openshell/providers/<provider>/.
// Content is a template in which {{config.KEY}} stands for a value of the
// provider's config, and EnvVar names an environment variable that is given
// the file's path. Upstream added the message in OpenShell 0.1.3 and marks it
// experimental: it may change or be removed.
type ProfileFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	EnvVar  string `json:"envVar,omitempty"`
}

// ProfileEndpointInput is the short form of an endpoint: a host, a port and a
// protocol. Anything more goes in ProviderProfileInput.NetworkEndpoints.
//
// A profile that was read carries something else under the same name: the
// "host:port" summary of each endpoint, as a string (ProviderProfile.Endpoints).
// Such an entry is accepted and remembered as a summary, so that a profile
// can be read, changed and sent back without first deleting a field. It is
// never taken for an endpoint: see endpointsToProto.
type ProfileEndpointInput struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol,omitempty"`
	Port     uint32 `json:"port,omitempty"`
	summary  bool
}

// UnmarshalJSON reads an endpoint object, or the summary string of one.
func (e *ProfileEndpointInput) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '"' {
		*e = ProfileEndpointInput{summary: true}
		return nil
	}
	// The plain type has the fields without this method, and the decoder
	// refuses a field it does not know, as the body's own decoder does.
	type plain ProfileEndpointInput
	var endpoint plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&endpoint); err != nil {
		return err
	}
	*e = ProfileEndpointInput(endpoint)
	return nil
}

// ProviderProfileInput is a provider profile as a client writes it, for
// import, update and lint. It carries every field of
// openshell.v1.ProviderProfile and is forwarded as it is.
//
// The gateway replaces a stored profile with the one an update sends, so what
// an update leaves out is removed. A client that changes a profile reads it,
// changes what it means to, and sends all of it back.
//
// NetworkEndpoints takes each endpoint whole, as ProviderProfile returns them:
// one protojson openshell.sandbox.v1.NetworkEndpoint per entry. Endpoints is
// the short form (ProfileEndpointInput). A request sends one or the other,
// except that the "host:port" summaries a read profile carries in Endpoints
// may come back beside NetworkEndpoints and are then ignored.
//
// Source and Scope are set by the gateway and ignored when written; they are
// accepted so that a profile that was read, or exported to a file, can be sent
// back unchanged. The same goes for a credential's TokenGrantOwners (see
// ProfileCredential). ImportSource is not part of the profile: it is
// ProviderProfileImportItem.source, a label the gateway repeats in the
// diagnostics it returns for this profile. The CLI sends the file path.
type ProviderProfileInput struct {
	Discovery        *ProfileDiscovery      `json:"discovery,omitempty"`
	Annotations      map[string]string      `json:"annotations,omitempty"`
	ID               string                 `json:"id"`
	DisplayName      string                 `json:"displayName"`
	Description      string                 `json:"description,omitempty"`
	Category         string                 `json:"category"`
	Source           string                 `json:"source,omitempty"`
	Scope            string                 `json:"scope,omitempty"`
	ImportSource     string                 `json:"importSource,omitempty"`
	Credentials      []ProfileCredential    `json:"credentials,omitempty"`
	Files            []ProfileFile          `json:"files,omitempty"`
	Endpoints        []ProfileEndpointInput `json:"endpoints,omitempty"`
	NetworkEndpoints []json.RawMessage      `json:"networkEndpoints,omitempty"`
	Binaries         []ProfileBinary        `json:"binaries,omitempty"`
	InferenceCapable bool                   `json:"inferenceCapable"`
	ResourceVersion  uint64                 `json:"resourceVersion,omitempty"`
}

const (
	profileCategoryPrefix = "PROVIDER_PROFILE_CATEGORY_"
	refreshStrategyPrefix = "PROVIDER_CREDENTIAL_REFRESH_STRATEGY_"
	grantTypePrefix       = "PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_"
	enumUnspecified       = "UNSPECIFIED"
)

// enumName is a proto enum value as the DTOs spell it: its name without the
// enum's prefix. A number the SDK's proto has no name for, which a newer
// gateway can send, is written as that number so that it survives being read
// and written back.
func enumName(names map[int32]string, prefix string, value int32) string {
	if name, ok := names[value]; ok {
		return strings.TrimPrefix(name, prefix)
	}
	return strconv.FormatInt(int64(value), 10)
}

// enumValue reads what enumName wrote. An empty string is the unspecified
// value, which every one of these enums numbers 0.
func enumValue(values map[string]int32, prefix, name string) (int32, bool) {
	if name == "" {
		return 0, true
	}
	if value, ok := values[prefix+name]; ok {
		return value, true
	}
	number, err := strconv.ParseInt(name, 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(number), true
}

// formatProfileDuration writes a protobuf duration in its JSON form, and an
// absent one as the empty string.
func formatProfileDuration(d *durationpb.Duration) string {
	if d == nil {
		return ""
	}
	raw, err := protojson.Marshal(d)
	if err == nil {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
	}
	// Out of the range protojson will write. The gateway refuses such a
	// duration itself; it is shown as it is rather than hidden.
	return fmt.Sprintf("%d.%09ds", d.GetSeconds(), d.GetNanos())
}

// parseProfileDuration reads a protobuf duration from its JSON form. The empty
// string is an absent duration.
func parseProfileDuration(text string) (*durationpb.Duration, error) {
	if text == "" {
		return nil, nil
	}
	raw, err := json.Marshal(text)
	if err != nil {
		return nil, err
	}
	d := &durationpb.Duration{}
	if err := protojson.Unmarshal(raw, d); err != nil {
		return nil, fmt.Errorf("%q is not a duration such as \"300s\"", text)
	}
	return d, nil
}

// FromProtoProviderProfile converts a provider profile the gateway returned to
// the JSON DTO, endpoints included whole.
func FromProtoProviderProfile(profile *pb.ProviderProfile) (ProviderProfile, error) {
	out := profileWithoutEndpoints(profile)
	for index, endpoint := range profile.GetEndpoints() {
		out.Endpoints = appendEndpointSummary(out.Endpoints, endpoint)
		raw, err := policyProtoMarshaler.Marshal(endpoint)
		if err != nil {
			return ProviderProfile{}, fmt.Errorf("endpoints[%d]: %w", index, err)
		}
		out.NetworkEndpoints = append(out.NetworkEndpoints, raw)
	}
	return out, nil
}

// FromNarrowProviderProfile converts a profile whose endpoints are known to be
// incomplete, because it was read through the SDK. It summarizes them and
// leaves NetworkEndpoints out: three fields of an endpoint presented as the
// endpoint would be written back as one.
func FromNarrowProviderProfile(profile *pb.ProviderProfile) ProviderProfile {
	out := profileWithoutEndpoints(profile)
	for _, endpoint := range profile.GetEndpoints() {
		out.Endpoints = appendEndpointSummary(out.Endpoints, endpoint)
	}
	return out
}

// ProviderProfileFromProto converts a profile of a gateway answer. narrow says
// it was read through a client that does not hold an endpoint whole (see
// FromNarrowProviderProfile).
func ProviderProfileFromProto(profile *pb.ProviderProfile, narrow bool) (ProviderProfile, error) {
	if narrow {
		return FromNarrowProviderProfile(profile), nil
	}
	dto, err := FromProtoProviderProfile(profile)
	if err != nil {
		return ProviderProfile{}, fmt.Errorf("profile %q: %w", profile.GetId(), err)
	}
	return dto, nil
}

// ProviderProfilesFromProto converts the profiles of one gateway answer, each
// as ProviderProfileFromProto does.
func ProviderProfilesFromProto(profiles []*pb.ProviderProfile, narrow bool) ([]ProviderProfile, error) {
	out := make([]ProviderProfile, 0, len(profiles))
	for _, profile := range profiles {
		dto, err := ProviderProfileFromProto(profile, narrow)
		if err != nil {
			return nil, err
		}
		out = append(out, dto)
	}
	return out, nil
}

// ProfileImportItems converts the profiles of an import, update or lint body
// to what the gateway takes. An error names the profile and the field.
func ProfileImportItems(profiles []ProviderProfileInput) ([]*pb.ProviderProfileImportItem, error) {
	items := make([]*pb.ProviderProfileImportItem, 0, len(profiles))
	for index := range profiles {
		profile, err := profiles[index].ToProto()
		if err != nil {
			return nil, fmt.Errorf("profiles[%d]: %w", index, err)
		}
		items = append(items, &pb.ProviderProfileImportItem{
			Profile: profile,
			Source:  profiles[index].ImportSource,
		})
	}
	return items, nil
}

func profileWithoutEndpoints(profile *pb.ProviderProfile) ProviderProfile {
	out := ProviderProfile{
		ID:               profile.GetId(),
		DisplayName:      profile.GetDisplayName(),
		Description:      profile.GetDescription(),
		Category:         enumName(pb.ProviderProfileCategory_name, profileCategoryPrefix, int32(profile.GetCategory())),
		Credentials:      []ProfileCredential{},
		InferenceCapable: profile.GetInferenceCapable(),
		Source:           profile.GetSource(),
		Scope:            profile.GetScope(),
		ResourceVersion:  profile.GetResourceVersion(),
		Annotations:      profile.GetAnnotations(),
	}
	for _, credential := range profile.GetCredentials() {
		out.Credentials = append(out.Credentials, profileCredentialFromProto(credential))
	}
	for _, file := range profile.GetFiles() {
		out.Files = append(out.Files, ProfileFile{
			Path:    file.GetPath(),
			Content: file.GetContent(),
			EnvVar:  file.GetEnvVar(),
		})
	}
	for _, binary := range profile.GetBinaries() {
		out.Binaries = append(out.Binaries, ProfileBinary{Path: binary.GetPath()})
	}
	if discovery := profile.GetDiscovery(); discovery != nil {
		out.Discovery = &ProfileDiscovery{Credentials: discovery.GetCredentials()}
	}
	return out
}

// appendEndpointSummary adds the host:port line the profile list shows for an
// endpoint. An endpoint that lists several ports shows them all.
func appendEndpointSummary(summaries []string, endpoint *sbv1.NetworkEndpoint) []string {
	host := endpoint.GetHost()
	switch {
	case endpoint.GetPort() > 0:
		return append(summaries, fmt.Sprintf("%s:%d", host, endpoint.GetPort()))
	case len(endpoint.GetPorts()) > 0:
		ports := make([]string, 0, len(endpoint.GetPorts()))
		for _, port := range endpoint.GetPorts() {
			ports = append(ports, strconv.FormatUint(uint64(port), 10))
		}
		return append(summaries, host+":"+strings.Join(ports, ","))
	case host != "":
		return append(summaries, host)
	}
	return summaries
}

func profileCredentialFromProto(credential *pb.ProviderProfileCredential) ProfileCredential {
	return ProfileCredential{
		Name:              credential.GetName(),
		Description:       credential.GetDescription(),
		EnvVars:           credential.GetEnvVars(),
		Required:          credential.GetRequired(),
		AuthStyle:         credential.GetAuthStyle(),
		HeaderName:        credential.GetHeaderName(),
		QueryParam:        credential.GetQueryParam(),
		PathTemplate:      credential.GetPathTemplate(),
		Refresh:           profileRefreshFromProto(credential.GetRefresh()),
		TokenGrant:        profileTokenGrantFromProto(credential.GetTokenGrant()),
		TokenGrantOwners:  credential.GetTokenGrantOwners(),
		StablePlaceholder: credential.GetStablePlaceholder(),
	}
}

func profileRefreshFromProto(refresh *pb.ProviderCredentialRefresh) *ProfileCredentialRefresh {
	if refresh == nil {
		return nil
	}
	out := &ProfileCredentialRefresh{
		Strategy:      enumName(pb.ProviderCredentialRefreshStrategy_name, refreshStrategyPrefix, int32(refresh.GetStrategy())),
		TokenURL:      refresh.GetTokenUrl(),
		Scopes:        refresh.GetScopes(),
		RefreshBefore: formatProfileDuration(refresh.GetRefreshBefore()),
		MaxLifetime:   formatProfileDuration(refresh.GetMaxLifetime()),
	}
	for _, material := range refresh.GetMaterial() {
		out.Material = append(out.Material, ProfileRefreshMaterial{
			Name:        material.GetName(),
			Description: material.GetDescription(),
			Required:    material.GetRequired(),
			Secret:      material.GetSecret(),
		})
	}
	for _, output := range refresh.GetAdditionalOutputs() {
		out.AdditionalOutputs = append(out.AdditionalOutputs, ProfileRefreshOutput{
			Output:     output.GetOutput(),
			Credential: output.GetCredential(),
		})
	}
	return out
}

func profileTokenGrantFromProto(grant *pb.ProviderCredentialTokenGrant) *ProfileTokenGrant {
	if grant == nil {
		return nil
	}
	out := &ProfileTokenGrant{
		GrantType:           enumName(pb.ProviderCredentialTokenGrantType_name, grantTypePrefix, int32(grant.GetGrantType())),
		TokenEndpoint:       grant.GetTokenEndpoint(),
		Audience:            grant.GetAudience(),
		JWTSVIDAudience:     grant.GetJwtSvidAudience(),
		Scopes:              grant.GetScopes(),
		CacheTTL:            formatProfileDuration(grant.GetCacheTtl()),
		ClientAssertionType: grant.GetClientAssertionType(),
		RequestedTokenType:  grant.GetRequestedTokenType(),
	}
	if subject := grant.GetSubjectToken(); subject != nil {
		out.SubjectToken = &ProfileTokenGrantSubjectToken{
			Source:           subject.GetSource(),
			Credential:       subject.GetCredential(),
			SubjectTokenType: subject.GetSubjectTokenType(),
		}
	}
	for _, override := range grant.GetAudienceOverrides() {
		out.AudienceOverrides = append(out.AudienceOverrides, ProfileTokenGrantAudienceOverride{
			Host:     override.GetHost(),
			Port:     override.GetPort(),
			Path:     override.GetPath(),
			Audience: override.GetAudience(),
			Scopes:   override.GetScopes(),
		})
	}
	return out
}

// parseProfileCategory reads a category as the DTOs spell it. A name that is
// no category is OTHER, which is also what a profile file defaults to.
func parseProfileCategory(name string) pb.ProviderProfileCategory {
	if name == enumUnspecified {
		return pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_UNSPECIFIED
	}
	if value, ok := enumValue(pb.ProviderProfileCategory_value, profileCategoryPrefix, name); ok && name != "" {
		return pb.ProviderProfileCategory(value)
	}
	return pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_OTHER
}

// ToProto converts the profile to the gateway's message. It fails on what the
// message cannot hold: a duration or an enum name that is none, an endpoint
// that is not a NetworkEndpoint, or both forms of endpoint at once.
func (in *ProviderProfileInput) ToProto() (*pb.ProviderProfile, error) {
	out := &pb.ProviderProfile{
		Id:               in.ID,
		DisplayName:      in.DisplayName,
		Description:      in.Description,
		Category:         parseProfileCategory(in.Category),
		InferenceCapable: in.InferenceCapable,
		ResourceVersion:  in.ResourceVersion,
		Annotations:      in.Annotations,
		Source:           in.Source,
		Scope:            in.Scope,
	}
	for index := range in.Credentials {
		credential, err := profileCredentialToProto(&in.Credentials[index])
		if err != nil {
			return nil, fmt.Errorf("credentials[%d].%w", index, err)
		}
		out.Credentials = append(out.Credentials, credential)
	}
	for _, file := range in.Files {
		out.Files = append(out.Files, &pb.ProviderProfileFile{
			Path:    file.Path,
			Content: file.Content,
			EnvVar:  file.EnvVar,
		})
	}
	endpoints, err := in.endpointsToProto()
	if err != nil {
		return nil, err
	}
	out.Endpoints = endpoints
	for _, binary := range in.Binaries {
		out.Binaries = append(out.Binaries, &sbv1.NetworkBinary{Path: binary.Path})
	}
	if in.Discovery != nil && len(in.Discovery.Credentials) > 0 {
		out.Discovery = &pb.ProviderProfileDiscovery{Credentials: in.Discovery.Credentials}
	}
	return out, nil
}

func (in *ProviderProfileInput) endpointsToProto() ([]*sbv1.NetworkEndpoint, error) {
	// What a read profile holds in endpoints is a summary of each endpoint,
	// not the endpoint. Beside networkEndpoints it says nothing new and is
	// dropped. Without them it is all there is, and writing it would store a
	// profile with its endpoints cut down to nothing: refuse.
	written := make([]ProfileEndpointInput, 0, len(in.Endpoints))
	summaries := 0
	for _, endpoint := range in.Endpoints {
		if endpoint.summary {
			summaries++
			continue
		}
		written = append(written, endpoint)
	}
	switch {
	case summaries > 0 && len(written) > 0:
		return nil, fmt.Errorf("endpoints mixes host:port summaries with endpoint objects: send objects, or networkEndpoints")
	case summaries > 0 && len(in.NetworkEndpoints) == 0:
		return nil, fmt.Errorf("endpoints holds the host:port summaries of a profile that was read, which is not enough to write it: send networkEndpoints, or endpoints as objects with a host and a port")
	case len(written) > 0 && len(in.NetworkEndpoints) > 0:
		return nil, fmt.Errorf("endpoints and networkEndpoints are two forms of the same list: send one of them")
	}
	out := make([]*sbv1.NetworkEndpoint, 0, len(written)+len(in.NetworkEndpoints))
	for _, endpoint := range written {
		out = append(out, &sbv1.NetworkEndpoint{
			Host:     endpoint.Host,
			Port:     endpoint.Port,
			Protocol: endpoint.Protocol,
		})
	}
	for index, raw := range in.NetworkEndpoints {
		endpoint := &sbv1.NetworkEndpoint{}
		if err := protojson.Unmarshal(raw, endpoint); err != nil {
			return nil, fmt.Errorf("networkEndpoints[%d] is not a NetworkEndpoint: %w", index, err)
		}
		out = append(out, endpoint)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func profileCredentialToProto(credential *ProfileCredential) (*pb.ProviderProfileCredential, error) {
	refresh, err := profileRefreshToProto(credential.Refresh)
	if err != nil {
		return nil, fmt.Errorf("refresh.%w", err)
	}
	grant, err := profileTokenGrantToProto(credential.TokenGrant)
	if err != nil {
		return nil, fmt.Errorf("tokenGrant.%w", err)
	}
	return &pb.ProviderProfileCredential{
		Name:              credential.Name,
		Description:       credential.Description,
		EnvVars:           credential.EnvVars,
		Required:          credential.Required,
		AuthStyle:         credential.AuthStyle,
		HeaderName:        credential.HeaderName,
		QueryParam:        credential.QueryParam,
		PathTemplate:      credential.PathTemplate,
		Refresh:           refresh,
		TokenGrant:        grant,
		TokenGrantOwners:  credential.TokenGrantOwners,
		StablePlaceholder: credential.StablePlaceholder,
	}, nil
}

func profileRefreshToProto(refresh *ProfileCredentialRefresh) (*pb.ProviderCredentialRefresh, error) {
	if refresh == nil {
		return nil, nil
	}
	strategy, ok := enumValue(pb.ProviderCredentialRefreshStrategy_value, refreshStrategyPrefix, refresh.Strategy)
	if !ok {
		return nil, fmt.Errorf("strategy: %q is not a refresh strategy", refresh.Strategy)
	}
	refreshBefore, err := parseProfileDuration(refresh.RefreshBefore)
	if err != nil {
		return nil, fmt.Errorf("refreshBefore: %w", err)
	}
	maxLifetime, err := parseProfileDuration(refresh.MaxLifetime)
	if err != nil {
		return nil, fmt.Errorf("maxLifetime: %w", err)
	}
	out := &pb.ProviderCredentialRefresh{
		Strategy:      pb.ProviderCredentialRefreshStrategy(strategy),
		TokenUrl:      refresh.TokenURL,
		Scopes:        refresh.Scopes,
		RefreshBefore: refreshBefore,
		MaxLifetime:   maxLifetime,
	}
	for _, material := range refresh.Material {
		out.Material = append(out.Material, &pb.ProviderCredentialRefreshMaterial{
			Name:        material.Name,
			Description: material.Description,
			Required:    material.Required,
			Secret:      material.Secret,
		})
	}
	for _, output := range refresh.AdditionalOutputs {
		out.AdditionalOutputs = append(out.AdditionalOutputs, &pb.ProviderCredentialRefreshOutput{
			Output:     output.Output,
			Credential: output.Credential,
		})
	}
	return out, nil
}

func profileTokenGrantToProto(grant *ProfileTokenGrant) (*pb.ProviderCredentialTokenGrant, error) {
	if grant == nil {
		return nil, nil
	}
	grantType, ok := enumValue(pb.ProviderCredentialTokenGrantType_value, grantTypePrefix, grant.GrantType)
	if !ok {
		return nil, fmt.Errorf("grantType: %q is not a token grant type", grant.GrantType)
	}
	cacheTTL, err := parseProfileDuration(grant.CacheTTL)
	if err != nil {
		return nil, fmt.Errorf("cacheTtl: %w", err)
	}
	out := &pb.ProviderCredentialTokenGrant{
		GrantType:           pb.ProviderCredentialTokenGrantType(grantType),
		TokenEndpoint:       grant.TokenEndpoint,
		Audience:            grant.Audience,
		JwtSvidAudience:     grant.JWTSVIDAudience,
		Scopes:              grant.Scopes,
		CacheTtl:            cacheTTL,
		ClientAssertionType: grant.ClientAssertionType,
		RequestedTokenType:  grant.RequestedTokenType,
	}
	if grant.SubjectToken != nil {
		out.SubjectToken = &pb.ProviderCredentialTokenGrantSubjectToken{
			Source:           grant.SubjectToken.Source,
			Credential:       grant.SubjectToken.Credential,
			SubjectTokenType: grant.SubjectToken.SubjectTokenType,
		}
	}
	for _, override := range grant.AudienceOverrides {
		out.AudienceOverrides = append(out.AudienceOverrides, &pb.ProviderCredentialTokenGrantAudienceOverride{
			Host:     override.Host,
			Port:     override.Port,
			Path:     override.Path,
			Audience: override.Audience,
			Scopes:   override.Scopes,
		})
	}
	return out, nil
}

// FromProtoDiagnostics converts the gateway's profile diagnostics to JSON DTOs.
func FromProtoDiagnostics(diagnostics []*pb.ProviderProfileDiagnostic) []ProviderProfileDiagnostic {
	out := make([]ProviderProfileDiagnostic, 0, len(diagnostics))
	for _, d := range diagnostics {
		out = append(out, ProviderProfileDiagnostic{
			Source:    d.GetSource(),
			ProfileID: d.GetProfileId(),
			Field:     d.GetField(),
			Message:   d.GetMessage(),
			Severity:  d.GetSeverity(),
		})
	}
	return out
}
