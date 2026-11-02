package bridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const syntheticToken = "synthetic-bridge-token-for-tests-only-001"

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(ClientConfig{BaseURL: server.URL, Token: syntheticToken,
		HTTPClient: server.Client(), InitialBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond,
		RequestTimeout: time.Second, MaxFailures: 3})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testHandler(t *testing.T, service *Service) http.Handler {
	t.Helper()
	handler, err := NewHandler(service, syntheticToken)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHTTPRejectsUnauthenticatedAndInvalidRequests(t *testing.T) {
	s, _ := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("invalid request reached executor")
		return Outcome{}, nil
	}})
	handler := testHandler(t, s)
	base, _ := json.Marshal(testRequest("operation"))
	for _, fixture := range []struct {
		name, body, token, path string
		tls                     bool
		status                  int
	}{
		{"plaintext", string(base), syntheticToken, "operation", false, 401},
		{"missing-token", string(base), "", "operation", true, 401},
		{"wrong-token", string(base), "wrong-token", "operation", true, 401},
		{"path-mismatch", string(base), syntheticToken, "other-operation", true, 400},
		{"trailing-json", string(base) + "{}", syntheticToken, "operation", true, 400},
		{"unknown-field", strings.TrimSuffix(string(base), "}") + `,"side_effect":false}`, syntheticToken, "operation", true, 400},
		{"oversized", strings.Repeat(" ", MaxRequestBytes) + string(base), syntheticToken, "operation", true, 400},
		{"invalid-version", strings.Replace(string(base), `"version":1`, `"version":2`, 1), syntheticToken, "operation", true, 400},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/v1/operations/"+fixture.path, strings.NewReader(fixture.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+fixture.token)
			if fixture.tls {
				req.TLS = &tls.ConnectionState{}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != fixture.status {
				t.Fatalf("status %d, want %d: %s", response.Code, fixture.status, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "synthetic-secret") || strings.Contains(response.Body.String(), syntheticToken) {
				t.Fatal("HTTP error leaked request data or credentials")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("bridge response can be cached")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientLostResponseRetriesSameOperationOnce(t *testing.T) {
	var calls atomic.Int32
	s, _ := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		calls.Add(1)
		return Outcome{State: Succeeded, Code: "completed"}, nil
	}})
	server := httptest.NewTLSServer(testHandler(t, s))
	defer server.Close()
	c := testClient(t, server)
	baseTransport := c.http.Transport
	var lost atomic.Bool
	var bodiesMu sync.Mutex
	var bodies [][]byte
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			body, _ := r.GetBody()
			data, _ := io.ReadAll(body)
			body.Close()
			bodiesMu.Lock()
			bodies = append(bodies, data)
			bodiesMu.Unlock()
		}
		resp, err := baseTransport.RoundTrip(r)
		if err == nil && lost.CompareAndSwap(false, true) {
			resp.Body.Close()
			return nil, errors.New("synthetic-secret-transport-error")
		}
		return resp, err
	})
	op, err := c.Run(t.Context(), testRequest("lost-response"))
	if err != nil || op.State != Succeeded || calls.Load() != 1 {
		t.Fatalf("lost response retry: %+v %v calls=%d", op, err, calls.Load())
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("retry changed operation body: %d submissions", len(bodies))
	}
}

func TestClientPollsExplicitRunningWithoutNewSubmission(t *testing.T) {
	s, _ := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		time.Sleep(30 * time.Millisecond)
		return Outcome{State: Failed, Code: "execution_failed"}, nil
	}})
	var puts, gets atomic.Int32
	handler := testHandler(t, s)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
		} else {
			gets.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	op, err := testClient(t, server).Run(t.Context(), testRequest("poll"))
	if err != nil || op.State != Failed || puts.Load() != 1 || gets.Load() < 1 {
		t.Fatalf("running/failure semantics: %+v %v puts=%d gets=%d", op, err, puts.Load(), gets.Load())
	}
}

func TestClientUnknownResponseDoesNotReplay(t *testing.T) {
	var calls atomic.Int32
	s, _ := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		calls.Add(1)
		return Outcome{State: Unknown, Code: "outcome_unknown"}, nil
	}})
	server := httptest.NewTLSServer(testHandler(t, s))
	defer server.Close()
	op, err := testClient(t, server).Run(t.Context(), testRequest("unknown"))
	if err != nil || op.State != Unknown || calls.Load() != 1 {
		t.Fatalf("unknown outcome was retried: %+v %v calls=%d", op, err, calls.Load())
	}
}

func TestClientDropsStaleIdentityAndMismatchedParameters(t *testing.T) {
	for _, mutate := range []func(*Operation){
		func(op *Operation) { op.Generation++ },
		func(op *Operation) { op.InstanceID = "other-instance" },
		func(op *Operation) { op.ActorID = "other-owner" },
		func(op *Operation) { op.RequestHash = strings.Repeat("0", 64) },
		func(op *Operation) { op.Code = "synthetic-secret-output" },
	} {
		req := testRequest("stale")
		hash, _ := req.fingerprint()
		now := time.Now().UTC()
		op := Operation{Identity: req.Identity, RequestHash: hash, Outcome: Outcome{State: Succeeded, Code: "completed"}, CreatedAt: now, UpdatedAt: now}
		mutate(&op)
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(op)
		}))
		_, err := testClient(t, server).Run(t.Context(), req)
		server.Close()
		if !errors.Is(err, ErrProtocol) || !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("invalid completion accepted: %v", err)
		}
	}
}

func TestClientFailuresDoNotExposeRemoteText(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "synthetic-secret "+syntheticToken)
	}))
	defer server.Close()
	c := testClient(t, server)
	_, err := c.Run(t.Context(), testRequest("unreachable"))
	if !errors.Is(err, ErrOutcomeUnknown) || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), syntheticToken) {
		t.Fatalf("unsafe communication error: %v", err)
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic-secret " + syntheticToken)
	})
	_, err = c.Run(t.Context(), testRequest("transport"))
	if !errors.Is(err, ErrOutcomeUnknown) || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), syntheticToken) {
		t.Fatalf("unsafe transport error: %v", err)
	}
}

func TestClientNeverFollowsRedirect(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := testClient(t, server).Run(t.Context(), testRequest("redirect"))
	if !errors.Is(err, ErrProtocol) || forwarded.Load() != 0 {
		t.Fatalf("redirect forwarded operation: err=%v forwarded=%d", err, forwarded.Load())
	}
}

func TestClientPollingLostJournalDoesNotResubmit(t *testing.T) {
	req := testRequest("journal-lost")
	hash, _ := req.fingerprint()
	now := time.Now().UTC()
	var puts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			_ = json.NewEncoder(w).Encode(Operation{Identity: req.Identity, RequestHash: hash, Outcome: Outcome{State: Running}, CreatedAt: now, UpdatedAt: now})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	_, err := testClient(t, server).Run(t.Context(), req)
	if !errors.Is(err, ErrOutcomeUnknown) || puts.Load() != 1 {
		t.Fatalf("missing accepted operation was replayed: %v puts=%d", err, puts.Load())
	}
}

func TestClientRejectsUnsafeEndpointAndToken(t *testing.T) {
	for _, endpoint := range []string{"http://localhost", "https://user:secret@localhost", "https://localhost?token=secret", "https://localhost/other", "https://localhost/#fragment"} {
		if _, err := NewClient(ClientConfig{BaseURL: endpoint, Token: syntheticToken}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe endpoint accepted: %s %v", endpoint, err)
		}
	}
	if _, err := NewClient(ClientConfig{BaseURL: "https://localhost", Token: "short"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("weak token accepted: %v", err)
	}
}
