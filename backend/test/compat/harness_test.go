//go:build compat

// Package compat is the gateway compatibility suite. It drives the BFF's REST
// API against a real OpenShell gateway and asserts the contracts the frontend
// depends on.
//
// It is build-tagged so `go test ./...` never picks it up — it needs a live
// stack. CI runs it against the one gateway the branch pins; see
// .github/workflows/ci.yml and deploy/ci/.
//
//	BFF_URL=http://localhost:9080 go test -tags compat -count=1 ./test/compat/... -v
//
// This suite is the only proof of the gateway <-> SDK link: the compiler and
// the unit tests prove that the BFF fits the SDK, but only a request that
// crosses the wire proves that the SDK we pin still speaks the protocol of a
// given gateway. So the map below is organized by what crosses the wire.
//
// # What a green run looks like
//
// Almost every test drives the gateway through the BFF's real routes. The ones
// in guard_test.go do not: they check the suite's own guards against canned
// answers and send the gateway nothing.
//
// A green run on a gateway of line 0.1 has no test that SKIPs. The skips
// the suite can produce all say why: a known bug (see the end of this comment,
// none at present), a gateway that is not this run's own (the writing subtests
// of TestGlobalSettings, TestGlobalPolicy and TestSandboxSettings, and all of
// TestSandboxSettingManagedGlobally), a compute driver other than the compat
// stack's Docker (TestRuntimeClassReachesTheDriver), a delete the gateway only
// accepted, or a BFF started with a gateway release line it could not read
// (TestGatewayCompatibility; CI sets none, so the built-in line is used). On
// the disposable gateway CI uses, any skip, or any failure, is news. The
// comment does not state how many tests there are: a count is wrong as soon
// as another change adds a test.
//
// # Coverage map
//
// Covered, one test per capability the UI depends on:
//
//	gateway        health, readiness, info with the negotiated
//	               extensions, auth config                     gateway_test.go
//	               the health every signed-in user can read    compatibility_test.go
//	settings       global settings read, set, delete, the
//	               refusal of a value a setting does not take,
//	               and the delete of a setting that is not set gateway_test.go
//	               a sandbox's settings read with their scope,
//	               set and delete, and the refusal of both
//	               while the key is set globally               sandbox_options_test.go
//	workspaces     lifecycle, label selector, delete envelope  workspace_test.go, contract_test.go
//	members        add, list, role change, remove              workspace_test.go
//	sandboxes      lifecycle, CPU and memory limits (reported
//	               back, and read from the sandbox's own
//	               cgroup) and log level, labels and the label
//	               selector, workspace isolation, stop and
//	               start, logs with the lines/level/source/
//	               since filters, exposed services             sandbox_test.go
//	               a stop of a sandbox whose main process has
//	               exited, completed or failed, and the start
//	               after it; suspend and resume, by the refusal
//	               this stack's driver gives both              sandbox_phases_test.go
//	               the main command and its terminal,
//	               environment, annotations and services
//	               exposed at create, read back; a runtime
//	               class and a driver config, by the refusal
//	               each gets on this stack                     sandbox_options_test.go
//	               a create without an image, which runs the
//	               gateway's default one; every starter policy
//	               of the create form, by a sandbox created
//	               from it; a template without an image and a
//	               sandbox made from it                        sandbox_create_defaults_test.go
//	               the pending draft chunks of a workspace's
//	               sandboxes, and the empty summary outside
//	               a workspace                                 draft_summary_test.go
//	exec           file upload, including one larger than a
//	               gRPC message the gateway accepts, and
//	               download (the direct gRPC call in
//	               pkg/clients/rawexec.go and the SDK's
//	               non-interactive Exec().Run), the terminal
//	               websocket (interactive exec)                exec_test.go
//	files          a folder up with its structure and a
//	               directory down as a tar, files of several
//	               megabytes both ways (a download is relayed
//	               from the SDK's Exec().Stream), a path that
//	               is missing, unreadable or a device refused
//	               before a byte is sent, and a transfer that
//	               breaks when tar fails after                 files_test.go
//	terminal       a session started with a command, a
//	               working directory, an environment and no
//	               login shell; the default shell when the
//	               start message chooses nothing; a first
//	               frame that is not a start message refused   terminal_options_test.go
//	policy         revisions, network-policy updates with and
//	               without a stale resource version, the
//	               sections a live sandbox refuses to change,
//	               the enum spelling, global policy, the draft
//	               inbox, incremental updates (add an endpoint,
//	               append an allow rule, remove an endpoint or
//	               a rule) that leave untouched rules as they
//	               were, one revision by number, the effective
//	               policy and its source                       policy_test.go, contract_test.go
//	providers      profiles (lint, import, get, update,
//	               delete), provider create/get/list/update/
//	               delete with write-only credentials whose
//	               keys are reported, from a workspace profile,
//	               a platform one and an id both hold, attach
//	               and detach on a sandbox including the stale
//	               resource version, credential refresh status provider_test.go
//	               an edit that sends only the configuration it
//	               changed, the key a credential with several
//	               is stored under, the refusal of a key the
//	               profile does not declare, a provider whose
//	               profile was deleted from under it           provider_form_test.go
//	templates      create/get/list/delete with a startup service
//	               level, create-from-template with a command
//	               and a service exposed                       template_test.go
//	all workspaces sandboxes, templates, providers (with their
//	               credential names) and service endpoints
//	               listed across workspaces, each carrying its
//	               own workspace; a workspace's endpoints; the
//	               unnamed service endpoint                    allworkspaces_test.go
//	list shapes    legacy arrays and opt-in cursor pages       contract_test.go, list_pagination_test.go
//
// Most of these tests run in a workspace other than "default" on purpose.
// Every workspace-scoped call carries a workspace selector, and only a second
// workspace can show that the selector is honored rather than merely present:
// a call that always said "default" would pass a suite that never used
// anything else.
//
// Deliberately not covered, and why:
//
//   - Inference routes and an exec endpoint of its own: the BFF has no such
//     routes. Non-interactive exec is reachable only through file transfer,
//     which is where it is covered.
//   - auth/whoami: the BFF answers it without calling the gateway, because
//     this stack runs with AUTH_DISABLED.
//   - Deciding a real draft chunk (approve, reject, edit, undo), and a draft
//     summary that counts one: chunks are produced only by the in-sandbox
//     supervisor's policy analysis, which a test cannot trigger on demand.
//     The endpoints are driven against an empty inbox instead, which still
//     proves each RPC reaches the gateway.
//   - A successful credential-refresh configuration: it needs a profile that
//     declares a token endpoint and a live OAuth server behind it.
//   - GPU requests on create: the compat stack has no GPU to give.
//   - A runtime class or a driver config that takes effect: the compat
//     stack's Docker driver refuses the first and its gateway has not enabled
//     the second. Each refusal is asserted, which proves the field arrives.
//   - A suspend and a resume that take effect: no compute driver upstream
//     ships can keep a sandbox's memory state, and the compat stack's Docker
//     driver refuses both. The refusal is asserted. The round trip is
//     written, runs only against a driver that advertises the capability, and
//     has never run.
//   - Request validation the BFF does on its own (bad names, bad paths,
//     malformed bodies): it never reaches the gateway, so it belongs in the
//     handler unit tests.
//   - WatchSandbox, ForwardTcp and SSH sessions: not wired into the BFF.
//
// # What the suite does to the gateway
//
// The suite is written for a disposable gateway, which is what CI and
// deploy/ci/e2e-stack.sh give it, but it must not damage one that is not.
// Almost everything it creates is its own and has a random name: workspaces,
// sandboxes (some of them in "default"), providers, templates, and one
// platform-scoped provider profile per provider test, which every workspace
// lists until that test ends.
//
// Three tests change state that belongs to the whole gateway, and all read
// before they write:
//
//   - TestGlobalSettings sets and unsets a setting, and only one the gateway
//     reports as unset. The value it writes is what the gateway does anyway
//     while the setting is unset.
//   - TestSandboxSettingManagedGlobally does the same with the same setting,
//     to see a sandbox report it and refuse to override it.
//   - TestGlobalPolicy sets and removes a global policy, and only when none is
//     in force. A global policy replaces the policy of every sandbox on the
//     gateway and blocks their own policy updates while it is set.
//
// All three also stand down when the gateway runs a sandbox this run did not
// create (see foreignSandboxes), because gateway scope wins over sandbox scope
// for settings and policy alike. In each of those cases the writing subtests
// skip and say exactly what they found, and the reads are still asserted. None
// of them removes anything it cannot show it wrote.
//
// TestDeleteUnsetGlobalSetting sends the gateway a delete too, for a setting
// it has just read as unset. That removes nothing and moves no revision, so
// it runs on a shared gateway as well.
//
// What a run cannot take back: every global policy it sets stays in the
// revision history as SUPERSEDED, and the settings revision counter moves on.
// A run that is killed leaves behind whatever its cleanups would have removed:
// workspaces, sandboxes, a platform profile and, if it dies in the instant
// between a set and its delete, that setting or global policy.
//
// # Known bugs
//
// A test that hits a product bug probes for it and calls t.Skip with a message
// starting "KNOWN BUG:" only when it sees that exact failure, so it starts
// asserting again by itself once the bug is fixed, and the probe is removed
// with the fix so that the bug coming back is a failure. There are none at
// present.
//
// A probe is only as good as the request it sends. Send what the UI sends: a
// test that keeps sending a request the UI no longer makes goes on reporting a
// bug that is gone.
package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	bffURL     string
	httpClient = &http.Client{Timeout: 30 * time.Second}

	// gatewayVersion is resolved once in TestMain and reported in failures, so
	// a failure says which gateway it was against: the pinned one in ci.yml, or
	// the upcoming release in follow-upstream.yml's check A.
	gatewayVersion = "unknown"
)

