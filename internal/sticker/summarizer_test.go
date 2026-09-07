package sticker

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type mockVisionCaller struct {
	mu                     sync.Mutex
	desc                   string
	keywords               []string
	suspectedInappropriate bool
	err                    error
	called                 int
}

func (m *mockVisionCaller) Describe(imageBase64, mimeType string) (string, []string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.called++
	if m.err != nil {
		return "", nil, false, m.err
	}
	return m.desc, m.keywords, m.suspectedInappropriate, nil
}

func (m *mockVisionCaller) setResponse(desc string, keywords []string, suspectedInappropriate bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.desc = desc
	m.keywords = keywords
	m.suspectedInappropriate = suspectedInappropriate
	m.err = err
}

func (m *mockVisionCaller) getCalled() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.called
}

func TestParseVisionResult(t *testing.T) {
	tests := []struct {
		input                      string
		wantDesc                   string
		wantKeywords               []string
		wantSuspectedInappropriate bool
		wantErr                    bool
	}{
		{
			input:        `{"description": "一只猫猫在挥手", "keywords": ["开心", "打招呼", "猫猫"], "suspected_inappropriate": false}`,
			wantDesc:     "一只猫猫在挥手",
			wantKeywords: []string{"开心", "打招呼", "猫猫"},
		},
		{
			input:        "```json\n{\"description\": \"委屈巴巴的表情\", \"keywords\": [\"委屈\", \"难过\"], \"suspected_inappropriate\": false}\n```",
			wantDesc:     "委屈巴巴的表情",
			wantKeywords: []string{"委屈", "难过"},
		},
		{
			input:                      `这里是分析结果：{"description": "测试", "keywords": ["测试词"], "suspected_inappropriate": true} 谢谢`,
			wantDesc:                   "测试",
			wantKeywords:               []string{"测试词"},
			wantSuspectedInappropriate: true,
		},
		{
			input:   `{"description":"缺少安全字段","keywords":["测试"]}`,
			wantErr: true,
		},
		{
			input:   `不是JSON`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		desc, kws, suspectedInappropriate, err := ParseVisionResult(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseVisionResult(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if tt.wantErr {
			continue
		}
		if desc != tt.wantDesc {
			t.Errorf("ParseVisionResult(%q) desc = %q, want %q", tt.input, desc, tt.wantDesc)
		}
		if len(kws) != len(tt.wantKeywords) {
			t.Errorf("ParseVisionResult(%q) kws length = %d, want %d", tt.input, len(kws), len(tt.wantKeywords))
		}
		if suspectedInappropriate != tt.wantSuspectedInappropriate {
			t.Errorf("ParseVisionResult(%q) suspected_inappropriate = %v, want %v", tt.input, suspectedInappropriate, tt.wantSuspectedInappropriate)
		}
	}
}

func TestSummarizerMarksSuspectedInappropriateStickerUnused(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	mockVision := &mockVisionCaller{
		desc:                   "疑似不适合主动发送的图片",
		keywords:               []string{"敏感"},
		suspectedInappropriate: true,
	}
	summarizer := NewSummarizer(store, mockVision)
	defer summarizer.Stop()

	data := []byte("fake_inappropriate_image")
	id := HashBytes(data)
	if err := store.Add(id, id+".png", data); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	summarizer.Enqueue(id)

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		entry, ok := store.Get(id)
		if ok && entry.Status == StatusReady {
			if !entry.SuspectedInappropriate || entry.Weight != 0 {
				t.Fatalf("flagged entry = %+v, want suspected and weight 0", entry)
			}
			return
		}
	}
	t.Fatal("summarizer did not process sticker in time")
}

func TestSummarizer_ProcessAndRetry(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	mockVision := &mockVisionCaller{
		desc:     "一只生气的柴犬",
		keywords: []string{"生气", "愤怒", "柴犬"},
	}

	summarizer := NewSummarizer(store, mockVision)
	defer summarizer.Stop()

	// 1. Add an unsummarized sticker
	data := []byte("fake_image_dog")
	hash := HashBytes(data)
	if err := store.Add(hash, hash+".png", data); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 2. Enqueue and wait for worker
	summarizer.Enqueue(hash)

	// Poll until status becomes ready
	var ready bool
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		entry, ok := store.Get(hash)
		if ok && entry.Status == StatusReady {
			ready = true
			if entry.Description != "一只生气的柴犬" {
				t.Errorf("unexpected desc: %s", entry.Description)
			}
			break
		}
	}
	if !ready {
		t.Fatal("summarizer did not process sticker in time")
	}

	// 3. Test retry unsummarized when vision fails
	mockVision.setResponse("", nil, false, errors.New("network error"))
	data2 := []byte("fake_image_cat")
	hash2 := HashBytes(data2)
	_ = store.Add(hash2, hash2+".png", data2)

	currentCalls := mockVision.getCalled()
	summarizer.Enqueue(hash2)
	for i := 0; i < 50; i++ {
		if mockVision.getCalled() > currentCalls {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	entry2, _ := store.Get(hash2)
	if entry2.Status != StatusUnsummarized {
		t.Errorf("expected failed sticker to remain unsummarized, got %s", entry2.Status)
	}

	// 4. Recover vision and retry all unsummarized
	mockVision.setResponse("一只好奇的猫咪", []string{"好奇", "猫咪"}, false, nil)

	enqueued := summarizer.EnqueueUnsummarized()
	if enqueued != 1 {
		t.Errorf("expected 1 enqueued, got %d", enqueued)
	}

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		entry, ok := store.Get(hash2)
		if ok && entry.Status == StatusReady {
			if entry.Description != "一只好奇的猫咪" {
				t.Errorf("unexpected desc: %s", entry.Description)
			}
			return
		}
	}
	t.Fatal("retry did not succeed in time")
}
