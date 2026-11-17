package botstatus

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestSessionHistoryInspection(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"fixture-a", "fixture-b"} {
		if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 2); err != nil {
			t.Fatal(err)
		}
	}
	store := chathistory.NewStore(db, "fixture-a")
	stamp := time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC)
	record := chathistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-user:group:opaque"}, ID: "fixture-record", MessageID: "fixture-message",
		SenderID: "fixture-user", SenderName: "Fixture", Role: "user", Content: "<script>original</script>\nsecond line", OccurredAt: stamp, DeliveryStatus: "received", ReplyTo: "fixture-source"}
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	manager := llm.NewSessionManager()
	// No live session is needed: inspection reads the committed archive.
	service := New(&llm.Engine{SessionManager: manager, ChatHistory: chathistory.NewRecorder(store, nil, nil)}, "test", "")
	for _, prefix := range []string{"private:", "qq:private:", "onebot:private:"} {
		response, err := service.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: prefix + record.TargetID, After: "2026-01-02T00:00:00+08:00", Before: "2026-01-03T00:00:00+08:00"}))
		if err != nil || len(response.Msg.Records) != 1 {
			t.Fatalf("alias history unavailable: %v %v", response, err)
		}
		got := response.Msg.Records[0]
		if got.Content != record.Content || got.SenderId != record.SenderID || got.SenderName != record.SenderName || got.MessageId != record.MessageID || got.ReplyTo != record.ReplyTo || got.OccurredAt != stamp.Format(time.RFC3339Nano) || got.DeliveryStatus != record.DeliveryStatus {
			t.Fatalf("archive fields changed: %v", got)
		}
	}
	for _, id := range []string{"private:other", "group:" + record.TargetID, "fixture:private:" + record.TargetID} {
		response, err := service.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: id}))
		if err != nil || len(response.Msg.Records) != 0 {
			t.Fatal("history crossed conversation scope", response, err)
		}
	}
	other := New(&llm.Engine{ChatHistory: chathistory.NewRecorder(chathistory.NewStore(db, "fixture-b"), nil, nil)}, "test", "")
	request := connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: "private:" + record.TargetID})
	response, err := other.GetSessionHistory(ctx, request)
	if err != nil || len(response.Msg.Records) != 0 {
		t.Fatal("history crossed instance scope", response, err)
	}
	for _, invalid := range []*v1.GetSessionHistoryRequest{
		{}, {SessionId: "mock:fixture:private:fixture-user"}, {SessionId: "invalid"}, {SessionId: "private:"},
		{SessionId: "private:fixture-user", After: "2026-01-02"},
		{SessionId: "private:fixture-user", After: "2026-01-03T00:00:00Z", Before: "2026-01-02T00:00:00Z"},
		{SessionId: "private:fixture-user", PageToken: "malformed"}, {SessionId: "private:fixture-user", PageSize: -1},
	} {
		if _, err := service.GetSessionHistory(ctx, connect.NewRequest(invalid)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid input accepted: %v %v", invalid, err)
		}
	}
	manager.GetOrCreate("private:fixture-mock").MarkMock()
	if _, err := service.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: "private:fixture-mock"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("mock inspection accepted", err)
	}
	if _, err := New(&llm.Engine{}, "test", "").GetSessionHistory(ctx, request); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("missing archive not reported", err)
	}
	if _, err := db.SQL.ExecContext(ctx, "DROP TABLE chat_history"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetSessionHistory(ctx, request); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatal("database failure not reported", err)
	}
}
