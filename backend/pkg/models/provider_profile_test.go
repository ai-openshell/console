package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/types"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
)

// populate sets every field of a message, and of every message under it, to a
// value that is not its default, from the message's own descriptor. A field
// the SDK's proto gains is therefore set here without anyone listing it,
// which is what lets the round trips below notice a field nothing carries.
func populate(t *testing.T, m protoreflect.Message, next *int) {
	t.Helper()
	if m.Descriptor().FullName() == "google.protobuf.Duration" {
		// Not any two numbers make a duration: the parts share a sign and the
		// nanoseconds stay under a second.
		*next++
		m.Set(m.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(int64(*next)))
		m.Set(m.Descriptor().Fields().ByName("nanos"), protoreflect.ValueOfInt32(500_000_000))
		return
	}
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		switch {
		case fd.IsMap():
			entries := m.Mutable(fd).Map()
			key := scalar(t, fd.MapKey(), next).MapKey()
			if fd.MapValue().Message() != nil {
				value := entries.NewValue()
				populate(t, value.Message(), next)
				entries.Set(key, value)
			} else {
				entries.Set(key, scalar(t, fd.MapValue(), next))
			}
		case fd.IsList():
			list := m.Mutable(fd).List()
			if fd.Message() != nil {
				element := list.NewElement()
				populate(t, element.Message(), next)
				list.Append(element)
			} else {
				list.Append(scalar(t, fd, next))
			}
		case fd.Message() != nil:
			populate(t, m.Mutable(fd).Message(), next)
		default:
			m.Set(fd, scalar(t, fd, next))
		}
	}
}

func scalar(t *testing.T, fd protoreflect.FieldDescriptor, next *int) protoreflect.Value {
	t.Helper()
	*next++
	switch fd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(fmt.Sprintf("%s-%d", fd.Name(), *next))
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(true)
	case protoreflect.Uint32Kind:
		return protoreflect.ValueOfUint32(uint32(*next))
	case protoreflect.Uint64Kind:
		return protoreflect.ValueOfUint64(uint64(*next))
	case protoreflect.Int32Kind:
		return protoreflect.ValueOfInt32(int32(*next))
	case protoreflect.Int64Kind:
		return protoreflect.ValueOfInt64(int64(*next))
	case protoreflect.EnumKind:
		// The first value after the zero one, which is the unspecified value.
		return protoreflect.ValueOfEnum(fd.Enum().Values().Get(1).Number())
	}
	t.Fatalf("field %s is a %s: teach scalar() to set one", fd.FullName(), fd.Kind())
	return protoreflect.Value{}
}

// populatedProfile is a ProviderProfile with every field of every message
// under it set.
func populatedProfile(t *testing.T) *pb.ProviderProfile {
	t.Helper()
	profile := &pb.ProviderProfile{}
	next := 0
	populate(t, profile.ProtoReflect(), &next)
	return profile
}

// writtenBack turns a profile the BFF returned into the body a client sends
// to write it: the same JSON, read the way DecodeBody reads a request, with
// unknown fields refused. Only the host:port summaries are left out, which
// are derived from networkEndpoints and are not part of what is written.
func writtenBack(t *testing.T, dto ProviderProfile) ProviderProfileInput {
	t.Helper()
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal the profile: %v", err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("read the profile back: %v", err)
	}
	delete(fields, "endpoints")
	if raw, err = json.Marshal(fields); err != nil {
		t.Fatalf("marshal the request: %v", err)
	}
	var input ProviderProfileInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		t.Fatalf("the profile the BFF returns is not a profile it accepts: %v\n%s", err, raw)
	}
	return input
}

