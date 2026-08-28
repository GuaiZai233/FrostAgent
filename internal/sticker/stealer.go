package sticker

import (
	"FrostAgent/internal/logs"
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
		s.doSteal(imageURL)
	}()
}

func (s *Stealer) doSteal(imageURL string) {
	if rand.Float64() > stealProbability {
		return
	}

	data, err := downloadImage(imageURL)
	if err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("sticker: download failed: %v", err))
		return
	}

	hash := HashBytes(data)

	if s.store.Exists(hash) {
		if err := s.store.IncrementWeight(hash); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("sticker: increment weight failed: %v", err))
		}
		logs.Debug(logs.SYSTEM, fmt.Sprintf("sticker: duplicate %s, weight incremented", hash[:12]))
		return
	}

	ext := guessExtension(imageURL, data)
	fileName := hash + ext

	if err := s.store.Add(hash, fileName, data); err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("sticker: add failed: %v", err))
		return
	}

	logs.Info(logs.SYSTEM, fmt.Sprintf("sticker: stolen %s%s", hash[:12], ext))

	if s.summarizer != nil {
		s.summarizer.Enqueue(hash)
	}
}

const maxImageSize = 10 << 20 // 10 MiB

var httpClient = &http.Client{Timeout: 30 * time.Second}

func downloadImage(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
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
