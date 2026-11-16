package chathistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type Recorder struct {
	Store    *Store
	getenv   func(string) string
	report   func(string)
	write    func(context.Context, Record) error
	queue    chan Record
	mu       sync.Mutex
	stopped  bool
	warnings map[string]time.Time
	pending  atomic.Int64
}

func NewRecorder(store *Store, getenv func(string) string, report func(string)) *Recorder {
	return &Recorder{Store: store, getenv: getenv, report: report, write: store.Save,
		queue: make(chan Record, 64), warnings: map[string]time.Time{}}
}

// Record performs no disk or network I/O. The collection policy is checked
// before content enters the bounded queue, including messages with no reply.
func (r *Recorder) Record(record Record) bool {
	if r == nil {
		return false
	}
	record.Scope = record.Scope.Canonical()
	if !RecordingAllowed(r.getenv, record.Scope) {
		return false
	}
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}
	if record.ID == "" {
		if record.MessageID == "" {
			record.ID = uuid.NewString()
		} else {
			identity, _ := json.Marshal([]string{record.Platform, record.MessageType, record.TargetID, record.Role, record.MessageID})
			hash := sha256.Sum256(identity)
			record.ID = hex.EncodeToString(hash[:])
		}
	}
	if record.Validate() != nil {
		r.warn("聊天档案记录无效或超过 1 MiB，已跳过；聊天继续。")
		return false
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return false
	}
	r.pending.Add(1)
	select {
	case r.queue <- record:
		r.mu.Unlock()
		return true
	default:
		r.pending.Add(-1)
		r.mu.Unlock()
		r.warn("聊天档案队列已满，部分原文未保存；聊天继续。")
		return false
	}
}

func (r *Recorder) warn(message string) {
	if r.report == nil {
		return
	}
	r.mu.Lock()
	now := time.Now()
	if now.Sub(r.warnings[message]) < time.Minute {
		r.mu.Unlock()
		return
	}
	r.warnings[message] = now
	r.mu.Unlock()
	r.report(message)
}

func (r *Recorder) persist(ctx context.Context, record Record) bool {
	for attempt := range 3 {
		if ctx.Err() != nil {
			return false
		}
		writeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := r.write(writeCtx, record)
		cancel()
		if err == nil {
			return true
		}
		r.warn("聊天档案写入失败，正在有限重试；聊天继续。")
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
	}
	return false
}

// Run is owned by one runtime activation. Shutdown gets a bounded drain window;
// a power loss can still discard any records not yet committed to the database.
func (r *Recorder) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			r.drain(nil)
			return
		case record := <-r.queue:
			ok := r.persist(ctx, record)
			if !ok && ctx.Err() != nil {
				r.drain(&record)
				return
			}
			if !ok {
				r.warn("聊天档案重试已耗尽，部分原文未保存；聊天继续。")
			}
			r.pending.Add(-1)
		}
	}
}

func (r *Recorder) drain(first *Record) {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	finish := func(record Record) {
		if !r.persist(ctx, record) {
			r.warn("停止实例时聊天档案未能全部保存。")
		}
		r.pending.Add(-1)
	}
	if first != nil {
		finish(*first)
	}
	for {
		select {
		case record := <-r.queue:
			finish(record)
		default:
			return
		}
	}
}

func (r *Recorder) Flush(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for r.pending.Load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}
