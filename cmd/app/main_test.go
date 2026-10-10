package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListenerSwitchPreservesCurrentHTTPResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	oldAddress := listener.Addr().String()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	newAddress := probe.Addr().String()
	probe.Close()
	listeners := newListenerSet()
	defer listeners.Close(context.Background())
	listeners.active[oldAddress] = listeners.start(oldAddress, listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		err := listeners.Apply(map[string]http.Handler{
			newAddress: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("switch completed"))
	}))
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://" + oldAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "switch completed" {
		t.Fatalf("switch terminated the active response: %d %q, %v", response.StatusCode, body, err)
	}
	response, err = client.Get("http://" + newAddress)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("new listener status = %d", response.StatusCode)
	}
}

func TestAdapterListenerDoesNotExposeManagement(t *testing.T) {
	handler := instanceWebSocketHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/instances/a1b2c3d4/ws/onebot", 204},
		{"/instances/a1b2c3d4/ws/astrbot", 204},
		{"/instances/a1b2c3d4/frostagent.v1.SettingsService/GetEnvVars", 404},
		{"/instances/a1b2c3d4/api/sticker/example/image", 404},
		{"/api/instances", 404},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", test.path, nil))
		if w.Code != test.status {
			t.Errorf("%s: got %d, want %d", test.path, w.Code, test.status)
		}
	}
}

func TestManagementMux_ActionsCatRouting(t *testing.T) {
	managerCalled := false
	var capturedPath string
	manager := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		managerCalled = true
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"manager":true}`))
	})

	mux := managementMux(manager)

	paths := []string{
		"/api/actionscat/status",
		"/api/v1/actionscat/actions",
		"/api/actionscat/actions/act_1/runs",
	}

	for _, p := range paths {
		managerCalled = false
		capturedPath = ""
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if !managerCalled {
			t.Fatalf("expected path %q to route to manager, but manager was not called", p)
		}
		if capturedPath != p {
			t.Fatalf("expected captured path %q, got %q", p, capturedPath)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	}
}
