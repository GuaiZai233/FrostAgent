package richimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testRemote(body []byte) remoteFont {
	sum := sha256.Sum256(body)
	return remoteFont{name: "test-font.ttf", sha256: hex.EncodeToString(sum[:]), size: int64(len(body))}
}

func withDownloads(t *testing.T) {
	t.Helper()
	old := fontDownloadEnabled
	fontDownloadEnabled = true
	fetchMu.Lock()
	fetchFail = map[string]fetchFailure{}
	fetchMu.Unlock()
	t.Cleanup(func() { fontDownloadEnabled = old })
}

func serve(t *testing.T, body []byte, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestPinnedFontsAreFixedGitHubSources(t *testing.T) {
	for _, rf := range []remoteFont{lxgwZhenKai, stixTwoMath} {
		if len(rf.sha256) != 64 || rf.size <= 0 {
			t.Errorf("%s: sha256/size not pinned", rf.name)
		}
		if _, err := hex.DecodeString(rf.sha256); err != nil {
			t.Errorf("%s: sha256 is not hex", rf.name)
		}
	}
	if !strings.HasPrefix(lxgwZhenKai.url, "https://github.com/lxgw/LxgwZhenKai/releases/download/v0.825/") {
		t.Errorf("unexpected LXGW url %s", lxgwZhenKai.url)
	}
	if !strings.HasPrefix(stixTwoMath.url, "https://raw.githubusercontent.com/stipub/stixfonts/") ||
		strings.Contains(stixTwoMath.url, "/master/") || strings.Contains(stixTwoMath.url, "/main/") {
		t.Errorf("STIX url must be pinned to a commit: %s", stixTwoMath.url)
	}
}

func TestEnsureFontDownloadsVerifiesAndCaches(t *testing.T) {
	withDownloads(t)
	body := []byte("pretend font bytes")
	srv, hits := serve(t, body, http.StatusOK)
	rf := testRemote(body)
	rf.url = srv.URL
	dir := t.TempDir()
	got, err := ensureFont(context.Background(), rf, dir)
	if err != nil || string(got) != string(body) {
		t.Fatalf("download: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, rf.name)); err != nil {
		t.Fatalf("not cached: %v", err)
	}
	// The second call is served from the verified cache, with no network.
	srv.Close()
	if _, err := ensureFont(context.Background(), rf, dir); err != nil {
		t.Fatalf("cache miss after download: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestEnsureFontRejectsBadDownloads(t *testing.T) {
	good := []byte("0123456789")
	cases := map[string]struct {
		body   []byte
		status int
	}{
		"wrong content, right size": {[]byte("9876543210"), http.StatusOK},
		"too short":                 {[]byte("0123"), http.StatusOK},
		"oversized":                 {[]byte(strings.Repeat("0123456789", 100)), http.StatusOK},
		"not found":                 {[]byte("nope"), http.StatusNotFound},
	}
	for name, tc := range cases {
		withDownloads(t)
		srv, _ := serve(t, tc.body, tc.status)
		rf := testRemote(good)
		rf.url = srv.URL
		dir := t.TempDir()
		if _, err := ensureFont(context.Background(), rf, dir); err == nil {
			t.Errorf("%s: must fail", name)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s: failed download left files: %v", name, entries)
		}
	}
}

func TestEnsureFontReplacesCorruptCacheAndNeverTrustsIt(t *testing.T) {
	withDownloads(t)
	body := []byte("real font")
	rf := testRemote(body)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, rf.name), []byte("tampered!"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Offline: a tampered cache file is rejected rather than used.
	fontDownloadEnabled = false
	if _, err := ensureFont(context.Background(), rf, dir); err == nil {
		t.Fatal("tampered cache must not be used")
	}
	// Online: it is replaced by a verified download.
	fontDownloadEnabled = true
	srv, _ := serve(t, body, http.StatusOK)
	rf.url = srv.URL
	if got, err := ensureFont(context.Background(), rf, dir); err != nil || string(got) != string(body) {
		t.Fatalf("replace: %v", err)
	}
	if onDisk, _ := os.ReadFile(filepath.Join(dir, rf.name)); string(onDisk) != string(body) {
		t.Fatal("corrupt cache file was not replaced")
	}
}

func TestEnsureFontFailureCooldownAndCancel(t *testing.T) {
	withDownloads(t)
	srv, hits := serve(t, nil, http.StatusInternalServerError)
	rf := testRemote([]byte("font"))
	rf.url = srv.URL
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := ensureFont(context.Background(), rf, dir); err == nil {
			t.Fatal("expected failure")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("cooldown must stop retries: %d hits", hits.Load())
	}
	// A canceled caller says nothing about the network: no cooldown is recorded.
	withDownloads(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ensureFont(ctx, rf, dir); err == nil {
		t.Fatal("canceled download must fail")
	}
	fetchMu.Lock()
	_, cooled := fetchFail[rf.name]
	fetchMu.Unlock()
	if cooled {
		t.Fatal("cancellation must not start the retry cooldown")
	}
}

func TestLoadFontsReportsMissingPinnedFonts(t *testing.T) {
	old := fontDownloadEnabled
	fontDownloadEnabled = false
	t.Cleanup(func() { fontDownloadEnabled = old })
	set, err := loadFonts(context.Background(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if set.fetchErr == nil {
		t.Fatal("missing pinned fonts must be reported")
	}
	if set.text == nil || set.goReg == nil {
		t.Fatal("must still fall back to a usable font")
	}
	if !set.cjk {
		if err := newFaces(set).checkText("中"); !errors.Is(err, ErrNoCJKFont) {
			t.Fatalf("CJK text without a font must fail with ErrNoCJKFont: %v", err)
		}
	}
}

func TestMathAlnum(t *testing.T) {
	cases := []struct {
		in   rune
		bold bool
		want rune
	}{
		{'a', false, 0x1D44E}, {'z', false, 0x1D467}, {'A', false, 0x1D434}, {'h', false, 0x210E},
		{'a', true, 0x1D41A}, {'A', true, 0x1D400}, {'0', true, 0x1D7CE},
		{0x3B1, false, 0x1D6FC}, {0x3C9, false, 0x1D714},
		{'0', false, '0'}, {'+', false, '+'}, {0x4E2D, false, 0x4E2D},
	}
	for _, tc := range cases {
		if got := mathAlnum(tc.in, tc.bold); got != tc.want {
			t.Errorf("mathAlnum(%U, bold=%v) = %U, want %U", tc.in, tc.bold, got, tc.want)
		}
	}
}

// A canceled waiter must return promptly while another caller's download is
// still in flight, and must not disturb that download.
func TestEnsureFontCanceledWaiterDoesNotBlock(t *testing.T) {
	withDownloads(t)
	body := []byte("slow font")
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	rf := testRemote(body)
	rf.url = srv.URL
	dir := t.TempDir()

	first := make(chan error, 1)
	go func() { _, err := ensureFont(context.Background(), rf, dir); first <- err }()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() { _, err := ensureFont(ctx, rf, dir); second <- err }()
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter stayed blocked behind the download")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first download failed: %v", err)
	}
	if len(started) != 0 {
		t.Fatal("waiters must share one download")
	}
}

// RenderContext releases its render slot when canceled during font loading.
func TestRenderContextCanceledDuringFontLoad(t *testing.T) {
	withDownloads(t)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer close(release)
	oldBody, oldMath := lxgwZhenKai, stixTwoMath
	lxgwZhenKai.url, stixTwoMath.url = srv.URL, srv.URL
	defer func() { lxgwZhenKai, stixTwoMath = oldBody, oldMath }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := RenderContext(ctx, Request{Format: FormatMarkdown, Content: "x", FontDir: t.TempDir()})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("render stayed blocked on font loading")
	}
	if len(renderSlots) != 0 {
		t.Fatalf("render slot leaked: %d in use", len(renderSlots))
	}
}
