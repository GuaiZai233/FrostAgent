package logs

import (
	"container/ring"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Category string
type Level string

const (
	SYSTEM       Category = "SYSTEM"
	TOOL         Category = "TOOL"
	LLM_REQUEST  Category = "LLM_REQUEST"
	LLM_RESPONSE Category = "LLM_RESPONSE"
	WEBSOCKET    Category = "WEBSOCKET"
	HTTP         Category = "HTTP"

	INFO  Level = "INFO"
	WARN  Level = "WARN"
	ERROR Level = "ERROR"
	DEBUG Level = "DEBUG"
)

type LogEntry struct {
	ID           uint64    `json:"id"`
	TraceID      string    `json:"trace_id"`
	Timestamp    time.Time `json:"timestamp"`
	Direction    string    `json:"direction"` // INBOUND / OUTBOUND / INTERNAL
	Category     Category  `json:"category"`
	Level        Level     `json:"level"`
	Content      string    `json:"content"`
	InstanceID   string    `json:"instance_id"`
	InstanceName string    `json:"instance_name"`
	imageRefs    []string
}

// subscriber receives broadcasted log entries.
type subscriber struct {
	ch     chan LogEntry
	filter func(LogEntry) bool // nil = accept all
}

type Store struct {
	buffer      *ring.Ring
	mu          sync.RWMutex
	size        int
	subscribers map[int]*subscriber
	nextSubID   int
	subMu       sync.Mutex
	nextEntryID uint64
	imageCache  map[string]*cachedImage
	name        string
	id          string
}

var General = New("", "General", 5000)

func New(id, name string, size int) *Store {
	s := &Store{id: id, name: name, subscribers: make(map[int]*subscriber)}
	s.Init(size)
	return s
}
func (s *Store) Rename(name string) { s.mu.Lock(); s.name = name; s.mu.Unlock() }

// Init 初始化日志系统，指定环形缓冲区大小
func (s *Store) Init(capacity int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.size = max(1, capacity)
	s.buffer = ring.New(s.size)
	s.imageCache = make(map[string]*cachedImage)
	s.nextEntryID = 0
}

func (s *Store) log(level Level, category Category, content string, traceID string, direction string) {
	s.logWithImages(level, category, content, traceID, direction, nil)
}

func (s *Store) logWithImages(level Level, category Category, content string, traceID string, direction string, retainedImages []retainedImage) {
	s.mu.Lock()

	if s.buffer == nil {
		s.mu.Unlock()
		return
	}

	if previous, ok := s.buffer.Value.(LogEntry); ok {
		s.releaseImagesLocked(previous.imageRefs)
	}

	s.nextEntryID++
	entry := LogEntry{
		ID:           s.nextEntryID,
		TraceID:      traceID,
		Timestamp:    time.Now(),
		InstanceID:   s.id,
		InstanceName: s.name,
		Direction:    direction,
		Category:     category,
		Level:        level,
		Content:      content,
	}
	entry.imageRefs = s.retainImagesLocked(retainedImages)

	s.buffer.Value = entry
	s.buffer = s.buffer.Next()
	s.mu.Unlock()

	// 广播给 subscriber
	s.broadcast(entry)

	// 同时输出到控制台，方便调试
	if level != DEBUG {
		fmt.Println(Console(entry))
	}
}

func (s *Store) Info(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	s.log(INFO, category, content, tid, "INTERNAL")
}

// InfoWithInlineImages writes an informational log while retaining its images
// separately. The stored and copied content contains only compact placeholders.
func (s *Store) InfoWithInlineImages(category Category, content string, images []InlineImage, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	retainedImages, placeholders := prepareInlineImages(images)
	if len(placeholders) > 0 {
		content = strings.TrimRight(content, " \t\r\n")
		if content != "" {
			content += "\n"
		}
		content += strings.Join(placeholders, "\n")
	}
	s.logWithImages(INFO, category, content, tid, "INTERNAL", retainedImages)
}

func (s *Store) Warn(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	s.log(WARN, category, content, tid, "INTERNAL")
}

func (s *Store) Error(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	s.log(ERROR, category, content, tid, "INTERNAL")
}

func (s *Store) Debug(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	s.log(DEBUG, category, content, tid, "INTERNAL")
}

func (s *Store) LLMRequest(content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	redacted, retainedImages := redactInlineImages(content)
	s.logWithImages(INFO, LLM_REQUEST, redacted, tid, "OUTBOUND", retainedImages)
}

func (s *Store) LLMResponse(content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	s.log(INFO, LLM_RESPONSE, content, tid, "INBOUND")
}

func (s *Store) Websocket(level Level, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	s.log(level, WEBSOCKET, content, tid, "INTERNAL")
}

// Snapshot 获取当前缓冲区中所有日志的快照
func (s *Store) Snapshot() []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var logs []LogEntry
	if s.buffer == nil {
		return logs
	}

	s.buffer.Do(func(p interface{}) {
		if p != nil {
			logs = append(logs, p.(LogEntry))
		}
	})
	return logs
}

