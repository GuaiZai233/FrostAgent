package sticker

import (
	"FrostAgent/internal/logs"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

type VisionCaller interface {
	Describe(imageBase64, mimeType string) (description string, keywords []string, err error)
}

type Summarizer struct {
	store  *Store
	vision VisionCaller
	queue  chan string
	wg     sync.WaitGroup
	stop   chan struct{}
}

func NewSummarizer(store *Store, vision VisionCaller) *Summarizer {
	s := &Summarizer{
		store:  store,
		vision: vision,
		queue:  make(chan string, 256),
		stop:   make(chan struct{}),
	}
	s.wg.Add(1)
	go s.worker()
	return s
}

func (s *Summarizer) Enqueue(id string) {
	select {
	case s.queue <- id:
	default:
		logs.Warn(logs.SYSTEM, fmt.Sprintf("sticker summarizer: queue full, dropping %s", id[:12]))
	}
}

func (s *Summarizer) EnqueueUnsummarized() int {
	entries := s.store.Unsummarized()
	count := 0
	for _, e := range entries {
		select {
		case s.queue <- e.ID:
			count++
		default:
		}
	}
	return count
}

func (s *Summarizer) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *Summarizer) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		case id := <-s.queue:
			s.process(id)
		}
	}
}

func (s *Summarizer) process(id string) {
	if s.vision == nil {
		return
	}

	entry, ok := s.store.Get(id)
	if !ok || entry.Status != StatusUnsummarized {
		return
	}

	filePath, ok := s.store.FilePath(id)
	if !ok {
		return
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("sticker summarizer: read %s: %v", id[:12], err))
		return
	}

	mime := guessMimeType(entry.FileName)
	b64 := base64.StdEncoding.EncodeToString(data)

	desc, keywords, err := s.vision.Describe(b64, mime)
	if err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("sticker summarizer: vision call failed for %s: %v", id[:12], err))
		return
	}

	if err := s.store.Update(id, desc, keywords); err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("sticker summarizer: update %s: %v", id[:12], err))
	} else {
		logs.Info(logs.SYSTEM, fmt.Sprintf("sticker summarizer: %s => %q %v", id[:12], desc, keywords))
	}
}

func guessMimeType(fileName string) string {
	lower := strings.ToLower(fileName)
	switch {
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// VisionResult is the structured output from the vision model.
type VisionResult struct {
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
}

func ParseVisionResult(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		raw = raw[start : end+1]
	}

	var result VisionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return raw, nil
	}
	return result.Description, result.Keywords
}
