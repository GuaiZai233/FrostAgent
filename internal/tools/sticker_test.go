package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSendStickerTool(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "1")
	tempDir := t.TempDir()
	store, err := sticker.NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// 1. Add sticker A (ready, weight 10, keywords: ["开心", "哈哈"])
	dataA := []byte("fake_image_happy")
	hashA := sticker.HashBytes(dataA)
	if err := store.Add(hashA, hashA+".jpg", dataA); err != nil {
		t.Fatalf("Add A failed: %v", err)
	}
	_ = store.Update(hashA, "开心的柴犬在大笑", []string{"开心", "哈哈", "柴犬"})
	for i := 0; i < 9; i++ {
		_ = store.IncrementWeight(hashA)
	}

	// 2. Add sticker B (ready, weight 1, keywords: ["生气", "愤怒"])
	dataB := []byte("fake_image_angry")
	hashB := sticker.HashBytes(dataB)
	if err := store.Add(hashB, hashB+".jpg", dataB); err != nil {
		t.Fatalf("Add B failed: %v", err)
	}
	_ = store.Update(hashB, "非常生气的表情", []string{"生气", "愤怒"})

	// 3. Add sticker C (unsummarized, keywords empty)
	dataC := []byte("fake_image_unsummarized")
	hashC := sticker.HashBytes(dataC)
	_ = store.Add(hashC, hashC+".jpg", dataC)

	// 4. Add sticker D (ready but marked as suspected inappropriate)
	dataD := []byte("fake_image_inappropriate")
	hashD := sticker.HashBytes(dataD)
	_ = store.Add(hashD, hashD+".jpg", dataD)
	_ = store.UpdateSummary(hashD, "只匹配这个词条", []string{"唯一敏感词条"}, true)

	tool := SendStickerTool(store)

	// Test case: empty query error
	_, err = tool.Execute(`{"query": ""}`)
	if err == nil {
		t.Error("expected error for empty query")
	}

	// Test case: no match query
	out, err := tool.Execute(`{"query": "难过悲伤绝望"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "查无此词条") {
		t.Errorf("expected no match, got: %s", out)
	}

	// Test case: match "开心"
	out, err = tool.Execute(`{"query": "开心"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res struct {
		Messages []Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(res.Messages))
	}
	msg := res.Messages[0]
	if msg.Type != "image" || !msg.IsSticker {
		t.Errorf("unexpected message format: %+v", msg)
	}
	if !strings.Contains(msg.Path, hashA) {
		t.Errorf("expected matched image path to contain hashA %s, got %s", hashA, msg.Path)
	}
	expectedURL := "/api/sticker/" + hashA + "/image"
	if msg.URL != expectedURL {
		t.Errorf("expected sticker image URL %q, got %q", expectedURL, msg.URL)
	}

	// Test case: verify unsummarized sticker C is never matched
	out, err = tool.Execute(`{"query": "unsummarized"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "查无此词条") {
		t.Errorf("unsummarized sticker was matched unexpectedly: %s", out)
	}

	// A uniquely matching suspected-inappropriate sticker must look absent.
	out, err = tool.Execute(`{"query": "唯一敏感词条"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "查无此词条") {
		t.Errorf("suspected-inappropriate sticker was matched unexpectedly: %s", out)
	}
}

func TestSendStickerDisabled(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "0")
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := SendStickerTool(store).Execute(`{"query":"开心"}`)
	if err != nil {
		t.Fatal(err)
	}
	const expected = `{"error":"表情包发送功能已被禁用，请管理员在 FrostAgent 后端启用此功能。"}`
	if result != expected {
		t.Fatalf("disabled result = %q, want %q", result, expected)
	}
}

func TestWeightedRandomSticker(t *testing.T) {
	c1 := stickerCandidate{
		entry:  sticker.Entry{ID: "high_weight"},
		weight: 100,
	}
	c2 := stickerCandidate{
		entry:  sticker.Entry{ID: "low_weight"},
		weight: 1,
	}

	highCount := 0
	for i := 0; i < 100; i++ {
		selected := weightedRandomSticker([]stickerCandidate{c1, c2})
		if selected.entry.ID == "high_weight" {
			highCount++
		}
	}

	// High weight (100 vs 1) should be chosen the vast majority of times
	if highCount < 80 {
		t.Errorf("expected high weight candidate to be chosen >= 80 times, got %d", highCount)
	}
}

