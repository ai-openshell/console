package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
	"github.com/ai-openshell/console/backend/pkg/models"
	"github.com/ai-openshell/console/backend/pkg/services"
)

// draftSummaryRoutes mounts the two summary routes on the paths app.go gives
// them, with the sandbox service app.go gives the handler.
func draftSummaryRoutes(sdk *mockSDK) http.Handler {
	handler := NewDraftsHandler(services.NewPolicyService(sdk.Policy()))
	handler.SetSandboxService(services.NewSandboxService(sdk.Sandboxes(), nil))
	r := chi.NewRouter()
	r.Get("/draft-summary", handler.GetDraftSummary)
	r.Get("/workspaces/{workspace}/draft-summary", handler.GetWorkspaceDraftSummary)
	return r
}

// draftRead is one GetDraftPolicy the summary made.
type draftRead struct {
	workspace, sandbox, filter string
}

// draftInbox answers GetDraft from a table keyed by "workspace/sandbox" and
// records every read. A sandbox the table does not name has an empty inbox.
type draftInbox struct {
	chunks map[string][]openshell.PolicyChunk
	errs   map[string]error
	reads  []draftRead
	mu     sync.Mutex
}

func (d *draftInbox) get(_ context.Context, workspace, name string, opts ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
	d.mu.Lock()
	d.reads = append(d.reads, draftRead{workspace: workspace, sandbox: name, filter: draftStatusFilter(opts)})
	d.mu.Unlock()
	key := workspace + "/" + name
	if err := d.errs[key]; err != nil {
		return nil, err
	}
	return &openshell.DraftPolicy{Chunks: d.chunks[key]}, nil
}

// readSet returns the reads without their order, which the summary does not
// promise: it reads several sandboxes at once.
func (d *draftInbox) readSet() map[draftRead]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	set := map[draftRead]int{}
	for _, read := range d.reads {
		set[read]++
	}
	return set
}

func sandboxesNamed(workspace string, names ...string) []*openshell.Sandbox {
	out := make([]*openshell.Sandbox, 0, len(names))
	for _, name := range names {
		out = append(out, &openshell.Sandbox{Name: name, Workspace: workspace})
	}
	return out
}