func TestMain(m *testing.M) {
	bffURL = strings.TrimSuffix(os.Getenv("BFF_URL"), "/")
	if bffURL == "" {
		bffURL = "http://localhost:9080"
	}

	if err := waitFor("/api/v1/healthz", 90*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "compat: BFF never became ready at %s: %v\n", bffURL, err)
		os.Exit(1)
	}
	// The gateway's gRPC port can open a moment after its health port, so a
	// stack that was only just started would fail its first tests for no
	// reason. Give it a short while to settle, but do not make this fatal: if
	// the gateway really is unreachable, the tests are what should say so.
	if err := waitFor("/api/v1/readyz", 30*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "compat: gateway not reachable through the BFF yet (%v) — running anyway\n", err)
	}
	// An error body decodes into an empty version without an error, and
	// "[gateway ]" in every failure message would say less than "unknown".
	if v, err := resolveGatewayVersion(); err == nil && v != "" {
		gatewayVersion = v
	}
	fmt.Printf("compat: BFF=%s gateway=%s\n", bffURL, gatewayVersion)

	code := m.Run()
	// os.Exit skips deferred calls, so the shared fixtures are released here.
	teardownShared()
	// When the gateway could not boot sandboxes, most of the failures above
	// have that one cause, so it is said once where a reader of the log looks
	// first: at the end.
	if summary := bootSummary(); summary != "" {
		fmt.Fprintf(os.Stderr, "compat: [gateway %s] %s\n", gatewayVersion, summary)
	}
	os.Exit(code)
}