// A profile read from the gateway and written back is the profile that was
// read. This is the property the profile update depends on: the gateway
// replaces the stored profile with what it is sent, so a field that does not
// survive the trip is removed from the profile.
//
// Every field is covered, including ones this test does not name: populate
// walks the message descriptors of the SDK's proto. Moving the SDK to a
// release that adds a field to ProviderProfile, or to a message under it,
// fails here until the DTOs carry it.
func TestProviderProfileSurvivesBeingReadAndWrittenBack(t *testing.T) {
	want := populatedProfile(t)

	dto, err := FromProtoProviderProfile(want)
	if err != nil {
		t.Fatalf("FromProtoProviderProfile: %v", err)
	}
	input := writtenBack(t, dto)
	got, err := input.ToProto()
	if err != nil {
		t.Fatalf("ToProto: %v", err)
	}
	if !proto.Equal(want, got) {
		t.Errorf("the profile changed on the way through the DTOs.\nread:    %s\nwritten: %s",
			protojson.Format(want), protojson.Format(got))
	}
}

// The fields the dashboard dropped before, named one by one so that the test
// reads as the list of what a profile update used to remove.
func TestProviderProfileCarriesWhatUsedToBeDropped(t *testing.T) {
	profile := &pb.ProviderProfile{
		Id:          "github",
		DisplayName: "GitHub",
		Annotations: map[string]string{"example.com/source": "platform"},
		Credentials: []*pb.ProviderProfileCredential{{
			Name:         "api_token",
			EnvVars:      []string{"GITHUB_TOKEN"},
			AuthStyle:    "path",
			HeaderName:   "authorization",
			QueryParam:   "api_key",
			PathTemplate: "/v1/{credential}/resources",
			Refresh: &pb.ProviderCredentialRefresh{
				Strategy:      pb.ProviderCredentialRefreshStrategy_PROVIDER_CREDENTIAL_REFRESH_STRATEGY_OAUTH2_CLIENT_CREDENTIALS,
				TokenUrl:      "https://login.example.com/oauth2/token",
				Scopes:        []string{"api.read"},
				RefreshBefore: durationpb.New(300 * time.Second),
				MaxLifetime:   durationpb.New(time.Hour),
				Material: []*pb.ProviderCredentialRefreshMaterial{
					{Name: "client_secret", Description: "OAuth client secret", Required: true, Secret: true},
				},
				AdditionalOutputs: []*pb.ProviderCredentialRefreshOutput{
					{Output: "session_token", Credential: "session"},
				},
			},
			TokenGrant: &pb.ProviderCredentialTokenGrant{
				GrantType:     pb.ProviderCredentialTokenGrantType_PROVIDER_CREDENTIAL_TOKEN_GRANT_TYPE_TOKEN_EXCHANGE,
				TokenEndpoint: "https://login.example.com/token",
				CacheTtl:      durationpb.New(1500 * time.Millisecond),
				SubjectToken: &pb.ProviderCredentialTokenGrantSubjectToken{
					Source: "provider_credential", Credential: "user_oidc_token",
				},
				AudienceOverrides: []*pb.ProviderCredentialTokenGrantAudienceOverride{
					{Host: "api.example.com", Port: 443, Audience: "api://projects"},
				},
			},
		}},
		Endpoints: []*sbv1.NetworkEndpoint{{
			Host:        "api.github.com",
			Port:        443,
			Protocol:    "rest",
			Access:      sbv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY,
			Enforcement: sbv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_ENFORCE,
			DenyRules:   []*sbv1.L7DenyRule{{Method: "DELETE", Path: "/repos/**"}},
		}},
		Binaries:  []*sbv1.NetworkBinary{{Path: "/usr/bin/gh"}},
		Discovery: &pb.ProviderProfileDiscovery{Credentials: []string{"api_token"}},
	}

	dto, err := FromProtoProviderProfile(profile)
	if err != nil {
		t.Fatalf("FromProtoProviderProfile: %v", err)
	}
	if len(dto.NetworkEndpoints) != 1 {
		t.Fatalf("networkEndpoints = %d entries, want 1", len(dto.NetworkEndpoints))
	}
	var endpoint, wantEndpoint map[string]any
	if err := json.Unmarshal(dto.NetworkEndpoints[0], &endpoint); err != nil {
		t.Fatalf("networkEndpoints[0] is not JSON: %v", err)
	}
	// The spelling sandbox policies already use: camelCase names, enum names.
	wantJSON := `{"host":"api.github.com","port":443,"protocol":"rest","access":"NETWORK_ACCESS_PRESET_READ_ONLY",
		"enforcement":"NETWORK_ENFORCEMENT_MODE_ENFORCE","denyRules":[{"method":"DELETE","path":"/repos/**"}]}`
	if err := json.Unmarshal([]byte(wantJSON), &wantEndpoint); err != nil {
		t.Fatalf("wantJSON: %v", err)
	}
	if !reflect.DeepEqual(endpoint, wantEndpoint) {
		t.Errorf("networkEndpoints[0] = %s, want %s", dto.NetworkEndpoints[0], wantJSON)
	}

	dto.NetworkEndpoints = nil
	want := ProviderProfile{
		ID:          "github",
		DisplayName: "GitHub",
		Category:    "UNSPECIFIED",
		Annotations: map[string]string{"example.com/source": "platform"},
		// The host:port line the list shows.
		Endpoints: []string{"api.github.com:443"},
		Binaries:  []ProfileBinary{{Path: "/usr/bin/gh"}},
		Discovery: &ProfileDiscovery{Credentials: []string{"api_token"}},
		Credentials: []ProfileCredential{{
			Name:         "api_token",
			EnvVars:      []string{"GITHUB_TOKEN"},
			AuthStyle:    "path",
			HeaderName:   "authorization",
			QueryParam:   "api_key",
			PathTemplate: "/v1/{credential}/resources",
			Refresh: &ProfileCredentialRefresh{
				Strategy:      "OAUTH2_CLIENT_CREDENTIALS",
				TokenURL:      "https://login.example.com/oauth2/token",
				Scopes:        []string{"api.read"},
				RefreshBefore: "300s",
				MaxLifetime:   "3600s",
				Material: []ProfileRefreshMaterial{
					{Name: "client_secret", Description: "OAuth client secret", Required: true, Secret: true},
				},
				AdditionalOutputs: []ProfileRefreshOutput{{Output: "session_token", Credential: "session"}},
			},
			TokenGrant: &ProfileTokenGrant{
				GrantType:     "TOKEN_EXCHANGE",
				TokenEndpoint: "https://login.example.com/token",
				CacheTTL:      "1.500s",
				SubjectToken:  &ProfileTokenGrantSubjectToken{Source: "provider_credential", Credential: "user_oidc_token"},
				AudienceOverrides: []ProfileTokenGrantAudienceOverride{
					{Host: "api.example.com", Port: 443, Audience: "api://projects"},
				},
			},
		}},
	}
	if !reflect.DeepEqual(dto, want) {
		t.Errorf("profile = %+v\nwant      %+v", dto, want)
	}
}

