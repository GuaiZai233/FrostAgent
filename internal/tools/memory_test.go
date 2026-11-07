package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestMemoryToolSearchIncrementsAccessCount(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := memory.NewStore(storePath)
	reader := memory.NewReader(store, 5)
	gateway := memory.NewGateway()
	writer := memory.NewWriter(store)

	engine := &llm.Engine{
		MemoryReader:  reader,
		MemoryWriter:  writer,
		MemoryGateway: gateway,
	}

	tool := NewMemoryTool(engine)

	// Write a memory first
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{
		Owner:     "alice",
		OwnerType: memory.OwnerUser,
	})

	writeArgs := `{"action":"write","content":"Alice loves apples","tags":["apple","fruit"]}`
	writeRes, err := tool.ExecuteContext(ctx, writeArgs)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if writeRes != "记忆已写入" {
		t.Fatalf("unexpected write response: %s", writeRes)
	}

	// Search memory
	searchArgs := `{"action":"search","tags":["apple"]}`
	searchRes, err := tool.ExecuteContext(ctx, searchArgs)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	var results []memory.MemoryEntry
	if err := json.Unmarshal([]byte(searchRes), &results); err != nil {
		t.Fatalf("failed to unmarshal search response: %v, raw: %s", err, searchRes)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(results))
	}
	if results[0].AccessCount != 1 {
		t.Errorf("expected returned memory AccessCount = 1, got %d", results[0].AccessCount)
	}

	// Check store has updated access count
	stored, err := store.ListByOwner("alice")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}
	if len(stored) != 1 || stored[0].AccessCount != 1 {
		t.Errorf("expected stored memory AccessCount = 1, got %v", stored)
	}

	// Search again
	searchRes2, err := tool.ExecuteContext(ctx, searchArgs)
	if err != nil {
		t.Fatalf("search 2 failed: %v", err)
	}
	var results2 []memory.MemoryEntry
	if err := json.Unmarshal([]byte(searchRes2), &results2); err != nil {
		t.Fatalf("failed to unmarshal search 2 response: %v", err)
	}
	if len(results2) != 1 || results2[0].AccessCount != 2 {
		t.Errorf("expected returned memory AccessCount = 2 on second search, got %v", results2)
	}
}

func TestMemoryToolSetMemberNameCurrentGroupOnly(t *testing.T) {
	manager := memory.NewGroupManager(t.TempDir(), nil)
	group, err := manager.GetGroupStoreForPlatform("qq", "group-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := group.ObserveMember("user-one", "Nickname", "CardOnly", "member", "test"); err != nil {
		t.Fatal(err)
	}
	other, err := manager.GetGroupStoreForPlatform("qq", "group-two")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ObserveMember("user-one", "OtherNickname", "", "member", "test"); err != nil {
		t.Fatal(err)
	}
	tool := NewMemoryTool(&llm.Engine{GroupManager: manager})
	groupCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		Owner: "group:group-one", OwnerType: memory.OwnerGroup, ActorUserID: "user-one",
		RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "group-one"},
	})
	result, err := tool.ExecuteContext(groupCtx, `{"action":"set_member_name","preferred_name":"小狐"}`)
	if err != nil || result != "成员优先称呼已更新" {
		t.Fatalf("set current member name: %q, %v", result, err)
	}
	profile, err := group.GetProfile()
	if err != nil || profile.Members["user-one"].PreferredName != "小狐" || profile.Members["user-one"].Card != "CardOnly" {
		t.Fatalf("unexpected current group profile: %#v, %v", profile, err)
	}
	otherProfile, err := other.GetProfile()
	if err != nil || otherProfile.Members["user-one"].PreferredName != "" {
		t.Fatalf("changed another group: %#v, %v", otherProfile, err)
	}
	result, err = tool.ExecuteContext(groupCtx, `{"action":"set_member_name","user_id":"unobserved","preferred_name":"伪造"}`)
	if err != nil || result != "当前群尚未观察到该成员，无法设置称呼" {
		t.Fatalf("unobserved member should be rejected: %q, %v", result, err)
	}
	result, err = tool.ExecuteContext(groupCtx, `{"action":"set_member_name","preferred_name":""}`)
	if err != nil || result != "成员优先称呼已清除" {
		t.Fatalf("clear current member name: %q, %v", result, err)
	}
	profile, err = group.GetProfile()
	if err != nil || profile.Members["user-one"].PreferredName != "" || profile.Members["user-one"].ResolveCallingName() != "Nickname" {
		t.Fatalf("cleared name did not fall back to nickname: %#v, %v", profile, err)
	}
	privateCtx := llm.WithRunContext(context.Background(), llm.RunContext{Owner: "user-one", OwnerType: memory.OwnerUser, ActorUserID: "user-one"})
	result, err = tool.ExecuteContext(privateCtx, `{"action":"set_member_name","preferred_name":"Private"}`)
	if err != nil || result != "只能在群聊中设置成员称呼" {
		t.Fatalf("private call should be rejected: %q, %v", result, err)
	}
	mockCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		Owner: "group:group-one", OwnerType: memory.OwnerGroup, ActorUserID: "user-one", Mock: true,
		RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "group-one"},
	})
	result, err = tool.ExecuteContext(mockCtx, `{"action":"set_member_name","preferred_name":"Mock"}`)
	if err != nil || result != "模拟会话模式下不能持久化成员称呼" {
		t.Fatalf("mock call should be rejected: %q, %v", result, err)
	}
}

