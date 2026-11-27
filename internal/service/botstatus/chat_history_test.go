package botstatus

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
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
	coldList, err := New(&llm.Engine{ChatHistory: chathistory.NewRecorder(store, nil, nil)}, "test", "").GetSessions(ctx, connect.NewRequest(&v1.GetSessionsRequest{}))
	if err != nil || len(coldList.Msg.Sessions) != 1 || coldList.Msg.Sessions[0].SessionId != memory.CanonicalSessionKey("qq:private:"+record.TargetID) {
		t.Fatalf("session list lost persisted archive without a live session manager: %v %v", coldList, err)
	}
	for _, invalid := range []*v1.GetSessionHistoryRequest{
		{}, {SessionId: "mock:fixture:private:fixture-user"}, {SessionId: "invalid"}, {SessionId: "private:"},
		{SessionId: "discord_group_synthetic-group"}, {SessionId: "onebot_private_synthetic-user"},
		{SessionId: "private:" + strings.Repeat("x", 2048)},
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

func TestSessionHistoryMockAliasesUseArchiveScope(t *testing.T) {
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
	qqPrivate := chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "Synthetic-User:group:opaque"}
	qqGroup := chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "Synthetic-Group:private:opaque"}
	fixturePrivate := chathistory.Scope{Platform: "fixture", MessageType: "private", TargetID: "Synthetic-User:group:opaque"}
	mockGroup := chathistory.Scope{Platform: "mock", MessageType: "group", TargetID: "Synthetic-Group:private:opaque"}
	for _, scope := range []chathistory.Scope{qqPrivate, qqGroup, fixturePrivate, mockGroup} {
		record := chathistory.Record{Scope: scope, ID: scope.Platform + ":" + scope.MessageType,
			Role: "user", Content: "original for " + scope.Platform + ":" + scope.MessageType,
			OccurredAt: time.Now().UTC(), DeliveryStatus: "received"}
		if err := store.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	qqPrivateAliases := []string{
		"private:" + qqPrivate.TargetID,
		"qq:private:" + qqPrivate.TargetID,
		"onebot:private:" + qqPrivate.TargetID,
		"QQ:private:" + qqPrivate.TargetID,
		"ONEBOT:private:" + qqPrivate.TargetID,
		" OnEbOt :private: " + qqPrivate.TargetID + " ",
	}
	qqGroupAliases := []string{
		"group:" + qqGroup.TargetID,
		"qq:group:" + qqGroup.TargetID,
		"onebot:group:" + qqGroup.TargetID,
		"QQ:group:" + qqGroup.TargetID,
		"ONEBOT:group:" + qqGroup.TargetID,
		" oNeBoT :group: " + qqGroup.TargetID + " ",
	}
	cases := []struct {
		name, storedID string
		scope          chathistory.Scope
		aliases        []string
	}{
		{"native private", qqPrivateAliases[0], qqPrivate, qqPrivateAliases},
		{"mixed-case private", qqPrivateAliases[5], qqPrivate, qqPrivateAliases},
		{"native group", qqGroupAliases[0], qqGroup, qqGroupAliases},
		{"uppercase group", qqGroupAliases[3], qqGroup, qqGroupAliases},
		{"custom platform", "FiXtUrE:private: " + fixturePrivate.TargetID, fixturePrivate,
			[]string{"fixture:private:" + fixturePrivate.TargetID, "FIXTURE:private:" + fixturePrivate.TargetID}},
		{"real mock platform", "MOCK:group: " + mockGroup.TargetID, mockGroup,
			[]string{"mock:group:" + mockGroup.TargetID, "MoCk:group:" + mockGroup.TargetID}},
		{"long stored alias", "ONEBOT" + strings.Repeat(" ", 2048) + ":private:" + qqPrivate.TargetID, qqPrivate, qqPrivateAliases},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := llm.NewSessionManager()
			marked := manager.GetOrCreate(tc.storedID)
			// A separate real alias must not shadow a Mock with the same archive identity.
			manager.GetOrCreate(memory.CanonicalSessionID(tc.scope.Platform, tc.scope.MessageType, tc.scope.TargetID))
			service := New(&llm.Engine{SessionManager: manager, ChatHistory: chathistory.NewRecorder(store, nil, nil)}, "test", "")
			for _, alias := range tc.aliases {
				response, err := service.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: alias}))
				if err != nil || len(response.Msg.Records) != 1 || response.Msg.Records[0].Content != "original for "+tc.scope.Platform+":"+tc.scope.MessageType {
					t.Fatalf("real alias cannot read its own archive: %q %v %v", alias, response, err)
				}
			}
			marked.MarkMock()
			for _, alias := range tc.aliases {
				if _, err := service.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: alias})); connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Errorf("alias bypassed trusted Mock flag: %q %v", alias, err)
				}
			}
			// Only the exact canonical archive scope is denied. Platform, kind,
			// opaque target casing and target contents remain separate identities.
			unrelated := []chathistory.Scope{
				{Platform: "other-platform", MessageType: tc.scope.MessageType, TargetID: tc.scope.TargetID},
				{Platform: tc.scope.Platform, MessageType: "group", TargetID: tc.scope.TargetID},
				{Platform: tc.scope.Platform, MessageType: "private", TargetID: tc.scope.TargetID},
				{Platform: tc.scope.Platform, MessageType: tc.scope.MessageType, TargetID: strings.ToLower(tc.scope.TargetID)},
				{Platform: tc.scope.Platform, MessageType: tc.scope.MessageType, TargetID: "another-target"},
			}
			for _, scope := range unrelated {
				if scope == tc.scope {
					continue
				}
				id := memory.CanonicalSessionID(scope.Platform, scope.MessageType, scope.TargetID)
				if _, err := service.GetSessionHistory(ctx, connect.NewRequest(&v1.GetSessionHistoryRequest{SessionId: id})); err != nil {
					t.Errorf("Mock flag blocked a different archive scope: %q %v", id, err)
				}
			}
		})
	}
}
