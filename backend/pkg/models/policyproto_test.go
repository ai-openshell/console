package models

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
)

// fullPolicyJSON exercises every advanced policy field that the pre-SDK
// protojson contract carried. If the converter drops any of these, the
// round-trip below stops being the identity and the test fails.
const fullPolicyJSON = `{
  "version": 3,
  "filesystem": {"includeWorkdir": true, "readOnly": ["/etc"], "readWrite": ["/tmp", "/work"]},
  "landlock": {"compatibility": "best_effort"},
  "process": {"runAsUser": "agent", "runAsGroup": "agents"},
  "networkPolicies": {
    "anthropic": {
      "name": "anthropic",
      "endpoints": [
        {
          "host": "api.anthropic.com",
          "port": 443,
          "ports": [443, 8443],
          "protocol": "https",
          "tls": "NETWORK_TLS_MODE_SKIP",
          "enforcement": "NETWORK_ENFORCEMENT_MODE_ENFORCE",
          "access": "NETWORK_ACCESS_PRESET_READ_WRITE",
          "allowedIps": ["1.2.3.4", "5.6.7.8"],
          "allowEncodedSlash": true,
          "persistedQueries": "strict",
          "graphqlMaxBodyBytes": 1048576,
          "path": "/v1",
          "websocketCredentialRewrite": true,
          "requestBodyCredentialRewrite": true,
          "allowUninspectedCredentials": true,
          "providerCredentialed": true,
          "advisorProposed": true,
          "credentialSigning": "aws-sigv4",
          "signingService": "bedrock",
          "signingRegion": "us-east-1",
          "jsonRpcMaxBodyBytes": 65536,
          "credentialBinding": {"provider": "claude-code"},
          "mcp": {"strictToolNames": true, "allowAllKnownMcpMethods": false},
          "rules": [
            {"allow": {"method": "POST", "path": "/v1/messages", "command": "chat", "operationType": "query", "operationName": "Msg", "fields": ["a", "b"], "query": {"q": {"glob": "x*", "any": ["1", "2"]}}, "params": {"p": {"glob": "y*"}}}}
          ],
          "denyRules": [
            {"method": "DELETE", "path": "/v1/admin", "fields": ["secret"], "query": {"z": {"any": ["9"]}}}
          ]
        }
      ],
      "binaries": [{"path": "/usr/bin/claude"}]
    }
  },
  "networkMiddlewares": {
    "redact": {"name": "redact", "middleware": "body-redactor", "onError": "deny", "order": 5, "config": {"pattern": "sk-.*"}, "endpoints": {"include": ["*.anthropic.com"], "exclude": ["logs.*"]}}
  }
}`

func normalizeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	return m
}

func TestSandboxPolicyRoundTripFullFidelity(t *testing.T) {
	// JSON -> domain -> JSON must be the identity for a fully-populated policy.
	policy, err := ParseSDKPolicy([]byte(fullPolicyJSON))
	if err != nil {
		t.Fatalf("ParseSDKPolicy: %v", err)
	}
	got := marshalSDKPolicy(policy)
	if got == nil {
		t.Fatal("marshalSDKPolicy returned nil")
	}

	want := normalizeJSON(t, []byte(fullPolicyJSON))
	have := normalizeJSON(t, got)
	if !reflect.DeepEqual(want, have) {
		t.Fatalf("policy round-trip lost or changed fields.\nwant: %s\n\ngot:  %s", fullPolicyJSON, got)
	}
}

func TestSandboxPolicyRoundTripAdvancedFieldsSurvive(t *testing.T) {
	// Explicit guard for the fields the pre-fix hand-rolled converter dropped.
	policy, err := ParseSDKPolicy([]byte(fullPolicyJSON))
	if err != nil {
		t.Fatalf("ParseSDKPolicy: %v", err)
	}
	ep := policy.NetworkPolicies["anthropic"].Endpoints[0]
	checks := []struct {
		name string
		ok   bool
	}{
		{"ports", len(ep.Ports) == 2},
		{"allowedIps", len(ep.AllowedIPs) == 2},
		{"L7 allow rules", len(ep.Rules) == 1 && ep.Rules[0].Allow != nil && ep.Rules[0].Allow.Method == "POST"},
		{"L7 deny rules", len(ep.DenyRules) == 1 && ep.DenyRules[0].Method == "DELETE"},
		{"mcp options", ep.Mcp != nil && ep.Mcp.StrictToolNames != nil && *ep.Mcp.StrictToolNames},
		{"credentialBinding", ep.CredentialBinding != nil && ep.CredentialBinding.Provider == "claude-code"},
		{"allowUninspectedCredentials", ep.AllowUninspectedCredentials},
		{"providerCredentialed", ep.ProviderCredentialed},
		{"jsonRpcMaxBodyBytes", ep.JSONRPCMaxBodyBytes == 65536},
		{"graphqlPersistedQueries + graphqlMaxBodyBytes", ep.GraphqlMaxBodyBytes == 1048576},
		{"networkMiddlewares", len(policy.NetworkMiddlewares) == 1},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("advanced field dropped: %s", c.name)
		}
	}
}