// Clear 清空日志缓冲区
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buffer = ring.New(s.size)
	s.imageCache = make(map[string]*cachedImage)
}

// Subscribe returns a channel that receives new log entries matching the filter.
// filter may be nil to accept all entries. Returns a subscription ID for Unsubscribe.
func (s *Store) Subscribe(filter func(LogEntry) bool) (int, <-chan LogEntry) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	s.nextSubID++
	sub := &subscriber{
		ch:     make(chan LogEntry, 256),
		filter: filter,
	}
	s.subscribers[s.nextSubID] = sub
	return s.nextSubID, sub.ch
}

// Unsubscribe removes a subscription.
func (s *Store) Unsubscribe(id int) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	if sub, ok := s.subscribers[id]; ok {
		close(sub.ch)
		delete(s.subscribers, id)
	}
}

// broadcast sends an entry to all matching s.subscribers.
func (s *Store) broadcast(entry LogEntry) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	for _, sub := range s.subscribers {
		if sub.filter == nil || sub.filter(entry) {
			select {
			case sub.ch <- entry:
			default:
				// slow consumer, drop
			}
		}
	}
}

func Console(e LogEntry) string {
	label := "General"
	if e.InstanceID != "" {
		label = "Instance: " + e.InstanceName
	}
	body := strings.Join(strings.Fields(e.Content), " ")
	r := []rune(body)
	if len(r) > 200 {
		body = string(r[:197]) + "..."
	}
	return fmt.Sprintf("[%s](%s)[%s][%s] %s", e.Timestamp.Format("15:04:05"), label, e.Level, e.Category, body)
}
func Init(size int)                           { General.Init(size) }
func Info(c Category, v string, t ...string)  { General.Info(c, v, t...) }
func Warn(c Category, v string, t ...string)  { General.Warn(c, v, t...) }
func Error(c Category, v string, t ...string) { General.Error(c, v, t...) }
func Debug(c Category, v string, t ...string) { General.Debug(c, v, t...) }
func InfoWithInlineImages(c Category, v string, i []InlineImage, t ...string) {
	General.InfoWithInlineImages(c, v, i, t...)
}
func LLMRequest(v string, t ...string)                       { General.LLMRequest(v, t...) }
func LLMResponse(v string, t ...string)                      { General.LLMResponse(v, t...) }
func Websocket(l Level, v string, t ...string)               { General.Websocket(l, v, t...) }
func Snapshot() []LogEntry                                   { return General.Snapshot() }
func Clear()                                                 { General.Clear() }
func Subscribe(f func(LogEntry) bool) (int, <-chan LogEntry) { return General.Subscribe(f) }
func Unsubscribe(id int)                                     { General.Unsubscribe(id) }

func (s *Store) EndStreams() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for id, sub := range s.subscribers {
		close(sub.ch)
		delete(s.subscribers, id)
	}
}
