package models

import (
	"fmt"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/types"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// This file converts a provider profile between the gateway's message and the
// SDK's curated type, for the store that reaches provider profiles through the
// SDK (services.NewSDKProviderProfileStore). The SDK has the same conversion
// and keeps it internal.
//
// Everything of a profile fits the SDK type except its endpoints, of which the
// SDK carries a host, a port and a protocol. ProviderProfileFromSDK therefore
// yields endpoints that are known to be incomplete, and ProviderProfileToSDK
// refuses an endpoint it could only send in part.
//
// The one other field the SDK type has no place for is a credential's
// token_grant_owners (OpenShell 0.1.3). The gateway derives it and ignores a
// value a profile is written with, so a profile read through the SDK comes
// without it and ProviderProfileToSDK leaves it out instead of refusing:
// nothing the gateway would have stored is lost.

// ErrEndpointNotExpressible is what ProviderProfileToSDK returns for an
// endpoint that holds more than the SDK's NetworkEndpoint can carry.
var ErrEndpointNotExpressible = fmt.Errorf(
	"the profile has an endpoint that sets more than a host, a port and a protocol, " +
		"which is all this backend can send to the gateway")

var (
	sdkProfileCategories = map[openshell.ProfileCategory]pb.ProviderProfileCategory{
		openshell.ProfileCategoryOther:         pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_OTHER,
		openshell.ProfileCategoryInference:     pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_INFERENCE,
		openshell.ProfileCategoryAgent:         pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_AGENT,
		openshell.ProfileCategorySourceControl: pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_SOURCE_CONTROL,
		openshell.ProfileCategoryMessaging:     pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_MESSAGING,
		openshell.ProfileCategoryData:          pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_DATA,
		openshell.ProfileCategoryKnowledge:     pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_KNOWLEDGE,
	}
	sdkRefreshStrategies = map[openshell.RefreshStrategy]pb.ProviderCredentialRefreshStrategy{
		openshell.RefreshStrategyStatic:                  pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_STATIC,
		openshell.RefreshStrategyExternal:                pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_EXTERNAL,
		openshell.RefreshStrategyOAuth2RefreshToken:      pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_OAUTH2_REFRESH_TOKEN,
		openshell.RefreshStrategyOAuth2ClientCredentials: pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_OAUTH2_CLIENT_CREDENTIALS,
		openshell.RefreshStrategyGoogleServiceAccountJWT: pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_GOOGLE_SERVICE_ACCOUNT_JWT,
		RefreshStrategyAWSStsAssumeRole:                  pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_AWS_STS_ASSUME_ROLE,
	}
	sdkTokenGrantTypes = map[types.CredentialTokenGrantType]pb.ProviderCredentialTokenGrantType{
		types.CredentialTokenGrantTypeClientCredentials: pb.ProviderCredentialTokenGrantType_PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_CLIENT_CREDENTIALS,
		types.CredentialTokenGrantTypeTokenExchange:     pb.ProviderCredentialTokenGrantType_PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_TOKEN_EXCHANGE,
	}
)

// sdkEnumOf is the SDK's name for a proto enum value, and the SDK's zero value
// for one it has no name for.
func sdkEnumOf[S comparable, P comparable](names map[S]P, value P) S {
	for name, candidate := range names {
		if candidate == value {
			return name
		}
	}
	var none S
	return none
}

func sdkDurationToProto(d *types.ProfileDuration) *durationpb.Duration {
	if d == nil {
		return nil
	}
	return &durationpb.Duration{Seconds: d.Seconds, Nanos: d.Nanos}
}

func sdkDurationFromProto(d *durationpb.Duration) *types.ProfileDuration {
	if d == nil {
		return nil
	}
	return &types.ProfileDuration{Seconds: d.GetSeconds(), Nanos: d.GetNanos()}
}

// ProviderProfileFromSDK converts a profile read through the SDK to the
// gateway's message. Its endpoints hold the three fields the SDK carries.
func ProviderProfileFromSDK(profile *openshell.ProviderProfile) *pb.ProviderProfile {
	if profile == nil {
		return nil
	}
	out := &pb.ProviderProfile{
		Id:               profile.ID,
		DisplayName:      profile.DisplayName,
		Description:      profile.Description,
		Category:         sdkProfileCategories[profile.Category],
		InferenceCapable: profile.InferenceCapable,
		ResourceVersion:  profile.ResourceVersion,
		Annotations:      profile.Annotations,
		Source:           profile.Source,
		Scope:            profile.Scope,
	}
	for index := range profile.Credentials {
		out.Credentials = append(out.Credentials, sdkCredentialToProto(&profile.Credentials[index]))
	}
	for _, file := range profile.Files {
		out.Files = append(out.Files, &pb.ProviderProfileFile{
			Path:    file.Path,
			Content: file.Content,
			EnvVar:  file.EnvVar,
		})
	}
	for _, endpoint := range profile.Endpoints {
		out.Endpoints = append(out.Endpoints, &sbv1.NetworkEndpoint{
			Host:     endpoint.Host,
			Port:     endpoint.Port,
			Protocol: endpoint.Protocol,
		})
	}
	for _, binary := range profile.Binaries {
		out.Binaries = append(out.Binaries, &sbv1.NetworkBinary{Path: binary.Path})
	}
	if len(profile.Discovery.Credentials) > 0 {
		out.Discovery = &pb.ProviderProfileDiscovery{Credentials: profile.Discovery.Credentials}
	}
	return out
}

func sdkCredentialToProto(credential *openshell.ProfileCredential) *pb.ProviderProfileCredential {
	out := &pb.ProviderProfileCredential{
		Name:              credential.Name,
		Description:       credential.Description,
		EnvVars:           credential.EnvVars,
		Required:          credential.Required,
		AuthStyle:         credential.AuthStyle,
		HeaderName:        credential.HeaderName,
		QueryParam:        credential.QueryParam,
		PathTemplate:      credential.PathTemplate,
		StablePlaceholder: credential.StablePlaceholder,
	}
	if refresh := credential.Refresh; refresh != nil {
		out.Refresh = &pb.ProviderCredentialRefresh{
			Strategy:      sdkRefreshStrategies[refresh.Strategy],
			TokenUrl:      refresh.TokenURL,
			Scopes:        refresh.Scopes,
			RefreshBefore: sdkDurationToProto(refresh.RefreshBefore),
			MaxLifetime:   sdkDurationToProto(refresh.MaxLifetime),
		}
		for _, material := range refresh.Material {
			out.Refresh.Material = append(out.Refresh.Material, &pb.ProviderCredentialRefreshMaterial{
				Name:        material.Name,
				Description: material.Description,
				Required:    material.Required,
				Secret:      material.Secret,
			})
		}
		for _, output := range refresh.AdditionalOutputs {
			out.Refresh.AdditionalOutputs = append(out.Refresh.AdditionalOutputs, &pb.ProviderCredentialRefreshOutput{
				Output:     output.Output,
				Credential: output.Credential,
			})
		}
	}
	out.TokenGrant = sdkTokenGrantToProto(credential.TokenGrant)
	return out
}

func sdkTokenGrantToProto(grant *types.CredentialTokenGrant) *pb.ProviderCredentialTokenGrant {
	if grant == nil {
		return nil
	}
	out := &pb.ProviderCredentialTokenGrant{
		TokenEndpoint:       grant.TokenEndpoint,
		Audience:            grant.Audience,
		JwtSvidAudience:     grant.JWTSVIDAudience,
		Scopes:              grant.Scopes,
		CacheTtl:            sdkDurationToProto(grant.CacheTTL),
		ClientAssertionType: grant.ClientAssertionType,
		GrantType:           sdkTokenGrantTypes[grant.GrantType],
		RequestedTokenType:  grant.RequestedTokenType,
	}
	if subject := grant.SubjectToken; subject != nil {
		out.SubjectToken = &pb.ProviderCredentialTokenGrantSubjectToken{
			Source:           subject.Source,
			Credential:       subject.Credential,
			SubjectTokenType: subject.SubjectTokenType,
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
	return out
}

// ProviderProfileToSDK converts the gateway's message to the SDK type, for
// sending through the SDK. It returns ErrEndpointNotExpressible rather than
// send part of an endpoint: the gateway stores the endpoint it is sent, and
// what a narrowed one no longer says (an access preset, enforcement, L7 rules)
// is what bounded the provider's traffic.
func ProviderProfileToSDK(profile *pb.ProviderProfile) (openshell.ProviderProfile, error) {
	out := openshell.ProviderProfile{
		ID:               profile.GetId(),
		DisplayName:      profile.GetDisplayName(),
		Description:      profile.GetDescription(),
		Category:         sdkEnumOf(sdkProfileCategories, profile.GetCategory()),
		InferenceCapable: profile.GetInferenceCapable(),
		ResourceVersion:  profile.GetResourceVersion(),
		Annotations:      profile.GetAnnotations(),
		Source:           profile.GetSource(),
		Scope:            profile.GetScope(),
	}
	for _, credential := range profile.GetCredentials() {
		out.Credentials = append(out.Credentials, sdkCredentialFromProto(credential))
	}
	for _, file := range profile.GetFiles() {
		out.Files = append(out.Files, types.ProfileFile{
			Path:    file.GetPath(),
			Content: file.GetContent(),
			EnvVar:  file.GetEnvVar(),
		})
	}
	for index, endpoint := range profile.GetEndpoints() {
		narrow := &sbv1.NetworkEndpoint{
			Host:     endpoint.GetHost(),
			Port:     endpoint.GetPort(),
			Protocol: endpoint.GetProtocol(),
		}
		if !proto.Equal(narrow, endpoint) {
			return openshell.ProviderProfile{}, fmt.Errorf("endpoints[%d]: %w", index, ErrEndpointNotExpressible)
		}
		out.Endpoints = append(out.Endpoints, openshell.NetworkEndpoint{
			Host:     narrow.GetHost(),
			Port:     narrow.GetPort(),
			Protocol: narrow.GetProtocol(),
		})
	}
	for _, binary := range profile.GetBinaries() {
		out.Binaries = append(out.Binaries, openshell.NetworkBinary{Path: binary.GetPath()})
	}
	out.Discovery = openshell.ProfileDiscovery{Credentials: profile.GetDiscovery().GetCredentials()}
	return out, nil
}

func sdkCredentialFromProto(credential *pb.ProviderProfileCredential) openshell.ProfileCredential {
	out := openshell.ProfileCredential{
		Name:              credential.GetName(),
		Description:       credential.GetDescription(),
		EnvVars:           credential.GetEnvVars(),
		Required:          credential.GetRequired(),
		AuthStyle:         credential.GetAuthStyle(),
		HeaderName:        credential.GetHeaderName(),
		QueryParam:        credential.GetQueryParam(),
		PathTemplate:      credential.GetPathTemplate(),
		TokenGrant:        sdkTokenGrantFromProto(credential.GetTokenGrant()),
		StablePlaceholder: credential.GetStablePlaceholder(),
	}
	if refresh := credential.GetRefresh(); refresh != nil {
		out.Refresh = &types.ProfileCredentialRefresh{
			Strategy:      sdkEnumOf(sdkRefreshStrategies, refresh.GetStrategy()),
			TokenURL:      refresh.GetTokenUrl(),
			Scopes:        refresh.GetScopes(),
			RefreshBefore: sdkDurationFromProto(refresh.GetRefreshBefore()),
			MaxLifetime:   sdkDurationFromProto(refresh.GetMaxLifetime()),
		}
		for _, material := range refresh.GetMaterial() {
			out.Refresh.Material = append(out.Refresh.Material, types.ProfileCredentialRefreshMaterial{
				Name:        material.GetName(),
				Description: material.GetDescription(),
				Required:    material.GetRequired(),
				Secret:      material.GetSecret(),
			})
		}
		for _, output := range refresh.GetAdditionalOutputs() {
			out.Refresh.AdditionalOutputs = append(out.Refresh.AdditionalOutputs, types.ProfileCredentialRefreshOutput{
				Output:     output.GetOutput(),
				Credential: output.GetCredential(),
			})
		}
	}
	return out
}

func sdkTokenGrantFromProto(grant *pb.ProviderCredentialTokenGrant) *types.CredentialTokenGrant {
	if grant == nil {
		return nil
	}
	out := &types.CredentialTokenGrant{
		TokenEndpoint:       grant.GetTokenEndpoint(),
		Audience:            grant.GetAudience(),
		JWTSVIDAudience:     grant.GetJwtSvidAudience(),
		Scopes:              grant.GetScopes(),
		CacheTTL:            sdkDurationFromProto(grant.GetCacheTtl()),
		ClientAssertionType: grant.GetClientAssertionType(),
		GrantType:           sdkEnumOf(sdkTokenGrantTypes, grant.GetGrantType()),
		RequestedTokenType:  grant.GetRequestedTokenType(),
	}
	if subject := grant.GetSubjectToken(); subject != nil {
		out.SubjectToken = &types.TokenGrantSubjectToken{
			Source:           subject.GetSource(),
			Credential:       subject.GetCredential(),
			SubjectTokenType: subject.GetSubjectTokenType(),
		}
	}
	for _, override := range grant.GetAudienceOverrides() {
		out.AudienceOverrides = append(out.AudienceOverrides, types.TokenGrantAudienceOverride{
			Host:     override.GetHost(),
			Port:     override.GetPort(),
			Path:     override.GetPath(),
			Audience: override.GetAudience(),
			Scopes:   override.GetScopes(),
		})
	}
	return out
}

// ProfileDiagnosticsFromSDK converts the diagnostics an SDK call returned to
// the gateway's message.
func ProfileDiagnosticsFromSDK(diagnostics []openshell.ProfileDiagnostic) []*pb.ProviderProfileDiagnostic {
	out := make([]*pb.ProviderProfileDiagnostic, 0, len(diagnostics))
	for _, d := range diagnostics {
		out = append(out, &pb.ProviderProfileDiagnostic{
			Source:    d.Source,
			ProfileId: d.ProfileID,
			Field:     d.Field,
			Message:   d.Message,
			Severity:  d.Severity,
		})
	}
	return out
}
