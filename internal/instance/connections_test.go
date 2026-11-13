package instance

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/gorilla/websocket"
)

func TestSQLConnectionModesFollowInstanceLifecycle(t *testing.T) {
	for _, adapter := range []string{"ONEBOT"} {
		t.Run(adapter, func(t *testing.T) {
			connected, disconnected := make(chan string, 16), make(chan struct{}, 16)
			var dials atomic.Int32
			upgrader := websocket.Upgrader{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				dials.Add(1)
				connected <- r.Header.Get("Authorization")
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						break
					}
				}
				disconnected <- struct{}{}
			}))
			defer upstream.Close()
			root := t.TempDir()
			m, err := NewDatabase(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if m != nil {
					m.Close()
				}
			}()
			info, err := m.Create("Connection Lifecycle")
			if err != nil {
				t.Fatal(err)
			}
			other, err := m.Create("Unrelated Instance")
			if err != nil {
				t.Fatal(err)
			}
			upstreamURL := "ws" + strings.TrimPrefix(upstream.URL, "http")
			mode, url, token, enabled := adapter+"_WS_MODE", adapter+"_WS_URL", adapter+"_WS_TOKEN", adapter+"_WS_ENABLED"
			assertAdapterDisabled := func() {
				t.Helper()
				if m.instances[info.ID].runtime.Scope.Context().Err() != nil {
					t.Fatal("adapter flag disabled the whole instance")
				}
				if m.instances[other.ID].config.Get(enabled) != "" || m.global.Get(enabled) != "" {
					t.Fatal("adapter enable flag leaked across instances")
				}
				local := httptest.NewServer(m)
				defer local.Close()
				endpoint := "ws" + strings.TrimPrefix(local.URL, "http") + "/instances/" + info.ID + "/ws/" + strings.ToLower(adapter)
				conn, response, err := websocket.DefaultDialer.Dial(endpoint, nil)
				if err == nil {
					conn.Close()
					t.Fatal("disabled adapter accepted a real upstream")
				}
				if response == nil || response.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("expected disabled adapter status, got %v", response)
				}
				response.Body.Close()
				conn, _, err = websocket.DefaultDialer.Dial(endpoint+"?mock=true", nil)
				if err != nil {
					t.Fatalf("disabled adapter blocked direct chat: %v", err)
				}
				conn.Close()

			}
			draft := func(updates map[string]string) string {
				t.Helper()
				entries := make([]map[string]string, 0, len(updates))
				for key, value := range updates {
					entries = append(entries, map[string]string{"key": key, "value": value})
				}
				body, err := json.Marshal(map[string]any{"updates": entries})
				if err != nil {
					t.Fatal(err)
				}
				return string(body)
			}
			response := applySettingsDraft(m, info.ID, draft(map[string]string{mode: "forward", url: upstreamURL, token: "synthetic-instance-token"}))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("save failed: %d %s", response.Code, response.Body.String())
			}
			if m.instances[info.ID].runtime.Scope.Context().Err() == nil {
				t.Fatal("saving settings activated a disabled instance")
			}
			if dials.Load() != 0 || m.instances[other.ID].config.Get(mode) != "" || m.global.Get(mode) != "" {
				t.Fatal("disabled configuration dialed or leaked across instances")
			}
			awaitConnect := func() {
				t.Helper()
				select {
				case got := <-connected:
					if got != "Bearer synthetic-instance-token" {
						t.Fatal("outbound token missing or incorrect")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("forward connection did not start")
				}
			}
			awaitDisconnect := func() {
				t.Helper()
				select {
				case <-disconnected:
				case <-time.After(5 * time.Second):
					t.Fatal("old connection survived runtime stop")
				}
			}
			if err := m.Enable(info.ID, true); err != nil {
				t.Fatal(err)
			}
			awaitConnect()
			active := m.instances[info.ID].runtime
			active.StartBackground()
			if dials.Load() != 1 {
				t.Fatal("background workers started more than once")
			}
			response = applySettingsDraft(m, info.ID, draft(map[string]string{url: "https://invalid.example/", "BOT_NAME": "must-not-save"}))
			if strings.Contains(response.Body.String(), `"success":true`) || m.instances[info.ID].runtime != active || active.Scope.Context().Err() != nil {
				t.Fatalf("invalid draft disturbed the active connection: %s", response.Body.String())
			}
			if m.instances[info.ID].config.Get("BOT_NAME") == "must-not-save" {
				t.Fatal("invalid connection draft partially persisted")
			}
			if err := m.Enable(info.ID, false); err != nil {
				t.Fatal(err)
			}
			awaitDisconnect()
			if err := m.Enable(info.ID, true); err != nil {
				t.Fatal(err)
			}
			awaitConnect()
			response = applySettingsDraft(m, info.ID, draft(map[string]string{mode: "reverse"}))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("switch to reverse failed: %s", response.Body.String())
			}
			awaitDisconnect()
			if dials.Load() != 2 {
				t.Fatal("unexpected outbound connection while switching mode")
			}
			local := httptest.NewServer(m)
			conn, responseHTTP, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(local.URL, "http")+"/instances/"+info.ID+"/ws/"+strings.ToLower(adapter), nil)
			if err != nil {
				local.Close()
				t.Fatalf("reverse endpoint no longer available: %v (%v)", err, responseHTTP)
			}
			response = applySettingsDraft(m, info.ID, draft(map[string]string{enabled: "false"}))
			if !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("disable reverse adapter failed: %s", response.Body.String())
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, _, err := conn.ReadMessage(); err == nil {
				t.Fatal("disabled adapter left the reverse socket open")
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				t.Fatal("timed out waiting for the disabled reverse socket to close")
			}
			conn.Close()
			local.Close()
			assertAdapterDisabled()
			response = applySettingsDraft(m, info.ID, draft(map[string]string{enabled: "true", mode: "forward"}))
			if !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("switch back to forward failed: %s", response.Body.String())
			}
			awaitConnect()
			m.Close()
			awaitDisconnect()
			m, err = NewDatabase(root)
			if err != nil {
				t.Fatal(err)
			}
			awaitConnect()
			stored, err := instanceconfig.OpenDatabase(m.db, info.ID, false)
			if err != nil || stored.Get(mode) != "forward" || stored.Get(url) != upstreamURL {
				t.Fatalf("connection configuration lost after restart: %v", err)
			}
			response = applySettingsDraft(m, info.ID, draft(map[string]string{enabled: "false"}))
			if !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("disable forward adapter failed: %s", response.Body.String())
			}
			awaitDisconnect()
			assertAdapterDisabled()
			beforeRestart := dials.Load()
			m.Close()
			m, err = NewDatabase(root)
			if err != nil {
				t.Fatal(err)
			}
			assertAdapterDisabled()
			stored, err = instanceconfig.OpenDatabase(m.db, info.ID, false)
			if err != nil || stored.Get(enabled) != "false" || stored.Get(mode) != "forward" || stored.Get(url) != upstreamURL || stored.Get(token) != "synthetic-instance-token" {
				t.Fatal("disabling lost its flag or retained configuration")
			}
			select {
			case <-connected:
				t.Fatal("disabled adapter reconnected after process restart")
			case <-time.After(150 * time.Millisecond):
			}
			if dials.Load() != beforeRestart {
				t.Fatal("disabled adapter dialed after process restart")
			}
			response = applySettingsDraft(m, info.ID, draft(map[string]string{enabled: "true"}))
			if !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("re-enable forward adapter failed: %s", response.Body.String())
			}
			awaitConnect()
			if err := m.Enable(info.ID, false); err != nil {
				t.Fatal(err)
			}
			awaitDisconnect()
			select {
			case <-connected:
				t.Fatal("stopped runtime reconnected")
			case <-time.After(150 * time.Millisecond):
			}
		})
	}
}

