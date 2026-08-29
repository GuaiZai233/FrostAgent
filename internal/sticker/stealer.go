package sticker

import (
	"FrostAgent/internal/logs"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	stealProbability = 0.25
	maxConcurrent    = 3
)

var ErrStealerBusy = errors.New("sticker stealer is busy")

type StealResult struct {
	ID        string
	Duplicate bool
}

type Stealer struct {
	store      *Store
	summarizer *Summarizer
	sem        chan struct{}
}

func NewStealer(store *Store, summarizer *Summarizer) *Stealer {
	return &Stealer{
		store:      store,
		summarizer: summarizer,
		sem:        make(chan struct{}, maxConcurrent),
	}
}

func (s *Stealer) TrySteal(imageURL string) {
	select {
	case s.sem <- struct{}{}:
	default:
		return
	}

	go func() {
		defer func() { <-s.sem }()
		if rand.Float64() > stealProbability {
			return
		}
		if _, err := s.steal(context.Background(), imageURL); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("sticker: steal failed: %v", err))
		}
	}()
}

// Steal deterministically collects one trusted sticker URL. Unlike TrySteal,
// it skips the probability gate but keeps the shared non-blocking concurrency
// limit, size cap, deduplication and summarization pipeline.
func (s *Stealer) Steal(ctx context.Context, imageURL string) (StealResult, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return StealResult{}, ErrStealerBusy
	}
	return s.steal(ctx, imageURL)
}

func (s *Stealer) steal(ctx context.Context, imageURL string) (StealResult, error) {
	if strings.TrimSpace(imageURL) == "" {
		return StealResult{}, errors.New("sticker URL is empty")
	}

	data, err := downloadImageContext(ctx, imageURL)
	if err != nil {
		return StealResult{}, fmt.Errorf("download sticker: %w", err)
	}

	hash := HashBytes(data)

	if s.store.Exists(hash) {
		if err := s.store.IncrementWeight(hash); err != nil {
			return StealResult{}, fmt.Errorf("increment sticker weight: %w", err)
		}
		logs.Debug(logs.SYSTEM, fmt.Sprintf("sticker: duplicate %s, weight incremented", hash[:12]))
		return StealResult{ID: hash, Duplicate: true}, nil
	}

	ext := guessExtension(imageURL, data)
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

const maxImageSize = 10 << 20 // 10 MiB

var httpClient = &http.Client{Timeout: 30 * time.Second}

func downloadImage(url string) ([]byte, error) {
	return downloadImageContext(context.Background(), url)
}

func downloadImageContext(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageSize {
		return nil, fmt.Errorf("image too large (>%d bytes)", maxImageSize)
	}
	return data, nil
}

func guessExtension(url string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(strings.SplitN(url, "?", 2)[0]))
	if ext == ".gif" || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" {
		return ext
	}
	if len(data) >= 3 && data[0] == 'G' && data[1] == 'I' && data[2] == 'F' {
		return ".gif"
	}
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return ".png"
	}
	if len(data) >= 4 && string(data[:4]) == "RIFF" && len(data) >= 12 && string(data[8:12]) == "WEBP" {
		return ".webp"
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return ".jpg"
	}
	return ".jpg"
}
