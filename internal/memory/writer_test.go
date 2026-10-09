package memory

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/modelrouter"
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
)

type mockWriterLLM struct {
	customReply func(req core.ChatRequest) (string, error)
}

func (m *mockWriterLLM) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	if m.customReply != nil {
		reply, err := m.customReply(req)
		if err != nil {
			return nil, err
		}
		return &core.ChatResponse{
			Message: core.ChatMessage{
				Role:    core.RoleAssistant,
				Content: reply,
			},
		}, nil
	}
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "[]",
		},
	}, nil
}

func TestWriter_ExtractGroupTurn_EvidenceAndAttributionEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)
	gm := NewGroupManager(tmpDir, nil)

	mockLLM := &mockWriterLLM{}
	writer := NewWriter(store)
	writer.SetGroupManager(gm)
	writer.SetLLM(mockLLM, "mock-writer-model")

	groupID := "syn_test_grp_writer_01"
	speakerID := "syn_u_speaker_01"
	speakerName := "张三"

	gStore, err := gm.GetGroupStore(groupID)
	if err != nil {
		t.Fatalf("GetGroupStore failed: %v", err)
	}

	messages := []core.ChatMessage{
		{
			Role:    core.RoleUser,
			Content: "张三: 我平时每天早上喝生椰拿铁",
		},
		{
			Role:    core.RoleUser,
			Content: "李四明天要参加高考考试，大家祝他顺利",
		},
		{
			Role:    core.RoleAssistant,
			Content: "收到，已为李四送上高考祝福，并且推荐了高考复习资料",
		},
	}

	extractedEntries := []groupExtractedCandidate{
		{
			// 1. Missing / trivial evidence (< 3 runes): must be rejected
			Summary:  "用户每天喝生椰拿铁",
			Tags:     []string{"drink"},
			Evidence: "喝",
			IsSelf:   true,
		},
		{
			// 2. Foreign evidence (not present in any message in this turn): must be rejected
			Summary:  "用户喜欢在周末打篮球",
			Tags:     []string{"sport"},
			Evidence: "周末打篮球",
			IsSelf:   true,
		},
		{
			// 3. Assistant-derived evidence (present in RoleAssistant, not in RoleUser): must be rejected
			Summary:  "推荐了高考复习资料",
			Tags:     []string{"study"},
			Evidence: "高考复习资料",
			IsSelf:   false,
		},
		{
			// 4. Evidence-plus-fabricated-suffix / unsupported additions in Summary:
			// "我平时每天早上喝生椰拿铁" -> summary appends fabricated suffix "并且是本群的管理员"
			// Under Option A, authoritative Content is strictly verbatim "生椰拿铁"!
			Summary:  "用户每天早上喝生椰拿铁，并且是本群的管理员",
			Tags:     []string{"drink", "admin"},
			Evidence: "生椰拿铁",
			IsSelf:   true,
		},
		{
			// 5. Valid general group fact: is_self: false, grounded in User message -> saved as group
			Summary:  "群友祝李四高考顺利",
			Tags:     []string{"group", "exam"},
			Evidence: "高考考试",
			IsSelf:   false,
		},
	}

	rawJSON, err := json.Marshal(extractedEntries)
	if err != nil {
		t.Fatalf("marshal extracted entries failed: %v", err)
	}

	mockLLM.customReply = func(req core.ChatRequest) (string, error) {
		return string(rawJSON), nil
	}

	err = writer.ExtractGroupTurnWithRouteContext(
		context.Background(),
		groupID,
		speakerID,
		speakerName,
		core.RouteContext{},
		messages,
		nil,
	)
	if err != nil {
		t.Fatalf("ExtractGroupTurnWithRouteContext failed: %v", err)
	}

	savedEntries, err := gStore.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}

	t.Logf("Total saved entries: %d", len(savedEntries))
	for i, ent := range savedEntries {
		t.Logf("entry [%d]: Owner=%q Content=%q Evidence=%q Summary=%q",
			i, ent.Owner, ent.Content, ent.Evidence, ent.Summary)
	}

	// Exactly 2 entries must be saved: #4 (valid personal fact with verbatim quote) and #5 (valid group fact)
	if len(savedEntries) != 2 {
		t.Fatalf("expected exactly 2 saved entries, got %d", len(savedEntries))
	}

	entriesByContent := make(map[string]MemoryEntry)
	for _, ent := range savedEntries {
		entriesByContent[ent.Content] = ent
	}

	// 4. Valid personal fact: Content is verbatim "生椰拿铁", Owner is speakerID
	e4, ok := entriesByContent["生椰拿铁"]
	if !ok {
		t.Errorf("entry 4 (personal fact, Content='生椰拿铁') missing")
	} else {
		if e4.Owner != speakerID {
			t.Errorf("entry 4 owner mismatch: got %q, want %q", e4.Owner, speakerID)
		}
		if e4.Evidence != "生椰拿铁" {
			t.Errorf("entry 4 evidence mismatch: got %q, want %q", e4.Evidence, "生椰拿铁")
		}
		if e4.Summary != "用户每天早上喝生椰拿铁，并且是本群的管理员" {
			t.Errorf("entry 4 summary mismatch: got %q", e4.Summary)
		}
	}

	// 5. Valid group fact: Content is verbatim "高考考试", Owner is GroupOwnerExplicit
	e5, ok := entriesByContent["高考考试"]
	if !ok {
		t.Errorf("entry 5 (group fact, Content='高考考试') missing")
	} else if e5.Owner != GroupOwnerExplicit {
		t.Errorf("entry 5 owner mismatch: got %q, want %q", e5.Owner, GroupOwnerExplicit)
	}

	// Rejected and fabricated contents must not exist as authoritative Content
	rejectedContents := []string{
		"喝",
		"周末打篮球",
		"高考复习资料",
		"用户每天早上喝生椰拿铁，并且是本群的管理员",
	}
	for _, rejected := range rejectedContents {
		if _, exists := entriesByContent[rejected]; exists {
			t.Errorf("expected rejected content %q to NOT be saved in Content, but found in store", rejected)
		}
	}
}

