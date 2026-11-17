package botstatus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Shared with the frontend test so timestamps come from the real RPC projection.
func TestPrivateMessageTimeWireFixture(t *testing.T) {
	manager := llm.NewSessionManager()
	session := manager.GetOrCreate("private:fixture-timed-context")
	stamp := time.Date(2026, 11, 17, 20, 34, 34, 0, time.FixedZone("fixture", 8*60*60))
	session.ReplaceMessages([]llm.ChatMessage{
		{Role: "user", Content: "User Message: fixture input\n\n<system_context>\n{\"message_id\":\"fixture-message\",\"user_id\":\"fixture-user\"}\n</system_context>", RecordedAt: stamp},
		{Role: "assistant", Content: "fixture reply", RecordedAt: stamp.Add(6 * time.Second)},
	})
	response, err := New(&llm.Engine{SessionManager: manager}, "test", "").GetSessionContext(context.Background(),
		connect.NewRequest(&v1.GetSessionContextRequest{SessionId: session.ID()}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "..", "apps", "web", "tests", "fixtures", "private-timed-context.json")
	if os.Getenv("FROSTAGENT_UPDATE_TIME_CONTEXT_FIXTURE") == "1" {
		data, err := (protojson.MarshalOptions{Multiline: true, Indent: "  "}).Marshal(response.Msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture v1.GetSessionContextResponse
	if err := protojson.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(response.Msg, &fixture) {
		t.Fatal("private timestamp fixture differs from the RPC response")
	}
}

func TestPrivateSessionInspection(t *testing.T) {
	for _, id := range []string{"private:test_native_user", "qq:private:test_alias_user", "fixture:private:test_bridge_user", "telegram:private:test_telegram_user", "telegram:private:test_user:group:label"} {
		t.Run(id, func(t *testing.T) {
			manager := llm.NewSessionManager()
			svc := New(&llm.Engine{SessionManager: manager}, "test", "")
			session := manager.GetOrCreate(id)
			session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "private input"})
			session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "private reply"})
			session.SetLastPromptTrace("test persona", "test-model")
			other := manager.GetOrCreate("private:test_other_user")
			other.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "other user's secret"})

			getContext := func(limit int32) *v1.GetSessionContextResponse {
				t.Helper()
				response, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{
					SessionId: id, RecentLimit: limit,
				}))
				if err != nil {
					t.Fatal(err)
				}
				return response.Msg
			}
			before := getContext(50)
			if len(before.RecentStructuredMessages) != 2 || before.RecentStructuredMessages[1].Role != "assistant" || len(before.History) != 2 {
				t.Fatalf("private messages not exposed: %+v", before)
			}
			for _, message := range before.RecentStructuredMessages {
				if message.Time == "" {
					t.Fatalf("private message timestamp lost in RPC: %+v", message)
				}
			}
			snapshot, err := session.SnapshotPrivateCompact()
			if err != nil {
				t.Fatal(err)
			}
			session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "new input"})
			if !session.CommitPrivateCompact(snapshot, "private compact summary") {
				t.Fatal("compact failed")
			}
			after := getContext(1)
			if after.RunningSummary != "private compact summary" || len(after.SummaryGroups) != 1 || len(after.SummaryGroups[0].StructuredMessages) != 2 {
				t.Fatalf("compact metadata missing: %+v", after)
			}
			if len(after.RecentStructuredMessages) != 1 || after.RecentStructuredMessages[0].Content != "new input" {
				t.Fatalf("unexpected recent messages: %+v", after)
			}
			if after.RecentStructuredMessages[0].Time == "" ||
				after.SummaryGroups[0].StructuredMessages[0].Time != before.RecentStructuredMessages[0].Time ||
				after.SummaryGroups[0].StructuredMessages[1].Time != before.RecentStructuredMessages[1].Time {
				t.Fatal("private compact or recent RPC projection lost timestamps")
			}
			if after.SystemPrompt != "test persona" || after.Model != "test-model" {
				t.Fatalf("prompt trace missing: %+v", after)
			}
			var preview struct {
				Model    string `json:"model"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal([]byte(after.PromptText), &preview); err != nil {
				t.Fatal(err)
			}
			if preview.Model != "test-model" || len(preview.Messages) != 4 || preview.Messages[0].Role != "system" || preview.Messages[1].Content != "[先前对话总结] private compact summary" {
				t.Fatalf("preview does not reflect private history: %+v", preview)
			}
			if strings.Contains(after.PromptText, "group_running_summary") || strings.Contains(after.PromptText, "other user's secret") {
				t.Fatalf("group or other-session context leaked: %s", after.PromptText)
			}
			list, err := svc.GetSessions(context.Background(), connect.NewRequest(&v1.GetSessionsRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, info := range list.Msg.Sessions {
				if info.SessionId == session.ID() {
					found = true
					if info.GroupSummary != "private compact summary" {
						t.Fatalf("private list summary missing: %+v", info)
					}
				}
			}
			if !found {
				t.Fatal("private session missing from list")
			}
			deleted, err := svc.DeleteGroupSummary(context.Background(), connect.NewRequest(&v1.DeleteGroupSummaryRequest{SessionId: id}))
			if err != nil || deleted.Msg.Success {
				t.Fatalf("private inspection must not enable group-only mutations: %+v, %v", deleted, err)
			}
			if err := session.ResetSession(nil); err != nil {
				t.Fatal(err)
			}
			reset := getContext(50)
			if reset.RunningSummary != "" || len(reset.SummaryGroups) != 0 || len(reset.RecentStructuredMessages) != 0 {
				t.Fatalf("reset left stale inspection data: %+v", reset)
			}
		})
	}
}

func TestSessionInspectionKindWithOpaqueID(t *testing.T) {
	for _, tt := range []struct {
		id      string
		private bool
		group   bool
	}{
		{"private:test_user:group:label", true, false},
		{"group:test_room:private:label", false, true},
		{"telegram:private:test_user:group:label", true, false},
		{"telegram:group:test_room:private:label", false, true},
		{"mock:test_connection:fixture:private:test_user:group:label", true, false},
		{"mock:test_connection:fixture:group:test_room:private:label", false, true},
		{"FIXTURE:PRIVATE:test_user", true, false},
		{"unknown:test_session", false, false},
	} {
		t.Run(tt.id, func(t *testing.T) {
			if isPrivateSession(tt.id) != tt.private || isGroupSession(tt.id) != tt.group {
				t.Fatalf("wrong session kind for %q", tt.id)
			}
		})
	}
}

func TestGroupInspectionWithPrivateMarkerInID(t *testing.T) {
	manager := llm.NewSessionManager()
	svc := New(&llm.Engine{SessionManager: manager}, "test", "")
	id := "telegram:group:test_room:private:label"
	session := manager.GetOrCreate(id)
	session.AppendGroupCompactMessage(llm.GroupCompactMessage{Role: "user", Content: "group input"}, 20)
	snapshot, ok := session.SnapshotGroupCompact(1)
	if !ok || !session.CommitGroupCompact(snapshot, "group summary") {
		t.Fatal("group compact failed")
	}
	response, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: id}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.RunningSummary != "group summary" || len(response.Msg.SummaryGroups) != 1 || !strings.Contains(response.Msg.PromptText, "<group_running_summary>") {
		t.Fatalf("group ID was misclassified as private: %+v", response.Msg)
	}
}
