package logs

import (
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func captureConsole(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	defer func() {
		os.Stdout = previous
		_ = writer.Close()
		_ = reader.Close()
	}()
	fn()
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = previous
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestConsoleRedactsStoredLogContent(t *testing.T) {
	a := New("a1b2c3d4", "实例甲", 2)
	b := New("b1c2d3e4", "实例乙", 2)
	full := strings.Repeat("字", 240) + "\nend"
	a.LLMRequest(full)
	entry := a.Snapshot()[0]
	if entry.Content != full || len(b.Snapshot()) != 0 {
		t.Fatal("full log lost or leaked")
	}
	console := Console(entry)
	if !strings.HasSuffix(console, "] ...") || strings.Contains(console, full) || strings.Contains(console, "\n") {
		t.Fatal(console)
	}
	if !strings.Contains(console, "(Instance: 实例甲)") {
		t.Fatal(console)
	}
	e := LogEntry{Timestamp: time.Now(), Category: TOOL, Level: INFO, Content: full}
	toolConsole := Console(e)
	if !strings.HasSuffix(toolConsole, "] ...") || strings.Contains(toolConsole, full) || strings.Contains(toolConsole, "\n") {
		t.Fatal(toolConsole)
	}
	listener := formatConsoleLine(time.Now(), "", "General", INFO, HTTP, "listening on :8080")
	if !strings.HasSuffix(listener, "] listening on :8080") {
		t.Fatal(listener)
	}
}

func TestConsoleSummaryKeepsFullStoredAndStreamedContent(t *testing.T) {
	store := New("a1b2c3d4", "实例甲", 2)
	subscription, stream := store.Subscribe(nil)
	defer store.Unsubscribe(subscription)
	full := "full diagnostic payload that must remain outside stdout"
	warning := "full warning payload that must remain outside stdout"
	output := captureConsole(t, func() {
		store.InfoWithConsoleSummary(SYSTEM, full, "安全短事件")
		store.WarnWithConsoleSummary(SYSTEM, warning, "安全警告事件")
	})
	if !strings.Contains(output, "安全短事件") || !strings.Contains(output, "安全警告事件") || strings.Contains(output, full) || strings.Contains(output, warning) {
		t.Fatalf("console did not isolate summary from content: %q", output)
	}
	snapshot := store.Snapshot()
	if len(snapshot) != 2 || snapshot[0].Content != full || snapshot[1].Content != warning {
		t.Fatalf("snapshot lost full content: %+v", snapshot)
	}
	for _, want := range []string{full, warning} {
		select {
		case entry := <-stream:
			if entry.Content != want {
				t.Fatalf("stream content = %q, want %q", entry.Content, want)
			}
		case <-time.After(time.Second):
			t.Fatal("stream did not receive full log entry")
		}
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

func TestStore_ByteBudgetEviction(t *testing.T) {
	// Create store with capacity 10 and an 800-byte budget
	store := NewWithLimits("inst1", "TestInst", 10, 800)
	if store.MaxBytes() != 800 {
		t.Fatalf("expected maxBytes=800, got %d", store.MaxBytes())
	}

	// Write three moderate messages (~177 bytes each in memory)
	msg1 := strings.Repeat("A", 100)
	msg2 := strings.Repeat("B", 100)
	msg3 := strings.Repeat("C", 100)

	store.Info(SYSTEM, msg1)
	store.Info(SYSTEM, msg2)
	store.Info(SYSTEM, msg3)

	snap := store.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(snap))
	}
	if store.TotalBytes() > 800 {
		t.Fatalf("totalBytes (%d) exceeded maxBytes (800)", store.TotalBytes())
	}

	// Now add a large 350-byte message (~427 bytes). This will push totalBytes to ~958,
	// exceeding 800 and triggering eviction of msg1.
	msg4 := strings.Repeat("D", 350)
	store.Info(SYSTEM, msg4)

	if store.TotalBytes() > 800 {
		t.Fatalf("after large write, totalBytes (%d) exceeded maxBytes (800)", store.TotalBytes())
	}

	snapAfter := store.Snapshot()
	if len(snapAfter) >= 4 {
		t.Fatalf("expected oldest entries to be evicted, got %d entries", len(snapAfter))
	}

	// msg1 must have been evicted
	for _, e := range snapAfter {
		if e.Content == msg1 {
			t.Fatalf("expected msg1 to be evicted, but found in snapshot")
		}
	}

	// The latest entry (msg4) must definitely be in snapshot
	last := snapAfter[len(snapAfter)-1]
	if last.Content != msg4 {
		t.Fatalf("expected latest entry to be msg4, got %q", last.Content)
	}

	// Ensure entries in snapshot are strictly ordered by ID
	for i := 1; i < len(snapAfter); i++ {
		if snapAfter[i].ID <= snapAfter[i-1].ID {
			t.Fatalf("entries not strictly sorted by ID: %d <= %d", snapAfter[i].ID, snapAfter[i-1].ID)
		}
	}

	// Clear resets totalBytes
	store.Clear()
	if store.TotalBytes() != 0 {
		t.Fatalf("expected totalBytes=0 after Clear, got %d", store.TotalBytes())
	}
	if len(store.Snapshot()) != 0 {
		t.Fatalf("expected 0 entries after Clear")
	}
}

func TestStore_SetMaxBytesRetroactiveEviction(t *testing.T) {
	store := NewWithLimits("inst2", "TestInst2", 10, 2000)
	store.Info(SYSTEM, strings.Repeat("1", 200))
	store.Info(SYSTEM, strings.Repeat("2", 200))
	store.Info(SYSTEM, strings.Repeat("3", 200))

	if store.TotalBytes() < 600 {
		t.Fatalf("expected totalBytes >= 600, got %d", store.TotalBytes())
	}

	// Lower maxBytes to 350, triggering immediate retroactive eviction
	store.SetMaxBytes(350)
	if store.TotalBytes() > 350 {
		t.Fatalf("expected totalBytes <= 350 after SetMaxBytes, got %d", store.TotalBytes())
	}

	snap := store.Snapshot()
	if len(snap) >= 3 {
		t.Fatalf("expected older entries to be evicted, got %d entries", len(snap))
	}
}
