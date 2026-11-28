package codeinterpreter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox/codeinterpreter"
)

// statefulGateway models the parts of the real gateway the network policy
// depends on: a session request replaces a worker whose mode differs, an exec
// without a worker implicitly creates an isolated one, and workers can vanish.
type statefulGateway struct {
	mu       sync.Mutex
	workers  map[string]string // user_uuid -> network mode of the live worker
	requests []string          // "session:<mode>", "exec:<mode>", "release"
	sessions []string          // modes requested, in order

	sessionCode int
	sessionBody string
	// denied mirrors the gateway's revoked state: public sessions get 403.
	denied bool
	// policyCode, when set, makes PUT /network-policy answer with that status.
	policyCode  int
	policyCalls []bool
	// onSession runs inside the session handler before it answers.
	onSession func(mode string)
	// inFlight tracks concurrent session/release requests per user.
	inFlight    map[string]int
	maxInFlight int
	delay       time.Duration
}

func newStatefulGateway() *statefulGateway {
	return &statefulGateway{workers: map[string]string{}, inFlight: map[string]int{}}
}

func (g *statefulGateway) enter(user string) func() {
	g.mu.Lock()
	g.inFlight[user]++
	if g.inFlight[user] > g.maxInFlight {
		g.maxInFlight = g.inFlight[user]
	}
	g.mu.Unlock()
	if g.delay > 0 {
		time.Sleep(g.delay)
	}
	return func() {
		g.mu.Lock()
		g.inFlight[user]--
		g.mu.Unlock()
	}
}

