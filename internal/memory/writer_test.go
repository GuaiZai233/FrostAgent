package memory

import (
	"FrostAgent/internal/core"
	"context"
	"encoding/json"
	"path/filepath"
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

	extractedEntries := []groupExtractedEntry{
		{
			// 1. Missing / trivial evidence (< 3 runes): must be rejected
			Content:  "用户每天喝生椰拿铁",
			Tags:     []string{"drink"},
			Evidence: "喝",
			IsSelf:   true,
		},
		{
			// 2. Foreign evidence (not present in any message in this turn): must be rejected
			Content:  "用户喜欢在周末打篮球",
			Tags:     []string{"sport"},
			Evidence: "周末打篮球",
			IsSelf:   true,
		},
		{
			// 3. Assistant-derived evidence (present in RoleAssistant, not in RoleUser): must be rejected
			Content:  "推荐了高考复习资料",
			Tags:     []string{"study"},
			Evidence: "高考复习资料",
			IsSelf:   false,
		},
		{
			// 4. Evidence-plus-fabricated-suffix / unsupported additions:
			// "我平时每天早上喝生椰拿铁" -> content appends fabricated suffix "并且是本群的管理员"
			Content:  "用户每天早上喝生椰拿铁，并且是本群的管理员",
			Tags:     []string{"drink", "admin"},
			Evidence: "生椰拿铁",
			IsSelf:   true,
		},
		{
			// 5. Unsupported personal fact: speaker is 张三, message describes third-person 李四 ("李四明天要参加高考考试")
			// is_self: true without first-person self-attribution marker -> must be rejected
			Content:  "李四明天参加高考考试",
			Tags:     []string{"exam"},
			Evidence: "高考考试",
			IsSelf:   true,
		},
		{
			// 6. Valid personal fact: first-person statement, grounded in User message -> saved as speakerID
			Content:  "用户每天早上喝生椰拿铁",
			Tags:     []string{"drink"},
			Evidence: "生椰拿铁",
			IsSelf:   true,
		},
		{
			// 7. Valid general group fact: is_self: false, grounded in User message -> saved as group
			Content:  "群友祝李四高考顺利",
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
		t.Logf("entry [%d]: Owner=%q Content=%q", i, ent.Owner, ent.Content)
	}

	// Exactly 2 entries must be saved: #6 (valid personal fact) and #7 (valid group fact)
	if len(savedEntries) != 2 {
		t.Fatalf("expected exactly 2 saved entries, got %d", len(savedEntries))
	}

	entriesByContent := make(map[string]MemoryEntry)
	for _, ent := range savedEntries {
		entriesByContent[ent.Content] = ent
	}

	// 6. Valid personal fact
	e6, ok := entriesByContent["用户每天早上喝生椰拿铁"]
	if !ok {
		t.Errorf("entry 6 (personal fact) missing")
	} else if e6.Owner != speakerID {
		t.Errorf("entry 6 owner mismatch: got %q, want %q", e6.Owner, speakerID)
	}

	// 7. Valid group fact
	e7, ok := entriesByContent["群友祝李四高考顺利"]
	if !ok {
		t.Errorf("entry 7 (group fact) missing")
	} else if e7.Owner != GroupOwnerExplicit {
		t.Errorf("entry 7 owner mismatch: got %q, want %q", e7.Owner, GroupOwnerExplicit)
	}

	// Rejected entries must not exist
	rejectedContents := []string{
		"用户每天喝生椰拿铁",
		"用户喜欢在周末打篮球",
		"推荐了高考复习资料",
		"用户每天早上喝生椰拿铁，并且是本群的管理员",
		"李四明天参加高考考试",
	}
	for _, rejected := range rejectedContents {
		if _, exists := entriesByContent[rejected]; exists {
			t.Errorf("expected rejected content %q to NOT be saved, but found in store", rejected)
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

	extractedEntries := []groupExtractedEntry{
		{
			// Finding 1 regression: "我不是管理员" -> negation dropped to "我是管理员"
			Content:  "我是管理员",
			Tags:     []string{"admin"},
			Evidence: "不是管理员",
			IsSelf:   true,
		},
		{
			// Finding 1 regression: "我不是管理员" -> negation dropped to "用户是管理员" with partial evidence "管理员"
			Content:  "用户是管理员",
			Tags:     []string{"admin"},
			Evidence: "管理员",
			IsSelf:   true,
		},
		{
			// Finding 1 regression: "我不喜欢舞萌" -> negation dropped to "我喜欢舞萌"
			Content:  "我喜欢舞萌",
			Tags:     []string{"game"},
			Evidence: "不喜欢舞萌",
			IsSelf:   true,
		},
		{
			// Finding 1 regression: "我不喜欢舞萌" -> negation dropped to "用户喜欢舞萌" with partial evidence "舞萌"
			Content:  "用户喜欢舞萌",
			Tags:     []string{"game"},
			Evidence: "舞萌",
			IsSelf:   true,
		},
		{
			// Finding 2 regression: source message has unrelated "我", but evidence/claim is about 李四
			// False self-attribution must be rejected!
			Content:  "李四喜欢玩舞萌",
			Tags:     []string{"game"},
			Evidence: "李四喜欢玩舞萌",
			IsSelf:   true,
		},
		{
			// Finding 2 regression: partial evidence "喜欢玩舞萌" for third person 李四 with IsSelf: true
			Content:  "李四喜欢舞萌",
			Tags:     []string{"game"},
			Evidence: "喜欢玩舞萌",
			IsSelf:   true,
		},
		{
			// Finding 2: Preserve valid quoted self-claim (first-person statement in source)
			Content:  "用户今天来打机",
			Tags:     []string{"game"},
			Evidence: "我今天来打机",
			IsSelf:   true,
		},
		{
			// Preserve valid negated self-claim (negation maintained)
			Content:  "用户不喜欢舞萌",
			Tags:     []string{"game"},
			Evidence: "我不喜欢舞萌",
			IsSelf:   true,
		},
		{
			// Valid third-person claim saved as group fact (IsSelf: false)
			Content:  "李四喜欢玩舞萌",
			Tags:     []string{"game"},
			Evidence: "李四喜欢玩舞萌",
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

	entriesByContent := make(map[string][]MemoryEntry)
	for _, ent := range savedEntries {
		entriesByContent[ent.Content] = append(entriesByContent[ent.Content], ent)
	}

	// 1. Preserved valid quoted self-claim: "用户今天来打机" -> Owner == speakerID
	selfEntries, ok := entriesByContent["用户今天来打机"]
	if !ok || len(selfEntries) == 0 {
		t.Fatalf("expected preserved self-claim '用户今天来打机' to be saved")
	}
	if selfEntries[0].Owner != speakerID {
		t.Errorf("expected '用户今天来打机' owner to be %q, got %q", speakerID, selfEntries[0].Owner)
	}

	// 2. Preserved valid negative self-claim: "用户不喜欢舞萌" -> Owner == speakerID
	negEntries, ok := entriesByContent["用户不喜欢舞萌"]
	if !ok || len(negEntries) == 0 {
		t.Fatalf("expected preserved negative claim '用户不喜欢舞萌' to be saved")
	}
	if negEntries[0].Owner != speakerID {
		t.Errorf("expected '用户不喜欢舞萌' owner to be %q, got %q", speakerID, negEntries[0].Owner)
	}

	// 3. Valid group fact: "李四喜欢玩舞萌" (IsSelf: false) -> Owner == GroupOwnerExplicit
	groupEntries, ok := entriesByContent["李四喜欢玩舞萌"]
	if !ok || len(groupEntries) == 0 {
		t.Fatalf("expected group fact '李四喜欢玩舞萌' to be saved")
	}
	if groupEntries[0].Owner != GroupOwnerExplicit {
		t.Errorf("expected '李四喜欢玩舞萌' owner to be %q, got %q", GroupOwnerExplicit, groupEntries[0].Owner)
	}

	// Verify that none of the inverted or false self-attribution claims were saved as speakerID
	rejectedContents := []string{
		"我是管理员",
		"用户是管理员",
		"我喜欢舞萌",
		"用户喜欢舞萌",
		"李四喜欢舞萌",
	}
	for _, rej := range rejectedContents {
		if _, exists := entriesByContent[rej]; exists {
			t.Errorf("expected rejected content %q to NOT exist in store", rej)
		}
	}
}
