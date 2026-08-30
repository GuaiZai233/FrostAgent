package sticker

import (
	"FrostAgent/internal/logs"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

const (
	stealProbability         = 0.25
	maxConcurrent            = 3
	maxObservedStickers      = 256
	maxObservedStickerBytes  = 128 << 20
	observedStickerRetention = 24 * time.Hour
)

var (
	ErrStealerBusy       = errors.New("sticker stealer is busy")
	ErrStickerNotInScope = errors.New("sticker is not available in the current session context")
)

type StealResult struct {
	ID        string
	Duplicate bool
}

// ImageLoader resolves one adapter-trusted image source into bytes. The model
// never supplies this function or the source behind it.
type ImageLoader func(context.Context) ([]byte, error)

type observedSticker struct {
	sessionID  string
	messageID  string
	index      int
	observedAt time.Time
	ready      chan struct{}
	data       []byte
	err        error
}

type Stealer struct {
	store      *Store
	summarizer *Summarizer
	sem        chan struct{}
	loadSem    chan struct{}

	observedMu sync.Mutex
	observed   []*observedSticker
}

func NewStealer(store *Store, summarizer *Summarizer) *Stealer {
	return &Stealer{
		store:      store,
		summarizer: summarizer,
		sem:        make(chan struct{}, maxConcurrent),
		loadSem:    make(chan struct{}, maxConcurrent),
	}
}

// Observe registers one sticker from a trusted platform event. Loading occurs
// asynchronously, while a tool call targeting the same message waits for the
// pending bytes instead of being restricted to the triggering message.
func (s *Stealer) Observe(
	sessionID string,
	messageID string,
	index int,
	loader ImageLoader,
	autoCollect bool,
) {
	if s == nil || sessionID == "" || messageID == "" || index < 0 || loader == nil {
		return
	}

	now := time.Now()
	s.observedMu.Lock()
	s.pruneObservedLocked(now)
	for i, existing := range s.observed {
		if existing.sessionID == sessionID && existing.messageID == messageID && existing.index == index {
			select {
			case <-existing.ready:
				if existing.err != nil {
					s.observed = append(s.observed[:i], s.observed[i+1:]...)
					break
				}
				s.observedMu.Unlock()
				return
			default:
				s.observedMu.Unlock()
				return
			}
			break
		}
	}
	entry := &observedSticker{
		sessionID:  sessionID,
		messageID:  messageID,
		index:      index,
		observedAt: now,
		ready:      make(chan struct{}),
	}
	s.observed = append(s.observed, entry)
	s.pruneObservedLocked(now)
	s.observedMu.Unlock()

	go s.loadObserved(entry, loader, autoCollect)
}

func (s *Stealer) loadObserved(entry *observedSticker, loader ImageLoader, autoCollect bool) {
	s.loadSem <- struct{}{}
	data, err := loader(context.Background())
	<-s.loadSem
	if err == nil {
		err = validateImageData(data)
	}

	s.observedMu.Lock()
	if err == nil {
		entry.data = append([]byte(nil), data...)
	} else {
		entry.err = err
	}
	close(entry.ready)
	s.pruneObservedLocked(time.Now())
	s.observedMu.Unlock()

	if err != nil {
		logs.Warn(logs.SYSTEM, fmt.Sprintf(
			"sticker: failed to cache session=%s message=%s index=%d: %v",
			entry.sessionID,
			entry.messageID,
			entry.index,
			err,
		))
		return
	}
	if autoCollect {
		s.TrySteal(data)
	}
}

// StealObserved collects a trusted sticker previously observed in the same
// session. When messageID is empty, the latest sticker-bearing message is used.
func (s *Stealer) StealObserved(
	ctx context.Context,
	sessionID string,
	messageID string,
	stickerIndex int,
) (StealResult, string, error) {
	if s == nil || sessionID == "" || stickerIndex < 0 {
		return StealResult{}, "", ErrStickerNotInScope
	}

	entry, resolvedMessageID := s.findObserved(sessionID, messageID, stickerIndex)
	if entry == nil {
		return StealResult{}, resolvedMessageID, ErrStickerNotInScope
	}

	select {
	case <-entry.ready:
	case <-ctx.Done():
		return StealResult{}, resolvedMessageID, ctx.Err()
	}

	s.observedMu.Lock()
	data := append([]byte(nil), entry.data...)
	loadErr := entry.err
	s.observedMu.Unlock()
	if loadErr != nil {
		return StealResult{}, resolvedMessageID, fmt.Errorf("load observed sticker: %w", loadErr)
	}

	result, err := s.Steal(ctx, data)
	return result, resolvedMessageID, err
}

func (s *Stealer) findObserved(sessionID, messageID string, stickerIndex int) (*observedSticker, string) {
	s.observedMu.Lock()
	defer s.observedMu.Unlock()
	s.pruneObservedLocked(time.Now())

	resolvedMessageID := messageID
	if resolvedMessageID == "" {
		for i := len(s.observed) - 1; i >= 0; i-- {
			if s.observed[i].sessionID == sessionID {
				resolvedMessageID = s.observed[i].messageID
				break
			}
		}
	}
	for i := len(s.observed) - 1; i >= 0; i-- {
		entry := s.observed[i]
		if entry.sessionID == sessionID && entry.messageID == resolvedMessageID && entry.index == stickerIndex {
			return entry, resolvedMessageID
		}
	}
	return nil, resolvedMessageID
}

func (s *Stealer) pruneObservedLocked(now time.Time) {
	cutoff := now.Add(-observedStickerRetention)
	kept := s.observed[:0]
	totalBytes := 0
	for _, entry := range s.observed {
		if entry.observedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, entry)
		totalBytes += len(entry.data)
	}
	s.observed = kept
	for len(s.observed) > maxObservedStickers || totalBytes > maxObservedStickerBytes {
		totalBytes -= len(s.observed[0].data)
		s.observed = s.observed[1:]
	}
}

