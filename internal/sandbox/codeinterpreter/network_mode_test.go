package codeinterpreter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox/codeinterpreter"
)

type sessionGateway struct {
	mu          sync.Mutex
	networks    []string
	profiles    []string
	order       []string
	sessionCode int
	sessionBody string
}

func (g *sessionGateway) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserUUID == "" {
				t.Errorf("invalid session request: %v", err)
			}
			if r.Header.Get("X-Auth-Token") != "tok" {
				t.Errorf("session request missing auth token")
			}
			g.networks = append(g.networks, body.Network)
			g.profiles = append(g.profiles, body.Profile)
			g.order = append(g.order, "session")
			if g.sessionCode != 0 {
				w.WriteHeader(g.sessionCode)
				_, _ = w.Write([]byte(g.sessionBody))
				return
			}
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/shell/exec":
			g.order = append(g.order, "exec")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"stdout":"ok","stderr":"","exit_code":0,"timed_out":false}`))
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
}

func newNetworkClient(srv *httptest.Server, allow bool) *codeinterpreter.Client {
	return codeinterpreter.New(sandbox.Config{
		BaseURL: srv.URL, AuthToken: "tok", SessionNamespace: "ns", AllowNetwork: allow,
	}, codeinterpreter.WithHTTPClient(srv.Client()))
}

func execOnce(t *testing.T, c *codeinterpreter.Client, session string) error {
	t.Helper()
	_, err := c.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: session, Command: "true", Timeout: 5 * time.Second,
	})
	return err
}

func TestExecRequestsPublicNetworkOnlyWhenAllowed(t *testing.T) {
	for _, tc := range []struct {
		allow bool
		want  string
	}{{false, "isolated"}, {true, "public"}} {
		gw := &sessionGateway{}
		srv := httptest.NewServer(gw.handler(t))
		client := newNetworkClient(srv, tc.allow)
		if err := execOnce(t, client, "s1"); err != nil {
			t.Fatalf("allow=%v: %v", tc.allow, err)
		}
		srv.Close()
		if len(gw.networks) != 1 || gw.networks[0] != tc.want || gw.profiles[0] != "minimal" {
			t.Fatalf("allow=%v: networks=%v profiles=%v, want %s/minimal", tc.allow, gw.networks, gw.profiles, tc.want)
		}
		if strings.Join(gw.order, ",") != "session,exec" {
			t.Fatalf("session must be provisioned before exec, got %v", gw.order)
		}
	}
}

func TestSessionProvisionedOncePerSessionAndAgainAfterRelease(t *testing.T) {
	gw := &sessionGateway{}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	client := newNetworkClient(srv, true)

	for i := 0; i < 3; i++ {
		if err := execOnce(t, client, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := execOnce(t, client, "s2"); err != nil {
		t.Fatal(err)
	}
	if len(gw.networks) != 2 {
		t.Fatalf("want one provisioning per session, got %v", gw.networks)
	}
	if err := client.Release(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if err := execOnce(t, client, "s1"); err != nil {
		t.Fatal(err)
	}
	if len(gw.networks) != 3 {
		t.Fatalf("release must force re-provisioning, got %v", gw.networks)
	}
}

func TestPublicNetworkFailsClosedWhenSessionSetupFails(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		gw := &sessionGateway{sessionCode: code, sessionBody: "nope"}
		srv := httptest.NewServer(gw.handler(t))
		err := execOnce(t, newNetworkClient(srv, true), "s1")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "network=public") {
			t.Fatalf("status %d: want session setup error, got %v", code, err)
		}
		for _, step := range gw.order {
			if step == "exec" {
				t.Fatalf("status %d: command must not run without the requested network policy", code)
			}
		}
	}
}

func TestIsolatedModeToleratesGatewayWithoutSessionsEndpoint(t *testing.T) {
	gw := &sessionGateway{sessionCode: http.StatusNotFound, sessionBody: "Not Found"}
	srv := httptest.NewServer(gw.handler(t))
	defer srv.Close()
	if err := execOnce(t, newNetworkClient(srv, false), "s1"); err != nil {
		t.Fatalf("legacy gateway must keep working in isolated mode: %v", err)
	}

	gw500 := &sessionGateway{sessionCode: http.StatusInternalServerError, sessionBody: "boom"}
	srv500 := httptest.NewServer(gw500.handler(t))
	defer srv500.Close()
	if err := execOnce(t, newNetworkClient(srv500, false), "s1"); err == nil {
		t.Fatal("isolated mode must not ignore real gateway failures, a stale public worker could be reused")
	}
}