// A profile read through the SDK shows three fields of each endpoint. Those
// are summarized and not offered as networkEndpoints, where a client would
// take them for the endpoint and write them back as one.
func TestProviderProfileReadThroughTheSDKHasNoNetworkEndpoints(t *testing.T) {
	dto := FromSDKProviderProfile(&openshell.ProviderProfile{
		ID:        "github",
		Endpoints: []openshell.NetworkEndpoint{{Host: "api.github.com", Port: 443, Protocol: "rest"}},
		Binaries:  []openshell.NetworkBinary{{Path: "/usr/bin/gh"}},
		Credentials: []openshell.ProfileCredential{{
			Name:       "api_token",
			HeaderName: "authorization",
			Refresh:    &types.ProfileCredentialRefresh{Strategy: openshell.RefreshStrategyOAuth2RefreshToken},
		}},
	})
	if dto.NetworkEndpoints != nil {
		t.Errorf("networkEndpoints = %s, want none", dto.NetworkEndpoints)
	}
	if len(dto.Endpoints) != 1 || dto.Endpoints[0] != "api.github.com:443" {
		t.Errorf("endpoint summaries = %v", dto.Endpoints)
	}
	// Everything the SDK does carry is there.
	if len(dto.Binaries) != 1 || dto.Credentials[0].HeaderName != "authorization" ||
		dto.Credentials[0].Refresh == nil || dto.Credentials[0].Refresh.Strategy != "OAUTH2_REFRESH_TOKEN" {
		t.Errorf("profile = %+v", dto)
	}
}