func TestNetworkPolicyRuleRoundTrip(t *testing.T) {
	const ruleJSON = `{"name":"gh","endpoints":[{"host":"api.github.com","port":443,"protocol":"https","access":"NETWORK_ACCESS_PRESET_READ_WRITE","allowedIps":["140.82.0.0"],"denyRules":[{"method":"POST","path":"/graphql"}]}],"binaries":[{"path":"/usr/bin/gh"}]}`
	rule, err := ParseSDKNetworkPolicyRule([]byte(ruleJSON))
	if err != nil {
		t.Fatalf("ParseSDKNetworkPolicyRule: %v", err)
	}
	got := MarshalSDKNetworkPolicyRule(rule)
	if got == nil {
		t.Fatal("MarshalSDKNetworkPolicyRule returned nil")
	}
	if !reflect.DeepEqual(normalizeJSON(t, []byte(ruleJSON)), normalizeJSON(t, got)) {
		t.Fatalf("rule round-trip changed fields.\nwant: %s\ngot:  %s", ruleJSON, got)
	}
}

func TestParseSDKPolicyRejectsUnknownField(t *testing.T) {
	// protojson rejects unknown fields, preserving the pre-SDK validation.
	if _, err := ParseSDKPolicy([]byte(`{"version":1,"bogusField":true}`)); err == nil {
		t.Fatal("expected error for unknown policy field, got nil")
	}
}

// policyFieldsTheSDKCannotCarry names, by their full proto name, the fields of
// the policy tree that populateMessage leaves unset. It is the one place a
// field is excused from the round trips in this file and in policywire_test.go.
// Every field that is not listed is populated, so one upstream adds still
// fails those tests by name until policyproto.go carries it.
//
// token_grant_owner (NetworkEndpoint field 27, new in OpenShell 0.1.3) is the
// gateway's own note of which provider, or global policy, may obtain a token
// grant for the endpoint. The gateway derives it and ignores a value a user
// writes. The SDK's domain type, types.PolicyNetworkEndpoint, has no field for
// it as of v0.1.3, and the SDK's own converter leaves it out by name, so a
// policy cannot bring it through the SDK in either direction and the port in
// policyproto.go has nowhere to put it. The entry goes when the SDK type gains
// the field and the port copies it:
// TestPolicyFieldsTheSDKCannotCarryAreStillNotCarried fails if it is still
// listed then.
var policyFieldsTheSDKCannotCarry = map[protoreflect.FullName]bool{
	"openshell.sandbox.v1.NetworkEndpoint.token_grant_owner": true,
}

// populateMessage sets every field of msg — scalars, enums, lists, maps and
// nested messages, recursively — to a non-zero value. It walks the proto
// descriptor, so a field upstream adds to the policy tree is populated here
// without anyone remembering to extend a fixture. The only fields it skips
// are the ones policyFieldsTheSDKCannotCarry names.
func populateMessage(t *testing.T, msg protoreflect.Message) {
	t.Helper()
	fields := msg.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if policyFieldsTheSDKCannotCarry[fd.FullName()] {
			continue
		}
		switch {
		case fd.IsMap():
			m := msg.Mutable(fd).Map()
			key := populatedScalar(t, fd.MapKey()).MapKey()
			if fd.MapValue().Message() != nil {
				value := m.NewValue()
				populateMessage(t, value.Message())
				m.Set(key, value)
			} else {
				m.Set(key, populatedScalar(t, fd.MapValue()))
			}
		case fd.IsList():
			list := msg.Mutable(fd).List()
			if fd.Message() != nil {
				populateMessage(t, list.AppendMutable().Message())
			} else {
				list.Append(populatedScalar(t, fd))
				list.Append(populatedScalar(t, fd))
			}
		case fd.Message() != nil:
			if fd.Message().FullName() == "google.protobuf.Struct" {
				s, err := structpb.NewStruct(map[string]any{
					"pattern": "sk-.*",
					"limit":   3.0,
					"nested":  map[string]any{"on": true},
				})
				if err != nil {
					t.Fatalf("structpb.NewStruct: %v", err)
				}
				msg.Set(fd, protoreflect.ValueOfMessage(s.ProtoReflect()))
				continue
			}
			populateMessage(t, msg.Mutable(fd).Message())
		default:
			msg.Set(fd, populatedScalar(t, fd))
		}
	}
}