// waitFor polls a BFF path until it answers 200 or the limit passes.
func waitFor(path string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	var last error
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(bffURL + path)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(time.Second)
	}
	return last
}

func resolveGatewayVersion() (string, error) {
	var info struct {
		GatewayVersion string `json:"gatewayVersion"`
	}
	if _, err := doJSON(http.MethodGet, "/api/v1/gateway", nil, &info); err != nil {
		return "", err
	}
	return info.GatewayVersion, nil
}

// doRequest is the one place that talks HTTP. It returns the status, the
// response headers and the raw body.
func doRequest(method, path, contentType string, body io.Reader) (int, http.Header, []byte, error) {
	req, err := http.NewRequest(method, bffURL+path, body)
	if err != nil {
		return 0, nil, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw, err
}

// do issues a JSON request and returns the status and raw body.
func do(method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	contentType := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
		contentType = "application/json"
		noteSandboxCreate(method, path, b)
	}
	status, _, raw, err := doRequest(method, path, contentType, rdr)
	return status, raw, err
}

// doJSON issues a request and decodes a successful body into out.
func doJSON(method, path string, body, out any) (int, error) {
	status, raw, err := do(method, path, body)
	if err != nil {
		return status, err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return status, fmt.Errorf("decode %s %s (status %d): %w; body: %s", method, path, status, err, truncate(raw))
		}
	}
	return status, nil
}