func TestWorkspaceDraftSummary(t *testing.T) {
	older := time.UnixMilli(1_700_000_100_000)
	newer := time.UnixMilli(1_700_000_200_000)
	tests := []struct {
		chunks    map[string][]openshell.PolicyChunk
		errs      map[string]error
		name      string
		sandboxes []*openshell.Sandbox
		wantReads []draftRead
		want      models.DraftSummary
	}{
		{
			name:      "no sandboxes",
			sandboxes: nil,
			want:      models.DraftSummary{Sandboxes: []models.DraftSandboxSummary{}},
		},
		{
			name:      "nothing pending anywhere",
			sandboxes: sandboxesNamed("team-a", "quiet-1", "quiet-2"),
			want:      models.DraftSummary{Sandboxes: []models.DraftSandboxSummary{}},
			wantReads: []draftRead{
				{workspace: "team-a", sandbox: "quiet-1", filter: "pending"},
				{workspace: "team-a", sandbox: "quiet-2", filter: "pending"},
			},
		},
		{
			name:      "a sandbox with none pending is left out, the others are counted in list order",
			sandboxes: sandboxesNamed("team-a", "busy", "quiet", "flagged"),
			chunks: map[string][]openshell.PolicyChunk{
				"team-a/busy": {
					{ID: "c1", Status: "pending", CreatedAt: newer, Rationale: "allow pypi.org for pip"},
					{ID: "c2", Status: "pending", CreatedAt: older, Rationale: "older proposal"},
				},
				"team-a/flagged": {
					{ID: "c3", Status: "pending", CreatedAt: older, SecurityNotes: "wildcard host", Rationale: "broad egress"},
				},
			},
			want: models.DraftSummary{
				TotalPending: 3,
				Sandboxes: []models.DraftSandboxSummary{
					{
						Workspace: "team-a", SandboxName: "busy", PendingCount: 2,
						LatestDraftMs: newer.UnixMilli(), LatestRationale: "allow pypi.org for pip",
					},
					{
						Workspace: "team-a", SandboxName: "flagged", PendingCount: 1,
						LatestDraftMs: older.UnixMilli(), HasSecurityFlags: true, LatestRationale: "broad egress",
					},
				},
			},
			wantReads: []draftRead{
				{workspace: "team-a", sandbox: "busy", filter: "pending"},
				{workspace: "team-a", sandbox: "quiet", filter: "pending"},
				{workspace: "team-a", sandbox: "flagged", filter: "pending"},
			},
		},
		{
			name:      "a sandbox whose inbox cannot be read is unavailable, not empty, and the rest still count",
			sandboxes: sandboxesNamed("team-a", "busy", "denied", "broken"),
			chunks: map[string][]openshell.PolicyChunk{
				"team-a/busy": {{ID: "c1", Status: "pending", CreatedAt: newer}},
				// Never reported: the read fails first.
				"team-a/denied": {{ID: "c9", Status: "pending"}},
			},
			errs: map[string]error{
				"team-a/denied": &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "workspace role required"},
				"team-a/broken": fmt.Errorf("connection reset"),
			},
			want: models.DraftSummary{
				TotalPending: 1,
				Sandboxes: []models.DraftSandboxSummary{
					{Workspace: "team-a", SandboxName: "busy", PendingCount: 1, LatestDraftMs: newer.UnixMilli()},
					{Workspace: "team-a", SandboxName: "denied", Unavailable: true},
					{Workspace: "team-a", SandboxName: "broken", Unavailable: true},
				},
			},
		},
		{
			name:      "a sandbox deleted since it was listed is left out",
			sandboxes: sandboxesNamed("team-a", "gone", "busy"),
			chunks: map[string][]openshell.PolicyChunk{
				"team-a/busy": {{ID: "c1", Status: "pending", CreatedAt: older}},
			},
			errs: map[string]error{
				"team-a/gone": &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "sandbox not found"},
			},
			want: models.DraftSummary{
				TotalPending: 1,
				Sandboxes: []models.DraftSandboxSummary{
					{Workspace: "team-a", SandboxName: "busy", PendingCount: 1, LatestDraftMs: older.UnixMilli()},
				},
			},
		},
		{
			// The SDK's Sandbox type allows an empty workspace. The gateway's
			// own lists always name one.
			name:      "a sandbox that names no workspace is read in the one that was asked for",
			sandboxes: []*openshell.Sandbox{{Name: "busy"}},
			chunks: map[string][]openshell.PolicyChunk{
				"team-a/busy": {{ID: "c1", Status: "pending"}},
			},
			want: models.DraftSummary{
				TotalPending: 1,
				Sandboxes:    []models.DraftSandboxSummary{{Workspace: "team-a", SandboxName: "busy", PendingCount: 1}},
			},
			wantReads: []draftRead{{workspace: "team-a", sandbox: "busy", filter: "pending"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.listFn = func(_ context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				if workspace != "team-a" || len(opts) != 0 {
					t.Errorf("sandboxes listed in %q with %+v, want team-a and no options", workspace, opts)
				}
				return tc.sandboxes, nil
			}
			inbox := &draftInbox{chunks: tc.chunks, errs: tc.errs}
			sdk.policy.getDraftFn = inbox.get

			w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/workspaces/team-a/draft-summary", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			// An empty summary is an empty array, never null: the list reads
			// sandboxes.find.
			if !strings.Contains(w.Body.String(), `"sandboxes":[`) {
				t.Errorf(`want a "sandboxes" array, got: %s`, w.Body.String())
			}
			var got models.DraftSummary
			decodeInto(t, w, &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("summary differs.\ngot:  %+v\nwant: %+v", got, tc.want)
			}
			if tc.wantReads != nil {
				want := map[draftRead]int{}
				for _, read := range tc.wantReads {
					want[read]++
				}
				if reads := inbox.readSet(); !reflect.DeepEqual(reads, want) {
					t.Errorf("draft reads = %v, want %v", reads, want)
				}
			}
		})
	}
}