func TestOneBotDisabledKeepsDirectChat(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("OneBot Disabled")
	if err != nil {
		t.Fatal(err)
	}
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"ONEBOT_WS_ENABLED","value":"false"}]}`)
	if !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatal(response.Body.String())
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m)
	defer server.Close()
	for _, adapter := range []string{"onebot"} {
		endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/instances/" + info.ID + "/ws/" + adapter
		conn, response, err := websocket.DefaultDialer.Dial(endpoint, nil)
		if err == nil {
			conn.Close()
			t.Fatalf("disabled %s admitted a real upstream", adapter)
		}
		if response == nil || response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("unexpected disabled %s response: %v", adapter, response)
		}
		response.Body.Close()
		conn, _, err = websocket.DefaultDialer.Dial(endpoint+"?mock=true", nil)
		if err != nil {
			t.Fatalf("disabled %s blocked direct chat: %v", adapter, err)
		}
		conn.Close()
	}
}

func TestConnectionClientDoesNotStartBeforeRuntimePublication(t *testing.T) {
	var dials atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		http.Error(w, "candidate must not dial", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Unpublished Candidate")
	if err != nil {
		t.Fatal(err)
	}
	item := m.instances[info.ID]
	for key, value := range map[string]string{"ONEBOT_WS_MODE": "forward", "ONEBOT_WS_URL": "ws" + strings.TrimPrefix(upstream.URL, "http")} {
		if err := item.config.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	candidate, _, err := m.buildFresh(info.ID, item, true, m.dir(info.ID))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if dials.Load() != 0 {
		t.Fatal("unpublished runtime initiated an outbound connection")
	}
	candidate.Stop()
	if candidate.Scope.Context().Err() != context.Canceled {
		t.Fatal("unpublished candidate did not stop")
	}
	// Starting a cancelled candidate is a no-op, including after repeated calls.
	candidate.StartBackground()
	candidate.StartBackground()
}
