package sticker

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
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
	scope      string
	observedAt time.Time
}

type Stealer struct {
	*runtimescope.Scope
	store      *Store
	summarizer *Summarizer
	sem        chan struct{}
	random     func() float64

	observedMu sync.Mutex
	observed   []*observedSticker
}

func NewStealer(store *Store, summarizer *Summarizer) *Stealer {
	var scope *runtimescope.Scope
	if summarizer != nil {
		scope = summarizer.Scope
	}
	return &Stealer{
		Scope:      scope,
		store:      store,
		summarizer: summarizer,
		sem:        make(chan struct{}, maxConcurrent),
		random:     rand.Float64,
	}
}

// Observe registers lightweight metadata for one sticker from a trusted
// platform event. Automatic collection is admitted before any goroutine or
// image load is started; explicit collection resolves bytes on demand later.
func (s *Stealer) Observe(
	sessionID string,
	messageID string,
	index int,
	loader ImageLoader,
	autoCollect bool,
) {
	s.ObserveScoped(sessionID, messageID, index, "", loader, autoCollect)
}

// ObserveScoped registers lightweight metadata for one sticker scoped to a
// specific connection or upstream generation.
func (s *Stealer) ObserveScoped(
	sessionID string,
	messageID string,
	index int,
	scope string,
	loader ImageLoader,
	autoCollect bool,
) {
	if s == nil || sessionID == "" || messageID == "" || index < 0 || (autoCollect && loader == nil) {
		return
	}

	now := time.Now()
	s.observedMu.Lock()
	s.pruneObservedLocked(now)
	for _, existing := range s.observed {
		if existing.sessionID == sessionID && existing.messageID == messageID && existing.index == index && existing.scope == scope {
			s.observedMu.Unlock()
			return
		}
	}
	s.observed = append(s.observed, &observedSticker{
		sessionID:  sessionID,
		messageID:  messageID,
		index:      index,
		scope:      scope,
		observedAt: now,
	})
	s.pruneObservedLocked(now)
	s.observedMu.Unlock()

	if !autoCollect || s.random() > stealProbability {
		return
	}
	select {
	case s.sem <- struct{}{}:
	default:
		return
	}

	if !s.Go(func() {
		defer func() { <-s.sem }()
		data, err := loader(s.Context())
		if err == nil {
			_, err = s.collect(s.Context(), data)
		}
		if err != nil {
			s.Log().Error(logs.SYSTEM, fmt.Sprintf(
				"sticker: automatic steal failed for session=%s message=%s index=%d: %v",
				sessionID,
				messageID,
				index,
				err,
			))
		}
	}) {
		<-s.sem
	}
}

// StealObserved collects a trusted sticker previously observed in the same
// session. Bytes are resolved on demand by the current platform adapter only
// after the message and index pass the session-scope check.
func (s *Stealer) StealObserved(
	ctx context.Context,
	sessionID string,
	messageID string,
	stickerIndex int,
	loader func(context.Context, string, int) ([]byte, error),
) (StealResult, string, error) {
	return s.StealObservedScoped(ctx, sessionID, messageID, stickerIndex, "", loader)
}

// StealObservedScoped collects a trusted sticker observed in the same session
// and matching the caller's connection/upstream scope.
func (s *Stealer) StealObservedScoped(
	ctx context.Context,
	sessionID string,
	messageID string,
	stickerIndex int,
	scope string,
	loader func(context.Context, string, int) ([]byte, error),
) (StealResult, string, error) {
	if s == nil || sessionID == "" || stickerIndex < 0 {
		return StealResult{}, "", ErrStickerNotInScope
	}

	found, resolvedMessageID := s.findObserved(sessionID, messageID, stickerIndex, scope)
	if !found {
		return StealResult{}, resolvedMessageID, ErrStickerNotInScope
	}
	if loader == nil {
		return StealResult{}, resolvedMessageID, errors.New("sticker loader is unavailable")
	}

	data, err := loader(ctx, resolvedMessageID, stickerIndex)
	if err != nil {
		return StealResult{}, resolvedMessageID, fmt.Errorf("load observed sticker: %w", err)
	}

	result, err := s.Steal(ctx, data)
	return result, resolvedMessageID, err
}

func (s *Stealer) findObserved(sessionID, messageID string, stickerIndex int, scope string) (bool, string) {
	s.observedMu.Lock()
	defer s.observedMu.Unlock()
	s.pruneObservedLocked(time.Now())

	resolvedMessageID := messageID
	if resolvedMessageID == "" {
		for i := len(s.observed) - 1; i >= 0; i-- {
			entry := s.observed[i]
			if entry.sessionID == sessionID && (scope == "" || entry.scope == "" || entry.scope == scope) {
				resolvedMessageID = entry.messageID
				break
			}
		}
	}
	for i := len(s.observed) - 1; i >= 0; i-- {
		entry := s.observed[i]
		if entry.sessionID == sessionID && entry.messageID == resolvedMessageID && entry.index == stickerIndex {
			if scope != "" && entry.scope != "" && entry.scope != scope {
				// Reject cross-connection/upstream message IDs to prevent stale or colliding handle lookups
				return false, resolvedMessageID
			}
			return true, resolvedMessageID
		}
	}
	return false, resolvedMessageID
}

// ClearObservedScope removes all observed stickers matching scope (or all if scope is empty).
// Used when an upstream connection closes or identity changes to prevent stale message handles.
func (s *Stealer) ClearObservedScope(scope string) {
	if s == nil {
		return
	}
	s.observedMu.Lock()
	defer s.observedMu.Unlock()
	if scope == "" {
		s.observed = nil
		return
	}
	kept := s.observed[:0]
	for _, entry := range s.observed {
		if entry.scope != scope {
			kept = append(kept, entry)
		}
	}
	s.observed = kept
}

func (s *Stealer) pruneObservedLocked(now time.Time) {
	cutoff := now.Add(-observedStickerRetention)
	kept := s.observed[:0]
	for _, entry := range s.observed {
		if entry.observedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, entry)
	}
	s.observed = kept
	for len(s.observed) > maxObservedStickers {
		s.observed = s.observed[1:]
	}
}

// TrySteal applies the normal probability gate to already-loaded image bytes.
func (s *Stealer) TrySteal(data []byte) {
	if s.random() > stealProbability {
		return
	}
	select {
	case s.sem <- struct{}{}:
	default:
		return
	}

	data = append([]byte(nil), data...)
	if !s.Go(func() {
		defer func() { <-s.sem }()
		if _, err := s.collect(s.Context(), data); err != nil {
			s.Log().Error(logs.SYSTEM, fmt.Sprintf("sticker: steal failed: %v", err))
		}
	}) {
		<-s.sem
	}
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
		s.Log().Debug(logs.SYSTEM, fmt.Sprintf("sticker: duplicate %s, weight incremented", hash[:12]))
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

	s.Log().Info(logs.SYSTEM, fmt.Sprintf("sticker: stolen %s%s", hash[:12], ext))

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