// mustRaw fails the test unless the call returns wantStatus, and returns the
// body as it was sent. It is for the checks that look at the JSON itself: that
// a list is an array and not null, or that a secret is nowhere in the response.
func mustRaw(t *testing.T, method, path string, body any, wantStatus int) []byte {
	t.Helper()
	status, raw, err := do(method, path, body)
	if err != nil {
		t.Fatalf("%s %s [gateway %s]: %v", method, path, gatewayVersion, err)
	}
	if status != wantStatus {
		t.Fatalf("%s %s [gateway %s]: status = %d, want %d; body: %s",
			method, path, gatewayVersion, status, wantStatus, truncate(raw))
	}
	return raw
}

// mustDecode fails the test unless raw decodes into out.
func mustDecode(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode [gateway %s]: %v; body: %s", gatewayVersion, err, truncate(raw))
	}
}

// mustJSON fails the test unless the call returns wantStatus, and decodes the
// body into out when out is not nil.
func mustJSON(t *testing.T, method, path string, body, out any, wantStatus int) {
	t.Helper()
	raw := mustRaw(t, method, path, body, wantStatus)
	if out != nil && len(raw) > 0 {
		mustDecode(t, raw, out)
	}
}

// apiError mirrors apiutils.ErrorResponse, the envelope every BFF error uses.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// wantError reports a failure unless the call is refused with wantStatus and
// an error envelope carrying wantCode. The frontend branches on both, so a
// refusal that arrives as a 500 or under a different code is a contract change
// even though the call still "fails".
func wantError(t *testing.T, method, path string, body any, wantStatus int, wantCode string) {
	t.Helper()
	status, raw, err := do(method, path, body)
	if err != nil {
		t.Fatalf("%s %s [gateway %s]: %v", method, path, gatewayVersion, err)
	}
	checkError(t, method+" "+path, status, raw, wantStatus, wantCode)
}

// checkError is wantError for a response the caller already holds, which is
// the case for the upload and download helpers.
func checkError(t *testing.T, what string, status int, raw []byte, wantStatus int, wantCode string) {
	t.Helper()
	var env apiError
	_ = json.Unmarshal(raw, &env)
	if status != wantStatus || env.Code != wantCode {
		t.Errorf("%s [gateway %s]: status = %d code = %q, want %d %q; body: %s",
			what, gatewayVersion, status, env.Code, wantStatus, wantCode, truncate(raw))
	}
}

// truncate renders a response body for a failure message. The BFF ends every
// JSON body with a newline, which would otherwise break the message in two.
func truncate(b []byte) string {
	const limit = 800
	b = bytes.TrimSpace(b)
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "...(truncated)"
}

// poll calls fn until it returns true or the deadline passes.
func poll(t *testing.T, limit, every time.Duration, what string, fn func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(limit)
	last := ""
	for time.Now().Before(deadline) {
		ok, detail := fn()
		if ok {
			return
		}
		last = detail
		time.Sleep(every)
	}
	t.Fatalf("timed out after %s waiting for %s [gateway %s]; last state: %s", limit, what, gatewayVersion, last)
}