func TestSendStickerConfirmationMode(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "1")
	t.Setenv(sticker.ConfirmEnvKey, "true")
	tempDir := t.TempDir()
	store, err := sticker.NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// 1. Add sticker A
	dataA := []byte("fake_image_happy_dog")
	hashA := sticker.HashBytes(dataA)
	if err := store.Add(hashA, hashA+".jpg", dataA); err != nil {
		t.Fatalf("Add A failed: %v", err)
	}
	_ = store.Update(hashA, "开心的柴犬在大笑", []string{"开心", "哈哈", "柴犬"})

	// 2. Add sticker B
	dataB := []byte("fake_image_happy_cat")
	hashB := sticker.HashBytes(dataB)
	if err := store.Add(hashB, hashB+".jpg", dataB); err != nil {
		t.Fatalf("Add B failed: %v", err)
	}
	_ = store.Update(hashB, "开心的猫咪在微笑", []string{"开心", "微笑"})

	tool := SendStickerTool(store)
	ctx := context.Background()

	// Step 1: Query "开心" returns compact candidates without image payload
	execResult, err := tool.ExecuteResult(ctx, `{"query": "开心"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if execResult.MessagePayload != "" {
		t.Errorf("expected MessagePayload to be empty in query mode, got: %s", execResult.MessagePayload)
	}

	var queryResp struct {
		Candidates []struct {
			ID          string   `json:"id"`
			Keywords    []string `json:"keywords"`
			Description string   `json:"description"`
		} `json:"candidates"`
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(execResult.Content), &queryResp); err != nil {
		t.Fatalf("failed to parse query response: %v\ncontent: %s", err, execResult.Content)
	}
	if len(queryResp.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(queryResp.Candidates))
	}
	if !strings.Contains(queryResp.Hint, "send_sticker") {
		t.Errorf("expected hint to mention send_sticker, got: %s", queryResp.Hint)
	}

	foundA, foundB := false, false
	for _, c := range queryResp.Candidates {
		if c.ID == hashA && c.Description == "开心的柴犬在大笑" {
			foundA = true
		}
		if c.ID == hashB && c.Description == "开心的猫咪在微笑" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Errorf("expected candidates to contain both A and B, got: %+v", queryResp.Candidates)
	}

	// Step 2: Select sticker A by ID returns image message payload
	selectResult, err := tool.ExecuteResult(ctx, `{"id": "`+hashA+`"}`)
	if err != nil {
		t.Fatalf("unexpected error during select: %v", err)
	}
	if selectResult.MessagePayload == "" {
		t.Fatal("expected non-empty MessagePayload on select")
	}

	var res struct {
		Messages []Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(selectResult.MessagePayload), &res); err != nil {
		t.Fatalf("failed to parse select payload: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(res.Messages))
	}
	if res.Messages[0].Type != "image" || !res.Messages[0].IsSticker {
		t.Errorf("unexpected message: %+v", res.Messages[0])
	}
	if !strings.Contains(res.Messages[0].Path, hashA) {
		t.Errorf("expected image path to contain hashA %s, got %s", hashA, res.Messages[0].Path)
	}

	// Step 3: Select invalid ID
	invalidResult, err := tool.ExecuteResult(ctx, `{"id": "non_existent_id"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(invalidResult.Content, "指定表情包不存在或不可用") {
		t.Errorf("expected error for non-existent id, got: %s", invalidResult.Content)
	}
	if invalidResult.MessagePayload != "" {
		t.Errorf("expected empty MessagePayload for failed select, got: %s", invalidResult.MessagePayload)
	}

	// Step 4: Query with no matching items
	noMatchResult, err := tool.ExecuteResult(ctx, `{"query": "不存在的情绪词"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(noMatchResult.Content, "查无此词条") {
		t.Errorf("expected 查无此词条, got: %s", noMatchResult.Content)
	}

	// Step 5: Empty query and id
	_, err = tool.ExecuteResult(ctx, `{"query": ""}`)
	if err == nil {
		t.Error("expected error for empty query and id")
	}
}

func TestSendStickerConfirmationBoundsCandidates(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "1")
	t.Setenv(sticker.ConfirmEnvKey, "true")
	for _, tc := range []struct {
		name        string
		count       int
		description string
		keywords    []string
		wantCount   int
		truncated   bool
	}{
		{
			name: "exact candidate limit", count: stickerConfirmMaxCandidates,
			description: "开心的猫咪", keywords: []string{"开心"},
			wantCount: stickerConfirmMaxCandidates,
		},
		{
			name: "large library", count: 120,
			description: strings.Repeat("猫", 180), keywords: []string{"开心"},
			wantCount: stickerConfirmMaxCandidates, truncated: true,
		},
		{
			name: "oversized unicode fields", count: 1,
			description: strings.Repeat("开心猫咪🙂", 1500),
			keywords:    strings.Split(strings.Repeat("开心"+strings.Repeat("猫🙂", 100)+",", 30), ","),
			wantCount:   1, truncated: true,
		},
		{
			name: "serialized escape budget", count: stickerConfirmMaxCandidates,
			description: strings.Repeat("<>&\"\\\x00", 1000),
			keywords:    strings.Split(strings.Repeat("开心"+strings.Repeat("<>&\x00", 40)+",", 12), ","),
			truncated:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for i := range tc.count {
				data := []byte(fmt.Sprintf("confirmation-candidate-%d", i))
				id := sticker.HashBytes(data)
				ids = append(ids, id)
				if err := store.Add(id, id+".png", data); err != nil {
					t.Fatal(err)
				}
				if err := store.Update(id, tc.description, tc.keywords); err != nil {
					t.Fatal(err)
				}
			}
			tool := SendStickerTool(store)
			result, err := tool.ExecuteResult(context.Background(), `{"query":"开心"}`)
			if err != nil {
				t.Fatal(err)
			}
			if result.MessagePayload != "" {
				t.Fatal("candidate lookup must not prepare an outbound message")
			}
			if len(result.Content) > stickerConfirmMaxOutputBytes || !utf8.ValidString(result.Content) {
				t.Fatalf("invalid candidate output: bytes=%d valid_utf8=%v", len(result.Content), utf8.ValidString(result.Content))
			}
			var response stickerConfirmationResponse
			if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
				t.Fatalf("candidate JSON must remain complete: %v", err)
			}
			if response.Truncated != tc.truncated || response.Hint != stickerConfirmationHint {
				t.Fatalf("missing truncation metadata or selection hint: truncated=%v", response.Truncated)
			}
			if len(response.Candidates) == 0 || len(response.Candidates) > stickerConfirmMaxCandidates {
				t.Fatalf("unexpected candidate count: %d", len(response.Candidates))
			}
			if tc.wantCount > 0 && len(response.Candidates) != tc.wantCount {
				t.Fatalf("candidate count=%d, want %d", len(response.Candidates), tc.wantCount)
			}
			if tc.wantCount == 0 && len(response.Candidates) >= tc.count {
				t.Fatal("escape-heavy fields must exercise the serialized byte limit, not just the count limit")
			}
			for i, candidate := range response.Candidates {
				if candidate.ID != ids[i] {
					t.Fatalf("candidate ID or store order changed: got %q, want %q", candidate.ID, ids[i])
				}
				if len(candidate.Description) > stickerConfirmMaxDescriptionBytes || !utf8.ValidString(candidate.Description) || strings.ContainsRune(candidate.Description, utf8.RuneError) {
					t.Fatal("description was not bounded on a UTF-8 boundary")
				}
				if len(candidate.Keywords) > stickerConfirmMaxKeywords {
					t.Fatalf("too many keywords: %d", len(candidate.Keywords))
				}
				for _, keyword := range candidate.Keywords {
					if len(keyword) > stickerConfirmMaxKeywordBytes || !utf8.ValidString(keyword) || strings.ContainsRune(keyword, utf8.RuneError) {
						t.Fatal("keyword was not bounded on a UTF-8 boundary")
					}
				}
			}
			entry, ok := store.Get(ids[0])
			if !ok || entry.Description != tc.description || strings.Join(entry.Keywords, "\x01") != strings.Join(tc.keywords, "\x01") {
				t.Fatal("compacting candidates must not change stored metadata")
			}
			selected, err := tool.ExecuteResult(context.Background(), `{"id":"`+response.Candidates[0].ID+`"}`)
			if err != nil || selected.MessagePayload == "" {
				t.Fatalf("bounded candidates must remain selectable: %v", err)
			}
		})
	}
}

func TestCompactStickerTextUTF8Boundaries(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		maxBytes         int
		truncated        bool
	}{
		{name: "empty", maxBytes: 8},
		{name: "exact budget", text: "开心🙂", want: "开心🙂", maxBytes: 10},
		{name: "Chinese boundary", text: "开心的猫咪", want: "开心…", maxBytes: 10, truncated: true},
		{name: "emoji boundary", text: "🙂🙂🙂", want: "🙂…", maxBytes: 10, truncated: true},
		{name: "ASCII boundary", text: "abcdefghijk", want: "abcdefg…", maxBytes: 10, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := compactStickerText(tc.text, tc.maxBytes)
			if got != tc.want || truncated != tc.truncated || !utf8.ValidString(got) {
				t.Fatalf("compactStickerText = (%q, %v), want (%q, %v)", got, truncated, tc.want, tc.truncated)
			}
		})
	}
}