func populatedScalar(t *testing.T, fd protoreflect.FieldDescriptor) protoreflect.Value {
	t.Helper()
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(true)
	case protoreflect.StringKind:
		return protoreflect.ValueOfString("v-" + string(fd.Name()))
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes([]byte(fd.Name()))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(7)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(7)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(7)
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(7)
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(1.5)
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(1.5)
	case protoreflect.EnumKind:
		// The last declared value, so that the zero value is never the one
		// that happens to survive.
		values := fd.Enum().Values()
		return protoreflect.ValueOfEnum(values.Get(values.Len() - 1).Number())
	default:
		t.Fatalf("populatedScalar: field %s has kind %s, which this test does not know how to populate",
			fd.FullName(), fd.Kind())
		return protoreflect.Value{}
	}
}

// lostFields lists the field paths that are set in want and absent or
// different in got.
func lostFields(path string, want, got protoreflect.Message) []string {
	var lost []string
	want.Range(func(fd protoreflect.FieldDescriptor, wv protoreflect.Value) bool {
		p := path + "." + fd.JSONName()
		if !got.Has(fd) {
			lost = append(lost, p)
			return true
		}
		gv := got.Get(fd)
		switch {
		case fd.IsMap():
			wv.Map().Range(func(k protoreflect.MapKey, wmv protoreflect.Value) bool {
				kp := p + "[" + k.String() + "]"
				gmv := gv.Map().Get(k)
				switch {
				case !gmv.IsValid():
					lost = append(lost, kp)
				case fd.MapValue().Message() != nil:
					lost = append(lost, lostFields(kp, wmv.Message(), gmv.Message())...)
				case !wmv.Equal(gmv):
					lost = append(lost, kp)
				}
				return true
			})
		case fd.IsList():
			wl, gl := wv.List(), gv.List()
			if wl.Len() != gl.Len() {
				lost = append(lost, p)
				return true
			}
			for i := 0; i < wl.Len(); i++ {
				if fd.Message() != nil {
					lost = append(lost, lostFields(fmt.Sprintf("%s[%d]", p, i), wl.Get(i).Message(), gl.Get(i).Message())...)
				} else if !wl.Get(i).Equal(gl.Get(i)) {
					lost = append(lost, fmt.Sprintf("%s[%d]", p, i))
				}
			}
		case fd.Message() != nil:
			if fd.Message().FullName() == "google.protobuf.Struct" {
				if !proto.Equal(wv.Message().Interface(), gv.Message().Interface()) {
					lost = append(lost, p)
				}
				return true
			}
			lost = append(lost, lostFields(p, wv.Message(), gv.Message())...)
		default:
			if !wv.Equal(gv) {
				lost = append(lost, p)
			}
		}
		return true
	})
	sort.Strings(lost)
	return lost
}