// uploadFile posts one file to a sandbox the way the browser does: a
// multipart form with a "file" part, and the destination directory as a query
// parameter (empty means the BFF's default, /sandbox).
func uploadFile(workspace, sandboxName, destDir, filename string, content []byte) (int, []byte, error) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		return 0, nil, err
	}
	if _, err = part.Write(content); err != nil {
		return 0, nil, err
	}
	if err = form.Close(); err != nil {
		return 0, nil, err
	}
	path := sandboxPath(workspace, sandboxName) + "/files"
	if destDir != "" {
		path += "?dest=" + url.QueryEscape(destDir)
	}
	status, _, raw, err := doRequest(http.MethodPost, path, form.FormDataContentType(), &buf)
	return status, raw, err
}

// downloadFile fetches one file from a sandbox by absolute path.
func downloadFile(workspace, sandboxName, filePath string) (int, http.Header, []byte, error) {
	return doRequest(http.MethodGet,
		sandboxPath(workspace, sandboxName)+"/files?path="+url.QueryEscape(filePath), "", nil)
}

// uploadResult mirrors the JSON the upload endpoint answers with.
type uploadResult struct {
	Path     string `json:"path"`
	Size     int    `json:"size"`
	ExitCode int    `json:"exitCode"`
	Success  bool   `json:"success"`
}

// mustUpload uploads one file and fails the test unless the BFF answers 200.
func mustUpload(t *testing.T, workspace, sandboxName, destDir, filename string, content []byte) uploadResult {
	t.Helper()
	status, raw, err := uploadFile(workspace, sandboxName, destDir, filename, content)
	if err != nil {
		t.Fatalf("upload %s: %v", filename, err)
	}
	if status != http.StatusOK {
		t.Fatalf("upload %s (%d bytes) [gateway %s]: status = %d, want 200; body: %s",
			filename, len(content), gatewayVersion, status, truncate(raw))
	}
	var res uploadResult
	mustDecode(t, raw, &res)
	return res
}

// mustDownload downloads one file and fails the test unless the BFF answers
// 200.
func mustDownload(t *testing.T, workspace, sandboxName, filePath string) ([]byte, http.Header) {
	t.Helper()
	status, header, raw, err := downloadFile(workspace, sandboxName, filePath)
	if err != nil {
		t.Fatalf("download %s: %v", filePath, err)
	}
	if status != http.StatusOK {
		t.Fatalf("download %s [gateway %s]: status = %d, want 200; body: %s",
			filePath, gatewayVersion, status, truncate(raw))
	}
	return raw, header
}

// requireSandboxes skips tests that boot a sandbox when -short is set, and
// fails them at once when an earlier test has already shown that sandboxes do
// not boot on this gateway (see bootFailure).
func requireSandboxes(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping: needs a running sandbox, which -short excludes")
	}
	if bootFailure != "" {
		t.Fatalf("[gateway %s] %s", gatewayVersion, bootFailure)
	}
}

// ownSandboxes holds every sandbox this process has asked the gateway to
// create, as "workspace/name". It is what lets the tests that change
// gateway-global state tell a gateway they have to themselves from one that
// other people are using. Tests run one after another, so it needs no lock.
var ownSandboxes = map[string]bool{}

// noteSandboxCreate records the sandbox a create request names as this run's
// own. do calls it for every JSON request, so no test has to remember to: a
// sandbox that went untracked would look like somebody else's, and the tests
// that change gateway-global state would stand down in CI without anyone
// noticing. It runs before the request is sent, because a sandbox that came
// into being although the request failed is still ours.
func noteSandboxCreate(method, path string, body []byte) {
	if method != http.MethodPost {
		return
	}
	rest, ok := strings.CutPrefix(path, "/api/v1/workspaces/")
	if !ok {
		return
	}
	workspace, tail, _ := strings.Cut(rest, "/")
	if tail != "sandboxes" && tail != "sandboxes/from-template" {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &req) == nil && req.Name != "" {
		ownSandboxes[workspace+"/"+req.Name] = true
	}
}

