package models

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"

	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
	dm "github.com/ai-openshell/sdk-go/proto/datamodelv1"
	pb "github.com/ai-openshell/sdk-go/proto/openshellv1"
	sbv1 "github.com/ai-openshell/sdk-go/proto/sandboxv1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// These tests follow a policy the whole way between the browser's JSON and the
// gateway's wire format, through this package's converters and the SDK's real
// client, against a gRPC server that plays the gateway. The round-trip tests
// in policyproto_test.go stop at the SDK's types; a field the SDK itself did
// not carry would pass them and fail here.

// fakePolicyGateway records the policy writes it is sent and serves the policy
// it is given on every read.
type fakePolicyGateway struct {
	pb.UnimplementedOpenShellServer
	served       *sbv1.SandboxPolicy
	servedRule   *sbv1.NetworkPolicyRule
	updateConfig *pb.UpdateConfigRequest
	editChunk    *pb.EditDraftChunkRequest
	mu           sync.Mutex
}

func (g *fakePolicyGateway) UpdateConfig(_ context.Context, req *pb.UpdateConfigRequest) (*pb.UpdateConfigResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.updateConfig = req
	return &pb.UpdateConfigResponse{Version: 2, PolicyHash: "hash"}, nil
}

func (g *fakePolicyGateway) EditDraftChunk(_ context.Context, req *pb.EditDraftChunkRequest) (*pb.EditDraftChunkResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.editChunk = req
	return &pb.EditDraftChunkResponse{}, nil
}

func (g *fakePolicyGateway) GetSandboxPolicyStatus(context.Context, *pb.GetSandboxPolicyStatusRequest) (*pb.GetSandboxPolicyStatusResponse, error) {
	return &pb.GetSandboxPolicyStatusResponse{
		Revision:      &pb.SandboxPolicyRevision{Version: 3, Policy: g.served},
		ActiveVersion: 3,
	}, nil
}

// GetSandbox is what the SDK's Config().GetSandbox calls first.
func (g *fakePolicyGateway) GetSandbox(_ context.Context, req *pb.GetSandboxRequest) (*pb.SandboxResponse, error) {
	return &pb.SandboxResponse{Sandbox: &pb.Sandbox{Metadata: &dm.ObjectMeta{Name: req.GetName()}}}, nil
}

func (g *fakePolicyGateway) GetSandboxConfig(context.Context, *sbv1.GetSandboxConfigRequest) (*sbv1.GetSandboxConfigResponse, error) {
	return &sbv1.GetSandboxConfigResponse{Policy: g.served, Version: 3}, nil
}

func (g *fakePolicyGateway) GetDraftPolicy(context.Context, *pb.GetDraftPolicyRequest) (*pb.GetDraftPolicyResponse, error) {
	return &pb.GetDraftPolicyResponse{Chunks: []*pb.PolicyChunk{{
		Id:                       "c1",
		ProposedRule:             g.servedRule,
		CurrentEffectivePolicy:   g.served,
		CandidateEffectivePolicy: g.served,
	}}}, nil
}

func (g *fakePolicyGateway) sentUpdate() *pb.UpdateConfigRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.updateConfig
}

func (g *fakePolicyGateway) sentEdit() *pb.EditDraftChunkRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.editChunk
}