func (g *statefulGateway) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "tok" {
			t.Errorf("request %s missing auth token", r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserUUID == "" || body.Profile != "minimal" {
				t.Errorf("invalid session request: %v %+v", err, body)
			}
			defer g.enter(body.UserUUID)()
			if g.onSession != nil {
				g.onSession(body.Network)
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			g.requests = append(g.requests, "session:"+body.Network)
			g.sessions = append(g.sessions, body.Network)
			if g.sessionCode != 0 {
				w.WriteHeader(g.sessionCode)
				_, _ = w.Write([]byte(g.sessionBody))
				return
			}
			if g.denied && body.Network == "public" {
				http.Error(w, "revoked", http.StatusForbidden)
				return
			}
			g.workers[body.UserUUID] = body.Network
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/shell/exec":
			user := r.URL.Query().Get("user_uuid")
			g.mu.Lock()
			mode, ok := g.workers[user]
			if !ok {
				mode = "isolated"
				g.workers[user] = mode
			}
			g.requests = append(g.requests, "exec:"+mode)
			g.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"stdout":"` + mode + `","stderr":"","exit_code":0,"timed_out":false}`))
		case "/api/v1/network-policy":
			var body struct {
				Profile       string `json:"profile"`
				PublicAllowed bool   `json:"public_allowed"`
			}
			if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&body) != nil || body.Profile != "minimal" {
				t.Errorf("invalid network policy request: %s %+v", r.Method, body)
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			g.policyCalls = append(g.policyCalls, body.PublicAllowed)
			if g.policyCode != 0 {
				http.Error(w, "nope", g.policyCode)
				return
			}
			g.denied = !body.PublicAllowed
			if !body.PublicAllowed {
				for user, mode := range g.workers {
					if mode == "public" {
						delete(g.workers, user)
					}
				}
			}
			_, _ = w.Write([]byte(`{"profile":"minimal","public_allowed":` + map[bool]string{true: "true", false: "false"}[body.PublicAllowed] + `,"revoked_workers":0}`))
		case "/api/v1/release":
			user := r.URL.Query().Get("user_uuid")
			defer g.enter(user)()
			g.mu.Lock()
			delete(g.workers, user)
			g.requests = append(g.requests, "release")
			g.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
}

func (g *statefulGateway) evict(user string) {
	g.mu.Lock()
	delete(g.workers, user)
	g.mu.Unlock()
}

func (g *statefulGateway) mode(user string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.workers[user]
}

func (g *statefulGateway) sessionModes() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.sessions...)
}

func newNetworkClient(srv *httptest.Server, allow bool, opts ...codeinterpreter.Option) *codeinterpreter.Client {
	opts = append([]codeinterpreter.Option{codeinterpreter.WithHTTPClient(srv.Client())}, opts...)
	return codeinterpreter.New(sandbox.Config{
		BaseURL: srv.URL, AuthToken: "tok", SessionNamespace: "ns", AllowNetwork: allow,
	}, opts...)
}

// execMode runs one command and returns the network mode of the worker that ran it.
func execMode(t *testing.T, c *codeinterpreter.Client, session string) string {
	t.Helper()
	res, err := c.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: session, Command: "true", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("exec %s: %v", session, err)
	}
	return res.Stdout
}

func TestExecRequestsPublicNetworkOnlyWhenAllowed(t *testing.T) {
	for _, tc := range []struct {
		allow bool
		want  string
	}{{false, "isolated"}, {true, "public"}} {
		gw := newStatefulGateway()
		srv := httptest.NewServer(gw.handler(t))
		got := execMode(t, newNetworkClient(srv, tc.allow), "s1")
		srv.Close()
		if got != tc.want {
			t.Fatalf("allow=%v: command ran in %q worker, want %q", tc.allow, got, tc.want)
		}
		if strings.Join(gw.requests, ",") != "session:"+tc.want+",exec:"+tc.want {
			t.Fatalf("session must be provisioned before exec, got %v", gw.requests)
		}
	}
}

func TestPolicyIsReassertedBeforeEveryCommand(t *testing.T) {
	gw := newStatefulGateway()
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	client := newNetworkClient(srv, true)
	user := client.SessionIDToUUID("s1")

	if got := execMode(t, client, "s1"); got != "public" {
		t.Fatalf("first command ran in %q worker", got)
	}
	// The gateway recycles an idle worker; a previous success must not be trusted.
	gw.evict(user)
	if got := execMode(t, client, "s1"); got != "public" {
		t.Fatalf("after eviction the command ran in a %q worker, want public", got)
	}
	if modes := gw.sessionModes(); len(modes) != 2 {
		t.Fatalf("policy must be requested for every command, got %v", modes)
	}
}

func TestReleaseForgetsNothingAndNextCommandReprovisions(t *testing.T) {
	gw := newStatefulGateway()
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	client := newNetworkClient(srv, true)
	execMode(t, client, "s1")
	if err := client.Release(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if got := execMode(t, client, "s1"); got != "public" {
		t.Fatalf("command after release ran in %q worker", got)
	}
}

func TestSetPublicNetworkRevokesWorkersTheProcessNeverTracked(t *testing.T) {
	gw := newStatefulGateway()
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	var allow atomic.Bool
	allow.Store(true)
	live := codeinterpreter.WithNetworkPolicy(allow.Load)
	staleClient := newNetworkClient(srv, true, live)
	execMode(t, staleClient, "a")
	execMode(t, staleClient, "b")
	execMode(t, staleClient, "idle")

	allow.Store(false)
	// A fresh client (as after a restart or on another replica) knows no sessions.
	if err := newNetworkClient(srv, false).SetPublicNetwork(context.Background(), false); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for _, id := range []string{"a", "b", "idle"} {
		if got := gw.mode(staleClient.SessionIDToUUID(id)); got != "" {
			t.Fatalf("session %s still holds a %q worker after revocation", id, got)
		}
	}
	if got := execMode(t, staleClient, "a"); got != "isolated" {
		t.Fatalf("command after revocation ran in a %q worker", got)
	}
	if len(gw.sessionModes()) != 4 {
		t.Fatalf("revocation must not provision sessions for stale uuids: %v", gw.sessionModes())
	}
}

func TestSetPublicNetworkNeverReportsSuccessWithoutGatewayAcknowledgement(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusInternalServerError} {
		gw := newStatefulGateway()
		gw.policyCode = code
		srv := httptest.NewServer(gw.handler(t))
		err := newNetworkClient(srv, false).SetPublicNetwork(context.Background(), false)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "network-policy") {
			t.Fatalf("status %d: revocation must fail, got %v", code, err)
		}
	}
}

// An old-generation command that read the switch before it was turned off must
// not be able to create a public worker after the gateway revoked access.
func TestStaleCommandCannotProvisionPublicAfterRevocation(t *testing.T) {
	gw := newStatefulGateway()
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	revoker := newNetworkClient(srv, false)
	var once sync.Once
	stale := newNetworkClient(srv, true, codeinterpreter.WithNetworkPolicy(func() bool {
		once.Do(func() { // the old request has read "allowed", then the switch is turned off
			if err := revoker.SetPublicNetwork(context.Background(), false); err != nil {
				t.Error(err)
			}
		})
		return true
	}))
	_, err := stale.Exec(context.Background(), sandbox.ExecRequest{SessionID: "s1", Command: "true", Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("stale public provisioning must be rejected, got %v", err)
	}
	for _, step := range gw.requests {
		if strings.HasPrefix(step, "exec") {
			t.Fatalf("command ran despite the rejected session: %v", gw.requests)
		}
	}
	if got := gw.mode(stale.SessionIDToUUID("s1")); got == "public" {
		t.Fatal("a public worker exists after revocation")
	}
}

// A command prepared while the switch flips must not run in a public worker that
// was provisioned after revocation finished.
func TestSwitchTurnedOffWhileProvisioningNeverRunsPublic(t *testing.T) {
	gw := newStatefulGateway()
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	var allow atomic.Bool
	allow.Store(true)
	gw.onSession = func(mode string) {
		if mode == "public" {
			allow.Store(false) // the setting changes while the old-generation request is in flight
		}
	}
	client := newNetworkClient(srv, true, codeinterpreter.WithNetworkPolicy(allow.Load))
	if got := execMode(t, client, "s1"); got != "isolated" {
		t.Fatalf("command ran in a %q worker although the switch was already off", got)
	}
	modes := gw.sessionModes()
	if len(modes) != 2 || modes[0] != "public" || modes[1] != "isolated" {
		t.Fatalf("expected public then corrective isolated provisioning, got %v", modes)
	}
}

func TestConcurrentCommandsAndReleaseSerializeSessionProvisioning(t *testing.T) {
	gw := newStatefulGateway()
	gw.delay = 20 * time.Millisecond
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	// Two clients model two configuration generations or instances sharing a session.
	a := newNetworkClient(srv, true)
	b := newNetworkClient(srv, true)

	var wg sync.WaitGroup
	for i := range 6 {
		client := a
		if i%2 == 1 {
			client = b
		}
		wg.Go(func() {
			_, err := client.Exec(context.Background(), sandbox.ExecRequest{SessionID: "shared", Command: "true", Timeout: 5 * time.Second})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Go(func() {
		if err := a.Release(context.Background(), "shared"); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()

	gw.mu.Lock()
	defer gw.mu.Unlock()
	if gw.maxInFlight != 1 {
		t.Fatalf("session provisioning and release overlapped (max %d in flight)", gw.maxInFlight)
	}
}

func TestPublicNetworkFailsClosedWhenSessionSetupFails(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		gw := newStatefulGateway()
		gw.sessionCode, gw.sessionBody = code, "nope"
		srv := httptest.NewServer(gw.handler(t))
		_, err := newNetworkClient(srv, true).Exec(context.Background(), sandbox.ExecRequest{SessionID: "s1", Command: "true", Timeout: time.Second})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "network=public") {
			t.Fatalf("status %d: want session setup error, got %v", code, err)
		}
		for _, step := range gw.requests {
			if strings.HasPrefix(step, "exec") {
				t.Fatalf("status %d: command must not run without the requested network policy", code)
			}
		}
	}
}

func TestGatewayWithoutSessionsEndpointFailsClosedInBothModes(t *testing.T) {
	for _, allow := range []bool{false, true} {
		for _, code := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusInternalServerError} {
			gw := newStatefulGateway()
			gw.sessionCode, gw.sessionBody = code, "Not Found"
			srv := httptest.NewServer(gw.handler(t))
			_, err := newNetworkClient(srv, allow).Exec(context.Background(), sandbox.ExecRequest{SessionID: "s1", Command: "true", Timeout: time.Second})
			srv.Close()
			if err == nil {
				t.Fatalf("allow=%v status %d: the mode cannot be enforced, the command must not run", allow, code)
			}
			for _, step := range gw.requests {
				if strings.HasPrefix(step, "exec") {
					t.Fatalf("allow=%v status %d: command ran: %v", allow, code, gw.requests)
				}
			}
		}
	}
}