func TestWriter_ExtractGroupTurn_PolarityAndClauseScopedAttributionRegressions(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)
	gm := NewGroupManager(tmpDir, nil)

	mockLLM := &mockWriterLLM{}
	writer := NewWriter(store)
	writer.SetGroupManager(gm)
	writer.SetLLM(mockLLM, "mock-writer-model")

	groupID := "syn_test_grp_writer_polarity"
	speakerID := "syn_u_speaker_01"
	speakerName := "张三"

	gStore, err := gm.GetGroupStore(groupID)
	if err != nil {
		t.Fatalf("GetGroupStore failed: %v", err)
	}

	messages := []core.ChatMessage{
		{
			Role:    core.RoleUser,
			Content: "张三: 我今天来打机，李四喜欢玩舞萌",
		},
		{
			Role:    core.RoleUser,
			Content: "张三: 我不是管理员",
		},
		{
			Role:    core.RoleUser,
			Content: "张三: 我不喜欢舞萌",
		},
	}

	idx0 := 0
	idx1 := 1
	idx2 := 2

	extractedEntries := []groupExtractedCandidate{
		{
			// Finding 1 regression: "我不是管理员" -> hallucinated affirmative claim "我是管理员"
			// "我是管理员" is NOT in Msg 1 -> rejected!
			Summary:        "我是管理员",
			Tags:           []string{"admin"},
			Evidence:       "我是管理员",
			SourceMsgIndex: &idx1,
			IsSelf:         true,
		},
		{
			// Finding 1 regression: "我不喜欢舞萌" -> hallucinated affirmative claim "我喜欢舞萌"
			// "我喜欢舞萌" is NOT in Msg 2 -> rejected!
			Summary:        "我喜欢舞萌",
			Tags:           []string{"game"},
			Evidence:       "我喜欢舞萌",
			SourceMsgIndex: &idx2,
			IsSelf:         true,
		},
		{
			// Finding 1: Preserve verbatim negative evidence "不是管理员" even if summary attempts inversion
			Summary:        "我是管理员",
			Tags:           []string{"admin"},
			Evidence:       "不是管理员",
			SourceMsgIndex: &idx1,
			IsSelf:         true,
		},
		{
			// Finding 2: Preserve valid quoted self-claim (first-person statement in source)
			Summary:        "用户今天来打机",
			Tags:           []string{"game"},
			Evidence:       "我今天来打机",
			SourceMsgIndex: &idx0,
			IsSelf:         true,
		},
		{
			// Preserve valid negated self-claim (negation maintained)
			Summary:        "用户不喜欢舞萌",
			Tags:           []string{"game"},
			Evidence:       "我不喜欢舞萌",
			SourceMsgIndex: &idx2,
			IsSelf:         true,
		},
		{
			// Valid third-person claim saved as group fact (IsSelf: false)
			Summary:        "李四喜欢玩舞萌",
			Tags:           []string{"game"},
			Evidence:       "李四喜欢玩舞萌",
			SourceMsgIndex: &idx0,
			IsSelf:         false,
		},
	}

	rawJSON, err := json.Marshal(extractedEntries)
	if err != nil {
		t.Fatalf("marshal extracted entries failed: %v", err)
	}

	mockLLM.customReply = func(req core.ChatRequest) (string, error) {
		return string(rawJSON), nil
	}

	err = writer.ExtractGroupTurnWithRouteContext(
		context.Background(),
		groupID,
		speakerID,
		speakerName,
		core.RouteContext{},
		messages,
		nil,
	)
	if err != nil {
		t.Fatalf("ExtractGroupTurnWithRouteContext failed: %v", err)
	}

	savedEntries, err := gStore.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}

	entriesByContent := make(map[string][]MemoryEntry)
	for _, ent := range savedEntries {
		entriesByContent[ent.Content] = append(entriesByContent[ent.Content], ent)
	}

	// 1. Preserved valid quoted self-claim: "我今天来打机" -> Owner == speakerID
	selfEntries, ok := entriesByContent["我今天来打机"]
	if !ok || len(selfEntries) == 0 {
		t.Fatalf("expected preserved self-claim '我今天来打机' to be saved")
	}
	if selfEntries[0].Owner != speakerID {
		t.Errorf("expected '我今天来打机' owner to be %q, got %q", speakerID, selfEntries[0].Owner)
	}

	// 2. Preserved valid negative self-claim: "我不喜欢舞萌" -> Owner == speakerID
	negEntries, ok := entriesByContent["我不喜欢舞萌"]
	if !ok || len(negEntries) == 0 {
		t.Fatalf("expected preserved negative claim '我不喜欢舞萌' to be saved")
	}
	if negEntries[0].Owner != speakerID {
		t.Errorf("expected '我不喜欢舞萌' owner to be %q, got %q", speakerID, negEntries[0].Owner)
	}

	// 3. Valid group fact: "李四喜欢玩舞萌" (IsSelf: false) -> Owner == GroupOwnerExplicit
	groupEntries, ok := entriesByContent["李四喜欢玩舞萌"]
	if !ok || len(groupEntries) == 0 {
		t.Fatalf("expected group fact '李四喜欢玩舞萌' to be saved")
	}
	if groupEntries[0].Owner != GroupOwnerExplicit {
		t.Errorf("expected '李四喜欢玩舞萌' owner to be %q, got %q", GroupOwnerExplicit, groupEntries[0].Owner)
	}

	// 4. Inverted contents must NEVER exist as authoritative Content in store
	rejectedContents := []string{
		"我是管理员",
		"我喜欢舞萌",
	}
	for _, rej := range rejectedContents {
		if _, exists := entriesByContent[rej]; exists {
			t.Errorf("expected rejected content %q to NOT exist in store", rej)
		}
	}
}

