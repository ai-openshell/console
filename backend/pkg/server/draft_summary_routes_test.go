package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/fake"

	"github.com/ai-openshell/console/backend/pkg/auth"
	"github.com/ai-openshell/console/backend/pkg/models"
)

// withDrafts is an SDK client whose draft inboxes come from a fixed table
// keyed by "workspace/sandbox". The SDK's fake client answers every draft
// call "unimplemented".
type withDrafts struct {
	openshell.ClientInterface
	inbox *pendingInbox
}

func (c withDrafts) Policy() openshell.PolicyInterface {
	return fixedDrafts{PolicyInterface: c.ClientInterface.Policy(), inbox: c.inbox}
}

type pendingInbox struct {
	pending map[string]int
	reads   []string
	mu      sync.Mutex
}

type fixedDrafts struct {
	openshell.PolicyInterface
	inbox *pendingInbox
}

func (d fixedDrafts) GetDraft(_ context.Context, workspace, name string, _ ...openshell.GetDraftOption) (*openshell.DraftPolicy, error) {
	key := workspace + "/" + name
	d.inbox.mu.Lock()
	d.inbox.reads = append(d.inbox.reads, key)
	d.inbox.mu.Unlock()
	return &openshell.DraftPolicy{Chunks: make([]openshell.PolicyChunk, d.inbox.pending[key])}, nil
}

// draftSummaryApp is the app's real router over a gateway that holds a
// sandbox named "agent" with pending chunks in each of two workspaces, and
// one with none.
func draftSummaryApp(t *testing.T, authMiddleware auth.MiddlewareInterface) (http.Handler, *pendingInbox) {
	t.Helper()
	ctx := context.Background()
	sdk := fake.NewClient()
	for workspace, names := range map[string][]string{"team-a": {"agent", "quiet"}, "team-b": {"agent"}} {
		if _, err := sdk.Workspaces().Create(ctx, workspace, nil); err != nil {
			t.Fatalf("create workspace %s: %v", workspace, err)
		}
		for _, name := range names {
			if _, err := sdk.Sandboxes().Create(ctx, workspace, name, &openshell.SandboxSpec{}); err != nil {
				t.Fatalf("create sandbox %s/%s: %v", workspace, name, err)
			}
		}
	}
	inbox := &pendingInbox{pending: map[string]int{"team-a/agent": 2, "team-b/agent": 1}}
	app := NewApp(withDrafts{ClientInterface: sdk, inbox: inbox}, nil, authMiddleware, "", models.AuthConfigResponse{})
	return app.Routes(), inbox
}

// The draft-summary routes are registered on the real router, the workspace
// one with the workspace taken from the path. A route registered with another
// parameter name would summarize the default workspace for every workspace
// without any handler test noticing.
func TestDraftSummaryRoutes(t *testing.T) {
	tests := []struct {
		want map[string]int
		name string
		path string
	}{
		{
			name: "a workspace",
			path: "/api/v1/workspaces/team-a/draft-summary",
			want: map[string]int{"team-a/agent": 2},
		},
		{
			name: "another workspace",
			path: "/api/v1/workspaces/team-b/draft-summary",
			want: map[string]int{"team-b/agent": 1},
		},
		{
			name: "the route outside a workspace, with ?workspace=",
			path: "/api/v1/draft-summary?workspace=team-b",
			want: map[string]int{"team-b/agent": 1},
		},
		{
			// Empty: see DraftsHandler.GetDraftSummary.
			name: "the route outside a workspace, without one",
			path: "/api/v1/draft-summary",
			want: map[string]int{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := draftSummaryApp(t, auth.New(auth.Config{Disabled: true}))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s = %d; body: %s", tc.path, recorder.Code, recorder.Body.String())
			}
			var summary models.DraftSummary
			if err := json.Unmarshal(recorder.Body.Bytes(), &summary); err != nil {
				t.Fatalf("decode GET %s: %v; body: %s", tc.path, err, recorder.Body.String())
			}
			got, total := map[string]int{}, 0
			for _, entry := range summary.Sandboxes {
				got[entry.Workspace+"/"+entry.SandboxName] = entry.PendingCount
				total += entry.PendingCount
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("pending per sandbox = %v, want %v", got, tc.want)
			}
			if summary.TotalPending != total {
				t.Errorf("totalPending = %d, want the sum of the entries, %d", summary.TotalPending, total)
			}
		})
	}
}

// The summaries sit behind the same authentication as every other gateway
// call: without a bearer they answer 401 and read no inbox.
func TestDraftSummaryRoutesRequireAuth(t *testing.T) {
	router, inbox := draftSummaryApp(t, auth.New(auth.Config{}))
	for _, path := range []string{"/api/v1/draft-summary", "/api/v1/workspaces/team-a/draft-summary"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a bearer = %d, want 401", path, recorder.Code)
		}
	}
	if len(inbox.reads) != 0 {
		t.Errorf("unauthenticated requests read draft inboxes: %v", inbox.reads)
	}
}