// "unavailable" is only on the wire for a sandbox it is true of: an entry
// whose count was read carries no such field. What the gateway said about
// that one sandbox stays in the BFF's log.
func TestWorkspaceDraftSummaryWireShape(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		return sandboxesNamed("team-a", "busy", "denied"), nil
	}
	inbox := &draftInbox{
		chunks: map[string][]openshell.PolicyChunk{"team-a/busy": {{ID: "c1", Status: "pending"}}},
		errs: map[string]error{
			"team-a/denied": &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "principal alice lacks role in team-a"},
		},
	}
	sdk.policy.getDraftFn = inbox.get

	w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/workspaces/team-a/draft-summary", "")
	const want = `{"sandboxes":[` +
		`{"workspace":"team-a","sandboxName":"busy","latestDraftMs":0,"pendingCount":1,"hasSecurityFlags":false},` +
		`{"workspace":"team-a","sandboxName":"denied","latestDraftMs":0,"pendingCount":0,"hasSecurityFlags":false,"unavailable":true}` +
		`],"totalPending":1}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("body differs.\ngot:  %s\nwant: %s", got, want)
	}
}

func TestWorkspaceDraftSummaryForNamedPage(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		t.Fatal("a named page must not list every sandbox")
		return nil, nil
	}
	inbox := &draftInbox{chunks: map[string][]openshell.PolicyChunk{
		"team-a/busy": {{ID: "c1", Status: "pending"}},
	}}
	sdk.policy.getDraftFn = inbox.get
	w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/workspaces/team-a/draft-summary?sandbox=busy&sandbox=quiet&sandbox=busy", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	want := map[draftRead]int{
		{workspace: "team-a", sandbox: "busy", filter: "pending"}:  1,
		{workspace: "team-a", sandbox: "quiet", filter: "pending"}: 1,
	}
	if got := inbox.readSet(); !reflect.DeepEqual(got, want) {
		t.Errorf("draft reads = %+v, want %+v", got, want)
	}
	var body models.DraftSummary
	decodeInto(t, w, &body)
	if body.TotalPending != 1 || len(body.Sandboxes) != 1 || body.Sandboxes[0].SandboxName != "busy" {
		t.Errorf("summary = %+v", body)
	}
}

func TestWorkspaceDraftSummaryRejectsInvalidNamedPage(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		t.Fatal("invalid named page must not list")
		return nil, nil
	}
	for _, query := range []string{"sandbox=", "sandbox=bad%2Fname", strings.Repeat("sandbox=agent&", 101)} {
		w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/workspaces/team-a/draft-summary?"+query, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d; body: %s", query, w.Code, w.Body.String())
		}
	}
}

// The list of sandboxes is the one call the summary cannot do without, so
// its failure is the answer.
func TestDraftSummaryListFails(t *testing.T) {
	tests := []struct {
		err        error
		name       string
		path       string
		wantCode   apiutils.ResponseCode
		wantStatus int
	}{
		{
			name:       "an unknown workspace",
			path:       "/workspaces/nowhere/draft-summary",
			err:        &openshell.StatusError{Code: openshell.ErrorNotFound, Message: "workspace 'nowhere' not found"},
			wantStatus: http.StatusNotFound,
			wantCode:   apiutils.NotFound,
		},
		{
			name:       "a workspace the caller has no role in",
			path:       "/workspaces/team-b/draft-summary",
			err:        &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "workspace role required"},
			wantStatus: http.StatusForbidden,
			wantCode:   apiutils.PermissionDenied,
		},
		{
			name:       "the gateway is unreachable",
			path:       "/workspaces/team-a/draft-summary",
			err:        &openshell.StatusError{Code: openshell.ErrorUnavailable, Message: "connection refused"},
			wantStatus: http.StatusBadGateway,
			wantCode:   apiutils.GatewayUnavailable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				return nil, tc.err
			}
			sdk.policy.getDraftFn = func(context.Context, string, string, ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
				t.Error("an inbox was read although the sandboxes could not be listed")
				return nil, nil
			}
			wantErrorResponse(t, serve(t, draftSummaryRoutes(sdk), http.MethodGet, tc.path, ""), tc.wantStatus, tc.wantCode)
		})
	}
}

// The route outside a workspace summarizes the scope it is asked for, and
// nothing wider: a workspace by name, every workspace by name, and without
// either, nothing at all.
func TestDraftSummaryScope(t *testing.T) {
	everywhere := []*openshell.Sandbox{
		{Name: "agent", Workspace: "team-a"},
		// The same name in another workspace is another sandbox.
		{Name: "agent", Workspace: "team-b"},
		{Name: "quiet", Workspace: "team-b"},
	}
	chunks := map[string][]openshell.PolicyChunk{
		"team-a/agent": {{ID: "c1", Status: "pending"}},
		"team-b/agent": {{ID: "c2", Status: "pending"}, {ID: "c3", Status: "pending", SecurityNotes: "broad"}},
	}
	teamB := models.DraftSummary{
		TotalPending: 2,
		Sandboxes: []models.DraftSandboxSummary{
			{Workspace: "team-b", SandboxName: "agent", PendingCount: 2, HasSecurityFlags: true},
		},
	}
	tests := []struct {
		name          string
		path          string
		wantWorkspace string
		listed        []*openshell.Sandbox
		wantOpts      []openshell.ListOptions
		want          models.DraftSummary
		wantNoList    bool
	}{
		{
			name:          "?workspace= is that workspace's summary",
			path:          "/draft-summary?workspace=team-b",
			wantWorkspace: "team-b",
			listed:        everywhere[1:],
			want:          teamB,
		},
		{
			name:          "the workspace route",
			path:          "/workspaces/team-b/draft-summary",
			wantWorkspace: "team-b",
			listed:        everywhere[1:],
			want:          teamB,
		},
		{
			// Each sandbox names its own workspace, which is what the entries
			// report: the empty workspace argument is only a fallback.
			name:     "?allWorkspaces=true is every workspace's summary",
			path:     "/draft-summary?allWorkspaces=true",
			listed:   everywhere,
			wantOpts: []openshell.ListOptions{{AllWorkspaces: true}},
			want: models.DraftSummary{
				TotalPending: 3,
				Sandboxes: []models.DraftSandboxSummary{
					{Workspace: "team-a", SandboxName: "agent", PendingCount: 1},
					{Workspace: "team-b", SandboxName: "agent", PendingCount: 2, HasSecurityFlags: true},
				},
			},
		},
		{
			// Neither is the old answer: empty, without a call to the gateway.
			name:       "neither names a scope, so nothing is summarized",
			path:       "/draft-summary",
			wantNoList: true,
			want:       models.DraftSummary{Sandboxes: []models.DraftSandboxSummary{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.listFn = func(_ context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				if tc.wantNoList {
					t.Errorf("sandboxes listed (workspace %q, %+v) for a summary that names no scope", workspace, opts)
					return nil, nil
				}
				if workspace != tc.wantWorkspace || !reflect.DeepEqual(opts, tc.wantOpts) {
					t.Errorf("sandboxes listed in %q with %+v, want workspace %q with %+v",
						workspace, opts, tc.wantWorkspace, tc.wantOpts)
				}
				return tc.listed, nil
			}
			inbox := &draftInbox{chunks: chunks}
			sdk.policy.getDraftFn = inbox.get

			w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, tc.path, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			var got models.DraftSummary
			decodeInto(t, w, &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("summary differs.\ngot:  %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

// The gateway answers an all-workspaces list for platform admins only. Its
// refusal is the answer, relayed as it is: the BFF does not authorize, and a
// 403 is not an empty summary.
func TestDraftSummaryAllWorkspacesRefused(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		return nil, &openshell.StatusError{Code: openshell.ErrorPermissionDenied, Message: "platform admin required"}
	}
	sdk.policy.getDraftFn = func(context.Context, string, string, ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
		t.Error("an inbox was read although the sandboxes could not be listed")
		return nil, nil
	}
	wantErrorResponse(t,
		serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/draft-summary?allWorkspaces=true", ""),
		http.StatusForbidden, apiutils.PermissionDenied)
}

// However many sandboxes a workspace has, only so many inboxes are read at
// once.
func TestDraftSummaryBoundsConcurrentReads(t *testing.T) {
	const sandboxCount = draftSummaryConcurrency*3 + 5
	names := make([]string, 0, sandboxCount)
	for i := range sandboxCount {
		names = append(names, fmt.Sprintf("sb-%d", i))
	}
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		return sandboxesNamed("team-a", names...), nil
	}
	var inFlight, peak, reads atomic.Int32
	sdk.policy.getDraftFn = func(context.Context, string, string, ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			seen := peak.Load()
			if now <= seen || peak.CompareAndSwap(seen, now) {
				break
			}
		}
		reads.Add(1)
		// Long enough for the other workers to be in a read of their own.
		time.Sleep(5 * time.Millisecond)
		return &openshell.DraftPolicy{Chunks: []openshell.PolicyChunk{{ID: "c", Status: "pending"}}}, nil
	}

	w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/workspaces/team-a/draft-summary", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got models.DraftSummary
	decodeInto(t, w, &got)
	if got.TotalPending != sandboxCount || len(got.Sandboxes) != sandboxCount {
		t.Errorf("summary covers %d sandboxes with %d pending, want %d of each", len(got.Sandboxes), got.TotalPending, sandboxCount)
	}
	if n := reads.Load(); n != sandboxCount {
		t.Errorf("inboxes read = %d, want one per sandbox (%d)", n, sandboxCount)
	}
	if p := peak.Load(); p > draftSummaryConcurrency {
		t.Errorf("%d inboxes were read at once, want at most %d", p, draftSummaryConcurrency)
	}
	if p := peak.Load(); p < 2 {
		t.Errorf("inboxes were read one at a time (peak %d), want them read concurrently", p)
	}
}

// One inbox that does not answer is given up on, and reported as unavailable,
// without waiting for it longer than the read timeout.
func TestDraftSummaryGivesUpOnASlowInbox(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		return sandboxesNamed("team-a", "stuck", "busy"), nil
	}
	sdk.policy.getDraftFn = func(ctx context.Context, _, name string, _ ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
		if name != "stuck" {
			return &openshell.DraftPolicy{Chunks: []openshell.PolicyChunk{{ID: "c", Status: "pending"}}}, nil
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("the read of an inbox has no deadline")
			return nil, context.Canceled
		}
		if left := time.Until(deadline); left > draftSummaryReadTimeout {
			t.Errorf("the read of an inbox may take %s, want at most %s", left, draftSummaryReadTimeout)
		}
		// What the SDK returns once the deadline passes, without the wait.
		return nil, &openshell.StatusError{Code: openshell.ErrorDeadlineExceeded, Message: "context deadline exceeded"}
	}

	w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/workspaces/team-a/draft-summary", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got models.DraftSummary
	decodeInto(t, w, &got)
	want := models.DraftSummary{
		TotalPending: 1,
		Sandboxes: []models.DraftSandboxSummary{
			{Workspace: "team-a", SandboxName: "stuck", Unavailable: true},
			{Workspace: "team-a", SandboxName: "busy", PendingCount: 1},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary differs.\ngot:  %+v\nwant: %+v", got, want)
	}
}

// A handler nobody gave a sandbox service cannot list sandboxes. Built with
// NewDraftsHandler alone it answers an empty summary on every summary route,
// not a 500.
func TestDraftSummaryWithoutSandboxService(t *testing.T) {
	handler := NewDraftsHandler(services.NewPolicyService((&mockSDK{}).Policy()))
	r := chi.NewRouter()
	r.Get("/draft-summary", handler.GetDraftSummary)
	r.Get("/workspaces/{workspace}/draft-summary", handler.GetWorkspaceDraftSummary)
	for _, path := range []string{"/draft-summary", "/draft-summary?workspace=team-a", "/workspaces/team-a/draft-summary"} {
		t.Run(path, func(t *testing.T) {
			wantEmptyDraftSummary(t, serve(t, r, http.MethodGet, path, ""))
		})
	}
}

func wantEmptyDraftSummary(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	// The array is there and empty, not null: a client maps over it.
	if got := strings.TrimSpace(w.Body.String()); got != `{"sandboxes":[],"totalPending":0}` {
		t.Errorf("body = %s, want an empty summary", got)
	}
}

// Without a workspace the route answers an empty summary, without a call to
// the gateway. A summary of every workspace would be a draft read per sandbox
// on the gateway, and a 403 for everyone who is not a platform admin.
func TestDraftSummaryWithoutAWorkspaceIsEmpty(t *testing.T) {
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(_ context.Context, workspace string, opts ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		t.Errorf("sandboxes listed (workspace %q, %+v) for a summary that names no workspace", workspace, opts)
		return nil, nil
	}
	sdk.policy.getDraftFn = func(context.Context, string, string, ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
		t.Error("an inbox was read for a summary that names no workspace")
		return nil, nil
	}
	wantEmptyDraftSummary(t, serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/draft-summary", ""))
}

// Once the client has gone, no further inbox is read, and a sandbox the
// summary did not get to is reported as unavailable, never as nothing
// pending.
func TestDraftSummaryStopsWhenNobodyIsWaiting(t *testing.T) {
	const sandboxCount = draftSummaryConcurrency * 4
	names := make([]string, sandboxCount)
	for i := range names {
		names[i] = fmt.Sprintf("sb-%03d", i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reads atomic.Int32
	sdk := &mockSDK{}
	sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
		return sandboxesNamed("team-a", names...), nil
	}
	sdk.policy.getDraftFn = func(context.Context, string, string, ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
		reads.Add(1)
		// The client goes away while the first inboxes are being read.
		cancel()
		return &openshell.DraftPolicy{}, nil
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/workspaces/team-a/draft-summary", nil).WithContext(ctx)
	draftSummaryRoutes(sdk).ServeHTTP(w, req)

	// A worker may already hold the next sandbox when the context ends, so
	// allow for the ones in flight, not for the whole list.
	if got := reads.Load(); got > draftSummaryConcurrency*2 {
		t.Errorf("%d of %d inboxes were read after the client had gone, want at most %d", got, sandboxCount, draftSummaryConcurrency*2)
	}
	var got models.DraftSummary
	decodeInto(t, w, &got)
	unavailable := 0
	for _, entry := range got.Sandboxes {
		if entry.Unavailable {
			unavailable++
		}
	}
	if want := sandboxCount - int(reads.Load()); unavailable != want {
		t.Errorf("%d sandboxes reported as unavailable, want the %d that were never read", unavailable, want)
	}
}

// Across workspaces there is no workspace to fall back on. A sandbox the
// gateway lists without one is reported unavailable and never read: the
// gateway refuses a selector with an empty name (hard fact 12). Any other
// sandbox in the same list is read as usual.
func TestDraftSummaryAllWorkspacesSandboxWithoutAWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		listed    []*openshell.Sandbox
		wantReads []draftRead
		want      models.DraftSummary
	}{
		{
			name:   "the only sandbox names no workspace",
			listed: []*openshell.Sandbox{{Name: "orphan"}},
			want: models.DraftSummary{
				Sandboxes: []models.DraftSandboxSummary{{SandboxName: "orphan", Unavailable: true}},
			},
			wantReads: []draftRead{},
		},
		{
			name: "one of several names no workspace",
			listed: []*openshell.Sandbox{
				{Name: "agent", Workspace: "team-a"},
				{Name: "orphan"},
				{Name: "quiet", Workspace: "team-b"},
			},
			want: models.DraftSummary{
				TotalPending: 1,
				Sandboxes: []models.DraftSandboxSummary{
					{Workspace: "team-a", SandboxName: "agent", PendingCount: 1},
					{SandboxName: "orphan", Unavailable: true},
				},
			},
			wantReads: []draftRead{
				{workspace: "team-a", sandbox: "agent", filter: "pending"},
				{workspace: "team-b", sandbox: "quiet", filter: "pending"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &mockSDK{}
			sdk.sandboxes.listFn = func(context.Context, string, ...openshell.ListOptions) ([]*openshell.Sandbox, error) {
				return tc.listed, nil
			}
			inbox := &draftInbox{chunks: map[string][]openshell.PolicyChunk{
				"team-a/agent": {{ID: "c1", Status: "pending"}},
			}}
			sdk.policy.getDraftFn = inbox.get

			w := serve(t, draftSummaryRoutes(sdk), http.MethodGet, "/draft-summary?allWorkspaces=true", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			var got models.DraftSummary
			decodeInto(t, w, &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("summary differs.\ngot:  %+v\nwant: %+v", got, tc.want)
			}
			want := map[draftRead]int{}
			for _, read := range tc.wantReads {
				want[read]++
			}
			if reads := inbox.readSet(); !reflect.DeepEqual(reads, want) {
				t.Errorf("draft reads = %v, want %v", reads, want)
			}
		})
	}
}
