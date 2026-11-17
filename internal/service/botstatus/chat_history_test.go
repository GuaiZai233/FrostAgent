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
		{SessionId: "discord_group_synthetic-group"}, {SessionId: "onebot_private_synthetic-user"},
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

func TestSessionHistoryMockIdentityUsesTrustedFlag(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: "fixture-instance", Name: "Fixture", CreatedAt: time.Now()}, 1); err != nil {
		t.Fatal(err)
	}
	store := chathistory.NewStore(db, "fixture-instance")
	manager := llm.NewSessionManager()
	service := New(&llm.Engine{SessionManager: manager, ChatHistory: chathistory.NewRecorder(store, nil, nil)}, "test", "")
	cases := []struct {
		sessionID string
		scope     chathistory.Scope
		mock      bool
	}{
		{"mock:private:synthetic-user", chathistory.Scope{Platform: "mock", MessageType: "private", TargetID: "synthetic-user"}, false},
		{"mock:group:synthetic-group", chathistory.Scope{Platform: "mock", MessageType: "group", TargetID: "synthetic-group"}, false},
		{"private:synthetic-marked-user", chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "synthetic-marked-user"}, true},
		{"mock:private:synthetic-marked-prefix", chathistory.Scope{Platform: "mock", MessageType: "private", TargetID: "synthetic-marked-prefix"}, true},
	}
	for _, tc := range cases {
		record := chathistory.Record{Scope: tc.scope, ID: tc.sessionID, Content: "original for " + tc.sessionID,
			Role: "user", OccurredAt: time.Now().UTC(), DeliveryStatus: "received"}
		if err := store.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
		session := manager.GetOrCreate(tc.sessionID)
		if tc.mock {
			session.MarkMock()
		}
		response, err := service.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: tc.sessionID}))
		if tc.mock {
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("trusted Mock session returned archive: %s %v", tc.sessionID, err)
			}
		} else if err != nil || len(response.Msg.Records) != 1 || response.Msg.Records[0].Content != record.Content {
			t.Fatalf("real mock-prefix session lost its archive: %s %v %v", tc.sessionID, response, err)
		}
	}
	list, err := service.GetSessions(ctx, connect.NewRequest(&v1.GetSessionsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, session := range list.Msg.Sessions {
		seen[session.SessionId] = true
	}
	for _, tc := range cases {
		if seen[tc.sessionID] == tc.mock {
			t.Fatalf("session list disagrees with trusted Mock identity: %s", tc.sessionID)
		}
	}
	// Archive reads after restart must also accept a real platform named mock.
	coldService := New(&llm.Engine{ChatHistory: chathistory.NewRecorder(store, nil, nil)}, "test", "")
	response, err := coldService.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: cases[0].sessionID}))
	if err != nil || len(response.Msg.Records) != 1 {
		t.Fatalf("real mock-prefix archive requires a live session: %v %v", response, err)
	}
}