// notOwn returns the entries of listed, each a "workspace/name", that this
// process did not create.
func notOwn(listed []string) []string {
	var foreign []string
	for _, id := range listed {
		if !ownSandboxes[id] {
			foreign = append(foreign, id)
		}
	}
	return foreign
}

// foreignSandboxes lists the sandboxes on the gateway that this process did
// not create: somebody's real workload, or another run of this suite.
//
// A global policy and a global setting both override what every sandbox on the
// gateway has for itself, for as long as they are set. That is harmless on a
// gateway where all the sandboxes are this run's own, and it is not something
// a test may do to anybody else's.
func foreignSandboxes(t *testing.T) []string {
	t.Helper()
	var workspaces []workspace
	mustJSON(t, http.MethodGet, "/api/v1/workspaces", nil, &workspaces, http.StatusOK)
	var listed []string
	for _, ws := range workspaces {
		path := sandboxesPath(ws.Metadata.Name)
		status, raw, err := do(http.MethodGet, path, nil)
		if err != nil {
			t.Fatalf("GET %s [gateway %s]: %v", path, gatewayVersion, err)
		}
		if status == http.StatusNotFound {
			// Deleted between the two calls, as this suite's own workspaces are.
			continue
		}
		if status != http.StatusOK {
			t.Fatalf("GET %s [gateway %s]: status = %d, want 200; body: %s", path, gatewayVersion, status, truncate(raw))
		}
		var sandboxes []sandbox
		mustDecode(t, raw, &sandboxes)
		for _, sb := range sandboxes {
			listed = append(listed, ws.Metadata.Name+"/"+sb.Metadata.Name)
		}
	}
	return notOwn(listed)
}

// sharedWithOthers returns why this run must leave gateway-global state alone,
// or "" when every sandbox on the gateway is its own.
func sharedWithOthers(t *testing.T) string {
	t.Helper()
	foreign := foreignSandboxes(t)
	if len(foreign) == 0 {
		return ""
	}
	return fmt.Sprintf("gateway %s runs %d sandbox(es) this run did not create (%s), and a gateway-global "+
		"setting or policy overrides what each of them has for itself — so this is not a gateway the suite "+
		"has to itself, and it leaves the global state alone",
		gatewayVersion, len(foreign), strings.Join(foreign, ", "))
}

// shared is one READY sandbox in its own workspace, booted on first use and
// kept until the process exits. Booting and deleting a sandbox costs several
// seconds, so the tests that only need "some running sandbox" (file transfer,
// the terminal, logs, services, the draft inbox, provider attachment) share
// this one. Tests that change a sandbox's lifecycle or assert exact policy
// revision numbers create their own.
//
// It outlives a single test and, under -count=N, every repetition, so tests
// that use it must leave it as they found it and must not assume it is new.
var shared struct {
	err       error
	workspace string
	sandbox   string
	// runID labels the shared sandbox and is exported into its environment.
	// It is random so a selector or an environment check cannot be satisfied
	// by something another run left behind.
	runID string
	once  sync.Once
}

const (
	sharedLabelKey = "compat-run"
	sharedEnvKey   = "COMPAT_RUN"
)