func TestCrossTrigger_TurnAndCompact_IdempotencyAndConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)
	gm := NewGroupManager(tmpDir, nil)

	mockLLM := &mockWriterLLM{}
	writer := NewWriter(store)
	writer.SetGroupManager(gm)
	writer.SetLLM(mockLLM, "mock-writer-model")

	groupID := "syn_test_grp_cross_trigger"
	gStore, err := gm.GetGroupStore(groupID)
	if err != nil {
		t.Fatalf("GetGroupStore failed: %v", err)
	}

	// Identical source message that will be seen in both turn extraction and compact distillation
	sharedMessageID := "syn_msg_998877"
	sharedSpeakerID := "syn_user_alice_42"
	sourceMessages := []GroupMessage{
		{
			MessageID: sharedMessageID,
			SenderID:  sharedSpeakerID,
			Sender:    "Alice",
			Role:      "user",
			Content:   "Alice: 我平时每天早上喝生椰拿铁，对花生重度过敏",
		},
	}

	idx0 := 0
	extractedCandidateJSON, _ := json.Marshal([]groupExtractedCandidate{
		{
			Evidence:       "对花生重度过敏",
			Summary:        "Alice自述对花生重度过敏",
			Tags:           []string{"allergy", "peanut"},
			SourceMsgIndex: &idx0,
			IsSelf:         true,
		},
	})

	mockLLM.customReply = func(req core.ChatRequest) (string, error) {
		return string(extractedCandidateJSON), nil
	}

	route := core.RouteContext{Platform: "onebot", GroupID: groupID}

	// Trigger 1: Turn extraction (awakened turn completion)
	err = writer.ExtractGroupMemories(context.Background(), groupID, route, sourceMessages, SourceExtract, nil)
	if err != nil {
		t.Fatalf("Turn extraction failed: %v", err)
	}

	entriesAfterTurn, err := gStore.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(entriesAfterTurn) != 1 {
		t.Fatalf("expected exactly 1 entry after turn extraction, got %d", len(entriesAfterTurn))
	}
	eTurn := entriesAfterTurn[0]
	if eTurn.Content != "对花生重度过敏" {
		t.Errorf("expected Content '对花生重度过敏', got %q", eTurn.Content)
	}
	if eTurn.Evidence != "对花生重度过敏" {
		t.Errorf("expected Evidence '对花生重度过敏', got %q", eTurn.Evidence)
	}
	if eTurn.SourceMessageID != sharedMessageID {
		t.Errorf("expected SourceMessageID %q, got %q", sharedMessageID, eTurn.SourceMessageID)
	}
	if eTurn.Owner != sharedSpeakerID {
		t.Errorf("expected Owner %q, got %q", sharedSpeakerID, eTurn.Owner)
	}
	if eTurn.Source != SourceExtract {
		t.Errorf("expected Source %q, got %q", SourceExtract, eTurn.Source)
	}

	// Trigger 2: Passive compact buffer distillation (contains the exact same message and quote, plus extra tag)
	distillCandidateJSON, _ := json.Marshal([]groupExtractedCandidate{
		{
			Evidence:       "对花生重度过敏",
			Summary:        "Alice对花生重度过敏（摘要）",
			Tags:           []string{"health", "allergy"},
			SourceMsgIndex: &idx0,
			IsSelf:         true,
		},
	})
	mockLLM.customReply = func(req core.ChatRequest) (string, error) {
		return string(distillCandidateJSON), nil
	}

	err = writer.ExtractGroupMemories(context.Background(), groupID, route, sourceMessages, SourceDistill, nil)
	if err != nil {
		t.Fatalf("Compact distillation failed: %v", err)
	}

	// Cross-trigger idempotency assertion: MUST NOT create duplicate memory entry!
	entriesAfterCompact, err := gStore.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(entriesAfterCompact) != 1 {
		t.Fatalf("cross-trigger idempotency violated: expected 1 entry, got %d", len(entriesAfterCompact))
	}

	eMerged := entriesAfterCompact[0]
	if eMerged.ID != eTurn.ID {
		t.Errorf("expected entry ID to be preserved (%q), got %q", eTurn.ID, eMerged.ID)
	}
	if eMerged.Content != "对花生重度过敏" {
		t.Errorf("expected Content '对花生重度过敏', got %q", eMerged.Content)
	}
	if eMerged.SourceMessageID != sharedMessageID {
		t.Errorf("expected SourceMessageID %q, got %q", sharedMessageID, eMerged.SourceMessageID)
	}
	// Tags should be merged
	hasHealth := false
	hasPeanut := false
	for _, tag := range eMerged.Tags {
		if tag == "health" {
			hasHealth = true
		}
		if tag == "peanut" {
			hasPeanut = true
		}
	}
	if !hasHealth || !hasPeanut {
		t.Errorf("expected merged tags to contain 'health' and 'peanut', got %v", eMerged.Tags)
	}

	// Concurrency test: 10 goroutines calling ExtractGroupMemories concurrently
	var wg sync.WaitGroup
	errCh := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cErr := writer.ExtractGroupMemories(context.Background(), groupID, route, sourceMessages, SourceExtract, nil)
			if cErr != nil {
				errCh <- cErr
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for cErr := range errCh {
		t.Errorf("concurrent ExtractGroupMemories error: %v", cErr)
	}

	entriesAfterConcurrent, err := gStore.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(entriesAfterConcurrent) != 1 {
		t.Fatalf("expected still exactly 1 entry after concurrent executions, got %d", len(entriesAfterConcurrent))
	}
}

func TestWriter_ExtractGroupMemories_DisabledRoute(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := NewStore(storePath)
	gm := NewGroupManager(tmpDir, nil)

	mockLLM := &mockWriterLLM{
		customReply: func(req core.ChatRequest) (string, error) {
			return "", modelrouter.ErrDisabled
		},
	}
	writer := NewWriter(store)
	writer.SetGroupManager(gm)
	writer.SetLLM(mockLLM, "mock-writer-model")

	groupID := "syn_test_grp_disabled_route"
	msgs := []GroupMessage{
		{
			MessageID: "msg_1",
			SenderID:  "u_1",
			Role:      "user",
			Content:   "测试消息",
		},
	}

	err := writer.ExtractGroupMemories(context.Background(), groupID, core.RouteContext{}, msgs, SourceExtract, nil)
	if err != nil {
		t.Fatalf("expected nil when route is disabled, got %v", err)
	}
}
