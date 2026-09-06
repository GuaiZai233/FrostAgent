package logs

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestConsoleSummaryDoesNotTruncateStoredLog(t *testing.T) {
	a := New("a1b2c3d4", "实例甲", 2)
	b := New("b1c2d3e4", "实例乙", 2)
	full := strings.Repeat("字", 240) + "\nend"
	a.LLMRequest(full)
	entry := a.Snapshot()[0]
	if entry.Content != full || len(b.Snapshot()) != 0 {
		t.Fatal("full log lost or leaked")
	}
	console := Console(entry)
	parts := strings.SplitN(console, "] ", 2)
	if len(parts) != 2 || utf8.RuneCountInString(parts[1]) != 200 || !strings.HasSuffix(parts[1], "...") || strings.Contains(console, "\n") {
		t.Fatal(console)
	}
	if !strings.Contains(console, "(Instance: 实例甲)") {
		t.Fatal(console)
	}
	e := LogEntry{Timestamp: time.Now(), Category: TOOL, Level: INFO, Content: full}
	if !strings.HasSuffix(Console(e), full) {
		t.Fatal("TOOL unexpectedly truncated")
	}
}
func TestImageCacheIsPerStore(t *testing.T) {
	a := New("a1b2c3d4", "a", 2)
	b := New("b1c2d3e4", "b", 2)
	a.LLMRequest(`{"image_url":"data:image/png;base64,aGVsbG8="}`)
	a.mu.RLock()
	var hash string
	for key := range a.imageCache {
		hash = key
	}
	a.mu.RUnlock()
	if hash == "" {
		t.Fatal("image not retained")
	}
	request := httptest.NewRequest("GET", LogImagePathPrefix+hash, nil)
	wa := httptest.NewRecorder()
	a.ImageHandler(wa, request)
	wb := httptest.NewRecorder()
	b.ImageHandler(wb, request)
	if wa.Code != 200 || wb.Code != 404 {
		t.Fatal(wa.Code, wb.Code)
	}
	a.Clear()
	wc := httptest.NewRecorder()
	a.ImageHandler(wc, request)
	if wc.Code != 404 {
		t.Fatal("image remained after clear")
	}
}