// sharedSandbox returns the workspace and name of the shared sandbox, booting
// it on first use.
func sharedSandbox(t *testing.T) (workspace, name string) {
	t.Helper()
	requireSandboxes(t)
	// Everything inside Do reports through shared.err rather than t: a
	// t.Fatal in there would end only the first caller, and sync.Once would
	// hand every later caller a half-built fixture with no error.
	shared.once.Do(func() {
		shared.runID = randName("run")
		ws := randName("cx")
		if status, raw, err := do(http.MethodPost, "/api/v1/workspaces", map[string]any{"name": ws}); err != nil || status != http.StatusCreated {
			shared.err = fmt.Errorf("create workspace %s: status %d, err %v, body %s", ws, status, err, truncate(raw))
			return
		}
		shared.workspace = ws

		sb := randName("cx")
		status, raw, err := do(http.MethodPost, sandboxesPath(ws), map[string]any{
			"name":        sb,
			"image":       sandboxImage(),
			"policy":      basePolicy(),
			"labels":      map[string]string{sharedLabelKey: shared.runID, "tier": "shared"},
			"annotations": map[string]string{"compat/purpose": "shared fixture"},
			"environment": map[string]string{sharedEnvKey: shared.runID},
		})
		if err != nil || status != http.StatusCreated {
			shared.err = fmt.Errorf("create sandbox %s/%s: status %d, err %v, body %s", ws, sb, status, err, truncate(raw))
			return
		}
		shared.sandbox = sb
		if _, err := awaitReady(t.Name(), ws, sb, readyLimit); err != nil {
			shared.err = err
		}
	})
	if shared.err != nil {
		t.Fatalf("shared sandbox is not available [gateway %s]: %v", gatewayVersion, shared.err)
	}
	return shared.workspace, shared.sandbox
}

func teardownShared() {
	if shared.sandbox != "" {
		_, _, _ = do(http.MethodDelete, sandboxPath(shared.workspace, shared.sandbox), nil)
	}
	if shared.workspace != "" {
		removeWorkspace(shared.workspace)
	}
}

// profileCredentialKey is the one credential the profiles of this suite
// require, seeded or imported. The gateway keys a provider's credentials by
// environment variable name, not by the credential's own name, so here the
// credential is named after its variable and the two agree.
// TestProviderCredentialKeyedByEnvVar seeds a profile where they differ, as
// they do in the profiles upstream publishes.
const profileCredentialKey = "COMPAT_API_KEY"

// credentialSchema is one credential of a provider profile, as the BFF takes
// it in a profile body.
type credentialSchema struct {
	Name     string   `json:"name"`
	EnvVars  []string `json:"envVars,omitempty"`
	Required bool     `json:"required"`
}

// agreeingCredential is the credential schema described at
// profileCredentialKey.
func agreeingCredential() credentialSchema {
	return credentialSchema{
		Name: profileCredentialKey, EnvVars: []string{profileCredentialKey}, Required: true,
	}
}

// seedPlatformProfile imports a platform-scoped provider profile with one
// credential and returns its id, which is the provider "type" to create
// against.
//
// The dashboard also has to work with the profiles a workspace did not import:
// the ones a gateway ships and the ones an admin imports for every workspace.
// The compat stack's gateway ships none (it logs `provider profile sources
// configured sources=["user"]`), so a platform profile is seeded to stand in
// for them. TestProviderFromWorkspaceProfile covers the other kind, imported
// into a workspace.
//
// It goes through the BFF's own platform route, POST /api/v1/provider-profiles,
// like everything else in this suite: every test that seeds a profile then
// also shows that the route works. Until the BFF had that route this helper
// went around it, with an SDK connection of its own to the gateway.
func seedPlatformProfile(t *testing.T, credential credentialSchema) string {
	t.Helper()
	id := randName("cpp")
	path := platformProfilesPath()
	var res struct {
		Diagnostics []profileDiagnostic `json:"diagnostics"`
		Imported    bool                `json:"imported"`
	}
	// Registered before the import: a platform profile is listed in every
	// workspace, and an import that applied but whose answer was lost must
	// not leave one behind. Deleting an id that was never imported is a 404.
	t.Cleanup(func() {
		_, _, _ = do(http.MethodDelete, path+"/"+id, nil)
	})
	mustJSON(t, http.MethodPost, path, map[string]any{"profiles": []any{map[string]any{
		"id":               id,
		"displayName":      "Compat platform profile",
		"description":      "seeded by backend/test/compat",
		"category":         "INFERENCE",
		"inferenceCapable": true,
		"credentials":      []credentialSchema{credential},
	}}}, &res, http.StatusCreated)
	if !res.Imported {
		t.Fatalf("seed platform profile %s [gateway %s]: the gateway did not import it; diagnostics: %+v",
			id, gatewayVersion, res.Diagnostics)
	}
	return id
}