func TestEndpointSummaries(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *sbv1.NetworkEndpoint
		want     []string
	}{
		{name: "one port", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Port: 443}, want: []string{"a.example:443"}},
		{name: "several ports", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Ports: []uint32{80, 443}}, want: []string{"a.example:80,443"}},
		{name: "host only", endpoint: &sbv1.NetworkEndpoint{Host: "a.example"}, want: []string{"a.example"}},
		{name: "addresses only", endpoint: &sbv1.NetworkEndpoint{AllowedIps: []string{"10.0.0.0/8"}}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := appendEndpointSummary(nil, tc.endpoint); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("summary = %v, want %v", got, tc.want)
			}
		})
	}
}

// An absent duration and a zero one mean different things to the gateway: an
// absent refresh_before takes its default and "0s" refreshes at expiry.
func TestProfileDurationsKeepAbsentApartFromZero(t *testing.T) {
	tests := []struct {
		duration *durationpb.Duration
		name     string
		text     string
	}{
		{name: "absent", duration: nil, text: ""},
		{name: "zero", duration: &durationpb.Duration{}, text: "0s"},
		{name: "whole seconds", duration: &durationpb.Duration{Seconds: 300}, text: "300s"},
		{name: "milliseconds", duration: &durationpb.Duration{Seconds: 1, Nanos: 500_000_000}, text: "1.500s"},
		{name: "nanoseconds", duration: &durationpb.Duration{Nanos: 1}, text: "0.000000001s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatProfileDuration(tc.duration); got != tc.text {
				t.Errorf("formatProfileDuration = %q, want %q", got, tc.text)
			}
			back, err := parseProfileDuration(tc.text)
			if err != nil {
				t.Fatalf("parseProfileDuration(%q): %v", tc.text, err)
			}
			if (back == nil) != (tc.duration == nil) || !proto.Equal(back, tc.duration) {
				t.Errorf("parseProfileDuration(%q) = %v, want %v", tc.text, back, tc.duration)
			}
		})
	}
	for _, text := range []string{"300", "5m", "soon", "1.5"} {
		if _, err := parseProfileDuration(text); err == nil {
			t.Errorf("parseProfileDuration(%q) succeeded, want an error", text)
		}
	}
}

// A newer gateway can send an enum value the SDK's proto has no name for.
// It is carried as its number so that reading the profile and writing it back
// does not turn it into something else.
func TestProfileEnumsTheProtoHasNoNameFor(t *testing.T) {
	profile := &pb.ProviderProfile{
		Id:       "future",
		Category: pb.ProviderProfileCategory(41),
		Credentials: []*pb.ProviderProfileCredential{{
			Name:       "token",
			Refresh:    &pb.ProviderCredentialRefresh{Strategy: pb.ProviderCredentialRefreshStrategy(42)},
			TokenGrant: &pb.ProviderCredentialTokenGrant{GrantType: pb.ProviderCredentialTokenGrantType(43)},
		}},
	}
	dto, err := FromProtoProviderProfile(profile)
	if err != nil {
		t.Fatalf("FromProtoProviderProfile: %v", err)
	}
	if dto.Category != "41" || dto.Credentials[0].Refresh.Strategy != "42" || dto.Credentials[0].TokenGrant.GrantType != "43" {
		t.Errorf("enums = %q, %q, %q; want the numbers", dto.Category,
			dto.Credentials[0].Refresh.Strategy, dto.Credentials[0].TokenGrant.GrantType)
	}
	input := writtenBack(t, dto)
	back, err := input.ToProto()
	if err != nil {
		t.Fatalf("ToProto: %v", err)
	}
	if !proto.Equal(profile, back) {
		t.Errorf("profile = %s, want %s", protojson.Format(back), protojson.Format(profile))
	}
}

