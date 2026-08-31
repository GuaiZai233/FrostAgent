package tools

import (
	"FrostAgent/internal/sticker"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendStickerTool(t *testing.T) {
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
