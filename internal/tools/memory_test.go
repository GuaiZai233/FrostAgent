package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/memory"
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

func TestMemoryToolProactiveRestrictions(t *testing.T) {
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

	// 1. write in proactive turn must be blocked
	writeRes, err := tool.ExecuteContext(proactiveCtx, `{"action":"write","content":"proactive note","tags":["test"]}`)
	if err != nil {
		t.Fatalf("write unexpected error: %v", err)
	}
	if writeRes != "主动回复轮次禁止写入记忆" {
		t.Fatalf("expected write rejection, got: %s", writeRes)
	}
	stored, err := store.ListByOwner("proactive_user")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("expected 0 memories stored in proactive turn, got %d", len(stored))
	}

	// 2. reflect in proactive turn must be blocked
	reflectRes, err := tool.ExecuteContext(proactiveCtx, `{"action":"reflect"}`)
	if err != nil {
		t.Fatalf("reflect unexpected error: %v", err)
	}
	if reflectRes != "主动回复轮次禁止触发记忆反思重构" {
		t.Fatalf("expected reflect rejection, got: %s", reflectRes)
	}

	// 3. Write a memory legitimately outside proactive turn
	normalCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		Owner:     "proactive_user",
		OwnerType: memory.OwnerUser,
	})
	_, _ = tool.ExecuteContext(normalCtx, `{"action":"write","content":"Alice loves apples","tags":["apple"]}`)

	// Search in proactive turn should be allowed but not increment AccessCount
	searchRes, err := tool.ExecuteContext(proactiveCtx, `{"action":"search","tags":["apple"]}`)
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
	// Check AccessCount remains 0 in store
	storedAfter, _ := store.ListByOwner("proactive_user")
	if len(storedAfter) != 1 || storedAfter[0].AccessCount != 0 {
		t.Fatalf("expected AccessCount = 0 for proactive search, got %d", storedAfter[0].AccessCount)
	}
}
