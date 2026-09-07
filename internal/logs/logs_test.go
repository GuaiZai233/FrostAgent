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