func TestMemoryToolMockWriteDoesNotPersist(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := memory.NewStore(storePath)
	reader := memory.NewReader(store, 5)
	gateway := memory.NewGateway()
	writer := memory.NewWriter(store)

	engine := &llm.Engine{
		MemoryReader:  reader,
		MemoryWriter:  writer,
		MemoryGateway: gateway,
	}

	tool := NewMemoryTool(engine)

	ctx := llm.WithRunContext(context.Background(), llm.RunContext{
		Owner:     "mock_test_user",
		OwnerType: memory.OwnerUser,
		Mock:      true,
	})

	writeArgs := `{"action":"write","content":"Secret ephemeral message","tags":["ephemeral"]}`
	writeRes, err := tool.ExecuteContext(ctx, writeArgs)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	expectedMsg := "记忆已记录（模拟会话：断电即丢，不持久化保存）"
	if writeRes != expectedMsg {
		t.Fatalf("expected response %q, got %q", expectedMsg, writeRes)
	}

	// Verify nothing was written to store
	stored, err := store.ListByOwner("mock_test_user")
	if err != nil {
		t.Fatalf("ListByOwner failed: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("expected 0 stored memories for mock session, got %d", len(stored))
	}
}

func TestMemoryToolProactiveAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := memory.NewStore(storePath)
	reader := memory.NewReader(store, 5)
	gateway := memory.NewGateway()
	writer := memory.NewWriter(store)

	engine := &llm.Engine{
		MemoryReader:  reader,
		MemoryWriter:  writer,
		MemoryGateway: gateway,
	}

	tool := NewMemoryTool(engine)

	proactiveCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		Owner:     "proactive_user",
		OwnerType: memory.OwnerUser,
		Proactive: true,
	})

	// 1. write in proactive turn is permitted per maintainer clarification
	writeRes, err := tool.ExecuteContext(proactiveCtx, `{"action":"write","content":"proactive note","tags":["test"]}`)
	if err != nil {
		t.Fatalf("write unexpected error: %v", err)
	}
	if writeRes != "记忆已写入" {
		t.Fatalf("expected write success, got: %s", writeRes)
	}
	stored, err := store.ListByOwner("proactive_user")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected 1 memory stored in proactive turn, got %d", len(stored))
	}

	// 2. Search in proactive turn should be allowed and increment AccessCount
	searchRes, err := tool.ExecuteContext(proactiveCtx, `{"action":"search","tags":["test"]}`)
	if err != nil {
		t.Fatalf("search in proactive turn failed: %v", err)
	}
	var searchResults []memory.MemoryEntry
	if err := json.Unmarshal([]byte(searchRes), &searchResults); err != nil {
		t.Fatalf("failed to unmarshal search response: %v", err)
	}
	if len(searchResults) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(searchResults))
	}
	// Check AccessCount is incremented to 1 in store
	storedAfter, _ := store.ListByOwner("proactive_user")
	if len(storedAfter) != 1 || storedAfter[0].AccessCount != 1 {
		t.Fatalf("expected AccessCount = 1 for proactive search, got %d", storedAfter[0].AccessCount)
	}
}