// TrySteal applies the normal probability gate to already-loaded image bytes.
func (s *Stealer) TrySteal(data []byte) {
	if rand.Float64() > stealProbability {
		return
	}
	select {
	case s.sem <- struct{}{}:
	default:
		return
	}

	data = append([]byte(nil), data...)
	go func() {
		defer func() { <-s.sem }()
		if _, err := s.collect(context.Background(), data); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("sticker: steal failed: %v", err))
		}
	}()
}

// Steal deterministically collects trusted image bytes. It skips the
// probability gate but keeps the shared non-blocking concurrency limit,
// size cap, deduplication and summarization pipeline.
func (s *Stealer) Steal(ctx context.Context, data []byte) (StealResult, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return StealResult{}, ErrStealerBusy
	}
	return s.collect(ctx, data)
}

func (s *Stealer) collect(ctx context.Context, data []byte) (StealResult, error) {
	if err := validateImageData(data); err != nil {
		return StealResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return StealResult{}, err
	}
	if s.store == nil {
		return StealResult{}, errors.New("sticker store is unavailable")
	}

	hash := HashBytes(data)

	if s.store.Exists(hash) {
		if err := s.store.IncrementWeight(hash); err != nil {
			return StealResult{}, fmt.Errorf("increment sticker weight: %w", err)
		}
		logs.Debug(logs.SYSTEM, fmt.Sprintf("sticker: duplicate %s, weight incremented", hash[:12]))
		return StealResult{ID: hash, Duplicate: true}, nil
	}

	ext := guessExtension(data)
	fileName := hash + ext

	if err := s.store.Add(hash, fileName, data); err != nil {
		// An automatic steal and an explicit admin steal can race on the same
		// incoming sticker. Treat a concurrently-created entry as a duplicate.
		if s.store.Exists(hash) {
			if incrementErr := s.store.IncrementWeight(hash); incrementErr != nil {
				return StealResult{}, fmt.Errorf("increment concurrently added sticker weight: %w", incrementErr)
			}
			return StealResult{ID: hash, Duplicate: true}, nil
		}
		return StealResult{}, fmt.Errorf("add sticker: %w", err)
	}

	logs.Info(logs.SYSTEM, fmt.Sprintf("sticker: stolen %s%s", hash[:12], ext))

	if s.summarizer != nil {
		s.summarizer.Enqueue(hash)
	}
	return StealResult{ID: hash}, nil
}

func validateImageData(data []byte) error {
	if len(data) == 0 {
		return errors.New("sticker image is empty")
	}
	if len(data) > maxImageSize {
		return fmt.Errorf("image too large (>%d bytes)", maxImageSize)
	}
	return nil
}

func guessExtension(data []byte) string {
	if len(data) >= 3 && data[0] == 'G' && data[1] == 'I' && data[2] == 'F' {
		return ".gif"
	}
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return ".png"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return ".webp"
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return ".jpg"
	}
	return ".jpg"
}
