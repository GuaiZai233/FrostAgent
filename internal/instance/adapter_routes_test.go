package instance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/gorilla/websocket"
)

func TestInstanceOnlyExposesOneBot(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "synthetic-adapter-removal")
	item := m.instances[info.ID]
	for key, value := range map[string]string{
		"ENABLE_ONEBOT_ADAPTER": "false",
		"ASTRBOT_WS_MODE":       "invalid", "ASTRBOT_WS_ENABLED": "invalid",
		"ASTRBOT_WS_URL": "invalid", "ASTRBOT_WS_TOKEN": "synthetic-retired-token",
	} {
		if err := item.config.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m)
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http") + "/instances/" + info.ID + "/ws/"
	conn, _, err := websocket.DefaultDialer.Dial(base+"onebot", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	for _, suffix := range []string{"astrbot", "astrbot?mock=true"} {
		conn, response, err := websocket.DefaultDialer.Dial(base+suffix, nil)
		if err == nil {
			conn.Close()
			t.Fatal("removed adapter accepted a connection")
		}
		if response == nil || response.StatusCode != http.StatusNotFound {
			t.Fatalf("removed adapter status: %v", response)
		}
		response.Body.Close()
	}
	for _, target := range []llm.AwarenessTarget{
		{AdapterID: "astrbot", Platform: "qq"},
		{AdapterID: "aiocqhttp", Platform: "qq"},
		{Platform: "aiocqhttp"},
	} {
		if sender, ready := item.runtime.Engine.Awareness.Resolve(target); sender != nil || ready {
			t.Fatal("removed adapter has an awareness route")
		}
	}
}