func TestProviderProfileInputCategory(t *testing.T) {
	tests := []struct {
		category string
		want     pb.ProviderProfileCategory
	}{
		{category: "INFERENCE", want: pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_INFERENCE},
		{category: "SOURCE_CONTROL", want: pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_SOURCE_CONTROL},
		{category: "UNSPECIFIED", want: pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_UNSPECIFIED},
		// A name that is no category is OTHER.
		{category: "", want: pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_OTHER},
		{category: "nonsense", want: pb.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_OTHER},
	}
	for _, tc := range tests {
		t.Run(tc.category, func(t *testing.T) {
			in := ProviderProfileInput{ID: "p", Category: tc.category}
			got, err := in.ToProto()
			if err != nil {
				t.Fatalf("ToProto: %v", err)
			}
			if got.GetCategory() != tc.want {
				t.Errorf("category = %v, want %v", got.GetCategory(), tc.want)
			}
		})
	}
}

func TestProviderProfileInputRefusesWhatTheMessageCannotHold(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		input   ProviderProfileInput
	}{
		{
			name:    "both forms of endpoint",
			wantErr: "send one of them",
			input: ProviderProfileInput{
				Endpoints:        []ProfileEndpointInput{{Host: "a.example", Port: 443}},
				NetworkEndpoints: []json.RawMessage{json.RawMessage(`{"host":"a.example"}`)},
			},
		},
		{
			name:    "an endpoint field that does not exist",
			wantErr: "networkEndpoints[0] is not a NetworkEndpoint",
			input: ProviderProfileInput{
				NetworkEndpoints: []json.RawMessage{json.RawMessage(`{"host":"a.example","acces":"full"}`)},
			},
		},
		{
			name:    "an access preset that does not exist",
			wantErr: "networkEndpoints[0] is not a NetworkEndpoint",
			input: ProviderProfileInput{
				NetworkEndpoints: []json.RawMessage{json.RawMessage(`{"host":"a.example","access":"read-only"}`)},
			},
		},
		{
			name:    "a refresh strategy that does not exist",
			wantErr: `credentials[0].refresh.strategy: "oauth2" is not a refresh strategy`,
			input: ProviderProfileInput{Credentials: []ProfileCredential{
				{Name: "token", Refresh: &ProfileCredentialRefresh{Strategy: "oauth2"}},
			}},
		},
		{
			name:    "a duration that is not one",
			wantErr: `credentials[0].refresh.refreshBefore: "5m" is not a duration`,
			input: ProviderProfileInput{Credentials: []ProfileCredential{
				{Name: "token", Refresh: &ProfileCredentialRefresh{Strategy: "STATIC", RefreshBefore: "5m"}},
			}},
		},
		{
			name:    "a grant type that does not exist",
			wantErr: `credentials[1].tokenGrant.grantType: "password" is not a token grant type`,
			input: ProviderProfileInput{Credentials: []ProfileCredential{
				{Name: "first"},
				{Name: "token", TokenGrant: &ProfileTokenGrant{GrantType: "password"}},
			}},
		},
		{
			name:    "a cache lifetime that is not a duration",
			wantErr: `credentials[0].tokenGrant.cacheTtl: "300" is not a duration`,
			input: ProviderProfileInput{Credentials: []ProfileCredential{
				{Name: "token", TokenGrant: &ProfileTokenGrant{CacheTTL: "300"}},
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.input.ToProto()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ToProto error = %v, want one that says %q", err, tc.wantErr)
			}
		})
	}
}

// A body with the short form of its endpoints, a host and a port for each, is
// a profile.
func TestProviderProfileInputTakesTheHostAndPortForm(t *testing.T) {
	var input ProviderProfileInput
	body := `{"id":"custom","displayName":"Custom","category":"DATA","inferenceCapable":false,
		"credentials":[{"name":"api_key","envVars":["CUSTOM_API_KEY"],"required":true}],
		"endpoints":[{"host":"api.example.com","port":443},{"host":"db.example.com","port":5432,"protocol":"sql"}]}`
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, err := input.ToProto()
	if err != nil {
		t.Fatalf("ToProto: %v", err)
	}
	want := []*sbv1.NetworkEndpoint{
		{Host: "api.example.com", Port: 443},
		{Host: "db.example.com", Port: 5432, Protocol: "sql"},
	}
	if len(got.GetEndpoints()) != len(want) {
		t.Fatalf("endpoints = %v", got.GetEndpoints())
	}
	for i := range want {
		if !proto.Equal(got.GetEndpoints()[i], want[i]) {
			t.Errorf("endpoints[%d] = %v, want %v", i, got.GetEndpoints()[i], want[i])
		}
	}
}

