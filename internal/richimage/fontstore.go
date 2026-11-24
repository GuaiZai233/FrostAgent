package richimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// remoteFont is a font file fetched from a fixed upstream GitHub URL and
// verified against a pinned SHA-256 before it is ever parsed. The URLs are
// constants: neither the model nor configuration can point downloads elsewhere.
type remoteFont struct {
	// name is the cache file name; it embeds the pinned upstream version.
	name   string
	url    string
	sha256 string
	size   int64
}

// Body font (Chinese and English): LXGW ZhenKai GB v0.825, SIL OFL 1.1.
var lxgwZhenKai = remoteFont{
	name:   "LXGWZhenKaiGB-Regular-v0.825.ttf",
	url:    "https://github.com/lxgw/LxgwZhenKai/releases/download/v0.825/LXGWZhenKaiGB-Regular.ttf",
	sha256: "40876902a7ce25268ab710ad8fe6e2b63bc002aa4b68d22fd45fc1243726ced5",
	size:   17545474,
}

// Math font: STIX Two Math v2.13 (pinned to its tag commit), SIL OFL 1.1.
var stixTwoMath = remoteFont{
	name:   "STIXTwoMath-Regular-v2.13.otf",
	url:    "https://raw.githubusercontent.com/stipub/stixfonts/37308d44583ef882dc8b544aaa2e80ac8c406ebf/fonts/static_otf/STIXTwoMath-Regular.otf",
	sha256: "f2076b9f1676438439dd41e23676f5ab99056e83d6b8f8c27841591ef2ccfa72",
	size:   838652,
}

const (
	// FontDirEnvKey names the directory holding (or receiving) the pinned fonts.
	FontDirEnvKey = "FROSTAGENT_RICH_IMAGE_FONT_DIR"

	fontDownloadTimeout = 3 * time.Minute
	// A failed download is not retried for a while so every render does not
	// block on an unreachable GitHub.
	fontRetryCooldown = 5 * time.Minute
)

var (
	// fontDownloadEnabled lets tests stay offline.
	fontDownloadEnabled = true
	fontHTTPClient      = &http.Client{}

	// fetchMu guards only these maps and is never held across I/O.
	fetchMu   sync.Mutex
	fetchFail = map[string]fetchFailure{}
	flights   = map[string]*flight{}
)

type fetchFailure struct {
	at  time.Time
	err error
}

// fontDir resolves the cache directory.
func fontDir(override string) string {
	if override != "" {
		return override
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "frostagent", "fonts")
	}
	return filepath.Join(os.TempDir(), "frostagent-fonts")
}

func verifyFont(data []byte, rf remoteFont) error {
	if int64(len(data)) != rf.size {
		return fmt.Errorf("大小 %d 与预期 %d 不符", len(data), rf.size)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != rf.sha256 {
		return fmt.Errorf("sha256 %s 与预期 %s 不符", got, rf.sha256)
	}
	return nil
}

// readCachedFont returns the cached file only when it matches the pin.
func readCachedFont(path string, rf remoteFont) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != rf.size {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || verifyFont(data, rf) != nil {
		return nil, false
	}
	return data, true
}

// flight is one shared in-progress download. The download runs detached from
// any single caller, so a canceled caller never aborts it for the others.
type flight struct {
	done chan struct{}
	data []byte
	err  error
}

// ensureFont returns the verified bytes of rf, downloading them into dir on
// first use. Corrupt or tampered cache files are replaced, never used.
// Waiting is always cancellable and no lock is held across network I/O.
func ensureFont(ctx context.Context, rf remoteFont, dir string) ([]byte, error) {
	path := filepath.Join(dir, rf.name)
	if data, ok := readCachedFont(path, rf); ok {
		return data, nil
	}
	if !fontDownloadEnabled {
		return nil, fmt.Errorf("字体 %s 不在缓存目录 %s 中，且已禁用下载", rf.name, dir)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := path
	fetchMu.Lock()
	if f, ok := fetchFail[rf.name]; ok && time.Since(f.at) < fontRetryCooldown {
		fetchMu.Unlock()
		return nil, fmt.Errorf("字体 %s 最近下载失败，稍后重试: %w", rf.name, f.err)
	}
	fl, running := flights[key]
	if !running {
		fl = &flight{done: make(chan struct{})}
		flights[key] = fl
		go runFlight(key, fl, rf, dir)
	}
	fetchMu.Unlock()
	select {
	case <-fl.done:
		return fl.data, fl.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func runFlight(key string, fl *flight, rf remoteFont, dir string) {
	data, err := downloadFont(context.Background(), rf)
	if err == nil {
		err = storeFont(dir, rf, data)
	}
	fetchMu.Lock()
	if err != nil {
		err = fmt.Errorf("下载字体 %s 失败: %w", rf.name, err)
		fetchFail[rf.name] = fetchFailure{at: time.Now(), err: err}
	} else {
		delete(fetchFail, rf.name)
		fl.data = data
	}
	fl.err = err
	delete(flights, key)
	fetchMu.Unlock()
	close(fl.done)
}

func downloadFont(ctx context.Context, rf remoteFont) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fontDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rf.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "frostagent-font-fetch")
	resp, err := fontHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Read one byte past the pin so an oversized body is detected, not buffered.
	data, err := io.ReadAll(io.LimitReader(resp.Body, rf.size+1))
	if err != nil {
		return nil, err
	}
	if err := verifyFont(data, rf); err != nil {
		return nil, err
	}
	return data, nil
}

// storeFont writes atomically so a crash never leaves a partial cache file.
func storeFont(dir string, rf remoteFont, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, rf.name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, rf.name)); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
