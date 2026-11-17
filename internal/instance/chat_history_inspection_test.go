package instance

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
)

func TestChatHistoryInspectionRPCRoutesToSelectedInstance(t *testing.T) {
	m := testManager(t)
	a, b := create(t, m, "history-fixture-a"), create(t, m, "history-fixture-b")
	record := chathistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "synthetic-user"},
		ID: "synthetic-record", Role: "user", Content: "committed original", OccurredAt: time.Now().UTC(), DeliveryStatus: "received"}
	if err := chathistory.NewStore(m.db, a.ID).Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	for _, instance := range []Info{a, b} {
		response := rpc(t, m, instance.ID, "BotStatusService/GetSessionHistory", `{"sessionId":"private:synthetic-user"}`, false)
		if response.Code != http.StatusOK {
			t.Fatalf("history RPC: %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Records []struct{ Content string }
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if instance.ID == a.ID {
			if len(result.Records) != 1 || result.Records[0].Content != record.Content {
				t.Fatal("owning instance did not return committed archive")
			}
		} else if len(result.Records) != 0 {
			t.Fatal("archive escaped its owning instance")
		}
	}
}