func TestProfileImportItemsNameTheProfileAtFault(t *testing.T) {
	_, err := ProfileImportItems([]ProviderProfileInput{
		{ID: "fine"},
		{ID: "broken", Credentials: []ProfileCredential{{Refresh: &ProfileCredentialRefresh{Strategy: "nope"}}}},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "profiles[1]: credentials[0].refresh.strategy") {
		t.Errorf("error = %v, want it to name profiles[1] and the field", err)
	}

	items, err := ProfileImportItems([]ProviderProfileInput{{ID: "github", ImportSource: "github.yaml"}})
	if err != nil {
		t.Fatalf("ProfileImportItems: %v", err)
	}
	if items[0].GetSource() != "github.yaml" || items[0].GetProfile().GetId() != "github" {
		t.Errorf("item = %v, want profile github from github.yaml", items[0])
	}
}

// fillSDK sets every field of an SDK value, and of everything under it, to a
// value that is not its zero value. Like populate it works from the type, so a
// field the SDK gains is set without being listed.
func fillSDK(t *testing.T, v reflect.Value, next *int) {
	t.Helper()
	*next++
	switch v.Kind() {
	case reflect.String:
		switch v.Interface().(type) {
		case types.ProfileCategory:
			v.Set(reflect.ValueOf(types.ProfileCategoryInference))
		case types.RefreshStrategy:
			v.Set(reflect.ValueOf(types.RefreshStrategyOAuth2ClientCredentials))
		case types.CredentialTokenGrantType:
			v.Set(reflect.ValueOf(types.CredentialTokenGrantTypeTokenExchange))
		default:
			v.SetString(fmt.Sprintf("value-%d", *next))
		}
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int32, reflect.Int64:
		v.SetInt(int64(*next))
	case reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(*next))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillSDK(t, v.Elem(), next)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillSDK(t, v.Index(0), next)
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key, value := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fillSDK(t, key, next)
		fillSDK(t, value, next)
		v.SetMapIndex(key, value)
	case reflect.Struct:
		for i := range v.NumField() {
			fillSDK(t, v.Field(i), next)
		}
	default:
		t.Fatalf("%s is a %s: teach fillSDK to set one", v.Type(), v.Kind())
	}
}

// The same property for the SDK-backed store: a profile read with the SDK,
// returned as the DTO, sent back and written with the SDK is the profile that
// was read, field for field of the SDK's type. fillSDK walks that type, so a
// field the SDK gains fails here until it is carried.
func TestProviderProfileSurvivesTheSDKRoundTrip(t *testing.T) {
	var want openshell.ProviderProfile
	next := 0
	fillSDK(t, reflect.ValueOf(&want).Elem(), &next)

	message := ProviderProfileFromSDK(&want)
	// FromProtoProviderProfile, not the narrow conversion, so that the three
	// endpoint fields the SDK does carry are part of what is checked.
	dto, err := FromProtoProviderProfile(message)
	if err != nil {
		t.Fatalf("FromProtoProviderProfile: %v", err)
	}
	input := writtenBack(t, dto)
	written, err := input.ToProto()
	if err != nil {
		t.Fatalf("ToProto: %v", err)
	}
	got, err := ProviderProfileToSDK(written)
	if err != nil {
		t.Fatalf("ProviderProfileToSDK: %v", err)
	}
	// ProfileCredential.Secret is not a field of the gateway's message. The
	// SDK sets it on a profile it reads, to whether the credential declares a
	// refresh, and ignores it on one it writes; it is set here the way the
	// SDK's own reader would.
	for i := range got.Credentials {
		got.Credentials[i].Secret = got.Credentials[i].Refresh != nil
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("the profile changed on the way through the DTOs.\nread:    %+v\nwritten: %+v", want, got)
	}
}

