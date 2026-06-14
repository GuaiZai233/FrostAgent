package logs

import (
	"container/ring"
	"fmt"
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
	TraceID   string    `json:"trace_id"`
	Timestamp time.Time `json:"timestamp"`
	Direction string    `json:"direction"` // INBOUND / OUTBOUND / INTERNAL
	Category  Category  `json:"category"`
	Level     Level     `json:"level"`
	Content   string    `json:"content"`
}

var (
	buffer *ring.Ring
	mu     sync.RWMutex
	size   int
)

// Init 初始化日志系统，指定环形缓冲区大小
func Init(s int) {
	size = s
	buffer = ring.New(size)
}

func log(level Level, category Category, content string, traceID string, direction string) {
	mu.Lock()
	defer mu.Unlock()

	if buffer == nil {
		return
	}

	entry := LogEntry{
		TraceID:   traceID,
		Timestamp: time.Now(),
		Direction: direction,
		Category:  category,
		Level:     level,
		Content:   content,
	}

	buffer.Value = entry
	buffer = buffer.Next()
	
	// 同时输出到控制台，方便调试
	if level == ERROR {
		fmt.Printf("[%s][%s][%s] %s\n", entry.Timestamp.Format("15:04:05"), level, category, content)
	} else if level != DEBUG {
		fmt.Printf("[%s][%s][%s] %s\n", entry.Timestamp.Format("15:04:05"), level, category, content)
	}
}

func Info(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	log(INFO, category, content, tid, "INTERNAL")
}

func Warn(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	log(WARN, category, content, tid, "INTERNAL")
}

func Error(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	log(ERROR, category, content, tid, "INTERNAL")
}

func Debug(category Category, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	log(DEBUG, category, content, tid, "INTERNAL")
}

func LLMRequest(content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	log(INFO, LLM_REQUEST, content, tid, "OUTBOUND")
}

func LLMResponse(content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	log(INFO, LLM_RESPONSE, content, tid, "INBOUND")
}

func Websocket(level Level, content string, traceID ...string) {
	tid := ""
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	log(level, WEBSOCKET, content, tid, "INTERNAL")
}

// Snapshot 获取当前缓冲区中所有日志的快照
func Snapshot() []LogEntry {
	mu.RLock()
	defer mu.RUnlock()

	var logs []LogEntry
	if buffer == nil {
		return logs
	}

	buffer.Do(func(p interface{}) {
		if p != nil {
			logs = append(logs, p.(LogEntry))
		}
	})
	return logs
}

// Clear 清空日志缓冲区
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	buffer = ring.New(size)
}