// newPolicyGatewayClient starts the fake gateway on a loopback port and
// returns the SDK's own client connected to it.
func newPolicyGatewayClient(t *testing.T, gateway pb.OpenShellServer) *openshell.Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterOpenShellServer(srv, gateway)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := openshell.NewClient(openshell.Config{Address: "http://" + lis.Addr().String()})
	if err != nil {
		t.Fatalf("openshell.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func populatedPolicy(t *testing.T) *sbv1.SandboxPolicy {
	t.Helper()
	policy := &sbv1.SandboxPolicy{}
	populateMessage(t, policy.ProtoReflect())
	return policy
}

func populatedRule(t *testing.T) *sbv1.NetworkPolicyRule {
	t.Helper()
	rule := &sbv1.NetworkPolicyRule{}
	populateMessage(t, rule.ProtoReflect())
	return rule
}

// mustMatch fails the test when got is not want, naming the fields that went
// missing or changed.
func mustMatch(t *testing.T, what string, want, got proto.Message) {
	t.Helper()
	if lost := lostFields(what, want.ProtoReflect(), got.ProtoReflect()); len(lost) > 0 {
		t.Fatalf("%s lost or changed %d field(s) on the way:\n  %s", what, len(lost), strings.Join(lost, "\n  "))
	}
	if !proto.Equal(want, got) {
		t.Fatalf("%s is not what was sent.\nsent: %s\ngot:  %s", what, protojson.Format(want), protojson.Format(got))
	}
}

func mustProtoJSON(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	raw, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return raw
}

// A whole-policy save: what the browser PUTs is what the gateway is sent.
func TestPolicyReachesTheGatewayUnchanged(t *testing.T) {
	gateway := &fakePolicyGateway{}
	client := newPolicyGatewayClient(t, gateway)
	full := populatedPolicy(t)

	policy, err := ParseSDKPolicy(mustProtoJSON(t, full))
	if err != nil {
		t.Fatalf("ParseSDKPolicy: %v", err)
	}
	if _, err := client.Config().Update(context.Background(), "team-a", &openshell.ConfigUpdate{Name: "sb", Policy: policy}); err != nil {
		t.Fatalf("Config().Update: %v", err)
	}

	sent := gateway.sentUpdate()
	if sent == nil || sent.GetPolicy() == nil {
		t.Fatalf("the gateway was not sent a policy: %v", sent)
	}
	mustMatch(t, "policy", full, sent.GetPolicy())
	if len(sent.GetMergeOperations()) != 0 {
		t.Errorf("a full replacement also carried %d merge operation(s)", len(sent.GetMergeOperations()))
	}
}

// Every read that carries a policy: a revision, the effective configuration
// and the three policy-shaped fields of a draft chunk.
func TestPolicyComesBackFromTheGatewayUnchanged(t *testing.T) {
	full := populatedPolicy(t)
	rule := populatedRule(t)
	client := newPolicyGatewayClient(t, &fakePolicyGateway{served: full, servedRule: rule})
	ctx := context.Background()

	parsePolicy := func(t *testing.T, raw json.RawMessage) *sbv1.SandboxPolicy {
		t.Helper()
		got := &sbv1.SandboxPolicy{}
		if err := protojson.Unmarshal(raw, got); err != nil {
			t.Fatalf("the JSON the BFF returns is not a SandboxPolicy: %v\n%s", err, raw)
		}
		return got
	}

	t.Run("revision", func(t *testing.T) {
		status, err := client.Policy().GetStatus(ctx, "team-a", "sb")
		if err != nil {
			t.Fatalf("Policy().GetStatus: %v", err)
		}
		mustMatch(t, "policy", full, parsePolicy(t, FromSDKPolicyRevision(&status.Revision).Policy))
	})

	t.Run("effective policy", func(t *testing.T) {
		config, err := client.Config().GetSandbox(ctx, "team-a", "sb")
		if err != nil {
			t.Fatalf("Config().GetSandbox: %v", err)
		}
		mustMatch(t, "policy", full, parsePolicy(t, FromSDKEffectivePolicy(config).Policy))
	})

	t.Run("draft chunk", func(t *testing.T) {
		draft, err := client.Policy().GetDraft(ctx, "team-a", "sb")
		if err != nil {
			t.Fatalf("Policy().GetDraft: %v", err)
		}
		chunks := FromSDKDraftPolicy(draft).Chunks
		if len(chunks) != 1 {
			t.Fatalf("got %d chunks, want 1", len(chunks))
		}
		gotRule := &sbv1.NetworkPolicyRule{}
		if err := protojson.Unmarshal(chunks[0].ProposedRule, gotRule); err != nil {
			t.Fatalf("proposedRule is not a NetworkPolicyRule: %v\n%s", err, chunks[0].ProposedRule)
		}
		mustMatch(t, "rule", rule, gotRule)
		mustMatch(t, "policy", full, parsePolicy(t, chunks[0].CurrentEffectivePolicy))
		mustMatch(t, "policy", full, parsePolicy(t, chunks[0].CandidateEffectivePolicy))
	})
}

// Editing a draft chunk: the rule the browser PUTs is the rule the gateway
// stores in place of the proposed one.
func TestDraftRuleReachesTheGatewayUnchanged(t *testing.T) {
	gateway := &fakePolicyGateway{}
	client := newPolicyGatewayClient(t, gateway)
	full := populatedRule(t)

	rule, err := ParseSDKNetworkPolicyRule(mustProtoJSON(t, full))
	if err != nil {
		t.Fatalf("ParseSDKNetworkPolicyRule: %v", err)
	}
	if err := client.Policy().EditDraftChunk(context.Background(), "team-a", "sb", "c1", rule); err != nil {
		t.Fatalf("Policy().EditDraftChunk: %v", err)
	}

	sent := gateway.sentEdit()
	if sent == nil || sent.GetProposedRule() == nil {
		t.Fatalf("the gateway was not sent a rule: %v", sent)
	}
	mustMatch(t, "rule", full, sent.GetProposedRule())
}

// populatedMergeOperations returns one operation per variant of the oneof,
// each with every field set, in the order the proto declares them.
func populatedMergeOperations(t *testing.T) []*pb.PolicyMergeOperation {
	t.Helper()
	variants := (&pb.PolicyMergeOperation{}).ProtoReflect().Descriptor().Oneofs().ByName("operation").Fields()
	operations := make([]*pb.PolicyMergeOperation, 0, variants.Len())
	for i := 0; i < variants.Len(); i++ {
		op := &pb.PolicyMergeOperation{}
		populateMessage(t, op.ProtoReflect().Mutable(variants.Get(i)).Message())
		operations = append(operations, op)
	}
	return operations
}

// An incremental update: every variant of PolicyMergeOperation, with every
// field set, reaches the gateway as the browser sent it. A variant upstream
// adds is picked up here from the proto and fails by name until
// policyMergeOperationFromProto handles it.
func TestPolicyMergeOperationsReachTheGatewayUnchanged(t *testing.T) {
	gateway := &fakePolicyGateway{}
	client := newPolicyGatewayClient(t, gateway)
	full := populatedMergeOperations(t)
	if len(full) < 6 {
		t.Fatalf("PolicyMergeOperation declares %d variants, want at least the six this BFF knows", len(full))
	}

	raw := make([]json.RawMessage, 0, len(full))
	for _, op := range full {
		raw = append(raw, mustProtoJSON(t, op))
	}
	operations, err := ParseSDKPolicyMergeOperations(raw)
	if err != nil {
		t.Fatalf("ParseSDKPolicyMergeOperations: %v", err)
	}
	if _, err := client.Config().Update(context.Background(), "team-a", &openshell.ConfigUpdate{
		Name:            "sb",
		MergeOperations: operations,
	}); err != nil {
		t.Fatalf("Config().Update: %v", err)
	}

	sent := gateway.sentUpdate()
	if sent == nil {
		t.Fatal("the gateway was not sent an update")
	}
	if sent.GetPolicy() != nil {
		t.Errorf("an incremental update also carried a full policy: %s", protojson.Format(sent.GetPolicy()))
	}
	if len(sent.GetMergeOperations()) != len(full) {
		t.Fatalf("the gateway was sent %d operation(s), want %d", len(sent.GetMergeOperations()), len(full))
	}
	for i, want := range full {
		variant := want.ProtoReflect().WhichOneof(want.ProtoReflect().Descriptor().Oneofs().ByName("operation"))
		mustMatch(t, variant.JSONName(), want, sent.GetMergeOperations()[i])
	}
}

// The target of an L7 append says three different things with its path: absent
// asks the gateway for the one endpoint that matches, empty selects the
// endpoint that has no path, and a value selects that path.
func TestPolicyMergeTargetPathPresenceSurvives(t *testing.T) {
	tests := []struct {
		want *string
		name string
		path string
	}{
		{name: "absent", path: ``, want: nil},
		{name: "empty", path: `"path":"",`, want: proto.String("")},
		{name: "set", path: `"path":"/v1/**",`, want: proto.String("/v1/**")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &fakePolicyGateway{}
			client := newPolicyGatewayClient(t, gateway)
			body := `{"addAllowRules":{"target":{"ruleName":"api","host":"api.example.com","ports":[443],` + tc.path +
				`"anyBinary":true},"rules":[{"allow":{"method":"GET","path":"/v1/models"}}]}}`

			operations, err := ParseSDKPolicyMergeOperations([]json.RawMessage{json.RawMessage(body)})
			if err != nil {
				t.Fatalf("ParseSDKPolicyMergeOperations: %v", err)
			}
			if _, err := client.Config().Update(context.Background(), "team-a", &openshell.ConfigUpdate{
				Name:            "sb",
				MergeOperations: operations,
			}); err != nil {
				t.Fatalf("Config().Update: %v", err)
			}

			target := gateway.sentUpdate().GetMergeOperations()[0].GetAddAllowRules().GetTarget()
			switch {
			case tc.want == nil && target.Path != nil:
				t.Errorf("path = %q, want it absent", target.GetPath())
			case tc.want != nil && target.Path == nil:
				t.Errorf("path is absent, want %q", *tc.want)
			case tc.want != nil && target.GetPath() != *tc.want:
				t.Errorf("path = %q, want %q", target.GetPath(), *tc.want)
			}
			if !target.GetAnyBinary() || len(target.GetBinaries()) != 0 {
				t.Errorf("binary scope = any:%v binaries:%v, want any binary and no list", target.GetAnyBinary(), target.GetBinaries())
			}
		})
	}
}

func TestParseSDKPolicyMergeOperationsRejects(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "no variant", body: `{}`, wantErr: "operations[0]: one of addRule"},
		{name: "unknown variant", body: `{"renameRule":{"ruleName":"a"}}`, wantErr: "operations[0]"},
		{name: "unknown field", body: `{"removeRule":{"ruleName":"a","force":true}}`, wantErr: "operations[0]"},
		{name: "two variants", body: `{"removeRule":{"ruleName":"a"},"removeBinary":{"ruleName":"a","binaryPath":"/bin/x"}}`, wantErr: "operations[0]"},
		{name: "not an object", body: `"removeRule"`, wantErr: "operations[0]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSDKPolicyMergeOperations([]json.RawMessage{json.RawMessage(tc.body)})
			if err == nil {
				t.Fatalf("ParseSDKPolicyMergeOperations(%s) succeeded, want an error", tc.body)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	// The position in the error is the operation's own, not always the first.
	_, err := ParseSDKPolicyMergeOperations([]json.RawMessage{
		json.RawMessage(`{"removeRule":{"ruleName":"a"}}`),
		json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "operations[1]") {
		t.Errorf("error = %v, want it to name operations[1]", err)
	}
}