// From OpenShell 0.1.9 the SDK's profile endpoint is its policy endpoint, so
// an endpoint with an access preset, enforcement, rules, several ports or a
// path goes through the SDK whole: written and read back, it is the endpoint
// that was sent. Before, the SDK kept a host, a port and a protocol, and these
// were refused rather than narrowed.
func TestProviderProfileToSDKCarriesAnEndpointWhole(t *testing.T) {
	tests := []struct {
		endpoint *sbv1.NetworkEndpoint
		name     string
	}{
		{name: "host, port and protocol", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Port: 443, Protocol: "rest"}},
		{name: "an access preset", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Access: sbv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY}},
		{name: "enforcement", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Enforcement: sbv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_ENFORCE}},
		{name: "rules", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Rules: []*sbv1.L7Rule{{Allow: &sbv1.L7Allow{Method: "GET"}}}}},
		{name: "several ports", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Ports: []uint32{80, 443}}},
		{name: "a path", endpoint: &sbv1.NetworkEndpoint{Host: "a.example", Path: "/graphql"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk, err := ProviderProfileToSDK(&pb.ProviderProfile{
				Id:        "p",
				Endpoints: []*sbv1.NetworkEndpoint{{Host: "first.example"}, tc.endpoint},
			})
			if err != nil {
				t.Fatalf("ProviderProfileToSDK: %v", err)
			}
			back := ProviderProfileFromSDK(&sdk).GetEndpoints()
			if len(back) != 2 || !proto.Equal(back[1], tc.endpoint) {
				t.Errorf("endpoint after the SDK = %v, want %v", back, tc.endpoint)
			}
		})
	}
}

// The owners the gateway derives for a token grant have no place in the SDK's
// type. The gateway ignores them when a profile is written, so a profile that
// was read with them is still written through the SDK, without them, and is
// not refused the way a narrowed endpoint is.
func TestProviderProfileToSDKLeavesOutTokenGrantOwners(t *testing.T) {
	got, err := ProviderProfileToSDK(&pb.ProviderProfile{
		Id: "p",
		Credentials: []*pb.ProviderProfileCredential{
			{Name: "token", TokenGrantOwners: []string{"gateway-derived-owner"}},
		},
	})
	if err != nil {
		t.Fatalf("ProviderProfileToSDK: %v", err)
	}
	if want := []openshell.ProfileCredential{{Name: "token"}}; !reflect.DeepEqual(got.Credentials, want) {
		t.Errorf("credentials = %+v, want %+v", got.Credentials, want)
	}
}

func TestProviderProfileFromSDKNil(t *testing.T) {
	if got := ProviderProfileFromSDK(nil); got != nil {
		t.Errorf("ProviderProfileFromSDK(nil) = %v, want nil", got)
	}
}

