package memsvc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/memory"

	"connectrpc.com/connect"
)

func setupTestService(t *testing.T) (*Service, string, *memory.GroupManager) {
	t.Helper()
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := memory.NewStore(storePath)
	gm := memory.NewGroupManager(tmpDir, nil)
	svc := New(store, gm, nil)
	return svc, tmpDir, gm
}

func TestService_Finding1_AddMemory_ManualNotDiscardedAndPersisted(t *testing.T) {
	svc, _, gm := setupTestService(t)
	groupID := "mock_grp_service_f1"
	gs, err := gm.GetGroupStore(groupID)
	if err != nil {
		t.Fatalf("GetGroupStore failed: %v", err)
	}

	now := time.Now()
	// 1. Seed an automatic entry: Owner="group", Content="周六聚会", SourceMessageID="msg-auto-1"
	autoEntry := memory.MemoryEntry{
		ID:              "auto-service-1",
		Owner:           memory.GroupOwnerExplicit,
		OwnerType:       memory.OwnerGroup,
		ScopeType:       memory.ScopeGroup,
		GroupID:         groupID,
		Content:         "周六聚会",
		Evidence:        "周六聚会",
		SourceMessageID: "msg-auto-1",
		SourceSenderID:  "mock_u_alice",
		Source:          memory.SourceExtract,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := gs.SaveEntry(&autoEntry); err != nil {
		t.Fatalf("SaveEntry auto failed: %v", err)
	}

	// 2. Call ConnectRPC AddMemory with identical content in group scope
	addReq := connect.NewRequest(&v1.AddMemoryRequest{
		Content: "周六聚会",
		Scope:   "group",
		GroupId: groupID,
		Owner:   memory.GroupOwnerExplicit,
		Tags:    []string{"event"},
	})
	addResp, err := svc.AddMemory(context.Background(), addReq)
	if err != nil {
		t.Fatalf("AddMemory RPC failed: %v", err)
	}
	if addResp.Msg.GetError() != "" {
		t.Fatalf("AddMemory returned error: %s", addResp.Msg.GetError())
	}

	returnedMemory := addResp.Msg.GetMemory()
	if returnedMemory == nil {
		t.Fatalf("AddMemory returned nil Memory")
	}
	manualID := returnedMemory.GetId()
	if manualID == "" {
		t.Fatalf("AddMemory returned empty ID")
	}
	// Crucial check: returned ID must NOT be the existing autoEntry.ID!
	if manualID == autoEntry.ID {
		t.Fatalf("AddMemory collapsed manual entry into auto entry %q instead of saving distinct manual entry", autoEntry.ID)
	}

	// 3. Verify ListMemories returns BOTH entries
	listReq := connect.NewRequest(&v1.ListMemoriesRequest{
		Scope:   "group",
		GroupId: groupID,
	})
	listResp, err := svc.ListMemories(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListMemories RPC failed: %v", err)
	}
	if len(listResp.Msg.GetMemories()) != 2 {
		t.Fatalf("expected 2 memories (auto + manual), got %d", len(listResp.Msg.GetMemories()))
	}

	// 4. Verify editing the newly returned manual ID succeeds on disk
	updateReq := connect.NewRequest(&v1.UpdateMemoryRequest{
		Id:      manualID,
		Content: "周日聚会",
		Scope:   "group",
		GroupId: groupID,
	})
	updateResp, err := svc.UpdateMemory(context.Background(), updateReq)
	if err != nil {
		t.Fatalf("UpdateMemory RPC failed: %v", err)
	}
	if !updateResp.Msg.GetSuccess() {
		t.Fatalf("UpdateMemory on returned manual ID failed: %s", updateResp.Msg.GetError())
	}

	// 5. Verify deleting the newly returned manual ID succeeds on disk
	delReq := connect.NewRequest(&v1.DeleteMemoryRequest{
		Id:      manualID,
		Scope:   "group",
		GroupId: groupID,
	})
	delResp, err := svc.DeleteMemory(context.Background(), delReq)
	if err != nil {
		t.Fatalf("DeleteMemory RPC failed: %v", err)
	}
	if !delResp.Msg.GetSuccess() {
		t.Fatalf("DeleteMemory on returned manual ID failed: %s", delResp.Msg.GetError())
	}

	// Only the autoEntry should remain
	listResp2, err := svc.ListMemories(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListMemories after delete failed: %v", err)
	}
	if len(listResp2.Msg.GetMemories()) != 1 || listResp2.Msg.GetMemories()[0].GetId() != autoEntry.ID {
		t.Fatalf("expected only autoEntry to remain, got %+v", listResp2.Msg.GetMemories())
	}
}

func TestService_Finding1_DistinctSourceIdenticalQuotes(t *testing.T) {
	svc, _, gm := setupTestService(t)
	groupID := "mock_grp_service_distinct_sources"
	gs, err := gm.GetGroupStore(groupID)
	if err != nil {
		t.Fatalf("GetGroupStore failed: %v", err)
	}

	now := time.Now()
	// Two platform messages from different senders with identical group quotes
	quoteA := memory.MemoryEntry{
		ID:              "msgA_entry",
		Owner:           memory.GroupOwnerExplicit,
		OwnerType:       memory.OwnerGroup,
		ScopeType:       memory.ScopeGroup,
		GroupID:         groupID,
		Content:         "收到大家汇报",
		Evidence:        "收到大家汇报",
		SourceMessageID: "msg-source-101",
		SourceSenderID:  "mock_u_alice",
		Source:          memory.SourceExtract,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	quoteB := memory.MemoryEntry{
		ID:              "msgB_entry",
		Owner:           memory.GroupOwnerExplicit,
		OwnerType:       memory.OwnerGroup,
		ScopeType:       memory.ScopeGroup,
		GroupID:         groupID,
		Content:         "收到大家汇报",
		Evidence:        "收到大家汇报",
		SourceMessageID: "msg-source-102", // DISTINCT message ID!
		SourceSenderID:  "mock_u_bob",
		Source:          memory.SourceExtract,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := gs.SaveEntry(&quoteA); err != nil {
		t.Fatalf("SaveEntry quoteA failed: %v", err)
	}
	if err := gs.SaveEntry(&quoteB); err != nil {
		t.Fatalf("SaveEntry quoteB failed: %v", err)
	}

	// ConnectRPC ListMemories must return both entries with independent provenance
	listReq := connect.NewRequest(&v1.ListMemoriesRequest{
		Scope:   "group",
		GroupId: groupID,
	})
	listResp, err := svc.ListMemories(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListMemories failed: %v", err)
	}

	mems := listResp.Msg.GetMemories()
	if len(mems) != 2 {
		t.Fatalf("expected 2 distinct entries with identical quotes from distinct messages, got %d", len(mems))
	}

	byMsgID := make(map[string]*v1.MemoryEntry)
	for _, m := range mems {
		byMsgID[m.GetSourceMessageId()] = m
	}

	mA, okA := byMsgID["msg-source-101"]
	mB, okB := byMsgID["msg-source-102"]
	if !okA || !okB {
		t.Fatalf("expected entries for both msg-source-101 and msg-source-102")
	}
	if mA.GetSourceSenderId() != "mock_u_alice" {
		t.Errorf("mA source sender mismatch: got %q, want 'mock_u_alice'", mA.GetSourceSenderId())
	}
	if mB.GetSourceSenderId() != "mock_u_bob" {
		t.Errorf("mB source sender mismatch: got %q, want 'mock_u_bob'", mB.GetSourceSenderId())
	}
}

func TestService_Finding3_UpdateMemory_AuditableProvenance(t *testing.T) {
	svc, _, gm := setupTestService(t)
	groupID := "mock_grp_service_audit"
	gs, err := gm.GetGroupStore(groupID)
	if err != nil {
		t.Fatalf("GetGroupStore failed: %v", err)
	}

	now := time.Now()
	extEntry := memory.MemoryEntry{
		ID:              "ext-audit-01",
		Owner:           memory.GroupOwnerExplicit,
		OwnerType:       memory.OwnerGroup,
		ScopeType:       memory.ScopeGroup,
		GroupID:         groupID,
		Content:         "我不吃花生",
		Evidence:        "我不吃花生",
		SourceMessageID: "msg-audit-999",
		SourceSenderID:  "mock_u_alice",
		Tags:            []string{"allergy"},
		Source:          memory.SourceExtract,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := gs.SaveEntry(&extEntry); err != nil {
		t.Fatalf("SaveEntry extEntry failed: %v", err)
	}

	// Web admin modifies Content via ConnectRPC UpdateMemory
	updateReq := connect.NewRequest(&v1.UpdateMemoryRequest{
		Id:      "ext-audit-01",
		Scope:   "group",
		GroupId: groupID,
		Content: "我每天吃花生",
		Tags:    []string{"allergy", "correction"},
	})
	updateResp, err := svc.UpdateMemory(context.Background(), updateReq)
	if err != nil {
		t.Fatalf("UpdateMemory failed: %v", err)
	}
	if !updateResp.Msg.GetSuccess() {
		t.Fatalf("UpdateMemory returned unsuccessful: %s", updateResp.Msg.GetError())
	}

	// Verify via ListMemories
	listReq := connect.NewRequest(&v1.ListMemoriesRequest{
		Scope:   "group",
		GroupId: groupID,
	})
	listResp, err := svc.ListMemories(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListMemories failed: %v", err)
	}
	if len(listResp.Msg.GetMemories()) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(listResp.Msg.GetMemories()))
	}

	updated := listResp.Msg.GetMemories()[0]
	// Invariant 1: Content is updated
	if updated.GetContent() != "我每天吃花生" {
		t.Errorf("Content mismatch: got %q, want '我每天吃花生'", updated.GetContent())
	}
	// Invariant 2: Source transitioned to manual
	if updated.GetSource() != string(memory.SourceManual) {
		t.Errorf("Source mismatch: got %q, want %q", updated.GetSource(), memory.SourceManual)
	}
	// Invariant 3: Original verbatim quote Evidence is immutable & preserved!
	if updated.GetEvidence() != "我不吃花生" {
		t.Errorf("Evidence mismatch: got %q, want '我不吃花生'", updated.GetEvidence())
	}
	// Invariant 4: SourceMessageId preserved!
	if updated.GetSourceMessageId() != "msg-audit-999" {
		t.Errorf("SourceMessageId mismatch: got %q, want 'msg-audit-999'", updated.GetSourceMessageId())
	}
	// Invariant 5: SourceSenderId preserved!
	if updated.GetSourceSenderId() != "mock_u_alice" {
		t.Errorf("SourceSenderId mismatch: got %q, want 'mock_u_alice'", updated.GetSourceSenderId())
	}
}
