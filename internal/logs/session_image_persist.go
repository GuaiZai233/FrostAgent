package logs

import (
	"sync"
	"time"
)

const (
	maxSessionPersistQueue = 256
	sessionTouchInterval   = time.Minute
	sessionPersistWarnGap  = time.Minute
	maxSessionTouchEntries = 1024
)

type persistJob struct {
	key   string
	touch bool
}

// sessionPersister moves durable preview-image I/O off the message path. The
// queue holds keys only (bytes are read from the memory cache when a job runs),
// is bounded and de-duplicated, and a full queue drops the job: the image stays
// marked unpersisted and is retried the next time it is stored or fetched.
type sessionPersister struct {
	mu       sync.Mutex
	queue    []persistJob
	queued   map[persistJob]struct{}
	running  bool
	touched  map[string]time.Time
	lastWarn time.Time
}

func (p *sessionPersister) enqueueLocked(job persistJob) bool {
	if _, ok := p.queued[job]; ok {
		return true
	}
	if len(p.queue) >= maxSessionPersistQueue {
		return false
	}
	if p.queued == nil {
		p.queued = make(map[persistJob]struct{})
	}
	p.queued[job] = struct{}{}
	p.queue = append(p.queue, job)
	return true
}

// schedule queues a persist (or LRU touch) and starts a drain goroutine if none runs.
func (s *Store) scheduleSessionJob(job persistJob) {
	p := &s.sessionPersist
	if !job.touch {
		// Set before queuing: a full queue drops the job, and the flag is what a
		// later Put or Fetch of the same image uses to try again.
		s.sessionImages.setUnpersisted(job.key, true)
	}
	p.mu.Lock()
	p.enqueueLocked(job)
	start := !p.running && len(p.queue) > 0
	if start {
		p.running = true
	}
	p.mu.Unlock()
	if start {
		go s.drainSessionJobs()
	}
}

// retrySessionPersist requeues an image that is cached in memory but whose
// durable write has not succeeded yet.
func (s *Store) retrySessionPersist(key string) {
	if image, ok := s.sessionImages.get(key); ok && image.unpersisted {
		s.scheduleSessionJob(persistJob{key: key})
	}
}

// touchSessionImage records a real view of a cached image in the durable LRU,
// at most once per interval per key.
func (s *Store) touchSessionImage(key string) {
	if s.sessionImageBackend() == nil {
		return
	}
	p := &s.sessionPersist
	now := time.Now()
	p.mu.Lock()
	if last, ok := p.touched[key]; ok && now.Sub(last) < sessionTouchInterval {
		p.mu.Unlock()
		return
	}
	if p.touched == nil || len(p.touched) >= maxSessionTouchEntries {
		p.touched = make(map[string]time.Time)
	}
	p.touched[key] = now
	p.mu.Unlock()
	s.scheduleSessionJob(persistJob{key: key, touch: true})
}

func (s *Store) drainSessionJobs() {
	p := &s.sessionPersist
	for {
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.running = false
			p.mu.Unlock()
			return
		}
		job := p.queue[0]
		p.queue = p.queue[1:]
		delete(p.queued, job)
		p.mu.Unlock()
		s.runSessionJob(job)
	}
}

func (s *Store) runSessionJob(job persistJob) {
	backend := s.sessionImageBackend()
	if backend == nil {
		return
	}
	p := &s.sessionPersist
	if job.touch {
		backend.TouchSessionImage(job.key)
		return
	}
	image, ok := s.sessionImages.get(job.key)
	if !ok {
		// Evicted from memory before it could be written; its flag went with it.
		return
	}
	err := backend.PutSessionImage(job.key, image.contentType, image.data)
	if err == nil {
		s.sessionImages.setUnpersisted(job.key, false)
		return
	}
	p.mu.Lock()
	warn := time.Since(p.lastWarn) >= sessionPersistWarnGap
	if warn {
		p.lastWarn = time.Now()
	}
	p.mu.Unlock()
	if warn {
		s.Warn(SYSTEM, "会话图片预览持久化失败，将在下次出现时重试（预览仍可在本次运行期间使用）")
	}
}