func TestFromSDKRefreshStatusSaysWhatAFailedRefreshNeeds(t *testing.T) {
	failedAt := time.UnixMilli(1_900_000_000_000)
	got := FromSDKRefreshStatus(&openshell.RefreshStatus{
		CredentialKey:        "GOOGLE_ACCESS_TOKEN",
		Strategy:             openshell.RefreshStrategyOAuth2RefreshToken,
		Status:               "failed",
		LastError:            "invalid_grant",
		RecoveryAction:       types.RefreshRecoveryActionReauthorize,
		FailureCode:          "oauth_invalid_grant",
		ProviderErrorSubtype: "token_revoked",
		LastErrorAt:          failedAt,
	})
	if got.RecoveryAction != "REAUTHORIZE" || got.FailureCode != "oauth_invalid_grant" ||
		got.ProviderErrorSubtype != "token_revoked" || got.LastErrorAtMs != failedAt.UnixMilli() {
		t.Errorf("status = %+v", got)
	}

	actions := map[types.RefreshRecoveryAction]string{
		types.RefreshRecoveryActionUnspecified:      "",
		types.RefreshRecoveryActionRetry:            "RETRY",
		types.RefreshRecoveryActionReauthorize:      "REAUTHORIZE",
		types.RefreshRecoveryActionFixConfiguration: "FIX_CONFIGURATION",
		types.RefreshRecoveryActionInvestigate:      "INVESTIGATE",
	}
	for action, want := range actions {
		if got := sdkRecoveryActionString(action); got != want {
			t.Errorf("sdkRecoveryActionString(%v) = %q, want %q", action, got, want)
		}
	}

	// A refresh that is working says nothing about recovery.
	raw, err := json.Marshal(FromSDKRefreshStatus(&openshell.RefreshStatus{CredentialKey: "K", Status: "active"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"recoveryAction", "failureCode", "providerErrorSubtype", "lastErrorAtMs"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("a healthy status carries %s: %s", key, raw)
		}
	}
}

// A client that reads a profile and sends the same JSON back, changing
// nothing and deleting nothing, writes the profile it read. The summaries in
// "endpoints" come back beside the whole endpoints and add nothing.
func TestProviderProfileCanBeSentBackExactlyAsRead(t *testing.T) {
	want := populatedProfile(t)
	dto, err := FromProtoProviderProfile(want)
	if err != nil {
		t.Fatalf("FromProtoProviderProfile: %v", err)
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal the profile: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"endpoints":["`)) || !bytes.Contains(raw, []byte(`"networkEndpoints":[`)) {
		t.Fatalf("the read profile carries no endpoint summaries beside its endpoints, so the test proves nothing: %s", raw)
	}

	var input ProviderProfileInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		t.Fatalf("the profile the BFF returns is not a profile it accepts: %v\n%s", err, raw)
	}
	got, err := input.ToProto()
	if err != nil {
		t.Fatalf("ToProto: %v", err)
	}
	if !proto.Equal(want, got) {
		t.Errorf("the profile changed on the way through the DTOs.\nread:    %s\nwritten: %s",
			protojson.Format(want), protojson.Format(got))
	}
}

// What "endpoints" may hold when a profile is written, and what it may not.
func TestProviderProfileInputEndpointForms(t *testing.T) {
	tests := []struct { //nolint:govet // fieldalignment: test readability
		name       string
		body       string
		wantDecode string
		wantErr    string
		wantHosts  []string
	}{
		{
			name:      "objects, the form the profile form writes",
			body:      `{"endpoints":[{"host":"a.example","port":443},{"host":"b.example","port":80,"protocol":"rest"}]}`,
			wantHosts: []string{"a.example", "b.example"},
		},
		{
			name:      "summaries beside the endpoints they summarize are ignored",
			body:      `{"endpoints":["a.example:443"],"networkEndpoints":[{"host":"a.example","port":443,"access":"NETWORK_ACCESS_PRESET_READ_ONLY"}]}`,
			wantHosts: []string{"a.example"},
		},
		{
			// A profile read through the SDK has summaries and no whole
			// endpoints. Written back as it is, it would lose every endpoint.
			name:    "summaries alone are not endpoints",
			body:    `{"endpoints":["a.example:443","b.example:80"]}`,
			wantErr: "not enough to write it",
		},
		{
			name:    "summaries mixed with objects",
			body:    `{"endpoints":["a.example:443",{"host":"b.example","port":80}]}`,
			wantErr: "mixes host:port summaries with endpoint objects",
		},
		{
			name:    "objects beside whole endpoints",
			body:    `{"endpoints":[{"host":"a.example","port":443}],"networkEndpoints":[{"host":"a.example","port":443}]}`,
			wantErr: "send one of them",
		},
		{
			name:       "a field an endpoint object does not have",
			body:       `{"endpoints":[{"host":"a.example","port":443,"acces":"full"}]}`,
			wantDecode: "unknown field",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var input ProviderProfileInput
			decoder := json.NewDecoder(strings.NewReader(tc.body))
			decoder.DisallowUnknownFields()
			err := decoder.Decode(&input)
			if tc.wantDecode != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantDecode) {
					t.Fatalf("decode = %v, want an error containing %q", err, tc.wantDecode)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			endpoints, err := input.endpointsToProto()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("endpointsToProto = %v, want an error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("endpointsToProto: %v", err)
			}
			hosts := make([]string, 0, len(endpoints))
			for _, endpoint := range endpoints {
				hosts = append(hosts, endpoint.GetHost())
			}
			if strings.Join(hosts, ",") != strings.Join(tc.wantHosts, ",") {
				t.Errorf("endpoints written = %v, want %v", hosts, tc.wantHosts)
			}
		})
	}
}