// TestSandboxPolicyRoundTripEveryProtoField is the guard the hand-written
// fixture above cannot be: it populates every field the SDK's SandboxPolicy
// proto declares and sends the result the way a policy travels through the
// BFF in both directions (browser JSON -> SDK types -> browser JSON). A field
// the port in policyproto.go does not copy shows up here by name.
func TestSandboxPolicyRoundTripEveryProtoField(t *testing.T) {
	full := &sbv1.SandboxPolicy{}
	populateMessage(t, full.ProtoReflect())
	raw, err := protojson.Marshal(full)
	if err != nil {
		t.Fatalf("marshal the populated policy: %v", err)
	}

	policy, err := ParseSDKPolicy(raw)
	if err != nil {
		t.Fatalf("ParseSDKPolicy: %v\n%s", err, raw)
	}
	out := marshalSDKPolicy(policy)
	if out == nil {
		t.Fatal("marshalSDKPolicy returned nil")
	}
	back := &sbv1.SandboxPolicy{}
	if err := protojson.Unmarshal(out, back); err != nil {
		t.Fatalf("unmarshal the round-tripped policy: %v\n%s", err, out)
	}
	if lost := lostFields("policy", full.ProtoReflect(), back.ProtoReflect()); len(lost) > 0 {
		t.Fatalf("the policy round trip through the BFF lost or changed %d field(s):\n  %s",
			len(lost), strings.Join(lost, "\n  "))
	}
	if !proto.Equal(full, back) {
		t.Fatalf("the policy round trip through the BFF is not the identity.\nsent: %s\ngot:  %s", raw, out)
	}
}

// TestNetworkPolicyRuleRoundTripEveryProtoField is the same guard for the
// path a draft chunk's proposed rule takes (GetDraftPolicy out, EditDraftChunk
// in).
func TestNetworkPolicyRuleRoundTripEveryProtoField(t *testing.T) {
	full := &sbv1.NetworkPolicyRule{}
	populateMessage(t, full.ProtoReflect())
	raw, err := protojson.Marshal(full)
	if err != nil {
		t.Fatalf("marshal the populated rule: %v", err)
	}

	rule, err := ParseSDKNetworkPolicyRule(raw)
	if err != nil {
		t.Fatalf("ParseSDKNetworkPolicyRule: %v\n%s", err, raw)
	}
	out := MarshalSDKNetworkPolicyRule(rule)
	if out == nil {
		t.Fatal("MarshalSDKNetworkPolicyRule returned nil")
	}
	back := &sbv1.NetworkPolicyRule{}
	if err := protojson.Unmarshal(out, back); err != nil {
		t.Fatalf("unmarshal the round-tripped rule: %v\n%s", err, out)
	}
	if lost := lostFields("rule", full.ProtoReflect(), back.ProtoReflect()); len(lost) > 0 {
		t.Fatalf("the rule round trip through the BFF lost or changed %d field(s):\n  %s",
			len(lost), strings.Join(lost, "\n  "))
	}
	if !proto.Equal(full, back) {
		t.Fatalf("the rule round trip through the BFF is not the identity.\nsent: %s\ngot:  %s", raw, out)
	}
}

// A field is excused from the round trips above only for as long as it has
// nowhere to go. This sets each excused field and nothing else, and expects it
// lost. When the SDK's endpoint type gains a place for one and policyproto.go
// copies it, this fails, and the entry is to be removed so that the round
// trips cover the field like any other.
func TestPolicyFieldsTheSDKCannotCarryAreStillNotCarried(t *testing.T) {
	for name := range policyFieldsTheSDKCannotCarry {
		full := &sbv1.NetworkPolicyRule{Endpoints: []*sbv1.NetworkEndpoint{{}}}
		endpoint := full.GetEndpoints()[0].ProtoReflect()
		fd := endpoint.Descriptor().Fields().ByName(name.Name())
		if fd == nil || fd.FullName() != name {
			t.Fatalf("%s is not a field of %s, the one message this test knows how to fill",
				name, endpoint.Descriptor().FullName())
		}
		endpoint.Set(fd, populatedScalar(t, fd))
		raw, err := protojson.Marshal(full)
		if err != nil {
			t.Fatalf("marshal the rule: %v", err)
		}

		rule, err := ParseSDKNetworkPolicyRule(raw)
		if err != nil {
			t.Fatalf("ParseSDKNetworkPolicyRule: %v\n%s", err, raw)
		}
		back := &sbv1.NetworkPolicyRule{}
		if err := protojson.Unmarshal(MarshalSDKNetworkPolicyRule(rule), back); err != nil {
			t.Fatalf("unmarshal the round-tripped rule: %v", err)
		}
		if len(back.GetEndpoints()) != 1 {
			t.Fatalf("the rule came back with %d endpoint(s), want 1", len(back.GetEndpoints()))
		}
		if back.GetEndpoints()[0].ProtoReflect().Has(fd) {
			t.Errorf("%s survives the round trip now: remove it from policyFieldsTheSDKCannotCarry", name)
		}
	}
}
