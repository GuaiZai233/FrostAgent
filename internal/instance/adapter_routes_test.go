package instance

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestInstanceExposesOneBotAndAstrBotTogether(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "both-adapters")
	item := m.instances[info.ID]
	if err := item.config.Update("ENABLE_ONEBOT_ADAPTER", "false", false); err != nil {
		t.Fatal(err)
	}
	if err := item.config.Update("ENABLE_ASTRBOT_ADAPTER", "false", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(m)
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http") + "/instances/" + info.ID + "/ws/"

	for _, adapter := range []string{"onebot", "astrbot"} {
		conn, resp, err := websocket.DefaultDialer.Dial(base+adapter, nil)
		if err != nil {
			if resp != nil {
				t.Fatalf("%s websocket handshake failed: status=%d err=%v", adapter, resp.StatusCode, err)
			}
			t.Fatalf("%s websocket handshake failed: %v", adapter, err)
		}
		defer conn.Close()
	}
}
