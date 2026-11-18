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
	// An alias must not bypass a trusted Mock flag in its owning instance, or
	// make that flag affect the same conversation in another instance.
	if err := chathistory.NewStore(m.db, b.ID).Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	m.instances[a.ID].runtime.Engine.SessionManager.GetOrCreate("private:synthetic-user").MarkMock()
	for _, alias := range []string{"private:synthetic-user", "onebot:private:synthetic-user", "ONEBOT:private:synthetic-user", "QQ:private:synthetic-user"} {
		body, err := json.Marshal(map[string]string{"sessionId": alias})
		if err != nil {
			t.Fatal(err)
		}
		denied := rpc(t, m, a.ID, "BotStatusService/GetSessionHistory", string(body), false)
		if denied.Code != http.StatusBadRequest {
			t.Fatalf("Mock alias returned archive: %q %d %s", alias, denied.Code, denied.Body.String())
		}
		allowed := rpc(t, m, b.ID, "BotStatusService/GetSessionHistory", string(body), false)
		if allowed.Code != http.StatusOK {
			t.Fatalf("Mock flag affected another instance: %q %d %s", alias, allowed.Code, allowed.Body.String())
		}
		var result struct {
			Records []struct{ Content string }
		}
		if err := json.Unmarshal(allowed.Body.Bytes(), &result); err != nil || len(result.Records) != 1 || result.Records[0].Content != record.Content {
			t.Fatalf("real alias lost original archive: %q %v %v", alias, result, err)
		}
	}
}
